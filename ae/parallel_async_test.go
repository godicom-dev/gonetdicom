package ae_test

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestStorageCommitmentAsyncNewAssociation(t *testing.T) {
	t.Parallel()

	eventLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer eventLn.Close()

	scpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer scpLn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotEvent atomic.Uint32
	eventDone := make(chan struct{})
	errCh := make(chan error, 2)

	go func() {
		errCh <- ae.Serve(ctx, eventLn, ae.ServerConfig{
			AETitle: "COMMITSCU",
			AcceptedAbstractSyntaxes: []string{
				ae.StorageCommitmentPushModelSOPClass,
			},
			OnNEventReport: func(_ context.Context, req ae.EventReportRequest) uint16 {
				gotEvent.Store(uint32(req.EventTypeID))
				close(eventDone)
				return dimse.StatusSuccess
			},
		})
	}()
	go func() {
		errCh <- ae.Serve(ctx, scpLn, ae.ServerConfig{
			AETitle: "COMMITSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.StorageCommitmentPushModelSOPClass,
			},
			OnNAction: func(_ context.Context, req ae.ActionRequest) (ae.ActionResult, *ae.EventReportRequest) {
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
						AsyncDestination: &ae.EventReportDestination{
							Addr:     eventLn.Addr().String(),
							CalledAE: "COMMITSCU",
						},
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
	}, scpLn.Addr().String(), "COMMITSCP")
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
		// no OnNEventReport — report arrives on a new association
	})
	if err != nil {
		t.Fatalf("N-ACTION: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status=0x%04x", res.Status)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}

	select {
	case <-eventDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for async N-EVENT-REPORT")
	}
	if gotEvent.Load() != uint32(dimse.StorageCommitmentEventTypeSuccess) {
		t.Fatalf("event type=%d", gotEvent.Load())
	}
	cancel()
	<-errCh
	<-errCh
}

func TestCMoveParallelDestinationStores(t *testing.T) {
	t.Parallel()

	ctUID := "1.2.840.10008.5.1.4.1.1.2"
	const n = 8

	storeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer storeLn.Close()

	moveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer moveLn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stored atomic.Int32
	var peak atomic.Int32
	var inflight atomic.Int32
	errCh := make(chan error, 2)

	go func() {
		errCh <- ae.Serve(ctx, storeLn, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{ctUID},
			OnCStore: func(_ context.Context, _ ae.StoreRequest) uint16 {
				cur := inflight.Add(1)
				for {
					old := peak.Load()
					if cur <= old || peak.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond) // make concurrency observable
				inflight.Add(-1)
				stored.Add(1)
				return dimse.StatusSuccess
			},
		})
	}()
	go func() {
		errCh <- ae.Serve(ctx, moveLn, ae.ServerConfig{
			AETitle: "MOVESCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelMove,
			},
			MoveDestinations: map[string]ae.MoveDestination{
				"STORESCP": {
					Addr:            storeLn.Addr().String(),
					MaxAssociations: 4,
				},
			},
			OnCMove: func(_ context.Context, _ ae.MoveRequest) ae.MovePlan {
				stores := make([]ae.StoreRequest, n)
				for i := range stores {
					ds := godicom.NewDataset()
					uid := "1.2.3.4." + strconv.Itoa(i)
					ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
					ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, uid))
					stores[i] = ae.StoreRequest{
						AffectedSOPClassUID:    ctUID,
						AffectedSOPInstanceUID: uid,
						Data:                   ds,
					}
				}
				return ae.MovePlan{Stores: stores}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "MOVESCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelMove,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, moveLn.Addr().String(), "MOVESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	matches, err := assoc.CMove(dialCtx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "STORESCP",
		IdentifierData:  query,
	})
	if err != nil {
		t.Fatalf("C-MOVE: %v", err)
	}
	if stored.Load() != n {
		t.Fatalf("stored=%d want %d", stored.Load(), n)
	}
	if peak.Load() < 2 {
		t.Fatalf("expected concurrent stores, peak inflight=%d", peak.Load())
	}
	last := matches[len(matches)-1]
	if last.Status != dimse.StatusSuccess || last.SubOperations.Completed != n {
		t.Fatalf("final: %+v", last)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
	<-errCh
}
