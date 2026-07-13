package ae_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestCMoveSCURoundtrip(t *testing.T) {
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
			AETitle: "MOVESCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelMove,
			},
			OnCMove: func(_ context.Context, req ae.MoveRequest) []ae.RetrieveMatch {
				if req.MoveDestination != "STORESCP" {
					t.Errorf("MoveDestination=%q", req.MoveDestination)
				}
				return []ae.RetrieveMatch{
					{
						Status: dimse.StatusPending,
						SubOperations: dimse.SubOperations{
							Remaining: 1, Completed: 0, Present: true,
						},
					},
					{
						Status: dimse.StatusSuccess,
						SubOperations: dimse.SubOperations{
							Remaining: 0, Completed: 1, Present: true,
						},
					},
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "MOVESCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelMove,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "MOVESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
	query.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3"))

	matches, err := assoc.CMove(dialCtx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "STORESCP",
		IdentifierData:  query,
	})
	if err != nil {
		t.Fatalf("C-MOVE: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches=%d", len(matches))
	}
	if matches[0].Status != dimse.StatusPending || matches[0].SubOperations.Remaining != 1 {
		t.Fatalf("pending: %+v", matches[0])
	}
	if matches[1].Status != dimse.StatusSuccess || matches[1].SubOperations.Completed != 1 {
		t.Fatalf("final: %+v", matches[1])
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}

func TestCGetSCURoundtrip(t *testing.T) {
	t.Parallel()

	ctUID := string(uid.CTImageStorage)
	sopInstance := "1.2.840.10008.1.2.3.4.5"

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
			AETitle: "GETSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelGet,
				ctUID,
			},
			OnCGet: func(_ context.Context, _ ae.GetRequest) ae.GetPlan {
				ds := godicom.NewDataset()
				ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
				ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
				ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
				return ae.GetPlan{
					Stores: []ae.StoreRequest{{
						AffectedSOPClassUID:    ctUID,
						AffectedSOPInstanceUID: sopInstance,
						Data:                   ds,
					}},
					Responses: []ae.RetrieveMatch{
						{
							Status: dimse.StatusPending,
							SubOperations: dimse.SubOperations{
								Remaining: 0, Completed: 1, Present: true,
							},
						},
						{
							Status: dimse.StatusSuccess,
							SubOperations: dimse.SubOperations{
								Remaining: 0, Completed: 1, Present: true,
							},
						},
					},
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "GETSCU",
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
	}, ln.Addr().String(), "GETSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	var stored atomic.Int32
	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	query.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))

	matches, err := assoc.CGet(dialCtx, ae.GetRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
		IdentifierData: query,
		OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
			stored.Add(1)
			if req.AffectedSOPInstanceUID != sopInstance {
				t.Errorf("store instance=%q", req.AffectedSOPInstanceUID)
			}
			if len(req.Dataset) == 0 {
				t.Error("empty store dataset")
			}
			return dimse.StatusSuccess
		},
	})
	if err != nil {
		t.Fatalf("C-GET: %v", err)
	}
	if stored.Load() != 1 {
		t.Fatalf("stored=%d", stored.Load())
	}
	if len(matches) != 2 {
		t.Fatalf("matches=%d", len(matches))
	}
	if matches[0].Status != dimse.StatusPending || matches[0].SubOperations.Completed != 1 {
		t.Fatalf("pending: %+v", matches[0])
	}
	if matches[1].Status != dimse.StatusSuccess {
		t.Fatalf("final status 0x%04x", matches[1].Status)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
