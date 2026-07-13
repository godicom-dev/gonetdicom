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

func sampleFile(t *testing.T) *godicom.FileDataset {
	t.Helper()
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, "1.2.3.4"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P001"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDate"), godicom.VRDA, "20260101"))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
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

	fd := sampleFile(t)
	res, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{fd})
	if err != nil {
		t.Fatalf("STOW: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("STOW status=%d", res.StatusCode)
	}

	raw, err := client.RetrieveInstance(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("WADO: %v", err)
	}
	got, err := godicom.ReadBytes(raw, nil)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	name, _ := got.GetString(godicom.MustTag("PatientName"))
	if name != "DOE^JOHN" {
		t.Fatalf("PatientName=%q", name)
	}

	meta, err := client.RetrieveInstanceMetadata(ctx, "1.2.3", "1.2.3.4", "1.2.3.4.5")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if _, ok := meta.Get(godicom.MustTag("PixelData")); ok {
		t.Fatal("metadata should not include PixelData")
	}
	pid, _ := meta.GetString(godicom.MustTag("PatientID"))
	if pid != "P001" {
		t.Fatalf("PatientID=%q", pid)
	}

	matches, err := client.SearchStudies(ctx, url.Values{"PatientID": {"P001"}})
	if err != nil {
		t.Fatalf("QIDO: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("QIDO matches=%d", len(matches))
	}
	study, _ := matches[0].GetString(godicom.MustTag("StudyInstanceUID"))
	if study != "1.2.3" {
		t.Fatalf("StudyInstanceUID=%q", study)
	}
}
