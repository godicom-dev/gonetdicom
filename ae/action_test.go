package ae_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestStorageCommitmentPushRoundtrip(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotAction atomic.Bool
	var gotEvent atomic.Uint32

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "COMMITSCU",
			AcceptedAbstractSyntaxes: []string{
				ae.StorageCommitmentPushModelSOPClass,
			},
			OnNAction: func(_ context.Context, req ae.ActionRequest) (ae.ActionResult, *ae.EventReportRequest) {
				gotAction.Store(true)
				if req.ActionTypeID != dimse.StorageCommitmentActionTypeRequest {
					t.Errorf("ActionTypeID=%d", req.ActionTypeID)
				}
				if req.RequestedSOPInstanceUID != ae.StorageCommitmentPushModelSOPInstance {
					t.Errorf("instance=%q", req.RequestedSOPInstanceUID)
				}
				return ae.ActionResult{
						Status:                 dimse.StatusSuccess,
						ActionTypeID:           req.ActionTypeID,
						AffectedSOPClassUID:    req.RequestedSOPClassUID,
						AffectedSOPInstanceUID: req.RequestedSOPInstanceUID,
					}, &ae.EventReportRequest{
						AffectedSOPClassUID:    req.RequestedSOPClassUID,
						AffectedSOPInstanceUID: req.RequestedSOPInstanceUID,
						EventTypeID:            dimse.StorageCommitmentEventTypeSuccess,
						EventInformationData:   req.ActionInformationData,
					}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "COMMITSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.StorageCommitmentPushModelSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "COMMITSCU")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	info := godicom.NewDataset()
	info.Set(godicom.NewDataElement(godicom.MustTag("TransactionUID"), godicom.VRUI, "1.2.3.4.5"))

	res, err := assoc.NAction(dialCtx, ae.ActionRequest{
		RequestedSOPClassUID:    ae.StorageCommitmentPushModelSOPClass,
		RequestedSOPInstanceUID: ae.StorageCommitmentPushModelSOPInstance,
		ActionTypeID:            dimse.StorageCommitmentActionTypeRequest,
		ActionInformationData:   info,
		OnNEventReport: func(_ context.Context, req ae.EventReportRequest) uint16 {
			gotEvent.Store(uint32(req.EventTypeID))
			if req.EventInformationData == nil {
				t.Error("missing event information")
			}
			return dimse.StatusSuccess
		},
	})
	if err != nil {
		t.Fatalf("N-ACTION: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status=0x%04x", res.Status)
	}
	if !gotAction.Load() {
		t.Fatal("OnNAction not called")
	}
	if gotEvent.Load() != uint32(dimse.StorageCommitmentEventTypeSuccess) {
		t.Fatalf("event type=%d", gotEvent.Load())
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
