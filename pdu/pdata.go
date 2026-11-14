package pdu

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Message control header bits (PS3.8 Annex E).
const (
	MCHCommand      byte = 0x01
	MCHLastFragment byte = 0x02
)

// PDV is a Presentation Data Value item inside a P-DATA-TF PDU.
// Value is Message Control Header + fragment (command or dataset).
//
// Value decoded by DecodePDataTF is a subslice of the buffer that was passed
// in, not a copy: a P-DATA-TF carries up to the peer's Maximum Length Received
// of payload, and copying it here doubled the memory a C-STORE needed before
// the handler saw a byte. The buffer belongs to the caller, so it must stay
// unchanged while the PDVs are in use — which is what ReadLimit guarantees by
// allocating one buffer per PDU and never reusing it.
type PDV struct {
	ContextID byte
	Value     []byte // includes MCH as first byte
}

// IsCommand reports whether this PDV carries a command fragment.
func (p PDV) IsCommand() bool {
	return len(p.Value) > 0 && p.Value[0]&MCHCommand != 0
}

// IsLast reports whether this is the last fragment of the message part.
func (p PDV) IsLast() bool {
	return len(p.Value) > 0 && p.Value[0]&MCHLastFragment != 0
}

// Fragment returns the bytes after the Message Control Header.
func (p PDV) Fragment() []byte {
	if len(p.Value) <= 1 {
		return nil
	}
	return p.Value[1:]
}

// PDataTF is a P-DATA-TF PDU.
type PDataTF struct {
	PDVs []PDV
}

func (p *PDataTF) Type() byte { return TypePDataTF }

// Encode serializes the PDU.
//
// The body is measured first and the result allocated once. Building it by
// appending per-PDV item buffers cost three passes over the payload — item,
// body, header — and a C-STORE is nothing but payload, so the extra copies were
// the whole cost of sending a dataset.
func (p *PDataTF) Encode() ([]byte, error) {
	bodyLen := 0
	for _, pdv := range p.PDVs {
		if len(pdv.Value) == 0 {
			return nil, fmt.Errorf("pdu: empty PDV value")
		}
		bodyLen += 4 + 1 + len(pdv.Value) // item length + context ID + value
	}
	if uint64(bodyLen) > math.MaxUint32 {
		return nil, errTooLong("P-DATA-TF body", bodyLen, math.MaxUint32)
	}
	out := make([]byte, 6+bodyLen)
	out[0] = TypePDataTF
	binary.BigEndian.PutUint32(out[2:6], uint32(bodyLen))
	off := 6
	for _, pdv := range p.PDVs {
		itemLen := 1 + len(pdv.Value)
		binary.BigEndian.PutUint32(out[off:off+4], uint32(itemLen))
		out[off+4] = pdv.ContextID
		copy(out[off+5:off+5+len(pdv.Value)], pdv.Value)
		off += 4 + itemLen
	}
	return out, nil
}

// DecodePDataTF parses a P-DATA-TF PDU.
//
// Each PDV.Value is a subslice of raw rather than a copy — see PDV. The PDV
// slice itself is this call's own, so appending to it never writes into raw.
func DecodePDataTF(raw []byte) (*PDataTF, error) {
	if len(raw) < 6 {
		return nil, fmt.Errorf("pdu: P-DATA-TF too short")
	}
	if raw[0] != TypePDataTF {
		return nil, fmt.Errorf("%w: got 0x%02x want P-DATA-TF", ErrUnexpectedType, raw[0])
	}
	length := binary.BigEndian.Uint32(raw[2:6])
	if 6+uint64(length) != uint64(len(raw)) {
		return nil, fmt.Errorf("pdu: P-DATA-TF length mismatch")
	}
	p := &PDataTF{}
	off := 6
	for off < len(raw) {
		if off+4 > len(raw) {
			return nil, fmt.Errorf("pdu: truncated PDV length")
		}
		// The item length is 32 bits and is compared in uint64 for the same reason
		// as the element lengths in dimse: converting it to a 32-bit int first turns
		// a large length negative, and negative lengths pass a bounds check.
		itemLen := uint64(binary.BigEndian.Uint32(raw[off : off+4]))
		if itemLen < 2 || uint64(off)+4+itemLen > uint64(len(raw)) {
			return nil, fmt.Errorf("pdu: bad PDV item length %d", itemLen)
		}
		pdv := PDV{
			ContextID: raw[off+4],
			Value:     raw[off+5 : off+4+int(itemLen)],
		}
		p.PDVs = append(p.PDVs, pdv)
		off += 4 + int(itemLen)
	}
	return p, nil
}

// NewCommandPDV builds a single last-fragment command PDV.
func NewCommandPDV(contextID byte, commandSet []byte) PDV {
	value := make([]byte, 1+len(commandSet))
	value[0] = MCHCommand | MCHLastFragment
	copy(value[1:], commandSet)
	return PDV{ContextID: contextID, Value: value}
}

// NewDataPDV builds a single last-fragment dataset PDV.
func NewDataPDV(contextID byte, dataset []byte) PDV {
	value := make([]byte, 1+len(dataset))
	value[0] = MCHLastFragment // data + last
	copy(value[1:], dataset)
	return PDV{ContextID: contextID, Value: value}
}

// FragmentMessage builds P-DATA-TF PDUs for a command set and optional dataset.
//
// maxPDULength is the peer's Maximum Length Received (0 = unlimited). Each
// P-DATA-TF PDV list is kept within that limit (PS3.8 Annex D), matching
// pynetdicom's encode_msg fragmentation.
func FragmentMessage(contextID byte, command, dataset []byte, maxPDULength uint32) ([]*PDataTF, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("pdu: empty command set")
	}
	var out []*PDataTF
	cmdPDUs, err := fragmentPart(contextID, command, true, maxPDULength)
	if err != nil {
		return nil, err
	}
	out = append(out, cmdPDUs...)
	if len(dataset) > 0 {
		dsPDUs, err := fragmentPart(contextID, dataset, false, maxPDULength)
		if err != nil {
			return nil, err
		}
		out = append(out, dsPDUs...)
	}
	return out, nil
}

func fragmentPart(contextID byte, data []byte, isCommand bool, maxPDULength uint32) ([]*PDataTF, error) {
	// PDV item overhead: 4 (item length) + 1 (context ID) + 1 (MCH) = 6
	// Maximum Length Received limits the PDV list size in the PDU body.
	maxFrag := 0
	if maxPDULength > 0 {
		if maxPDULength < 8 {
			return nil, fmt.Errorf("pdu: max PDU length %d too small", maxPDULength)
		}
		maxFrag = int(maxPDULength) - 6
	}

	var frags [][]byte
	if maxFrag == 0 || len(data) <= maxFrag {
		frags = [][]byte{data}
	} else {
		for off := 0; off < len(data); {
			end := off + maxFrag
			if end > len(data) {
				end = len(data)
			}
			frags = append(frags, data[off:end])
			off = end
		}
	}

	out := make([]*PDataTF, 0, len(frags))
	for i, frag := range frags {
		last := i == len(frags)-1
		var mch byte
		if isCommand {
			mch = MCHCommand
		}
		if last {
			mch |= MCHLastFragment
		}
		value := make([]byte, 1+len(frag))
		value[0] = mch
		copy(value[1:], frag)
		out = append(out, &PDataTF{PDVs: []PDV{{ContextID: contextID, Value: value}}})
	}
	return out, nil
}
