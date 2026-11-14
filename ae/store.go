package ae

import (
	"context"
	"fmt"
	"io"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// StoreRequest is an outbound C-STORE-RQ payload.
//
// Provide either Dataset (already encoded) or Data (godicom Dataset to
// encode with the negotiated transfer syntax). Data takes precedence when
// both are set.
type StoreRequest struct {
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	Dataset                []byte
	Data                   *godicom.Dataset
	TransferSyntax         string
	// Priority requests a service priority from the peer, which may ignore it.
	// The zero value is dimse.PriorityMedium — normal priority.
	Priority                             uint16
	MoveOriginatorApplicationEntityTitle string
	MoveOriginatorMessageID              uint16
}

// StoreResult is the peer's C-STORE-RSP summary.
type StoreResult struct {
	Status                 uint16
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
}

// StoreEvent is a received C-STORE-RQ, passed to OnCStore (pynetdicom's
// EVT_C_STORE). It mirrors that event:
//
//	Stream         ≈ event.request.DataSet, but pulled rather than buffered
//	FileMeta       ≈ event.file_meta
//	TransferSyntax ≈ the accepted presentation context's syntax
//
// The dataset is not read before the handler runs, and on the way here it was
// never assembled into a contiguous buffer: fragments arrive from the
// association reader one P-DATA at a time, so a 500 MB store costs one
// fragment of memory for as long as the handler keeps up. That is what
// pynetdicom's lazy event.dataset buys with a full BytesIO and what dcm4che
// buys with PDVInputStream and a caller-managed buffer; here it costs a
// channel with a single slot.
//
// The body is single-use: Stream, Bytes and Decode all consume it, and once
// the handler returns the library discards whatever is left. A decode failure
// is reported as a normal error from Decode, and the handler picks the
// response status; returning 0xC211 (cannot understand) mirrors what
// pynetdicom answers when a store handler raises.
type StoreEvent struct {
	AffectedSOPClassUID                  string
	AffectedSOPInstanceUID               string
	FileMeta                             *godicom.FileMetaDataset
	TransferSyntax                       string
	Priority                             uint16
	MoveOriginatorApplicationEntityTitle string
	MoveOriginatorMessageID              uint16

	body *msgBody
}

// Stream returns the dataset as an io.Reader over the fragments still on the
// wire. io.Copy to a file or a socket moves a store of any size through
// constant memory, and io.Copy's buffer, not the dataset, is what grows.
func (ev StoreEvent) Stream() io.Reader {
	return bodyReader(ev.body)
}

// Bytes drains the dataset into memory. Use it when the dataset is small or
// when a decoder needs it contiguous; prefer Stream for anything that could
// be an image.
func (ev StoreEvent) Bytes() ([]byte, error) {
	if ev.body == nil {
		return nil, nil
	}
	return ev.body.Bytes()
}

// Decode parses the dataset with the negotiated transfer syntax.
//
// The error is returned, never hidden: an undecodable store surfaces here,
// where the one piece of code that can react to it is running. Empty data
// and a missing negotiated transfer syntax are errors too.
func (ev StoreEvent) Decode() (*godicom.Dataset, error) {
	if ev.TransferSyntax == "" {
		return nil, fmt.Errorf("ae: C-STORE has no negotiated transfer syntax")
	}
	raw, err := ev.Bytes()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("ae: C-STORE dataset is empty")
	}
	return godicom.DecodeDataset(raw, godicom.UID(ev.TransferSyntax))
}

// CStore sends a C-STORE-RQ and waits for the C-STORE-RSP.
//
// req.Data is read-only: when neither req.AffectedSOPInstanceUID nor the
// dataset's SOPInstanceUID supplies one, CStore generates a UID and encodes a
// copy of the Dataset carrying it, leaving the caller's Dataset as it was.
// Supplying the UID skips the copy.
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
		if req.Data != nil {
			// The generated UID goes on a copy. req.Data belongs to the caller, and
			// writing it back there turned their Dataset into one that already has a
			// SOP Instance UID: re-sending it after changing pixel data or an
			// InstanceNumber then shipped a second, different instance under the
			// first one's identity, which an archive stores as one instance.
			//
			// Callers that set SOPInstanceUID themselves — or pass
			// AffectedSOPInstanceUID, or pre-encoded Dataset bytes — never reach the
			// copy.
			data := req.Data.Clone()
			data.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, req.AffectedSOPInstanceUID))
			req.Data = data
		}
	}
	pc, ok := a.contextForStore(req.AffectedSOPClassUID, req.TransferSyntax)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.AffectedSOPClassUID)
	}

	payload := req.Dataset
	if req.Data != nil {
		encoded, err := req.Data.Encode(godicom.UID(pc.TransferSyntax))
		if err != nil {
			return nil, fmt.Errorf("ae: encode dataset: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("ae: C-STORE missing dataset")
	}

	msgID := a.nextMessageID()
	cmd, err := (&dimse.CStoreRQ{
		MessageID:                            msgID,
		Priority:                             req.Priority,
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

// newInboundStoreEvent builds an OnCStore payload from a received
// C-STORE-RQ (pynetdicom Event.request.DataSet + Event.file_meta). The dataset
// arrives undecoded and unread; the handler opts into Stream, Bytes or Decode.
func newInboundStoreEvent(rq *dimse.CStoreRQ, body *msgBody, transferSyntax string) StoreEvent {
	return StoreEvent{
		AffectedSOPClassUID:                  rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:               rq.AffectedSOPInstanceUID,
		body:                                 body,
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
