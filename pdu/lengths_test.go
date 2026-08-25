package pdu

import (
	"encoding/binary"
	"testing"
)

// A PDV item length is 32 bits, like the DIMSE element lengths one layer down,
// and it was read into an int the same way. On a 32-bit build a declared length
// of MaxInt32 stays positive but makes "off+4+itemLen" overflow, so the bounds
// check reads as satisfied and the slice after it panics — from twenty bytes on
// an established association. Checked against GOARCH=386.
func TestDecodePDataTFRejectsLengthsNoIntCanHold(t *testing.T) {
	t.Parallel()

	for _, itemLen := range []uint32{
		0xFFFFFFFF, // -1 as a 32-bit int
		0x80000000, // MinInt32
		0x7FFFFFFF, // MaxInt32: positive, and off+4+itemLen overflows
	} {
		// A well-formed 20-byte P-DATA-TF whose single PDV item lies about its size.
		raw := append(header(TypePDataTF, 14), make([]byte, 14)...)
		binary.BigEndian.PutUint32(raw[6:10], itemLen)

		p, err := DecodePDataTF(raw)
		if err == nil {
			t.Errorf("PDV item length 0x%08X: decoded %d PDV(s) out of 20 bytes, want an error",
				itemLen, len(p.PDVs))
		}
	}
}

// The declared PDU length is 32 bits too, and "6 + length" used to be added in
// uint32 before the comparison, so a peer claiming 0xFFFFFFFA wrapped it to 0.
// That never let anything through — a wrapped total is smaller than the shortest
// PDU these decoders accept, so the comparison failed anyway — but it failed for
// the wrong reason. The arithmetic is in uint64 now; this pins the answer.
func TestDecodersRejectDeclaredLengthThatCannotFit(t *testing.T) {
	t.Parallel()

	for _, typ := range []byte{TypePDataTF, TypeAAssociateRQ, TypeAAssociateAC} {
		for _, length := range []uint32{0xFFFFFFFA, 0xFFFFFFFF} {
			raw := append(header(typ, length), make([]byte, 68)...)
			var err error
			switch typ {
			case TypePDataTF:
				_, err = DecodePDataTF(raw)
			case TypeAAssociateRQ:
				_, err = DecodeAAssociateRQ(raw)
			case TypeAAssociateAC:
				_, err = DecodeAAssociateAC(raw)
			}
			if err == nil {
				t.Errorf("%s declaring length %d in a %d-byte PDU: no error",
					TypeName(typ), length, len(raw))
			}
		}
	}
}
