package pdu

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"
)

// header builds a 6-byte PDU header declaring the given body length.
func header(typ byte, length uint32) []byte {
	h := []byte{typ, 0, 0, 0, 0, 0}
	h[2] = byte(length >> 24)
	h[3] = byte(length >> 16)
	h[4] = byte(length >> 8)
	h[5] = byte(length)
	return h
}

func TestReadRejectsOverlargeDeclaredLength(t *testing.T) {
	t.Parallel()

	// Six bytes of input claiming a 4 GiB body.
	_, err := Read(bytes.NewReader(header(TypePDataTF, 0xFFFFFFFF)))
	if !errors.Is(err, ErrPDUTooLarge) {
		t.Fatalf("err=%v, want ErrPDUTooLarge", err)
	}
}

func TestReadLimitHonoursCallerLimit(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := Write(&buf, &PDataTF{PDVs: []PDV{NewCommandPDV(1, make([]byte, 4096))}}); err != nil {
		t.Fatal(err)
	}
	in := buf.Bytes()

	if _, err := ReadLimit(bytes.NewReader(in), 1024); !errors.Is(err, ErrPDUTooLarge) {
		t.Fatalf("limit=1024: err=%v, want ErrPDUTooLarge", err)
	}
	// Same PDU under a limit that admits it decodes normally.
	if _, err := ReadLimit(bytes.NewReader(in), 8192); err != nil {
		t.Fatalf("limit=8192: unexpected error: %v", err)
	}
}

// A declared length under the cap must not be allocated up front: a peer that
// claims 8 MiB and then hangs up should cost us only what it actually sent.
func TestReadAllocatesProportionallyToBytesReceived(t *testing.T) {
	const declared = 8 << 20

	in := append(header(TypePDataTF, declared), make([]byte, 512)...)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Read(bytes.NewReader(in))
	runtime.ReadMemStats(&after)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("allocated %d bytes for a %d-byte body claiming %d", grew, 512, declared)
	}
}

// A legitimate PDU larger than one read chunk must still decode.
func TestReadMultiChunkBody(t *testing.T) {
	t.Parallel()

	payload := make([]byte, 3*pduReadChunk+7)
	for i := range payload {
		payload[i] = byte(i)
	}
	var buf bytes.Buffer
	if err := Write(&buf, &PDataTF{PDVs: []PDV{NewCommandPDV(1, payload)}}); err != nil {
		t.Fatal(err)
	}

	p, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, ok := p.(*PDataTF)
	if !ok {
		t.Fatalf("got %T", p)
	}
	if len(got.PDVs) != 1 || !bytes.Equal(got.PDVs[0].Fragment(), payload) {
		t.Fatalf("payload round-trip mismatch")
	}
}
