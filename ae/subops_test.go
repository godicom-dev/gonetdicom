package ae

import (
	"errors"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

func TestCategorizeStorageStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status uint16
		want   statusCategory
	}{
		{"success", dimse.StatusSuccess, statusSuccess},
		{"coercion warning", 0xB000, statusWarning},
		{"elements discarded", 0xB006, statusWarning},
		{"data set does not match SOP class", 0xB007, statusWarning},
		{"processing failure", dcmstatus.ProcessingFailure, statusFailure},
		{"out of resources", dcmstatus.OutOfResources, statusFailure},
		{"cannot understand", 0xC211, statusFailure},
		{"unrecognised", 0xFF00, statusFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := categorizeStorageStatus(tc.status); got != tc.want {
				t.Fatalf("categorizeStorageStatus(0x%04X) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}
}

// The aggregate status follows PS3.4 C.4.3.1.3.2 as pynetdicom applies it: a
// batch where every sub-operation failed is 0xA702, anything else with a
// failure or a warning is 0xB000. An all-warning batch is 0xB000 — the
// instances arrived, so "unable to perform sub-operations" would be a lie.
func TestSubOperationsFinalStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ops  *subOperations
		want uint16
	}{
		{"nothing to do", newSubOperations(0), dimse.StatusSuccess},
		{"all completed", &subOperations{total: 3, completed: 3}, dimse.StatusSuccess},
		{"all failed", &subOperations{total: 3, failed: 3}, dcmstatus.UnableToPerformSubOperations},
		{"one failed", &subOperations{total: 3, completed: 2, failed: 1}, dimse.StatusWarning},
		{"all warned", &subOperations{total: 3, warning: 3}, dimse.StatusWarning},
		{"failed and warned", &subOperations{total: 2, failed: 1, warning: 1}, dimse.StatusWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.ops.finalStatus(); got != tc.want {
				t.Fatalf("finalStatus() = 0x%04X, want 0x%04X", got, tc.want)
			}
		})
	}
}

// A transport error carries no meaningful status, so it is a failure whatever
// the zero value of the status field would otherwise mean.
func TestSubOperationsRecordTransportError(t *testing.T) {
	t.Parallel()

	ops := newSubOperations(1)
	ops.record("1.2.3", errors.New("connection reset"), dimse.StatusSuccess)
	if ops.failed != 1 || ops.completed != 0 {
		t.Fatalf("failed=%d completed=%d", ops.failed, ops.completed)
	}
	if got := ops.counts(); got.Failed != 1 || got.Completed != 0 || !got.Present {
		t.Fatalf("counts=%+v", got)
	}
}

// Only failed instances belong in the Failed SOP Instance UID List; a warning
// means the instance was stored, so listing it would send an SCU after an
// instance it already has.
func TestSubOperationsFailedIdentifier(t *testing.T) {
	t.Parallel()

	warned := newSubOperations(1)
	warned.record("1.2.3", nil, 0xB000)
	if got := warned.failedIdentifier(); got != nil {
		t.Fatalf("warning-only outcome produced an identifier: %v", got)
	}

	ops := newSubOperations(3)
	ops.record("1.2.1", nil, dimse.StatusSuccess)
	ops.record("1.2.2", nil, 0xB000)
	ops.record("1.2.3", nil, 0xC211)

	ds := ops.failedIdentifier()
	if ds == nil {
		t.Fatal("no identifier for a failed sub-operation")
	}
	elem, ok := ds.Get(godicom.MustTag("FailedSOPInstanceUIDList"))
	if !ok {
		t.Fatal("identifier has no Failed SOP Instance UID List")
	}
	uids, _ := elem.Value.([]string)
	if len(uids) != 1 || uids[0] != "1.2.3" {
		t.Fatalf("FailedSOPInstanceUIDList = %v, want [1.2.3]", uids)
	}
}

// recordMissing keeps the counts summing to the number of matches when a Move
// Destination stops accepting associations part-way through a batch.
func TestSubOperationsRecordMissing(t *testing.T) {
	t.Parallel()

	ops := newSubOperations(5)
	ops.record("1.2.1", nil, dimse.StatusSuccess)
	ops.record("1.2.2", nil, dimse.StatusSuccess)
	ops.recordMissing()

	if ops.attempted() != 5 || ops.failed != 3 {
		t.Fatalf("attempted=%d failed=%d", ops.attempted(), ops.failed)
	}
	if got := ops.finalStatus(); got != dimse.StatusWarning {
		t.Fatalf("finalStatus() = 0x%04X, want 0x%04X", got, dimse.StatusWarning)
	}
}

// reconcile is where a handler's claim meets what actually happened: a
// Success it yielded is replaced by the aggregate, a status it chose otherwise
// stands, and the counts are always the framework's.
func TestSubOperationsReconcile(t *testing.T) {
	t.Parallel()

	t.Run("success upgraded to warning", func(t *testing.T) {
		t.Parallel()
		ops := newSubOperations(2)
		ops.record("1.2.1", nil, dimse.StatusSuccess)
		ops.record("1.2.2", nil, 0xA700)

		out := ops.reconcile([]RetrieveMatch{{Status: dimse.StatusSuccess}})
		if len(out) != 1 {
			t.Fatalf("responses=%d", len(out))
		}
		if out[0].Status != dimse.StatusWarning {
			t.Fatalf("status 0x%04X, want 0x%04X", out[0].Status, dimse.StatusWarning)
		}
		if out[0].SubOperations.Completed != 1 || out[0].SubOperations.Failed != 1 {
			t.Fatalf("counts=%+v", out[0].SubOperations)
		}
		if out[0].Identifier == nil {
			t.Fatal("no Failed SOP Instance UID List on a warning final response")
		}
	})

	t.Run("handler failure keeps its status", func(t *testing.T) {
		t.Parallel()
		ops := newSubOperations(1)
		ops.record("1.2.1", nil, dimse.StatusSuccess)

		out := ops.reconcile([]RetrieveMatch{{Status: dcmstatus.OutOfResources, SubOperations: dimse.SubOperations{Present: true}}})
		if out[0].Status != dcmstatus.OutOfResources {
			t.Fatalf("status 0x%04X", out[0].Status)
		}
		if out[0].SubOperations.Completed != 1 {
			t.Fatalf("counts=%+v", out[0].SubOperations)
		}
	})

	t.Run("pending only gains a final response", func(t *testing.T) {
		t.Parallel()
		ops := newSubOperations(1)
		ops.record("1.2.1", nil, dimse.StatusSuccess)

		out := ops.reconcile([]RetrieveMatch{{Status: dimse.StatusPending}})
		if len(out) != 2 {
			t.Fatalf("responses=%d", len(out))
		}
		if out[1].Status != dimse.StatusSuccess || out[1].SubOperations.Completed != 1 {
			t.Fatalf("final=%+v", out[1])
		}
	})

	t.Run("no stores passes through", func(t *testing.T) {
		t.Parallel()
		in := []RetrieveMatch{{Status: dimse.StatusPending, SubOperations: dimse.SubOperations{Completed: 7, Present: true}}}
		out := newSubOperations(0).reconcile(in)
		if len(out) != 1 || out[0].SubOperations.Completed != 7 {
			t.Fatalf("handler counts replaced: %+v", out)
		}
	})

	t.Run("caller slice untouched", func(t *testing.T) {
		t.Parallel()
		ops := newSubOperations(1)
		ops.record("1.2.1", nil, 0xA700)
		in := []RetrieveMatch{{Status: dimse.StatusSuccess}}
		_ = ops.reconcile(in)
		if in[0].Status != dimse.StatusSuccess {
			t.Fatalf("reconcile mutated the caller's slice: 0x%04X", in[0].Status)
		}
	})
}
