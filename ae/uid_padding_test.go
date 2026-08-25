package ae_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// pad is the NUL a conforming peer appends to an odd-length UID (PS3.5 9.1). The
// three UIDs traded here are all odd-length, so this is what arrives from a peer
// that pads — pynetdicom does not send it, but it tolerates it on the way in, and
// the standard is on the side of the peer that does.
const pad = "\x00"

// The SCP matches a proposed Abstract Syntax against the SOP classes it accepts
// by string equality, and looks for Implicit VR among the proposed Transfer
// Syntaxes the same way. With the padding left in place both comparisons lose:
// the SCP answered "abstract syntax not supported" for Verification, which it
// always supports, and where it did accept a context it echoed the padded
// Transfer Syntax back in the AC and then handed that to the decoder.
func TestSCPAcceptsPaddedUIDs(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})

	rq := associateRQ("REAL_SCP")
	rq.ApplicationContextName = pdu.ApplicationContextName + pad
	rq.PresentationContexts[0].AbstractSyntax = pdu.VerificationSOPClass + pad
	rq.PresentationContexts[0].TransferSyntaxes = []string{pdu.ImplicitVRLittleEndian + pad}

	ac, ok := associate(t, addr, rq).(*pdu.AAssociateAC)
	if !ok {
		t.Fatal("expected an A-ASSOCIATE-AC")
	}
	if len(ac.PresentationContexts) != 1 {
		t.Fatalf("got %d presentation contexts in the AC, want 1", len(ac.PresentationContexts))
	}
	got := ac.PresentationContexts[0]
	if got.Result != 0 {
		t.Fatalf("presentation context result %d, want 0 (acceptance)", got.Result)
	}
	if got.TransferSyntax != pdu.ImplicitVRLittleEndian {
		t.Errorf("accepted Transfer Syntax %q, want %q", got.TransferSyntax, pdu.ImplicitVRLittleEndian)
	}
}

// Role Selection carries its SOP Class UID behind its own length field, and the
// SCP looks the negotiated roles up by that UID. A pad byte left on made the
// lookup miss, so the peer's proposed roles were dropped and it got the acceptor
// defaults instead of an answer to what it asked for.
func TestSCPAcceptsPaddedRoleSelectionUID(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "REAL_SCP",
		RoleSelections: []pdu.RoleSelection{
			{SOPClassUID: pdu.VerificationSOPClass, SCURole: true, SCPRole: true},
		},
	})

	rq := associateRQ("REAL_SCP")
	rq.UserInformation.RoleSelections = []pdu.RoleSelection{
		{SOPClassUID: pdu.VerificationSOPClass + pad, SCURole: true, SCPRole: true},
	}

	ac, ok := associate(t, addr, rq).(*pdu.AAssociateAC)
	if !ok {
		t.Fatal("expected an A-ASSOCIATE-AC")
	}
	var got *pdu.RoleSelection
	for i, r := range ac.UserInformation.RoleSelections {
		if r.SOPClassUID == pdu.VerificationSOPClass {
			got = &ac.UserInformation.RoleSelections[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no role selection for %q in the AC, got %+v",
			pdu.VerificationSOPClass, ac.UserInformation.RoleSelections)
	}
	if !got.SCURole || !got.SCPRole {
		t.Errorf("negotiated roles SCU=%v SCP=%v, want both", got.SCURole, got.SCPRole)
	}
}
