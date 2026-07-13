package dimse

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNActionRQGolden(t *testing.T) {
	t.Parallel()
	// From pynetdicom encoded_dimse_n_msg.n_action_rq_cmd (without MCH).
	want := mustHex(t, "00000000040000006e000000000003001a000000312e322e3834302e31303030382e352e312e342e312e312e3200000000010200000030010000100102000000070000000008020000000100000001101c000000312e322e3339322e3230303033362e393131362e322e362e312e343800000810020000000100")
	rq, err := DecodeNActionRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.ActionTypeID != 1 || !rq.HasDataset {
		t.Fatalf("%+v", rq)
	}
	if rq.RequestedSOPClassUID != "1.2.840.10008.5.1.4.1.1.2" {
		t.Fatalf("class=%q", rq.RequestedSOPClassUID)
	}
	got, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNActionRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "0000000004000000560000000000020008000000312e322e342e313000000001020000003081000020010200000005000000000802000000010000000009020000000000000000100c000000312e322e342e352e372e380000000810020000000100")
	rsp, err := DecodeNActionRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 5 || rsp.ActionTypeID != 1 || rsp.Status != 0 || !rsp.HasDataset {
		t.Fatalf("%+v", rsp)
	}
	got, err := rsp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNEventReportRQGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000006e000000000002001a000000312e322e3834302e31303030382e352e312e342e312e312e3200000000010200000000010000100102000000070000000008020000000100000000101c000000312e322e3339322e3230303033362e393131362e322e362e312e343800000210020000000200")
	rq, err := DecodeNEventReportRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.EventTypeID != 2 || !rq.HasDataset {
		t.Fatalf("%+v", rq)
	}
	got, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNEventReportRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "0000000004000000560000000000020008000000312e322e342e313000000001020000000081000020010200000005000000000802000000010000000009020000000000000000100c000000312e322e342e352e372e380000000210020000000200")
	rsp, err := DecodeNEventReportRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 5 || rsp.EventTypeID != 2 || rsp.Status != 0 {
		t.Fatalf("%+v", rsp)
	}
	got, err := rsp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}
