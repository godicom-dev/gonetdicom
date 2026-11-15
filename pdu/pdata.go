package pdu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

// FragmentReader is FragmentMessage for a dataset that is not in memory.
//
// FragmentMessage materialises every P-DATA-TF of a message before the first
// one is written, so a dataset of any size is copied once more on the way out
// and is live twice at the peak. Here the command set is fragmented as usual —
// it is small, and it has to be whole to be logged — while the dataset is read
// from r one fragment at a time, each handed to emit before the next is read.
// A store of any size then leaves the process through one fragment of memory.
//
// size is how many bytes r supplies, and it is not optional: a PDV item length
// is explicit on the wire (PS3.8 Annex E), so a source that cannot say how long
// it is cannot be sent. Bytes past size are ignored, which is what
// io.CopyN-guarded writers do; a reader that stops before size is an error
// rather than a short message, because a truncated instance that still decodes
// is worse than a store that failed.
//
// maxPDULength is the peer's Maximum Length Received (0 = unlimited). Unlimited
// says what the peer will accept; it is not an instruction to send the whole
// dataset as one PDV, so a stream uses DefaultMaxPDULength fragments in that
// case. Smaller PDUs are always legal, and bounded is the point.
//
// Every PDU handed to emit is freshly allocated and owned by the caller, so a
// callback may retain it — including the fragment inside it.
func FragmentReader(contextID byte, command []byte, dataset io.Reader, size int64, maxPDULength uint32, emit func(*PDataTF) error) error {
	if len(command) == 0 {
		return fmt.Errorf("pdu: empty command set")
	}
	if size < 0 {
		return fmt.Errorf("pdu: dataset length %d cannot be sent: a PDV item length is explicit", size)
	}
	if size > 0 && dataset == nil {
		return fmt.Errorf("pdu: dataset length %d with no reader", size)
	}
	maxFrag, err := fragmentChunk(maxPDULength)
	if err != nil {
		return err
	}
	cmdPDUs, err := fragmentPart(contextID, command, true, maxPDULength)
	if err != nil {
		return err
	}
	for _, p := range cmdPDUs {
		if err := emit(p); err != nil {
			return err
		}
	}
	if size == 0 {
		return nil
	}
	if maxFrag == 0 {
		// The peer stated no limit, so pick one: the default PDU length is a
		// fragment size that every implementation accepts, and it keeps the
		// memory this path costs constant.
		maxFrag = DefaultMaxPDULength - pdvOverhead
	}
	for remaining := size; remaining > 0; {
		n := int64(maxFrag)
		last := false
		if remaining <= n {
			n = remaining
			last = true
		}
		// MCH + fragment, sized exactly: the value's length is the PDV item
		// length, and the peer reads the dataset from it.
		value := make([]byte, 1+n)
		if last {
			value[0] = MCHLastFragment
		}
		got, err := io.ReadFull(dataset, value[1:])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("pdu: dataset ended %d bytes short of its stated %d: %w",
					remaining-int64(got), size, io.ErrUnexpectedEOF)
			}
			return fmt.Errorf("pdu: read dataset: %w", err)
		}
		remaining -= n
		if err := emit(&PDataTF{PDVs: []PDV{{ContextID: contextID, Value: value}}}); err != nil {
			return err
		}
	}
	return nil
}

// pdvOverhead is what one PDV costs besides its fragment: 4 (item length) +
// 1 (context ID) + 1 (message control header).
const pdvOverhead = 6

// fragmentChunk is how much of a part one PDV may carry for a given Maximum
// Length Received, which limits the PDV list size in the PDU body. Zero means
// "all of it", which is what FragmentMessage does when the peer states no
// limit.
func fragmentChunk(maxPDULength uint32) (int, error) {
	if maxPDULength == 0 {
		return 0, nil
	}
	if maxPDULength < pdvOverhead+2 {
		return 0, fmt.Errorf("pdu: max PDU length %d too small", maxPDULength)
	}
	return int(maxPDULength) - pdvOverhead, nil
}

func fragmentPart(contextID byte, data []byte, isCommand bool, maxPDULength uint32) ([]*PDataTF, error) {
	maxFrag, err := fragmentChunk(maxPDULength)
	if err != nil {
		return nil, err
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
