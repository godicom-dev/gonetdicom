package ae_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// rawConn opens a TCP connection the test drives by hand.
func rawConn(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// requireServerStillServes dials again and completes a C-ECHO, proving the
// previous connection did not take the server down with it.
func requireServerStillServes(t *testing.T, addr, calledAE string) {
	t.Helper()
	scu := dialRawSCU(t, addr, calledAE, pdu.VerificationSOPClass)
	command, err := (&dimse.CEchoRQ{MessageID: 1}).Encode()
	if err != nil {
		t.Fatalf("encode C-ECHO-RQ: %v", err)
	}
	scu.send(command, nil)
	raw, err := scu.nextCommand(5 * time.Second)
	if err != nil {
		t.Fatalf("C-ECHO after the bad connection: %v", err)
	}
	rsp, err := dimse.DecodeCEchoRSP(raw)
	if err != nil {
		t.Fatalf("decode C-ECHO-RSP: %v", err)
	}
	if rsp.Status != 0x0000 {
		t.Fatalf("C-ECHO status 0x%04x, want success", rsp.Status)
	}
}

// A-ABORT is legal at any time, including before the association exists. The
// server has to end that connection and keep accepting others: an abort is a
// peer saying "forget it", not a fault in the listener.
func TestSCPSurvivesAbortAsFirstPDU(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "ABORTSCP"})
	conn := rawConn(t, addr)
	if err := pdu.Write(conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00}); err != nil {
		t.Fatalf("write A-ABORT: %v", err)
	}
	// The server may answer with an abort of its own or just close; either way
	// the read must finish rather than hang.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = pdu.Read(conn)

	requireServerStillServes(t, addr, "ABORTSCP")
}

// Bytes that are not a PDU at all: the listener must drop the connection, not
// the process.
func TestSCPSurvivesGarbageBeforeAssociation(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "GARBAGESCP"})
	conn := rawConn(t, addr)
	if _, err := conn.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = pdu.Read(conn)

	requireServerStillServes(t, addr, "GARBAGESCP")
}

// A truncated A-ASSOCIATE-RQ — the header promises more than the peer will ever
// send — has to time out or fail cleanly. The dangerous outcome is a goroutine
// stuck reading a length it will never satisfy, so the check is that the
// listener still answers afterwards.
func TestSCPSurvivesTruncatedAssociateRQ(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "TRUNCSCP"})
	conn := rawConn(t, addr)
	// Type 0x01, reserved, length 0x00010000 (64 KiB) with no body at all.
	if _, err := conn.Write([]byte{0x01, 0x00, 0x00, 0x01, 0x00, 0x00}); err != nil {
		t.Fatalf("write partial header: %v", err)
	}
	_ = conn.Close()

	requireServerStillServes(t, addr, "TRUNCSCP")
}

// An A-ASSOCIATE-RQ with no presentation contexts is legal to send and has a
// defined answer: an A-ASSOCIATE-AC with no contexts. Anything the peer then
// sends has no context to run on, and the answer is 0x0122 — not a panic and
// not a silent drop.
func TestSCPAssociationWithoutPresentationContexts(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "NOCTXSCP"})
	conn := rawConn(t, addr)

	rq := associateRQ("NOCTXSCP")
	rq.PresentationContexts = nil
	if err := pdu.Write(conn, rq); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	ac, ok := raw.(*pdu.AAssociateAC)
	if !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", raw)
	}
	if len(ac.PresentationContexts) != 0 {
		t.Fatalf("AC carries %d contexts, want none", len(ac.PresentationContexts))
	}

	command, err := (&dimse.CEchoRQ{MessageID: 1}).Encode()
	if err != nil {
		t.Fatalf("encode C-ECHO-RQ: %v", err)
	}
	pdus, err := pdu.FragmentMessage(1, command, nil, 0)
	if err != nil {
		t.Fatalf("fragment: %v", err)
	}
	for _, p := range pdus {
		if err := pdu.Write(conn, p); err != nil {
			t.Fatalf("write P-DATA: %v", err)
		}
	}

	raw, err = pdu.Read(conn)
	if err != nil {
		t.Fatalf("read C-ECHO-RSP: %v", err)
	}
	pdata, ok := raw.(*pdu.PDataTF)
	if !ok {
		t.Fatalf("expected P-DATA-TF, got %T", raw)
	}
	var cmd []byte
	for _, pdv := range pdata.PDVs {
		cmd = append(cmd, pdv.Fragment()...)
	}
	rsp, err := dimse.DecodeCEchoRSP(cmd)
	if err != nil {
		t.Fatalf("decode C-ECHO-RSP: %v", err)
	}
	if rsp.Status != 0x0122 {
		t.Fatalf("status 0x%04x, want 0x0122 (SOP class not supported)", rsp.Status)
	}
}

// The peer aborting mid-association is a normal end-of-association event: the
// server must not answer it with anything, and must keep serving.
func TestSCPSurvivesPeerAbortMidAssociation(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "MIDABORT"})
	scu := dialRawSCU(t, addr, "MIDABORT", pdu.VerificationSOPClass)
	if err := pdu.Write(scu.conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00}); err != nil {
		t.Fatalf("write A-ABORT: %v", err)
	}

	requireServerStillServes(t, addr, "MIDABORT")
}

// A server whose Admit hook cancels must still accept the next peer: the
// rejected connection is over, the listener is not.
func TestSCPSurvivesRepeatedEarlyFailures(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "FAILSCP",
		Admit: func(ctx context.Context, info ae.AssocInfo) (func(), error) {
			return nil, &ae.RejectError{
				Result: pdu.RejectResultTransient,
				Source: pdu.RejectSourceServiceProviderPres,
				Reason: pdu.RejectReasonTemporaryCongestion,
			}
		},
	})

	for i := 0; i < 3; i++ {
		conn := rawConn(t, addr)
		if err := pdu.Write(conn, associateRQ("FAILSCP")); err != nil {
			t.Fatalf("write RQ: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		raw, err := pdu.Read(conn)
		if err != nil {
			t.Fatalf("read rejection %d: %v", i, err)
		}
		if _, ok := raw.(*pdu.AAssociateRJ); !ok {
			t.Fatalf("attempt %d: expected A-ASSOCIATE-RJ, got %T", i, raw)
		}
		_ = conn.Close()
	}
}
