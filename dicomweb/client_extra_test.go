package dicomweb_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// The file-returning helpers are the convenience layer an application actually
// calls: a store that writes to disk wants a FileDataset, not bytes plus a
// second ReadBytes call it may forget to make.
func TestClientRetrieveFileAndSeriesMetadata(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	files := []*godicom.FileDataset{
		sampleFile(t, "1.2.3.4.5", "1.2.3.4"),
		sampleFile(t, "1.2.3.4.6", "1.2.3.4"),
	}
	if _, err := client.StoreFiles(ctx, "", files); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	fd, err := client.RetrieveInstanceFile(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("RetrieveInstanceFile: %v", err)
	}
	if fd.FileMeta == nil {
		t.Fatal("file dataset has no file meta")
	}
	if name, _ := fd.GetString(godicom.MustTag("PatientName")); name != "DOE^JOHN" {
		t.Fatalf("PatientName = %q", name)
	}

	metas, err := client.RetrieveSeriesMetadata(ctx, "1.2.3", "1.2.3.4")
	if err != nil {
		t.Fatalf("RetrieveSeriesMetadata: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("series metadata = %d datasets, want 2", len(metas))
	}
	for _, meta := range metas {
		if _, ok := meta.Get(godicom.MustTag("PixelData")); ok {
			t.Error("metadata carries PixelData")
		}
	}
}

// A missing instance is reported as a plain HTTP 404, not as a decode failure:
// the error has to say "not there" so a caller can distinguish it from a broken
// response.
func TestClientRetrieveMissingInstance(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()
	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}

	if _, err := client.RetrieveInstance(context.Background(), "1.2.3", "1.2.3.4", "9.9.9"); err == nil {
		t.Fatal("a missing instance produced no error")
	}
}

// A requestor that asks for application/dicom gets a single part, not a
// multipart body. The server has to honour Accept; a client that only ever
// sees multipart never notices until it talks to a stricter peer.
func TestServerSinglePartWhenAcceptIsDICOM(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	if _, err := client.StoreFiles(context.Background(), "", []*godicom.FileDataset{sampleFile(t, "1.2.3.4.5", "1.2.3.4")}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/dicom-web/studies/1.2.3/series/1.2.3.4/instances/1.2.3.4.5", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", dicomweb.MediaTypeDICOM)
	rsp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", rsp.StatusCode)
	}
	if got := rsp.Header.Get("Content-Type"); got != dicomweb.MediaTypeDICOM {
		t.Fatalf("Content-Type = %q, want %q", got, dicomweb.MediaTypeDICOM)
	}
	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := godicom.ReadBytes(body, nil); err != nil {
		t.Fatalf("single-part body is not a Part 10 file: %v", err)
	}
}

// QIDO with no matches is 204 No Content, and the client has to read that as an
// empty result rather than an error.
func TestQIDONoMatchesIsEmptyNotAnError(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()
	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}

	matches, err := client.SearchStudies(context.Background(), nil)
	if err != nil {
		t.Fatalf("SearchStudies: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %d, want 0", len(matches))
	}
}

func TestNewClientOptionPlumbing(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	c, err := dicomweb.NewClient("https://pacs.example/dicom-web",
		dicomweb.WithTimeout(5*time.Second),
		dicomweb.WithTLSConfig(tlsCfg),
		dicomweb.WithMaxResponseBytes(4096),
		dicomweb.WithLogger(log),
		nil, // a nil option is skipped, not a panic
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v", c.Timeout)
	}
	if c.TLS != tlsCfg {
		t.Error("TLS config was not applied")
	}
	if c.MaxResponseBytes != 4096 {
		t.Errorf("MaxResponseBytes = %d", c.MaxResponseBytes)
	}
	if c.HTTPClient == nil || c.HTTPClient.Timeout != 5*time.Second {
		t.Errorf("default HTTP client timeout = %v", c.HTTPClient)
	}

	if _, err := dicomweb.NewClient("https://x", dicomweb.WithTimeout(0)); err == nil {
		t.Error("a non-positive timeout was accepted")
	}
}

// A Client built by hand (no HTTPClient) must still work: the fallback is the
// shared default transport, and a caller that only sets Timeout should not get
// a nil-pointer or a client that never times out.
func TestClientFallbackHTTPClient(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	seed := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	if _, err := seed.StoreFiles(context.Background(), "", []*godicom.FileDataset{sampleFile(t, "1.2.3.4.5", "1.2.3.4")}); err != nil {
		t.Fatalf("STOW: %v", err)
	}

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", Timeout: 5 * time.Second}
	parts, err := client.RetrieveStudy(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("RetrieveStudy: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
}
