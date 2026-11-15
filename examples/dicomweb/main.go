// Command dicomweb runs a DICOMweb origin server over an in-memory archive and
// talks to it over HTTP.
//
//	go run ./examples/dicomweb
//
// The handler holds no data dictionary: it hands the query parameters to the
// Store, which is why the reference store is the part that knows what
// limit, offset, fuzzymatching and includefield mean. What the handler owns is
// HTTP — DICOM JSON responses, multipart parts, and the status a failed request
// deserves.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

func main() {
	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	files := []*godicom.FileDataset{
		instance(1, "DOE^JOHN", "CHEST"),
		instance(2, "DOE^JANE", "CHEST"),
		instance(3, "SMITH^ANNA", "HEAD"),
	}
	stored, err := client.StoreFiles(ctx, "", files)
	if err != nil {
		log.Fatalf("STOW: %v", err)
	}
	fmt.Printf("STOW: %s, %d instance(s)\n", http.StatusText(stored.StatusCode), len(stored.Referenced))

	// QIDO with the parameters that shape the answer. A fuzzy match on a name is
	// a case-insensitive substring, and the other VRs stay exact.
	matches, err := client.SearchStudies(ctx, url.Values{
		"PatientName":   {"doe"},
		"fuzzymatching": {"true"},
		"limit":         {"1"},
		"includefield":  {"StudyDescription"},
	})
	if err != nil {
		log.Fatalf("QIDO: %v", err)
	}
	for _, ds := range matches {
		name, _ := ds.GetString(godicom.MustTag("PatientName"))
		desc, _ := ds.GetString(godicom.MustTag("StudyDescription"))
		study, _ := ds.GetString(godicom.MustTag("StudyInstanceUID"))
		fmt.Printf("QIDO: %-12s %-6s %s\n", name, desc, study)
	}

	// WADO-RS: the instance as it was stored.
	raw, err := client.RetrieveInstance(ctx, studyUID(1), seriesUID(1), instanceUID(1))
	if err != nil {
		log.Fatalf("WADO: %v", err)
	}
	fmt.Printf("WADO: %d bytes of Part 10\n", len(raw))
}

// instance is one Secondary Capture file: one study per patient, one series, one
// instance, so the QIDO response has something to distinguish.
func studyUID(n int) string    { return fmt.Sprintf("1.2.826.0.1.3680043.10.541.5.%d", n) }
func seriesUID(n int) string   { return studyUID(n) + ".1" }
func instanceUID(n int) string { return seriesUID(n) + ".1" }

func instance(n int, patientName, description string) *godicom.FileDataset {
	study := studyUID(n)
	series := seriesUID(n)
	instanceUID := instanceUID(n)

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.SecondaryCaptureImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, instanceUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, study))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, series))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, fmt.Sprintf("P%03d", n)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, patientName))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDate"), godicom.VRDA, "20260101"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDescription"), godicom.VRLO, description))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "OT"))
	pixels := make([]byte, 32<<10)
	for i := range pixels {
		pixels[i] = byte(i)
	}
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pixels))

	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.SecondaryCaptureImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, instanceUID))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))

	return &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)}
}
