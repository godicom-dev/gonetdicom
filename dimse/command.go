// Package dimse encodes and decodes DIMSE command sets (Implicit VR Little Endian).
package dimse

import (
	"encoding/binary"
	"fmt"
)

// Command field values (PS3.7).
const (
	CommandCEchoRQ  uint16 = 0x0030
	CommandCEchoRSP uint16 = 0x8030
)

// CommandDataSetTypeNone means no dataset follows the command (0x0101).
const CommandDataSetTypeNone uint16 = 0x0101

// VerificationSOPClass is the Verification SOP Class UID.
const VerificationSOPClass = "1.2.840.10008.1.1"

// StatusSuccess is DIMSE status 0x0000.
const StatusSuccess uint16 = 0x0000

// CEchoRQ is a C-ECHO-RQ command.
type CEchoRQ struct {
	MessageID           uint16
	AffectedSOPClassUID string
}

// Encode returns the Implicit VR LE command set (without Message Control Header).
func (m *CEchoRQ) Encode() ([]byte, error) {
	sop := m.AffectedSOPClassUID
	if sop == "" {
		sop = VerificationSOPClass
	}
	body := encodeElements(
		elemUI(0x0002, sop),
		elemUS(0x0100, CommandCEchoRQ),
		elemUS(0x0110, m.MessageID),
		elemUS(0x0800, CommandDataSetTypeNone),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCEchoRQ parses a C-ECHO-RQ command set.
func DecodeCEchoRQ(b []byte) (*CEchoRQ, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CEchoRQ{AffectedSOPClassUID: VerificationSOPClass}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCEchoRQ {
				return nil, fmt.Errorf("dimse: not C-ECHO-RQ (0x%04x)", v)
			}
		case 0x00000110:
			m.MessageID = asUS(e.value)
		}
	}
	return m, nil
}

// CEchoRSP is a C-ECHO-RSP command.
type CEchoRSP struct {
	MessageIDBeingRespondedTo uint16
	AffectedSOPClassUID       string
	Status                    uint16
}

// Encode returns the Implicit VR LE command set.
func (m *CEchoRSP) Encode() ([]byte, error) {
	sop := m.AffectedSOPClassUID
	if sop == "" {
		sop = VerificationSOPClass
	}
	body := encodeElements(
		elemUI(0x0002, sop),
		elemUS(0x0100, CommandCEchoRSP),
		elemUS(0x0120, m.MessageIDBeingRespondedTo),
		elemUS(0x0800, CommandDataSetTypeNone),
		elemUS(0x0900, m.Status),
	)
	return withCommandGroupLength(body), nil
}

// DecodeCEchoRSP parses a C-ECHO-RSP command set.
func DecodeCEchoRSP(b []byte) (*CEchoRSP, error) {
	els, err := decodeElements(b)
	if err != nil {
		return nil, err
	}
	m := &CEchoRSP{AffectedSOPClassUID: VerificationSOPClass}
	for _, e := range els {
		switch e.tag {
		case 0x00000002:
			m.AffectedSOPClassUID = trimUID(e.value)
		case 0x00000100:
			if v := asUS(e.value); v != CommandCEchoRSP {
				return nil, fmt.Errorf("dimse: not C-ECHO-RSP (0x%04x)", v)
			}
		case 0x00000120:
			m.MessageIDBeingRespondedTo = asUS(e.value)
		case 0x00000900:
			m.Status = asUS(e.value)
		}
	}
	return m, nil
}

type element struct {
	tag   uint32
	value []byte
}

func elemUS(elem uint16, v uint16) element {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	return element{tag: uint32(elem), value: b}
}

func elemUI(elem uint16, uid string) element {
	b := []byte(uid)
	if len(b)%2 == 1 {
		b = append(b, 0x00)
	}
	return element{tag: uint32(elem), value: b}
}

func encodeElements(els ...element) []byte {
	var out []byte
	for _, e := range els {
		out = append(out, encodeElement(e)...)
	}
	return out
}

func encodeElement(e element) []byte {
	out := make([]byte, 8+len(e.value))
	binary.LittleEndian.PutUint16(out[0:2], 0x0000) // group
	binary.LittleEndian.PutUint16(out[2:4], uint16(e.tag))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(e.value)))
	copy(out[8:], e.value)
	return out
}

func withCommandGroupLength(body []byte) []byte {
	// (0000,0000) UL length-of-following-command-elements
	hdr := make([]byte, 12)
	binary.LittleEndian.PutUint16(hdr[0:2], 0x0000)
	binary.LittleEndian.PutUint16(hdr[2:4], 0x0000)
	binary.LittleEndian.PutUint32(hdr[4:8], 4)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(body)))
	return append(hdr, body...)
}

func decodeElements(b []byte) ([]element, error) {
	var els []element
	off := 0
	for off+8 <= len(b) {
		group := binary.LittleEndian.Uint16(b[off : off+2])
		elem := binary.LittleEndian.Uint16(b[off+2 : off+4])
		vlen := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		if off+8+vlen > len(b) {
			return nil, fmt.Errorf("dimse: truncated element (%04X,%04X)", group, elem)
		}
		val := append([]byte(nil), b[off+8:off+8+vlen]...)
		els = append(els, element{tag: uint32(group)<<16 | uint32(elem), value: val})
		off += 8 + vlen
	}
	if off != len(b) {
		return nil, fmt.Errorf("dimse: trailing %d bytes", len(b)-off)
	}
	return els, nil
}

func asUS(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func trimUID(b []byte) string {
	for len(b) > 0 && b[len(b)-1] == 0x00 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// CommandHasDataset reports whether Command Data Set Type indicates a dataset follows.
func CommandHasDataset(cmd []byte) (bool, error) {
	els, err := decodeElements(cmd)
	if err != nil {
		return false, err
	}
	for _, e := range els {
		if e.tag == 0x00000800 {
			return asUS(e.value) != CommandDataSetTypeNone, nil
		}
	}
	return false, nil
}
