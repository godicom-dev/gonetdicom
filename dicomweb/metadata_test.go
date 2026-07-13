package dicomweb_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

func sampleFileWithPixels(t *testing.T, sopUID, seriesUID string) *godicom.FileDataset {
	t.Helper()
	fd := sampleFile(t, sopUID, seriesUID)
	fd.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, []byte{0x01, 0x02, 0x03, 0x04}))
	return fd
}

func TestMetadataBulkDataURI(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	if _, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{
		sampleFileWithPixels(t, "1.2.3.4.5", "1.2.3.4"),
	}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	wantURI := dicomweb.BulkDataURI("/dicom-web", "1.2.3", "1.2.3.4", "1.2.3.4.5")
	resp, err := srv.Client().Get(srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5/metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), wantURI) {
		t.Fatalf("missing BulkDataURI %q in %s", wantURI, raw)
	}

	var docs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatal(err)
	}
	pd := string(docs[0]["7FE00010"])
	if !strings.Contains(pd, "BulkDataURI") {
		t.Fatalf("PixelData missing BulkDataURI: %s", pd)
	}
	if strings.Contains(pd, "InlineBinary") {
		t.Fatalf("PixelData must not use InlineBinary: %s", pd)
	}

	meta, err := client.RetrieveInstanceMetadata(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if _, ok := meta.Get(godicom.MustTag("PatientName")); !ok {
		t.Fatal("missing PatientName")
	}
}
