package dicomweb_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/encaps"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

func mono8File(t *testing.T) *godicom.FileDataset {
	t.Helper()
	const rows, cols = 8, 8
	pix := make([]byte, rows*cols)
	for i := range pix {
		pix[i] = byte(i * 4)
	}

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.SecondaryCaptureImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, "1.2.3.4"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Rows"), godicom.VRUS, uint16(rows)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Columns"), godicom.VRUS, uint16(cols)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SamplesPerPixel"), godicom.VRUS, uint16(1)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("BitsAllocated"), godicom.VRUS, uint16(8)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("BitsStored"), godicom.VRUS, uint16(8)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("HighBit"), godicom.VRUS, uint16(7)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelRepresentation"), godicom.VRUS, uint16(0)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PhotometricInterpretation"), godicom.VRCS, "MONOCHROME2"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pix))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.SecondaryCaptureImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))

	return &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)}
}

func TestRenderedAndBulkData(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	fd := mono8File(t)
	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{fd}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	mt, body, err := client.RetrieveRenderedInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5", dicomweb.RenderOptions{
		MediaType: dicomweb.MediaTypeJPEG,
		Quality:   85,
	})
	if err != nil {
		t.Fatalf("rendered: %v", err)
	}
	if !bytes.Contains([]byte(mt), []byte("jpeg")) && mt != dicomweb.MediaTypeJPEG {
		t.Fatalf("media type %q", mt)
	}
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("size %dx%d", b.Dx(), b.Dy())
	}
	_ = image.Image(img)

	bulk, err := client.RetrieveBulkData(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("bulkdata: %v", err)
	}
	if len(bulk) != 64 {
		t.Fatalf("bulk len=%d", len(bulk))
	}
	if bulk[0] != 0 || bulk[63] != byte(63*4) {
		t.Fatalf("bulk content mismatch: first=%d last=%d", bulk[0], bulk[63])
	}

	_, pngBody, err := client.RetrieveRenderedInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5", dicomweb.RenderOptions{
		MediaType: dicomweb.MediaTypePNG,
	})
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	if len(pngBody) < 8 || string(pngBody[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("not a PNG")
	}
}

// PS3.18 HTTP contract tests for rendered / bulkdata (not pixel-pipeline coverage —
// that lives in godicom / pydicom).
func TestRenderedBulkDataHTTPErrors(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{mono8File(t)}); err != nil {
		t.Fatalf("STOW: %v", err)
	}
	// Instance with no PixelData (metadata-only).
	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{sampleFile(t, "1.2.3.4.9", "1.2.3.4")}); err != nil {
		t.Fatalf("STOW meta: %v", err)
	}

	tests := []struct {
		name string
		do   func(t *testing.T)
	}{
		{
			name: "rendered missing instance is error",
			do: func(t *testing.T) {
				_, _, err := client.RetrieveRenderedInstance(ctx, "1.2.3", "1.2.3.4", "9.9.9", dicomweb.RenderOptions{})
				if err == nil {
					t.Fatal("expected error")
				}
			},
		},
		{
			name: "bulkdata missing instance is error",
			do: func(t *testing.T) {
				_, err := client.RetrieveBulkData(ctx, "1.2.3", "1.2.3.4", "9.9.9")
				if err == nil {
					t.Fatal("expected error")
				}
			},
		},
		{
			name: "bulkdata without PixelData is error",
			do: func(t *testing.T) {
				_, err := client.RetrieveBulkData(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.9")
				if err == nil {
					t.Fatal("expected error")
				}
			},
		},
		{
			name: "rendered frame out of range is 406",
			do: func(t *testing.T) {
				url := srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5/rendered?frame=99"
				resp, err := srv.Client().Get(url)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusNotAcceptable {
					t.Fatalf("status=%d", resp.StatusCode)
				}
			},
		},
		{
			name: "rendered missing instance is 404",
			do: func(t *testing.T) {
				url := srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/9.9.9/rendered"
				resp, err := srv.Client().Get(url)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusNotFound {
					t.Fatalf("status=%d", resp.StatusCode)
				}
			},
		},
		{
			name: "bulkdata missing instance is 404",
			do: func(t *testing.T) {
				url := srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/9.9.9/bulkdata"
				resp, err := srv.Client().Get(url)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusNotFound {
					t.Fatalf("status=%d", resp.StatusCode)
				}
			},
		},
		{
			name: "client rejects empty UIDs",
			do: func(t *testing.T) {
				if _, _, err := client.RetrieveRenderedInstance(ctx, "", "a", "b", dicomweb.RenderOptions{}); err == nil {
					t.Fatal("expected rendered UID error")
				}
				if _, err := client.RetrieveBulkData(ctx, "a", "", "b"); err == nil {
					t.Fatal("expected bulkdata UID error")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.do(t)
		})
	}
}

func TestRenderInstanceUnitErrors(t *testing.T) {
	t.Parallel()

	part10, err := mono8File(t).EncodeFile(nil)
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}

	tests := []struct {
		name string
		opts dicomweb.RenderOptions
	}{
		{name: "unsupported media type", opts: dicomweb.RenderOptions{MediaType: "image/gif"}},
		{name: "frame out of range", opts: dicomweb.RenderOptions{Frame: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := dicomweb.RenderInstance(part10, tt.opts)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}

	metaOnly, err := sampleFile(t, "1.2.3.4.8", "1.2.3.4").EncodeFile(nil)
	if err != nil {
		t.Fatalf("EncodeFile meta: %v", err)
	}
	if _, err := dicomweb.ExtractPixelBulkData(metaOnly); err == nil {
		t.Fatal("expected missing PixelData error")
	}
	if _, err := dicomweb.ExtractPixelBulkData([]byte("not-dicom")); err == nil {
		t.Fatal("expected bad Part 10 error")
	}
}

func TestRetrieveFrames(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	fd := mono8File(t)
	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{fd}); err != nil {
		t.Fatalf("STOW: %v", err)
	}
	frames, err := client.RetrieveFrames(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5", []int{1})
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	if len(frames) != 1 || len(frames[0]) != 64 {
		n := 0
		if len(frames) > 0 {
			n = len(frames[0])
		}
		t.Fatalf("native frames=%d len0=%d", len(frames), n)
	}
	if frames[0][0] != 0 || frames[0][63] != byte(63*4) {
		t.Fatalf("native content first=%d last=%d", frames[0][0], frames[0][63])
	}

	compressed := compressedFile(t)
	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{compressed}); err != nil {
		t.Fatalf("STOW compressed: %v", err)
	}
	got, err := client.RetrieveFrames(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.7", []int{2, 1})
	if err != nil {
		t.Fatalf("compressed frames: %v", err)
	}
	wantA := []byte{0xFF, 0xD8, 0x11, 0xD9}
	wantB := []byte{0xFF, 0xD8, 0x22, 0xD9}
	if len(got) != 2 || !bytes.Equal(got[0], wantB) || !bytes.Equal(got[1], wantA) {
		t.Fatalf("compressed frames = %x", got)
	}

	resp, err := srv.Client().Get(srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5/frames/1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "multipart/related") || !strings.Contains(ct, "application/octet-stream") {
		t.Fatalf("content-type %q", ct)
	}
	if !strings.Contains(ct, "transfer-syntax="+string(uid.ExplicitVRLittleEndian)) {
		t.Fatalf("content-type missing transfer syntax: %q", ct)
	}

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5/frames/2", http.StatusNotAcceptable},
		{"/dicom-web/studies/1.2.3/series/1.2.3.4/instances/9.9.9/frames/1", http.StatusNotFound},
		{"/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5/frames/0", http.StatusBadRequest},
	} {
		r, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Fatalf("%s status=%d want %d", tc.path, r.StatusCode, tc.want)
		}
	}
}

func compressedFile(t *testing.T) *godicom.FileDataset {
	t.Helper()
	enc, err := encaps.Encapsulate([][]byte{
		{0xFF, 0xD8, 0x11, 0xD9},
		{0xFF, 0xD8, 0x22, 0xD9},
	}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	fd := mono8File(t)
	fd.Dataset.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.7"))
	fd.Dataset.Set(godicom.NewDataElement(godicom.MustTag("NumberOfFrames"), godicom.VRIS, "2"))
	fd.Dataset.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, enc))
	fd.FileMeta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, "1.2.3.4.7"))
	fd.FileMeta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.JPEGBaseline8Bit)))
	return fd
}
