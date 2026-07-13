package dimse

import (
	"bytes"
	"testing"
)

func TestCEchoRQGolden(t *testing.T) {
	if goldenCEchoRQPDV[0] != 0x03 {
		t.Fatalf("expected MCH 0x03, got 0x%02x", goldenCEchoRQPDV[0])
	}
	cmd := goldenCEchoRQPDV[1:]
	rq, err := DecodeCEchoRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 {
		t.Fatalf("MessageID=%d", rq.MessageID)
	}
	if rq.AffectedSOPClassUID != VerificationSOPClass {
		t.Fatalf("SOP=%q", rq.AffectedSOPClassUID)
	}
	got, err := (&CEchoRQ{MessageID: 7}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCEchoRSPGolden(t *testing.T) {
	cmd := goldenCEchoRSPPDV[1:]
	rsp, err := DecodeCEchoRSP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 8 {
		t.Fatalf("MessageIDBeingRespondedTo=%d", rsp.MessageIDBeingRespondedTo)
	}
	if rsp.Status != StatusSuccess {
		t.Fatalf("Status=0x%04x", rsp.Status)
	}
	got, err := (&CEchoRSP{
		MessageIDBeingRespondedTo: 8,
		Status:                    StatusSuccess,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}
