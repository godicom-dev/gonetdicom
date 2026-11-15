package pdu

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// collectReader runs FragmentReader and returns what it emitted.
func collectReader(t *testing.T, contextID byte, command []byte, dataset io.Reader, size int64, maxPDU uint32) ([]*PDataTF, error) {
	t.Helper()
	var out []*PDataTF
	err := FragmentReader(contextID, command, dataset, size, maxPDU, func(p *PDataTF) error {
		out = append(out, p)
		return nil
	})
	return out, err
}

// payloadOf is a deterministic payload of n bytes.
func payloadOf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	return b
}

func comparePDUs(t *testing.T, want, got []*PDataTF) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d PDUs, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i].PDVs) != 1 || len(want[i].PDVs) != 1 {
			t.Fatalf("PDU %d: %d/%d PDVs in one P-DATA-TF, want 1", i, len(got[i].PDVs), len(want[i].PDVs))
		}
		g, w := got[i].PDVs[0], want[i].PDVs[0]
		if g.ContextID != w.ContextID {
			t.Fatalf("PDU %d: context %d want %d", i, g.ContextID, w.ContextID)
		}
		if !bytes.Equal(g.Value, w.Value) {
			t.Fatalf("PDU %d: value %d bytes, want %d (mch got %#x want %#x)",
				i, len(g.Value), len(w.Value), mchOf(g), mchOf(w))
		}
	}
}

func mchOf(p PDV) byte {
	if len(p.Value) == 0 {
		return 0
	}
	return p.Value[0]
}

// The streaming encoder has to be the buffered one: a store read from a file is
// the same message on the wire as the same store held in memory, or an archive
// would talk to a peer differently depending on where its bytes came from.
func TestFragmentReaderMatchesFragmentMessage(t *testing.T) {
	t.Parallel()

	command := []byte{0x04, 0x00, 0x00, 0x00, 0x00, 0x64, 0x00, 0x00}
	dataset := payloadOf(1 << 15)
	oneFrag := int(DefaultMaxPDULength) - pdvOverhead

	cases := []struct {
		name    string
		command []byte
		dataset []byte
		maxPDU  uint32
	}{
		{"no dataset", command, nil, DefaultMaxPDULength},
		{"empty dataset", command, []byte{}, DefaultMaxPDULength},
		{"one fragment", command, dataset[:28], DefaultMaxPDULength},
		{"exactly one fragment", command, dataset[:oneFrag], DefaultMaxPDULength},
		{"one byte over", command, dataset[:oneFrag+1], DefaultMaxPDULength},
		{"many fragments", command, dataset, 32},
		{"command split as well", command, dataset, 8},
		{"no limit", command, dataset[:28], 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, err := FragmentMessage(7, tc.command, tc.dataset, tc.maxPDU)
			if err != nil {
				t.Fatalf("FragmentMessage: %v", err)
			}
			got, err := collectReader(t, 7, tc.command, bytes.NewReader(tc.dataset), int64(len(tc.dataset)), tc.maxPDU)
			if err != nil {
				t.Fatalf("FragmentReader: %v", err)
			}
			comparePDUs(t, want, got)
		})
	}
}

// Without a limit from the peer, the buffered encoder sends the dataset as one
// PDV. A reader cannot: it would have to hold the whole thing first, which is
// the cost this path exists to avoid. So the stream picks the default PDU length
// and says so — smaller PDUs are always legal.
func TestFragmentReaderWithoutPeerLimitStaysBounded(t *testing.T) {
	t.Parallel()

	dataset := payloadOf(100 << 10)
	pdus, err := collectReader(t, 3, []byte{1, 2, 3}, bytes.NewReader(dataset), int64(len(dataset)), 0)
	if err != nil {
		t.Fatalf("FragmentReader: %v", err)
	}
	if len(pdus) < 2 {
		t.Fatalf("got %d PDUs for %d bytes: the stream was not fragmented", len(pdus), len(dataset))
	}
	var joined []byte
	dataPDUs := pdus[1:]
	for i, p := range dataPDUs {
		pdv := p.PDVs[0]
		if len(pdv.Value) > int(DefaultMaxPDULength) {
			t.Fatalf("PDU %d carries %d bytes, over the default PDU length", i, len(pdv.Value))
		}
		if want := i == len(dataPDUs)-1; pdv.IsLast() != want {
			t.Fatalf("PDU %d: last=%v want %v", i, pdv.IsLast(), want)
		}
		joined = append(joined, pdv.Fragment()...)
	}
	if !bytes.Equal(joined, dataset) {
		t.Fatal("fragments do not reassemble into the dataset")
	}
}

func TestFragmentReaderRejectsBadInput(t *testing.T) {
	t.Parallel()

	emit := func(*PDataTF) error { return nil }

	if err := FragmentReader(1, nil, bytes.NewReader(nil), 0, 0, emit); err == nil {
		t.Fatal("empty command set accepted")
	}
	if err := FragmentReader(1, []byte{1}, bytes.NewReader(nil), -1, 0, emit); err == nil {
		t.Fatal("negative dataset length accepted")
	}
	if err := FragmentReader(1, []byte{1}, bytes.NewReader(nil), 1, 4, emit); err == nil {
		t.Fatal("max PDU length 4 accepted")
	}
	if err := FragmentReader(1, []byte{1}, nil, 1, 0, emit); err == nil {
		t.Fatal("a stated length with no reader was accepted")
	}
}

// A short read is a message whose declared length cannot be filled, so it is an
// error rather than a shorter message: a truncated instance that still decodes
// would be stored as a whole one.
func TestFragmentReaderReportsShortDataset(t *testing.T) {
	t.Parallel()

	err := FragmentReader(1, []byte{1}, bytes.NewReader(payloadOf(10)), 11, 0, func(*PDataTF) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}

	err = FragmentReader(1, []byte{1}, bytes.NewReader(payloadOf(10)), 100<<20, DefaultMaxPDULength, func(*PDataTF) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}
}

// The size is the caller's statement about its own bytes, so bytes past it are
// left unread — the same rule as io.CopyN, and the reason a Part 10 file can be
// sent from an offset without trimming it first.
func TestFragmentReaderSendsExactlySizeBytes(t *testing.T) {
	t.Parallel()

	raw := payloadOf(32)
	pdus, err := collectReader(t, 1, []byte{1}, bytes.NewReader(raw), 4, 0)
	if err != nil {
		t.Fatalf("FragmentReader: %v", err)
	}
	if len(pdus) != 2 {
		t.Fatalf("got %d PDUs, want 2 (command + one data fragment)", len(pdus))
	}
	if got := pdus[1].PDVs[0].Fragment(); !bytes.Equal(got, raw[:4]) {
		t.Fatalf("sent %v, want %v", got, raw[:4])
	}
}

// A write that fails stops the message: the rest of the dataset is not read, and
// the error the caller's write produced is the error the caller sees.
func TestFragmentReaderStopsOnEmitError(t *testing.T) {
	t.Parallel()

	stop := errors.New("socket closed")
	reads := 0
	dataset := &countingReader{r: bytes.NewReader(payloadOf(1 << 16)), n: &reads}
	err := FragmentReader(1, []byte{1}, dataset, 1<<16, 64, func(p *PDataTF) error {
		if p.PDVs[0].IsCommand() {
			return nil
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err=%v, want the emit error", err)
	}
	if reads != 1 {
		t.Fatalf("reader was read %d times after the first failed write, want 1", reads)
	}
}

// The property the whole decision rests on: the reader never runs ahead of the
// writer. At every emit, what has been taken from the source is at most one
// fragment more than what has been sent, so the peak is a fragment and not the
// dataset — whatever the dataset's size.
func TestFragmentReaderDoesNotReadAhead(t *testing.T) {
	t.Parallel()

	const (
		size   = 1 << 20
		maxPDU = 4096
	)
	maxFrag := int64(maxPDU) - pdvOverhead
	dataset := payloadOf(size)
	reads, sent := int64(0), int64(0)

	r := &countingBytesReader{r: bytes.NewReader(dataset), n: &reads}
	err := FragmentReader(1, []byte{1}, r, size, maxPDU, func(p *PDataTF) error {
		pdv := p.PDVs[0]
		if pdv.IsCommand() {
			return nil
		}
		if ahead := reads - sent; ahead > maxFrag {
			t.Fatalf("read %d bytes ahead of the writer, more than one %d-byte fragment", ahead, maxFrag)
		}
		sent += int64(len(pdv.Fragment()))
		return nil
	})
	if err != nil {
		t.Fatalf("FragmentReader: %v", err)
	}
	if sent != size {
		t.Fatalf("sent %d bytes, dataset is %d", sent, size)
	}
}

type countingBytesReader struct {
	r io.Reader
	n *int64
}

func (c *countingBytesReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}

type countingReader struct {
	r io.Reader
	n *int
}

func (c *countingReader) Read(p []byte) (int, error) {
	*c.n++
	return c.r.Read(p)
}
