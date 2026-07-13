package dimse

import (
	"fmt"
	"strings"
)

// Command field values for C-STORE (PS3.7).
const (
	CommandCStoreRQ  uint16 = 0x0001
	CommandCStoreRSP uint16 = 0x8001
)

// Priority values for C-STORE / C-FIND / C-MOVE / C-GET.
const (
	PriorityMedium uint16 = 0x0000
	PriorityHigh   uint16 = 0x0001
	PriorityLow    uint16 = 0x0002
)

// CommandDataSetTypePresent means a dataset follows the command (typically 0x0001).
const CommandDataSetTypePresent uint16 = 0x0001

// CStoreRQ is a C-STORE-RQ command.
type CStoreRQ struct {
	MessageID                            uint16
	Priority                             uint16
	AffectedSOPClassUID                  string
	AffectedSOPInstanceUID               string
	MoveOriginatorApplicationEntityTitle string
	MoveOriginatorMessageID              uint16
	HasMoveOriginator                    bool
}

// Encode returns the Implicit VR LE command set (without Message Control Header).
func (m *CStoreRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-STORE-RQ missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: C-STORE-RQ missing Affected SOP Instance UID")
	}
	els := []element{
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCStoreRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0700, m.Priority),
		elemUS(0x0800, CommandDataSetTypePresent),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	}
	if m.HasMoveOriginator || m.MoveOriginatorApplicationEntityTitle != "" {
		els = append(els, elemAE(0x1030, m.MoveOriginatorApplicationEntityTitle))
		els = append(els, elemUS(0x1031, m.MoveOriginatorMessageID))
	}
	return withCommandGroupLength(encodeElements(els...)), nil
}

// DecodeCStoreRQ parses a C-STORE-RQ command set.
func DecodeCStoreRQ(b []byte) (*CStoreRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CStoreRQ{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCStoreRQ {
				return nil, fmt.Errorf("dimse: not C-STORE-RQ (0x%04x)", v)
			}
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000700:
			m.Priority = asUS(e.value)
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		case 0x00001030:
			m.MoveOriginatorApplicationEntityTitle = trimAE(e.value)
			m.HasMoveOriginator = true
		case 0x00001031:
			m.MoveOriginatorMessageID = asUS(e.value)
			m.HasMoveOriginator = true
		}
	}
	return m, nil
}

// CStoreRSP is a C-STORE-RSP command.
type CStoreRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	AffectedSOPInstanceUID    string
	Status                    uint16
}

// Encode returns the Implicit VR LE command set.
func (m *CStoreRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-STORE-RSP missing Affected SOP Class UID")
	}
	if m.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("dimse: C-STORE-RSP missing Affected SOP Instance UID")
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCStoreRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, CommandDataSetTypeNone),
		elemUS(0x0900, m.Status),
		elemUI(0x1000, m.AffectedSOPInstanceUID),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCStoreRSP parses a C-STORE-RSP command set.
func DecodeCStoreRSP(b []byte) (*CStoreRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CStoreRSP{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCStoreRSP {
				return nil, fmt.Errorf("dimse: not C-STORE-RSP (0x%04x)", v)
			}
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001000:
			m.AffectedSOPInstanceUID = trimUID(e.value)
		}
	}
	return m, nil
}

func elemAE(elem uint16, ae string) element {
	b := []byte(ae)
	if len(b)%2 == 1 {
		b = append(b, ' ')
	}
	return element{tag: uint32(elem), value: b}
}

func trimAE(b []byte) string {
	return strings.TrimRight(string(b), " ")
}
