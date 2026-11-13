package ae

import (
	"context"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// UserIdentityHandler verifies a User Identity Negotiation request on the SCP.
// Return ok=false to reject the association (A-ASSOCIATE-RJ result=2, source=2, reason=1).
// For types Kerberos/SAML/JWT, when the request asks for a positive response, return
// a non-nil serverResponse to include a User Identity AC item (pynetdicom-aligned).
// Username / Username+Passcode never include an AC response item.
//
// ctx carries the association identity (AssocFromContext) — the calling AE
// title, the requestor's remote address and its implementation Class UID —
// attached before negotiation, so a handler can refuse an association by
// source address or peer software as well as by the identity item itself.
// pynetdicom's EVT_USER_IDENTITY event exposes evt.assoc the same way, and
// dcm4che hands its user identity negotiator the Association.
type UserIdentityHandler func(ctx context.Context, req pdu.UserIdentityRQ) (ok bool, serverResponse []byte)

// UsernameIdentity builds a username-only User Identity RQ item (type 1).
func UsernameIdentity(username string, requestPositiveResponse bool) *pdu.UserIdentityRQ {
	return &pdu.UserIdentityRQ{
		Type:                      pdu.UserIdentityUsername,
		PositiveResponseRequested: requestPositiveResponse,
		PrimaryField:              []byte(username),
	}
}

// UsernamePasscodeIdentity builds a username/passcode User Identity RQ item (type 2).
func UsernamePasscodeIdentity(username, passcode string, requestPositiveResponse bool) *pdu.UserIdentityRQ {
	return &pdu.UserIdentityRQ{
		Type:                      pdu.UserIdentityUsernamePasscode,
		PositiveResponseRequested: requestPositiveResponse,
		PrimaryField:              []byte(username),
		SecondaryField:            []byte(passcode),
	}
}
