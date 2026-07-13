package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/gonetdicom/dimse"
)

// StoreRequest is a C-STORE-RQ payload.
//
// Dataset must already be encoded in the transfer syntax negotiated for
// AffectedSOPClassUID.
type StoreRequest struct {
	AffectedSOPClassUID                  string
	AffectedSOPInstanceUID               string
	Dataset                              []byte
	Priority                             uint16 // 0 defaults to PriorityLow
	MoveOriginatorApplicationEntityTitle string
	MoveOriginatorMessageID              uint16
}

// StoreResult is the peer's C-STORE-RSP summary.
type StoreResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
}

// CStore sends a C-STORE-RQ and waits for the C-STORE-RSP.
func (a *Association) CStore(ctx context.Context, req StoreRequest) (*StoreResult, error) {
	if req.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: C-STORE missing Affected SOP Class UID")
	}
	if req.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: C-STORE missing Affected SOP Instance UID")
	}
	if len(req.Dataset) == 0 {
		return nil, fmt.Errorf("ae: C-STORE missing dataset")
	}
	pc, ok := a.contextByAbstract(req.AffectedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.AffectedSOPClassUID)
	}

	priority := req.Priority
	if priority == 0 {
		priority = dimse.PriorityLow
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.CStoreRQ{
		MessageID:                            msgID,
		Priority:                             priority,
		AffectedSOPClassUID:                  req.AffectedSOPClassUID,
		AffectedSOPInstanceUID:               req.AffectedSOPInstanceUID,
		MoveOriginatorApplicationEntityTitle: req.MoveOriginatorApplicationEntityTitle,
		MoveOriginatorMessageID:              req.MoveOriginatorMessageID,
		HasMoveOriginator:                    req.MoveOriginatorApplicationEntityTitle != "",
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, req.Dataset); err != nil {
		return nil, err
	}
	rspCmd, _, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeCStoreRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode C-STORE-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: C-STORE message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	return &StoreResult{
		Status:                 rsp.Status,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
	}, nil
}
