package ae_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// moveBatch is a C-MOVE SCP whose C-STORE sub-operations park in the Move
// Destination until the test opens the gate. Parking them is what makes a stop
// observable: the SCP can only look for a C-CANCEL-RQ or an A-RELEASE-RQ
// between two sub-operations, so a test that wants to interrupt a batch has to
// hold one in flight.
type moveBatch struct {
	addr    string
	entered chan string
	gate    chan struct{}
	openOne sync.Once

	stores atomic.Int32
}

func (m *moveBatch) open() { m.openOne.Do(func() { close(m.gate) }) }

// waitFor blocks until n sub-operations have reached the store SCP.
func (m *moveBatch) waitFor(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-m.entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d sub-operations reached the store SCP", i, n)
		}
	}
}

// startMoveBatch serves a C-MOVE SCP with n stores to a Move Destination that
// the caller can hold open. maxAssoc is passed straight to the destination.
func startMoveBatch(t *testing.T, n, maxAssoc int) *moveBatch {
	t.Helper()

	m := &moveBatch{
		entered: make(chan string, n),
		gate:    make(chan struct{}),
	}

	storeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	moveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = storeLn.Close()
		_ = moveLn.Close()
	})

	go func() {
		_ = ae.Serve(ctx, storeLn, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
			OnCStore: func(ctx context.Context, ev ae.StoreEvent) uint16 {
				m.stores.Add(1)
				select {
				case m.entered <- ev.AffectedSOPInstanceUID:
				default:
				}
				select {
				case <-m.gate:
				case <-ctx.Done():
				}
				return dimse.StatusSuccess
			},
		})
	}()

	instances := make([]string, n)
	for i := range instances {
		instances[i] = fmt.Sprintf("1.2.826.0.1.3680043.10.541.93.%d", i+1)
	}

	go func() {
		_ = ae.Serve(ctx, moveLn, ae.ServerConfig{
			AETitle:                  "MOVESCP",
			AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelMove},
			MoveDestinations: map[string]ae.MoveDestination{
				"STORESCP": {Addr: storeLn.Addr().String(), MaxAssociations: maxAssoc},
			},
			OnCMove: func(_ context.Context, _ ae.MoveRequest) ae.MovePlan {
				plan := ae.MovePlan{}
				for _, inst := range instances {
					plan.Stores = append(plan.Stores, ae.StoreRequest{
						AffectedSOPClassUID:    secondaryCaptureSOPClass,
						AffectedSOPInstanceUID: inst,
						Dataset:                []byte{0x08, 0x00, 0x05, 0x00, 0x43, 0x53, 0x00, 0x00},
					})
				}
				return plan
			},
		})
	}()

	m.addr = moveLn.Addr().String()
	return m
}

// sendCMove issues a C-MOVE-RQ at IMAGE level; the handler ignores the
// identifier and returns the batch it was built with.
func sendCMove(t *testing.T, scu *rawSCU, msgID uint16) {
	t.Helper()
	cmd, err := (&dimse.CMoveRQ{
		MessageID:           msgID,
		AffectedSOPClassUID: ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination:     "STORESCP",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	ident := []byte{0x08, 0x00, 0x52, 0x00, 0x43, 0x53, 0x06, 0x00,
		'I', 'M', 'A', 'G', 'E', ' '}
	scu.send(cmd, ident)
}

func nextMoveRSP(t *testing.T, scu *rawSCU, timeout time.Duration) *dimse.CMoveRSP {
	t.Helper()
	control, cmd, err := scu.nextItem(timeout)
	if err != nil {
		t.Fatalf("read C-MOVE-RSP: %v", err)
	}
	if control != nil {
		t.Fatalf("got %T, want a C-MOVE-RSP", control)
	}
	rsp, err := dimse.DecodeCMoveRSP(cmd)
	if err != nil {
		t.Fatalf("decode C-MOVE-RSP: %v", err)
	}
	return rsp
}

// settle gives the SCP time to read what the test just wrote. The stop has to
// cross a socket and be queued on the association's read side before the next
// sub-operation boundary is reached; without this the test would be measuring
// whether the reader goroutine happened to be scheduled, not what the SCP does
// with a stop it has seen.
const settle = 50 * time.Millisecond

// cancelSCU sends a C-CANCEL-RQ for msgID and opens the gate, so the
// sub-operation that is in flight finishes and the SCP reaches a boundary.
func cancelSCU(t *testing.T, scu *rawSCU, m *moveBatch, msgID uint16) {
	t.Helper()
	cmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: msgID}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(cmd, nil)
	time.Sleep(settle)
	m.open()
}

// readFinalMoveRSP reads past the Pending responses to the one that ends the
// operation.
func readFinalMoveRSP(t *testing.T, scu *rawSCU) *dimse.CMoveRSP {
	t.Helper()
	for {
		rsp := nextMoveRSP(t, scu, 10*time.Second)
		if !dimse.IsPending(rsp.Status) {
			return rsp
		}
	}
}

// A C-CANCEL-RQ that arrives while a C-MOVE batch is running has to stop it:
// PS3.4 C.4.2.1.4 makes the SCP stop starting sub-operations, and Table C.4-3
// makes the ones it never started Remaining, not Failed. Before this the batch
// ran to the end and the cancel was logged as late — the requestor waited for
// every remaining instance, and the C-CANCEL had no effect on the operation it
// named.
func TestSCPStopsCMoveBatchOnCancel(t *testing.T) {
	t.Parallel()

	const total = 4
	m := startMoveBatch(t, total, 0)
	scu := dialRawSCU(t, m.addr, "MOVESCP", ae.PatientRootQueryRetrieveInformationModelMove)
	sendCMove(t, scu, 7)

	if rsp := nextMoveRSP(t, scu, 5*time.Second); !dimse.IsPending(rsp.Status) {
		t.Fatalf("first response status 0x%04X, want Pending", rsp.Status)
	}

	// The first sub-operation is now parked inside the store SCP, so the batch
	// has somewhere to be interrupted.
	m.waitFor(t, 1)
	cancelSCU(t, scu, m, 7)

	final := readFinalMoveRSP(t, scu)
	if final.Status != dimse.StatusCancel {
		t.Errorf("final status 0x%04X, want 0x%04X (Cancel)", final.Status, dimse.StatusCancel)
	}
	c := final.SubOperations
	if !c.Present {
		t.Fatal("final response carries no sub-operation counts")
	}
	if c.Failed != 0 {
		t.Errorf("Failed=%d, want 0: the untried sub-operations are remaining, not failed", c.Failed)
	}
	if c.Completed == 0 {
		t.Error("Completed=0: the sub-operation that was in flight should have been finished")
	}
	if c.Remaining == 0 {
		t.Error("Remaining=0: the whole batch ran, so the cancel was not honoured")
	}
	if sum := int(c.Completed) + int(c.Failed) + int(c.Warning) + int(c.Remaining); sum != total {
		t.Errorf("counts add up to %d, want %d", sum, total)
	}
	if n := int(m.stores.Load()); n >= total {
		t.Errorf("the store SCP received %d of %d instances, want fewer: the batch was not cut short", n, total)
	}
}

// The same stop has to reach the parallel path, where the difference between
// "dispatched" and "started" is a channel hand-off. A batch pre-filled into the
// jobs channel before the cancel arrived would run to the end.
func TestSCPStopsParallelCMoveBatchOnCancel(t *testing.T) {
	t.Parallel()

	const (
		total   = 6
		workers = 3
	)
	m := startMoveBatch(t, total, workers)
	scu := dialRawSCU(t, m.addr, "MOVESCP", ae.PatientRootQueryRetrieveInformationModelMove)
	sendCMove(t, scu, 21)

	if rsp := nextMoveRSP(t, scu, 5*time.Second); !dimse.IsPending(rsp.Status) {
		t.Fatalf("first response status 0x%04X, want Pending", rsp.Status)
	}

	// One parked sub-operation per association: every worker is busy, so the
	// dispatcher is the only thing that can start another.
	m.waitFor(t, workers)
	cancelSCU(t, scu, m, 21)

	final := readFinalMoveRSP(t, scu)
	if final.Status != dimse.StatusCancel {
		t.Errorf("final status 0x%04X, want 0x%04X (Cancel)", final.Status, dimse.StatusCancel)
	}
	c := final.SubOperations
	if !c.Present {
		t.Fatal("final response carries no sub-operation counts")
	}
	if c.Failed != 0 {
		t.Errorf("Failed=%d, want 0", c.Failed)
	}
	if c.Completed < workers {
		t.Errorf("Completed=%d, want at least %d: the in-flight sub-operations finish", c.Completed, workers)
	}
	if c.Remaining == 0 {
		t.Error("Remaining=0: every sub-operation ran, so the cancel did not cut the batch short")
	}
	if sum := int(c.Completed) + int(c.Failed) + int(c.Warning) + int(c.Remaining); sum != total {
		t.Errorf("counts add up to %d, want %d", sum, total)
	}
	if n := int(m.stores.Load()); n >= total {
		t.Errorf("the store SCP received %d of %d instances, want fewer", n, total)
	}
}

// A requestor that releases while its C-MOVE is still running is waiting for
// A-RELEASE-RP: PS3.8 7.2.2 lets it receive P-DATA meanwhile and requires it to
// ignore it. Running the rest of the batch first turns a release into a stall
// the peer eventually reports as a network fault, so the SCP stops at the next
// boundary and answers.
func TestSCPReleasesMidCMove(t *testing.T) {
	t.Parallel()

	const total = 4
	m := startMoveBatch(t, total, 0)
	scu := dialRawSCU(t, m.addr, "MOVESCP", ae.PatientRootQueryRetrieveInformationModelMove)
	sendCMove(t, scu, 9)

	if rsp := nextMoveRSP(t, scu, 5*time.Second); !dimse.IsPending(rsp.Status) {
		t.Fatalf("first response status 0x%04X, want Pending", rsp.Status)
	}
	m.waitFor(t, 1)

	if err := pdu.Write(scu.conn, &pdu.AReleaseRQ{}); err != nil {
		t.Fatalf("send A-RELEASE-RQ: %v", err)
	}
	time.Sleep(settle)
	m.open()

	for {
		control, _, err := scu.nextItem(10 * time.Second)
		if err != nil {
			t.Fatalf("waiting for A-RELEASE-RP: %v", err)
		}
		if _, ok := control.(*pdu.AReleaseRP); ok {
			break
		}
		if control != nil {
			t.Fatalf("got %T, want A-RELEASE-RP", control)
		}
	}

	if n := int(m.stores.Load()); n != 1 {
		t.Errorf("the store SCP received %d instances, want 1: the batch should stop at the boundary", n)
	}
}
