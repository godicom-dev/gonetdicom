package ae_test

import (
	"context"
	"iter"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// BenchmarkCMoveStoreAssociations measures what a C-MOVE costs when the handler
// streams: the stores are produced and sent one at a time, so the association,
// the sub-operation and the response are the whole of the work.
func BenchmarkCMoveStoreAssociations(b *testing.B) {
	benchmarkCMoveStores(b, 16)
}

func benchmarkCMoveStores(b *testing.B, nStores int) {
	b.Helper()
	ctUID := string(uid.CTImageStorage)

	storeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer storeLn.Close()

	moveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer moveLn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- ae.Serve(ctx, storeLn, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{ctUID},
			OnCStore: func(_ context.Context, _ ae.StoreEvent) uint16 {
				time.Sleep(2 * time.Millisecond)
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
				"STORESCP": {Addr: storeLn.Addr().String()},
			},
			OnCMove: func(_ context.Context, _ ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
				stores := make([]ae.StoreRequest, nStores)
				for i := range stores {
					ds := godicom.NewDataset()
					instanceUID := "1.2.9." + strconv.Itoa(i)
					ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
					ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, instanceUID))
					stores[i] = ae.StoreRequest{
						AffectedSOPClassUID:    ctUID,
						AffectedSOPInstanceUID: instanceUID,
						Data:                   ds,
					}
				}
				return ae.PlannedMoveOps(ae.MovePlan{Stores: stores})
			},
		})
	}()

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
		assoc, err := ae.Dial(dialCtx, ae.Config{
			AETitle: "MOVESCU",
			PresentationContexts: []ae.PresentationContext{{
				ID:               1,
				AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelMove,
				TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
			}},
		}, moveLn.Addr().String(), "MOVESCP")
		if err != nil {
			dialCancel()
			b.Fatal(err)
		}
		_, err = assoc.CMove(dialCtx, ae.MoveRequest{
			QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
			MoveDestination: "STORESCP",
			IdentifierData:  query,
		})
		if err != nil {
			_ = assoc.Release(dialCtx)
			dialCancel()
			b.Fatal(err)
		}
		_ = assoc.Release(dialCtx)
		dialCancel()
	}
	b.StopTimer()
	cancel()
	<-errCh
	<-errCh
}
