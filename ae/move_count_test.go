package ae

import (
	"math"
	"testing"
)

// The four C-MOVE Sub-Operations counts are US on the wire, so something has to
// give when a plan holds more than 65535 stores. Wrapping was the wrong thing to
// give: it is not a less precise count, it is a count that moves backwards, and
// an SCU reading Remaining to decide whether the transfer is still running acts
// on the direction rather than the magnitude.
func TestSubOpCountSaturates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   int
		want uint16
	}{
		{0, 0},
		{1, 1},
		{math.MaxUint16 - 1, math.MaxUint16 - 1},
		{math.MaxUint16, math.MaxUint16},
		// The wrapping the US field invites: uint16(65536) is 0, and every count
		// above it lands somewhere arbitrary below it.
		{math.MaxUint16 + 1, math.MaxUint16},
		{math.MaxUint16 + 2, math.MaxUint16},
		{1 << 20, math.MaxUint16},
		// No caller subtracts past zero — done never exceeds total — but a helper
		// that answers for its whole input domain cannot be broken by a caller that
		// later does.
		{-1, 0},
	} {
		if got := subOpCount(tc.in); got != tc.want {
			t.Errorf("subOpCount(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Saturating keeps the counts monotonic, which is the property the responses are
// read for: Remaining only falls, Completed and Failed only rise. Counting the
// pending responses of an oversized plan in uint16 broke that in the first two
// responses it sent — 0 outstanding, then 65535.
func TestSubOpCountKeepsRemainingMonotonic(t *testing.T) {
	t.Parallel()

	const total = math.MaxUint16 + 10
	prev := subOpCount(total)
	if prev != math.MaxUint16 {
		t.Fatalf("first Remaining = %d, want %d", prev, uint16(math.MaxUint16))
	}
	for done := 1; done <= 20; done++ {
		got := subOpCount(total - done)
		if got > prev {
			t.Fatalf("Remaining rose from %d to %d after %d completions", prev, got, done)
		}
		prev = got
	}
}
