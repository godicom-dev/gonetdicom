package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// Common Query/Retrieve Information Model Find SOP Classes.
const (
	PatientRootQueryRetrieveInformationModelFind = string(uid.PatientRootQueryRetrieveInformationModelFind)
	StudyRootQueryRetrieveInformationModelFind   = string(uid.StudyRootQueryRetrieveInformationModelFind)
)

// FindRequest is a C-FIND-RQ payload.
//
// Provide Identifier (encoded) or IdentifierData (godicom Dataset). IdentifierData
// takes precedence when both are set.
type FindRequest struct {
	QueryModel     string // Affected SOP Class UID (information model)
	Identifier     []byte
	IdentifierData *godicom.Dataset
	Priority       uint16 // 0 defaults to PriorityLow
	// MessageID, when non-zero, is used as the C-FIND-RQ Message ID (for C-CANCEL).
	MessageID uint16
}

// FindMatch is one C-FIND-RSP (Pending or final).
type FindMatch struct {
	Status     uint16
	Identifier *godicom.Dataset // non-nil for Pending responses with a dataset
}

// CFind sends a C-FIND-RQ and collects responses until Success/Failure/Cancel.
func (a *Association) CFind(ctx context.Context, req FindRequest) ([]FindMatch, error) {
	if req.QueryModel == "" {
		return nil, fmt.Errorf("ae: C-FIND missing Query Model")
	}
	pc, ok := a.contextByAbstract(req.QueryModel)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.QueryModel)
	}

	payload := req.Identifier
	if req.IdentifierData != nil {
		encoded, err := req.IdentifierData.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode identifier: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("ae: C-FIND missing identifier")
	}

	priority := req.Priority
	if priority == 0 {
		priority = dimse.PriorityLow
	}
	msgID := req.MessageID
	if msgID == 0 {
		msgID = a.nextMessageID()
	}
	cmd, err := (&dimse.CFindRQ{
		MessageID:           msgID,
		Priority:            priority,
		AffectedSOPClassUID: req.QueryModel,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, payload); err != nil {
		return nil, err
	}

	var matches []FindMatch
	for {
		rspCmd, rspDS, err := a.recvMessage(ctx)
		if err != nil {
			return matches, err
		}
		rsp, err := dimse.DecodeCFindRSP(rspCmd)
		if err != nil {
			return matches, fmt.Errorf("ae: decode C-FIND-RSP: %w", err)
		}
		if rsp.MessageIDBeingRespondedTo != msgID {
			return matches, fmt.Errorf("ae: C-FIND message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
		}
		m := FindMatch{Status: rsp.Status}
		if len(rspDS) > 0 {
			ident, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
			if err != nil {
				return matches, fmt.Errorf("ae: decode C-FIND identifier: %w", err)
			}
			m.Identifier = ident
		}
		matches = append(matches, m)
		if !dimse.IsPending(rsp.Status) {
			return matches, nil
		}
	}
}
