// Command get runs a C-GET SCP and retrieves from it.
//
//	go run ./examples/get
//
// A C-GET is a retrieval on the association the request arrived on: the SCP
// turns around and sends C-STORE-RQs back over the same connection, which is why
// the requestor has to propose the storage presentation contexts with the SCP
// role. The handler yields a plan (how many sub-operations) and then one store at
// a time; the framework performs the store and writes the answer into
// op.Outcome before the next yield, so a handler that counts successes counts the
// ones that happened.
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
	queryModel = ae.StudyRootQueryRetrieveInformationModelGet
	storageSOP = "1.2.840.10008.5.1.4.1.1.7" // Secondary Capture
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "GET_SCP",
			AcceptedAbstractSyntaxes: []string{queryModel, storageSOP},
			// A C-GET SCP stores back to the requestor, so the storage context
			// has to be accepted with the SCP role.
			RoleSelections: []pdu.RoleSelection{ae.BuildRole(storageSOP, true, true)},
			OnCGet: func(_ context.Context, _ ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
				return func(yield func(*ae.RetrieveOp, error) bool) {
					const total = 3
					if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: total}}, nil) {
						return
					}
					for i := 1; i <= total; i++ {
						instance := fmt.Sprintf("1.2.826.0.1.3680043.10.541.3.%d", i)
						op := &ae.RetrieveOp{Store: &ae.StoreRequest{
							AffectedSOPClassUID:    storageSOP,
							AffectedSOPInstanceUID: instance,
							Data:                   instanceDataset(instance),
						}}
						if !yield(op, nil) {
							return
						}
						// op.Outcome is the peer's C-STORE-RSP, filled in while
						// this goroutine was suspended in the yield.
						fmt.Printf("stored %s: status %#04x\n", instance, op.Outcome.Status)
					}
				}
			},
		})
	}()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "GET_SCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: queryModel, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: storageSOP, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		// The requestor receives the instances: SCP role on storage.
		RoleSelections: []pdu.RoleSelection{ae.BuildRole(storageSOP, false, true)},
	}, ln.Addr().String(), "GET_SCP")
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
	query.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.826.0.1.3680043.10.541.2.1"))

	received := 0
	responses, err := assoc.CGet(ctx, ae.GetRequest{
		QueryModel:     queryModel,
		IdentifierData: query,
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			received++
			ds, err := ev.Decode()
			if err != nil {
				fmt.Fprintf(os.Stderr, "decode %s: %v\n", ev.AffectedSOPInstanceUID, err)
				return dcmstatus.CannotUnderstand
			}
			_, _ = ds.GetString(godicom.MustTag("SOPInstanceUID"))
			return dcmstatus.Success
		},
	})
	if err != nil {
		log.Fatalf("C-GET: %v", err)
	}
	final := responses[len(responses)-1]
	fmt.Printf("C-GET finished: status %#04x, completed %d, failed %d, received %d\n",
		final.Status, final.SubOperations.Completed, final.SubOperations.Failed, received)
}

// instanceDataset is one small Secondary Capture instance.
func instanceDataset(sopInstance string) *godicom.Dataset {
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, storageSOP))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "EXAMPLE^GET"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("Modality"), godicom.VRCS, "OT"))
	pixels := make([]byte, 32<<10)
	for i := range pixels {
		pixels[i] = byte(i)
	}
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, pixels))
	return ds
}
