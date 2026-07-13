package dimse

import "fmt"

// DIMSE-N command field values (PS3.7).
const (
	CommandNEventReportRQ  uint16 = 0x0100
	CommandNEventReportRSP uint16 = 0x8100
	CommandNActionRQ       uint16 = 0x0130
	CommandNActionRSP      uint16 = 0x8130
)

// Storage Commitment Push Model (PS3.4 Annex J).
const (
	StorageCommitmentPushModelSOPClass           = "1.2.840.10008.1.20.1"
	StorageCommitmentPushModelSOPInstance        = "1.2.840.10008.1.20.1.1"
	StorageCommitmentActionTypeRequest    uint16 = 1
	StorageCommitmentEventTypeSuccess     uint16 = 1
	StorageCommitmentEventTypeFailures    uint16 = 2
)

// NActionRQ is an N-ACTION-RQ command.
type NActionRQ struct {
	MessageID               uint16
	ActionTypeID            uint16
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	HasDataset              bool
}

// Encode returns the Implicit VR LE command set.
func (m *NActionRQ) Encode() ([]byte, error) {
	if m.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-ACTION-RQ missing Requested SOP Class UID")
	}
	if m.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-ACTION-RQ missing Requested SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0003, m.RequestedSOPClassUID),
		elemUS(0x0100, CommandNActionRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, dst),
		elemUI(0x1001, m.RequestedSOPInstanceUID),
		elemUS(0x1008, m.ActionTypeID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNActionRQ parses an N-ACTION-RQ command set.
func DecodeNActionRQ(b []byte) (*NActionRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NActionRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000003:
			m.RequestedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNActionRQ {
				return nil, fmt.Errorf("dimse: not N-ACTION-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00001001:
			m.RequestedSOPInstanceUID = trimUID(e.value)
		case 0x00001008:
			m.ActionTypeID = asUS(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-ACTION-RQ")
	}
	return m, nil
}

// NActionRSP is an N-ACTION-RSP command.
type NActionRSP struct {
	MessageIDBeingRespondedTo uint16
	ActionTypeID              uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *NActionRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-ACTION-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-ACTION-RSP missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNActionRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
		elemUS(0x1008, m.ActionTypeID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNActionRSP parses an N-ACTION-RSP command set.
func DecodeNActionRSP(b []byte) (*NActionRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NActionRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNActionRSP {
				return nil, fmt.Errorf("dimse: not N-ACTION-RSP (0x%04x)", v)
			}
			sawField = true
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		case 0x00001008:
			m.ActionTypeID = asUS(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-ACTION-RSP")
	}
	return m, nil
}

// NEventReportRQ is an N-EVENT-REPORT-RQ command.
type NEventReportRQ struct {
	MessageID              uint16
	EventTypeID            uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	HasDataset             bool
}

// Encode returns the Implicit VR LE command set.
func (m *NEventReportRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-EVENT-REPORT-RQ missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-EVENT-REPORT-RQ missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNEventReportRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, dst),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
		elemUS(0x1002, m.EventTypeID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNEventReportRQ parses an N-EVENT-REPORT-RQ command set.
func DecodeNEventReportRQ(b []byte) (*NEventReportRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NEventReportRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNEventReportRQ {
				return nil, fmt.Errorf("dimse: not N-EVENT-REPORT-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		case 0x00001002:
			m.EventTypeID = asUS(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-EVENT-REPORT-RQ")
	}
	return m, nil
}

// NEventReportRSP is an N-EVENT-REPORT-RSP command.
type NEventReportRSP struct {
	MessageIDBeingRespondedTo uint16
	EventTypeID               uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *NEventReportRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-EVENT-REPORT-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-EVENT-REPORT-RSP missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNEventReportRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
		elemUS(0x1002, m.EventTypeID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNEventReportRSP parses an N-EVENT-REPORT-RSP command set.
func DecodeNEventReportRSP(b []byte) (*NEventReportRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NEventReportRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNEventReportRSP {
				return nil, fmt.Errorf("dimse: not N-EVENT-REPORT-RSP (0x%04x)", v)
			}
			sawField = true
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		case 0x00001002:
			m.EventTypeID = asUS(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-EVENT-REPORT-RSP")
	}
	return m, nil
}
