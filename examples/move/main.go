// Command move runs a C-MOVE destination, a C-MOVE SCP, and a requestor.
//
//	go run ./examples/move
//
// C-MOVE is the one retrieval where the instances do not come back over the
// requestor's association: the SCP dials the destination named in the request and
// stores there, which is why the destination has to be reachable and why the
// sub-operation count is reported to a peer that receives nothing itself. The
// plan carries the destination and the presentation contexts the stores need,
// because the A-ASSOCIATE-RQ to the destination has to name them all before the
// first C-STORE.
package main

import (
	"context"
	"fmt"
	"iter"
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

const (
	queryModel = ae.StudyRootQueryRetrieveInformationModelMove
	storageSOP = "1.2.840.10008.5.1.4.1.1.7" // Secondary Capture
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The destination: an ordinary Storage SCP that receives the sub-operations.
	destLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, destLn, ae.ServerConfig{
			AETitle:                  "MOVE_DEST",
			AcceptedAbstractSyntaxes: []string{storageSOP},
			OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
				fmt.Printf("destination stored %s\n", ev.AffectedSOPInstanceUID)
				return dcmstatus.Success
			},
		})
	}()
	destAddr := destLn.Addr().String()

	// The SCP that answers C-MOVE and dials the destination.
	scpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, scpLn, ae.ServerConfig{
			AETitle:                  "MOVE_SCP",
			AcceptedAbstractSyntaxes: []string{queryModel},
			OnCMove: func(_ context.Context, _ ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
				return func(yield func(*ae.RetrieveOp, error) bool) {
					stores := []ae.StoreRequest{
						{
							AffectedSOPClassUID:    storageSOP,
							AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.541.4.1",
							Data:                   instanceDataset("1.2.826.0.1.3680043.10.541.4.1"),
						},
						{
							AffectedSOPClassUID:    storageSOP,
							AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.541.4.2",
							Data:                   instanceDataset("1.2.826.0.1.3680043.10.541.4.2"),
						},
					}
					plan := &ae.RetrievePlan{
						Total:       len(stores),
						Destination: &ae.MoveDestination{Addr: destAddr, CalledAE: "MOVE_DEST"},
						// The contexts the association to the destination will
						// propose: everything has to be declared before the
						// first C-STORE.
						Contexts: ae.StoreContexts(stores),
					}
					if !yield(&ae.RetrieveOp{Plan: plan}, nil) {
						return
					}
					for i := range stores {
						op := &ae.RetrieveOp{Store: &stores[i]}
						if !yield(op, nil) {
							return
						}
						fmt.Printf("C-MOVE sub-operation: status %#04x\n", op.Outcome.Status)
					}
				}
			},
		})
	}()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "MOVE_SCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   queryModel,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, scpLn.Addr().String(), "MOVE_SCP")
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
	query.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.2.1"))

	responses, err := assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      queryModel,
		MoveDestination: "MOVE_DEST",
		IdentifierData:  query,
	})
	if err != nil {
		log.Fatalf("C-MOVE: %v", err)
	}
	final := responses[len(responses)-1]
	fmt.Printf("C-MOVE finished: status %#04x, completed %d, failed %d\n",
		final.Status, final.SubOperations.Completed, final.SubOperations.Failed)
}

func instanceDataset(sopInstance string) *godicom.Dataset {
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, storageSOP))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "EXAMPLE^MOVE"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "OT"))
	pixels := make([]byte, 32<<10)
	for i := range pixels {
		pixels[i] = byte(i)
	}
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pixels))
	return ds
}
