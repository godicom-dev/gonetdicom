package ae_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// bigStoreDataset builds a store payload with an n-byte element, large enough
// that it no longer fits one P-DATA fragment and has to be reassembled.
func bigStoreDataset(t *testing.T, n int) []byte {
	t.Helper()

	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, secondaryCaptureSOPClass))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, payload))
	raw, err := ds.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatalf("encode dataset: %v", err)
	}
	return raw
}

func dialStoreSCU(t *testing.T, addr, calledAE string) *ae.Association {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "STREAMSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   secondaryCaptureSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, calledAE)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = assoc.Close() })
	return assoc
}

// A dataset bigger than one P-DATA fragment must come out of Stream byte for
// byte, in order, without ever being assembled whole.
func TestStoreStreamMatchesDataset(t *testing.T) {
	t.Parallel()

	const size = 1 << 20
	raw := bigStoreDataset(t, size)
	want := sha256.Sum256(raw)

	var (
		gotSum  [32]byte
		gotSize int64
		readErr error
	)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "STREAMSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			h := sha256.New()
			n, err := io.Copy(h, ev.Stream())
			gotSize, readErr = n, err
			copy(gotSum[:], h.Sum(nil))
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "STREAMSCP")
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Dataset:                raw,
	})
	if err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if readErr != nil {
		t.Fatalf("handler read: %v", readErr)
	}
	if gotSize != int64(len(raw)) {
		t.Fatalf("streamed %d bytes, dataset is %d", gotSize, len(raw))
	}
	if gotSum != want {
		t.Fatal("streamed bytes do not match the dataset")
	}
}

// A handler that ignores the dataset must not desynchronise the association:
// the rest of the message is read and discarded, so the next request parses.
func TestStoreHandlerIgnoringDatasetKeepsStreamAligned(t *testing.T) {
	t.Parallel()

	raw := bigStoreDataset(t, 512<<10)
	calls := 0
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "IGNORESCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(context.Context, ae.StoreEvent) uint16 {
			calls++
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "IGNORESCP")
	for i := 0; i < 3; i++ {
		res, err := assoc.CStore(context.Background(), ae.StoreRequest{
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.3.4.5",
			Dataset:                raw,
		})
		if err != nil {
			t.Fatalf("CStore %d: %v", i, err)
		}
		if res.Status != dimse.StatusSuccess {
			t.Fatalf("CStore %d status 0x%04x", i, res.Status)
		}
	}
	if calls != 3 {
		t.Fatalf("handler ran %d times, want 3", calls)
	}
}

// Reading part of the dataset and returning is allowed too; the remainder is
// discarded and the association stays usable.
func TestStorePartialReadIsDiscarded(t *testing.T) {
	t.Parallel()

	raw := bigStoreDataset(t, 256<<10)
	var first int
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "PARTSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			buf := make([]byte, 32)
			n, _ := io.ReadFull(ev.Stream(), buf)
			first = n
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "PARTSCP")
	for i := 0; i < 2; i++ {
		if _, err := assoc.CStore(context.Background(), ae.StoreRequest{
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.3.4.5",
			Dataset:                raw,
		}); err != nil {
			t.Fatalf("CStore %d: %v", i, err)
		}
	}
	if first != 32 {
		t.Fatalf("partial read returned %d bytes, want 32", first)
	}
	if len(raw) == 0 {
		t.Fatal("empty dataset")
	}
}

// Bytes and Stream are the same data by two routes; pinning both keeps the
// convenience path from drifting away from the streaming one.
func TestStoreBytesMatchesStream(t *testing.T) {
	t.Parallel()

	raw := bigStoreDataset(t, 64<<10)
	var streamed, whole []byte
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "BOTHSCP",
		MaxDatasetSize:           1 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			if len(streamed) == 0 {
				streamed, _ = io.ReadAll(ev.Stream())
				return dimse.StatusSuccess
			}
			whole, _ = ev.Bytes()
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSCU(t, addr, "BOTHSCP")
	for i := 0; i < 2; i++ {
		if _, err := assoc.CStore(context.Background(), ae.StoreRequest{
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.3.4.5",
			Dataset:                raw,
		}); err != nil {
			t.Fatalf("CStore %d: %v", i, err)
		}
	}
	if !bytes.Equal(streamed, raw) {
		t.Fatalf("Stream gave %d bytes, dataset is %d", len(streamed), len(raw))
	}
	if !bytes.Equal(whole, raw) {
		t.Fatalf("Bytes gave %d bytes, dataset is %d", len(whole), len(raw))
	}
}

// The SCU side streams too: a C-GET that stores a large series must not
// assemble each instance before the handler sees it. This path uses the pull
// body, where Close has to finish the read itself to keep the association
// aligned.
func TestCGetStreamsStoredDataset(t *testing.T) {
	t.Parallel()

	const size = 512 << 10
	ctUID := string(uid.CTImageStorage)
	sopInstance := "1.2.840.10008.1.2.3.4.5"

	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}
	stored := godicom.NewDataset()
	stored.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
	stored.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	stored.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, payload))
	wantRaw, err := stored.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := sha256.Sum256(wantRaw)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "STREAMGET",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelGet,
				ctUID,
			},
			RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, true, true)},
			OnCGet: func(context.Context, ae.GetRequest) ae.GetPlan {
				ds := stored
				return ae.GetPlan{
					Stores: []ae.StoreRequest{{
						AffectedSOPClassUID:    ctUID,
						AffectedSOPInstanceUID: sopInstance,
						Data:                   ds,
					}},
					Responses: []ae.RetrieveMatch{{
						Status: dimse.StatusSuccess,
						SubOperations: dimse.SubOperations{
							Completed: 1, Present: true,
						},
					}},
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STREAMSCU",
		PresentationContexts: []ae.PresentationContext{
			{
				ID:               1,
				AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelGet,
				TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
			},
			{
				ID:               3,
				AbstractSyntax:   ctUID,
				TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
			},
		},
		RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, false, true)},
	}, ln.Addr().String(), "STREAMGET")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	query.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))

	var (
		gotSum [32]byte
		gotLen int64
	)
	matches, err := assoc.CGet(dialCtx, ae.GetRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
		IdentifierData: query,
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			h := sha256.New()
			n, err := io.Copy(h, ev.Stream())
			if err != nil {
				t.Errorf("copy: %v", err)
			}
			gotLen = n
			copy(gotSum[:], h.Sum(nil))
			return dimse.StatusSuccess
		},
	})
	if err != nil {
		t.Fatalf("CGet: %v", err)
	}
	if len(matches) == 0 || matches[len(matches)-1].Status != dimse.StatusSuccess {
		t.Fatalf("matches: %+v", matches)
	}
	if gotLen != int64(len(wantRaw)) {
		t.Fatalf("streamed %d bytes, dataset is %d", gotLen, len(wantRaw))
	}
	if gotSum != want {
		t.Fatal("streamed bytes do not match the stored dataset")
	}
}
