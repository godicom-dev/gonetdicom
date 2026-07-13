package ae_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// BenchmarkCMoveStoreAssociations compares sequential vs parallel C-MOVE
// destination stores — the parallel path is a Go concurrency win over
// typical single-threaded DIMSE stacks.
func BenchmarkCMoveStoreAssociations(b *testing.B) {
	for _, maxAssoc := range []int{1, 4} {
		b.Run("maxAssoc="+strconv.Itoa(maxAssoc), func(b *testing.B) {
			benchmarkCMoveStores(b, maxAssoc, 16)
		})
	}
}

func benchmarkCMoveStores(b *testing.B, maxAssoc, nStores int) {
	b.Helper()
	ctUID := "1.2.840.10008.5.1.4.1.1.2"

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
			OnCStore: func(_ context.Context, _ ae.StoreRequest) uint16 {
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
				"STORESCP": {Addr: storeLn.Addr().String(), MaxAssociations: maxAssoc},
			},
			OnCMove: func(_ context.Context, _ ae.MoveRequest) ae.MovePlan {
				stores := make([]ae.StoreRequest, nStores)
				for i := range stores {
					ds := godicom.NewDataset()
					uid := "1.2.9." + strconv.Itoa(i)
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
