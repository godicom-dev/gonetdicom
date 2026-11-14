package dimse

import (
	"testing"
)

// Command set encode and decode, the pair pynetdicom measures in
// benchmarks/bench_dimse_message.py. Every DIMSE message on the wire is one of
// these, so these numbers multiply by the message count of a study: a C-MOVE of
// 500 instances is 500 C-STORE-RQs, each encoded once and decoded once.

// benchmarkCommand measures the two directions of one message type. Decode gets
// the bytes encode produced rather than a golden fixture, so a change that makes
// encoding cheaper and decoding dearer cannot hide in a hand-written constant.
func benchmarkCommand(b *testing.B, encode func() ([]byte, error), decode func([]byte) error) {
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := encode(); err != nil {
				b.Fatal(err)
			}
		}
	})

	raw, err := encode()
	if err != nil {
		b.Fatal(err)
	}
	b.Run("decode", func(b *testing.B) {
		b.SetBytes(int64(len(raw)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := decode(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCEchoRQ(b *testing.B) {
	m := &CEchoRQ{MessageID: 1, AffectedSOPClassUID: VerificationSOPClass}
	benchmarkCommand(b,
		func() ([]byte, error) { return m.Encode() },
		func(raw []byte) error { _, err := DecodeCEchoRQ(raw); return err },
	)
}

func BenchmarkCFindRQ(b *testing.B) {
	m := &CFindRQ{
		MessageID:           1,
		Priority:            PriorityMedium,
		AffectedSOPClassUID: "1.2.840.10008.5.1.4.1.2.2.1", // Study Root C-FIND
	}
	benchmarkCommand(b,
		func() ([]byte, error) { return m.Encode() },
		func(raw []byte) error { _, err := DecodeCFindRQ(raw); return err },
	)
}

// CStoreRQ is the one that matters for throughput: a retrieval is thousands of
// them, and the command set is the fixed cost paid before any pixel data moves.
func BenchmarkCStoreRQ(b *testing.B) {
	m := &CStoreRQ{
		MessageID:              1,
		Priority:               PriorityMedium,
		AffectedSOPClassUID:    "1.2.840.10008.5.1.4.1.1.2", // CT Image Storage
		AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.1075.1.1.1.1",
	}
	benchmarkCommand(b,
		func() ([]byte, error) { return m.Encode() },
		func(raw []byte) error { _, err := DecodeCStoreRQ(raw); return err },
	)
}

// CommandHasDataset runs on every inbound command before the reader decides
// whether a dataset follows. It parses the element list, so it is on the hot
// path of a store and worth its own line.
func BenchmarkCommandHasDataset(b *testing.B) {
	m := &CStoreRQ{
		MessageID:              1,
		Priority:               PriorityMedium,
		AffectedSOPClassUID:    "1.2.840.10008.5.1.4.1.1.2",
		AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.1075.1.1.1.1",
	}
	raw, err := m.Encode()
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CommandHasDataset(raw); err != nil {
			b.Fatal(err)
		}
	}
}
