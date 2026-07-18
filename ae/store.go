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
// SCP inbound (OnCStore) mirrors pynetdicom evt.EVT_C_STORE:
//
//	Data     ≈ event.dataset
//	FileMeta ≈ event.file_meta
//	Dataset  = raw PDV bytes (event.request.DataSet)
//
// Save like the pynetdicom storescp example:
//
//	fd := &godicom.FileDataset{Dataset: req.Data, FileMeta: req.FileMeta}
//	_ = fd.SaveAs(req.AffectedSOPInstanceUID+".dcm", &godicom.WriteOptions{EnforceFileFormat: true})
type StoreRequest struct {
	AffectedSOPClassUID                  string
	AffectedSOPInstanceUID               string
	Dataset                              []byte
	Data                                 *godicom.Dataset
	FileMeta                             *godicom.FileMetaDataset
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
	if req.AffectedSOPInstanceUID == "" && req.Data != nil {
		if v, ok := req.Data.GetString(godicom.MustTag("SOPInstanceUID")); ok && v != "" {
			req.AffectedSOPInstanceUID = v
		}
	}
	if req.AffectedSOPInstanceUID == "" {
		req.AffectedSOPInstanceUID = NewInstanceUID()
		if req.Data != nil && !req.Data.Has(godicom.MustTag("SOPInstanceUID")) {
			req.Data.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, req.AffectedSOPInstanceUID))
		}
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

// newInboundStoreRequest builds an OnCStore payload from a received C-STORE-RQ
// (pynetdicom Event.dataset + Event.file_meta).
func newInboundStoreRequest(rq *dimse.CStoreRQ, dataset []byte, transferSyntax string) StoreRequest {
	req := StoreRequest{
		AffectedSOPClassUID:                  rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:               rq.AffectedSOPInstanceUID,
		Dataset:                              dataset,
		TransferSyntax:                       transferSyntax,
		Priority:                             rq.Priority,
		MoveOriginatorApplicationEntityTitle: rq.MoveOriginatorApplicationEntityTitle,
		MoveOriginatorMessageID:              rq.MoveOriginatorMessageID,
		FileMeta: createFileMeta(
			rq.AffectedSOPClassUID,
			rq.AffectedSOPInstanceUID,
			transferSyntax,
		),
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

// createFileMeta mirrors pynetdicom.dsutils.create_file_meta / Event.file_meta.
func createFileMeta(sopClassUID, sopInstanceUID, transferSyntax string) *godicom.FileMetaDataset {
	meta := godicom.NewFileMetaDataset()
	meta.Set(godicom.NewDataElement(godicom.MustTag("FileMetaInformationGroupLength"), godicom.VRUL, uint32(0)))
	meta.Set(godicom.NewDataElement(godicom.MustTag("FileMetaInformationVersion"), godicom.VROB, []byte{0x00, 0x01}))
	if sopClassUID != "" {
		meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPClassUID"), godicom.VRUI, sopClassUID))
	}
	if sopInstanceUID != "" {
		meta.Set(godicom.NewDataElement(godicom.MustTag("MediaStorageSOPInstanceUID"), godicom.VRUI, sopInstanceUID))
	}
	if transferSyntax != "" {
		meta.Set(godicom.NewDataElement(godicom.MustTag("TransferSyntaxUID"), godicom.VRUI, transferSyntax))
	}
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationClassUID"), godicom.VRUI, ImplementationClassUID))
	meta.Set(godicom.NewDataElement(godicom.MustTag("ImplementationVersionName"), godicom.VRSH, ImplementationVersionName))
	return meta
}
