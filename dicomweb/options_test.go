package dicomweb_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

func TestNewClientOptions(t *testing.T) {
	t.Parallel()

	store := dicomweb.NewMemoryStore()
	srv := httptest.NewServer(dicomweb.Handler(store, "/dicom-web"))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client, err := dicomweb.NewClient(srv.URL+"/dicom-web",
		dicomweb.WithTimeout(5*time.Second),
		dicomweb.WithLogger(logger),
		dicomweb.WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SeriesInstanceUID"), godicom.VRUI, "1.2.3.4"))
	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, string(uid.CTImageStorage)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, string(uid.ExplicitVRLittleEndian)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.1"))
	fd := &godicom.FileDataset{Dataset: ds, FileMeta: meta, Preamble: make([]byte, 128)}

	if _, err := client.StoreFiles(context.Background(), "", []*godicom.FileDataset{fd}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("dicomweb: request")) {
		t.Fatalf("missing request log: %s", logBuf.String())
	}
}
