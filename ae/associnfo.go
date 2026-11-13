package ae

import (
	"context"
	"net"
)

// AssocInfo describes the association that carried a DIMSE request, and the
// presentation context within it. The SCP attaches it to the handler
// context: association-level fields (peer identity, remote address) when the
// A-ASSOCIATE-RQ is accepted, the message-level fields (presentation context
// ID, negotiated transfer syntax) before each handler dispatch. The SCU-side
// C-GET / C-MOVE sub-operation handlers receive it the same way, with the
// local AE title as CallingAETitle.
//
// A DIMSE handler that authenticates or audits its peer reads it with
// AssocFromContext. An absent value is safe to treat as untrusted: a handler
// verifying (source address, Calling AE Title) against a device registry
// should reject when the lookup fails.
type AssocInfo struct {
	// CallingAETitle is the requestor's AE title (the peer's, on an SCP).
	CallingAETitle string
	// CalledAETitle is the AE title the requestor asked for (this SCP's, or
	// the peer's when the SCU receives C-STORE sub-operations).
	CalledAETitle string
	// RemoteAddr is the requestor's network address.
	RemoteAddr net.Addr
	// PresentationContextID is the ID of the presentation context the
	// message arrived on.
	PresentationContextID byte
	// TransferSyntax is the transfer syntax negotiated for that
	// presentation context. It mirrors the TransferSyntax field of the
	// request payloads that carry one.
	TransferSyntax string
}

type assocInfoKey struct{}

// WithAssoc returns a context that carries info for AssocFromContext. SCP
// internals attach it; handlers normally read rather than set.
func WithAssoc(ctx context.Context, info AssocInfo) context.Context {
	return context.WithValue(ctx, assocInfoKey{}, info)
}

// AssocFromContext returns the AssocInfo the framework attached to ctx. ok
// is false for a context that never went through a dispatch (a plain
// context.Background, a handler invoked directly by a test); the zero value
// carries no peer identity.
func AssocFromContext(ctx context.Context) (AssocInfo, bool) {
	info, ok := ctx.Value(assocInfoKey{}).(AssocInfo)
	return info, ok
}
