package dimse

import (
	"bytes"
	"testing"
)

func TestCMoveRQGolden(t *testing.T) {
	cmd := goldenCMoveRQPDV[1:]
	rq, err := DecodeCMoveRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.Priority != PriorityLow || rq.MoveDestination != "MOVE_SCP" {
		t.Fatalf("rq: %+v", rq)
	}
	got, err := (&CMoveRQ{
		MessageID:           7,
		Priority:            PriorityLow,
		AffectedSOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
		MoveDestination:     "MOVE_SCP",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCMoveRSPGolden(t *testing.T) {
	cmd := goldenCMoveRSPPDV[1:]
	rsp, err := DecodeCMoveRSP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Status != StatusPending || !rsp.SubOperations.Present {
		t.Fatalf("rsp: %+v", rsp)
	}
	if rsp.SubOperations != (SubOperations{Remaining: 3, Completed: 1, Failed: 2, Warning: 4, Present: true}) {
		t.Fatalf("subops: %+v", rsp.SubOperations)
	}
	got, err := (&CMoveRSP{
		MessageIDBeingRespondedTo: 5,
		AffectedSOPClassUID:       "1.2.840.10008.5.1.4.1.1.2",
		Status:                    StatusPending,
		HasDataset:                true,
		SubOperations:             SubOperations{Remaining: 3, Completed: 1, Failed: 2, Warning: 4, Present: true},
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}

func TestCGetRQGolden(t *testing.T) {
	cmd := goldenCGetRQPDV[1:]
	rq, err := DecodeCGetRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.Priority != PriorityLow {
		t.Fatalf("rq: %+v", rq)
	}
	got, err := (&CGetRQ{
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

func TestCGetRSPGolden(t *testing.T) {
	cmd := goldenCGetRSPPDV[1:]
	rsp, err := DecodeCGetRSP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Status != StatusPending || rsp.SubOperations.Completed != 1 {
		t.Fatalf("rsp: %+v", rsp)
	}
	got, err := (&CGetRSP{
		MessageIDBeingRespondedTo: 5,
		AffectedSOPClassUID:       "1.2.840.10008.5.1.4.1.1.2",
		Status:                    StatusPending,
		HasDataset:                true,
		SubOperations:             SubOperations{Remaining: 3, Completed: 1, Failed: 2, Warning: 4, Present: true},
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cmd) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, cmd)
	}
}
