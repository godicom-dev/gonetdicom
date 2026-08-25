package ae

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// makeContexts builds n contexts with distinct abstract syntaxes and no IDs.
func makeContexts(n int) []PresentationContext {
	out := make([]PresentationContext, n)
	for i := range out {
		out[i] = PresentationContext{AbstractSyntax: fmt.Sprintf("1.2.3.%d", i)}
	}
	return out
}

// requireDistinctOddIDs is the invariant every proposed set has to hold: PS3.8
// gives each context an odd ID, and two contexts sharing one make the peer's
// reply ambiguous.
func requireDistinctOddIDs(t *testing.T, got []PresentationContext) {
	t.Helper()
	seen := map[byte]string{}
	for _, pc := range got {
		if pc.ID == 0 || pc.ID%2 == 0 {
			t.Errorf("context %s got ID %d, want a non-zero odd ID", pc.AbstractSyntax, pc.ID)
		}
		if other, dup := seen[pc.ID]; dup {
			t.Errorf("ID %d used by both %s and %s", pc.ID, other, pc.AbstractSyntax)
		}
		seen[pc.ID] = pc.AbstractSyntax
	}
}

// Deriving the ID from the slice position overflowed a byte at the 129th
// context — 2*128+1 truncates to 1 — so the RQ carried duplicate IDs and the
// association's own lookup kept only the last context of each colliding pair.
// There is no valid way to propose that many, so it has to be an error.
func TestBuildPresentationContextsRejectsTooMany(t *testing.T) {
	t.Parallel()

	if _, err := buildPresentationContexts(makeContexts(MaxPresentationContexts + 2)); !errors.Is(err, ErrPresentationContexts) {
		t.Fatalf("130 contexts: got %v, want ErrPresentationContexts", err)
	}
	// The last set that does fit must still be assigned cleanly.
	got, err := buildPresentationContexts(makeContexts(MaxPresentationContexts))
	if err != nil {
		t.Fatalf("%d contexts: %v", MaxPresentationContexts, err)
	}
	requireDistinctOddIDs(t, got)
}

func TestBuildPresentationContextsRejectsBadIDs(t *testing.T) {
	t.Parallel()

	tests := map[string][]PresentationContext{
		"even ID": {
			{ID: 2, AbstractSyntax: "1.2.3.1"},
		},
		"duplicate explicit IDs": {
			{ID: 5, AbstractSyntax: "1.2.3.1"},
			{ID: 5, AbstractSyntax: "1.2.3.2"},
		},
		"none": {},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := buildPresentationContexts(in); !errors.Is(err, ErrPresentationContexts) {
				t.Fatalf("got %v, want ErrPresentationContexts", err)
			}
		})
	}
}

// Auto-assignment used to ignore the IDs the caller had already chosen, so a
// mixed set proposed the same ID twice.
func TestBuildPresentationContextsAvoidsExplicitIDs(t *testing.T) {
	t.Parallel()

	in := []PresentationContext{
		{AbstractSyntax: "auto-1"},
		{ID: 3, AbstractSyntax: "explicit-3"},
		{AbstractSyntax: "auto-2"},
		{ID: 1, AbstractSyntax: "explicit-1"},
		{AbstractSyntax: "auto-3"},
	}
	got, err := buildPresentationContexts(in)
	if err != nil {
		t.Fatal(err)
	}
	requireDistinctOddIDs(t, got)
	for i, pc := range in {
		if pc.ID != 0 && got[i].ID != pc.ID {
			t.Errorf("%s: ID changed to %d, callers rely on the one they set", pc.AbstractSyntax, got[i].ID)
		}
	}
}

func TestBuildPresentationContextsDefaultsTransferSyntax(t *testing.T) {
	t.Parallel()

	got, err := buildPresentationContexts([]PresentationContext{{AbstractSyntax: "1.2.3.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].TransferSyntaxes) != 1 || got[0].TransferSyntaxes[0] != pdu.ImplicitVRLittleEndian {
		t.Fatalf("transfer syntaxes = %v", got[0].TransferSyntaxes)
	}
}

// The caller has to hear about it: an unproposable set used to be sent anyway,
// and the association came back missing contexts it thought it had.
func TestDialRejectsTooManyPresentationContexts(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = Dial(ctx, Config{
		AETitle:              "CTXSCU",
		PresentationContexts: makeContexts(MaxPresentationContexts + 2),
	}, ln.Addr().String(), "CTXSCP")
	if !errors.Is(err, ErrPresentationContexts) {
		t.Fatalf("got %v, want ErrPresentationContexts", err)
	}
}
