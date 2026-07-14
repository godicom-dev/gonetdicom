package dimse

import "github.com/godicom-dev/gonetdicom/status"

// DIMSE status aliases — prefer the status package for new code.
// Kept here so existing dimse.Status* call sites stay stable.
const (
	StatusSuccess        = status.Success
	StatusPending        = status.Pending
	StatusPendingWarning = status.PendingWarning
	StatusCancel         = status.Cancel
	// StatusWarning is C-GET/C-MOVE complete with one or more failures (0xB000).
	StatusWarning = status.OneOrMoreFailures
)

// IsPending reports whether status is Pending or Pending Warning.
func IsPending(code uint16) bool {
	return status.IsPending(code)
}
