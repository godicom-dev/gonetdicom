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

func TestCFindSCURoundtrip(t *testing.T) {
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
			AETitle: "FINDSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelFind,
			},
			OnCFind: func(_ context.Context, req ae.FindRequest) []ae.FindMatch {
				ident := godicom.NewDataset()
				ident.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
				ident.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
				ident.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "P001"))
				return []ae.FindMatch{
					{Status: dimse.StatusPending, Identifier: ident},
					{Status: dimse.StatusSuccess},
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "FINDSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelFind,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "FINDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
	query.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "*"))

	matches, err := assoc.CFind(dialCtx, ae.FindRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
		IdentifierData: query,
	})
	if err != nil {
		t.Fatalf("C-FIND: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches=%d", len(matches))
	}
	if matches[0].Status != dimse.StatusPending || matches[0].Identifier == nil {
		t.Fatalf("pending: %+v", matches[0])
	}
	name, _ := matches[0].Identifier.GetString(godicom.MustTag("PatientName"))
	if name != "DOE^JOHN" {
		t.Fatalf("PatientName=%q", name)
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
