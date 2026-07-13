package dimse_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/dimse"
)

func TestCCancelRQRoundtrip(t *testing.T) {
	t.Parallel()

	raw, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 7}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := dimse.DecodeCCancelRQ(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageIDBeingRespondedTo != 7 {
		t.Fatalf("msg id=%d", got.MessageIDBeingRespondedTo)
	}

	if _, err := dimse.DecodeCCancelRQ(raw[:8]); err == nil {
		t.Fatal("expected truncated error")
	}
}
