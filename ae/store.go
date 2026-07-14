package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// StoreRequest is a C-STORE-RQ payload.
//
// SCU outbound: provide either Dataset (already encoded) or Data (godicom
// Dataset to encode with the negotiated transfer syntax). Data takes
// precedence when both are set.
//
// SCP inbound (OnCStore): Dataset is always the raw PDV bytes; Data is the
// decoded godicom Dataset (pynetdicom event.dataset) when TransferSyntax is
// known — use Data.SaveAs like pydicom save_as. TransferSyntax is the
// accepted presentation context transfer syntax.
type StoreRequest struct {
	AffectedSOPClassUID                  string
	AffectedSOPInstanceUID               string
	Dataset                              []byte
	Data                                 *godicom.Dataset
	TransferSyntax                       string
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
	pc, ok := a.contextByAbstract(req.AffectedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.AffectedSOPClassUID)
	}

	payload := req.Dataset
	if req.Data != nil {
		encoded, err := req.Data.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode dataset: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("ae: C-STORE missing dataset")
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
	if err := a.sendMessage(ctx, pc.ID, cmd, payload); err != nil {
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

// newInboundStoreRequest builds an OnCStore payload from a received C-STORE-RQ.
// Dataset is always the raw PDV bytes; Data is decoded when transferSyntax is set.
func newInboundStoreRequest(rq *dimse.CStoreRQ, dataset []byte, transferSyntax string) StoreRequest {
	req := StoreRequest{
		AffectedSOPClassUID:                  rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:               rq.AffectedSOPInstanceUID,
		Dataset:                              dataset,
		TransferSyntax:                       transferSyntax,
		Priority:                             rq.Priority,
		MoveOriginatorApplicationEntityTitle: rq.MoveOriginatorApplicationEntityTitle,
		MoveOriginatorMessageID:              rq.MoveOriginatorMessageID,
	}
	if len(dataset) == 0 || transferSyntax == "" {
		return req
	}
	ds, err := godicom.DecodeDataset(dataset, transferSyntax)
	if err != nil {
		return req
	}
	req.Data = ds
	return req
}
