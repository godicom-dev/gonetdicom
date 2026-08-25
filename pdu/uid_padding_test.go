package pdu

import (
	"bytes"
	"slices"
	"testing"
)

// padOnTheWire is the NUL a conforming peer appends to an odd-length UID
// (PS3.5 9.1). Encode writes these strings verbatim, so appending it to a field
// is what puts the padding on the wire — short of assembling a PDU by hand,
// there is no other way to build one.
const padOnTheWire = "\x00"

// implClassUIDOdd is odd-length, so padding it is what a conforming peer does
// rather than something only this test would produce.
const implClassUIDOdd = "1.2.826.0.1.3680043.10.541.31"

// requireOddLength guards the premise: a peer pads because the UID is odd, so an
// even-length one here would make the rest of the test a fiction.
func requireOddLength(t *testing.T, uids ...string) {
	t.Helper()
	for _, u := range uids {
		if len(u)%2 == 0 {
			t.Fatalf("%q is %d bytes: a conforming peer would not pad it", u, len(u))
		}
	}
}

// Every UID in an A-ASSOCIATE-RQ is compared against one written in Go source
// above this package: the SCP matches the Abstract Syntax against the SOP classes
// it accepts and looks for Implicit VR among the Transfer Syntaxes, both by string
// equality. A pad byte left in the string loses those comparisons, so it comes
// off at the decoder.
func TestDecodeAAssociateRQTrimsUIDPadding(t *testing.T) {
	t.Parallel()

	requireOddLength(t, ApplicationContextName, VerificationSOPClass, ImplicitVRLittleEndian, implClassUIDOdd)

	rq := &AAssociateRQ{
		CalledAETitle:          "ANY-SCP",
		CallingAETitle:         "PADSCU",
		ApplicationContextName: ApplicationContextName + padOnTheWire,
		PresentationContexts: []PresentationContextRQ{{
			ID:               1,
			AbstractSyntax:   VerificationSOPClass + padOnTheWire,
			TransferSyntaxes: []string{ImplicitVRLittleEndian + padOnTheWire},
		}},
		UserInformation: UserInformation{
			MaxLength:              DefaultMaxPDULength,
			ImplementationClassUID: implClassUIDOdd + padOnTheWire,
			RoleSelections: []RoleSelection{
				{SOPClassUID: VerificationSOPClass + padOnTheWire, SCURole: true, SCPRole: true},
			},
		},
	}
	raw, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(VerificationSOPClass+padOnTheWire)) {
		t.Fatal("the padding never reached the wire: this test would pass without a decoder that trims")
	}

	got, err := DecodeAAssociateRQ(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ApplicationContextName != ApplicationContextName {
		t.Errorf("Application Context Name %q, want %q", got.ApplicationContextName, ApplicationContextName)
	}
	if len(got.PresentationContexts) != 1 {
		t.Fatalf("got %d presentation contexts, want 1", len(got.PresentationContexts))
	}
	pc := got.PresentationContexts[0]
	if pc.AbstractSyntax != VerificationSOPClass {
		t.Errorf("Abstract Syntax %q, want %q", pc.AbstractSyntax, VerificationSOPClass)
	}
	if want := []string{ImplicitVRLittleEndian}; !slices.Equal(pc.TransferSyntaxes, want) {
		t.Errorf("Transfer Syntaxes %q, want %q", pc.TransferSyntaxes, want)
	}
	if got := got.UserInformation.ImplementationClassUID; got != implClassUIDOdd {
		t.Errorf("Implementation Class UID %q, want %q", got, implClassUIDOdd)
	}

	roles := got.UserInformation.RoleSelections
	if len(roles) != 1 {
		t.Fatalf("got %d role selections, want 1", len(roles))
	}
	// The SCU/SCP role bytes follow the UID at the length the peer declared, so
	// trimming must not shift the offsets they are read from.
	if roles[0].SOPClassUID != VerificationSOPClass || !roles[0].SCURole || !roles[0].SCPRole {
		t.Errorf("role selection %+v, want %q as SCU and SCP", roles[0], VerificationSOPClass)
	}
}

// The AC's Transfer Syntax is the one the SCU then hands to godicom to decode and
// encode every dataset on the association, so a pad byte surviving here does not
// fail a comparison — it names a transfer syntax that does not exist.
func TestDecodeAAssociateACTrimsUIDPadding(t *testing.T) {
	t.Parallel()

	requireOddLength(t, ApplicationContextName, ImplicitVRLittleEndian, implClassUIDOdd)

	ac := &AAssociateAC{
		CalledAETitle:          "ANY-SCP",
		CallingAETitle:         "PADSCU",
		ApplicationContextName: ApplicationContextName + padOnTheWire,
		PresentationContexts: []PresentationContextAC{{
			ID:             1,
			TransferSyntax: ImplicitVRLittleEndian + padOnTheWire,
		}},
		UserInformation: UserInformation{
			MaxLength:              DefaultMaxPDULength,
			ImplementationClassUID: implClassUIDOdd + padOnTheWire,
		},
	}
	raw, err := ac.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(ImplicitVRLittleEndian+padOnTheWire)) {
		t.Fatal("the padding never reached the wire: this test would pass without a decoder that trims")
	}

	got, err := DecodeAAssociateAC(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ApplicationContextName != ApplicationContextName {
		t.Errorf("Application Context Name %q, want %q", got.ApplicationContextName, ApplicationContextName)
	}
	if len(got.PresentationContexts) != 1 {
		t.Fatalf("got %d presentation contexts, want 1", len(got.PresentationContexts))
	}
	if ts := got.PresentationContexts[0].TransferSyntax; ts != ImplicitVRLittleEndian {
		t.Errorf("accepted Transfer Syntax %q, want %q", ts, ImplicitVRLittleEndian)
	}
	if uid := got.UserInformation.ImplementationClassUID; uid != implClassUIDOdd {
		t.Errorf("Implementation Class UID %q, want %q", uid, implClassUIDOdd)
	}
}

// Every trailing NUL comes off, not just the one PS3.5 asks for. Trimming
// exactly one is not stable under re-encoding — the leftover NUL goes back on the
// wire and the next decode takes off another — and the association code echoes
// negotiated items to its peer, so a decode that shifts by a byte per pass
// changes the item it is echoing. FuzzRead found this; corpus entry
// 67a2b7e903f84363 is the input.
func TestTrimUIDPaddingRemovesEveryTrailingNUL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{[]byte(VerificationSOPClass), VerificationSOPClass},
		{[]byte(VerificationSOPClass + "\x00"), VerificationSOPClass},
		{[]byte(VerificationSOPClass + "\x00\x00"), VerificationSOPClass},
		{nil, ""},
		{[]byte{}, ""},
		{[]byte{0x00}, ""},
		{[]byte{0x00, 0x00}, ""},
	} {
		got := trimUIDPadding(tc.in)
		if got != tc.want {
			t.Errorf("trimUIDPadding(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		// Idempotence is the property the round trip needs, so state it here too
		// rather than leave it to the fuzzer to rediscover.
		if again := trimUIDPadding([]byte(got)); again != got {
			t.Errorf("trimUIDPadding(%q) is not idempotent: %q then %q", tc.in, got, again)
		}
	}
}
