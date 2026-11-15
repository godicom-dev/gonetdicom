package ae

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// StoreRequest is an outbound C-STORE-RQ payload.
//
// Three ways to supply the dataset, in the order they take precedence:
//
//	Data     — a godicom Dataset, encoded here with the negotiated transfer syntax
//	Dataset  — bytes that are already encoded
//	Source   — bytes that are opened and read as they go out
//
// Data takes precedence over Dataset, which takes precedence over Source. Use
// Source for an instance that should not be resident: a Part 10 file's dataset
// is the case it was written for, and it is the only one of the three whose
// memory cost does not follow the instance size.
//
// TransferSyntax states what those bytes are. For Dataset and Source it is
// checked rather than trusted: the bytes must be in the syntax of the
// presentation context that carries them, and a peer that accepted only the
// other little-endian syntax — which negotiates, since the two differ only in
// the VR field — gets ErrTransferSyntaxMismatch instead of bytes labelled with
// a syntax they are not in. Data is exempt, because the encoder writes whatever
// syntax the context declares. An empty TransferSyntax with pre-encoded bytes
// means the caller takes that responsibility; a requestor that read the
// accepted contexts itself is the case that wants it.
type StoreRequest struct {
	AffectedSOPClassUID    string
	AffectedSOPInstanceUID string
	Dataset                []byte
	Data                   *godicom.Dataset
	// Source is read as it goes out — see StoreSource. Ignored when Data or
	// Dataset is set.
	Source         StoreSource
	TransferSyntax string
	// Priority requests a service priority from the peer, which may ignore it.
	// The zero value is dimse.PriorityMedium — normal priority.
	Priority                             uint16
	MoveOriginatorApplicationEntityTitle string
	MoveOriginatorMessageID              uint16
}

// StoreSource supplies a dataset without keeping it in memory.
//
// Open returns a reader over the bytes and how many of them there are. The
// length is not optional: a PDV item length is explicit on the wire (PS3.8
// Annex E), so a source that cannot say how large it is cannot be sent — and
// the only way to find out is to buffer it, which is the cost this interface
// exists to avoid.
//
// Open may be called more than once, and each call returns a fresh reader:
// that is what makes a retry a retry. There is deliberately no Seek. DIMSE
// P-DATA is a one-way ordered stream, the retry granularity this package offers
// is the whole instance or the whole association, and an interface that offered
// to resume mid-instance would ask every implementation to answer a question
// the protocol never asks.
//
// A source is read once, during the store. An Open that fails is a store that
// never started; a reader that fails part-way leaves a message whose declared
// length cannot be filled, which ends the association — the protocol has no way
// to say "that dataset was short".
type StoreSource interface {
	Open() (r io.ReadCloser, size int64, err error)
}

// SourceFunc adapts a function to a StoreSource, the way http.HandlerFunc
// adapts a function to an http.Handler.
type SourceFunc func() (io.ReadCloser, int64, error)

// Open implements StoreSource.
func (f SourceFunc) Open() (io.ReadCloser, int64, error) { return f() }

// FileSource sends a dataset straight from a file: Size bytes starting at
// Offset. For a Part 10 file the dataset begins after the 132-byte preamble and
// the File Meta Information group, so Offset is 132 plus the group length;
// for a file that already holds nothing but the dataset, both fields can be
// left zero and Size is the whole file.
//
// Nothing is checked against the file here: the size is the caller's statement
// about a dataset it has already parsed, and re-deriving it would mean reading
// the file twice.
type FileSource struct {
	Path   string
	Offset int64
	Size   int64
}

// Open implements StoreSource.
func (s FileSource) Open() (io.ReadCloser, int64, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, 0, err
	}
	if s.Offset > 0 {
		if _, err := f.Seek(s.Offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, 0, err
		}
	}
	return f, s.Size, nil
}

// openStoreSource opens req.Source and checks that it declares a dataset,
// returning a reader the caller closes.
//
// Both things are done here rather than at the write because the check has to
// happen before the command set goes out: a C-STORE-RQ whose dataset turns out
// to be empty is a promise made and then broken.
func openStoreSource(src StoreSource) (io.ReadCloser, int64, error) {
	r, size, err := src.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("ae: open C-STORE source: %w", err)
	}
	if size <= 0 {
		_ = r.Close()
		return nil, 0, fmt.Errorf("ae: C-STORE source declares %d bytes of dataset", size)
	}
	return r, size, nil
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
//
// A req.Source is opened once, read while the dataset is written, and closed
// again; see StoreSource.
func (a *Association) CStore(ctx context.Context, req StoreRequest) (*StoreResult, error) {
	end, err := a.beginOp("C-STORE")
	if err != nil {
		return nil, err
	}
	defer end()
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
	// A pre-encoded dataset is bytes in the syntax the caller stated. When the
	// acceptor offered no context for that syntax but did offer the other
	// little-endian one, contextForStore picks it — they are the same layout
	// apart from the VR field — and those bytes would then go out on a context
	// that announces a syntax they are not in. Refusing is the honest answer:
	// the caller can pass Data and let the encoder write the negotiated
	// syntax, which is the only way to convert without reading the dataset.
	if req.Data == nil && req.TransferSyntax != "" && pc.TransferSyntax != req.TransferSyntax {
		return nil, fmt.Errorf("%w: context %d is %s, dataset is %s; pass Data to re-encode",
			ErrTransferSyntaxMismatch, pc.ID, pc.TransferSyntax, req.TransferSyntax)
	}

	payload := req.Dataset
	if req.Data != nil {
		encoded, err := req.Data.Encode(godicom.UID(pc.TransferSyntax))
		if err != nil {
			return nil, fmt.Errorf("ae: encode dataset: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 && req.Source == nil {
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
	if len(payload) > 0 {
		err = a.sendMessage(ctx, pc.ID, cmd, payload)
	} else {
		err = a.sendStoreSource(ctx, pc.ID, cmd, req.Source)
	}
	if err != nil {
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

// sendStoreSource writes a C-STORE-RQ whose dataset comes from src, one
// fragment at a time.
//
// The source is opened before anything of the message is written, so a file
// that is not there — the ordinary case for the archive this exists for — is a
// store that failed with nothing on the wire, rather than an association that
// ends half-way through a message.
func (a *Association) sendStoreSource(ctx context.Context, pcid byte, command []byte, src StoreSource) error {
	r, size, err := openStoreSource(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return a.sendMessageReader(ctx, pcid, command, r, size)
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
