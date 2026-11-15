package dicomweb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

// qidoStore seeds three studies whose UIDs sort in the order they are listed, so
// a test can say which one offset=1 means.
func qidoStore(t *testing.T) *httptest.Server {
	t.Helper()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	t.Cleanup(srv.Close)

	patients := []struct{ study, name, date string }{
		{"1.2.3.1", "DOE^JOHN", "20260101"},
		{"1.2.3.2", "DOE^JANE", "20260202"},
		{"1.2.3.3", "SMITH^ANNA", "20260303"},
	}
	var files []*godicom.FileDataset
	for i, p := range patients {
		ds := godicom.NewDataset()
		ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
		ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, p.study+".1"))
		ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, p.study))
		ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, p.study+".9"))
		ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P00"+string(rune('1'+i))))
		ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, p.name))
		ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDate"), godicom.VRDA, p.date))
		ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDescription"), godicom.VRLO, "CHEST "+p.study))
		ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "CT"))

		meta := godicom.NewFileMetaDataset()
		meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
		meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, p.study+".1"))
		meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
		meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))
		files = append(files, &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)})
	}

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	if _, err := client.StoreFiles(context.Background(), "", files); err != nil {
		t.Fatalf("seed STOW: %v", err)
	}
	return srv
}

func qidoClient(t *testing.T, srv *httptest.Server) *dicomweb.Client {
	t.Helper()
	return &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
}

func studies(t *testing.T, srv *httptest.Server, query url.Values) []string {
	t.Helper()
	got, err := qidoClient(t, srv).SearchStudies(context.Background(), query)
	if err != nil {
		t.Fatalf("SearchStudies %v: %v", query, err)
	}
	var out []string
	for _, ds := range got {
		v, _ := ds.GetString(godicom.MustTag("StudyInstanceUID"))
		out = append(out, v)
	}
	return out
}

// limit and offset are about the answer, not about matching: they trim an
// ordered result set, and the order has to be the same between two requests or
// offset means nothing.
func TestQIDOLimitAndOffset(t *testing.T) {
	t.Parallel()

	srv := qidoStore(t)

	if got := studies(t, srv, url.Values{"limit": {"2"}}); len(got) != 2 || got[0] != "1.2.3.1" || got[1] != "1.2.3.2" {
		t.Fatalf("limit=2: %v", got)
	}
	if got := studies(t, srv, url.Values{"limit": {"1"}, "offset": {"1"}}); len(got) != 1 || got[0] != "1.2.3.2" {
		t.Fatalf("limit=1 offset=1: %v", got)
	}
	// Past the end is an empty answer, which the server writes as 204 rather
	// than as a body with no matches.
	if got := studies(t, srv, url.Values{"offset": {"9"}}); len(got) != 0 {
		t.Fatalf("offset=9: %v", got)
	}
	// offset without limit is "everything after the first two".
	if got := studies(t, srv, url.Values{"offset": {"2"}}); len(got) != 1 || got[0] != "1.2.3.3" {
		t.Fatalf("offset=2: %v", got)
	}
}

// fuzzymatching turns the free-text comparisons into a case-insensitive
// substring search; the other VRs stay exact, because "2026" is not a fuzzy
// 20260101.
func TestQIDOFuzzyMatching(t *testing.T) {
	t.Parallel()

	srv := qidoStore(t)

	if got := studies(t, srv, url.Values{"PatientName": {"DOE"}}); len(got) != 0 {
		t.Fatalf("exact matching accepted a partial name: %v", got)
	}
	if got := studies(t, srv, url.Values{"PatientName": {"doe"}, "fuzzymatching": {"true"}}); len(got) != 2 {
		t.Fatalf("fuzzymatching did not match both DOES: %v", got)
	}
	if got := studies(t, srv, url.Values{"StudyDate": {"2026"}, "fuzzymatching": {"true"}}); len(got) != 0 {
		t.Fatalf("a date matched fuzzily: %v", got)
	}
	if got := studies(t, srv, url.Values{"StudyDate": {"20260202"}, "fuzzymatching": {"true"}}); len(got) != 1 {
		t.Fatalf("exact date with fuzzymatching on: %v", got)
	}
}

// includefield is how a requestor asks for attributes the level's projection
// does not carry; "all" returns the stored dataset as it is.
func TestQIDOIncludeField(t *testing.T) {
	t.Parallel()

	srv := qidoStore(t)

	got, err := qidoClient(t, srv).SearchStudies(context.Background(), url.Values{"limit": {"1"}})
	if err != nil {
		t.Fatalf("SearchStudies: %v", err)
	}
	if _, ok := got[0].Get(godicom.MustTag("StudyDescription")); ok {
		t.Fatal("the default projection carried StudyDescription")
	}

	got, err = qidoClient(t, srv).SearchStudies(context.Background(), url.Values{
		"limit":        {"1"},
		"includefield": {"StudyDescription"},
	})
	if err != nil {
		t.Fatalf("SearchStudies includefield: %v", err)
	}
	if desc, ok := got[0].GetString(godicom.MustTag("StudyDescription")); !ok || desc != "CHEST 1.2.3.1" {
		t.Fatalf("StudyDescription=%q ok=%v", desc, ok)
	}

	// "all" is a keyword rather than an attribute name, and it may be spelled
	// in a list beside a real one.
	got, err = qidoClient(t, srv).SearchStudies(context.Background(), url.Values{
		"limit":        {"1"},
		"includefield": {"all"},
	})
	if err != nil {
		t.Fatalf("SearchStudies includefield=all: %v", err)
	}
	if _, ok := got[0].Get(godicom.MustTag("SOPInstanceUID")); !ok {
		t.Fatal("includefield=all did not return the stored dataset")
	}
}

// A parameter the store cannot honour is the requestor's mistake: 400, not 500
// and not a silently different answer.
func TestQIDOBadParameterIsBadRequest(t *testing.T) {
	t.Parallel()

	srv := qidoStore(t)
	for _, q := range []string{
		"limit=ten",
		"offset=-1",
		"fuzzymatching=maybe",
		"includefield=NotAnAttribute",
	} {
		resp, err := http.Get(srv.URL + "/dicom-web/studies?" + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", q, resp.StatusCode)
		}
	}
}
