package dimse

import "fmt"

// PeekCommandField returns Command Field (0000,0100) from a command set.
func PeekCommandField(cmd []byte) (uint16, error) {
	els, err := decodeElements(cmd)
	if err != nil {
		return 0, err
	}
	for _, e := range els {
		if e.tag == 0x00000100 {
			return asUS(e.value), nil
		}
	}
	return 0, fmt.Errorf("dimse: missing Command Field")
}

// PeekMessageID returns Message ID (0000,0110) when present.
func PeekMessageID(cmd []byte) (uint16, bool) {
	els, err := decodeElements(cmd)
	if err != nil {
		return 0, false
	}
	for _, e := range els {
		if e.tag == 0x00000110 {
			return asUS(e.value), true
		}
	}
	return 0, false
}

// PeekStatus returns Status (0000,0900) when present.
func PeekStatus(cmd []byte) (uint16, bool) {
	els, err := decodeElements(cmd)
	if err != nil {
		return 0, false
	}
	for _, e := range els {
		if e.tag == 0x00000900 {
			return asUS(e.value), true
		}
	}
	return 0, false
}

// CommandName returns a short name for a DIMSE Command Field value.
func CommandName(field uint16) string {
	switch field {
	case CommandCStoreRQ:
		return "C-STORE-RQ"
	case CommandCStoreRSP:
		return "C-STORE-RSP"
	case CommandCGetRQ:
		return "C-GET-RQ"
	case CommandCGetRSP:
		return "C-GET-RSP"
	case CommandCFindRQ:
		return "C-FIND-RQ"
	case CommandCFindRSP:
		return "C-FIND-RSP"
	case CommandCMoveRQ:
		return "C-MOVE-RQ"
	case CommandCMoveRSP:
		return "C-MOVE-RSP"
	case CommandCEchoRQ:
		return "C-ECHO-RQ"
	case CommandCEchoRSP:
		return "C-ECHO-RSP"
	case CommandCCancelRQ:
		return "C-CANCEL-RQ"
	case CommandNEventReportRQ:
		return "N-EVENT-REPORT-RQ"
	case CommandNEventReportRSP:
		return "N-EVENT-REPORT-RSP"
	case CommandNGetRQ:
		return "N-GET-RQ"
	case CommandNGetRSP:
		return "N-GET-RSP"
	case CommandNSetRQ:
		return "N-SET-RQ"
	case CommandNSetRSP:
		return "N-SET-RSP"
	case CommandNActionRQ:
		return "N-ACTION-RQ"
	case CommandNActionRSP:
		return "N-ACTION-RSP"
	case CommandNCreateRQ:
		return "N-CREATE-RQ"
	case CommandNCreateRSP:
		return "N-CREATE-RSP"
	case CommandNDeleteRQ:
		return "N-DELETE-RQ"
	case CommandNDeleteRSP:
		return "N-DELETE-RSP"
	default:
		return fmt.Sprintf("CMD-0x%04X", field)
	}
}

// FormatStatus returns a hex status string for logs.
func FormatStatus(status uint16) string {
	return fmt.Sprintf("0x%04x", status)
}
