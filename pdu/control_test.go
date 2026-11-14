package pdu

import (
	"bytes"
	"errors"
	"testing"
)

// Every control PDU is exactly ten bytes and typed. A decoder that accepted a
// short read would hand back a zero-valued PDU and the association layer would
// treat a truncated frame as a release.
func TestControlPDUDecodeRejectsBadFrames(t *testing.T) {
	short := []byte{0x05, 0x00, 0x00, 0x00, 0x00, 0x04}
	wrongType := []byte{0x07, 0x00, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00}

	tests := []struct {
		name   string
		raw    []byte
		decode func([]byte) error
	}{
		{"A-RELEASE-RQ short", short, func(b []byte) error { _, err := DecodeAReleaseRQ(b); return err }},
		{"A-RELEASE-RQ wrong type", append([]byte{TypeAAbort}, short[1:]...), func(b []byte) error { _, err := DecodeAReleaseRQ(b); return err }},
		{"A-RELEASE-RP short", short, func(b []byte) error { _, err := DecodeAReleaseRP(b); return err }},
		{"A-RELEASE-RP wrong type", wrongType, func(b []byte) error { _, err := DecodeAReleaseRP(b); return err }},
		{"A-ABORT short", short, func(b []byte) error { _, err := DecodeAAbort(b); return err }},
		{"A-ABORT wrong type", append([]byte{TypeAReleaseRQ}, short[1:]...), func(b []byte) error { _, err := DecodeAAbort(b); return err }},
		{"A-ASSOCIATE-RJ short", short, func(b []byte) error { _, err := DecodeAAssociateRJ(b); return err }},
		{"A-ASSOCIATE-RJ wrong type", append([]byte{TypeAAssociateAC}, short[1:]...), func(b []byte) error { _, err := DecodeAAssociateRJ(b); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.decode(tc.raw); err == nil {
				t.Fatal("decoded a frame that should have been refused")
			}
		})
	}

	// The wrong-type cases must say so specifically, not just "bad length":
	// callers switch on ErrUnexpectedType to tell a protocol error from a
	// framing one.
	if _, err := DecodeAReleaseRQ(wrongType); !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("error = %v, want ErrUnexpectedType", err)
	}
}

// A control PDU round-trips at exactly ten bytes, so a peer can read the
// encoded form with the same length check this decoder applies.
func TestControlPDURoundTrip(t *testing.T) {
	encode := func(p PDU) []byte {
		t.Helper()
		raw, err := p.Encode()
		if err != nil {
			t.Fatalf("encode %T: %v", p, err)
		}
		if len(raw) != 10 {
			t.Fatalf("%T encoded to %d bytes, want 10", p, len(raw))
		}
		return raw
	}

	rq := encode(&AReleaseRQ{})
	if _, err := DecodeAReleaseRQ(rq); err != nil {
		t.Fatalf("A-RELEASE-RQ: %v", err)
	}
	rp := encode(&AReleaseRP{})
	if _, err := DecodeAReleaseRP(rp); err != nil {
		t.Fatalf("A-RELEASE-RP: %v", err)
	}
	ab := encode(&AAbort{Source: 0x02, ReasonDiagnostic: 0x04})
	got, err := DecodeAAbort(ab)
	if err != nil {
		t.Fatalf("A-ABORT: %v", err)
	}
	if got.Source != 0x02 || got.ReasonDiagnostic != 0x04 {
		t.Fatalf("A-ABORT = %+v, want source 2 reason 4", got)
	}
	rj := encode(&AAssociateRJ{Result: RejectResultTransient, Source: RejectSourceServiceProviderPres, ReasonDiagnostic: RejectReasonLocalLimitExceeded})
	decoded, err := DecodeAAssociateRJ(rj)
	if err != nil {
		t.Fatalf("A-ASSOCIATE-RJ: %v", err)
	}
	if decoded.Result != RejectResultTransient || decoded.Source != RejectSourceServiceProviderPres || decoded.ReasonDiagnostic != RejectReasonLocalLimitExceeded {
		t.Fatalf("A-ASSOCIATE-RJ = %+v", decoded)
	}
}

func TestTypeName(t *testing.T) {
	want := map[byte]string{
		TypeAAssociateRQ: "A-ASSOCIATE-RQ",
		TypeAAssociateAC: "A-ASSOCIATE-AC",
		TypeAAssociateRJ: "A-ASSOCIATE-RJ",
		TypePDataTF:      "P-DATA-TF",
		TypeAReleaseRQ:   "A-RELEASE-RQ",
		TypeAReleaseRP:   "A-RELEASE-RP",
		TypeAAbort:       "A-ABORT",
	}
	for code, name := range want {
		if got := TypeName(code); got != name {
			t.Errorf("TypeName(0x%02x) = %q, want %q", code, got, name)
		}
	}
	if got := TypeName(0x99); got != "PDU-0x99" {
		t.Errorf("TypeName(0x99) = %q, want PDU-0x99", got)
	}
}

// FragmentMessage is the contract a receiver relies on: the command set arrives
// in one piece and every PDU body stays within the peer's declared Maximum
// Length Received.
func TestFragmentMessageRespectsMaxPDULength(t *testing.T) {
	const maxLen = 512
	command := bytes.Repeat([]byte{0xAA}, 100)
	dataset := bytes.Repeat([]byte{0xBB}, 2000)

	pdus, err := FragmentMessage(1, command, dataset, maxLen)
	if err != nil {
		t.Fatalf("FragmentMessage: %v", err)
	}
	if len(pdus) < 2 {
		t.Fatalf("a 2 KB dataset came back in %d PDUs; it must be split", len(pdus))
	}

	var (
		cmdBuf, dsBuf []byte
		cmdLast       bool
		dsLast        bool
	)
	for i, p := range pdus {
		body, err := p.Encode()
		if err != nil {
			t.Fatalf("PDU %d encode: %v", i, err)
		}
		// 6 header bytes are not part of the body budget; the body is what the
		// peer counts against Maximum Length Received.
		if bodyLen := len(body) - 6; bodyLen > maxLen {
			t.Fatalf("PDU %d body is %d bytes, over the %d limit", i, bodyLen, maxLen)
		}
		for _, pdv := range p.PDVs {
			if pdv.ContextID != 1 {
				t.Fatalf("PDV on context %d, want 1", pdv.ContextID)
			}
			if pdv.IsCommand() {
				cmdBuf = append(cmdBuf, pdv.Fragment()...)
				cmdLast = cmdLast || pdv.IsLast()
				continue
			}
			dsBuf = append(dsBuf, pdv.Fragment()...)
			dsLast = dsLast || pdv.IsLast()
		}
	}
	if !bytes.Equal(cmdBuf, command) || !cmdLast {
		t.Fatal("command set did not survive fragmentation")
	}
	if !bytes.Equal(dsBuf, dataset) || !dsLast {
		t.Fatal("dataset did not survive fragmentation")
	}
}

func TestFragmentMessageRejectsBadInput(t *testing.T) {
	if _, err := FragmentMessage(1, nil, nil, 0); err == nil {
		t.Fatal("empty command set was accepted")
	}
	if _, err := FragmentMessage(1, []byte{1, 2, 3}, nil, 4); err == nil {
		t.Fatal("a Maximum Length Received smaller than a PDV header was accepted")
	}
}

// Zero means unlimited: one PDU per part, however large.
func TestFragmentMessageUnlimited(t *testing.T) {
	pdus, err := FragmentMessage(3, []byte{1, 2, 3}, bytes.Repeat([]byte{9}, 10000), 0)
	if err != nil {
		t.Fatalf("FragmentMessage: %v", err)
	}
	if len(pdus) != 2 {
		t.Fatalf("got %d PDUs, want 2 (command + dataset)", len(pdus))
	}
	for _, p := range pdus {
		if len(p.PDVs) != 1 || !p.PDVs[0].IsLast() {
			t.Fatalf("PDU is not a single last-fragment PDV: %+v", p.PDVs)
		}
	}
}
