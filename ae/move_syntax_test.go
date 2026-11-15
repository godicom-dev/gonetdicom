package ae

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestMovePresentationContexts(t *testing.T) {
	jpeg := "1.2.840.10008.1.2.4.50"
	// StoreContexts does the deduplication a plan's stores need, so a handler
	// that still enumerates a batch gets the same contexts it always did.
	pcs, err := movePresentationContexts(StoreContexts([]StoreRequest{
		{AffectedSOPClassUID: "1.2.3", TransferSyntax: jpeg},
		{AffectedSOPClassUID: "1.2.3", TransferSyntax: jpeg},
		{AffectedSOPClassUID: "1.2.3", TransferSyntax: explicitVRLittleEndian},
		{AffectedSOPClassUID: "1.2.4"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcs) != 3 {
		t.Fatalf("contexts: %+v", pcs)
	}
	if len(pcs[0].TransferSyntaxes) != 1 || pcs[0].TransferSyntaxes[0] != jpeg {
		t.Fatalf("stored syntax: %+v", pcs[0].TransferSyntaxes)
	}
	if pcs[1].TransferSyntaxes[0] != explicitVRLittleEndian {
		t.Fatalf("second syntax: %+v", pcs[1].TransferSyntaxes)
	}
	if pcs[2].TransferSyntaxes[0] != pdu.ImplicitVRLittleEndian {
		t.Fatalf("default syntax: %+v", pcs[2].TransferSyntaxes)
	}
	if _, err := movePresentationContexts(nil); err == nil {
		t.Fatal("expected missing SOP class")
	}
	// A handler that declares duplicates in its plan is not punished for it:
	// the association gets one context per pair either way.
	dup, err := movePresentationContexts([]StoreContext{
		{SOPClassUID: "1.2.3", TransferSyntax: jpeg},
		{SOPClassUID: "1.2.3", TransferSyntax: jpeg},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dup) != 1 {
		t.Fatalf("declared duplicates: %+v", dup)
	}
}

func TestContextForStore(t *testing.T) {
	jpeg := "1.2.840.10008.1.2.4.50"
	a := &Association{contexts: []AcceptedContext{
		{ID: 1, AbstractSyntax: "1.2.3", TransferSyntax: pdu.ImplicitVRLittleEndian},
		{ID: 3, AbstractSyntax: "1.2.3", TransferSyntax: explicitVRLittleEndian},
		{ID: 5, AbstractSyntax: "1.2.4", TransferSyntax: jpeg},
	}}

	got, ok := a.contextForStore("1.2.3", explicitVRLittleEndian)
	if !ok || got.ID != 3 {
		t.Fatalf("exact: %+v %v", got, ok)
	}
	got, ok = a.contextForStore("1.2.3", deflatedExplicitVRLittleEndian)
	if !ok || got.ID != 1 {
		t.Fatalf("convertible: %+v %v", got, ok)
	}
	if _, ok := a.contextForStore("1.2.4", pdu.ImplicitVRLittleEndian); ok {
		t.Fatal("compressed syntax converted")
	}
	got, ok = a.contextForStore("1.2.3", "")
	if !ok || got.ID != 1 {
		t.Fatalf("empty syntax: %+v %v", got, ok)
	}
}
