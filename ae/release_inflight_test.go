package ae_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// silentEchoPeer accepts the association, reads one P-DATA and then answers
// nothing, so a C-ECHO against it stays outstanding until its context expires.
// It reports the request on sawRequest and answers the A-RELEASE-RQ that
// follows with an A-RELEASE-RP.
func silentEchoPeer(t *testing.T, sawRequest chan<- struct{}) (string, <-chan error) {
	t.Helper()
	return fakePeer(t, func(conn net.Conn, _ *pdu.AAssociateRQ) {
		if _, err := pdu.Read(conn); err != nil {
			t.Errorf("read C-ECHO-RQ: %v", err)
			return
		}
		close(sawRequest)
		raw, err := pdu.Read(conn)
		if err != nil {
			t.Errorf("await A-RELEASE-RQ: %v", err)
			return
		}
		if _, ok := raw.(*pdu.AReleaseRQ); !ok {
			t.Errorf("got %T, want A-RELEASE-RQ", raw)
			return
		}
		if err := pdu.Write(conn, &pdu.AReleaseRP{}); err != nil {
			t.Errorf("send A-RELEASE-RP: %v", err)
		}
	})
}

func dialEchoSCU(t *testing.T, addr string) *ae.Association {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "INFLIGHT",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, "INFLIGHTP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return assoc
}

// PS3.8 7.2.2 allows a requestor nothing but A-ABORT once it has asked to
// release, and an association has one read side, so a release during an
// outstanding operation cannot be honoured — it can only be refused before it
// writes anything. The refusal has to leave the operation untouched: the point
// of failing is that the release never reached the wire.
func TestReleaseRefusesWhileOperationInFlight(t *testing.T) {
	t.Parallel()

	saw := make(chan struct{})
	addr, peerDone := silentEchoPeer(t, saw)
	assoc := dialEchoSCU(t, addr)

	opCtx, opCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer opCancel()
	opDone := make(chan error, 1)
	go func() { opDone <- assoc.CEcho(opCtx) }()

	select {
	case <-saw:
	case <-time.After(5 * time.Second):
		t.Fatal("peer never saw the C-ECHO-RQ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := assoc.Release(ctx); !errors.Is(err, ae.ErrOperationInFlight) {
		t.Fatalf("Release during C-ECHO = %v, want ErrOperationInFlight", err)
	}
	if err := <-opDone; err == nil {
		t.Fatal("C-ECHO succeeded against a peer that answers nothing")
	}
	// The association is untouched by the refusal: a release now works.
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("Release after the operation ended: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}

// A second operation would read the first one's responses, so it is refused
// rather than allowed to interleave. Before this check the failure surfaced
// later and somewhere else: as a message-id mismatch, or as one operation
// quietly receiving another's dataset.
func TestConcurrentOperationRefused(t *testing.T) {
	t.Parallel()

	saw := make(chan struct{})
	addr, peerDone := silentEchoPeer(t, saw)
	assoc := dialEchoSCU(t, addr)

	opCtx, opCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer opCancel()
	opDone := make(chan error, 1)
	go func() { opDone <- assoc.CEcho(opCtx) }()

	select {
	case <-saw:
	case <-time.After(5 * time.Second):
		t.Fatal("peer never saw the C-ECHO-RQ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := assoc.CEcho(ctx); !errors.Is(err, ae.ErrOperationInFlight) {
		t.Fatalf("second C-ECHO = %v, want ErrOperationInFlight", err)
	}
	<-opDone

	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}

// Every entry point has to check before it touches the connection, not just the
// ones this file happens to drive. The zero-value requests would fail argument
// validation if they got that far, so reaching ErrOperationInFlight is also
// evidence that the claim comes first — nothing is written for a request that
// cannot be served.
func TestEveryOperationRefusedWhenBusy(t *testing.T) {
	t.Parallel()

	saw := make(chan struct{})
	addr, peerDone := silentEchoPeer(t, saw)
	assoc := dialEchoSCU(t, addr)

	opCtx, opCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer opCancel()
	opDone := make(chan error, 1)
	go func() { opDone <- assoc.CEcho(opCtx) }()

	select {
	case <-saw:
	case <-time.After(5 * time.Second):
		t.Fatal("peer never saw the C-ECHO-RQ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queries := map[string]func() error{
		"C-ECHO":  func() error { return assoc.CEcho(ctx) },
		"C-STORE": func() error { _, err := assoc.CStore(ctx, ae.StoreRequest{}); return err },
		"C-FIND":  func() error { _, err := assoc.CFind(ctx, ae.FindRequest{}); return err },
		"C-MOVE":  func() error { _, err := assoc.CMove(ctx, ae.MoveRequest{}); return err },
		"C-GET":   func() error { _, err := assoc.CGet(ctx, ae.GetRequest{}); return err },
		"N-GET":   func() error { _, err := assoc.NGet(ctx, ae.NGetRequest{}); return err },
		"N-SET":   func() error { _, err := assoc.NSet(ctx, ae.SetRequest{}); return err },
		"N-CREATE": func() error {
			_, err := assoc.NCreate(ctx, ae.CreateRequest{})
			return err
		},
		"N-DELETE": func() error { _, err := assoc.NDelete(ctx, ae.DeleteRequest{}); return err },
		"N-ACTION": func() error { _, err := assoc.NAction(ctx, ae.ActionRequest{}); return err },
		"N-EVENT-REPORT": func() error {
			_, err := assoc.NEventReport(ctx, ae.EventReportRequest{})
			return err
		},
	}
	for name, call := range queries {
		if err := call(); !errors.Is(err, ae.ErrOperationInFlight) {
			t.Errorf("%s while C-ECHO outstanding = %v, want ErrOperationInFlight", name, err)
		}
	}

	<-opDone
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}

// The 7.2.2 Note is explicit that a requestor "may receive P-DATA Indication
// primitives" while it waits for its release response, and that it must not
// answer them. A peer that was mid-retrieve when the release was requested
// therefore keeps sending, and the first fragment used to fail the release with
// "unexpected PDU *pdu.PDataTF" — an ordinary race dressed up as a protocol
// error.
func TestReleaseIgnoresDIMSEStillArriving(t *testing.T) {
	t.Parallel()

	addr, peerDone := fakePeer(t, func(conn net.Conn, _ *pdu.AAssociateRQ) {
		raw, err := pdu.Read(conn)
		if err != nil {
			t.Errorf("await A-RELEASE-RQ: %v", err)
			return
		}
		if _, ok := raw.(*pdu.AReleaseRQ); !ok {
			t.Errorf("got %T, want A-RELEASE-RQ", raw)
			return
		}
		// A C-STORE-RQ the peer had already committed to sending. Its response
		// is owed to nobody now: the only primitive we may still send is
		// A-ABORT, so the store is simply never answered.
		cmd, err := (&dimse.CStoreRQ{
			MessageID:              1,
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.541.99.1",
		}).Encode()
		if err != nil {
			t.Errorf("encode C-STORE-RQ: %v", err)
			return
		}
		pdus, err := pdu.FragmentMessage(1, cmd, []byte{0x08, 0x00, 0x05, 0x00}, 16384)
		if err != nil {
			t.Errorf("fragment: %v", err)
			return
		}
		for _, p := range pdus {
			if err := pdu.Write(conn, p); err != nil {
				t.Errorf("write store: %v", err)
				return
			}
		}
		if err := pdu.Write(conn, &pdu.AReleaseRP{}); err != nil {
			t.Errorf("send A-RELEASE-RP: %v", err)
		}
	})

	assoc := dialEchoSCU(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("Release while the peer was still sending: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}
