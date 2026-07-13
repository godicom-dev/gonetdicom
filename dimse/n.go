package dimse

import (
	"encoding/binary"
	"fmt"
)

// DIMSE-N command field values (PS3.7).
const (
	CommandNEventReportRQ  uint16 = 0x0100
	CommandNEventReportRSP uint16 = 0x8100
	CommandNGetRQ          uint16 = 0x0110
	CommandNGetRSP         uint16 = 0x8110
	CommandNSetRQ          uint16 = 0x0120
	CommandNSetRSP         uint16 = 0x8120
	CommandNActionRQ       uint16 = 0x0130
	CommandNActionRSP      uint16 = 0x8130
	CommandNCreateRQ       uint16 = 0x0140
	CommandNCreateRSP      uint16 = 0x8140
	CommandNDeleteRQ       uint16 = 0x0150
	CommandNDeleteRSP      uint16 = 0x8150
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

// Tag is a DICOM data element tag (group<<16 | element), little-endian on the wire for AT.
type Tag uint32

// NGetRQ is an N-GET-RQ command.
type NGetRQ struct {
	MessageID               uint16
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	AttributeIdentifierList []Tag
}

// Encode returns the Implicit VR LE command set.
func (m *NGetRQ) Encode() ([]byte, error) {
	if m.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-GET-RQ missing Requested SOP Class UID")
	}
	if m.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-GET-RQ missing Requested SOP Instance UID")
	}
	els := []element{
		elemUI(0x0003, m.RequestedSOPClassUID),
		elemUS(0x0100, CommandNGetRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, CommandDataSetTypeNone),
		elemUI(0x1001, m.RequestedSOPInstanceUID),
	}
	if len(m.AttributeIdentifierList) > 0 {
		els = append(els, elemAT(0x1005, m.AttributeIdentifierList))
	}
	return withCommandGroupLength(encodeElements(els...)), nil
}

// DecodeNGetRQ parses an N-GET-RQ command set.
func DecodeNGetRQ(b []byte) (*NGetRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NGetRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000003:
			m.RequestedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNGetRQ {
				return nil, fmt.Errorf("dimse: not N-GET-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00001001:
			m.RequestedSOPInstanceUID = trimUID(e.value)
		case 0x00001005:
			m.AttributeIdentifierList = asATList(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-GET-RQ")
	}
	return m, nil
}

// NGetRSP is an N-GET-RSP command.
type NGetRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *NGetRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-GET-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-GET-RSP missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNGetRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNGetRSP parses an N-GET-RSP command set.
func DecodeNGetRSP(b []byte) (*NGetRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NGetRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNGetRSP {
				return nil, fmt.Errorf("dimse: not N-GET-RSP (0x%04x)", v)
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
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-GET-RSP")
	}
	return m, nil
}

// NSetRQ is an N-SET-RQ command.
type NSetRQ struct {
	MessageID               uint16
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	HasDataset              bool
}

// Encode returns the Implicit VR LE command set.
func (m *NSetRQ) Encode() ([]byte, error) {
	if m.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-SET-RQ missing Requested SOP Class UID")
	}
	if m.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-SET-RQ missing Requested SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0003, m.RequestedSOPClassUID),
		elemUS(0x0100, CommandNSetRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, dst),
		elemUI(0x1001, m.RequestedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNSetRQ parses an N-SET-RQ command set.
func DecodeNSetRQ(b []byte) (*NSetRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NSetRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000003:
			m.RequestedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNSetRQ {
				return nil, fmt.Errorf("dimse: not N-SET-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00001001:
			m.RequestedSOPInstanceUID = trimUID(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-SET-RQ")
	}
	return m, nil
}

// NSetRSP is an N-SET-RSP command.
type NSetRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *NSetRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-SET-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-SET-RSP missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNSetRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNSetRSP parses an N-SET-RSP command set.
func DecodeNSetRSP(b []byte) (*NSetRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NSetRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNSetRSP {
				return nil, fmt.Errorf("dimse: not N-SET-RSP (0x%04x)", v)
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
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-SET-RSP")
	}
	return m, nil
}

// NCreateRQ is an N-CREATE-RQ command.
type NCreateRQ struct {
	MessageID              uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string // optional; empty if SCP assigns
	HasDataset             bool
}

// Encode returns the Implicit VR LE command set.
func (m *NCreateRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-CREATE-RQ missing Affected SOP Class UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	els := []element{
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNCreateRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, dst),
	}
	if m.AffectedSOPInstanceUID != "" {
		els = append(els, elemUI(0x1000, m.AffectedSOPInstanceUID))
	} else {
		els = append(els, element{tag: 0x1000, value: nil})
	}
	return withCommandGroupLength(encodeElements(els...)), nil
}

// DecodeNCreateRQ parses an N-CREATE-RQ command set.
func DecodeNCreateRQ(b []byte) (*NCreateRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NCreateRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNCreateRQ {
				return nil, fmt.Errorf("dimse: not N-CREATE-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-CREATE-RQ")
	}
	return m, nil
}

// NCreateRSP is an N-CREATE-RSP command.
type NCreateRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *NCreateRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-CREATE-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-CREATE-RSP missing Affected SOP Instance UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNCreateRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNCreateRSP parses an N-CREATE-RSP command set.
func DecodeNCreateRSP(b []byte) (*NCreateRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NCreateRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNCreateRSP {
				return nil, fmt.Errorf("dimse: not N-CREATE-RSP (0x%04x)", v)
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
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-CREATE-RSP")
	}
	return m, nil
}

// NDeleteRQ is an N-DELETE-RQ command.
type NDeleteRQ struct {
	MessageID               uint16
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
}

// Encode returns the Implicit VR LE command set.
func (m *NDeleteRQ) Encode() ([]byte, error) {
	if m.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-DELETE-RQ missing Requested SOP Class UID")
	}
	if m.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-DELETE-RQ missing Requested SOP Instance UID")
	}
	body := encodeElements(
		elemUI(0x0003, m.RequestedSOPClassUID),
		elemUS(0x0100, CommandNDeleteRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, CommandDataSetTypeNone),
		elemUI(0x1001, m.RequestedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNDeleteRQ parses an N-DELETE-RQ command set.
func DecodeNDeleteRQ(b []byte) (*NDeleteRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NDeleteRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000003:
			m.RequestedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNDeleteRQ {
				return nil, fmt.Errorf("dimse: not N-DELETE-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00001001:
			m.RequestedSOPInstanceUID = trimUID(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-DELETE-RQ")
	}
	return m, nil
}

// NDeleteRSP is an N-DELETE-RSP command.
type NDeleteRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
}

// Encode returns the Implicit VR LE command set.
func (m *NDeleteRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: N-DELETE-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: N-DELETE-RSP missing Affected SOP Instance UID")
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandNDeleteRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, CommandDataSetTypeNone),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeNDeleteRSP parses an N-DELETE-RSP command set.
func DecodeNDeleteRSP(b []byte) (*NDeleteRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &NDeleteRSP{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandNDeleteRSP {
				return nil, fmt.Errorf("dimse: not N-DELETE-RSP (0x%04x)", v)
			}
			sawField = true
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not N-DELETE-RSP")
	}
	return m, nil
}

func elemAT(elem uint16, tags []Tag) element {
	b := make([]byte, 4*len(tags))
	for i, t := range tags {
		binary.LittleEndian.PutUint16(b[i*4:], uint16(t>>16))
		binary.LittleEndian.PutUint16(b[i*4+2:], uint16(t))
	}
	return element{tag: uint32(elem), value: b}
}

func asATList(b []byte) []Tag {
	out := make([]Tag, 0, len(b)/4)
	for i := 0; i+4 <= len(b); i += 4 {
		g := binary.LittleEndian.Uint16(b[i:])
		e := binary.LittleEndian.Uint16(b[i+2:])
		out = append(out, Tag(uint32(g)<<16|uint32(e)))
	}
	return out
}
