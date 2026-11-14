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

// acceptRawAssociation answers an A-ASSOCIATE-RQ on conn. False means the
// handshake did not happen; the test notices through the SCU's own error.
func acceptRawAssociation(conn net.Conn) bool {
	raw, err := pdu.Read(conn)
	if err != nil {
		return false
	}
	rq, ok := raw.(*pdu.AAssociateRQ)
	if !ok {
		return false
	}
	acs := make([]pdu.PresentationContextAC, 0, len(rq.PresentationContexts))
	for _, pc := range rq.PresentationContexts {
		ts := pdu.ImplicitVRLittleEndian
		if len(pc.TransferSyntaxes) > 0 {
			ts = pc.TransferSyntaxes[0]
		}
		acs = append(acs, pdu.PresentationContextAC{ID: pc.ID, Result: 0, TransferSyntax: ts})
	}
	return pdu.Write(conn, &pdu.AAssociateAC{
		CalledAETitle:          rq.CalledAETitle,
		CallingAETitle:         rq.CallingAETitle,
		ApplicationContextName: rq.ApplicationContextName,
		PresentationContexts:   acs,
		UserInformation: pdu.UserInformation{
			MaxLength:              pdu.DefaultMaxPDULength,
			ImplementationClassUID: "1.2.826.0.1.3680043.10.541.2",
		},
	}) == nil
}

// echoPeer accepts one association, answers the C-ECHO-RQ with whatever
// respond writes, and then answers the release. respond receives the command
// set's presentation context so it can build the response; a nil respond means
// the peer stays silent, which is what an IdleTimeout test needs.
func echoPeer(t *testing.T, ln net.Listener, respond func(conn net.Conn, pcid byte, rq *dimse.CEchoRQ)) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	if !acceptRawAssociation(conn) {
		return
	}

	scu := &rawSCU{t: t, conn: conn}
	control, cmd, err := scu.nextItem(10 * time.Second)
	if err != nil || control != nil {
		t.Errorf("peer: read C-ECHO-RQ: control=%T err=%v", control, err)
		return
	}
	echo, err := dimse.DecodeCEchoRQ(cmd)
	if err != nil {
		t.Errorf("peer: decode C-ECHO-RQ: %v", err)
		return
	}
	if respond != nil {
		respond(conn, 1, echo)
	}

	for {
		control, _, err := scu.nextItem(10 * time.Second)
		if err != nil {
			return
		}
		if _, ok := control.(*pdu.AReleaseRQ); ok {
			_ = pdu.Write(conn, &pdu.AReleaseRP{})
			return
		}
		if control != nil {
			t.Errorf("peer: got %T, want A-RELEASE-RQ", control)
			return
		}
	}
}

// writeSplitEchoRSP writes a C-ECHO-RSP in two pieces with a pause in the
// middle, so a reader that lets a deadline interrupt a PDU loses the split.
func writeSplitEchoRSP(t *testing.T, conn net.Conn, pcid byte, msgID uint16, finish <-chan struct{}) {
	t.Helper()
	cmd, err := (&dimse.CEchoRSP{MessageIDBeingRespondedTo: msgID, Status: dimse.StatusSuccess}).Encode()
	if err != nil {
		t.Errorf("peer: encode C-ECHO-RSP: %v", err)
		return
	}
	pdus, err := pdu.FragmentMessage(pcid, cmd, nil, pdu.DefaultMaxPDULength)
	if err != nil || len(pdus) != 1 {
		t.Errorf("peer: fragment: %v (%d PDUs)", err, len(pdus))
		return
	}
	b, err := pdus[0].Encode()
	if err != nil {
		t.Errorf("peer: encode PDU: %v", err)
		return
	}
	// Six bytes of header plus six of body: enough that the header is consumed
	// and the body read is the one that blocks.
	if _, err := conn.Write(b[:12]); err != nil {
		return
	}
	<-finish
	_, _ = conn.Write(b[12:])
}

// A context deadline that expires while a PDU is half-read must not cost the
// association. The deadline used to be applied to the socket, so the bytes
// already read were gone and the rest of that PDU was parsed as the header of
// the next one: every later read was garbage, Release included, and A-ABORT was
// the only way out. Now the reader goroutine owns the PDU it is in, finishes it
// when the rest arrives, and Release — which skips P-DATA — drains to the
// A-RELEASE-RP.
func TestSCUDeadlineMidPDULeavesTheStreamFramed(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	finish := make(chan struct{})
	go echoPeer(t, ln, func(conn net.Conn, pcid byte, rq *dimse.CEchoRQ) {
		writeSplitEchoRSP(t, conn, pcid, rq.MessageID, finish)
	})

	assoc, err := ae.Dial(context.Background(), ae.Config{AETitle: "SPLITSCU"}, ln.Addr().String(), "ECHOSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err = assoc.CEcho(ctx)
	if err == nil {
		t.Fatal("C-ECHO returned nil, want the deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("C-ECHO error = %v, want context.DeadlineExceeded", err)
	}

	// The rest of the response arrives after the caller has given up, which is
	// the case a socket-level deadline cannot survive.
	close(finish)

	relCtx, relCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer relCancel()
	if err := assoc.Release(relCtx); err != nil {
		t.Fatalf("release after a mid-PDU deadline: %v", err)
	}
}

// IdleTimeout survives the move to a reader goroutine: a peer that goes silent
// fails the waiting read instead of holding it forever, and the association is
// still healthy afterwards — the release goes through.
func TestSCUIdleTimeoutFailsTheWaitAndKeepsTheAssociation(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go echoPeer(t, ln, nil) // never answers the C-ECHO

	assoc, err := ae.Dial(context.Background(), ae.Config{
		AETitle:     "IDLESCU",
		IdleTimeout: 150 * time.Millisecond,
	}, ln.Addr().String(), "ECHOSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	start := time.Now()
	err = assoc.CEcho(context.Background())
	if !errors.Is(err, ae.ErrIdleTimeout) {
		t.Fatalf("C-ECHO error = %v, want ErrIdleTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("idle timeout took %v", elapsed)
	}

	relCtx, relCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer relCancel()
	if err := assoc.Release(relCtx); err != nil {
		t.Fatalf("release after an idle timeout: %v", err)
	}
}

// The caller's context still decides how long a wait may last: a long
// IdleTimeout must not extend it.
func TestSCUDeadlineWinsOverALongIdleTimeout(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go echoPeer(t, ln, nil)

	assoc, err := ae.Dial(context.Background(), ae.Config{
		AETitle:     "IDLESCU",
		IdleTimeout: 30 * time.Second,
	}, ln.Addr().String(), "ECHOSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := assoc.CEcho(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("C-ECHO error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the deadline took %v to fire", elapsed)
	}
}

// A peer that stops sending mid-association ends the reader, and the operation
// waiting on it reports a closed connection instead of waiting forever. The
// half-close is what makes it deterministic: our write still goes through, so
// the failure is the one the read side sees.
func TestSCUPeerHangupEndsTheReadSide(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if !acceptRawAssociation(conn) {
			return
		}
		// Half-close: the SCU's writes still work, its reads see EOF.
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		time.Sleep(2 * time.Second)
	}()

	assoc, err := ae.Dial(context.Background(), ae.Config{AETitle: "HANGUPSU"}, ln.Addr().String(), "HANGUPSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = assoc.CEcho(ctx)
	if err == nil {
		t.Fatal("C-ECHO succeeded after the peer hung up")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("C-ECHO waited for its context instead of reporting the hangup: %v", err)
	}
}

// Close with PDUs queued is the case where the reader is parked handing one
// over rather than blocked on the socket; it must still stop, and an operation
// started afterwards must fail instead of waiting for a reader that is gone.
func TestSCUCloseWithQueuedPDUsStopsTheReader(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	queued := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if !acceptRawAssociation(conn) {
			return
		}
		// Two responses nobody asked for: the caller never reads, so the reader
		// fills its one-slot channel and parks on the second hand-off.
		cmd, err := (&dimse.CEchoRSP{MessageIDBeingRespondedTo: 1, Status: dimse.StatusSuccess}).Encode()
		if err != nil {
			return
		}
		for i := 0; i < 2; i++ {
			pdus, err := pdu.FragmentMessage(1, cmd, nil, pdu.DefaultMaxPDULength)
			if err != nil {
				return
			}
			if err := pdu.Write(conn, pdus[0]); err != nil {
				return
			}
		}
		close(queued)
		time.Sleep(2 * time.Second)
	}()

	assoc, err := ae.Dial(context.Background(), ae.Config{AETitle: "QUEUEDSU"}, ln.Addr().String(), "QUEUEDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	<-queued
	time.Sleep(50 * time.Millisecond) // let the reader reach the parked hand-off
	if err := assoc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := assoc.CEcho(ctx); err == nil {
		t.Fatal("C-ECHO succeeded on a closed association")
	}
}
