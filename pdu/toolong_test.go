package pdu

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A User Identity primary field holds whatever credential the caller was given:
// a Kerberos ticket, a SAML assertion, a JWT. Those run past 64 KiB, and both
// the field and the item around it are length-prefixed with 16 bits. The lengths
// used to be written truncated — the PDU encoded without complaint, and the peer
// decoded a short credential followed by whatever the overflow happened to look
// like.
func TestEncodeRefusesOversizedUserIdentity(t *testing.T) {
	t.Parallel()

	big := bytes.Repeat([]byte("s"), 70*1024)
	rq := &AAssociateRQ{
		CalledAETitle:          "SCP",
		CallingAETitle:         "SCU",
		ApplicationContextName: ApplicationContextName,
		UserInformation: UserInformation{
			MaxLength:              DefaultMaxPDULength,
			ImplementationClassUID: "1.2.3.4",
			UserIdentityRQ: &UserIdentityRQ{
				Type:         UserIdentitySAML,
				PrimaryField: big,
			},
		},
	}

	raw, err := rq.Encode()
	if errors.Is(err, ErrTooLong) {
		return
	}
	if err != nil {
		t.Fatalf("Encode: got %v, want ErrTooLong", err)
	}
	// It encoded. Show what it put on the wire, which is the reason this has to be
	// an error rather than a best effort.
	got, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Encode accepted a %d-byte primary field and produced a PDU that will not decode: %v", len(big), err)
	}
	decoded, ok := got.(*AAssociateRQ)
	if !ok {
		t.Fatalf("round trip gave %T", got)
	}
	if decoded.UserInformation.UserIdentityRQ == nil {
		t.Fatalf("Encode accepted a %d-byte primary field and the identity item vanished", len(big))
	}
	t.Fatalf("Encode accepted a %d-byte primary field and sent %d bytes of it",
		len(big), len(decoded.UserInformation.UserIdentityRQ.PrimaryField))
}

// The same 16-bit ceiling applies to an item built out of sub-items: enough
// transfer syntaxes on one presentation context overflow the context item, and
// the peer then reads the remainder as further items.
func TestEncodeRefusesOversizedPresentationContext(t *testing.T) {
	t.Parallel()

	syntaxes := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		syntaxes = append(syntaxes, "1.2.840.10008.1.2."+strings.Repeat("9", 30))
	}
	rq := &AAssociateRQ{
		CalledAETitle:          "SCP",
		CallingAETitle:         "SCU",
		ApplicationContextName: ApplicationContextName,
		PresentationContexts: []PresentationContextRQ{{
			ID:               1,
			AbstractSyntax:   VerificationSOPClass,
			TransferSyntaxes: syntaxes,
		}},
		UserInformation: UserInformation{
			MaxLength:              DefaultMaxPDULength,
			ImplementationClassUID: "1.2.3.4",
		},
	}
	if _, err := rq.Encode(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("Encode: got %v, want ErrTooLong", err)
	}
}

// The boundary itself still encodes: the field holds 65535, so 65535 is legal.
func TestEncodeItemAtLengthBoundary(t *testing.T) {
	t.Parallel()

	item, err := encodeItem(ItemApplicationContext, bytes.Repeat([]byte("x"), maxItemLength))
	if err != nil {
		t.Fatalf("%d bytes: %v", maxItemLength, err)
	}
	if len(item) != 4+maxItemLength {
		t.Fatalf("item is %d bytes, want %d", len(item), 4+maxItemLength)
	}
	items, err := decodeItems(item)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || len(items[0].Data) != maxItemLength {
		t.Fatalf("round trip gave %d items", len(items))
	}
	if _, err := encodeItem(ItemApplicationContext, bytes.Repeat([]byte("x"), maxItemLength+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("%d bytes: got %v, want ErrTooLong", maxItemLength+1, err)
	}
}
