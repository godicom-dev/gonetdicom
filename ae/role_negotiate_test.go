package ae

import "testing"

func TestNegotiateRolesCGetLike(t *testing.T) {
	t.Parallel()
	// Requestor wants SCP only; acceptor supports both → inverted.
	out := negotiateRoles(false, true, true, true, true, true)
	if out.ReqSCU || !out.ReqSCP || !out.AcSCU || out.AcSCP {
		t.Fatalf("outcome=%+v", out)
	}
}

func TestNegotiateRolesDefault(t *testing.T) {
	t.Parallel()
	out := negotiateRoles(false, false, false, false, false, false)
	if !out.ReqSCU || out.ReqSCP || out.AcSCU || !out.AcSCP {
		t.Fatalf("default=%+v", out)
	}
}

func TestReplyRoleCannotUpgrade(t *testing.T) {
	t.Parallel()
	rq := BuildRole("1.2.3", false, true)
	ac := BuildRole("1.2.3", true, true)
	got := replyRole("1.2.3", rq, ac)
	if got.SCURole || !got.SCPRole {
		t.Fatalf("reply=%+v", got)
	}
}
