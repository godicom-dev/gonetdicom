package ae_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestServeCStoreSCP(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var got atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				if req.AffectedSOPInstanceUID == "1.2.3.4.5" && len(req.Dataset) > 0 && req.Data != nil && req.FileMeta != nil {
					got.Add(1)
				}
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "STORESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	res, err := assoc.CStore(dialCtx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("handler calls: %d", got.Load())
	}
	cancel()
	<-errCh
}

func TestServeCStoreSCPAcceptAllStorage(t *testing.T) {
	t.Parallel()

	const ctSOPClass = "1.2.840.10008.5.1.4.1.1.2"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var got atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				if req.AffectedSOPClassUID == ctSOPClass && len(req.Dataset) > 0 && req.Data != nil &&
					req.TransferSyntax == pdu.ImplicitVRLittleEndian {
					got.Add(1)
				}
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ctSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "STORESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	res, err := assoc.CStore(dialCtx, ae.StoreRequest{
		AffectedSOPClassUID:    ctSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.ct",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("handler calls: %d", got.Load())
	}
	cancel()
	<-errCh
}

func TestServeCStoreSCPWildcardAbstractSyntax(t *testing.T) {
	t.Parallel()

	const privateSOP = "1.2.3.4.5.999"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{"*"},
			OnCStore: func(_ context.Context, _ ae.StoreRequest) uint16 {
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: privateSOP, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "STORESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	res, err := assoc.CStore(dialCtx, ae.StoreRequest{
		AffectedSOPClassUID:    privateSOP,
		AffectedSOPInstanceUID: "1.2.3.4.5.priv",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	_ = assoc.Release(dialCtx)
	cancel()
	<-errCh
}

func TestAllStorageSOPClassesIncludesCT(t *testing.T) {
	t.Parallel()
	const ct = "1.2.840.10008.5.1.4.1.1.2"
	for _, uid := range ae.AllStorageSOPClasses {
		if uid == ct {
			return
		}
	}
	t.Fatalf("CT Image Storage %s missing from AllStorageSOPClasses (len=%d)", ct, len(ae.AllStorageSOPClasses))
}
