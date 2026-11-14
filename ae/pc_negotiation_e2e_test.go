package ae_test

import (
	"testing"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Presentation Context results (PS3.8 Table 9-18). They are not interchangeable:
// 3 says the SOP Class is unknown here, 4 says we know it but cannot read any of
// the offered encodings. A peer uses the first to stop asking, the second to
// retry with another syntax.
const (
	pcAcceptance               = 0
	pcUserRejection            = 1
	pcAbstractSyntaxNotSupport = 3
	pcTransferSyntaxesNotSup   = 4
)

// requireContextResults checks the result of every context in the AC, matched by
// ID so a reordered reply still fails loudly.
func requireContextResults(t *testing.T, got pdu.PDU, want map[byte]byte) {
	t.Helper()
	ac, ok := got.(*pdu.AAssociateAC)
	if !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", got)
	}
	if len(ac.PresentationContexts) != len(want) {
		t.Fatalf("AC carries %d contexts, want %d", len(ac.PresentationContexts), len(want))
	}
	for _, pc := range ac.PresentationContexts {
		expected, known := want[pc.ID]
		if !known {
			t.Errorf("AC carries unexpected context ID %d", pc.ID)
			continue
		}
		if pc.Result != expected {
			t.Errorf("context %d result=%d, want %d", pc.ID, pc.Result, expected)
		}
	}
}

// acceptRQ builds an A-ASSOCIATE-RQ proposing contexts as given.
func acceptRQ(calledAE string, contexts ...pdu.PresentationContextRQ) *pdu.AAssociateRQ {
	rq := associateRQ(calledAE)
	rq.PresentationContexts = contexts
	return rq
}

func contextRQ(id byte, abstractSyntax string, syntaxes ...string) pdu.PresentationContextRQ {
	return pdu.PresentationContextRQ{
		ID:               id,
		AbstractSyntax:   abstractSyntax,
		TransferSyntaxes: syntaxes,
	}
}

// A syntax we cannot decode has to be refused at negotiation, not accepted and
// then failed in the store handler. Accepting it puts a claim in the AC that
// the next C-STORE disproves.
func TestSCPRejectsUnsupportedTransferSyntax(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SYNTAXSCP",
		AcceptedAbstractSyntaxes: []string{string(uid.CTImageStorage)},
	})
	got := associate(t, addr, acceptRQ("SYNTAXSCP",
		contextRQ(1, string(uid.CTImageStorage), string(uid.JPEG2000Lossless)),
	))
	requireContextResults(t, got, map[byte]byte{1: pcTransferSyntaxesNotSup})
}

// One context the peer can use and one it cannot: the association still stands,
// and each proposal gets its own answer. A single global accept/reject would
// either lose the usable context or lie about the other one.
func TestSCPPartiallyAcceptsContexts(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	mwl := string(uid.ModalityWorklistInformationModelFind)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "PARTIALSCP",
		AcceptedAbstractSyntaxes: []string{ct},
	})
	got := associate(t, addr, acceptRQ("PARTIALSCP",
		contextRQ(1, ct, pdu.ImplicitVRLittleEndian),
		contextRQ(3, mwl, pdu.ImplicitVRLittleEndian),
		contextRQ(5, ct, string(uid.JPEG2000Lossless)),
	))
	requireContextResults(t, got, map[byte]byte{
		1: pcAcceptance,
		3: pcAbstractSyntaxNotSupport,
		5: pcTransferSyntaxesNotSup,
	})
}

// The acceptor picks, from its own preference order (PS3.8 9.1.5.2). A requestor
// listing explicit big endian first still gets explicit little endian, which is
// what this server prefers — the answer must not depend on the order the peer
// typed, or two peers proposing the same set would negotiate differently.
func TestSCPPicksItsPreferredTransferSyntax(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		proposed []string
		want     string
	}{
		{
			name:     "prefers implicit over explicit little endian",
			proposed: []string{string(uid.ExplicitVRLittleEndian), pdu.ImplicitVRLittleEndian},
			want:     pdu.ImplicitVRLittleEndian,
		},
		{
			name:     "prefers explicit little endian over explicit big endian",
			proposed: []string{string(uid.ExplicitVRBigEndian), string(uid.ExplicitVRLittleEndian)},
			want:     string(uid.ExplicitVRLittleEndian),
		},
		{
			name:     "falls back to the only acceptable syntax",
			proposed: []string{string(uid.ExplicitVRBigEndian), string(uid.JPEG2000Lossless)},
			want:     string(uid.ExplicitVRBigEndian),
		},
	}

	ct := string(uid.CTImageStorage)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := serveSCP(t, ae.ServerConfig{
				AETitle:                  "PREFERSCP",
				AcceptedAbstractSyntaxes: []string{ct},
			})
			got := associate(t, addr, acceptRQ("PREFERSCP", contextRQ(1, ct, tc.proposed...)))
			ac, ok := got.(*pdu.AAssociateAC)
			if !ok {
				t.Fatalf("expected A-ASSOCIATE-AC, got %T", got)
			}
			if len(ac.PresentationContexts) != 1 {
				t.Fatalf("AC carries %d contexts, want 1", len(ac.PresentationContexts))
			}
			if pc := ac.PresentationContexts[0]; pc.Result != pcAcceptance || pc.TransferSyntax != tc.want {
				t.Fatalf("result=%d syntax=%q, want acceptance on %q", pc.Result, pc.TransferSyntax, tc.want)
			}
		})
	}
}

// A pass-through SCP stores bytes it never decodes, so it can accept a syntax
// godicom cannot read. That is opt-in: "*" is a statement about this server, and
// the handler is expected to use Stream rather than Decode.
func TestSCPAcceptsAnyTransferSyntaxOnRequest(t *testing.T) {
	t.Parallel()

	j2k := string(uid.JPEG2000Lossless)
	ct := string(uid.CTImageStorage)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "PASSTHRU",
		AcceptedAbstractSyntaxes: []string{ct},
		AcceptedTransferSyntaxes: []string{"*"},
	})
	got := associate(t, addr, acceptRQ("PASSTHRU", contextRQ(1, ct, j2k)))
	requireContextResults(t, got, map[byte]byte{1: pcAcceptance})

	ac := got.(*pdu.AAssociateAC)
	if ac.PresentationContexts[0].TransferSyntax != j2k {
		t.Fatalf("negotiated %q, want the proposed %q", ac.PresentationContexts[0].TransferSyntax, j2k)
	}
}

// A context proposed with no transfer syntax cannot be used for anything, and
// PS3.8 has a result code for it. It must not be read as "the peer forgot, use
// implicit" — the proposal named no encoding at all.
func TestSCPRejectsContextWithoutTransferSyntaxes(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "NOTSSCP",
		AcceptedAbstractSyntaxes: []string{ct},
	})
	got := associate(t, addr, acceptRQ("NOTSSCP", contextRQ(1, ct)))
	requireContextResults(t, got, map[byte]byte{1: pcAbstractSyntaxNotSupport})
}

// Every context refused still yields an accepted association: the AC carries the
// refusals, and it is the requestor's decision whether to release. Aborting here
// would hide the reason from a peer that wants to report it per context.
func TestSCPAcceptsAssociationWhenAllContextsAreRejected(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "ALLCONTEXTSCP",
		AcceptedAbstractSyntaxes: []string{string(uid.CTImageStorage)},
	})
	got := associate(t, addr, acceptRQ("ALLCONTEXTSCP",
		contextRQ(1, string(uid.ModalityWorklistInformationModelFind), pdu.ImplicitVRLittleEndian),
		contextRQ(3, string(uid.CTImageStorage), string(uid.JPEG2000Lossless)),
	))
	requireContextResults(t, got, map[byte]byte{
		1: pcAbstractSyntaxNotSupport,
		3: pcTransferSyntaxesNotSup,
	})
}

// Verification is proposed by nearly every SCU and must not need configuring;
// a bare ServerConfig has to be able to answer C-ECHO.
func TestSCPAcceptsVerificationWithoutConfiguration(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "BARE"})
	got := associate(t, addr, acceptRQ("BARE",
		contextRQ(1, pdu.VerificationSOPClass, pdu.ImplicitVRLittleEndian),
	))
	requireContextResults(t, got, map[byte]byte{1: pcAcceptance})
}

// Table form of the result-code decision, so a future change to the branch order
// has to face every combination at once.
func TestPresentationContextResultMatrix(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	cases := []struct {
		name     string
		accepted []string
		cfgTS    []string
		ctx      pdu.PresentationContextRQ
		want     byte
	}{
		{
			name:     "accepted abstract syntax and default syntax",
			accepted: []string{ct},
			ctx:      contextRQ(1, ct, pdu.ImplicitVRLittleEndian),
			want:     pcAcceptance,
		},
		{
			name:     "unknown abstract syntax with acceptable syntax",
			accepted: []string{ct},
			ctx:      contextRQ(1, "1.2.3.4.5.6.7", pdu.ImplicitVRLittleEndian),
			want:     pcAbstractSyntaxNotSupport,
		},
		{
			name:     "known abstract syntax with no acceptable syntax",
			accepted: []string{ct},
			cfgTS:    []string{pdu.ImplicitVRLittleEndian},
			ctx:      contextRQ(1, ct, string(uid.ExplicitVRLittleEndian)),
			want:     pcTransferSyntaxesNotSup,
		},
		{
			name:     "unknown abstract syntax outranks unknown syntax",
			accepted: []string{ct},
			ctx:      contextRQ(1, "1.2.3.4.5.6.7", string(uid.JPEG2000Lossless)),
			want:     pcAbstractSyntaxNotSupport,
		},
		{
			name:     "wildcard abstract syntax",
			accepted: []string{"*"},
			ctx:      contextRQ(1, "1.2.3.4.5.6.7", pdu.ImplicitVRLittleEndian),
			want:     pcAcceptance,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := serveSCP(t, ae.ServerConfig{
				AETitle:                  "MATRIXSCP",
				AcceptedAbstractSyntaxes: tc.accepted,
				AcceptedTransferSyntaxes: tc.cfgTS,
			})
			got := associate(t, addr, acceptRQ("MATRIXSCP", tc.ctx))
			requireContextResults(t, got, map[byte]byte{tc.ctx.ID: tc.want})
		})
	}
}
