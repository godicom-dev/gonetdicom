// Command find runs a C-FIND SCP and queries it.
//
//	go run ./examples/find
//
// The handler returns an iterator, which is what makes a query answerable from a
// database cursor or a file walk: each match is encoded and written before the
// next one is produced, so the result set never exists as a whole. A yield that
// returns false means the peer is no longer listening — a C-CANCEL, a released
// association — and the handler's own loop is where that is noticed.
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
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

const queryModel = ae.StudyRootQueryRetrieveInformationModelFind

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "FIND_SCP",
			AcceptedAbstractSyntaxes: []string{queryModel},
			OnCFind: func(_ context.Context, _ ae.FindRequest) iter.Seq2[ae.FindMatch, error] {
				// A real implementation would walk its index here and yield as
				// it goes, rather than building a slice first.
				return func(yield func(ae.FindMatch, error) bool) {
					for i, name := range []string{"DOE^JOHN", "DOE^JANE", "SMITH^ANNA"} {
						ds := godicom.NewDataset()
						ds.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
						ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, name))
						ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI,
							fmt.Sprintf("1.2.826.0.1.3680043.10.541.2.%d", i+1)))
						// A match is a Pending response carrying an identifier;
						// Success is the terminal one, so a handler that yielded
						// matches with the zero status would end the query on the
						// first one.
						if !yield(ae.FindMatch{Status: dimse.StatusPending, Identifier: ds}, nil) {
							return
						}
					}
				}
			},
		})
	}()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "FIND_SCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   queryModel,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "FIND_SCP")
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
	query.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, ""))

	matches, err := assoc.CFind(ctx, ae.FindRequest{QueryModel: queryModel, IdentifierData: query})
	if err != nil {
		log.Fatalf("C-FIND: %v", err)
	}
	for _, m := range matches {
		if m.Identifier == nil {
			continue // a terminal response carries counts, not an identifier
		}
		name, _ := m.Identifier.GetString(godicom.MustTag("PatientName"))
		study, _ := m.Identifier.GetString(godicom.MustTag("StudyInstanceUID"))
		fmt.Printf("match: %-12s %s\n", name, study)
	}
}
