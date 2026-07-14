package status

import "testing"

func TestIsPending(t *testing.T) {
	cases := []struct {
		code uint16
		want bool
	}{
		{Success, false},
		{Cancel, false},
		{Pending, true},
		{PendingWarning, true},
		{SOPClassNotSupported, false},
		{UnableToPerformSubOperations, false},
	}
	for _, tc := range cases {
		if got := IsPending(tc.code); got != tc.want {
			t.Fatalf("IsPending(0x%04X)=%v want %v", tc.code, got, tc.want)
		}
	}
}

func TestCatalogValues(t *testing.T) {
	if Success != 0x0000 {
		t.Fatalf("Success=0x%04X", Success)
	}
	if SOPClassNotSupported != 0x0122 {
		t.Fatalf("SOPClassNotSupported=0x%04X", SOPClassNotSupported)
	}
	if MoveDestinationUnknown != 0xA801 {
		t.Fatalf("MoveDestinationUnknown=0x%04X", MoveDestinationUnknown)
	}
	if OneOrMoreFailures != 0xB000 || CoercionOfDataElements != 0xB000 {
		t.Fatalf("warning aliases mismatch")
	}
}
