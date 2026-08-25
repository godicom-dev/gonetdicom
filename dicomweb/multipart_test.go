package dicomweb_test

import (
	"bytes"
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// delimiterBait is the delimiter line for the fixed boundary the server used to
// hard-code. An instance carrying it verbatim used to truncate its own WADO-RS
// response, silently returning short data with HTTP 200.
const delimiterBait = "\r\n--gonetdicom-boundary\r\nX: 1\r\n\r\n"

func TestWADOBoundaryNotForgeableByInstanceData(t *testing.T) {
	t.Parallel()

	fd := sampleFile(t, "1.2.3.4.5", "1.2.3.4")
	fd.Set(godicom.NewDataElement(godicom.MustTag("PatientComments"), godicom.VRLT, delimiterBait))
	stored, err := fd.EncodeFile(nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(stored, []byte(delimiterBait)) {
		t.Fatalf("bait not present in encoded instance; test is not exercising the bug")
	}

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()
	if _, err := client.StoreInstances(ctx, "", [][]byte{stored}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	got, err := client.RetrieveInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("WADO instance: %v", err)
	}
	if !bytes.Equal(stored, got) {
		t.Fatalf("instance truncated by its own content: stored %d bytes, retrieved %d bytes",
			len(stored), len(got))
	}

	parts, err := client.RetrieveSeries(ctx, "1.2.3", "1.2.3.4")
	if err != nil {
		t.Fatalf("WADO series: %v", err)
	}
	if len(parts) != 1 || !bytes.Equal(stored, parts[0]) {
		t.Fatalf("series retrieve corrupted: %d parts", len(parts))
	}
}

func TestWADOBoundaryIsPerResponse(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	if _, err := client.StoreFiles(context.Background(), "", []*godicom.FileDataset{
		sampleFile(t, "1.2.3.4.5", "1.2.3.4"),
	}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	boundary := func() string {
		t.Helper()
		url := srv.URL + "/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Accept", dicomweb.MediaTypeMultipart)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("Content-Type %q: %v", resp.Header.Get("Content-Type"), err)
		}
		if params["boundary"] == "" {
			t.Fatalf("no boundary in Content-Type %q", resp.Header.Get("Content-Type"))
		}
		return params["boundary"]
	}

	if a, b := boundary(), boundary(); a == b {
		t.Fatalf("boundary is a fixed string %q; it must be generated per response", a)
	}
}
