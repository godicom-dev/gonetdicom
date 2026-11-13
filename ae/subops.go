package ae

import (
	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// The Query/Retrieve services report how a C-GET or C-MOVE went through the
// four Sub-Operation counts plus one aggregate status (PS3.4 C.4.3.1.3.2). The
// counts belong to the SCP: they describe the C-STORE sub-operations it
// performed, so a handler cannot supply them itself — it only decides which
// instances take part.
//
// subOperations is that tally. It is not safe for concurrent use; the C-MOVE
// path guards it with the mutex it already holds for its Pending responses.

// statusCategory is the coarse classification the service class applies to a
// C-STORE sub-operation response.
type statusCategory int

const (
	statusSuccess statusCategory = iota
	statusWarning
	statusFailure
)

// categorizeStorageStatus maps a C-STORE-RSP status onto its category.
//
// Anything that is neither Success nor in the warning range is a failure. That
// is what pynetdicom does by construction: it looks the status up in
// STORAGE_SERVICE_CLASS_STATUS and a status missing from that table raises,
// which the service class counts as a failed sub-operation.
func categorizeStorageStatus(status uint16) statusCategory {
	switch {
	case status == dimse.StatusSuccess:
		return statusSuccess
	case status >= 0xB000 && status <= 0xBFFF:
		return statusWarning
	default:
		return statusFailure
	}
}

type subOperations struct {
	total      int
	completed  int
	failed     int
	warning    int
	failedUIDs []string
}

func newSubOperations(total int) *subOperations {
	return &subOperations{total: total}
}

// record tallies one attempted C-STORE sub-operation. A transport error is a
// failure regardless of the status, which carries no meaning then.
func (s *subOperations) record(sopInstanceUID string, err error, status uint16) {
	if err == nil {
		switch categorizeStorageStatus(status) {
		case statusSuccess:
			s.completed++
			return
		case statusWarning:
			s.warning++
			return
		}
	}
	s.failed++
	// PS3.4 C.4.3.1.3.2: only failed instances go in the list; a warning means
	// the instance arrived, so it is not a failure to report.
	if sopInstanceUID != "" {
		s.failedUIDs = append(s.failedUIDs, sopInstanceUID)
	}
}

// recordMissing counts sub-operations that were never attempted. A Move
// Destination that stops accepting associations leaves the rest of the batch
// untried; the service class reports those as failed rather than silently
// dropping them, so the counts still add up to the number of matches.
func (s *subOperations) recordMissing() {
	if attempted := s.completed + s.failed + s.warning; attempted < s.total {
		s.failed += s.total - attempted
	}
}

func (s *subOperations) attempted() int {
	return s.completed + s.failed + s.warning
}

// counts is the wire form, with each count narrowed to its US field.
func (s *subOperations) counts() dimse.SubOperations {
	return dimse.SubOperations{
		Remaining: 0,
		Completed: subOpCount(s.completed),
		Failed:    subOpCount(s.failed),
		Warning:   subOpCount(s.warning),
		Present:   true,
	}
}

// finalStatus is the aggregate the SCP answers with: Success when nothing went
// wrong, 0xA702 when every sub-operation failed, 0xB000 in between.
//
// The middle case is where the distinction between a failure and a warning
// shows: a batch of nothing but warnings is a warning, not "unable to perform
// sub-operations" — the instances did arrive, merely not unmodified.
func (s *subOperations) finalStatus() uint16 {
	switch {
	case s.failed == 0 && s.warning == 0:
		return dimse.StatusSuccess
	case s.failed == s.total:
		return dcmstatus.UnableToPerformSubOperations
	default:
		return dimse.StatusWarning
	}
}

// failedIdentifier is the Failed SOP Instance UID List (0008,0058) that a
// Warning or Failure final response carries. Nil when nothing failed, so a
// warning-only outcome sends no identifier rather than an empty list.
func (s *subOperations) failedIdentifier() *godicom.Dataset {
	if len(s.failedUIDs) == 0 {
		return nil
	}
	uids := make([]string, len(s.failedUIDs))
	copy(uids, s.failedUIDs)
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("FailedSOPInstanceUIDList"), godicom.VRUI, uids))
	return ds
}

// reconcile folds the sub-operation results into the handler's responses for
// a C-GET, whose handler yields the responses the SCP sends.
//
// The final (non-pending) response defers to reality: when the handler yielded
// Success but a sub-operation failed or warned, the status becomes the
// aggregate — pynetdicom makes the same substitution rather than letting a
// handler claim a clean transfer. A final response the handler deliberately
// marked Warning or Failure keeps its status; only the counts and the failed
// instance list are filled in, because those are the framework's to report.
//
// With no stores planned there is nothing to account for, and the handler's
// responses pass through untouched.
func (s *subOperations) reconcile(responses []RetrieveMatch) []RetrieveMatch {
	if s.total == 0 {
		return responses
	}
	out := responses
	final := -1
	for i, m := range out {
		if !dimse.IsPending(m.Status) {
			final = i
			break
		}
	}
	if final < 0 {
		// Every response was Pending: without a final one the SCU waits for a
		// completion that never arrives.
		out = append(out, RetrieveMatch{Status: dimse.StatusSuccess})
		final = len(out) - 1
	}
	out = append([]RetrieveMatch(nil), out...)

	m := &out[final]
	if categorizeStorageStatus(m.Status) == statusSuccess {
		m.Status = s.finalStatus()
	}
	m.SubOperations = s.counts()
	if categorizeStorageStatus(m.Status) != statusSuccess && m.Identifier == nil {
		m.Identifier = s.failedIdentifier()
	}
	return out
}
