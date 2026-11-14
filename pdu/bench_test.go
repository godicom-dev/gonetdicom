package pdu

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// The two paths an association spends its life on, measured in the shapes
// pynetdicom's asv suite measures them (benchmarks/bench_pdu.py): the
// A-ASSOCIATE handshake and P-DATA.
//
// Read and Write sit next to the decoders because they are what the association
// loop actually calls. A decoder benchmark that skips the length header and the
// io.Reader measures a function with no caller, and would miss the one
// allocation Read is careful to avoid (it grows the body as bytes arrive rather
// than trusting the declared length).

func BenchmarkReadAssociateRQ(b *testing.B) {
	b.SetBytes(int64(len(goldenAAssociateRQ)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Read(bytes.NewReader(goldenAAssociateRQ)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteAssociateRQ(b *testing.B) {
	p, err := Read(bytes.NewReader(goldenAAssociateRQ))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(goldenAAssociateRQ)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Write(io.Discard, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeAAssociateRQ(b *testing.B) {
	b.SetBytes(int64(len(goldenAAssociateRQ)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeAAssociateRQ(goldenAAssociateRQ); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeAAssociateRQ(b *testing.B) {
	p, err := DecodeAAssociateRQ(goldenAAssociateRQ)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(goldenAAssociateRQ)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePDataTF(b *testing.B) {
	b.SetBytes(int64(len(goldenPDataTF)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := DecodePDataTF(goldenPDataTF); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePDataTF(b *testing.B) {
	p, err := DecodePDataTF(goldenPDataTF)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(goldenPDataTF)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}

// A 16 KiB PDV is what a C-STORE actually sends, because 16 KiB is the
// Maximum Length Received most stacks propose. The small fixture above cannot
// show it, but at this size B/op is the whole story: decoding a PDV must not
// copy its payload, and encoding one must not build it in pieces, so both land
// near 1x payload instead of 2x and 3x.
func BenchmarkDecodePDataTFLarge(b *testing.B) {
	payload := bytes.Repeat([]byte{0x5a}, 16382-6)
	value := append([]byte{MCHCommand | MCHLastFragment}, payload...)
	raw, err := (&PDataTF{PDVs: []PDV{{ContextID: 1, Value: value}}}).Encode()
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodePDataTF(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePDataTFLarge(b *testing.B) {
	payload := bytes.Repeat([]byte{0x5a}, 16382-6)
	p := &PDataTF{PDVs: []PDV{{ContextID: 1, Value: append([]byte{MCHCommand | MCHLastFragment}, payload...)}}}
	b.SetBytes(int64(6 + 5 + len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}

// FragmentMessage is the send-side counterpart of the streaming reader, and the
// place where a peer's Maximum Length Received turns one dataset into many
// PDUs. The two sizes bracket what a real negotiation offers: 16 KiB is the
// conservative value most stacks propose, 1 MiB is what this module advertises.
func BenchmarkFragmentMessage(b *testing.B) {
	command := bytes.Repeat([]byte{0x01}, 128)
	dataset := bytes.Repeat([]byte{0x02}, 4<<20)
	for _, max := range []uint32{16 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("maxPDU=%d", max), func(b *testing.B) {
			b.SetBytes(int64(len(command) + len(dataset)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pdus, err := FragmentMessage(1, command, dataset, max)
				if err != nil {
					b.Fatal(err)
				}
				if len(pdus) == 0 {
					b.Fatal("no PDUs")
				}
			}
		})
	}
}
