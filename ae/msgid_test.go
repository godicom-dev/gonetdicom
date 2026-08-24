package ae

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

const msgIDFindModel = StudyRootQueryRetrieveInformationModelFind

// Message IDs identify an outstanding request; two requests sharing one make the
// responses to them indistinguishable. The counter was a plain uint16, so
// concurrent requests lost increments — and the race detector flagged every
// association that CCancel was used with, which the API invites.
func TestNextMessageIDStaysUnique(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 8
		perG       = 1000
	)
	a := &Association{}
	var (
		mu  sync.Mutex
		got = make(map[uint16]int, goroutines*perG)
		wg  sync.WaitGroup
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids := make([]uint16, 0, perG)
			for i := 0; i < perG; i++ {
				ids = append(ids, a.nextMessageID())
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range ids {
				got[id]++
			}
		}()
	}
	wg.Wait()

	if len(got) != goroutines*perG {
		t.Errorf("%d distinct Message IDs from %d requests", len(got), goroutines*perG)
	}
	for id, n := range got {
		if n > 1 {
			t.Errorf("Message ID %d handed out %d times", id, n)
		}
	}
}

// Zero is skipped on wrap, as it was before the counter became atomic.
func TestNextMessageIDSkipsZeroOnWrap(t *testing.T) {
	t.Parallel()

	a := &Association{}
	if got := a.nextMessageID(); got != 1 {
		t.Errorf("first Message ID = %d, want 1", got)
	}
	a.nextID.Store(65534)
	if got := a.nextMessageID(); got != 65535 {
		t.Errorf("Message ID = %d, want 65535", got)
	}
	if got := a.nextMessageID(); got != 1 {
		t.Errorf("Message ID after wrap = %d, want 1", got)
	}
}

// Abort exists to reach an operation that is already blocked, so it runs on
// another goroutine by definition. It used to clear the conn field on its way
// out, and the goroutine unwinding out of readPDU then cleared its read deadline
// through a nil net.Conn — a panic that took the whole process with it, in
// exactly the situation Abort is for.
func TestAbortDuringBlockedOperation(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 64)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, ln, ServerConfig{
			AETitle:                  "ABORTSCP",
			AcceptedAbstractSyntaxes: []string{msgIDFindModel},
			OnCFind: func(hctx context.Context, _ FindRequest) []FindMatch {
				entered <- struct{}{}
				select {
				case <-release:
				case <-hctx.Done():
				}
				return []FindMatch{{Status: dimse.StatusSuccess}}
			},
		})
	}()

	// Repeated: the abort has to lose the race against the blocked reader's
	// unwinding for the old code to survive, and one attempt can get lucky.
	for i := 0; i < 20; i++ {
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
		assoc, err := Dial(dialCtx, Config{
			AETitle: "ABORTSCU",
			PresentationContexts: []PresentationContext{
				{AbstractSyntax: msgIDFindModel, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			},
		}, ln.Addr().String(), "ABORTSCP")
		if err != nil {
			dialCancel()
			t.Fatalf("dial %d: %v", i, err)
		}

		done := make(chan error, 1)
		go func() {
			query := godicom.NewDataset()
			query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
			_, err := assoc.CFind(dialCtx, FindRequest{QueryModel: msgIDFindModel, IdentifierData: query})
			done <- err
		}()

		<-entered // the SCP holds the request and will not answer it
		if err := assoc.Abort(); err != nil {
			t.Errorf("abort %d: %v", i, err)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("iteration %d: C-FIND reported success through an abort", i)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: C-FIND still blocked after Abort", i)
		}
		dialCancel()
	}

	cancel()
	<-errCh
}

// Teardown is idempotent, including the usual defer assoc.Abort() left behind a
// successful Release.
func TestTeardownIsIdempotent(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, ln, ServerConfig{AETitle: "IDEMSCP"})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	assoc, err := Dial(dialCtx, Config{AETitle: "IDEMSCU"}, ln.Addr().String(), "IDEMSCP")
	if err != nil {
		t.Fatal(err)
	}
	if err := assoc.CEcho(dialCtx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Errorf("second release: %v", err)
	}
	if err := assoc.Abort(); err != nil {
		t.Errorf("abort after release: %v", err)
	}
	if err := assoc.Close(); err != nil {
		t.Errorf("close after release: %v", err)
	}

	cancel()
	<-errCh
}
