package ae_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

const printManagementSOPClass = "1.2.840.10008.5.1.1.9" // Basic Film Session (example N-* SOP)

func TestNGetSetCreateDeleteRoundtrip(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sopInstance := "1.2.3.4.5"
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "NSCP",
			AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
			OnNGet: func(_ context.Context, req ae.NGetRequest) ae.NGetResult {
				if req.RequestedSOPInstanceUID != sopInstance {
					t.Errorf("get instance=%q", req.RequestedSOPInstanceUID)
				}
				ds := godicom.NewDataset()
				ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
				return ae.NGetResult{
					Status:                 dimse.StatusSuccess,
					AffectedSOPClassUID:    req.RequestedSOPClassUID,
					AffectedSOPInstanceUID: req.RequestedSOPInstanceUID,
					AttributeListData:      ds,
				}
			},
			OnNSet: func(_ context.Context, req ae.SetRequest) ae.SetResult {
				if req.ModificationListData == nil {
					t.Error("missing modification list")
				}
				return ae.SetResult{
					Status:                 dimse.StatusSuccess,
					AffectedSOPClassUID:    req.RequestedSOPClassUID,
					AffectedSOPInstanceUID: req.RequestedSOPInstanceUID,
					AttributeListData:      req.ModificationListData,
				}
			},
			OnNCreate: func(_ context.Context, req ae.CreateRequest) ae.CreateResult {
				uid := req.AffectedSOPInstanceUID
				if uid == "" {
					uid = sopInstance
				}
				return ae.CreateResult{
					Status:                 dimse.StatusSuccess,
					AffectedSOPClassUID:    req.AffectedSOPClassUID,
					AffectedSOPInstanceUID: uid,
					AttributeListData:      req.AttributeListData,
				}
			},
			OnNDelete: func(_ context.Context, req ae.DeleteRequest) ae.DeleteResult {
				return ae.DeleteResult{
					Status:                 dimse.StatusSuccess,
					AffectedSOPClassUID:    req.RequestedSOPClassUID,
					AffectedSOPInstanceUID: req.RequestedSOPInstanceUID,
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "NSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   printManagementSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "NSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	getRes, err := assoc.NGet(dialCtx, ae.NGetRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: sopInstance,
		AttributeIdentifierList: []dimse.Tag{0x00100010},
	})
	if err != nil {
		t.Fatalf("N-GET: %v", err)
	}
	if getRes.Status != dimse.StatusSuccess || getRes.AttributeListData == nil {
		t.Fatalf("get: %+v", getRes)
	}

	mod := godicom.NewDataset()
	mod.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P001"))
	setRes, err := assoc.NSet(dialCtx, ae.SetRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: sopInstance,
		ModificationListData:    mod,
	})
	if err != nil {
		t.Fatalf("N-SET: %v", err)
	}
	if setRes.Status != dimse.StatusSuccess {
		t.Fatalf("set status 0x%04x", setRes.Status)
	}

	createRes, err := assoc.NCreate(dialCtx, ae.CreateRequest{
		AffectedSOPClassUID:    printManagementSOPClass,
		AffectedSOPInstanceUID: sopInstance,
		AttributeListData:      mod,
	})
	if err != nil {
		t.Fatalf("N-CREATE: %v", err)
	}
	if createRes.Status != dimse.StatusSuccess || createRes.AffectedSOPInstanceUID != sopInstance {
		t.Fatalf("create: %+v", createRes)
	}

	delRes, err := assoc.NDelete(dialCtx, ae.DeleteRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: sopInstance,
	})
	if err != nil {
		t.Fatalf("N-DELETE: %v", err)
	}
	if delRes.Status != dimse.StatusSuccess {
		t.Fatalf("delete status 0x%04x", delRes.Status)
	}

	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
