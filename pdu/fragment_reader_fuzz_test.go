package pdu

import (
	"bytes"
	"testing"
)

// FragmentReader is the encoder every store goes through now, and it has the one
// property a fragmenter must not get wrong: the bytes it emits have to be the
// bytes it was given. So the fuzzer asserts that rather than looking for a panic
// alone — the payloads are reassembled from the emitted PDVs and compared with
// the input, and against FragmentMessage, which builds the same message in
// memory and is what the in-memory paths still use.
//
// The sizes are bounded because the point is the boundary arithmetic, not
// throughput: a fragment that lands exactly on maxPDU-6, one that is one byte
// over, an empty dataset, a command that itself needs two PDUs.
func FuzzFragmentReader(f *testing.F) {
	f.Add([]byte{0x04, 0x00, 0x00, 0x00, 0x00, 0x64, 0x00, 0x00}, []byte("dataset"), uint32(1<<16), 0)
	f.Add([]byte{0x04, 0x00}, []byte{}, uint32(16), -1)
	f.Add([]byte{0x01}, bytes.Repeat([]byte{7}, 300), uint32(24), 3)

	f.Fuzz(func(t *testing.T, command, dataset []byte, maxPDU uint32, sizeDelta int) {
		if len(command) == 0 || len(dataset) > 1<<16 {
			t.Skip()
		}
		size := int64(len(dataset)) + int64(sizeDelta)
		if size < 0 {
			return // the reader has a defined answer for this; not this test's
		}

		var got []*PDataTF
		err := FragmentReader(1, command, bytes.NewReader(dataset), size, maxPDU, func(p *PDataTF) error {
			got = append(got, p)
			return nil
		})
		if err != nil {
			// The only accepted failures are the documented input refusals: a
			// max PDU length below the item overhead, or a declared length
			// larger than the reader.
			if maxPDU > 0 && maxPDU < 8 {
				return
			}
			if size > int64(len(dataset)) {
				return
			}
			t.Fatalf("FragmentReader: %v (command %d B, dataset %d B, size %d, maxPDU %d)",
				err, len(command), len(dataset), size, maxPDU)
		}

		// Every PDV fits the length the peer stated, and the message control
		// header says which half of the message it is.
		var cmdBytes, dsBytes []byte
		for i, p := range got {
			if len(p.PDVs) != 1 {
				t.Fatalf("PDU %d carries %d PDVs, want one", i, len(p.PDVs))
			}
			pdv := p.PDVs[0]
			// 6 bytes of PDV item: its own length, the context ID and the
			// message control header. Nothing may exceed the length the peer
			// stated.
			if maxPDU >= 8 && uint32(6+len(pdv.Fragment())) > maxPDU {
				t.Fatalf("PDU %d carries %d bytes of dataset, over a max PDU length of %d",
					i, len(pdv.Fragment()), maxPDU)
			}
			if pdv.IsCommand() {
				cmdBytes = append(cmdBytes, pdv.Fragment()...)
			} else {
				dsBytes = append(dsBytes, pdv.Fragment()...)
			}
		}

		if !bytes.Equal(cmdBytes, command) {
			t.Fatalf("command reassembled to %d bytes, want %d", len(cmdBytes), len(command))
		}
		want := dataset[:size]
		if !bytes.Equal(dsBytes, want) {
			t.Fatalf("dataset reassembled to %d bytes, want %d", len(dsBytes), len(want))
		}

		// The two encoders are one wire format: same PDVs, in the same order,
		// with the same lengths and the same last-fragment flags.
		if maxPDU >= 8 {
			inMemory, err := FragmentMessage(1, command, want, maxPDU)
			if err == nil {
				if len(inMemory) != len(got) {
					t.Fatalf("FragmentMessage produced %d PDUs, FragmentReader %d", len(inMemory), len(got))
				}
				for i := range inMemory {
					a, b := inMemory[i].PDVs[0], got[i].PDVs[0]
					if a.IsCommand() != b.IsCommand() || a.IsLast() != b.IsLast() ||
						!bytes.Equal(a.Fragment(), b.Fragment()) {
						t.Fatalf("PDU %d differs between the two encoders", i)
					}
				}
			}
		}
	})
}
