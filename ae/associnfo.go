package ae

import (
	"context"
	"net"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// AssocInfo describes the association that carried a DIMSE request, and the
// presentation context within it. The SCP attaches it to the handler
// context: association-level fields (peer identity, remote address,
// implementation identification) when the A-ASSOCIATE-RQ is accepted, the
// message-level fields (abstract syntax, presentation context ID, negotiated
// transfer syntax) before each handler dispatch. The SCU-side C-GET /
// C-MOVE sub-operation handlers receive it the same way, with the local AE
// title as CallingAETitle.
//
// The shape follows the two reference implementations: pynetdicom hands its
// handlers Event.assoc (the Association) and Event.context (a
// PresentationContextTuple of context ID, abstract syntax and transfer
// syntax); dcm4che's DimseRQHandler.onDimseRQ receives (Association,
// PresentationContext, ...) and the Association exposes getCallingAET,
// getSocket and getAbstractSyntax(pcid). A Go handler has no mutable
// association object to hold, so this value snapshot carries the same
// identity fields.
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
	// RemoteImplementationClassUID is the Implementation Class UID the
	// requestor announced in its A-ASSOCIATE-RQ user information, or "" if
	// it sent none. It identifies the peer's software, the way dcm4che's
	// Association.getRemoteImplClassUID does.
	RemoteImplementationClassUID string
	// RemoteImplementationVersionName is the Implementation Version Name the
	// requestor announced, or "" if it sent none.
	RemoteImplementationVersionName string
	// AbstractSyntax is the SOP Class of the presentation context the
	// message arrived on. It mirrors the AffectedSOPClassUID of the request
	// payloads that carry one, and is the only place to read it for the
	// services whose payload does not.
	AbstractSyntax string
	// PresentationContextID is the ID of the presentation context the
	// message arrived on.
	PresentationContextID byte
	// TransferSyntax is the transfer syntax negotiated for that
	// presentation context. It mirrors the TransferSyntax field of the
	// request payloads that carry one.
	TransferSyntax string
	// AsyncOperationsWindow is the Asynchronous Operations Window in force for
	// this association, or nil when the requestor proposed none. On an SCP it
	// is what this server agreed to execute; on an SCU it is what the peer
	// granted.
	//
	// gonetdicom processes one DIMSE message at a time (PS3.8's default RW=1
	// behaviour), so an acceptor that does not configure a window answers
	// 1/1: a value larger than 1 would promise concurrency that the dispatch
	// loop does not provide.
	AsyncOperationsWindow *pdu.AsyncOperationsWindow
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
