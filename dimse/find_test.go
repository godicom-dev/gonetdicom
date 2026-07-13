package dimse

import (
	"bytes"
	"testing"
)

func TestCFindRQGolden(t *testing.T) {
	cmd := goldenCFindRQPDV[1:]
	rq, err := DecodeCFindRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.Priority != PriorityLow {
		t.Fatalf("rq: %+v", rq)
	}
	if rq.AffectedSOPClassUID != "1.2.840.10008.5.1.4.1.1.2" {
		t.Fatalf("sop=%q", rq.AffectedSOPClassUID)
	}
	got, err := (&CFindRQ{
		MessageID:           7,
		Priority:            PriorityLow,
		AffectedSOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCFindRSPGolden(t *testing.T) {
	cmd := goldenCFindRSPPDV[1:]
	rsp, err := DecodeCFindRSP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 5 || rsp.Status != StatusPending {
		t.Fatalf("rsp: %+v", rsp)
	}
	if !rsp.HasDataset {
		t.Fatal("expected dataset present")
	}
	got, err := (&CFindRSP{
		MessageIDBeingRespondedTo: 5,
		AffectedSOPClassUID:       "1.2.840.10008.5.1.4.1.1.2",
		Status:                    StatusPending,
		HasDataset:                true,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}
