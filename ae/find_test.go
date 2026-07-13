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

func TestCFindCancel(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const findMsgID uint16 = 7
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "FINDSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelFind,
			},
			OnCFind: func(_ context.Context, _ ae.FindRequest) []ae.FindMatch {
				// Hold so SCU can send C-CANCEL while the request is outstanding;
				// the cancel sits in the TCP buffer until responses are streamed.
				time.Sleep(80 * time.Millisecond)
				mk := func(id string) *godicom.Dataset {
					ds := godicom.NewDataset()
					ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, id))
					return ds
				}
				return []ae.FindMatch{
					{Status: dimse.StatusPending, Identifier: mk("A")},
					{Status: dimse.StatusPending, Identifier: mk("B")},
					{Status: dimse.StatusPending, Identifier: mk("C")},
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

	type result struct {
		matches []ae.FindMatch
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		m, err := assoc.CFind(dialCtx, ae.FindRequest{
			QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
			IdentifierData: query,
			MessageID:      findMsgID,
		})
		resCh <- result{m, err}
	}()

	time.Sleep(30 * time.Millisecond)
	if err := assoc.CCancel(dialCtx, findMsgID, 0, ae.PatientRootQueryRetrieveInformationModelFind); err != nil {
		t.Fatalf("C-CANCEL: %v", err)
	}

	res := <-resCh
	if res.err != nil {
		t.Fatalf("C-FIND: %v", res.err)
	}
	if len(res.matches) == 0 {
		t.Fatal("no matches")
	}
	last := res.matches[len(res.matches)-1]
	if last.Status != dimse.StatusCancel {
		t.Fatalf("final status=0x%04x matches=%d", last.Status, len(res.matches))
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
