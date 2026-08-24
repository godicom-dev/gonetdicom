package dicomweb_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// A UID the client was handed by something else — a worklist entry, a query
// result, a form field — used to be pasted straight into the request path. Slash
// is not escaped by url.URL, so this one addressed a different instance's
// metadata, and neither side had any reason to complain.
func TestHostileUIDNeverReachesTheWire(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", dicomweb.MediaTypeDICOMJSON)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	const hostile = "1.2.3.4.5/../../../../studies/9.9.9/series/9.9.9/instances/9.9.9/metadata"
	if _, err := client.RetrieveInstanceMetadata(ctx, "1.2.3", "1.2.3.4", hostile); !errors.Is(err, dicomweb.ErrInvalidPath) {
		t.Errorf("RetrieveInstanceMetadata: %v, want ErrInvalidPath", err)
	}
	if _, err := client.RetrieveInstance(ctx, "1.2.3", "1.2.3.4/../9.9.9", "1.2.3.4.5"); !errors.Is(err, dicomweb.ErrInvalidPath) {
		t.Errorf("RetrieveInstance: %v, want ErrInvalidPath", err)
	}
	if _, err := client.SearchSeries(ctx, "1.2.3/../../admin", nil); !errors.Is(err, dicomweb.ErrInvalidPath) {
		t.Errorf("SearchSeries: %v, want ErrInvalidPath", err)
	}
	if _, err := client.StoreInstances(ctx, "1.2.3/../9.9.9", [][]byte{[]byte("junk")}); !errors.Is(err, dicomweb.ErrInvalidPath) {
		t.Errorf("StoreInstances: %v, want ErrInvalidPath", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 0 {
		t.Errorf("requests reached the server: %v", seen)
	}
}

// errStore is a Store whose PutInstance fails the way a real one does: with an
// error describing the server's own insides.
type errStore struct {
	*dicomweb.MemoryStore
	err error
}

func (s errStore) PutInstance(studyUID string, part10 []byte) error { return s.err }

// Handler answers whoever connects to it, and a Store is third-party code whose
// errors name what it is made of. The handler used to hand those strings back as
// the response body, so a failed STOW told an anonymous client the server's
// filesystem layout.
func TestHandlerDoesNotEchoStoreErrors(t *testing.T) {
	t.Parallel()

	const secret = "open /var/lib/pacs/pgpass: permission denied"
	store := errStore{MemoryStore: dicomweb.NewMemoryStore(), err: errors.New(secret)}

	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	inner := dicomweb.Handler(store, "/dicom-web")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(gonetdicom.WithLogger(r.Context(), logger)))
	}))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	_, err := client.StoreFiles(context.Background(), "", []*godicom.FileDataset{sampleFile(t, "1.2.3.4.5", "1.2.3.4")})
	if err == nil {
		t.Fatal("STOW succeeded against a failing store")
	}
	// The client's error carries the response body verbatim, which is the point:
	// whatever is in there is what the requestor was told.
	if strings.Contains(err.Error(), "/var/lib/pacs") || strings.Contains(err.Error(), "permission denied") {
		t.Errorf("response repeated the store's error: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("STOW error lost the status: %v", err)
	}

	// The operator still gets the detail — on the server, where it belongs.
	if got := logs.String(); !strings.Contains(got, secret) {
		t.Errorf("store error was not logged; log was:\n%s", got)
	}
}

// A STOW-RS body is buffered whole and then copied into the Store, so before this
// bound existed one unauthenticated POST decided how much memory the process
// used.
func TestHandlerBoundsRequestBody(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web", dicomweb.WithMaxRequestBytes(4<<10)))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	_, err := client.StoreInstances(ctx, "", [][]byte{bytes.Repeat([]byte("d"), 64<<10)})
	if err == nil {
		t.Fatal("64 KiB body accepted under a 4 KiB bound")
	}
	if !strings.Contains(err.Error(), "413") {
		t.Errorf("STOW error = %v, want 413", err)
	}

	// A body that does not announce its length has to be caught as it arrives, not
	// by the Content-Length check: chunked is one header away for any client.
	var raw bytes.Buffer
	mw := multipart.NewWriter(&raw)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", dicomweb.MediaTypeDICOM)
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(bytes.Repeat([]byte("d"), 64<<10)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	// Hiding the reader's type keeps net/http from setting Content-Length.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/dicom-web/studies",
		struct{ io.Reader }{&raw})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", fmt.Sprintf(`%s; type=%q; boundary=%s`,
		dicomweb.MediaTypeMultipart, dicomweb.MediaTypeDICOM, mw.Boundary()))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("chunked STOW: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked 64 KiB body: status %d, want %d",
			resp.StatusCode, http.StatusRequestEntityTooLarge)
	}

	// The bound must not be in the way of an ordinary instance.
	res, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{sampleFile(t, "1.2.3.4.5", "1.2.3.4")})
	if err != nil {
		t.Fatalf("STOW of one small instance: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("STOW status = %d", res.StatusCode)
	}
}

// The client side of the same problem: a response body is read into memory whole,
// so a peer that answers with a wrong Content-Length — or simply keeps writing —
// used to be able to end the process.
func TestClientBoundsResponseBody(t *testing.T) {
	t.Parallel()

	const huge = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/metadata") {
			w.Header().Set("Content-Type", dicomweb.MediaTypeDICOMJSON)
			_, _ = w.Write(bytes.Repeat([]byte("j"), huge))
			return
		}
		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", fmt.Sprintf(`%s; type=%q; boundary=%s`,
			dicomweb.MediaTypeMultipart, dicomweb.MediaTypeDICOM, mw.Boundary()))
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", dicomweb.MediaTypeDICOM)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return
		}
		_, _ = pw.Write(bytes.Repeat([]byte("d"), huge))
		_ = mw.Close()
	}))
	defer srv.Close()

	client := &dicomweb.Client{
		BaseURL:          srv.URL + "/dicom-web",
		HTTPClient:       srv.Client(),
		MaxResponseBytes: 4 << 10,
	}
	ctx := context.Background()

	if _, err := client.RetrieveInstanceMetadata(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5"); !errors.Is(err, dicomweb.ErrTooLarge) {
		t.Errorf("metadata: %v, want ErrTooLarge", err)
	}
	// The multipart path reads through the same bound, one part at a time.
	if _, err := client.RetrieveInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5"); !errors.Is(err, dicomweb.ErrTooLarge) {
		t.Errorf("instance: %v, want ErrTooLarge", err)
	}
	if _, err := client.RetrieveBulkData(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5"); !errors.Is(err, dicomweb.ErrTooLarge) {
		t.Errorf("bulkdata: %v, want ErrTooLarge", err)
	}
}

// lockedBuffer is a bytes.Buffer safe to write from the server goroutine and read
// from the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ io.Writer = (*lockedBuffer)(nil)
