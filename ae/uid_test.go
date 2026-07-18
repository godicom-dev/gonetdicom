package ae_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestNewInstanceUID(t *testing.T) {
	t.Parallel()
	a := ae.NewInstanceUID()
	b := ae.NewInstanceUID()
	if a == "" || b == "" {
		t.Fatal("empty UID")
	}
	if a == b {
		t.Fatalf("expected distinct UIDs, both %q", a)
	}
	if err := uid.Validate(a); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, uid.RootUID) {
		t.Fatalf("prefix = %q, want %s…", a, uid.RootUID)
	}
}

func TestCStoreSCU_AutoInstanceUID(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mockSCUPeer(t, server)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}
	assoc, err := ae.AcceptFromConn(ctx, cfg, client, "ANY-SCP")
	if err != nil {
		t.Fatalf("associate: %v", err)
	}

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "Tube^HeNe"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "Test1101"))

	res, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID: secondaryCaptureSOPClass,
		Data:                ds,
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if res.AffectedSOPInstanceUID == "" {
		t.Fatal("expected auto-assigned instance UID")
	}
	if err := uid.Validate(res.AffectedSOPInstanceUID); err != nil {
		t.Fatal(err)
	}
	got, ok := ds.GetString(godicom.MustTag("SOPInstanceUID"))
	if !ok || got != res.AffectedSOPInstanceUID {
		t.Fatalf("dataset SOPInstanceUID = %q want %q", got, res.AffectedSOPInstanceUID)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done
}

func TestNCreate_SCPAssignsInstanceUID(t *testing.T) {
	t.Parallel()

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
			AETitle:                  "NSCP",
			AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
			OnNCreate: func(_ context.Context, req ae.CreateRequest) ae.CreateResult {
				// Leave AffectedSOPInstanceUID empty — SCP should mint one.
				return ae.CreateResult{
					Status:              dimse.StatusSuccess,
					AffectedSOPClassUID: req.AffectedSOPClassUID,
					AttributeListData:   req.AttributeListData,
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "NSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID: 1, AbstractSyntax: printManagementSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "NSCP")
	if err != nil {
		t.Fatal(err)
	}
	defer assoc.Abort()

	res, err := assoc.NCreate(dialCtx, ae.CreateRequest{
		AffectedSOPClassUID: printManagementSOPClass,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if res.AffectedSOPInstanceUID == "" {
		t.Fatal("SCP did not assign instance UID")
	}
	if err := uid.Validate(res.AffectedSOPInstanceUID); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-errCh
}
