package ae

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestStoreRequestSaveAsWritesFileMetaTransferSyntax(t *testing.T) {
	t.Parallel()

	const scUID = "1.2.840.10008.5.1.4.1.1.7"
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, scUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("NumberOfFrames"), godicom.VRIS, "2"))
	raw, err := ds.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatal(err)
	}

	req := newInboundStoreRequest(&dimse.CStoreRQ{
		MessageID:              1,
		AffectedSOPClassUID:    scUID,
		AffectedSOPInstanceUID: "1.2.3.4.5",
	}, raw, pdu.ImplicitVRLittleEndian)
	if req.Data == nil || req.File == nil {
		t.Fatalf("expected Data and File, got Data=%v File=%v", req.Data != nil, req.File != nil)
	}

	out := filepath.Join(t.TempDir(), "out.dcm")
	if err := req.SaveAs(out); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	fd, err := godicom.ReadFile(out, nil)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	ts, ok := fd.FileMeta.GetString(godicom.MustTag("TransferSyntaxUID"))
	if !ok || ts != pdu.ImplicitVRLittleEndian {
		t.Fatalf("TransferSyntaxUID=%q", ts)
	}
	nf, _ := fd.GetString(godicom.MustTag("NumberOfFrames"))
	if nf != "2" {
		t.Fatalf("NumberOfFrames=%q", nf)
	}
}

func TestStoreRequestSaveAsPreservesRLEMultiFrame(t *testing.T) {
	t.Parallel()

	src := filepath.Join("..", "godicom", "pydicom", "src", "pydicom", "data", "test_files", "SC_rgb_rle_2frame.dcm")
	orig, err := godicom.ReadFile(src, nil)
	if err != nil {
		t.Skipf("RLE fixture unavailable: %v", err)
	}
	ts, ok := orig.FileMeta.GetString(godicom.MustTag("TransferSyntaxUID"))
	if !ok || ts == "" {
		t.Fatal("missing TS")
	}
	raw, err := orig.Encode(ts)
	if err != nil {
		t.Fatal(err)
	}
	sopClass, _ := orig.GetString(godicom.MustTag("SOPClassUID"))
	sopInst, _ := orig.GetString(godicom.MustTag("SOPInstanceUID"))

	req := newInboundStoreRequest(&dimse.CStoreRQ{
		MessageID:              1,
		AffectedSOPClassUID:    sopClass,
		AffectedSOPInstanceUID: sopInst,
	}, raw, ts)

	out := filepath.Join(t.TempDir(), "rle.dcm")
	if err := req.SaveAs(out); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	got, err := godicom.ReadFile(out, nil)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	gotTS, _ := got.FileMeta.GetString(godicom.MustTag("TransferSyntaxUID"))
	if gotTS != ts {
		t.Fatalf("TS %q want %q", gotTS, ts)
	}
	nf, _ := got.GetString(godicom.MustTag("NumberOfFrames"))
	if nf != "2" {
		t.Fatalf("frames=%q", nf)
	}
	pdOrig, _ := orig.Get(godicom.MustTag("PixelData"))
	pdGot, _ := got.Get(godicom.MustTag("PixelData"))
	bo, _ := pdOrig.Value.([]byte)
	bg, _ := pdGot.Value.([]byte)
	if len(bo) != len(bg) || !bytes.Equal(bo, bg) || !pdGot.IsUndefinedLength {
		t.Fatalf("pixel data len %d→%d equal=%v undef=%v", len(bo), len(bg), bytes.Equal(bo, bg), pdGot.IsUndefinedLength)
	}
}
