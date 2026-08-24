package ae

import (
	"errors"
	"fmt"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// MaxPresentationContexts is how many presentation contexts one A-ASSOCIATE-RQ
// can carry. Presentation-context-IDs are odd values in 1..255 (PS3.8 9.3.2.2),
// which leaves exactly 128 of them.
const MaxPresentationContexts = 128

// ErrPresentationContexts is returned when Config.PresentationContexts cannot be
// proposed as written.
var ErrPresentationContexts = errors.New("ae: invalid presentation contexts")

// buildPresentationContexts assigns Presentation-context-IDs and rejects a set
// that cannot be proposed.
//
// IDs the caller chose are kept, because a peer's logs and any code correlating
// PDVs by ID depend on them; the rest are filled from the odd values still free.
// Deriving an ID from the slice position instead collides with every explicit ID
// and wraps past the 128th context — 2*128+1 truncates to 1 — which used to
// propose two contexts sharing one ID, with the second silently replacing the
// first in the association's own lookup table.
func buildPresentationContexts(in []PresentationContext) ([]PresentationContext, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: none proposed", ErrPresentationContexts)
	}
	if len(in) > MaxPresentationContexts {
		return nil, fmt.Errorf("%w: %d proposed, an A-ASSOCIATE-RQ holds at most %d",
			ErrPresentationContexts, len(in), MaxPresentationContexts)
	}

	taken := make(map[byte]int, len(in))
	for i, pc := range in {
		if pc.ID == 0 {
			continue
		}
		if pc.ID%2 == 0 {
			return nil, fmt.Errorf("%w: context %d (%s) has even ID %d, IDs must be odd",
				ErrPresentationContexts, i, pc.AbstractSyntax, pc.ID)
		}
		if first, dup := taken[pc.ID]; dup {
			return nil, fmt.Errorf("%w: contexts %d and %d both use ID %d",
				ErrPresentationContexts, first, i, pc.ID)
		}
		taken[pc.ID] = i
	}

	out := make([]PresentationContext, len(in))
	next := byte(1)
	for i, pc := range in {
		if pc.ID == 0 {
			// A free odd ID always exists here: there are MaxPresentationContexts
			// of them and fewer than that many contexts. The bound keeps a wrong
			// premise from becoming an endless loop rather than an error.
			for n := 0; n < MaxPresentationContexts; n++ {
				if _, dup := taken[next]; !dup {
					break
				}
				next += 2
			}
			if _, dup := taken[next]; dup {
				return nil, fmt.Errorf("%w: no free Presentation-context-ID for context %d",
					ErrPresentationContexts, i)
			}
			pc.ID = next
			taken[next] = i
			next += 2
		}
		if len(pc.TransferSyntaxes) == 0 {
			pc.TransferSyntaxes = []string{pdu.ImplicitVRLittleEndian}
		}
		out[i] = pc
	}
	return out, nil
}
