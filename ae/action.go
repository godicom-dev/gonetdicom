package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Storage Commitment Push Model SOP Class / Instance UIDs.
const (
	StorageCommitmentPushModelSOPClass    = string(uid.StorageCommitmentPushModel)
	StorageCommitmentPushModelSOPInstance = string(uid.StorageCommitmentPushModelInstance)
)

// ActionRequest is an N-ACTION-RQ payload (e.g. Storage Commitment request).
type ActionRequest struct {
	RequestedSOPClassUID    string
	RequestedSOPInstanceUID string
	ActionTypeID            uint16
	ActionInformation       []byte
	ActionInformationData   *godicom.Dataset // takes precedence when set
	// OnNEventReport, when non-nil, handles an N-EVENT-REPORT-RQ pushed on the
	// same association after the N-ACTION-RSP (Storage Commitment push model).
	OnNEventReport EventReportHandler
}

// ActionResult is the peer's N-ACTION-RSP summary.
type ActionResult struct {
	Status                 uint16
	ActionTypeID           uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	ActionReply            []byte
	ActionReplyData        *godicom.Dataset
}

// EventReportRequest is an N-EVENT-REPORT-RQ payload.
type EventReportRequest struct {
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	EventTypeID            uint16
	EventInformation       []byte
	EventInformationData   *godicom.Dataset
}

// EventReportResult is the peer's N-EVENT-REPORT-RSP summary.
type EventReportResult struct {
	Status                 uint16
	EventTypeID            uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
}

// EventReportHandler handles an incoming N-EVENT-REPORT-RQ; return DIMSE status.
type EventReportHandler func(ctx context.Context, req EventReportRequest) uint16

// NActionHandler handles an incoming N-ACTION-RQ.
// When EventReport is non-nil, the SCP pushes it on the same association after
// the N-ACTION-RSP (convenient for Storage Commitment push tests).
type NActionHandler func(ctx context.Context, req ActionRequest) (ActionResult, *EventReportRequest)

// NAction sends an N-ACTION-RQ and waits for the N-ACTION-RSP.
// If OnNEventReport is set, it then accepts one N-EVENT-REPORT-RQ (push model).
func (a *Association) NAction(ctx context.Context, req ActionRequest) (*ActionResult, error) {
	if req.RequestedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-ACTION missing Requested SOP Class UID")
	}
	if req.RequestedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: N-ACTION missing Requested SOP Instance UID")
	}
	pc, ok := a.contextByAbstract(req.RequestedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.RequestedSOPClassUID)
	}

	payload := req.ActionInformation
	if req.ActionInformationData != nil {
		encoded, err := req.ActionInformationData.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode action information: %w", err)
		}
		payload = encoded
	}

	msgID := a.nextMessageID()
	cmd, err := (&dimse.NActionRQ{
		MessageID:               msgID,
		ActionTypeID:            req.ActionTypeID,
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
	rsp, err := dimse.DecodeNActionRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-ACTION-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-ACTION message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	out := &ActionResult{
		Status:                 rsp.Status,
		ActionTypeID:           rsp.ActionTypeID,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
		ActionReply:            rspDS,
	}
	if len(rspDS) > 0 {
		ds, err := godicom.DecodeDataset(rspDS, pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: decode action reply: %w", err)
		}
		out.ActionReplyData = ds
	}

	if req.OnNEventReport != nil {
		if err := a.handleIncomingEventReport(ctx, req.OnNEventReport); err != nil {
			return out, err
		}
	}
	return out, nil
}

// NEventReport sends an N-EVENT-REPORT-RQ and waits for the N-EVENT-REPORT-RSP.
func (a *Association) NEventReport(ctx context.Context, req EventReportRequest) (*EventReportResult, error) {
	if req.AffectedSOPClassUID == "" {
		return nil, fmt.Errorf("ae: N-EVENT-REPORT missing Affected SOP Class UID")
	}
	if req.AffectedSOPInstanceUID == "" {
		return nil, fmt.Errorf("ae: N-EVENT-REPORT missing Affected SOP Instance UID")
	}
	pc, ok := a.contextByAbstract(req.AffectedSOPClassUID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.AffectedSOPClassUID)
	}

	payload := req.EventInformation
	if req.EventInformationData != nil {
		encoded, err := req.EventInformationData.Encode(pc.TransferSyntax)
		if err != nil {
			return nil, fmt.Errorf("ae: encode event information: %w", err)
		}
		payload = encoded
	}

	msgID := a.nextMessageID()
	cmd, err := (&dimse.NEventReportRQ{
		MessageID:              msgID,
		EventTypeID:            req.EventTypeID,
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
	rspCmd, _, err := a.recvMessage(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := dimse.DecodeNEventReportRSP(rspCmd)
	if err != nil {
		return nil, fmt.Errorf("ae: decode N-EVENT-REPORT-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return nil, fmt.Errorf("ae: N-EVENT-REPORT message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	return &EventReportResult{
		Status:                 rsp.Status,
		EventTypeID:            rsp.EventTypeID,
		AffectedSOPClassUID:    rsp.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rsp.AffectedSOPInstanceUID,
	}, nil
}

func (a *Association) handleIncomingEventReport(ctx context.Context, h EventReportHandler) error {
	pcid, rspCmd, rspDS, err := a.recvMessagePC(ctx)
	if err != nil {
		return err
	}
	rq, err := dimse.DecodeNEventReportRQ(rspCmd)
	if err != nil {
		return fmt.Errorf("ae: expected N-EVENT-REPORT-RQ: %w", err)
	}
	ac, ok := a.contextByID(pcid)
	ts := pdu.ImplicitVRLittleEndian
	if ok {
		ts = ac.TransferSyntax
	}
	var info *godicom.Dataset
	if len(rspDS) > 0 {
		info, err = godicom.DecodeDataset(rspDS, ts)
		if err != nil {
			return fmt.Errorf("ae: decode event information: %w", err)
		}
	}
	status := dimse.StatusSuccess
	if h != nil {
		status = h(ctx, EventReportRequest{
			AffectedSOPClassUID:    rq.AffectedSOPClassUID,
			AffectedSOPInstanceUID: rq.AffectedSOPInstanceUID,
			EventTypeID:            rq.EventTypeID,
			EventInformation:       rspDS,
			EventInformationData:   info,
		})
	}
	rsp, err := (&dimse.NEventReportRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		EventTypeID:               rq.EventTypeID,
		AffectedSOPClassUID:       rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    rq.AffectedSOPInstanceUID,
		Status:                    status,
	}).Encode()
	if err != nil {
		return err
	}
	return a.sendMessage(ctx, pcid, rsp, nil)
}

func (a *Association) contextByID(id byte) (AcceptedContext, bool) {
	for _, c := range a.contexts {
		if c.ID == id {
			return c, true
		}
	}
	return AcceptedContext{}, false
}
