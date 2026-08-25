package pdu

import (
	"bytes"
	"testing"
)

// Read is the first thing a peer's bytes reach: it runs on an open port, before
// any AE title has been checked and before an association exists, so every
// decoder beneath it is reachable by anyone who can connect. The golden fixtures
// already show that well-formed PDUs decode; what is left to establish is that
// nothing decodes into a panic, a hang, or a PDU whose fields contradict the
// bytes it came from.
func FuzzRead(f *testing.F) {
	for _, seed := range [][]byte{
		goldenAAssociateRQ,
		goldenAAssociateAC,
		goldenAReleaseRQ,
		goldenAReleaseRP,
		goldenAAbort,
		goldenPDataTFRQ,
		goldenPDataTF,
	} {
		f.Add(seed)
	}
	// An A-ASSOCIATE-RJ, and an RQ carrying what the golden pair leaves out:
	// role selection and user identity keep their own length fields inside the
	// item's, which is where a decoder loses track if it is going to.
	for _, p := range []PDU{
		&AAssociateRJ{
			Result:           RejectResultPermanent,
			Source:           RejectSourceServiceUser,
			ReasonDiagnostic: RejectReasonCalledAENotRecognized,
		},
		&AAssociateRQ{
			CalledAETitle:  "ANY-SCP",
			CallingAETitle: "FUZZSCU",
			PresentationContexts: []PresentationContextRQ{{
				ID:               1,
				AbstractSyntax:   VerificationSOPClass,
				TransferSyntaxes: []string{ImplicitVRLittleEndian},
			}},
			UserInformation: UserInformation{
				MaxLength:                 DefaultMaxPDULength,
				ImplementationClassUID:    "1.2.826.0.1.3680043.9.3811.0.9.0",
				ImplementationVersionName: "GONETDICOM",
				RoleSelections: []RoleSelection{
					{SOPClassUID: VerificationSOPClass, SCURole: true, SCPRole: true},
				},
				UserIdentityRQ: &UserIdentityRQ{
					Type:           UserIdentityUsernamePasscode,
					PrimaryField:   []byte("alice"),
					SecondaryField: []byte("secret"),
				},
			},
		},
	} {
		b, err := p.Encode()
		if err != nil {
			f.Fatalf("seed %T: %v", p, err)
		}
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		// A limit far below MaxPDUReadLength keeps the corpus from drifting towards
		// inputs that are merely long, without shielding any decoder.
		p, err := ReadLimit(bytes.NewReader(in), 64<<10)
		if err != nil {
			// Peer bytes: any error is a fine answer.
			return
		}
		if p.Type() != in[0] {
			t.Fatalf("PDU type 0x%02x decoded as a 0x%02x", in[0], p.Type())
		}

		// Encoding what was decoded and reading that back must reach the same
		// bytes. This is the failure worth catching: the association code echoes
		// negotiated items back to the peer, so a length field read differently
		// from how Encode writes it becomes a malformed PDU on the wire rather
		// than an error here.
		first, err := p.Encode()
		if err != nil {
			// Values a peer may send that this package will not originate — an empty
			// AE title, a User Information item with no Implementation Class UID —
			// are a refusal to encode, not a round-trip failure.
			return
		}
		q, err := ReadLimit(bytes.NewReader(first), 64<<10)
		if err != nil {
			t.Fatalf("re-reading own encoding of %T: %v\n% x", p, err, first)
		}
		second, err := q.Encode()
		if err != nil {
			t.Fatalf("re-encoding %T: %v", q, err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("%T does not survive a round trip:\n first: % x\nsecond: % x", p, first, second)
		}
	})
}

// DecodePDataTF walks a list of PDV items whose lengths are 32-bit and come
// straight off the wire, and it is the one decoder that runs on every message of
// an established association rather than once per handshake.
func FuzzDecodePDataTF(f *testing.F) {
	f.Add(goldenPDataTFRQ)
	f.Add(goldenPDataTF)

	// Several PDVs in one PDU, and a fragment that is MCH-only: both are legal
	// and neither appears in the goldens.
	multi := &PDataTF{PDVs: []PDV{
		NewCommandPDV(1, []byte("command")),
		NewDataPDV(1, nil),
		{ContextID: 3, Value: []byte{MCHLastFragment}},
	}}
	b, err := multi.Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)

	f.Fuzz(func(t *testing.T, in []byte) {
		p, err := DecodePDataTF(in)
		if err != nil {
			return
		}
		for i, pdv := range p.PDVs {
			// Everything above this decoder reads the Message Control Header out of
			// Value[0] — IsCommand, IsLast, Fragment — and none of them can tell an
			// empty value from a data fragment. Nor would Encode accept one back.
			if len(pdv.Value) == 0 {
				t.Fatalf("PDV %d decoded with an empty value", i)
			}
		}

		out, err := p.Encode()
		if err != nil {
			t.Fatalf("re-encoding a decoded P-DATA-TF: %v", err)
		}
		// The byte after the PDU type is reserved and is not kept in the struct, so
		// normalise it rather than pretend it round-trips.
		want := append([]byte(nil), in...)
		want[1] = 0
		if !bytes.Equal(want, out) {
			t.Fatalf("re-encoding changed the PDU:\n in: % x\nout: % x", want, out)
		}
	})
}
