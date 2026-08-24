package ae_test

import (
	"testing"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
)

func TestBuildRole(t *testing.T) {
	t.Parallel()
	ct := string(uid.CTImageStorage)
	r := ae.BuildRole(ct, false, true)
	if r.SOPClassUID != ct || r.SCURole || !r.SCPRole {
		t.Fatalf("%+v", r)
	}
}
