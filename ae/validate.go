package ae

import (
	"context"
	"fmt"
	"net"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/dimse"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// RetrieveOperation names the service whose identifier is being validated.
type RetrieveOperation string

// The three operations that carry a Query/Retrieve identifier.
const (
	OperationFind RetrieveOperation = "C-FIND"
	OperationMove RetrieveOperation = "C-MOVE"
	OperationGet  RetrieveOperation = "C-GET"
)

// IdentifierRequest is the decoded Query/Retrieve identifier of an inbound
// C-FIND / C-MOVE / C-GET, offered to a ServerConfig.IdentifierValidator before
// the handler runs.
type IdentifierRequest struct {
	Operation  RetrieveOperation
	QueryModel string // the SOP Class the identifier is written for
	// Identifier is the decoded dataset, or nil when the peer sent none. It is
	// the same value the handler will receive, decoded once.
	Identifier     *godicom.Dataset
	TransferSyntax string
}

// IdentifierValidator checks an identifier before dispatch and returns a DIMSE
// status: 0 to accept, anything else to answer the request with that status and
// skip the handler.
//
// A validator is the seam between "the framework checks the envelope" and "the
// handler owns its model". PS3.4 requires the SCP to refuse an identifier that
// does not match the SOP Class (0xA900), but what matches is model-specific, so
// the rule belongs to the application while the answer, the response shape and
// the request/response correlation belong here.
type IdentifierValidator func(ctx context.Context, req IdentifierRequest) uint16

// QueryRetrieveLevelValidator returns a validator that requires the identifier
// to carry Query Retrieve Level (0008,0052) and, when levels is non-empty, to
// name one of them.
//
// It is the check nearly every SCP wants and almost nobody writes: a C-FIND
// whose identifier omits the level cannot be answered correctly, and 0xA900 is
// how the peer finds out.
func QueryRetrieveLevelValidator(levels ...string) IdentifierValidator {
	allowed := make(map[string]struct{}, len(levels))
	for _, l := range levels {
		allowed[l] = struct{}{}
	}
	return func(_ context.Context, req IdentifierRequest) uint16 {
		if req.Identifier == nil {
			return dcmstatus.IdentifierDoesNotMatchSOPClass
		}
		level, ok := req.Identifier.GetString(tag.QueryRetrieveLevel)
		if !ok || level == "" {
			return dcmstatus.IdentifierDoesNotMatchSOPClass
		}
		if len(allowed) == 0 {
			return 0
		}
		if _, ok := allowed[level]; !ok {
			return dcmstatus.IdentifierDoesNotMatchSOPClass
		}
		return 0
	}
}

// validateIdentifier runs the configured validator, if any, and reports the
// status the request should be answered with (0 = proceed).
func (c ServerConfig) validateIdentifier(ctx context.Context, op RetrieveOperation, model string, ident *godicom.Dataset, ts string) uint16 {
	if c.IdentifierValidator == nil {
		return 0
	}
	return c.IdentifierValidator(ctx, IdentifierRequest{
		Operation:      op,
		QueryModel:     model,
		Identifier:     ident,
		TransferSyntax: ts,
	})
}

// The three refusals differ only in their response type, which DIMSE makes
// distinct; each carries the status the validator chose and no counts, because
// no sub-operation was attempted.
func refuseFindIdentifier(ctx context.Context, conn net.Conn, pcid byte, peerMax uint32, msgID uint16, model string, status uint16) error {
	rsp, err := (&dimse.CFindRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       model,
		Status:                    status,
	}).Encode()
	if err != nil {
		return fmt.Errorf("ae: encode C-FIND-RSP: %w", err)
	}
	return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
}

func refuseMoveIdentifier(ctx context.Context, conn net.Conn, pcid byte, peerMax uint32, msgID uint16, model string, status uint16) error {
	rsp, err := (&dimse.CMoveRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       model,
		Status:                    status,
	}).Encode()
	if err != nil {
		return fmt.Errorf("ae: encode C-MOVE-RSP: %w", err)
	}
	return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
}

func refuseGetIdentifier(ctx context.Context, conn net.Conn, pcid byte, peerMax uint32, msgID uint16, model string, status uint16) error {
	rsp, err := (&dimse.CGetRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       model,
		Status:                    status,
	}).Encode()
	if err != nil {
		return fmt.Errorf("ae: encode C-GET-RSP: %w", err)
	}
	return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
}
