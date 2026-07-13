// Package pdu implements DICOM Upper Layer Protocol Data Units (PS3.8).
//
// Encoding is big-endian. Behaviour aligns with pynetdicom's pdu module;
// golden fixtures live in fixtures_test.go (from pynetdicom tests).
package pdu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// PDU type values (PS3.8 Table 9-1).
const (
	TypeAAssociateRQ = 0x01
	TypeAAssociateAC = 0x02
	TypeAAssociateRJ = 0x03
	TypePDataTF      = 0x04
	TypeAReleaseRQ   = 0x05
	TypeAReleaseRP   = 0x06
	TypeAAbort       = 0x07
)

// Item type values for A-ASSOCIATE variable items.
const (
	ItemApplicationContext     = 0x10
	ItemPresentationContextRQ  = 0x20
	ItemPresentationContextAC  = 0x21
	ItemAbstractSyntax         = 0x30
	ItemTransferSyntax         = 0x40
	ItemUserInformation        = 0x50
	ItemMaxLength              = 0x51
	ItemImplementationClassUID = 0x52
	ItemRoleSelection          = 0x54
	ItemImplementationVersion  = 0x55
)

// Standard UIDs used during association.
const (
	ApplicationContextName = "1.2.840.10008.3.1.1.1"
	VerificationSOPClass   = "1.2.840.10008.1.1"
	ImplicitVRLittleEndian = "1.2.840.10008.1.2"
)

// DefaultMaxPDULength is a common Maximum Length Received default (16382).
const DefaultMaxPDULength = 16382

// ErrUnexpectedType is returned when a PDU type does not match expectations.
var ErrUnexpectedType = errors.New("pdu: unexpected type")

// PDU is a DICOM Upper Layer Protocol Data Unit.
type PDU interface {
	Type() byte
	Encode() ([]byte, error)
}

// Read reads one PDU from r (6-byte header + body).
func Read(r io.Reader) (PDU, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	typ := hdr[0]
	length := binary.BigEndian.Uint32(hdr[2:6])
	body := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("pdu: read body type=0x%02x: %w", typ, err)
		}
	}
	raw := append(hdr[:], body...)
	switch typ {
	case TypeAAssociateRQ:
		return DecodeAAssociateRQ(raw)
	case TypeAAssociateAC:
		return DecodeAAssociateAC(raw)
	case TypeAAssociateRJ:
		return DecodeAAssociateRJ(raw)
	case TypePDataTF:
		return DecodePDataTF(raw)
	case TypeAReleaseRQ:
		return DecodeAReleaseRQ(raw)
	case TypeAReleaseRP:
		return DecodeAReleaseRP(raw)
	case TypeAAbort:
		return DecodeAAbort(raw)
	default:
		return nil, fmt.Errorf("pdu: unknown type 0x%02x", typ)
	}
}

// Write encodes p and writes it to w.
func Write(w io.Writer, p PDU) error {
	b, err := p.Encode()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func encodeHeader(typ byte, body []byte) []byte {
	out := make([]byte, 6+len(body))
	out[0] = typ
	binary.BigEndian.PutUint32(out[2:6], uint32(len(body)))
	copy(out[6:], body)
	return out
}

// PadAETitle returns a 16-byte AE title (trailing spaces).
func PadAETitle(ae string) ([16]byte, error) {
	ae = strings.TrimSpace(ae)
	if ae == "" {
		return [16]byte{}, fmt.Errorf("pdu: empty AE title")
	}
	if len(ae) > 16 {
		return [16]byte{}, fmt.Errorf("pdu: AE title longer than 16 characters: %q", ae)
	}
	var out [16]byte
	for i := range out {
		out[i] = ' '
	}
	copy(out[:], ae)
	return out, nil
}

// TrimAETitle strips trailing spaces from a 16-byte AE title field.
func TrimAETitle(b []byte) string {
	return strings.TrimRight(string(b), " ")
}

func encodeItem(itemType byte, data []byte) []byte {
	out := make([]byte, 4+len(data))
	out[0] = itemType
	binary.BigEndian.PutUint16(out[2:4], uint16(len(data)))
	copy(out[4:], data)
	return out
}

func decodeItems(b []byte) ([]rawItem, error) {
	var items []rawItem
	off := 0
	for off < len(b) {
		if off+4 > len(b) {
			return nil, fmt.Errorf("pdu: truncated item header at offset %d", off)
		}
		itemType := b[off]
		itemLen := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		if off+4+itemLen > len(b) {
			return nil, fmt.Errorf("pdu: truncated item type=0x%02x", itemType)
		}
		items = append(items, rawItem{
			Type: itemType,
			Data: append([]byte(nil), b[off+4:off+4+itemLen]...),
		})
		off += 4 + itemLen
	}
	return items, nil
}

type rawItem struct {
	Type byte
	Data []byte
}
