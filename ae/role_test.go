package ae_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/ae"
)

func TestBuildRole(t *testing.T) {
	t.Parallel()
	r := ae.BuildRole("1.2.840.10008.5.1.4.1.1.2", false, true)
	if r.SOPClassUID != "1.2.840.10008.5.1.4.1.1.2" || r.SCURole || !r.SCPRole {
		t.Fatalf("%+v", r)
	}
}
