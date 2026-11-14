package pdu

import (
	"bytes"
	"testing"
)

// Two PDVs in one PDU, written out byte by byte. The rewritten Encode fills one
// buffer at a cumulative offset instead of appending item by item, so the second
// item's length and context ID are exactly where a per-item layout bug would put
// them wrong — and a self round-trip would not notice, because the decoder walks
// offsets the encoder produced.
func TestPDataTFEncodeMultiplePDVsWireFormat(t *testing.T) {
	p := &PDataTF{PDVs: []PDV{
		{ContextID: 1, Value: []byte{MCHCommand, 'a', 'b'}},
		{ContextID: 3, Value: []byte{MCHLastFragment, 'c'}},
	}}
	got, err := p.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{
		TypePDataTF, 0x00, 0x00, 0x00, 0x00, 0x0F, // body length 15
		0x00, 0x00, 0x00, 0x04, 0x01, MCHCommand, 'a', 'b', // item length 4 = ctx + value
		0x00, 0x00, 0x00, 0x03, 0x03, MCHLastFragment, 'c',
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded\n% x\nwant\n% x", got, want)
	}

	back, err := DecodePDataTF(got)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(back.PDVs) != 2 || back.PDVs[1].ContextID != 3 || !bytes.Equal(back.PDVs[1].Value, []byte{MCHLastFragment, 'c'}) {
		t.Fatalf("decoded %+v", back.PDVs)
	}
}

// PDV.Value is documented as a subslice of the decoded buffer rather than a
// copy. The two PDVs of one PDU must still be independent of each other: a
// decoder that handed back whole-buffer views would make writing through one
// PDV silently corrupt its neighbour.
func TestDecodePDataTFPDVsDoNotOverlap(t *testing.T) {
	p := &PDataTF{PDVs: []PDV{
		{ContextID: 1, Value: []byte{MCHCommand, 'a', 'b'}},
		{ContextID: 3, Value: []byte{MCHLastFragment, 'c'}},
	}}
	raw, err := p.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := DecodePDataTF(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	back.PDVs[0].Value[1] = 'X'
	if back.PDVs[1].Value[1] != 'c' {
		t.Fatalf("writing through the first PDV changed the second: % x", back.PDVs[1].Value)
	}
}

// Because Value is a view, the buffer it views has to outlive the PDVs. That is
// true exactly as long as Read hands out a fresh buffer per PDU, which is what
// this pins: pooling the read buffer would be a real speed-up and would break
// every PDV already handed to a handler.
func TestReadGivesEachPDUItsOwnBuffer(t *testing.T) {
	var stream bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := Write(&stream, &PDataTF{PDVs: []PDV{{ContextID: byte(i + 1), Value: []byte{MCHLastFragment, 'a', 'b'}}}}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	first, err := Read(&stream)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	second, err := Read(&stream)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	p1 := first.(*PDataTF)
	p2 := second.(*PDataTF)
	if p1.PDVs[0].ContextID != 1 || p2.PDVs[0].ContextID != 2 {
		t.Fatalf("context IDs %d and %d, want 1 and 2", p1.PDVs[0].ContextID, p2.PDVs[0].ContextID)
	}

	// Overwrite the first PDU's payload the way a handler that reuses a scratch
	// buffer would.
	for i := range p1.PDVs[0].Value {
		p1.PDVs[0].Value[i] = 0xEE
	}
	if want := []byte{MCHLastFragment, 'a', 'b'}; !bytes.Equal(p2.PDVs[0].Value, want) {
		t.Fatalf("the second PDU's value became % x, want % x", p2.PDVs[0].Value, want)
	}
}
