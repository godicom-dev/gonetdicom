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

// levelFile builds one instance somewhere in the hierarchy, so a test can put
// series in more than one study. sampleFile fixes the study, which is exactly
// the assumption the two top-level Search targets remove.
func levelFile(t *testing.T, studyUID, seriesUID, sopUID, modality string) *godicom.FileDataset {
	t.Helper()
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, studyUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, seriesUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P001"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, modality))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, sopUID))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))

	return &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)}
}

// twoStudies stores two studies: study 1.2.3 with two series, study 9.8.7 with
// one. Every assertion below is about a result that spans them, so a store or a
// handler that only ever sees one study cannot pass by accident.
func twoStudies(t *testing.T) (*dicomweb.MemoryStore, *httptest.Server, *dicomweb.Client) {
	t.Helper()
	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	t.Cleanup(srv.Close)

	files := []*godicom.FileDataset{
		levelFile(t, "1.2.3", "1.2.3.1", "1.2.3.1.1", "CT"),
		levelFile(t, "1.2.3", "1.2.3.1", "1.2.3.1.2", "CT"),
		levelFile(t, "1.2.3", "1.2.3.2", "1.2.3.2.1", "MR"),
		levelFile(t, "9.8.7", "9.8.7.1", "9.8.7.1.1", "CT"),
	}
	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	if _, err := client.StoreFiles(context.Background(), "", files); err != nil {
		t.Fatalf("STOW: %v", err)
	}
	return store, srv, client
}

func TestClientSearchesAllSeries(t *testing.T) {
	t.Parallel()
	_, _, client := twoStudies(t)

	all, err := client.SearchAllSeries(context.Background(), nil)
	if err != nil {
		t.Fatalf("SearchAllSeries: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("All Series = %d series, want 3", len(all))
	}
	// Every match has to name the study it belongs to, or the caller cannot turn
	// a series into a URL: the request URI did not carry that level.
	studies := map[string]bool{}
	for _, ds := range all {
		study, ok := ds.GetString(godicom.MustTag("StudyInstanceUID"))
		if !ok || study == "" {
			t.Fatalf("series result without StudyInstanceUID: %v", ds)
		}
		studies[study] = true
	}
	if len(studies) != 2 {
		t.Errorf("All Series reported %d studies, want 2", len(studies))
	}

	// A query key on a level the URI does not name is how one study's series is
	// asked for against /series.
	one, err := client.SearchAllSeries(context.Background(), url.Values{"StudyInstanceUID": {"9.8.7"}})
	if err != nil {
		t.Fatalf("SearchAllSeries(StudyInstanceUID): %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("All Series for one study = %d, want 1", len(one))
	}
	if got, _ := one[0].GetString(godicom.MustTag("SeriesInstanceUID")); got != "9.8.7.1" {
		t.Errorf("SeriesInstanceUID = %q, want 9.8.7.1", got)
	}
}

func TestClientSearchesAllInstances(t *testing.T) {
	t.Parallel()
	_, _, client := twoStudies(t)

	all, err := client.SearchAllInstances(context.Background(), nil)
	if err != nil {
		t.Fatalf("SearchAllInstances: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("All Instances = %d, want 4", len(all))
	}
	var prev string
	for _, ds := range all {
		sop, _ := ds.GetString(godicom.MustTag("SOPInstanceUID"))
		if sop < prev {
			t.Fatalf("instances not ordered by SOPInstanceUID: %q after %q", sop, prev)
		}
		prev = sop
	}

	mr, err := client.SearchAllInstances(context.Background(), url.Values{"Modality": {"MR"}})
	if err != nil {
		t.Fatalf("SearchAllInstances(Modality): %v", err)
	}
	if len(mr) != 1 {
		t.Fatalf("All Instances for MR = %d, want 1", len(mr))
	}

	// The series level is a query key here for the same reason the study level is
	// in All Series: the URI does not name it.
	series, err := client.SearchAllInstances(context.Background(), url.Values{"SeriesInstanceUID": {"1.2.3.1"}})
	if err != nil {
		t.Fatalf("SearchAllInstances(SeriesInstanceUID): %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("All Instances for one series = %d, want 2", len(series))
	}
}

// The top-level Search paths carry search and nothing else: PS3.18 defines the
// Store Transaction on /studies and /studies/{study}, so a POST here is a method
// that does not exist, not one this server left out.
func TestTopLevelSearchRefusesStore(t *testing.T) {
	t.Parallel()
	_, srv, _ := twoStudies(t)

	for _, path := range []string{"/dicom-web/series", "/dicom-web/instances"} {
		resp, err := srv.Client().Post(srv.URL+path, "application/dicom", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, resp.StatusCode)
		}
	}
}

// /instances is a Search target, not a level of the hierarchy: there is no
// retrieve under it, because the instance's study and series are not in the URI
// and the nested target addresses the same bytes with them in it.
func TestTopLevelInstanceRetrieveIsNotAResource(t *testing.T) {
	t.Parallel()
	_, srv, _ := twoStudies(t)

	resp, err := srv.Client().Get(srv.URL + "/dicom-web/instances/1.2.3.1.1")
	if err != nil {
		t.Fatalf("GET /instances/{instance}: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /instances/{instance} = %d, want 404", resp.StatusCode)
	}
}
