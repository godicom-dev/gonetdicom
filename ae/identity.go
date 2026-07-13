package ae

import "github.com/godicom-dev/gonetdicom/pdu"

// UserIdentityHandler verifies a User Identity Negotiation request on the SCP.
// Return ok=false to reject the association (A-ASSOCIATE-RJ result=2, source=2, reason=1).
// For types Kerberos/SAML/JWT, when the request asks for a positive response, return
// a non-nil serverResponse to include a User Identity AC item (pynetdicom-aligned).
// Username / Username+Passcode never include an AC response item.
type UserIdentityHandler func(req pdu.UserIdentityRQ) (ok bool, serverResponse []byte)

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
