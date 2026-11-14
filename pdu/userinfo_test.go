package pdu

import (
	"bytes"
	"testing"
)

// uiBody strips the ItemUserInformation wrapper: encodeUserInformation returns
// the whole item, while decodeUserInformation takes its payload, the way the
// A-ASSOCIATE decoders call it.
func uiBody(t *testing.T, item []byte) []byte {
	t.Helper()
	items, err := decodeItems(item)
	if err != nil {
		t.Fatalf("decode user information item: %v", err)
	}
	if len(items) != 1 || items[0].Type != ItemUserInformation {
		t.Fatalf("expected one ItemUserInformation, got %+v", items)
	}
	return items[0].Data
}

// The Asynchronous Operations Window sub-item is four bytes with no padding:
// Max Invoked then Max Performed, both big endian (PS3.8 D.3.3.3). The byte
// layout matters because a requestor reads it back to decide whether it may
// pipeline a second operation.
func TestAsyncOperationsWindowWireFormat(t *testing.T) {
	ui := UserInformation{
		ImplementationClassUID: "1.2.3",
		AsyncOperationsWindow:  &AsyncOperationsWindow{MaxInvoked: 2, MaxPerformed: 1},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{ItemAsyncOperationsWindow, 0x00, 0x00, 0x04, 0x00, 0x02, 0x00, 0x01}
	if !bytes.Contains(raw, want) {
		t.Fatalf("user information % x does not contain % x", raw, want)
	}

	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AsyncOperationsWindow == nil {
		t.Fatal("window was not preserved through the round trip")
	}
	if *got.AsyncOperationsWindow != (AsyncOperationsWindow{MaxInvoked: 2, MaxPerformed: 1}) {
		t.Fatalf("window = %+v, want 2/1", *got.AsyncOperationsWindow)
	}
}

// Zero/zero is the protocol's "no limit", and it must survive the round trip
// distinctly from "the peer sent no window at all": the first is a negotiated
// answer, the second is silence.
func TestAsyncOperationsWindowUnlimitedIsNotAbsent(t *testing.T) {
	ui := UserInformation{
		ImplementationClassUID: "1.2.3",
		AsyncOperationsWindow:  &AsyncOperationsWindow{},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AsyncOperationsWindow == nil {
		t.Fatal("unlimited window decoded as absent")
	}
	if !got.AsyncOperationsWindow.Unlimited() {
		t.Fatalf("window = %+v, want the unlimited (0/0) value", *got.AsyncOperationsWindow)
	}
}

// A SOP Class Extended Negotiation carries a length-prefixed UID followed by
// application information whose meaning belongs to the SOP Class. The acceptor
// has to give the info back byte for byte: it is opaque here.
func TestExtendedNegotiationRoundTrip(t *testing.T) {
	info := []byte{0x01, 0x00, 0x00, 0x02}
	ui := UserInformation{
		ImplementationClassUID: "1.2.3",
		ExtendedNegotiations: []ExtendedNegotiation{
			{SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", Info: info},
			{SOPClassUID: "1.2.840.10008.5.1.4.1.1.4", Info: nil},
		},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// 0x56 0x00 <len> <uid len> <uid> <info>, with len = 2 + uid + info.
	sop := []byte("1.2.840.10008.5.1.4.1.1.2")
	want := append([]byte{ItemSOPClassExtendedNegotiation, 0x00, 0x00, byte(2 + len(sop) + len(info)), 0x00, byte(len(sop))}, sop...)
	want = append(want, info...)
	if !bytes.Contains(raw, want) {
		t.Fatalf("user information % x does not contain % x", raw, want)
	}

	got, err := decodeUserInformation(uiBody(t, raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ExtendedNegotiations) != 2 {
		t.Fatalf("decoded %d items, want 2", len(got.ExtendedNegotiations))
	}
	if got.ExtendedNegotiations[0].SOPClassUID != string(sop) || !bytes.Equal(got.ExtendedNegotiations[0].Info, info) {
		t.Fatalf("first item = %+v, want %s with info % x", got.ExtendedNegotiations[0], sop, info)
	}
	if got.ExtendedNegotiations[1].Info != nil {
		t.Fatalf("empty info came back as % x", got.ExtendedNegotiations[1].Info)
	}
}

// Unknown user-information sub-items must be skipped, not rejected: an
// extension this version does not implement is the peer's business, and
// failing the association over it would break forward compatibility.
func TestUnknownUserInformationItemIsIgnored(t *testing.T) {
	data, err := appendItem(nil, ItemImplementationClassUID, []byte("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	if data, err = appendItem(data, 0x60, []byte("extension")); err != nil {
		t.Fatal(err)
	}
	ui, err := decodeUserInformation(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ui.ImplementationClassUID != "1.2.3" {
		t.Fatalf("implementation class UID = %q, want 1.2.3", ui.ImplementationClassUID)
	}
}

func TestUserInformationMalformed(t *testing.T) {
	t.Run("async window is not four bytes", func(t *testing.T) {
		data, err := appendItem(nil, ItemAsyncOperationsWindow, []byte{0x00, 0x01})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeUserInformation(data); err == nil {
			t.Fatal("a three-value window decoded without error")
		}
	})

	t.Run("extended negotiation UID length exceeds item", func(t *testing.T) {
		body := []byte{0x00, 0x40, '1', '2'}
		data, err := appendItem(nil, ItemSOPClassExtendedNegotiation, body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeUserInformation(data); err == nil {
			t.Fatal("a UID longer than its item decoded without error")
		}
	})

	t.Run("extended negotiation without SOP class", func(t *testing.T) {
		if _, err := encodeUserInformation(UserInformation{
			ImplementationClassUID: "1.2.3",
			ExtendedNegotiations:   []ExtendedNegotiation{{Info: []byte{1}}},
		}); err == nil {
			t.Fatal("an item with no SOP Class UID encoded without error")
		}
	})
}
