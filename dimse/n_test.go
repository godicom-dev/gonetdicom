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

func TestNGetRQGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "000000000400000078000000000003001a000000312e322e3834302e31303030382e352e312e342e312e312e3200000000010200000010010000100102000000070000000008020000000101000001101c000000312e322e3339322e3230303033362e393131362e322e362e312e3438000005100c000000e07f100000000000ffffffff")
	rq, err := DecodeNGetRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || len(rq.AttributeIdentifierList) != 3 {
		t.Fatalf("%+v", rq)
	}
	if rq.AttributeIdentifierList[0] != Tag(0x7fe00010) {
		t.Fatalf("tags: %+v", rq.AttributeIdentifierList)
	}
	got, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNGetRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000004c0000000000020008000000312e322e342e313000000001020000001081000020010200000005000000000802000000010000000009020000000000000000100c000000312e322e342e352e372e3800")
	rsp, err := DecodeNGetRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.MessageIDBeingRespondedTo != 5 || !rsp.HasDataset || rsp.Status != 0 {
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

func TestNSetRQGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "000000000400000064000000000003001a000000312e322e3834302e31303030382e352e312e342e312e312e3200000000010200000020010000100102000000070000000008020000000100000001101c000000312e322e3339322e3230303033362e393131362e322e362e312e3438")
	rq, err := DecodeNSetRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || !rq.HasDataset {
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

func TestNSetRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000004c0000000000020008000000312e322e342e313000000001020000002081000020010200000005000000000802000000010000000009020000000000000000100c000000312e322e342e352e372e3800")
	rsp, err := DecodeNSetRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rsp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNCreateRQGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "000000000400000064000000000002001a000000312e322e3834302e31303030382e352e312e342e312e312e3200000000010200000040010000100102000000070000000008020000000100000000101c000000312e322e3339322e3230303033362e393131362e322e362e312e3438")
	rq, err := DecodeNCreateRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || !rq.HasDataset || rq.AffectedSOPInstanceUID == "" {
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

func TestNCreateRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000004c0000000000020008000000312e322e342e313000000001020000004081000020010200000005000000000802000000010000000009020000000000000000100c000000312e322e342e352e372e3800")
	rsp, err := DecodeNCreateRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rsp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch\ngot  %x\nwant %x", got, want)
	}
}

func TestNDeleteRQGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000003a0000000000030006000000312e322e33000000000102000000500100001001020000000700000000080200000001010000011006000000312e322e3330")
	rq, err := DecodeNDeleteRQ(want)
	if err != nil {
		t.Fatal(err)
	}
	if rq.MessageID != 7 || rq.RequestedSOPClassUID != "1.2.3" || rq.RequestedSOPInstanceUID != "1.2.30" {
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

func TestNDeleteRSPGolden(t *testing.T) {
	t.Parallel()
	want := mustHex(t, "00000000040000004c0000000000020008000000312e322e342e3130000000010200000050810000200102000000050000000008020000000101000000090200000001c2000000100c000000312e322e342e352e372e3800")
	rsp, err := DecodeNDeleteRSP(want)
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Status != 0xC201 {
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
