package dimse

import "fmt"

// Command field values for C-MOVE / C-GET (PS3.7).
const (
	CommandCGetRQ   uint16 = 0x0010
	CommandCGetRSP  uint16 = 0x8010
	CommandCMoveRQ  uint16 = 0x0021
	CommandCMoveRSP uint16 = 0x8021
)

// StatusWarning is sub-operations complete with one or more failures (0xB000).
const StatusWarning uint16 = 0xB000

// SubOperations counts optional C-GET/C-MOVE response fields.
type SubOperations struct {
	Remaining uint16
	Completed uint16
	Failed    uint16
	Warning   uint16
	Present   bool // when true, all four counts are encoded
}

// CMoveRQ is a C-MOVE-RQ command.
type CMoveRQ struct {
	MessageID           uint16
	Priority            uint16
	AffectedSOPClassUID string
	MoveDestination     string
}

// Encode returns the Implicit VR LE command set (elements in tag order).
func (m *CMoveRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-MOVE-RQ missing Affected SOP Class UID")
	}
	if m.MoveDestination == "" {
		return nil, fmt.Errorf("dimse: C-MOVE-RQ missing Move Destination")
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCMoveRQ),
		elemUS(0x0110, m.MessageID),
		elemAE(0x0600, m.MoveDestination),
		elemUS(0x0700, m.Priority),
		elemUS(0x0800, CommandDataSetTypePresent),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCMoveRQ parses a C-MOVE-RQ command set.
func DecodeCMoveRQ(b []byte) (*CMoveRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CMoveRQ{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCMoveRQ {
				return nil, fmt.Errorf("dimse: not C-MOVE-RQ (0x%04x)", v)
			}
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000600:
			m.MoveDestination = trimAE(e.value)
		case 0x00000700:
			m.Priority = asUS(e.value)
		}
	}
	return m, nil
}

// CMoveRSP is a C-MOVE-RSP command.
type CMoveRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	Status                    uint16
	HasDataset                bool
	SubOperations             SubOperations
}

// Encode returns the Implicit VR LE command set.
func (m *CMoveRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-MOVE-RSP missing Affected SOP Class UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	els := []element{
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCMoveRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
	}
	if m.SubOperations.Present {
		els = append(els,
			elemUS(0x1020, m.SubOperations.Remaining),
			elemUS(0x1021, m.SubOperations.Completed),
			elemUS(0x1022, m.SubOperations.Failed),
			elemUS(0x1023, m.SubOperations.Warning),
		)
	}
	return withCommandGroupLength(encodeElements(els...)), nil
}

// DecodeCMoveRSP parses a C-MOVE-RSP command set.
func DecodeCMoveRSP(b []byte) (*CMoveRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CMoveRSP{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCMoveRSP {
				return nil, fmt.Errorf("dimse: not C-MOVE-RSP (0x%04x)", v)
			}
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001020:
			m.SubOperations.Remaining = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001021:
			m.SubOperations.Completed = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001022:
			m.SubOperations.Failed = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001023:
			m.SubOperations.Warning = asUS(e.value)
			m.SubOperations.Present = true
		}
	}
	return m, nil
}

// CGetRQ is a C-GET-RQ command.
type CGetRQ struct {
	MessageID           uint16
	Priority            uint16
	AffectedSOPClassUID string
}

// Encode returns the Implicit VR LE command set.
func (m *CGetRQ) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-GET-RQ missing Affected SOP Class UID")
	}
	body := encodeElements(
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCGetRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0700, m.Priority),
		elemUS(0x0800, CommandDataSetTypePresent),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCGetRQ parses a C-GET-RQ command set.
func DecodeCGetRQ(b []byte) (*CGetRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CGetRQ{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCGetRQ {
				return nil, fmt.Errorf("dimse: not C-GET-RQ (0x%04x)", v)
			}
		case 0x00000110:
			m.MessageID = asUS(e.value)
		case 0x00000700:
			m.Priority = asUS(e.value)
		}
	}
	return m, nil
}

// CGetRSP is a C-GET-RSP command.
type CGetRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	Status                    uint16
	HasDataset                bool
	SubOperations             SubOperations
}

// Encode returns the Implicit VR LE command set.
func (m *CGetRSP) Encode() ([]byte, error) {
	if m.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("dimse: C-GET-RSP missing Affected SOP Class UID")
	}
	dst := CommandDataSetTypeNone
	if m.HasDataset {
		dst = CommandDataSetTypePresent
	}
	els := []element{
		elemUI(0x0002, m.AffectedSOPClassUID),
		elemUS(0x0100, CommandCGetRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, dst),
		elemUS(0x0900, m.Status),
	}
	if m.SubOperations.Present {
		els = append(els,
			elemUS(0x1020, m.SubOperations.Remaining),
			elemUS(0x1021, m.SubOperations.Completed),
			elemUS(0x1022, m.SubOperations.Failed),
			elemUS(0x1023, m.SubOperations.Warning),
		)
	}
	return withCommandGroupLength(encodeElements(els...)), nil
}

// DecodeCGetRSP parses a C-GET-RSP command set.
func DecodeCGetRSP(b []byte) (*CGetRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CGetRSP{}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCGetRSP {
				return nil, fmt.Errorf("dimse: not C-GET-RSP (0x%04x)", v)
			}
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000800:
			m.HasDataset = asUS(e.value) != CommandDataSetTypeNone
		case 0x00000900:
			m.Status = asUS(e.value)
		case 0x00001020:
			m.SubOperations.Remaining = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001021:
			m.SubOperations.Completed = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001022:
			m.SubOperations.Failed = asUS(e.value)
			m.SubOperations.Present = true
		case 0x00001023:
			m.SubOperations.Warning = asUS(e.value)
			m.SubOperations.Present = true
		}
	}
	return m, nil
}

// IsFinalQRStatus reports whether status ends a C-FIND/C-GET/C-MOVE operation.
func IsFinalQRStatus(status uint16) bool {
	return !IsPending(status)
}
