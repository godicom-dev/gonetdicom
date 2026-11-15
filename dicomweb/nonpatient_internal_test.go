package dicomweb

import "testing"

// The UID check is the one thing standing between a request path and a store that
// joins it onto a directory, so its edges are worth pinning directly: each case
// below is either a legal DICOM UID or something that would resolve as a path
// rather than name an instance.
func TestValidInstanceUID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"1.2.840.10008.5.1.4.39.1", true},
		{"1", true},
		{"1.2.3", true},
		{"01.002.3", true}, // leading zeros are legal in a UID component
		{"", false},
		{"..", false},
		{".", false},
		{".1.2", false}, // would be a relative path
		{"1.2.", false}, // names an empty component
		{"1..2", false}, // the same, in the middle
		{"1.2 3", false},
		{"1/2", false},
		{"1.2.3/../4", false},
		{"ABC", false},
		{"1.2.3.4.5.6.7.8.9.10.11.12.13.14.15.16.17.18.19.20.21.22.23.24.25", false}, // 65 characters
	}
	for _, tc := range cases {
		if got := validInstanceUID(tc.in); got != tc.want {
			t.Errorf("validInstanceUID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
