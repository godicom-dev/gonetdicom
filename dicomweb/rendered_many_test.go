package dicomweb_test

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// mono8Variant is mono8File with the UIDs chosen by the caller, which is what a
// study of several renderable instances needs.
func mono8Variant(t *testing.T, sopUID, seriesUID string) *godicom.FileDataset {
	t.Helper()
	fd := mono8File(t)
	fd.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopUID))
	fd.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, seriesUID))
	fd.FileMeta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, sopUID))
	return fd
}

func TestRenderedStudyAndSeries(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{
		mono8Variant(t, "1.2.3.4.5.1", "1.2.3.4.1"), // series 1
		mono8Variant(t, "1.2.3.4.5.2", "1.2.3.4.1"), // series 1
		mono8Variant(t, "1.2.3.4.5.3", "1.2.3.4.2"), // series 2
		// A Structured Report in the same study: nothing to render, and a study
		// retrieve must not fail because of it.
		sampleFile(t, "1.2.3.4.5.4", "1.2.3.4.3"),
	}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	mediaType, parts, err := client.RetrieveRenderedStudy(ctx, "1.2.3", dicomweb.RenderOptions{MediaType: dicomweb.MediaTypePNG})
	if err != nil {
		t.Fatalf("study rendered: %v", err)
	}
	if mediaType != dicomweb.MediaTypePNG {
		t.Fatalf("media type %q", mediaType)
	}
	if len(parts) != 3 {
		t.Fatalf("study parts = %d, want the 3 images and not the report", len(parts))
	}
	for i, part := range parts {
		img, err := png.Decode(bytes.NewReader(part))
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
			t.Fatalf("part %d size %dx%d", i, b.Dx(), b.Dy())
		}
	}

	mediaType, parts, err = client.RetrieveRenderedSeries(ctx, "1.2.3", "1.2.3.4.1", dicomweb.RenderOptions{MediaType: dicomweb.MediaTypePNG})
	if err != nil {
		t.Fatalf("series rendered: %v", err)
	}
	if mediaType != dicomweb.MediaTypePNG {
		t.Fatalf("series media type %q", mediaType)
	}
	if len(parts) != 2 {
		t.Fatalf("series parts = %d, want 2", len(parts))
	}

	// JPEG is the default media type, so asking for nothing gets JPEG.
	mediaType, parts, err = client.RetrieveRenderedSeries(ctx, "1.2.3", "1.2.3.4.2", dicomweb.RenderOptions{})
	if err != nil {
		t.Fatalf("series rendered jpeg: %v", err)
	}
	if mediaType != dicomweb.MediaTypeJPEG {
		t.Fatalf("default media type %q", mediaType)
	}
	if len(parts) != 1 || len(parts[0]) == 0 {
		t.Fatalf("series parts = %d", len(parts))
	}
}

func TestRenderedManyContentTypeAndNothingToRender(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{
		mono8Variant(t, "1.2.3.4.5.1", "1.2.3.4.1"),
		// Study 9 holds only an instance with no Pixel Data.
	}); err != nil {
		t.Fatalf("STOW: %v", err)
	}
	report := sampleFile(t, "9.9.9.1", "9.9.9.2")
	report.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "9.9.9"))
	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{report}); err != nil {
		t.Fatalf("STOW report: %v", err)
	}

	resp, err := srv.Client().Get(srv.URL + "/dicom-web/studies/1.2.3/rendered")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	// The multipart's type parameter is what tells a reader the parts are
	// images, and which kind, without opening them.
	if !strings.HasPrefix(ct, dicomweb.MediaTypeMultipart) || !strings.Contains(ct, `type="`+dicomweb.MediaTypeJPEG+`"`) {
		t.Fatalf("Content-Type %q", ct)
	}

	// Study 9 has one instance and it is a report: nothing to render.
	resp9, err := srv.Client().Get(srv.URL + "/dicom-web/studies/9.9.9/rendered")
	if err != nil {
		t.Fatal(err)
	}
	defer resp9.Body.Close()
	if resp9.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("status=%d, want 406", resp9.StatusCode)
	}

	// A series that does not exist is missing, not unrenderable.
	respMissing, err := srv.Client().Get(srv.URL + "/dicom-web/studies/1.2.3/series/9.9.9.9/rendered")
	if err != nil {
		t.Fatal(err)
	}
	defer respMissing.Body.Close()
	if respMissing.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", respMissing.StatusCode)
	}
}
