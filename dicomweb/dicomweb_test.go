package dicomweb_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

func sampleFile(t *testing.T, sopUID, seriesUID string) *godicom.FileDataset {
	t.Helper()
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, seriesUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P001"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDate"), godicom.VRDA, "20260101"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "CT"))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, sopUID))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))

	return &godicom.FileDataset{
		Dataset:  ds,
		FileMeta: meta,
		Preamble: make([]byte, 128),
	}
}

func TestSTOWWADOQIDORoundtrip(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	files := []*godicom.FileDataset{
		sampleFile(t, "1.2.3.4.5", "1.2.3.4"),
		sampleFile(t, "1.2.3.4.6", "1.2.3.4"),
		sampleFile(t, "1.2.3.5.1", "1.2.3.5"),
	}
	res, err := client.StoreFiles(ctx, "", files)
	if err != nil {
		t.Fatalf("STOW: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("STOW status=%d", res.StatusCode)
	}

	raw, err := client.RetrieveInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("WADO instance: %v", err)
	}
	got, err := godicom.ReadBytes(raw, nil)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	name, _ := got.GetString(godicom.MustTag("PatientName"))
	if name != "DOE^JOHN" {
		t.Fatalf("PatientName=%q", name)
	}

	seriesParts, err := client.RetrieveSeries(ctx, "1.2.3", "1.2.3.4")
	if err != nil {
		t.Fatalf("WADO series: %v", err)
	}
	if len(seriesParts) != 2 {
		t.Fatalf("series parts=%d", len(seriesParts))
	}

	studyParts, err := client.RetrieveStudy(ctx, "1.2.3")
	if err != nil {
		t.Fatalf("WADO study: %v", err)
	}
	if len(studyParts) != 3 {
		t.Fatalf("study parts=%d", len(studyParts))
	}

	meta, err := client.RetrieveInstanceMetadata(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if _, ok := meta.Get(godicom.MustTag("PixelData")); ok {
		t.Fatal("metadata should not include PixelData")
	}

	studyMeta, err := client.RetrieveStudyMetadata(ctx, "1.2.3")
	if err != nil {
		t.Fatalf("study metadata: %v", err)
	}
	if len(studyMeta) != 3 {
		t.Fatalf("study metadata=%d", len(studyMeta))
	}

	matches, err := client.SearchStudies(ctx, url.Values{"PatientID": {"P001"}})
	if err != nil {
		t.Fatalf("QIDO studies: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("QIDO studies=%d", len(matches))
	}

	series, err := client.SearchSeries(ctx, "1.2.3", url.Values{"Modality": {"CT"}})
	if err != nil {
		t.Fatalf("QIDO series: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("QIDO series=%d", len(series))
	}

	instances, err := client.SearchInstances(ctx, "1.2.3", "1.2.3.4", nil)
	if err != nil {
		t.Fatalf("QIDO instances: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("QIDO instances=%d", len(instances))
	}

	allInst, err := client.SearchInstances(ctx, "1.2.3", "", nil)
	if err != nil {
		t.Fatalf("QIDO study instances: %v", err)
	}
	if len(allInst) != 3 {
		t.Fatalf("QIDO study instances=%d", len(allInst))
	}
}
