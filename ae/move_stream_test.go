package ae_test

import (
	"context"
	"errors"
	"iter"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// moveHarness is a Storage SCP with a C-MOVE SCP in front of it, plus a client
// association ready to send C-MOVE.
type moveHarness struct {
	assoc     *ae.Association
	storeAddr string // where the Storage SCP is listening
	stored    atomic.Int32
}

// startMoveSCP starts both ends and returns the harness. configuredAddr is what
// ServerConfig.MoveDestinations says; empty uses the real destination.
func startMoveSCP(t *testing.T, handler ae.MoveHandler, onStore ae.StoreHandler, configuredAddr string) *moveHarness {
	t.Helper()

	ctUID := string(uid.CTImageStorage)
	storeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	moveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if configuredAddr == "" {
		configuredAddr = storeLn.Addr().String()
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 2)
	h := &moveHarness{storeAddr: storeLn.Addr().String()}

	go func() {
		errCh <- ae.Serve(ctx, storeLn, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{ctUID},
			OnCStore: func(c context.Context, e ae.StoreEvent) uint16 {
				h.stored.Add(1)
				if onStore == nil {
					return dimse.StatusSuccess
				}
				return onStore(c, e)
			},
		})
	}()
	go func() {
		errCh <- ae.Serve(ctx, moveLn, ae.ServerConfig{
			AETitle:                  "MOVESCP",
			AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelMove},
			MoveDestinations: map[string]ae.MoveDestination{
				"STORESCP": {Addr: configuredAddr},
			},
			OnCMove: handler,
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "MOVESCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelMove,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, moveLn.Addr().String(), "MOVESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	h.assoc = assoc

	t.Cleanup(func() {
		_ = assoc.Abort()
		cancel()
		_ = storeLn.Close()
		_ = moveLn.Close()
		for i := 0; i < 2; i++ {
			select {
			case <-errCh:
			case <-time.After(5 * time.Second):
				t.Error("Serve did not return")
			}
		}
	})
	return h
}

// moveContexts is what a plan offers for stores of one SOP Class.
func moveContexts() []ae.StoreContext {
	return []ae.StoreContext{{SOPClassUID: string(uid.CTImageStorage)}}
}

// moveQuery is a C-MOVE identifier at IMAGE level.
func moveQuery() *godicom.Dataset {
	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	return query
}

// cmove runs one C-MOVE against the SCP and returns every response it got.
func cmove(t *testing.T, h *moveHarness) []ae.RetrieveMatch {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	matches, err := h.assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "STORESCP",
		IdentifierData:  moveQuery(),
	})
	if err != nil {
		t.Fatalf("C-MOVE: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("C-MOVE answered nothing")
	}
	return matches
}

// finalMatch is the last non-Pending response.
func finalMatch(t *testing.T, matches []ae.RetrieveMatch) ae.RetrieveMatch {
	t.Helper()
	for i := len(matches) - 1; i >= 0; i-- {
		if !dimse.IsPending(matches[i].Status) {
			return matches[i]
		}
	}
	t.Fatalf("no terminal response in %+v", matches)
	return ae.RetrieveMatch{}
}

// The write-back the C-MOVE handler never had: the framework performs the store
// on the destination association and hands back what the destination answered, so
// a handler can decide about the rest of the batch on what happened. pynetdicom
// cannot — there the framework tallies the statuses but the association is not
// the handler's to observe between sub-operations.
func TestCMoveStoreOutcomeReachesTheHandler(t *testing.T) {
	t.Parallel()

	instances := []string{"1.2.3.4.5.7.1", "1.2.3.4.5.7.2"}
	seen := make(chan []uint16, 1)
	var attempts atomic.Int32

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{
				Total:    len(instances),
				Contexts: moveContexts(),
			}}, nil) {
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
	}, func(context.Context, ae.StoreEvent) uint16 {
		if attempts.Add(1) == 1 {
			return dcmstatus.ElementsDiscarded // a warning: the instance arrived
		}
		return dimse.StatusSuccess
	}, "")

	matches := cmove(t, h)

	if h.stored.Load() != int32(len(instances)) {
		t.Fatalf("stored=%d, want %d", h.stored.Load(), len(instances))
	}
	select {
	case outcomes := <-seen:
		if len(outcomes) != 2 || outcomes[0] != dcmstatus.ElementsDiscarded || outcomes[1] != dimse.StatusSuccess {
			t.Fatalf("handler saw outcomes %#v", outcomes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never finished")
	}

	final := finalMatch(t, matches)
	if final.Status != dimse.StatusWarning {
		t.Fatalf("final status=0x%04x, want Warning", final.Status)
	}
	if final.SubOperations.Completed != 1 || final.SubOperations.Warning != 1 || final.SubOperations.Remaining != 0 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
}

// One store at a time, and the next one is only produced once the previous has
// been answered: that is what makes the write-back possible, and what keeps a
// 500-instance move from existing as a value.
func TestCMoveProducesEachStoreOnDemand(t *testing.T) {
	t.Parallel()

	firstStored := make(chan struct{})
	release := make(chan struct{})
	secondProduced := make(chan struct{})

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 2, Contexts: moveContexts()}}, nil) {
				return
			}
			store := getStoreRequest("1.2.3.4.5.7.11")
			if !yield(&ae.RetrieveOp{Store: &store}, nil) {
				return
			}
			// The first store is on the wire and answered by now.
			close(firstStored)
			<-release
			close(secondProduced)
			store = getStoreRequest("1.2.3.4.5.7.12")
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	}, nil, "")

	type result struct {
		matches []ae.RetrieveMatch
		err     error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		m, err := h.assoc.CMove(ctx, ae.MoveRequest{
			QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
			MoveDestination: "STORESCP",
			IdentifierData:  moveQuery(),
		})
		done <- result{m, err}
	}()

	select {
	case <-firstStored:
	case <-time.After(10 * time.Second):
		t.Fatal("first store never completed")
	}
	// The handler is parked here, so the second instance cannot be in flight.
	if n := h.stored.Load(); n != 1 {
		t.Fatalf("stored=%d before the handler resumed, want 1", n)
	}
	close(release)
	select {
	case <-secondProduced:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never resumed")
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("C-MOVE: %v", r.err)
		}
		final := finalMatch(t, r.matches)
		if final.Status != dimse.StatusSuccess || final.SubOperations.Completed != 2 {
			t.Fatalf("final=%+v", final)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("C-MOVE never finished")
	}
}

// A plan's own Destination wins over ServerConfig.MoveDestinations: the endpoint
// is as much a decision about this retrieval as the count is.
func TestCMovePlanDestinationOverridesConfig(t *testing.T) {
	t.Parallel()

	var h *moveHarness
	h = startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			// The configured address is a port nobody listens on; the plan names
			// the real one, and the store has to go there.
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{
				Total:       1,
				Destination: &ae.MoveDestination{Addr: h.storeAddr},
				Contexts:    moveContexts(),
			}}, nil) {
				return
			}
			store := getStoreRequest("1.2.3.4.5.7.21")
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	}, nil, "127.0.0.1:1")

	matches := cmove(t, h)
	final := finalMatch(t, matches)
	if final.Status != dimse.StatusSuccess || final.SubOperations.Completed != 1 {
		t.Fatalf("final=%+v", final)
	}
	if h.stored.Load() != 1 {
		t.Fatalf("stored=%d, want 1", h.stored.Load())
	}
}

// A plan with sub-operations but no presentation contexts cannot open a Storage
// association: an A-ASSOCIATE-RQ has to name them all before the first store.
func TestCMovePlanWithoutContextsIsRefused(t *testing.T) {
	t.Parallel()

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil)
		}
	}, nil, "")

	final := finalMatch(t, cmove(t, h))
	if final.Status != dcmstatus.MoveUnrecognizedOperation {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.MoveUnrecognizedOperation)
	}
}

// A handler that stops part-way says so by returning an error, and the peer is
// told the SCP could not process the request.
func TestCMoveHandlerErrorIsUnableToProcess(t *testing.T) {
	t.Parallel()

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1, Contexts: moveContexts()}}, nil) {
				return
			}
			yield(nil, errors.New("archive went away"))
		}
	}, nil, "")

	final := finalMatch(t, cmove(t, h))
	if final.Status != dcmstatus.MoveUnableToProcess {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.MoveUnableToProcess)
	}
}

// A destination that will not take an association never starts the transfer, so
// the answer is 0xA801 rather than the failure aggregate: the instances were
// never attempted. The counts still add up, because that is what the SCU reads
// them for.
func TestCMoveUnreachableDestinationIsUnknown(t *testing.T) {
	t.Parallel()

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{
				Total:    2,
				Contexts: moveContexts(),
			}}, nil) {
				return
			}
			// Never reached: the SCP cannot open the destination association.
			store := getStoreRequest("1.2.3.4.5.7.31")
			yield(&ae.RetrieveOp{Store: &store}, nil)
		}
	}, nil, "127.0.0.1:1")

	final := finalMatch(t, cmove(t, h))
	if final.Status != dcmstatus.MoveDestinationUnknown {
		t.Fatalf("status=0x%04x, want 0x%04x", final.Status, dcmstatus.MoveDestinationUnknown)
	}
	if final.SubOperations.Failed != 2 || final.SubOperations.Completed != 0 {
		t.Fatalf("counts=%+v", final.SubOperations)
	}
}

// A handler with nothing to move is answered without opening an association at
// all, which is what "the study is on another server" looks like.
func TestCMoveZeroPlanSucceeds(t *testing.T) {
	t.Parallel()

	h := startMoveSCP(t, func(context.Context, ae.MoveRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 0}}, nil)
		}
	}, nil, "")

	matches := cmove(t, h)
	final := finalMatch(t, matches)
	if final.Status != dimse.StatusSuccess {
		t.Fatalf("status=0x%04x, want Success", final.Status)
	}
	if h.stored.Load() != 0 {
		t.Fatalf("stored=%d, want 0", h.stored.Load())
	}
}
