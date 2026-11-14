package pdu

import (
	"bytes"
	"testing"
)

// The SOP Class Common Extended Negotiation sub-item is three length-prefixed
// fields in a row, and the third of them is a whole nested list rather than a
// UID (PS3.8 D.3.3.6, Tables D.3-12 and D.3-13). Small UIDs are used here so
// every length can be counted by hand: a decoder that swapped the Service
// Class length for the list length would still round-trip against itself.
func TestCommonExtendedNegotiationWireFormat(t *testing.T) {
	ui := UserInformation{
		ImplementationClassUID: "1.2.3",
		CommonExtendedNegotiations: []CommonExtendedNegotiation{{
			SOPClassUID:              "1.2.3",
			ServiceClassUID:          "1.2.4",
			RelatedGeneralSOPClasses: []string{"1.2.5"},
		}},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := []byte{
		ItemSOPClassCommonExtendedNegotiation, 0x00, 0x00, 0x17, // item, reserved, length 23
		0x00, 0x05, '1', '.', '2', '.', '3', // SOP Class UID
		0x00, 0x05, '1', '.', '2', '.', '4', // Service Class UID
		0x00, 0x07, // related general list: 2 + 5, the whole nested list
		0x00, 0x05, '1', '.', '2', '.', '5',
	}
	if !bytes.Contains(raw, want) {
		t.Fatalf("user information\n% x\ndoes not contain\n% x", raw, want)
	}

	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.CommonExtendedNegotiations) != 1 {
		t.Fatalf("decoded %d items, want 1", len(got.CommonExtendedNegotiations))
	}
	ce := got.CommonExtendedNegotiations[0]
	if ce.SOPClassUID != "1.2.3" || ce.ServiceClassUID != "1.2.4" {
		t.Fatalf("item = %+v, want SOP 1.2.3 under service 1.2.4", ce)
	}
	if len(ce.RelatedGeneralSOPClasses) != 1 || ce.RelatedGeneralSOPClasses[0] != "1.2.5" {
		t.Fatalf("related general classes = %v, want [1.2.5]", ce.RelatedGeneralSOPClasses)
	}
}

// The case this sub-item exists for: Storage Commitment announces that it
// commits to the storage classes it serves, and names the general classes an
// SR flavour can be matched to. The list is what makes C-FIND against a general
// class answer for a specialised one (PS3.4 C.4.1.1.3).
func TestCommonExtendedNegotiationRoundTrip(t *testing.T) {
	ce := CommonExtendedNegotiation{
		SOPClassUID:     "1.2.840.10008.5.1.4.1.1.88.40",
		ServiceClassUID: "1.2.840.10008.1.20.1",
		RelatedGeneralSOPClasses: []string{
			"1.2.840.10008.5.1.4.1.1.88.22",
			"1.2.840.10008.5.1.4.1.1.88.11",
		},
	}
	ui := UserInformation{
		ImplementationClassUID:     "1.2.3",
		CommonExtendedNegotiations: []CommonExtendedNegotiation{ce},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.CommonExtendedNegotiations) != 1 {
		t.Fatalf("decoded %d items, want 1", len(got.CommonExtendedNegotiations))
	}
	back := got.CommonExtendedNegotiations[0]
	if back.SOPClassUID != ce.SOPClassUID || back.ServiceClassUID != ce.ServiceClassUID {
		t.Fatalf("item = %+v, want %+v", back, ce)
	}
	if len(back.RelatedGeneralSOPClasses) != len(ce.RelatedGeneralSOPClasses) {
		t.Fatalf("related general classes = %v, want %v", back.RelatedGeneralSOPClasses, ce.RelatedGeneralSOPClasses)
	}
	for i := range ce.RelatedGeneralSOPClasses {
		if back.RelatedGeneralSOPClasses[i] != ce.RelatedGeneralSOPClasses[i] {
			t.Fatalf("related general class %d = %q, want %q", i, back.RelatedGeneralSOPClasses[i], ce.RelatedGeneralSOPClasses[i])
		}
	}
}

// An empty related-general list is legal: the sub-item still answers "this
// class is used under this Service Class". Writing a length of 0 and reading it
// back as "no list" has to be distinct from the item being absent altogether.
func TestCommonExtendedNegotiationWithoutRelatedClasses(t *testing.T) {
	ui := UserInformation{
		ImplementationClassUID: "1.2.3",
		CommonExtendedNegotiations: []CommonExtendedNegotiation{{
			SOPClassUID:     "1.2.840.10008.5.1.4.1.1.2",
			ServiceClassUID: "1.2.840.10008.1.20.1",
		}},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.CommonExtendedNegotiations) != 1 {
		t.Fatalf("decoded %d items, want 1", len(got.CommonExtendedNegotiations))
	}
	if n := len(got.CommonExtendedNegotiations[0].RelatedGeneralSOPClasses); n != 0 {
		t.Fatalf("related general classes = %v, want none", got.CommonExtendedNegotiations[0].RelatedGeneralSOPClasses)
	}
}

// A padded UID is what a peer produces if it applies the PS3.5 9.1 even-length
// rule to these fields, which D.3.3.6 does not ask for. Accepting it costs one
// trim and avoids rejecting an otherwise correct association.
func TestCommonExtendedNegotiationTrimsPaddedUIDs(t *testing.T) {
	sop := "1.2.840.10008.5.1.4.1.1.2\x00"
	svc := "1.2.840.10008.1.20.1\x00"
	body := make([]byte, 0, 64)
	body = binaryUint16(body, uint16(len(sop)))
	body = append(body, sop...)
	body = binaryUint16(body, uint16(len(svc)))
	body = append(body, svc...)
	body = binaryUint16(body, uint16(2+len("1.2.3\x00")))
	body = binaryUint16(body, uint16(len("1.2.3\x00")))
	body = append(body, "1.2.3\x00"...)

	ce, err := decodeCommonExtendedNegotiation(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ce.SOPClassUID != "1.2.840.10008.5.1.4.1.1.2" {
		t.Fatalf("SOP Class UID = %q, want it unpadded", ce.SOPClassUID)
	}
	if ce.ServiceClassUID != "1.2.840.10008.1.20.1" {
		t.Fatalf("Service Class UID = %q, want it unpadded", ce.ServiceClassUID)
	}
	if len(ce.RelatedGeneralSOPClasses) != 1 || ce.RelatedGeneralSOPClasses[0] != "1.2.3" {
		t.Fatalf("related general classes = %q, want [1.2.3]", ce.RelatedGeneralSOPClasses)
	}
}

// binaryUint16 is the append form the encoder uses, repeated here so the
// hand-built bodies above are independent of it.
func binaryUint16(dst []byte, v uint16) []byte {
	return append(dst, byte(v>>8), byte(v))
}

// Every length in this sub-item is a peer's claim, and each one indexes the
// bytes that follow. A claim larger than the item must be an error: reading it
// as a shorter answer would hand the caller a plausible UID that was never sent.
func TestCommonExtendedNegotiationMalformed(t *testing.T) {
	sop := []byte("1.2.3")
	svc := []byte("1.2.4")
	valid := func() []byte {
		body := binaryUint16(nil, uint16(len(sop)))
		body = append(body, sop...)
		body = binaryUint16(body, uint16(len(svc)))
		body = append(body, svc...)
		body = binaryUint16(body, 0)
		return body
	}

	t.Run("shorter than its three length fields", func(t *testing.T) {
		if _, err := decodeCommonExtendedNegotiation(valid()[:5]); err == nil {
			t.Fatal("a five-byte item decoded without error")
		}
	})

	t.Run("SOP Class UID length exceeds item", func(t *testing.T) {
		body := binaryUint16(nil, 0x40)
		body = append(body, sop...)
		if _, err := decodeCommonExtendedNegotiation(body); err == nil {
			t.Fatal("a UID longer than its item decoded without error")
		}
	})

	t.Run("Service Class UID length exceeds item", func(t *testing.T) {
		body := valid()
		// The Service Class length lives right after the SOP Class UID.
		off := 2 + len(sop)
		body[off] = 0x40
		if _, err := decodeCommonExtendedNegotiation(body); err == nil {
			t.Fatal("a Service Class UID longer than its item decoded without error")
		}
	})

	t.Run("related list length exceeds item", func(t *testing.T) {
		body := valid()
		off := 2 + len(sop) + 2 + len(svc)
		body[off] = 0x00
		body[off+1] = 0x10
		if _, err := decodeCommonExtendedNegotiation(body); err == nil {
			t.Fatal("a related general list longer than its item decoded without error")
		}
	})

	t.Run("related UID length exceeds list", func(t *testing.T) {
		body := binaryUint16(nil, uint16(len(sop)))
		body = append(body, sop...)
		body = binaryUint16(body, uint16(len(svc)))
		body = append(body, svc...)
		list := binaryUint16(nil, 0x40)
		list = append(list, '1')
		body = binaryUint16(body, uint16(len(list)))
		body = append(body, list...)
		if _, err := decodeCommonExtendedNegotiation(body); err == nil {
			t.Fatal("a related general UID longer than the list decoded without error")
		}
	})

	t.Run("related list ends mid length", func(t *testing.T) {
		body := binaryUint16(nil, uint16(len(sop)))
		body = append(body, sop...)
		body = binaryUint16(body, uint16(len(svc)))
		body = append(body, svc...)
		body = binaryUint16(body, 1)
		body = append(body, 0x00)
		if _, err := decodeCommonExtendedNegotiation(body); err == nil {
			t.Fatal("a one-byte related general list decoded without error")
		}
	})
}

// The encoding cannot invent the two UIDs the sub-item is about, and an empty
// UID would produce a zero-length field that decodes as a present-but-empty
// class — worse than refusing to encode.
func TestCommonExtendedNegotiationEncodeValidation(t *testing.T) {
	cases := map[string]CommonExtendedNegotiation{
		"no SOP Class UID": {
			ServiceClassUID: "1.2.840.10008.1.20.1",
		},
		"no Service Class UID": {
			SOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
		},
		"empty related general UID": {
			SOPClassUID:              "1.2.840.10008.5.1.4.1.1.2",
			ServiceClassUID:          "1.2.840.10008.1.20.1",
			RelatedGeneralSOPClasses: []string{""},
		},
		"oversized related general UID": {
			SOPClassUID:              "1.2.840.10008.5.1.4.1.1.2",
			ServiceClassUID:          "1.2.840.10008.1.20.1",
			RelatedGeneralSOPClasses: []string{string(bytes.Repeat([]byte("1"), maxItemLength+1))},
		},
		"oversized SOP Class UID": {
			SOPClassUID:     string(bytes.Repeat([]byte("1"), maxItemLength+1)),
			ServiceClassUID: "1.2.840.10008.1.20.1",
		},
		"oversized Service Class UID": {
			SOPClassUID:     "1.2.840.10008.5.1.4.1.1.2",
			ServiceClassUID: string(bytes.Repeat([]byte("1"), maxItemLength+1)),
		},
		"related general list overflows its length field": {
			SOPClassUID:              "1.2.840.10008.5.1.4.1.1.2",
			ServiceClassUID:          "1.2.840.10008.1.20.1",
			RelatedGeneralSOPClasses: overlongRelatedClasses(),
		},
	}
	for name, ce := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := encodeUserInformation(UserInformation{
				ImplementationClassUID:     "1.2.3",
				CommonExtendedNegotiations: []CommonExtendedNegotiation{ce},
			}); err == nil {
				t.Fatal("encoded without error")
			}
		})
	}
}

// overlongRelatedClasses builds a list whose entries are each legal but whose
// total no longer fits the two-byte length that describes it. The check has to
// happen on the sum, not per entry: a peer reading the field back would stop
// that many bytes in and silently drop the rest of the list.
func overlongRelatedClasses() []string {
	const uid = "1.2.840.10008.5.1.4.1.1.88.22"
	n := maxItemLength/(2+len(uid)) + 1
	out := make([]string, n)
	for i := range out {
		out[i] = uid
	}
	return out
}
