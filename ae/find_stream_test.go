package ae_test

import (
	"context"
	"errors"
	"iter"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// serveFindSCP starts an SCP with the given C-FIND handler and returns its
// address.
func serveFindSCP(t *testing.T, handler ae.FindHandler) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
	})
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STREAMSCP",
			AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelFind},
			OnCFind:                  handler,
		})
	}()
	return ln.Addr().String()
}

// matchDS is a minimal match identifier carrying an id.
func matchDS(id string) *godicom.Dataset {
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, id))
	return ds
}

// nextStatus reads one C-FIND-RSP and returns its status.
func nextStatus(t *testing.T, scu *rawSCU, timeout time.Duration) uint16 {
	t.Helper()
	rsp, err := scu.nextCommand(timeout)
	if err != nil {
		t.Fatalf("read C-FIND-RSP: %v", err)
	}
	st, ok := dimse.PeekStatus(rsp)
	if !ok {
		t.Fatal("C-FIND-RSP carries no status")
	}
	return st
}

// The whole point of the iterator contract: a match is encoded, written, and
// acknowledged by the socket before the handler is asked for the next one. If
// the framework collected the matching first — what the slice contract did —
// the handler below would never be released, because the peer only releases it
// after it has read the first response.
func TestCFindStreamsEachMatchBeforeTheNext(t *testing.T) {
	t.Parallel()

	allowSecond := make(chan struct{})
	addr := serveFindSCP(t, func(context.Context, ae.FindRequest) iter.Seq2[ae.FindMatch, error] {
		return func(yield func(ae.FindMatch, error) bool) {
			if !yield(ae.FindMatch{Status: dimse.StatusPending, Identifier: matchDS("P1")}, nil) {
				return
			}
			select {
			case <-allowSecond:
			case <-time.After(5 * time.Second):
				t.Error("first match was not on the wire before the handler produced the second")
				return
			}
			if !yield(ae.FindMatch{Status: dimse.StatusPending, Identifier: matchDS("P2")}, nil) {
				return
			}
			yield(ae.FindMatch{Status: dimse.StatusSuccess}, nil)
		}
	})

	scu := dialRawSCU(t, addr, "STREAMSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 21)
	scu.send(cmd, ident)

	if st := nextStatus(t, scu, 5*time.Second); !dimse.IsPending(st) {
		t.Fatalf("first response status=0x%04x, want Pending", st)
	}
	close(allowSecond)

	if st := nextStatus(t, scu, 5*time.Second); !dimse.IsPending(st) {
		t.Fatalf("second response status=0x%04x, want Pending", st)
	}
	if st := nextStatus(t, scu, 5*time.Second); st != dimse.StatusSuccess {
		t.Fatalf("final status=0x%04x, want Success", st)
	}
}

// A handler that stops part-way through says so by returning an error; the peer
// gets pynetdicom's 0xC311 and the operation ends there.
func TestCFindHandlerErrorIsUnableToProcess(t *testing.T) {
	t.Parallel()

	addr := serveFindSCP(t, func(context.Context, ae.FindRequest) iter.Seq2[ae.FindMatch, error] {
		return func(yield func(ae.FindMatch, error) bool) {
			if !yield(ae.FindMatch{Status: dimse.StatusPending, Identifier: matchDS("P1")}, nil) {
				return
			}
			yield(ae.FindMatch{}, errors.New("archive went away"))
		}
	})

	scu := dialRawSCU(t, addr, "STREAMSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 22)
	scu.send(cmd, ident)

	if st := nextStatus(t, scu, 5*time.Second); !dimse.IsPending(st) {
		t.Fatalf("first response status=0x%04x, want Pending", st)
	}
	if st := nextStatus(t, scu, 5*time.Second); st != dcmstatus.FindUnableToProcess {
		t.Fatalf("status after handler error=0x%04x, want 0x%04x", st, dcmstatus.FindUnableToProcess)
	}
}

// A match whose Identifier will not encode cannot be sent, so the response that
// would have carried it is the failure instead of a silently empty Pending.
func TestCFindUnencodableIdentifierIsReported(t *testing.T) {
	t.Parallel()

	addr := serveFindSCP(t, ae.FindMatches([]ae.FindMatch{
		{Status: dimse.StatusPending, Identifier: godicom.NewDataset()},
	}))

	scu := dialRawSCU(t, addr, "STREAMSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 23)
	scu.send(cmd, ident)

	if st := nextStatus(t, scu, 5*time.Second); st != dcmstatus.FindIdentifierNotEncodable {
		t.Fatalf("status=0x%04x, want 0x%04x", st, dcmstatus.FindIdentifierNotEncodable)
	}
}

// A handler that yields only matches has still said nothing about how the
// operation ends; the framework ends it with Success, as the reference does
// when the generator is exhausted (service_class.py:398-402). The slice
// contract used to leave the operation open in this case.
func TestCFindWithoutFinalResponseEndsWithSuccess(t *testing.T) {
	t.Parallel()

	addr := serveFindSCP(t, ae.FindMatches([]ae.FindMatch{
		{Status: dimse.StatusPending, Identifier: matchDS("P1")},
	}))

	scu := dialRawSCU(t, addr, "STREAMSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 24)
	scu.send(cmd, ident)

	if st := nextStatus(t, scu, 5*time.Second); !dimse.IsPending(st) {
		t.Fatalf("first response status=0x%04x, want Pending", st)
	}
	if st := nextStatus(t, scu, 5*time.Second); st != dimse.StatusSuccess {
		t.Fatalf("final status=0x%04x, want Success", st)
	}
}

// Cancellation reaches the handler as a cause, so a handler that is blocked
// part-way through its work learns why it was stopped and does not have to poll
// anything (pynetdicom's is_cancelled()).
func TestCFindHandlerSeesPeerCancelAsCause(t *testing.T) {
	t.Parallel()

	cause := make(chan error, 1)
	addr := serveFindSCP(t, func(hctx context.Context, _ ae.FindRequest) iter.Seq2[ae.FindMatch, error] {
		return func(yield func(ae.FindMatch, error) bool) {
			for i := 0; ; i++ {
				if hctx.Err() != nil {
					cause <- context.Cause(hctx)
					return
				}
				if !yield(ae.FindMatch{Status: dimse.StatusPending, Identifier: matchDS("P")}, nil) {
					cause <- context.Cause(hctx)
					return
				}
			}
		}
	})

	scu := dialRawSCU(t, addr, "STREAMSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 25)
	scu.send(cmd, ident)

	if st := nextStatus(t, scu, 5*time.Second); !dimse.IsPending(st) {
		t.Fatalf("first response status=0x%04x, want Pending", st)
	}

	cancelCmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 25}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(cancelCmd, nil)

	for {
		st := nextStatus(t, scu, 5*time.Second)
		if dimse.IsPending(st) {
			continue
		}
		if st != dimse.StatusCancel {
			t.Fatalf("final status=0x%04x, want Cancel", st)
		}
		break
	}

	select {
	case got := <-cause:
		if !errors.Is(got, ae.ErrPeerCancelled) {
			t.Fatalf("handler cause=%v, want ErrPeerCancelled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never observed the cancel")
	}
}
