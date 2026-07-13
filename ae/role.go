package ae

import "github.com/godicom-dev/gonetdicom/pdu"

// BuildRole returns an SCP/SCU Role Selection item for the given SOP Class.
// Mirrors pynetdicom.presentation.build_role.
func BuildRole(sopClassUID string, scuRole, scpRole bool) pdu.RoleSelection {
	return pdu.RoleSelection{
		SOPClassUID: sopClassUID,
		SCURole:     scuRole,
		SCPRole:     scpRole,
	}
}

// roleOutcome is (requestorAsSCU, requestorAsSCP, acceptorAsSCU, acceptorAsSCP).
// Port of pynetdicom.presentation.SCP_SCU_ROLES.
type roleOutcome struct {
	ReqSCU, ReqSCP, AcSCU, AcSCP bool
}

var (
	roleDefault  = roleOutcome{true, false, false, true}
	roleBoth     = roleOutcome{true, true, true, true}
	roleRejected = roleOutcome{false, false, false, false}
	roleInverted = roleOutcome{false, true, true, false}
)

type rolePair struct{ scu, scp bool }

// scpSCURoles is the pynetdicom.presentation.SCP_SCU_ROLES LUT (non-default cells only).
var scpSCURoles = map[rolePair]map[rolePair]roleOutcome{
	{true, true}: {
		{true, true}:   roleBoth,
		{false, true}:  roleInverted,
		{false, false}: roleRejected,
	},
	{true, false}: {
		{false, false}: roleRejected,
		{false, true}:  roleRejected,
	},
	{false, true}: {
		{true, true}:   roleInverted,
		{false, true}:  roleInverted,
		{false, false}: roleRejected,
		{true, false}:  roleRejected,
	},
	{false, false}: {
		{true, true}:   roleRejected,
		{true, false}:  roleRejected,
		{false, false}: roleRejected,
		{false, true}:  roleRejected,
	},
}

// negotiateRoles applies the pynetdicom SCP_SCU_ROLES LUT.
func negotiateRoles(rqSCU, rqSCP bool, rqPresent bool, acSCU, acSCP bool, acPresent bool) roleOutcome {
	if !rqPresent || !acPresent {
		return roleDefault
	}
	byAc, ok := scpSCURoles[rolePair{rqSCU, rqSCP}]
	if !ok {
		return roleDefault
	}
	if out, ok := byAc[rolePair{acSCU, acSCP}]; ok {
		return out
	}
	return roleDefault
}

func roleMap(roles []pdu.RoleSelection) map[string]pdu.RoleSelection {
	out := make(map[string]pdu.RoleSelection, len(roles))
	for _, r := range roles {
		if r.SOPClassUID == "" {
			continue
		}
		out[r.SOPClassUID] = r
	}
	return out
}

// replyRole builds the AC role selection item (cannot return 1 if RQ proposed 0).
func replyRole(uid string, rq, ac pdu.RoleSelection) pdu.RoleSelection {
	out := pdu.RoleSelection{SOPClassUID: uid}
	if rq.SCURole {
		out.SCURole = ac.SCURole
	}
	if rq.SCPRole {
		out.SCPRole = ac.SCPRole
	}
	return out
}
