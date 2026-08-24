package ae_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// priorityRecorder captures the Priority every SCP handler was handed.
type priorityRecorder struct {
	mu  sync.Mutex
	got map[string]uint16
}

func (r *priorityRecorder) record(op string, priority uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got[op] = priority
}

func (r *priorityRecorder) load(op string) uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[op]
}

// Priority is a uint16 whose MEDIUM value is zero, which used to be read as
// "unset" and quietly replaced with LOW. Asking for normal priority therefore
// sent the lowest one — the opposite of what the caller wrote, with no error.
func TestPriorityReachesPeerUnchanged(t *testing.T) {
	t.Parallel()

	const (
		findModel = ae.StudyRootQueryRetrieveInformationModelFind
		moveModel = ae.StudyRootQueryRetrieveInformationModelMove
		getModel  = ae.StudyRootQueryRetrieveInformationModelGet
	)

	rec := &priorityRecorder{got: map[string]uint16{}}
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
			AETitle: "PRIOSCP",
			AcceptedAbstractSyntaxes: []string{
				secondaryCaptureSOPClass, findModel, moveModel, getModel,
			},
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				rec.record("C-STORE", req.Priority)
				return dimse.StatusSuccess
			},
			OnCFind: func(_ context.Context, req ae.FindRequest) []ae.FindMatch {
				rec.record("C-FIND", req.Priority)
				return []ae.FindMatch{{Status: dimse.StatusSuccess}}
			},
			OnCMove: func(_ context.Context, req ae.MoveRequest) ae.MovePlan {
				rec.record("C-MOVE", req.Priority)
				return ae.MovePlan{Responses: []ae.RetrieveMatch{{Status: dimse.StatusSuccess}}}
			},
			OnCGet: func(_ context.Context, req ae.GetRequest) ae.GetPlan {
				rec.record("C-GET", req.Priority)
				return ae.GetPlan{Responses: []ae.RetrieveMatch{{Status: dimse.StatusSuccess}}}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "PRIOSCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: findModel, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 5, AbstractSyntax: moveModel, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 7, AbstractSyntax: getModel, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "PRIOSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := func() *godicom.Dataset {
		ds := godicom.NewDataset()
		ds.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
		ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3.4"))
		return ds
	}

	// Each request asks for one priority explicitly; the SCP must see that value.
	// Priority is a request the peer may ignore, but it must arrive as sent.
	for _, want := range []uint16{dimse.PriorityMedium, dimse.PriorityHigh, dimse.PriorityLow} {
		if _, err := assoc.CStore(dialCtx, ae.StoreRequest{
			AffectedSOPClassUID:    secondaryCaptureSOPClass,
			AffectedSOPInstanceUID: "1.2.3.4.5",
			Dataset:                goldenStoreDataset(),
			Priority:               want,
		}); err != nil {
			t.Fatalf("C-STORE priority 0x%04x: %v", want, err)
		}
		if _, err := assoc.CFind(dialCtx, ae.FindRequest{
			QueryModel:     findModel,
			IdentifierData: query(),
			Priority:       want,
		}); err != nil {
			t.Fatalf("C-FIND priority 0x%04x: %v", want, err)
		}
		if _, err := assoc.CMove(dialCtx, ae.MoveRequest{
			QueryModel:      moveModel,
			MoveDestination: "ANYWHERE",
			IdentifierData:  query(),
			Priority:        want,
		}); err != nil {
			t.Fatalf("C-MOVE priority 0x%04x: %v", want, err)
		}
		if _, err := assoc.CGet(dialCtx, ae.GetRequest{
			QueryModel:     getModel,
			IdentifierData: query(),
			Priority:       want,
		}); err != nil {
			t.Fatalf("C-GET priority 0x%04x: %v", want, err)
		}
		for _, op := range []string{"C-STORE", "C-FIND", "C-MOVE", "C-GET"} {
			if got := rec.load(op); got != want {
				t.Errorf("%s priority: SCP saw 0x%04x, requested 0x%04x", op, got, want)
			}
		}
	}

	// An unset Priority means MEDIUM, since that is what zero encodes.
	if _, err := assoc.CFind(dialCtx, ae.FindRequest{
		QueryModel:     findModel,
		IdentifierData: query(),
	}); err != nil {
		t.Fatalf("C-FIND default priority: %v", err)
	}
	if got := rec.load("C-FIND"); got != dimse.PriorityMedium {
		t.Errorf("default priority: SCP saw 0x%04x, want MEDIUM (0x%04x)", got, dimse.PriorityMedium)
	}

	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
