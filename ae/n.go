package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// StatusProcessingFailure is DIMSE status 0x0110 (pynetdicom default when no N-* handler).
const StatusProcessingFailure uint16 = 0x0110

// NGetRequest is an N-GET-RQ payload.
type NGetRequest struct {
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	AttributeIdentifierList []dimse.Tag
}

// NGetResult is the peer's N-GET-RSP summary.
type NGetResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	AttributeList          []byte
	AttributeListData      *godicom.Dataset
}

// SetRequest is an N-SET-RQ payload.
type SetRequest struct {
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	ModificationList        []byte
	ModificationListData    *godicom.Dataset
}

// SetResult is the peer's N-SET-RSP summary.
type SetResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	AttributeList          []byte
	AttributeListData      *godicom.Dataset
}

// CreateRequest is an N-CREATE-RQ payload.
type CreateRequest struct {
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string // optional; empty lets the SCP assign
	AttributeList          []byte
	AttributeListData      *godicom.Dataset
}

// CreateResult is the peer's N-CREATE-RSP summary.
type CreateResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	AttributeList          []byte
	AttributeListData      *godicom.Dataset
}

// DeleteRequest is an N-DELETE-RQ payload.
type DeleteRequest struct {
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
}

// DeleteResult is the peer's N-DELETE-RSP summary.
type DeleteResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
}

// NGetHandler handles an incoming N-GET-RQ.
type NGetHandler func(ctx context.Context, req NGetRequest) NGetResult

// NSetHandler handles an incoming N-SET-RQ.
type NSetHandler func(ctx context.Context, req SetRequest) SetResult

// NCreateHandler handles an incoming N-CREATE-RQ.
type NCreateHandler func(ctx context.Context, req CreateRequest) CreateResult

// NDeleteHandler handles an incoming N-DELETE-RQ.
type NDeleteHandler func(ctx context.Context, req DeleteRequest) DeleteResult

// NGet sends an N-GET-RQ and waits for the N-GET-RSP.
func (a *Association) NGet(ctx context.Context, req NGetRequest) (*NGetResult, error) {
	if req.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-GET missing Requested SOP Class UID")
	}
	if req.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: N-GET missing Requested SOP Instance UID")
	}
	pc, ok := a.contextByAbstract(req.RequestedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.RequestedSOPClassUID)
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.NGetRQ{
		MessageID:               msgID,
		RequestedSOPClassUID:    req.RequestedSOPClassUID,
		RequestedSOPInstanceUID: req.RequestedSOPInstanceUID,
		AttributeIdentifierList: req.AttributeIdentifierList,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, nil); err != nil {
		return nil, err
	}
	rspCmd, rspDS, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeNGetRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-GET-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-GET message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	out := &NGetResult{
		Status:                 rsp.Status,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
		AttributeList:          rspDS,
	}
	if len(rspDS) > 0 {
		ds, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: decode N-GET attribute list: %w", err)
		}
		out.AttributeListData = ds
	}
	return out, nil
}

// NSet sends an N-SET-RQ and waits for the N-SET-RSP.
func (a *Association) NSet(ctx context.Context, req SetRequest) (*SetResult, error) {
	if req.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-SET missing Requested SOP Class UID")
	}
	if req.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: N-SET missing Requested SOP Instance UID")
	}
	pc, ok := a.contextByAbstract(req.RequestedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.RequestedSOPClassUID)
	}
	payload := req.ModificationList
	if req.ModificationListData != nil {
		encoded, err := req.ModificationListData.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode modification list: %w", err)
		}
		payload = encoded
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.NSetRQ{
		MessageID:               msgID,
		RequestedSOPClassUID:    req.RequestedSOPClassUID,
		RequestedSOPInstanceUID: req.RequestedSOPInstanceUID,
		HasDataset:              len(payload) > 0,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, payload); err != nil {
		return nil, err
	}
	rspCmd, rspDS, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeNSetRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-SET-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-SET message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	out := &SetResult{
		Status:                 rsp.Status,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
		AttributeList:          rspDS,
	}
	if len(rspDS) > 0 {
		ds, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: decode N-SET attribute list: %w", err)
		}
		out.AttributeListData = ds
	}
	return out, nil
}

// NCreate sends an N-CREATE-RQ and waits for the N-CREATE-RSP.
func (a *Association) NCreate(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if req.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-CREATE missing Affected SOP Class UID")
	}
	pc, ok := a.contextByAbstract(req.AffectedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.AffectedSOPClassUID)
	}
	payload := req.AttributeList
	if req.AttributeListData != nil {
		encoded, err := req.AttributeListData.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode N-CREATE attribute list: %w", err)
		}
		payload = encoded
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.NCreateRQ{
		MessageID:              msgID,
		AffectedSOPClassUID:    req.AffectedSOPClassUID,
		AffectedSOPInstanceUID: req.AffectedSOPInstanceUID,
		HasDataset:             len(payload) > 0,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, payload); err != nil {
		return nil, err
	}
	rspCmd, rspDS, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeNCreateRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-CREATE-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-CREATE message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	out := &CreateResult{
		Status:                 rsp.Status,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
		AttributeList:          rspDS,
	}
	if len(rspDS) > 0 {
		ds, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: decode N-CREATE attribute list: %w", err)
		}
		out.AttributeListData = ds
	}
	return out, nil
}

// NDelete sends an N-DELETE-RQ and waits for the N-DELETE-RSP.
func (a *Association) NDelete(ctx context.Context, req DeleteRequest) (*DeleteResult, error) {
	if req.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-DELETE missing Requested SOP Class UID")
	}
	if req.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: N-DELETE missing Requested SOP Instance UID")
	}
	pc, ok := a.contextByAbstract(req.RequestedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.RequestedSOPClassUID)
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.NDeleteRQ{
		MessageID:               msgID,
		RequestedSOPClassUID:    req.RequestedSOPClassUID,
		RequestedSOPInstanceUID: req.RequestedSOPInstanceUID,
	}).Encode()
	if err != nil {
		return nil, err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, nil); err != nil {
		return nil, err
	}
	rspCmd, _, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeNDeleteRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-DELETE-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-DELETE message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	return &DeleteResult{
		Status:                 rsp.Status,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
	}, nil
}
