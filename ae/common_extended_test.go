package ae_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// storageCommitment is the pairing this sub-item exists for: a storage SOP
// Class used under the Storage Commitment Push Model Service Class.
var storageCommitment = pdu.CommonExtendedNegotiation{
	SOPClassUID:     "1.2.840.10008.5.1.4.1.1.88.22",
	ServiceClassUID: "1.2.840.10008.1.20.1",
	RelatedGeneralSOPClasses: []string{
		"1.2.840.10008.5.1.4.1.1.88.11",
	},
}

// Common extended negotiation is answered per SOP Class, and acceptance echoes
// the statement back: the requestor decides whether to commit based on what the
// acceptor confirmed, so a dropped or reordered related-general list would make
// it commit to a class the acceptor never claimed.
func TestCommonExtendedNegotiationAnsweredPerClass(t *testing.T) {
	t.Parallel()

	const declined = "1.2.840.10008.5.1.4.1.1.2"
	var calls int
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{storageCommitment.SOPClassUID, declined},
		CommonExtendedNegotiation: func(ce pdu.CommonExtendedNegotiation) bool {
			calls++
			return ce.SOPClassUID == storageCommitment.SOPClassUID
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: storageCommitment.SOPClassUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: declined, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		CommonExtendedNegotiations: []pdu.CommonExtendedNegotiation{
			storageCommitment,
			{SOPClassUID: declined, ServiceClassUID: storageCommitment.ServiceClassUID},
		},
	})

	if calls != 2 {
		t.Fatalf("hook called %d times, want once per proposed class", calls)
	}
	got, ok := assoc.CommonExtendedNegotiation(storageCommitment.SOPClassUID)
	if !ok {
		t.Fatal("accepted class got no answer")
	}
	if got.ServiceClassUID != storageCommitment.ServiceClassUID {
		t.Fatalf("service class = %q, want %q", got.ServiceClassUID, storageCommitment.ServiceClassUID)
	}
	if len(got.RelatedGeneralSOPClasses) != 1 || got.RelatedGeneralSOPClasses[0] != storageCommitment.RelatedGeneralSOPClasses[0] {
		t.Fatalf("related general classes = %v, want %v", got.RelatedGeneralSOPClasses, storageCommitment.RelatedGeneralSOPClasses)
	}
	if got, ok := assoc.CommonExtendedNegotiation(declined); ok {
		t.Fatalf("declined class got %+v", got)
	}
}

// Silence is not a refusal: an SCP without the hook sends no sub-item, and the
// association is still fully usable the plain DIMSE way. A requestor that read
// the absence as "cannot use this class" would break every SCP written before
// this item existed.
func TestCommonExtendedNegotiationAbsentWithoutHook(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{storageCommitment.SOPClassUID, pdu.VerificationSOPClass},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: storageCommitment.SOPClassUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: pdu.VerificationSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		CommonExtendedNegotiations: []pdu.CommonExtendedNegotiation{storageCommitment},
	})

	if got, ok := assoc.CommonExtendedNegotiation(storageCommitment.SOPClassUID); ok {
		t.Fatalf("unexpected answer %+v", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := assoc.CEcho(ctx); err != nil {
		t.Fatalf("association unusable after an unanswered common extended negotiation: %v", err)
	}
}

// Nothing proposed means nothing answered: an AC that invents a Service Class
// pairing would be telling the requestor something it never asked about.
func TestCommonExtendedNegotiationAbsentWhenNotProposed(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{
		CommonExtendedNegotiation: func(pdu.CommonExtendedNegotiation) bool {
			t.Error("hook called with nothing proposed")
			return true
		},
	}, ae.Config{})

	if got, ok := assoc.CommonExtendedNegotiation(storageCommitment.SOPClassUID); ok {
		t.Fatalf("unexpected answer %+v", got)
	}
}

// The accessor hands out a copy: a caller that trims or reorders the list must
// not change what the association reports to the next reader.
func TestCommonExtendedNegotiationAccessorCopies(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{storageCommitment.SOPClassUID},
		CommonExtendedNegotiation: func(pdu.CommonExtendedNegotiation) bool {
			return true
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: storageCommitment.SOPClassUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		CommonExtendedNegotiations: []pdu.CommonExtendedNegotiation{storageCommitment},
	})

	got, ok := assoc.CommonExtendedNegotiation(storageCommitment.SOPClassUID)
	if !ok {
		t.Fatal("no answer")
	}
	got.RelatedGeneralSOPClasses[0] = "9.9.9"

	again, ok := assoc.CommonExtendedNegotiation(storageCommitment.SOPClassUID)
	if !ok {
		t.Fatal("no answer on the second read")
	}
	if again.RelatedGeneralSOPClasses[0] != storageCommitment.RelatedGeneralSOPClasses[0] {
		t.Fatalf("mutating the returned slice changed the association: got %q", again.RelatedGeneralSOPClasses[0])
	}
}

// The hook is application code running on the association's goroutine before
// any DIMSE message is read, so a panic in it has to cost exactly what a panic
// in a handler costs: one association, not the process. Anything else would let
// a bug in negotiation policy take the server down the first time a peer
// proposes the item.
func TestCommonExtendedNegotiationHookPanicCostsOneAssociation(t *testing.T) {
	t.Parallel()

	const aet = "CEXTPANIC"
	var calls atomic.Int32
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  aet,
		AcceptedAbstractSyntaxes: []string{pdu.VerificationSOPClass},
		CommonExtendedNegotiation: func(pdu.CommonExtendedNegotiation) bool {
			if calls.Add(1) == 1 {
				panic("negotiation policy bug")
			}
			return true
		},
	})

	propose := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assoc, err := ae.Dial(ctx, ae.Config{
			AETitle: "CEXTSCU",
			PresentationContexts: []ae.PresentationContext{{
				ID:               1,
				AbstractSyntax:   pdu.VerificationSOPClass,
				TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
			}},
			CommonExtendedNegotiations: []pdu.CommonExtendedNegotiation{storageCommitment},
		}, addr, aet)
		if err != nil {
			return err
		}
		defer func() { _ = assoc.Close() }()
		return assoc.CEcho(ctx)
	}

	if err := propose(); err == nil {
		t.Fatal("Dial succeeded despite a panicking negotiation hook")
	}
	if err := propose(); err != nil {
		t.Fatalf("association after the panic: %v", err)
	}
}
