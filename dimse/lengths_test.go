package dimse

import (
	"encoding/binary"
	"testing"
)

// A DIMSE element's Value Length is 32 bits, so a peer can declare one that no
// int holds where int is 32 bits wide. It used to be converted to int on the way
// in: the length came out negative, the bounds check below it read as satisfied,
// and the slice that followed inverted and panicked. Eight bytes on the wire, no
// association state required. Checked against GOARCH=386, where each of these
// crashed the decoder instead of rejecting the input.
func TestDecodeElementsRejectsLengthsNoIntCanHold(t *testing.T) {
	t.Parallel()

	for _, vlen := range []uint32{
		0xFFFFFFFF, // -1 as a 32-bit int
		0xFFFFFFF8, // -8: off+8+vlen lands back on off, so the slice inverts
		0x80000000, // MinInt32
		0x7FFFFFFF, // MaxInt32: stays positive, but off+8+vlen overflows
	} {
		// One command element, (0000,0100), whose declared value is not there.
		b := []byte{0x00, 0x00, 0x00, 0x01, 0, 0, 0, 0}
		binary.LittleEndian.PutUint32(b[4:8], vlen)

		els, err := decodeElements(b)
		if err == nil {
			t.Errorf("Value Length 0x%08X: decoded %d element(s) out of 8 bytes, want an error",
				vlen, len(els))
		}
	}
}
