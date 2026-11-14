package ae_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Admit is the one place a caller says no. The answer has to reach the peer as
// a proper A-ASSOCIATE-RJ, not as a bare close: a peer that gets RJ knows to
// retry later, a peer whose socket disappears only knows something broke.
func TestAdmitRejectErrorIsSentToPeer(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "ADMITSCP",
		Admit: func(context.Context, ae.AssocInfo) (func(), error) {
			return nil, ae.RejectLocalLimit(errors.New("quota exhausted"))
		},
	})

	got := associate(t, addr, associateRQ("ADMITSCP"))
	requireRejectResult(t, got, pdu.RejectResultTransient,
		pdu.RejectSourceServiceProviderPres, pdu.RejectReasonLocalLimitExceeded)
}

// A policy that fails in a way it did not describe still owes the peer an
// answer. Transient is the only honest result: retry may well work.
func TestAdmitPlainErrorIsTransientRejection(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "ADMITSCP",
		Admit: func(context.Context, ae.AssocInfo) (func(), error) {
			return nil, errors.New("licence server unreachable")
		},
	})

	got := associate(t, addr, associateRQ("ADMITSCP"))
	requireRejectResult(t, got, pdu.RejectResultTransient,
		pdu.RejectSourceServiceProviderACSE, pdu.RejectReasonNoReasonGiven)
}

// The decision is taken with the requestor already identified, and whatever
// Admit took it must hand back when the association ends — otherwise a quota
// shrinks with every association and never recovers.
func TestAdmitSeesRequestorAndReleases(t *testing.T) {
	t.Parallel()

	infoCh := make(chan ae.AssocInfo, 1)
	released := make(chan struct{})
	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "ADMITSCP",
		Admit: func(_ context.Context, info ae.AssocInfo) (func(), error) {
			infoCh <- info
			return func() { close(released) }, nil
		},
	})

	scu := dialRawSCU(t, addr, "ADMITSCP", pdu.VerificationSOPClass)

	var info ae.AssocInfo
	select {
	case info = <-infoCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Admit was not called")
	}
	if info.CallingAETitle != "RAWSCU" {
		t.Errorf("CallingAETitle=%q", info.CallingAETitle)
	}
	if info.CalledAETitle != "ADMITSCP" {
		t.Errorf("CalledAETitle=%q", info.CalledAETitle)
	}
	if info.RemoteAddr == nil {
		t.Error("RemoteAddr is nil")
	}

	_ = scu.conn.Close()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("release func did not run when the association ended")
	}
}

// Admit may block — that is how a caller queues instead of rejecting. The
// block must not outlive the handshake it is queueing for, even when the policy
// itself ignores cancellation: the socket is closed at the deadline.
func TestAdmitBlockingRespectsHandshakeTimeout(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	ctxErr := make(chan error, 1)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:          "ADMITSCP",
		HandshakeTimeout: 150 * time.Millisecond,
		Admit: func(ctx context.Context, _ ae.AssocInfo) (func(), error) {
			<-block
			ctxErr <- ctx.Err()
			return nil, nil
		},
	})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := pdu.Write(conn, associateRQ("ADMITSCP")); err != nil {
		t.Fatalf("write RQ: %v", err)
	}

	requireClosed(t, conn, 3*time.Second)
	close(block)
	select {
	case err := <-ctxErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Admit context err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Admit never observed its context")
	}
}

// The promised alternative to a thread pool and a job queue: excess peers wait
// on the goroutine they already have. Nothing is rejected, no queue is sized,
// and the second association is served as soon as the first one ends.
func TestAdmitQueuesInsteadOfRejecting(t *testing.T) {
	t.Parallel()

	slots := make(chan struct{}, 1)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "ADMITSCP",
		Admit: func(ctx context.Context, _ ae.AssocInfo) (func(), error) {
			select {
			case slots <- struct{}{}:
				return func() { <-slots }, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})

	first := dialRawSCU(t, addr, "ADMITSCP", pdu.VerificationSOPClass)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := pdu.Write(conn, associateRQ("ADMITSCP")); err != nil {
		t.Fatalf("write RQ: %v", err)
	}

	// The slot is taken: the second requestor must wait, not be rejected.
	if err := conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if raw, err := pdu.Read(conn); err == nil {
		t.Fatalf("expected to be queued, got %T", raw)
	} else {
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("expected a read timeout while queued, got %v", err)
		}
	}

	// Ending the first association frees the slot; the queued one proceeds.
	_ = first.conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read AC after queueing: %v", err)
	}
	if _, ok := raw.(*pdu.AAssociateAC); !ok {
		t.Fatalf("expected A-ASSOCIATE-AC after the slot freed, got %T", raw)
	}
}

// A panicking handler must cost one association, not the process. A-ABORT is
// the answer rather than a DIMSE failure status: after an unexpected panic the
// SCP cannot vouch for its state, and a status code would claim it can.
func TestHandlerPanicAbortsAssociationNotServer(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "PANICSCP",
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(context.Context, ae.StoreEvent) uint16 {
			if calls.Add(1) == 1 {
				panic("handler bug")
			}
			return dimse.StatusSuccess
		},
	})

	store := func() (uint16, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assoc, err := ae.Dial(ctx, ae.Config{
			AETitle: "PANICSCU",
			PresentationContexts: []ae.PresentationContext{{
				ID:               1,
				AbstractSyntax:   secondaryCaptureSOPClass,
				TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
			}},
		}, addr, "PANICSCP")
		if err != nil {
			return 0, err
		}
		defer func() { _ = assoc.Close() }()
		res, err := assoc.CStore(ctx, ae.StoreRequest{
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.3.4.5",
			Dataset:                goldenStoreDataset(),
		})
		if err != nil {
			return 0, err
		}
		return res.Status, nil
	}

	if _, err := store(); !errors.Is(err, ae.ErrAborted) {
		t.Fatalf("first C-STORE: err = %v, want ErrAborted", err)
	}
	status, err := store()
	if err != nil {
		t.Fatalf("second C-STORE after the panic: %v", err)
	}
	if status != dimse.StatusSuccess {
		t.Fatalf("second C-STORE status 0x%04x", status)
	}
}

// MaxDatasetSize bounds what one message can make the SCP buffer. Over the cap
// the association ends — there is no DIMSE status meaning "too big" — and the
// server keeps serving normal traffic.
func TestMaxDatasetSizeEndsAssociation(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "BIGSCP",
		MaxDatasetSize:           2048,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(context.Context, ae.StoreEvent) uint16 {
			return dimse.StatusSuccess
		},
	})

	scu := dialRawSCU(t, addr, "BIGSCP", secondaryCaptureSOPClass)
	cmd, err := (&dimse.CStoreRQ{
		MessageID:              1,
		Priority:               dimse.PriorityMedium,
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
	}).Encode()
	if err != nil {
		t.Fatalf("encode C-STORE-RQ: %v", err)
	}
	scu.send(cmd, make([]byte, 8192))
	requireClosed(t, scu.conn, 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "BIGSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   secondaryCaptureSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, "BIGSCP")
	if err != nil {
		t.Fatalf("dial after the oversized message: %v", err)
	}
	defer func() { _ = assoc.Close() }()
	res, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.6",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE within the limit: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
}
