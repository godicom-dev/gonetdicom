package ae_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// datasetFile writes one encoded dataset to disk and says where it starts.
//
// preamble is how many bytes to put in front of it: 132 is a Part 10 preamble
// with no file meta group, which is the shape a FileSource is aimed at.
func datasetFile(t *testing.T, sopClass, sopInstance string, pixelBytes, preamble int) (path string, offset, size int64, raw []byte) {
	t.Helper()

	pixels := make([]byte, pixelBytes)
	for i := range pixels {
		pixels[i] = byte(i*13 + 5)
	}
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, sopClass))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pixels))

	raw, err := ds.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatalf("encode dataset: %v", err)
	}
	content := append(make([]byte, preamble), raw...)
	path = filepath.Join(t.TempDir(), "instance.dcm")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path, int64(preamble), int64(len(raw)), raw
}

// The point of StoreSource: a dataset that is on disk and stays there. The
// receiver has to get the file's bytes, in order, at a size that never had to be
// resident on the sending side — and the C-STORE has to be an ordinary one, with
// the same fragmentation and the same status.
func TestCStoreFromFileSource(t *testing.T) {
	t.Parallel()

	path, offset, size, raw := datasetFile(t, secondaryCaptureSOPClass, "1.2.3.4.5.10", 1<<20, 0)
	want := sha256.Sum256(raw)
	stored := make(chan [32]byte, 1)

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "FILESCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			h := sha256.New()
			if _, err := io.Copy(h, ev.Stream()); err != nil {
				t.Errorf("handler read: %v", err)
				return dcmstatus.ProcessingFailure
			}
			var sum [32]byte
			copy(sum[:], h.Sum(nil))
			stored <- sum
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "FILESCP")
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.10",
		Source:                 ae.FileSource{Path: path, Offset: offset, Size: size},
	})
	if err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	select {
	case got := <-stored:
		if got != want {
			t.Fatal("the receiver's bytes are not the file's bytes")
		}
	default:
		t.Fatal("the store never reached the handler")
	}
}

// The archive case, concretely: the dataset begins after the preamble and the
// file meta group, and neither is sent. Nothing trims the file first.
func TestCStoreFromFileSourceAtOffset(t *testing.T) {
	t.Parallel()

	const preamble = 132
	path, offset, size, raw := datasetFile(t, secondaryCaptureSOPClass, "1.2.3.4.5.11", 64<<10, preamble)
	want := sha256.Sum256(raw)
	got := make(chan int, 1)

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "OFFSETSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			b, err := ev.Bytes()
			if err != nil {
				t.Errorf("handler read: %v", err)
				return dcmstatus.ProcessingFailure
			}
			if sha256.Sum256(b) != want {
				t.Errorf("received %d bytes that are not the dataset at offset %d", len(b), offset)
			}
			got <- len(b)
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "OFFSETSCP")
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.11",
		Source:                 ae.FileSource{Path: path, Offset: offset, Size: size},
	})
	if err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if n := <-got; n != len(raw) {
		t.Fatalf("received %d bytes, dataset is %d", n, len(raw))
	}
}

// A source that cannot be opened fails the store with nothing on the wire, so the
// association is still a usable one. That is the difference between a missing
// instance and a broken connection, and it is why Open happens before the command
// set is written.
func TestStoreSourceOpenErrorLeavesAssociationUsable(t *testing.T) {
	t.Parallel()

	path, _, size, raw := datasetFile(t, secondaryCaptureSOPClass, "1.2.3.4.5.12", 32<<10, 0)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "MISSINGSCP",
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore:                 func(context.Context, ae.StoreEvent) uint16 { return dimse.StatusSuccess },
	})
	assoc := dialStoreSCU(t, addr, "MISSINGSCP")

	_, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.12",
		Source:                 ae.FileSource{Path: filepath.Join(t.TempDir(), "gone.dcm"), Size: size},
	})
	if err == nil {
		t.Fatal("CStore from a missing file succeeded")
	}
	if !strings.Contains(err.Error(), "open C-STORE source") {
		t.Fatalf("err=%v, want an open failure", err)
	}

	// The same association, a store that can be opened: the failure above left
	// nothing behind.
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.12",
		Source:                 ae.FileSource{Path: path, Size: int64(len(raw))},
	})
	if err != nil {
		t.Fatalf("CStore after a failed open: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
}

// A dataset that ends before its declared length cannot be completed on the
// wire — the PDV lengths were already promised — so it is an error the caller
// sees rather than a short instance the peer stores as a whole one.
func TestStoreSourceShortReadIsAnError(t *testing.T) {
	t.Parallel()

	path, _, size, _ := datasetFile(t, secondaryCaptureSOPClass, "1.2.3.4.5.13", 32<<10, 0)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SHORTSCP",
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore:                 func(context.Context, ae.StoreEvent) uint16 { return dimse.StatusSuccess },
	})
	assoc := dialStoreSCU(t, addr, "SHORTSCP")

	_, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.13",
		Source:                 ae.FileSource{Path: path, Size: size * 2},
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}
}

// Source is the last resort, not an alternative: a request that already carries
// bytes is sent from those, and the source is never opened. Opening it anyway
// would be a second read of the same instance and, for a file that was deleted
// after encoding, a store that fails for no reason.
func TestStoreSourceIgnoredWhenBytesArePresent(t *testing.T) {
	t.Parallel()

	raw := bigStoreDataset(t, 1024)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "PRECEDENCESCP",
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore:                 func(context.Context, ae.StoreEvent) uint16 { return dimse.StatusSuccess },
	})
	assoc := dialStoreSCU(t, addr, "PRECEDENCESCP")

	src := ae.SourceFunc(func() (io.ReadCloser, int64, error) {
		t.Error("the source was opened even though Dataset was set")
		return nil, 0, errors.New("no")
	})
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Dataset:                raw,
		Source:                 src,
	})
	if err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
}

// A source that declares no length is a C-STORE with no dataset, and that is
// refused before the request goes out rather than sent and answered for.
func TestStoreSourceDeclaringNothingIsRefused(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "EMPTYSCP",
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore:                 func(context.Context, ae.StoreEvent) uint16 { return dimse.StatusSuccess },
	})
	assoc := dialStoreSCU(t, addr, "EMPTYSCP")

	_, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.14",
		Source: ae.SourceFunc(func() (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(nil)), 0, nil
		}),
	})
	if err == nil {
		t.Fatal("a source that declares 0 bytes was accepted")
	}
	if !strings.Contains(err.Error(), "declares 0 bytes") {
		t.Fatalf("err=%v, want a size complaint", err)
	}
}

// The framework performs a retrieve's stores, so a Source has to work from a
// handler as well: the C-GET SCP reads the instance off disk while it writes it
// to the association it is serving.
func TestCGetStreamsStoreFromFileSource(t *testing.T) {
	t.Parallel()

	const sopInstance = "1.2.3.4.5.6.20"
	path, _, size, raw := datasetFile(t, string(uid.CTImageStorage), sopInstance, 256<<10, 0)
	want := sha256.Sum256(raw)

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil) {
				return
			}
			store := ae.StoreRequest{
				AffectedSOPClassUID:    string(uid.CTImageStorage),
				AffectedSOPInstanceUID: sopInstance,
				Source:                 ae.FileSource{Path: path, Size: size},
			}
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	})

	var gotSum [32]byte
	matches := cget(t, assoc, func(_ context.Context, ev ae.StoreEvent) uint16 {
		b, err := ev.Bytes()
		if err != nil {
			t.Errorf("read store: %v", err)
			return dcmstatus.ProcessingFailure
		}
		gotSum = sha256.Sum256(b)
		return dimse.StatusSuccess
	})

	if gotSum != want {
		t.Fatal("the retrieved bytes are not the file's bytes")
	}
	final := lastMatch(t, matches)
	if final.Status != dimse.StatusSuccess {
		t.Fatalf("final status 0x%04x", final.Status)
	}
	if final.SubOperations.Completed != 1 || final.SubOperations.Failed != 0 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
}

// And the same through C-MOVE, where the store runs on a second association the
// SCP dialled: the dataset is read from disk as the destination association
// takes it.
func TestCMoveStreamsStoreFromFileSource(t *testing.T) {
	t.Parallel()

	const sopInstance = "1.2.3.4.5.7.20"
	path, _, size, raw := datasetFile(t, string(uid.CTImageStorage), sopInstance, 256<<10, 0)
	want := sha256.Sum256(raw)

	var gotSum [32]byte
	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1, Contexts: moveContexts()}}, nil) {
				return
			}
			store := ae.StoreRequest{
				AffectedSOPClassUID:    string(uid.CTImageStorage),
				AffectedSOPInstanceUID: sopInstance,
				Source:                 ae.FileSource{Path: path, Size: size},
			}
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	}, func(_ context.Context, ev ae.StoreEvent) uint16 {
		b, err := ev.Bytes()
		if err != nil {
			t.Errorf("read store: %v", err)
			return dcmstatus.ProcessingFailure
		}
		gotSum = sha256.Sum256(b)
		return dimse.StatusSuccess
	}, "")

	final := finalMatch(t, cmove(t, h))
	if final.Status != dimse.StatusSuccess {
		t.Fatalf("final status 0x%04x", final.Status)
	}
	if final.SubOperations.Completed != 1 || final.SubOperations.Failed != 0 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
	if gotSum != want {
		t.Fatal("the moved bytes are not the file's bytes")
	}
}
