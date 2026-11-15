package ae_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// unknownPDU is a complete PDU whose type byte has no meaning: 0x53 appears in
// pynetdicom's test_dul.py::test_unknown_pdu_aborts for the same reason. The
// body is present because a receiver reads the declared length before it looks
// at the type, so a truncated one would be testing the wrong thing.
var unknownPDU = []byte{0x53, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00}

// requireUnrecognizedAbort reads one PDU and requires it to be an A-ABORT from
// the presentation layer with the "unrecognized PDU" reason.
func requireUnrecognizedAbort(t *testing.T, conn net.Conn, what string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("%s: no answer to the bad PDU: %v", what, err)
	}
	abort, ok := raw.(*pdu.AAbort)
	if !ok {
		t.Fatalf("%s: got %T, want A-ABORT", what, raw)
	}
	if abort.Source != 0x02 {
		t.Errorf("%s: abort source = 0x%02x, want 0x02 (presentation layer)", what, abort.Source)
	}
	if abort.ReasonDiagnostic != 0x01 {
		t.Errorf("%s: abort reason = 0x%02x, want 0x01 (unrecognized PDU)", what, abort.ReasonDiagnostic)
	}
}

// A PDU of a type this stack does not define, arriving mid-association. The
// bytes are not consumption-pacing or framing — the peer is speaking a dialect
// with a PDU in it that we do not have — so the answer is A-ABORT, not a closed
// socket: a close reads as a network fault and the peer retries the same bytes.
func TestSCPAbortsOnUnknownPDUType(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "BADPDUSCP"})
	scu := dialRawSCU(t, addr, "BADPDUSCP", pdu.VerificationSOPClass)

	if _, err := scu.conn.Write(unknownPDU); err != nil {
		t.Fatalf("write unknown PDU: %v", err)
	}
	requireUnrecognizedAbort(t, scu.conn, "unknown PDU type mid-association")

	requireServerStillServes(t, addr, "BADPDUSCP")
}

// A type we do know with a body its own rules do not allow: an A-RELEASE-RQ is
// ten bytes and this one declares no reserved field at all. Same answer, because
// the receiver is in the same position — it cannot consume the bytes.
func TestSCPAbortsOnUndecodableBody(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "BADBODYSCP"})
	scu := dialRawSCU(t, addr, "BADBODYSCP", pdu.VerificationSOPClass)

	if _, err := scu.conn.Write([]byte{0x05, 0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
		t.Fatalf("write short A-RELEASE-RQ: %v", err)
	}
	requireUnrecognizedAbort(t, scu.conn, "undecodable A-RELEASE-RQ")

	requireServerStillServes(t, addr, "BADBODYSCP")
}

// The same bytes before anything is negotiated. There is no association to end,
// but the peer still has to learn that its bytes were the problem rather than
// the network's.
func TestSCPAbortsOnUndecodableFirstPDU(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "EARLYBADSCP"})
	conn := rawConn(t, addr)

	if _, err := conn.Write(unknownPDU); err != nil {
		t.Fatalf("write unknown PDU: %v", err)
	}
	requireUnrecognizedAbort(t, conn, "undecodable first PDU")

	requireServerStillServes(t, addr, "EARLYBADSCP")
}

// The requestor's half of the same rule: an SCP that answers a C-ECHO with a PDU
// nobody defines gets an A-ABORT back, and the operation reports the decode
// failure instead of a timeout or a closed connection.
func TestSCUAbortsOnUnknownPDUType(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	peerSaw := make(chan pdu.PDU, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if !acceptRawAssociation(conn) {
			return
		}
		// The C-ECHO-RQ, so the requestor is definitely waiting on the read.
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := pdu.Read(conn); err != nil {
			return
		}
		_, _ = conn.Write(unknownPDU)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if raw, err := pdu.Read(conn); err == nil {
			peerSaw <- raw
		}
	}()

	assoc, err := ae.Dial(context.Background(), ae.Config{AETitle: "BADSCU"}, ln.Addr().String(), "BADSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Close() }()

	err = assoc.CEcho(context.Background())
	if err == nil {
		t.Fatal("C-ECHO succeeded against a peer that answered with an unknown PDU")
	}
	if !errors.Is(err, pdu.ErrUndecodable) {
		t.Errorf("C-ECHO error = %v, want it to wrap pdu.ErrUndecodable", err)
	}

	select {
	case raw := <-peerSaw:
		if _, ok := raw.(*pdu.AAbort); !ok {
			t.Errorf("peer received %T, want A-ABORT", raw)
		}
	case <-time.After(3 * time.Second):
		t.Error("peer never received an A-ABORT")
	}
}

// One abort, not one per read: after the decode failure the association is over,
// so a second statement to the peer would be a message to somebody who has
// already been told. Release is the call most likely to read again.
func TestSCUSendsOneAbortPerBadPDU(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	second := make(chan pdu.PDU, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if !acceptRawAssociation(conn) {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := pdu.Read(conn); err != nil {
			return
		}
		_, _ = conn.Write(unknownPDU)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := pdu.Read(conn); err != nil {
			return
		}
		// Anything else arriving here is a second abort.
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if raw, err := pdu.Read(conn); err == nil {
			second <- raw
		}
	}()

	assoc, err := ae.Dial(context.Background(), ae.Config{AETitle: "ONCEU"}, ln.Addr().String(), "ONCESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Close() }()

	_ = assoc.CEcho(context.Background())
	// The association is over, so releasing it is not a request that can be
	// made — and in particular it must not put an A-RELEASE-RQ on a wire the
	// peer has already been told to forget.
	if err := assoc.Release(context.Background()); !errors.Is(err, ae.ErrAborted) {
		t.Errorf("Release after the abort = %v, want ErrAborted", err)
	}

	select {
	case raw := <-second:
		t.Errorf("peer received a second PDU after the abort: %T", raw)
	case <-time.After(700 * time.Millisecond):
	}
}
