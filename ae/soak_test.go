package ae_test

import (
	"context"
	"fmt"
	"iter"
	"runtime"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// A retrieval is a loop with state in it — sub-operation counts, a reader
// goroutine, a store's answer written back before the handler resumes — and the
// failures that kind of code has are the ones that only show up on the thousandth
// instance: a count that drifts, a goroutine left behind per operation, a
// response that arrives one store late.
//
// So this runs the same retrieval several times on one association and asserts
// the two things a single pass cannot: the counts are identical every round, and
// the process is back where it started afterwards. It is not a substitute for the
// integration test against a real PACS (ae/integration_pacs_test.go), which is
// where a peer's own quirks live.
func TestRetrieveSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test: skipped under -short")
	}

	const (
		instances = 200
		rounds    = 3
	)
	assoc := startGetSCP(t, func(_ context.Context, _ ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: instances}}, nil) {
				return
			}
			for i := 0; i < instances; i++ {
				instance := fmt.Sprintf("1.2.826.0.1.3680043.10.541.9.%d", i)
				store := getStoreRequest(instance)
				if !yield(&ae.RetrieveOp{Store: &store}, nil) {
					return
				}
			}
		}
	})

	// The baseline is taken after the association is up: what must not grow is
	// what a retrieval allocates, not what a connection costs.
	baseline := runtime.NumGoroutine()

	for round := 0; round < rounds; round++ {
		delivered := 0
		matches := cget(t, assoc, func(_ context.Context, _ ae.StoreEvent) uint16 {
			delivered++
			return dimse.StatusSuccess
		})
		last := matches[len(matches)-1]
		if last.Status != dimse.StatusSuccess {
			t.Fatalf("round %d: final status %#04x", round, last.Status)
		}
		if last.SubOperations.Completed != instances || last.SubOperations.Failed != 0 {
			t.Fatalf("round %d: completed %d failed %d, want %d/0",
				round, last.SubOperations.Completed, last.SubOperations.Failed, instances)
		}
		if delivered != instances {
			t.Fatalf("round %d: delivered %d of %d", round, delivered, instances)
		}
	}

	if err := assoc.Abort(); err != nil {
		t.Logf("abort: %v", err)
	}
	// Goroutines need a moment to unwind after the association ends; the check
	// is that they are gone, not that they go instantly.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= baseline {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines: %d before, %d after %d retrievals\n%s",
				baseline, runtime.NumGoroutine(), rounds, buf[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
