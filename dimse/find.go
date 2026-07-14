package dimse

import "fmt"

// Command field values for C-FIND (PS3.7).
const (
	CommandCFindRQ  uint16 = 0x0020
	CommandCFindRSP uint16 = 0x8020
)

// CFindRQ is a C-FIND-RQ command.
type CFindRQ struct {
	MessageID           uint16
	Priority            uint16
	AffectedSOPClassUID string
}

// Encode returns the Implicit VR LE command set.
func (m *CFindRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-FIND-RQ missing Affected SOP Class UID")
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCFindRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0700, m.Priority),
		elemUS(0x0800, CommandDataSetTypePresent),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCFindRQ parses a C-FIND-RQ command set.
func DecodeCFindRQ(b []byte) (*CFindRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CFindRQ{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCFindRQ {
				return nil, fmt.Errorf("dimse: not C-FIND-RQ (0x%04x)", v)
			}
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000700:
			m.Priority = asUS(e.value)
		}
	}
	return m, nil
}

// CFindRSP is a C-FIND-RSP command.
type CFindRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	Status                    uint16
	HasDataset                bool
}

// Encode returns the Implicit VR LE command set.
func (m *CFindRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-FIND-RSP missing Affected SOP Class UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCFindRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCFindRSP parses a C-FIND-RSP command set.
func DecodeCFindRSP(b []byte) (*CFindRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CFindRSP{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCFindRSP {
				return nil, fmt.Errorf("dimse: not C-FIND-RSP (0x%04x)", v)
			}
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00000900:
			m.Status = asUS(e.value)
		}
	}
	return m, nil
}
