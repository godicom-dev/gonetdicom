package dimse_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/status"
)

// CommandName is what logs and error messages say when a peer sends something
// unexpected; a missing case turns 0x8001 into "0x8001" in the one message an
// operator will read.
func TestCommandNameCoversEveryCommandField(t *testing.T) {
	cases := map[uint16]string{
		dimse.CommandCStoreRQ:        "C-STORE-RQ",
		dimse.CommandCStoreRSP:       "C-STORE-RSP",
		dimse.CommandCGetRQ:          "C-GET-RQ",
		dimse.CommandCGetRSP:         "C-GET-RSP",
		dimse.CommandCFindRQ:         "C-FIND-RQ",
		dimse.CommandCFindRSP:        "C-FIND-RSP",
		dimse.CommandCMoveRQ:         "C-MOVE-RQ",
		dimse.CommandCMoveRSP:        "C-MOVE-RSP",
		dimse.CommandCEchoRQ:         "C-ECHO-RQ",
		dimse.CommandCEchoRSP:        "C-ECHO-RSP",
		dimse.CommandCCancelRQ:       "C-CANCEL-RQ",
		dimse.CommandNEventReportRQ:  "N-EVENT-REPORT-RQ",
		dimse.CommandNEventReportRSP: "N-EVENT-REPORT-RSP",
		dimse.CommandNGetRQ:          "N-GET-RQ",
		dimse.CommandNGetRSP:         "N-GET-RSP",
		dimse.CommandNSetRQ:          "N-SET-RQ",
		dimse.CommandNSetRSP:         "N-SET-RSP",
		dimse.CommandNActionRQ:       "N-ACTION-RQ",
		dimse.CommandNActionRSP:      "N-ACTION-RSP",
		dimse.CommandNCreateRQ:       "N-CREATE-RQ",
		dimse.CommandNCreateRSP:      "N-CREATE-RSP",
		dimse.CommandNDeleteRQ:       "N-DELETE-RQ",
		dimse.CommandNDeleteRSP:      "N-DELETE-RSP",
	}
	for field, want := range cases {
		if got := dimse.CommandName(field); got != want {
			t.Errorf("CommandName(0x%04x) = %q, want %q", field, got, want)
		}
	}
	if got := dimse.CommandName(0x7FFF); got == "" {
		t.Error("an unknown command field produced an empty name")
	}
}

// IsPending splits the three pending codes from every terminal one. The split
// is what decides whether a C-FIND/C-GET/C-MOVE caller keeps reading, so an
// off-by-one here reads one response too many or stops a series early.
func TestIsPendingClassification(t *testing.T) {
	pending := []uint16{status.Pending, status.PendingWarning, 0xFF00, 0xFF01}
	for _, code := range pending {
		if !dimse.IsPending(code) {
			t.Errorf("IsPending(0x%04x) = false, want true", code)
		}
	}
	terminal := []uint16{status.Success, status.OneOrMoreFailures, 0xA700, 0x0122, 0xFE00}
	for _, code := range terminal {
		if dimse.IsPending(code) {
			t.Errorf("IsPending(0x%04x) = true, want false", code)
		}
	}
}

func TestIsFinalQRStatusIsTheComplementOfPending(t *testing.T) {
	for _, code := range []uint16{status.Success, status.Pending, status.Cancel, status.OneOrMoreFailures, 0xA801} {
		if got, want := dimse.IsFinalQRStatus(code), !dimse.IsPending(code); got != want {
			t.Errorf("IsFinalQRStatus(0x%04x) = %v, want %v", code, got, want)
		}
	}
}
