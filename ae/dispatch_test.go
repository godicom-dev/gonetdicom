package ae_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// cmdElemUS encodes one Implicit VR LE group-0000 element holding a US value.
func cmdElemUS(elem, v uint16) []byte {
	b := make([]byte, 10)
	binary.LittleEndian.PutUint16(b[2:4], elem)
	binary.LittleEndian.PutUint32(b[4:8], 2)
	binary.LittleEndian.PutUint16(b[8:10], v)
	return b
}

// commandSet prefixes body with its (0000,0000) group length.
func commandSet(body []byte) []byte {
	out := make([]byte, 12)
	binary.LittleEndian.PutUint32(out[4:8], 4)
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(body)))
	return append(out, body...)
}

// A command set with no Command Field (0000,0100) satisfies every DIMSE decoder,
// because they only reject a Command Field that is present and wrong. Dispatch
// must therefore read the Command Field rather than trying decoders in turn —
// otherwise this was answered as whatever was tried first (a C-ECHO-RSP).
func TestSCPRejectsCommandWithoutCommandField(t *testing.T) {
	t.Parallel()

	scu := dialRawSCU(t, findSCP(t, 1), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)

	var body []byte
	body = append(body, cmdElemUS(0x0110, 42)...)                           // Message ID
	body = append(body, cmdElemUS(0x0800, dimse.CommandDataSetTypeNone)...) // no dataset
	scu.send(commandSet(body), nil)

	got, err := scu.nextPDU(5 * time.Second)
	if err != nil {
		t.Fatalf("expected an A-ABORT, got error: %v", err)
	}
	if _, ok := got.(*pdu.AAbort); ok {
		return
	}
	if pd, ok := got.(*pdu.PDataTF); ok && len(pd.PDVs) > 0 {
		if field, err := dimse.PeekCommandField(pd.PDVs[0].Fragment()); err == nil {
			t.Fatalf("SCP answered a Command-Field-less command with %s", dimse.CommandName(field))
		}
	}
	t.Fatalf("expected an A-ABORT, got %T", got)
}

// A C-CANCEL-RQ arriving after the operation has already finished is late, not
// invalid. It must be ignored, leaving the association usable.
func TestSCPIgnoresLateCCancel(t *testing.T) {
	t.Parallel()

	scu := dialRawSCU(t, findSCP(t, 2), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 21)
	scu.send(cmd, ident)
	for {
		rsp, err := scu.nextCommand(5 * time.Second)
		if err != nil {
			t.Fatalf("draining C-FIND: %v", err)
		}
		if st, ok := dimse.PeekStatus(rsp); ok && !dimse.IsPending(st) {
			break
		}
	}

	cancelCmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 21}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(cancelCmd, nil)

	echo, err := (&dimse.CEchoRQ{MessageID: 22, AffectedSOPClassUID: pdu.VerificationSOPClass}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(echo, nil)

	rsp, err := scu.nextCommand(5 * time.Second)
	if err != nil {
		t.Fatalf("C-ECHO after a late cancel: %v", err)
	}
	field, err := dimse.PeekCommandField(rsp)
	if err != nil {
		t.Fatalf("response has no Command Field: %v", err)
	}
	if field != dimse.CommandCEchoRSP {
		t.Fatalf("got %s, want C-ECHO-RSP", dimse.CommandName(field))
	}
}
