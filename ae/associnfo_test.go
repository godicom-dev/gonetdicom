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

func TestAssocFromContextAbsent(t *testing.T) {
	t.Parallel()

	if _, ok := ae.AssocFromContext(context.Background()); ok {
		t.Fatal("expected ok=false for a context without AssocInfo")
	}
}

// Every DIMSE handler runs with the association context attached: peer AE
// titles from the A-ASSOCIATE-RQ, the remote address, and per message the
// presentation context ID and its negotiated transfer syntax (issue #50).
func TestServeCStoreAssocContext(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
			OnCStore: func(hctx context.Context, ev ae.StoreEvent) uint16 {
				info, ok := ae.AssocFromContext(hctx)
				if !ok {
					t.Error("AssocFromContext: not attached")
					return dimse.StatusSuccess
				}
				if info.CallingAETitle != "STORESCU" {
					t.Errorf("CallingAETitle=%q", info.CallingAETitle)
				}
				if info.CalledAETitle != "STORESCP" {
					t.Errorf("CalledAETitle=%q", info.CalledAETitle)
				}
				if info.RemoteAddr == nil {
					t.Error("RemoteAddr is nil")
				} else if _, _, err := net.SplitHostPort(info.RemoteAddr.String()); err != nil {
					t.Errorf("RemoteAddr=%q: %v", info.RemoteAddr, err)
				}
				if info.PresentationContextID != 1 {
					t.Errorf("PresentationContextID=%d", info.PresentationContextID)
				}
				if info.TransferSyntax != pdu.ImplicitVRLittleEndian {
					t.Errorf("TransferSyntax=%q", info.TransferSyntax)
				}
				calls.Add(1)
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
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
	if calls.Load() != 1 {
		t.Fatalf("handler calls: %d", calls.Load())
	}
	cancel()
	<-errCh
}
