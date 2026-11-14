package ae_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

// dialRawWith opens an association proposing exactly ctxs and returns the live
// connection plus the accepted contexts, so a test can send on a context the
// server refused, or on one it never negotiated — the two ways an SCU can get a
// C-STORE wrong while still speaking correct DIMSE.
func dialRawWith(t *testing.T, addr, calledAE string, ctxs ...pdu.PresentationContextRQ) (*rawSCU, *pdu.AAssociateAC) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rq := acceptRQ(calledAE, ctxs...)
	if err := pdu.Write(conn, rq); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read AC: %v", err)
	}
	ac, ok := raw.(*pdu.AAssociateAC)
	if !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", raw)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return &rawSCU{t: t, conn: conn}, ac
}

// sendOn writes one DIMSE message on a chosen presentation context.
func sendOn(t *testing.T, s *rawSCU, pcid byte, command, dataset []byte) {
	t.Helper()
	pdus, err := pdu.FragmentMessage(pcid, command, dataset, pdu.DefaultMaxPDULength)
	if err != nil {
		t.Fatalf("fragment: %v", err)
	}
	for _, p := range pdus {
		if err := pdu.Write(s.conn, p); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// storeRQ encodes a C-STORE-RQ claiming the given SOP Class.
func storeRQ(t *testing.T, sopClass, instance string) []byte {
	t.Helper()
	cmd, err := (&dimse.CStoreRQ{
		MessageID:              1,
		Priority:               dimse.PriorityMedium,
		AffectedSOPClassUID:    sopClass,
		AffectedSOPInstanceUID: instance,
	}).Encode()
	if err != nil {
		t.Fatalf("encode C-STORE-RQ: %v", err)
	}
	return cmd
}

// readStoreStatus reads the C-STORE-RSP and returns its status, failing if the
// server sent something else instead.
func readStoreStatus(t *testing.T, s *rawSCU) uint16 {
	t.Helper()
	cmd, err := s.nextCommand(5 * time.Second)
	if err != nil {
		t.Fatalf("read C-STORE-RSP: %v", err)
	}
	rsp, err := dimse.DecodeCStoreRSP(cmd)
	if err != nil {
		t.Fatalf("decode C-STORE-RSP: %v", err)
	}
	return rsp.Status
}

// A C-STORE whose Affected SOP Class UID is not the abstract syntax of the
// presentation context it arrived on is a request the association cannot serve:
// 0x0122 says the class is not supported *here*, which is exactly true. Calling
// the handler instead would file an instance under a class the peer never
// negotiated.
func TestSCPRejectsStoreOnMismatchedAbstractSyntax(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	sc := string(uid.SecondaryCaptureImageStorage)

	var called atomic.Int32
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "CTXSCP",
		AcceptedAbstractSyntaxes: []string{ct, sc},
		OnCStore: func(context.Context, ae.StoreEvent) uint16 {
			called.Add(1)
			return status.Success
		},
	})

	scu, ac := dialRawWith(t, addr, "CTXSCP",
		contextRQ(1, ct, pdu.ImplicitVRLittleEndian),
		contextRQ(3, sc, pdu.ImplicitVRLittleEndian),
	)
	requireContextResults(t, ac, map[byte]byte{1: pcAcceptance, 3: pcAcceptance})

	// Context 1 is CT; claiming Secondary Capture there is the mismatch.
	sendOn(t, scu, 1, storeRQ(t, sc, "1.2.3.4.5"), goldenStoreDataset())
	if got := readStoreStatus(t, scu); got != status.SOPClassNotSupported {
		t.Fatalf("status=0x%04X, want 0x%04X", got, status.SOPClassNotSupported)
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("handler ran %d times for a mismatched context", n)
	}

	// The same instance on the context that does match must be accepted, so the
	// refusal above is about the context and not about the class.
	sendOn(t, scu, 3, storeRQ(t, sc, "1.2.3.4.6"), goldenStoreDataset())
	if got := readStoreStatus(t, scu); got != status.Success {
		t.Fatalf("matching context: status=0x%04X, want success", got)
	}
	if n := called.Load(); n != 1 {
		t.Fatalf("handler ran %d times, want 1", n)
	}
}

// Sending on a context ID that was never negotiated is a peer bug, but it is a
// recoverable one: the association stays up and the answer names the problem.
func TestSCPRejectsStoreOnUnnegotiatedContext(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	var called atomic.Int32
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "NOCTXSCP",
		AcceptedAbstractSyntaxes: []string{ct},
		OnCStore: func(context.Context, ae.StoreEvent) uint16 {
			called.Add(1)
			return status.Success
		},
	})

	scu, _ := dialRawWith(t, addr, "NOCTXSCP", contextRQ(1, ct, pdu.ImplicitVRLittleEndian))

	sendOn(t, scu, 5, storeRQ(t, ct, "1.2.3.4.5"), goldenStoreDataset())
	if got := readStoreStatus(t, scu); got != status.SOPClassNotSupported {
		t.Fatalf("status=0x%04X, want 0x%04X", got, status.SOPClassNotSupported)
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("handler ran %d times on an unnegotiated context", n)
	}

	// Still usable afterwards, on the context that was negotiated.
	sendOn(t, scu, 1, storeRQ(t, ct, "1.2.3.4.6"), goldenStoreDataset())
	if got := readStoreStatus(t, scu); got != status.Success {
		t.Fatalf("after the bad context: status=0x%04X, want success", got)
	}
}

// A refused context cannot be used even for its own abstract syntax: result 3 in
// the AC means the peer must not send on that ID at all.
func TestSCPRejectsStoreOnRefusedContext(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	mwl := string(uid.ModalityWorklistInformationModelFind)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "REFUSEDSCP",
		AcceptedAbstractSyntaxes: []string{ct},
	})

	scu, ac := dialRawWith(t, addr, "REFUSEDSCP",
		contextRQ(1, ct, pdu.ImplicitVRLittleEndian),
		contextRQ(3, mwl, pdu.ImplicitVRLittleEndian),
	)
	requireContextResults(t, ac, map[byte]byte{1: pcAcceptance, 3: pcAbstractSyntaxNotSupport})

	sendOn(t, scu, 3, storeRQ(t, mwl, "1.2.3.4.5"), goldenStoreDataset())
	if got := readStoreStatus(t, scu); got != status.SOPClassNotSupported {
		t.Fatalf("status=0x%04X, want 0x%04X", got, status.SOPClassNotSupported)
	}
}
