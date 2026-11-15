package dimse_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/dimse"
)

func TestPeekCommandFieldAndName(t *testing.T) {
	t.Parallel()
	cmd, err := (&dimse.CEchoRQ{MessageID: 7}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	field, err := dimse.PeekCommandField(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if field != dimse.CommandCEchoRQ {
		t.Fatalf("field=%04x", field)
	}
	if got := dimse.CommandName(field); got != "C-ECHO-RQ" {
		t.Fatalf("name=%q", got)
	}
	msgID, ok := dimse.PeekMessageID(cmd)
	if !ok || msgID != 7 {
		t.Fatalf("message id=%d ok=%v", msgID, ok)
	}
}

func TestPeekStatus(t *testing.T) {
	t.Parallel()
	cmd, err := (&dimse.CEchoRSP{MessageIDBeingRespondedTo: 1, Status: dimse.StatusSuccess}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := dimse.PeekStatus(cmd)
	if !ok || st != dimse.StatusSuccess {
		t.Fatalf("status=%04x ok=%v", st, ok)
	}
	if dimse.FormatStatus(st) != "0x0000" {
		t.Fatalf("format=%s", dimse.FormatStatus(st))
	}
}

func TestPeekResponseInfo(t *testing.T) {
	t.Parallel()

	rsp, err := (&dimse.CFindRSP{
		MessageIDBeingRespondedTo: 3,
		AffectedSOPClassUID:       "1.2.840.10008.5.1.4.1.2.2.1",
		Status:                    0xB001,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	field, status, sopClass, ok := dimse.PeekResponseInfo(rsp)
	if !ok {
		t.Fatal("a C-FIND-RSP is not recognised as a response")
	}
	if field != dimse.CommandCFindRSP || status != 0xB001 {
		t.Fatalf("field=0x%04x status=0x%04x", field, status)
	}
	if sopClass != "1.2.840.10008.5.1.4.1.2.2.1" {
		t.Fatalf("sop class=%q", sopClass)
	}

	// A request carries no Status and is not a response, so both halves of the
	// answer come back false. The Command Field's bit 15 is what tells them
	// apart (PS3.7 Table 9.1-1), not the presence of a Status.
	req, err := (&dimse.CFindRQ{
		MessageID:           3,
		AffectedSOPClassUID: "1.2.840.10008.5.1.4.1.2.2.1",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := dimse.PeekResponseInfo(req); ok {
		t.Fatal("a C-FIND-RQ is reported as a response")
	}

	// C-ECHO-RSP always names the Verification SOP Class, whether the SCU filled
	// it in or not (CEchoRSP.Encode substitutes it), so there is no response
	// without a class to look up.
	echo, err := (&dimse.CEchoRSP{MessageIDBeingRespondedTo: 3, Status: dimse.StatusSuccess}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	field, status, sopClass, ok = dimse.PeekResponseInfo(echo)
	if !ok || field != dimse.CommandCEchoRSP || status != dimse.StatusSuccess {
		t.Fatalf("C-ECHO-RSP: field=0x%04x status=0x%04x ok=%v", field, status, ok)
	}
	if sopClass != dimse.VerificationSOPClass {
		t.Fatalf("C-ECHO-RSP sop class=%q, want %q", sopClass, dimse.VerificationSOPClass)
	}
}
