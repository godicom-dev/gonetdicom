package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// Query/Retrieve Information Model Move / Get SOP Classes.
const (
	PatientRootQueryRetrieveInformationModelMove = string(uid.PatientRootQueryRetrieveInformationModelMove)
	PatientRootQueryRetrieveInformationModelGet  = string(uid.PatientRootQueryRetrieveInformationModelGet)
	StudyRootQueryRetrieveInformationModelMove   = string(uid.StudyRootQueryRetrieveInformationModelMove)
	StudyRootQueryRetrieveInformationModelGet    = string(uid.StudyRootQueryRetrieveInformationModelGet)
)

// MoveRequest is a C-MOVE-RQ payload.
type MoveRequest struct {
	QueryModel      string
	MoveDestination string
	Identifier      []byte
	IdentifierData  *godicom.Dataset
	Priority        uint16
}

// RetrieveMatch is one C-MOVE/C-GET response.
type RetrieveMatch struct {
	Status        uint16
	SubOperations dimse.SubOperations
	Identifier    *godicom.Dataset // Failed SOP Instance UID List when present
}

// CMove sends a C-MOVE-RQ and collects responses until a final status.
func (a *Association) CMove(ctx context.Context, req MoveRequest) ([]RetrieveMatch, error) {
	if req.QueryModel == "" {
		return nil, fmt.Errorf("ae: C-MOVE missing Query Model")
	}
	if req.MoveDestination == "" {
		return nil, fmt.Errorf("ae: C-MOVE missing Move Destination")
	}
	pc, ok := a.contextByAbstract(req.QueryModel)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.QueryModel)
	}
	payload, err := encodeIdentifier(pc.TransferSyntax, req.Identifier, req.IdentifierData)
	if err != nil {
		return nil, err
	}
	priority := req.Priority
	if priority == 0 {
		priority = dimse.PriorityLow
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.CMoveRQ{
		MessageID:           msgID,
		Priority:            priority,
		AffectedSOPClassUID: req.QueryModel,
		MoveDestination:     req.MoveDestination,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, payload); err != nil {
		return nil, err
	}
	return a.collectRetrieve(ctx, pc.TransferSyntax, msgID, true)
}

// GetRequest is a C-GET-RQ payload.
//
// OnCStore handles C-STORE sub-operations received on this association during
// the C-GET (SCU temporarily acts as Storage SCP).
type GetRequest struct {
	QueryModel     string
	Identifier     []byte
	IdentifierData *godicom.Dataset
	Priority       uint16
	OnCStore       StoreHandler
}

// CGet sends a C-GET-RQ, handles interleaved C-STORE requests, and collects
// C-GET responses until a final status.
func (a *Association) CGet(ctx context.Context, req GetRequest) ([]RetrieveMatch, error) {
	if req.QueryModel == "" {
		return nil, fmt.Errorf("ae: C-GET missing Query Model")
	}
	pc, ok := a.contextByAbstract(req.QueryModel)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.QueryModel)
	}
	payload, err := encodeIdentifier(pc.TransferSyntax, req.Identifier, req.IdentifierData)
	if err != nil {
		return nil, err
	}
	priority := req.Priority
	if priority == 0 {
		priority = dimse.PriorityLow
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.CGetRQ{
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

	var matches []RetrieveMatch
	for {
		pcid, rspCmd, rspDS, err := a.recvMessagePC(ctx)
		if err != nil {
			return matches, err
		}

		if storeRQ, err := dimse.DecodeCStoreRQ(rspCmd); err == nil {
			if err := a.handleInboundStore(ctx, pcid, storeRQ, rspDS, req.OnCStore); err != nil {
				return matches, err
			}
			continue
		}

		rsp, err := dimse.DecodeCGetRSP(rspCmd)
		if err != nil {
			return matches, fmt.Errorf("ae: decode C-GET-RSP: %w", err)
		}
		if rsp.MessageIDBeingRespondedTo != msgID {
			return matches, fmt.Errorf("ae: C-GET message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
		}
		m := RetrieveMatch{Status: rsp.Status, SubOperations: rsp.SubOperations}
		if len(rspDS) > 0 {
			ident, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
			if err != nil {
				return matches, fmt.Errorf("ae: decode C-GET identifier: %w", err)
			}
			m.Identifier = ident
		}
		matches = append(matches, m)
		if !dimse.IsPending(rsp.Status) {
			return matches, nil
		}
	}
}

func (a *Association) collectRetrieve(ctx context.Context, transferSyntax string, msgID uint16, isMove bool) ([]RetrieveMatch, error) {
	var matches []RetrieveMatch
	for {
		rspCmd, rspDS, err := a.recvMessage(ctx)
		if err != nil {
			return matches, err
		}
		var (
			status                    uint16
			subOps                    dimse.SubOperations
			messageIDBeingRespondedTo uint16
		)
		if isMove {
			rsp, err := dimse.DecodeCMoveRSP(rspCmd)
			if err != nil {
				return matches, fmt.Errorf("ae: decode C-MOVE-RSP: %w", err)
			}
			status = rsp.Status
			subOps = rsp.SubOperations
			messageIDBeingRespondedTo = rsp.MessageIDBeingRespondedTo
		} else {
			rsp, err := dimse.DecodeCGetRSP(rspCmd)
			if err != nil {
				return matches, fmt.Errorf("ae: decode C-GET-RSP: %w", err)
			}
			status = rsp.Status
			subOps = rsp.SubOperations
			messageIDBeingRespondedTo = rsp.MessageIDBeingRespondedTo
		}
		if messageIDBeingRespondedTo != msgID {
			return matches, fmt.Errorf("ae: retrieve message id mismatch: got %d want %d", messageIDBeingRespondedTo, msgID)
		}
		m := RetrieveMatch{Status: status, SubOperations: subOps}
		if len(rspDS) > 0 {
			ident, err := godicom.DecodeDataset(rspDS, transferSyntax)
			if err != nil {
				return matches, fmt.Errorf("ae: decode retrieve identifier: %w", err)
			}
			m.Identifier = ident
		}
		matches = append(matches, m)
		if !dimse.IsPending(status) {
			return matches, nil
		}
	}
}

func (a *Association) handleInboundStore(ctx context.Context, pcid byte, rq *dimse.CStoreRQ, dataset []byte, onStore StoreHandler) error {
	status := dimse.StatusSuccess
	if onStore != nil {
		status = onStore(ctx, StoreRequest{
			AffectedSOPClassUID:                  rq.AffectedSOPClassUID,
			AffectedSOPInstanceUID:               rq.AffectedSOPInstanceUID,
			Dataset:                              dataset,
			Priority:                             rq.Priority,
			MoveOriginatorApplicationEntityTitle: rq.MoveOriginatorApplicationEntityTitle,
			MoveOriginatorMessageID:              rq.MoveOriginatorMessageID,
		})
	}
	rsp, err := (&dimse.CStoreRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		AffectedSOPClassUID:       rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    rq.AffectedSOPInstanceUID,
		Status:                    status,
	}).Encode()
	if err != nil {
		return err
	}
	return a.sendMessage(ctx, pcid, rsp, nil)
}

func encodeIdentifier(transferSyntax string, raw []byte, data *godicom.Dataset) ([]byte, error) {
	payload := raw
	if data != nil {
		encoded, err := data.Encode(transferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode identifier: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("ae: missing identifier")
	}
	return payload, nil
}
