package dimse

import "fmt"

// CommandCCancelRQ is the C-CANCEL-RQ Command Field (PS3.7).
const CommandCCancelRQ uint16 = 0x0FFF

// CCancelRQ is a C-CANCEL-RQ command (no dataset).
// Aligned with pynetdicom C_CANCEL / C_CANCEL_RQ.
type CCancelRQ struct {
	MessageIDBeingRespondedTo uint16
}

// Encode returns the Implicit VR LE command set.
func (m *CCancelRQ) Encode() ([]byte, error) {
	body := encodeElements(
		elemUS(0x0100, CommandCCancelRQ),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, CommandDataSetTypeNone),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCCancelRQ parses a C-CANCEL-RQ command set.
func DecodeCCancelRQ(b []byte) (*CCancelRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CCancelRQ{}
	var sawField bool
	for _, e := range els {
		switch e.tag {
		case 0x00000100:
			if v := asUS(e.value); v != CommandCCancelRQ {
				return nil, fmt.Errorf("dimse: not C-CANCEL-RQ (0x%04x)", v)
			}
			sawField = true
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		}
	}
	if !sawField {
		return nil, fmt.Errorf("dimse: not C-CANCEL-RQ")
	}
	return m, nil
}
