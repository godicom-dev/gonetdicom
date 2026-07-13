package dimse

import (
	"bytes"
	"testing"
)

func TestCStoreRQGolden(t *testing.T) {
	if goldenCStoreRQPDV[0] != 0x03 {
		t.Fatalf("expected MCH 0x03, got 0x%02x", goldenCStoreRQPDV[0])
	}
	cmd := goldenCStoreRQPDV[1:]
	rq, err := DecodeCStoreRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.Priority != PriorityLow {
		t.Fatalf("rq: %+v", rq)
	}
	if rq.AffectedSOPClassUID != "1.1.1" || rq.AffectedSOPInstanceUID != "1.2.1" {
		t.Fatalf("uids: %+v", rq)
	}
	if rq.MoveOriginatorApplicationEntityTitle != "UNITTEST" || rq.MoveOriginatorMessageID != 3 {
		t.Fatalf("move: %+v", rq)
	}
	got, err := (&CStoreRQ{
		MessageID:                            7,
		Priority:                             PriorityLow,
		AffectedSOPClassUID:                  "1.1.1",
		AffectedSOPInstanceUID:               "1.2.1",
		MoveOriginatorApplicationEntityTitle: "UNITTEST",
		MoveOriginatorMessageID:              3,
		HasMoveOriginator:                    true,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCStoreRSPGolden(t *testing.T) {
	cmd := goldenCStoreRSPPDV[1:]
	rsp, err := DecodeCStoreRSP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 5 || rsp.Status != StatusSuccess {
		t.Fatalf("rsp: %+v", rsp)
	}
	if rsp.AffectedSOPClassUID != "1.2.4.10" || rsp.AffectedSOPInstanceUID != "1.2.4.5.7.8" {
		t.Fatalf("uids: %+v", rsp)
	}
	got, err := (&CStoreRSP{
		MessageIDBeingRespondedTo: 5,
		AffectedSOPClassUID:       "1.2.4.10",
		AffectedSOPInstanceUID:    "1.2.4.5.7.8",
		Status:                    StatusSuccess,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCStoreDatasetGolden(t *testing.T) {
	if goldenCStoreDSPDV[0] != 0x02 {
		t.Fatalf("expected data MCH 0x02, got 0x%02x", goldenCStoreDSPDV[0])
	}
	if len(goldenCStoreDSPDV[1:]) != 34 {
		t.Fatalf("dataset len %d", len(goldenCStoreDSPDV[1:]))
	}
}
