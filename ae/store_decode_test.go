package ae

import (
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// StoreEvent.Decode must surface undecodable stores as a normal error (issue
// #52): the old inbound path decoded eagerly and swallowed failures, leaving
// handlers with a silent nil dataset.
func TestStoreEventDecodeReportsErrors(t *testing.T) {
	t.Parallel()

	const scUID = string(uid.SecondaryCaptureImageStorage)

	// Bytes that are not a deflated bitstream: Decode must fail on the
	// inflate step. (godicom stops quietly at truncated plain elements, but
	// a corrupt deflated store is a hard error.)
	junk := []byte{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef}

	t.Run("corrupt deflated dataset", func(t *testing.T) {
		ev := newInboundStoreEvent(&dimse.CStoreRQ{
			MessageID:              1,
			AffectedSOPClassUID:    scUID,
			AffectedSOPInstanceUID: "1.2.3.4.5",
		}, junk, string(uid.DeflatedExplicitVRLittleEndian))
		if _, err := ev.Decode(); err == nil {
			t.Fatal("expected an error for a corrupt deflated dataset")
		}
	})

	t.Run("empty dataset", func(t *testing.T) {
		ev := newInboundStoreEvent(&dimse.CStoreRQ{
			MessageID:              1,
			AffectedSOPClassUID:    scUID,
			AffectedSOPInstanceUID: "1.2.3.4.5",
		}, nil, pdu.ImplicitVRLittleEndian)
		if _, err := ev.Decode(); err == nil {
			t.Fatal("expected an error for an empty dataset")
		}
	})

	t.Run("no negotiated transfer syntax", func(t *testing.T) {
		ev := newInboundStoreEvent(&dimse.CStoreRQ{
			MessageID:              1,
			AffectedSOPClassUID:    scUID,
			AffectedSOPInstanceUID: "1.2.3.4.5",
		}, junk, "")
		if _, err := ev.Decode(); err == nil {
			t.Fatal("expected an error without a transfer syntax")
		}
	})
}

func TestStoreEventDecodeRoundTrips(t *testing.T) {
	t.Parallel()

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.SecondaryCaptureImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "Test1001"))
	raw, err := ds.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatal(err)
	}

	ev := newInboundStoreEvent(&dimse.CStoreRQ{
		MessageID:              1,
		AffectedSOPClassUID:    string(uid.SecondaryCaptureImageStorage),
		AffectedSOPInstanceUID: "1.2.3.4.5",
	}, raw, pdu.ImplicitVRLittleEndian)

	got, err := ev.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if pid, _ := got.GetString(godicom.MustTag("PatientID")); pid != "Test1001" {
		t.Fatalf("PatientID=%q", pid)
	}
	if ev.FileMeta == nil {
		t.Fatal("FileMeta is nil")
	}
	if ts, _ := ev.FileMeta.GetString(godicom.MustTag("TransferSyntaxUID")); ts != pdu.ImplicitVRLittleEndian {
		t.Fatalf("file meta TransferSyntaxUID=%q", ts)
	}
}
