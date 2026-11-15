package ae_test

import (
	"context"
	"errors"
	"iter"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// startGetSCP starts a C-GET SCP that is also allowed to act as Storage SCU, and
// returns a client association ready to send C-GET.
func startGetSCP(t *testing.T, handler ae.GetHandler) *ae.Association {
	t.Helper()

	ctUID := string(uid.CTImageStorage)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "GETSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelGet,
				ctUID,
			},
			RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, true, true)},
			OnCGet:         handler,
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return")
		}
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "GETSCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: ctUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		// Requestor wants the SCP role on storage: it receives the C-STOREs.
		RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, false, true)},
	}, ln.Addr().String(), "GETSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = assoc.Abort() })
	return assoc
}

// getStoreRequest is one sub-operation of a C-GET over CT images.
func getStoreRequest(sopInstance string) ae.StoreRequest {
	ctUID := string(uid.CTImageStorage)
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
	return ae.StoreRequest{
		AffectedSOPClassUID:    ctUID,
		AffectedSOPInstanceUID: sopInstance,
		Data:                   ds,
	}
}

// cget runs one C-GET against the SCP and returns the responses.
func cget(t *testing.T, assoc *ae.Association, onStore ae.StoreHandler) []ae.RetrieveMatch {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	matches, err := assoc.CGet(ctx, ae.GetRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
		IdentifierData: query,
		OnCStore:       onStore,
	})
	if err != nil {
		t.Fatalf("C-GET: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("C-GET answered nothing")
	}
	return matches
}

// The point of the iterator contract: the framework performs the store and writes
// the peer's status back into the op, so a handler decides what to do next on
// what happened rather than on what it hoped. The counts in the final response
// are the same measurement — pynetdicom cannot report this, because there the
// handler does the store and the framework only believes what it is told.
func TestCGetStoreOutcomeReachesTheHandler(t *testing.T) {
	t.Parallel()

	instances := []string{"1.2.3.4.5.6.1", "1.2.3.4.5.6.2"}
	seen := make(chan []uint16, 1)

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: len(instances)}}, nil) {
				return
			}
			outcomes := make([]uint16, 0, len(instances))
			for _, inst := range instances {
				store := getStoreRequest(inst)
				op := &ae.RetrieveOp{Store: &store}
				if !yield(op, nil) {
					return
				}
				outcomes = append(outcomes, op.Outcome.Status)
			}
			seen <- outcomes
		}
	})

	var stored int
	matches := cget(t, assoc, func(context.Context, ae.StoreEvent) uint16 {
		stored++
		if stored == 1 {
			return dcmstatus.ElementsDiscarded // a warning: the instance arrived
		}
		return dimse.StatusSuccess
	})

	if stored != len(instances) {
		t.Fatalf("stored=%d, want %d", stored, len(instances))
	}
	select {
	case outcomes := <-seen:
		if len(outcomes) != 2 || outcomes[0] != dcmstatus.ElementsDiscarded || outcomes[1] != dimse.StatusSuccess {
			t.Fatalf("handler saw outcomes %#v", outcomes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never finished")
	}

	final := matches[len(matches)-1]
	if final.Status != dimse.StatusWarning {
		t.Fatalf("final status=0x%04x, want Warning", final.Status)
	}
	if final.SubOperations.Completed != 1 || final.SubOperations.Warning != 1 || final.SubOperations.Remaining != 0 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
}

// A handler that claims Success while the sub-operations failed is answered with
// the aggregate instead: the numbers are the framework's, not the handler's.
func TestCGetHandlerClaimIsReconciled(t *testing.T) {
	t.Parallel()

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil) {
				return
			}
			store := getStoreRequest("1.2.3.4.5.6.9")
			if !yield(&ae.RetrieveOp{Store: &store}, nil) {
				return
			}
			ok := &ae.RetrieveMatch{Status: dimse.StatusSuccess}
			yield(&ae.RetrieveOp{Result: ok}, nil)
		}
	})

	matches := cget(t, assoc, func(context.Context, ae.StoreEvent) uint16 {
		return dcmstatus.OutOfResources
	})

	final := matches[len(matches)-1]
	if final.Status != dcmstatus.UnableToPerformSubOperations {
		t.Fatalf("final status=0x%04x, want 0x%04x", final.Status, dcmstatus.UnableToPerformSubOperations)
	}
	if final.SubOperations.Failed != 1 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
}

// The sub-operation count is the first item because every Pending response is
// computed from it; a store offered before the plan cannot be counted.
func TestCGetStoreBeforePlanIsRefused(t *testing.T) {
	t.Parallel()

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			store := getStoreRequest("1.2.3.4.5.6.10")
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	})

	final := lastMatch(t, cget(t, assoc, nil))
	if final.Status != dcmstatus.GetUnrecognizedOperation {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.GetUnrecognizedOperation)
	}
}

// More sub-operations than the four US fields can report is refused up front,
// rather than reported as a wrong number later.
func TestCGetRejectsTooManySubOperations(t *testing.T) {
	t.Parallel()

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 65536}}, nil)
		}
	})

	final := lastMatch(t, cget(t, assoc, nil))
	if final.Status != dcmstatus.GetTooManySubOperations {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.GetTooManySubOperations)
	}
}

// A handler that stops part-way says so by returning an error, and the peer is
// told the SCP could not process the request.
func TestCGetHandlerErrorIsUnableToProcess(t *testing.T) {
	t.Parallel()

	assoc := startGetSCP(t, func(context.Context, ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil) {
				return
			}
			yield(nil, errors.New("archive went away"))
		}
	})

	final := lastMatch(t, cget(t, assoc, nil))
	if final.Status != dcmstatus.GetUnableToProcess {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.GetUnableToProcess)
	}
}

func lastMatch(t *testing.T, matches []ae.RetrieveMatch) ae.RetrieveMatch {
	t.Helper()
	return matches[len(matches)-1]
}
