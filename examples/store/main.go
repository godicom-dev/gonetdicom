// Command store runs a Storage SCP that writes each instance to a directory and
// sends it two instances: one from a godicom Dataset, one straight off disk.
//
//	go run ./examples/store
//
// The second one is the interesting half. StoreRequest.Source is read as the
// C-STORE-RQ goes out, so the instance never has to be resident — on the way in
// the SCP copies the fragments to the file for the same reason.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

const sopClass = "1.2.840.10008.5.1.4.1.1.7" // Secondary Capture

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dir, err := os.MkdirTemp("", "gonetdicom-store-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORE_SCP",
			AcceptedAbstractSyntaxes: []string{sopClass},
			OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
				path := filepath.Join(dir, ev.AffectedSOPInstanceUID+".dcm")
				f, err := os.Create(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "create %s: %v\n", path, err)
					return dcmstatus.OutOfResources
				}
				defer func() { _ = f.Close() }()
				// io.Copy from Stream: a 500 MB instance costs one fragment of
				// memory here, not 500 MB.
				n, err := io.Copy(f, ev.Stream())
				if err != nil {
					fmt.Fprintf(os.Stderr, "copy: %v\n", err)
					return dcmstatus.OutOfResources
				}
				fmt.Printf("stored %s (%d bytes, %s)\n", filepath.Base(path), n, ev.TransferSyntax)
				return dcmstatus.Success
			},
		})
	}()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "STORE_SCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   sopClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "STORE_SCP")
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	// 1. From a Dataset: the encoder writes the negotiated syntax.
	if _, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    sopClass,
		AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.541.1.1",
		Data:                   instance("1.2.826.0.1.3680043.10.541.1.1", 64<<10),
	}); err != nil {
		log.Fatalf("C-STORE from a Dataset: %v", err)
	}

	// 2. From a file. For a Part 10 file the dataset starts after the 132-byte
	// preamble and the File Meta Information group, so Offset is 132 plus that
	// group's length; here the file holds nothing but the dataset.
	raw := instance("1.2.826.0.1.3680043.10.541.1.2", 1<<20)
	encoded, err := raw.Encode(godicom.UID(pdu.ImplicitVRLittleEndian))
	if err != nil {
		log.Fatalf("encode: %v", err)
	}
	path := filepath.Join(dir, "source.dcm")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		log.Fatal(err)
	}
	if _, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    sopClass,
		AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.541.1.2",
		Source:                 ae.FileSource{Path: path, Size: int64(len(encoded))},
		TransferSyntax:         pdu.ImplicitVRLittleEndian,
	}); err != nil {
		log.Fatalf("C-STORE from a file: %v", err)
	}
}

// instance builds a small Secondary Capture dataset with a run of pixel data, so
// that the two paths differ in size as well as in where the bytes come from.
func instance(sopInstance string, pixelBytes int) *godicom.Dataset {
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, sopClass))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "EXAMPLE^STORE"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "OT"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Rows"), godicom.VRUS, uint16(256)))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Columns"), godicom.VRUS, uint16(256)))
	pixels := make([]byte, pixelBytes)
	for i := range pixels {
		pixels[i] = byte(i)
	}
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pixels))
	return ds
}
