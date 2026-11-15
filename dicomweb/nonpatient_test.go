package dicomweb_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// colorPalette is a Non-Patient Instance: it has no Study, Series or Patient, and
// its identifying attributes are its own. Storing one is the smallest proof that
// the service is not the Studies Service with a different prefix.
func colorPalette(t *testing.T, sopUID, label string) *godicom.FileDataset {
	t.Helper()
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.ColorPaletteStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("ContentLabel"), godicom.VRCS, label))
	ds.Set(godicom.NewDataElement(godicom.MustTag("ContentDescription"), godicom.VRLO, "palette "+label))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.ColorPaletteStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, sopUID))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))

	return &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)}
}

func encoded(t *testing.T, fd *godicom.FileDataset) []byte {
	t.Helper()
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

func npiServer(t *testing.T, store dicomweb.Store, opts ...dicomweb.HandlerOption) (*dicomweb.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web", opts...))
	t.Cleanup(srv.Close)
	return &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}, srv
}

func TestNonPatientStoreRetrieveSearch(t *testing.T) {
	t.Parallel()

	client, _ := npiServer(t, dicomweb.NewMemoryStore())
	ctx := context.Background()

	hot := colorPalette(t, "1.2.3.5.1", "HOT")
	pet := colorPalette(t, "1.2.3.5.2", "PET")
	res, err := client.StoreNonPatientFiles(ctx, "color-palettes", []*godicom.FileDataset{hot, pet})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("store status = %d, want 200", res.StatusCode)
	}
	// The response reports the instances that were stored: a user agent that did
	// not choose the UID learns it here or not at all.
	if len(res.Referenced) != 2 {
		t.Fatalf("store response references %d instances, want 2", len(res.Referenced))
	}
	ref, _ := res.Referenced[0].GetString(godicom.MustTag("ReferencedSOPInstanceUID"))
	if ref != "1.2.3.5.1" {
		t.Errorf("first reference = %q, want 1.2.3.5.1 (responses are ordered by request)", ref)
	}

	raw, err := client.RetrieveNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1")
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if !bytes.Equal(raw, encoded(t, hot)) {
		t.Error("retrieved bytes differ from what was stored")
	}

	fd, err := client.RetrieveNonPatientInstanceFile(ctx, "color-palettes", "1.2.3.5.2")
	if err != nil {
		t.Fatalf("retrieve file: %v", err)
	}
	if label, _ := fd.GetString(godicom.MustTag("ContentLabel")); label != "PET" {
		t.Errorf("ContentLabel = %q, want PET", label)
	}

	// Search by an attribute the instance carries.
	matches, err := client.SearchNonPatientInstances(ctx, "color-palettes", url.Values{"ContentLabel": {"PET"}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("search returned %d matches, want 1", len(matches))
	}
	if got, _ := matches[0].GetString(godicom.MustTag("SOPInstanceUID")); got != "1.2.3.5.2" {
		t.Errorf("match = %q, want 1.2.3.5.2", got)
	}

	// No query matches everything the category holds, in UID order.
	all, err := client.SearchNonPatientInstances(ctx, "color-palettes", nil)
	if err != nil {
		t.Fatalf("search all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("search all returned %d matches, want 2", len(all))
	}
	first, _ := all[0].GetString(godicom.MustTag("SOPInstanceUID"))
	if first != "1.2.3.5.1" {
		t.Errorf("first match = %q, want 1.2.3.5.1 (results are sorted)", first)
	}

	// An attribute the instance does not have is not a match. Answering yes here
	// would return instances that do not satisfy the query.
	if none, err := client.SearchNonPatientInstances(ctx, "color-palettes", url.Values{"PatientID": {"P001"}}); err != nil || none != nil {
		t.Errorf("search on an absent attribute = %v, %v; want no matches", none, err)
	}

	// A keyword the store cannot resolve is ignored rather than treated as a
	// filter nothing satisfies — the same reading the Studies Service's search
	// takes, and the one that keeps a newer user agent's extra parameter from
	// emptying its results.
	if all, err := client.SearchNonPatientInstances(ctx, "color-palettes", url.Values{"NoSuchKeyword": {"x"}}); err != nil || len(all) != 2 {
		t.Errorf("search on an unknown keyword = %d matches, %v; want 2", len(all), err)
	}

	// The category is part of the resource: the same UID under another name is a
	// different instance, and this one was never stored.
	if _, err := client.RetrieveNonPatientInstance(ctx, "hanging-protocols", "1.2.3.5.1"); err == nil {
		t.Error("retrieved a colour palette from /hanging-protocols")
	}

	// Non-patient instances do not appear in the Studies Service.
	if studies, err := client.SearchStudies(ctx, nil); err != nil || studies != nil {
		t.Errorf("studies search = %v, %v; want empty", studies, err)
	}
}

func TestNonPatientStoreAtUID(t *testing.T) {
	t.Parallel()

	client, _ := npiServer(t, dicomweb.NewMemoryStore())
	ctx := context.Background()
	hots := encoded(t, colorPalette(t, "1.2.3.5.1", "HOT"))

	res, err := client.StoreNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1", hots)
	if err != nil {
		t.Fatalf("store at UID: %v", err)
	}
	if len(res.Referenced) != 1 {
		t.Fatalf("store at UID referenced %d instances, want 1", len(res.Referenced))
	}

	// A UID in the path is a claim about the body. Accepting the mismatch would
	// file the instance under a URL its own retrieve can never find again.
	if _, err := client.StoreNonPatientInstance(ctx, "color-palettes", "9.9.9", hots); err == nil {
		t.Fatal("stored an instance under a UID it does not have")
	}
	if _, err := client.StoreNonPatientInstance(ctx, "color-palettes", "", hots); err == nil {
		t.Error("stored an instance at a path with no UID")
	}
	if _, err := client.RetrieveNonPatientInstance(ctx, "color-palettes", "9.9.9"); err == nil {
		t.Error("the rejected instance was stored under the path UID")
	}
	if _, err := client.RetrieveNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1"); err != nil {
		t.Errorf("the rejected store damaged the existing instance: %v", err)
	}
}

// npiSpy records every instance the handler asks it for.
type npiSpy struct {
	*dicomweb.MemoryStore
	mu    sync.Mutex
	asked []string
}

func (s *npiSpy) GetNonPatientInstance(category, uid string) ([]byte, error) {
	s.mu.Lock()
	s.asked = append(s.asked, category+"/"+uid)
	s.mu.Unlock()
	return s.MemoryStore.GetNonPatientInstance(category, uid)
}

func (s *npiSpy) askedFor() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

// Every resource name of the service is a fixed word and every instance is a UID,
// so a request that is neither cannot name an instance. The store may join these
// segments onto a filesystem path — that is the ordinary way to write one — so
// the check has to happen before the call, not inside it.
func TestNonPatientRejectsSegmentsThatAreNotUIDs(t *testing.T) {
	t.Parallel()

	spy := &npiSpy{MemoryStore: dicomweb.NewMemoryStore()}
	_, srv := npiServer(t, spy)

	for _, path := range []string{
		"/color-palettes/1.2.3%204",                  // a space, not a UID
		"/color-palettes/1.2.3/4",                    // two segments where one UID belongs
		"/color-palettes/..",                         // a parent directory, if the store joins paths
		"/color-palettes/%2e%2e",                     // the same, escaped
		"/color-palettes/" + strings.Repeat("1", 65), // a UID is at most 64 characters
		"/color-palette/1.2.3",                       // not one of the six resource names
		"/color-palettes",                            // the collection has no instance to retrieve
	} {
		resp, err := srv.Client().Get(srv.URL + "/dicom-web" + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		// "/color-palettes" is the collection: Go's mux redirects the trailing-slash
		// form, and either answer is a refusal to retrieve an instance.
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, want a refusal", path)
		}
	}

	if got := spy.askedFor(); len(got) != 0 {
		t.Errorf("store was asked for %v; every one of those requests should have been refused before the call", got)
	}
}

// readOnlyStore serves retrieval and search but not store. PS3.18 12.1 makes the
// Store Transaction optional for this service, and a read-only archive is the case
// that makes it optional: the resources exist, the method does not.
type readOnlyStore struct {
	dicomweb.Store
	retriever dicomweb.NonPatientRetriever
}

func (s readOnlyStore) GetNonPatientInstance(category, uid string) ([]byte, error) {
	return s.retriever.GetNonPatientInstance(category, uid)
}

func (s readOnlyStore) SearchNonPatientInstances(category string, query url.Values) ([]*godicom.Dataset, error) {
	return s.retriever.SearchNonPatientInstances(category, query)
}

func TestNonPatientReadOnlyStoreAnswers405(t *testing.T) {
	t.Parallel()

	full := dicomweb.NewMemoryStore()
	if err := full.PutNonPatientInstance("color-palettes", "", encoded(t, colorPalette(t, "1.2.3.5.1", "HOT"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client, srv := npiServer(t, readOnlyStore{Store: full, retriever: full})
	ctx := context.Background()

	if _, err := client.RetrieveNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1"); err != nil {
		t.Fatalf("read-only retrieve: %v", err)
	}
	if _, err := client.StoreNonPatientFiles(ctx, "color-palettes", []*godicom.FileDataset{colorPalette(t, "1.2.3.5.9", "X")}); err == nil {
		t.Fatal("a store without the Store Transaction accepted a POST")
	}

	// 405 says the resource is there and the method is not, and Allow says which
	// method is. A 404 here would tell the user agent the category does not exist.
	for _, path := range []string{"/dicom-web/color-palettes", "/dicom-web/color-palettes/1.2.3.5.9"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s = %d, want 405", path, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
			t.Errorf("Allow = %q, want GET", allow)
		}
	}
}

// studiesOnly presents the Store interface alone: embedding the interface promotes
// its methods and nothing else, so this is a server that has no non-patient
// resources at all.
type studiesOnly struct {
	dicomweb.Store
}

func TestNonPatientWithoutStoreIsNotServed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(dicomweb.Handler(studiesOnly{Store: dicomweb.NewMemoryStore()}, ""))
	defer srv.Close()

	for _, path := range []string{"/color-palettes", "/color-palettes/1.2.3", "/hanging-protocols"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestNonPatientRequestBodyIsBounded(t *testing.T) {
	t.Parallel()

	raw := encoded(t, colorPalette(t, "1.2.3.5.1", "HOT"))
	// The bound is the same one STOW-RS uses: the body is buffered whole before the
	// store sees it, and the request needs no credentials. Half the instance is
	// enough to make the point without depending on how large a Part 10 happens to
	// be.
	client, _ := npiServer(t, dicomweb.NewMemoryStore(), dicomweb.WithMaxRequestBytes(int64(len(raw)/2)))
	_, err := client.StoreNonPatientInstances(context.Background(), "color-palettes", [][]byte{raw})
	if err == nil {
		t.Fatal("an oversized instance was accepted")
	}
	if !strings.Contains(err.Error(), "413") {
		t.Errorf("error = %v, want a 413", err)
	}
}

func TestNonPatientRetrieveMediaTypes(t *testing.T) {
	t.Parallel()

	client, srv := npiServer(t, dicomweb.NewMemoryStore())
	ctx := context.Background()
	if _, err := client.StoreNonPatientFiles(ctx, "color-palettes", []*godicom.FileDataset{colorPalette(t, "1.2.3.5.1", "HOT")}); err != nil {
		t.Fatalf("store: %v", err)
	}

	// application/dicom is the Required media type of this service, so it is what
	// a request without an Accept gets — a single part, not a one-part multipart.
	resp, err := srv.Client().Get(srv.URL + "/dicom-web/color-palettes/1.2.3.5.1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != dicomweb.MediaTypeDICOM {
		t.Errorf("Content-Type = %q, want %q", ct, dicomweb.MediaTypeDICOM)
	}

	// A user agent that wants the framing the Studies Service uses gets it.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/dicom-web/color-palettes/1.2.3.5.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", dicomweb.MediaTypeMultipart)
	mresp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, mresp.Body)
	_ = mresp.Body.Close()
	if ct := mresp.Header.Get("Content-Type"); !strings.Contains(ct, dicomweb.MediaTypeMultipart) {
		t.Errorf("Content-Type = %q, want multipart/related", ct)
	}
}

// The list is handed out as a copy because it decides which paths the server
// answers: a caller that sorted or truncated it in place would silently remove a
// resource from the service.
func TestNonPatientCategoriesAreAStableList(t *testing.T) {
	t.Parallel()

	want := []string{
		"color-palettes",
		"defined-procedure-protocols",
		"hanging-protocols",
		"implant-templates",
		"inventories",
		"protocol-approvals",
	}
	got := dicomweb.NonPatientCategories()
	if !slices.Equal(got, want) {
		t.Fatalf("NonPatientCategories() = %v, want %v", got, want)
	}
	// Rewrite through a truncated view of it, which is how a caller that wanted
	// one category would touch the slice it was handed.
	short := got[:1]
	short[0] = "mangled"
	if again := dicomweb.NonPatientCategories(); !slices.Equal(again, want) {
		t.Errorf("after the caller rewrote its slice, NonPatientCategories() = %v, want %v", again, want)
	}
}

// failingNPIStore fails the way a real store does: with an error describing what
// it is made of. The handler answers 500 with a fixed reason; the cause goes to
// the log, because the response goes to whoever connected.
type failingNPIStore struct {
	dicomweb.Store
}

func (s failingNPIStore) GetNonPatientInstance(category, uid string) ([]byte, error) {
	return nil, errors.New("open /srv/secret/palettes/" + uid + ": permission denied")
}

func (s failingNPIStore) SearchNonPatientInstances(category string, query url.Values) ([]*godicom.Dataset, error) {
	return nil, errors.New("SELECT * FROM internal.npi failed")
}

func TestNonPatientStoreFailureIs500WithoutTheCause(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(dicomweb.Handler(failingNPIStore{}, "/dicom-web"))
	defer srv.Close()

	for _, tc := range []struct {
		path    string
		secrets []string
	}{
		{"/dicom-web/color-palettes/1.2.3", []string{"/srv/secret", "permission denied"}},
		{"/dicom-web/color-palettes", []string{"SELECT", "internal.npi"}},
	} {
		resp, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500", tc.path, resp.StatusCode)
		}
		for _, secret := range tc.secrets {
			if strings.Contains(string(body), secret) {
				t.Errorf("GET %s echoed %q in the body: %s", tc.path, secret, body)
			}
		}
	}
}

// A client with no base URL has nowhere to send anything, and each of these calls
// has to say so before it builds a request — the same stance ErrInvalidPath takes
// for a bad segment.
func TestNonPatientClientWithoutBaseURL(t *testing.T) {
	t.Parallel()

	client := &dicomweb.Client{}
	ctx := context.Background()
	raw := encoded(t, colorPalette(t, "1.2.3.5.1", "HOT"))
	file := colorPalette(t, "1.2.3.5.1", "HOT")

	calls := map[string]func() error{
		"RetrieveNonPatientInstance": func() error {
			_, err := client.RetrieveNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1")
			return err
		},
		"RetrieveNonPatientInstanceFile": func() error {
			_, err := client.RetrieveNonPatientInstanceFile(ctx, "color-palettes", "1.2.3.5.1")
			return err
		},
		"SearchNonPatientInstances": func() error {
			_, err := client.SearchNonPatientInstances(ctx, "color-palettes", nil)
			return err
		},
		"StoreNonPatientInstances": func() error {
			_, err := client.StoreNonPatientInstances(ctx, "color-palettes", [][]byte{raw})
			return err
		},
		"StoreNonPatientInstance": func() error {
			_, err := client.StoreNonPatientInstance(ctx, "color-palettes", "1.2.3.5.1", raw)
			return err
		},
		"StoreNonPatientFiles": func() error {
			_, err := client.StoreNonPatientFiles(ctx, "color-palettes", []*godicom.FileDataset{file})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s with no BaseURL: no error", name)
		}
	}
}

// The request asks for a single part, which is the Required media type of this
// service. An origin server that wraps one instance in multipart anyway is still
// answering the question — the client reads what it got, so that a server which is
// only slightly different is not the one it cannot talk to.
func TestNonPatientRetrieveAcceptsMultipartWrapper(t *testing.T) {
	t.Parallel()

	raw := encoded(t, colorPalette(t, "1.2.3.5.1", "HOT"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/color-palettes/1.2.3.5.1" {
			http.NotFound(w, r)
			return
		}
		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type",
			fmt.Sprintf(`multipart/related; type="%s"; boundary=%s`, dicomweb.MediaTypeDICOM, mw.Boundary()))
		part := make(textproto.MIMEHeader)
		part.Set("Content-Type", dicomweb.MediaTypeDICOM)
		pw, err := mw.CreatePart(part)
		if err != nil {
			return
		}
		_, _ = pw.Write(raw)
		_ = mw.Close()
	}))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	got, err := client.RetrieveNonPatientInstance(context.Background(), "color-palettes", "1.2.3.5.1")
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Error("the multipart part was not unwrapped")
	}
}
