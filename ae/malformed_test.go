package ae_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// commandFieldElement is the (0000,0100) tag as it appears on the wire. Little
// endian applies per field, so the bytes are group low/high then element
// low/high: 00 00 00 01 — not 00 00 01 00, which is the same tag read as if it
// were written big endian.
var commandFieldElement = []byte{0x00, 0x00, 0x00, 0x01}

// patchCommandField rewrites the Command Field value of an implicit VR LE
// command set, leaving its length alone so the rest still parses.
func patchCommandField(t *testing.T, cmd []byte, field uint16) []byte {
	t.Helper()
	idx := bytes.Index(cmd, commandFieldElement)
	if idx < 0 {
		t.Fatal("command field element not found")
	}
	out := append([]byte(nil), cmd...)
	binary.LittleEndian.PutUint16(out[idx+8:idx+10], field)
	return out
}

// requireAbort reads one PDU and insists it is an A-ABORT from the service
// provider. Anything else (EOF, silence) leaves the peer unable to tell a
// protocol error from a network failure.
func requireAbort(t *testing.T, s *rawSCU) {
	t.Helper()
	raw, err := s.nextPDU(5 * time.Second)
	if err != nil {
		t.Fatalf("read A-ABORT: %v", err)
	}
	abort, ok := raw.(*pdu.AAbort)
	if !ok {
		t.Fatalf("expected A-ABORT, got %T", raw)
	}
	if abort.Source != 0x02 {
		t.Fatalf("A-ABORT source=%d, want 2 (service provider)", abort.Source)
	}
}

// An unknown Command Field is not a DIMSE message at all. There is no Message ID
// to answer and no status that means "unknown request", so the association ends
// with A-ABORT (PS3.8 9.3.4).
func TestSCPAbortsOnUnknownCommandField(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "MALFORMED"})
	scu := dialRawSCU(t, addr, "MALFORMED", pdu.VerificationSOPClass)

	cmd, err := (&dimse.CEchoRQ{MessageID: 1}).Encode()
	if err != nil {
		t.Fatalf("encode C-ECHO-RQ: %v", err)
	}
	scu.send(patchCommandField(t, cmd, 0x7777), nil)
	requireAbort(t, scu)
}

// Bytes that do not even carry a Command Field are rejected the same way. A
// dispatcher that tried the decoders in turn would have treated the message as
// whichever one accepted it first.
func TestSCPAbortsOnNonCommandBytes(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "MALFORMED"})
	scu := dialRawSCU(t, addr, "MALFORMED", pdu.VerificationSOPClass)

	// A dataset-shaped blob: (0008,0018) SOP Instance UID, no command group.
	junk := []byte{
		0x08, 0x00, 0x18, 0x00, 0x04, 0x00, 0x00, 0x00, '1', '.', '2', '.',
	}
	scu.send(junk, nil)
	requireAbort(t, scu)
}

// A Command Field whose value cannot be read is a malformed command set, which
// has to end the association instead of being handled as "field 0".
func TestSCPAbortsOnUnreadableCommandField(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "MALFORMED"})
	scu := dialRawSCU(t, addr, "MALFORMED", pdu.VerificationSOPClass)

	cmd, err := (&dimse.CEchoRQ{MessageID: 1}).Encode()
	if err != nil {
		t.Fatalf("encode C-ECHO-RQ: %v", err)
	}
	idx := bytes.Index(cmd, commandFieldElement)
	if idx < 0 {
		t.Fatal("command field element not found")
	}
	broken := append([]byte(nil), cmd...)
	binary.LittleEndian.PutUint32(broken[idx+4:idx+8], 0) // US element with no value
	scu.send(broken, nil)

	requireAbort(t, scu)
}

// A malformed message must cost one association, not the server: the next
// requestor has to associate and get its answer.
func TestSCPSurvivesAbortedAssociations(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "MALFORMED"})

	for i := 0; i < 3; i++ {
		scu := dialRawSCU(t, addr, "MALFORMED", pdu.VerificationSOPClass)
		cmd, err := (&dimse.CEchoRQ{MessageID: 1}).Encode()
		if err != nil {
			t.Fatalf("encode C-ECHO-RQ: %v", err)
		}
		scu.send(patchCommandField(t, cmd, 0x7777), nil)
		requireAbort(t, scu)
	}

	// And a well-formed association still works afterwards.
	scu := dialRawSCU(t, addr, "MALFORMED", pdu.VerificationSOPClass)
	cmd, err := (&dimse.CEchoRQ{MessageID: 7}).Encode()
	if err != nil {
		t.Fatalf("encode C-ECHO-RQ: %v", err)
	}
	scu.send(cmd, nil)
	rspCmd, err := scu.nextCommand(5 * time.Second)
	if err != nil {
		t.Fatalf("read C-ECHO-RSP: %v", err)
	}
	rsp, err := dimse.DecodeCEchoRSP(rspCmd)
	if err != nil {
		t.Fatalf("decode C-ECHO-RSP: %v", err)
	}
	if rsp.Status != dimse.StatusSuccess || rsp.MessageIDBeingRespondedTo != 7 {
		t.Fatalf("echo rsp: status=0x%04X msgID=%d", rsp.Status, rsp.MessageIDBeingRespondedTo)
	}
}
