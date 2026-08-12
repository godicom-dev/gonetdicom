package pdu

import "fmt"

// TypeName returns a short name for a PDU type byte (for debug logs).
func TypeName(t byte) string {
	switch t {
	case TypeAAssociateRQ:
		return "A-ASSOCIATE-RQ"
	case TypeAAssociateAC:
		return "A-ASSOCIATE-AC"
	case TypeAAssociateRJ:
		return "A-ASSOCIATE-RJ"
	case TypePDataTF:
		return "P-DATA-TF"
	case TypeAReleaseRQ:
		return "A-RELEASE-RQ"
	case TypeAReleaseRP:
		return "A-RELEASE-RP"
	case TypeAAbort:
		return "A-ABORT"
	default:
		return fmt.Sprintf("PDU-0x%02X", t)
	}
}
