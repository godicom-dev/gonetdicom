package ae

import (
	"context"
	"fmt"
	"iter"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Query/Retrieve Information Model Move / Get SOP Classes.
//
// As with the Find models, none of these is interpreted here: the identifier
// goes to the handler. The list covers what pynetdicom registers, so a peer
// that still asks for the retired models is not forced to use raw UID strings.
const (
	PatientRootQueryRetrieveInformationModelMove = string(uid.PatientRootQueryRetrieveInformationModelMove)
	PatientRootQueryRetrieveInformationModelGet  = string(uid.PatientRootQueryRetrieveInformationModelGet)
	StudyRootQueryRetrieveInformationModelMove   = string(uid.StudyRootQueryRetrieveInformationModelMove)
	StudyRootQueryRetrieveInformationModelGet    = string(uid.StudyRootQueryRetrieveInformationModelGet)

	PatientStudyOnlyQueryRetrieveInformationModelMove = string(uid.PatientStudyOnlyQueryRetrieveInformationModelMove)
	PatientStudyOnlyQueryRetrieveInformationModelGet  = string(uid.PatientStudyOnlyQueryRetrieveInformationModelGet)

	// Composite Instance Root retrieval (PS3.4 C.4.3.3).
	CompositeInstanceRootRetrieveMove = string(uid.CompositeInstanceRootRetrieveMove)
	CompositeInstanceRootRetrieveGet  = string(uid.CompositeInstanceRootRetrieveGet)
	// CompositeInstanceRetrieveWithoutBulkDataGet retrieves metadata and
	// non-pixel attributes for an instance without the bulk data.
	CompositeInstanceRetrieveWithoutBulkDataGet = string(uid.CompositeInstanceRetrieveWithoutBulkDataGet)
)

// MoveRequest is a C-MOVE-RQ payload.
type MoveRequest struct {
	QueryModel      string
	MoveDestination string
	Identifier      []byte
	IdentifierData  *godicom.Dataset
	// Priority requests a service priority from the peer, which may ignore it.
	// The zero value is dimse.PriorityMedium — normal priority.
	Priority uint16
	// MessageID names the request on the wire. Zero allocates one, which is
	// what nearly every caller wants; set it when another goroutine needs to
	// C-CANCEL this operation by id while it is running.
	MessageID uint16
}

// RetrieveMatch is one C-MOVE/C-GET response.
type RetrieveMatch struct {
	Status        uint16
	SubOperations dimse.SubOperations
	Identifier    *godicom.Dataset // Failed SOP Instance UID List when present
}

// RetrievePlan is the first item a retrieval handler yields: how many
// sub-operations there are going to be, and — for C-MOVE — where they go and
// what they need.
//
// Total is not a convenience. A Pending response carries Number of Remaining
// Sub-operations and the final one carries all four counts, and PS3.4
// C.4.3.1.3 requires them to be consistent with each other — which cannot be
// written truthfully without knowing the total up front. pynetdicom has the
// same rule, expressed as "the first yield is the count"
// (service_class.py:1727 for C-GET, :2116 for C-MOVE).
type RetrievePlan struct {
	Total int

	// Destination is C-MOVE's. A C-MOVE sub-operation runs on a second
	// association, so its endpoint has to be known before the first store.
	// Nil falls back to ServerConfig.MoveDestinations[req.MoveDestination];
	// non-nil overrides it, and a non-nil value with an empty Addr is an
	// unknown destination (0xA801), which is what pynetdicom does with a
	// (None, None) first yield.
	Destination *MoveDestination

	// Contexts is C-MOVE's: the SOP Class / transfer syntax pairs the
	// sub-operations will be sent in. Required when Total > 0, because an
	// A-ASSOCIATE-RQ has to list every presentation context before the first
	// C-STORE can be proposed — the one thing about this service that cannot
	// be discovered as the iterator runs.
	//
	// pynetdicom gets the same information from whatever the caller put in
	// ApplicationEntity.requested_contexts, and raises if the list is empty
	// (ae.py:associate). Declaring it per operation is more precise than
	// declaring it per application entity: a handler that knows which series
	// it is about to move also knows which SOP Classes are in it.
	Contexts []StoreContext
}

// StoreContext is one SOP Class and the transfer syntax its datasets are
// encoded in, which is one presentation context of the Storage association a
// C-MOVE opens. An empty TransferSyntax offers Implicit VR Little Endian.
type StoreContext struct {
	SOPClassUID    string
	TransferSyntax string
}

// RetrieveOp is one item a C-GET or C-MOVE handler yields. Exactly one of Plan,
// Store and Result is set, and the order is the protocol's: a Plan first, then
// the sub-operations, then whatever the handler wants to say.
//
// A Store is performed by the framework before the handler resumes, and the
// peer's C-STORE-RSP is written back to Outcome. That write-back is what
// replaces pynetdicom's association handle in the handler: the handler reads
// the result on its own goroutine, with no lock and no future — and because the
// number is measured rather than reported, the counts in the responses are
// facts, not claims (PR #59 made the same change for C-MOVE).
//
// Where the store runs differs by service, and the difference is the protocol's
// rather than this API's: C-GET stores on the association the request arrived
// on (SCU role, same connection), C-MOVE stores on the association its Plan
// named a Destination for.
type RetrieveOp struct {
	// Plan states how many sub-operations follow. Required, and only valid as
	// the first item.
	Plan *RetrievePlan
	// Store asks for one C-STORE sub-operation.
	Store *StoreRequest
	// Result sends this response as a C-GET-RSP or C-MOVE-RSP. The counts and
	// the aggregate status are the framework's to fill in: whatever the handler
	// put there is replaced by the measurement.
	Result *RetrieveMatch

	// Outcome is what the peer answered for this Store. The framework writes it
	// before the yield returns.
	Outcome StoreOutcome
}

// StoreOutcome is the result of one sub-operation the framework performed.
//
// Status is the C-STORE-RSP the destination sent, which is the number the
// counts are computed from. A sub-operation that could not be attempted at all
// is not an outcome: for C-GET, which stores on the association it is serving,
// that is a broken association rather than a per-instance failure and it ends
// the operation; for C-MOVE it is counted as a failure, because the destination
// failing to accept an association is exactly a sub-operation that could not be
// performed.
type StoreOutcome struct {
	Status uint16
}

// GetHandler handles an incoming C-GET-RQ. It returns an iterator over
// *RetrieveOp, in the order the SCP should act on them.
//
// The iterator is the Go spelling of pynetdicom's C-GET generator
// (service_class.py:1690-1810) with one difference that is the point of it: the
// store belongs to the framework, so a 500-instance retrieval produces and sends
// one instance at a time instead of materialising five hundred StoreRequests
// before the first C-STORE leaves the process.
//
// ctx and the yield follow the same rules as FindHandler: the framework cancels
// ctx when the peer stops the operation (context.Cause reports
// ErrPeerCancelled / ErrPeerReleased), and a yield that returns false means the
// peer is no longer listening.
type GetHandler func(ctx context.Context, req GetRequest) iter.Seq2[*RetrieveOp, error]

// MoveHandler handles an incoming C-MOVE-RQ. It returns an iterator over
// *RetrieveOp, in the order the SCP should act on them.
//
// The same contract as GetHandler, with the same purpose: pynetdicom's C-MOVE
// SCP (service_class.py:2116-2400) takes the destination, then the count, then
// one (status, dataset) pair at a time and stores each before asking for the
// next, so the batch never has to exist as a value. Its first yield has always
// carried the endpoint and the count; here they are one RetrievePlan with the
// names they have in the standard, and Contexts joins them because a Storage
// association cannot propose a context it was not told about.
//
// The sub-operations run one at a time, on one association to the destination.
// That is what pynetdicom does, and it is what the write-back requires: a
// handler that resumes with Outcome filled in cannot have a store still in
// flight. MaxAssociations, which fanned C-MOVE stores across parallel
// associations, is gone for that reason — see ADR 0002 §6.
type MoveHandler func(ctx context.Context, req MoveRequest) iter.Seq2[*RetrieveOp, error]

// StoreContexts derives the presentation contexts a set of stores needs: one
// per distinct SOP Class and transfer syntax, in the order they first appear.
func StoreContexts(stores []StoreRequest) []StoreContext {
	seen := make(map[StoreContext]struct{}, len(stores))
	var out []StoreContext
	for _, store := range stores {
		if store.AffectedSOPClassUID == "" {
			continue
		}
		ts := store.TransferSyntax
		if ts == "" {
			ts = pdu.ImplicitVRLittleEndian
		}
		sc := StoreContext{SOPClassUID: store.AffectedSOPClassUID, TransferSyntax: ts}
		if _, ok := seen[sc]; ok {
			continue
		}
		seen[sc] = struct{}{}
		out = append(out, sc)
	}
	return out
}

// PlannedOps yields a GetPlan's items in the order the slice contract sent
// them: the plan, its stores one at a time, then its responses.
//
// It is the building block of PlannedGet and what a handler returns when it
// enumerates eagerly but still has to run first — a lookup, a log line, a
// decision about the identifier.
func PlannedOps(plan GetPlan) iter.Seq2[*RetrieveOp, error] {
	return retrieveOps(&RetrievePlan{Total: len(plan.Stores)}, plan.Stores, plan.Responses)
}

// PlannedMove adapts a MovePlan — the whole list of sub-operations, which is
// what every C-MOVE handler used before the iterator contract — to a
// MoveHandler. The plan's presentation contexts are derived from its stores,
// which is what the plan path did and what a handler that already has the batch
// in hand can still say.
func PlannedMove(plan MovePlan) MoveHandler {
	return func(context.Context, MoveRequest) iter.Seq2[*RetrieveOp, error] {
		return PlannedMoveOps(plan)
	}
}

// PlannedMoveOps yields a MovePlan's items in the order the slice contract sent
// them. It is PlannedOps' counterpart, and what the SCP uses as its default when
// no handler is configured — nothing to move, so the answer is a bare Success.
func PlannedMoveOps(plan MovePlan) iter.Seq2[*RetrieveOp, error] {
	return retrieveOps(
		&RetrievePlan{
			Total:       len(plan.Stores),
			Destination: plan.Destination,
			Contexts:    StoreContexts(plan.Stores),
		},
		plan.Stores,
		plan.Responses,
	)
}

func retrieveOps(plan *RetrievePlan, stores []StoreRequest, responses []RetrieveMatch) iter.Seq2[*RetrieveOp, error] {
	return func(yield func(*RetrieveOp, error) bool) {
		if !yield(&RetrieveOp{Plan: plan}, nil) {
			return
		}
		for i := range stores {
			if !yield(&RetrieveOp{Store: &stores[i]}, nil) {
				return
			}
		}
		for i := range responses {
			if !yield(&RetrieveOp{Result: &responses[i]}, nil) {
				return
			}
		}
	}
}

// PlannedGet adapts a GetPlan — the whole list of sub-operations, which is what
// every C-GET handler used before the iterator contract — to a GetHandler.
func PlannedGet(plan GetPlan) GetHandler {
	return func(context.Context, GetRequest) iter.Seq2[*RetrieveOp, error] {
		return PlannedOps(plan)
	}
}

// CMove sends a C-MOVE-RQ and collects responses until a final status.
func (a *Association) CMove(ctx context.Context, req MoveRequest) ([]RetrieveMatch, error) {
	end, err := a.beginOp("C-MOVE")
	if err != nil {
		return nil, err
	}
	defer end()
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
	msgID := req.MessageID
	if msgID == 0 {
		msgID = a.nextMessageID()
	}
	cmd, err := (&dimse.CMoveRQ{
		MessageID:           msgID,
		Priority:            req.Priority,
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
	// Priority requests a service priority from the peer, which may ignore it.
	// The zero value is dimse.PriorityMedium — normal priority.
	Priority uint16
	// MessageID names the request on the wire. Zero allocates one; set it when
	// another goroutine needs to C-CANCEL this retrieval by id while it runs —
	// the way to stop a C-GET that is already storing instances, since a
	// release is not allowed while an operation is outstanding.
	MessageID uint16
	OnCStore  StoreHandler
}

// CGet sends a C-GET-RQ, handles interleaved C-STORE requests, and collects
// C-GET responses until a final status.
func (a *Association) CGet(ctx context.Context, req GetRequest) ([]RetrieveMatch, error) {
	end, err := a.beginOp("C-GET")
	if err != nil {
		return nil, err
	}
	defer end()
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
	msgID := req.MessageID
	if msgID == 0 {
		msgID = a.nextMessageID()
	}
	cmd, err := (&dimse.CGetRQ{
		MessageID:           msgID,
		Priority:            req.Priority,
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
		pcid, rspCmd, rspBody, err := a.recvMessagePC(ctx)
		if err != nil {
			return matches, err
		}

		if storeRQ, err := dimse.DecodeCStoreRQ(rspCmd); err == nil {
			// The body is the stored dataset, possibly the largest thing on
			// this association. handleInboundStore closes it when the handler
			// is done, reading it or not.
			if err := a.handleInboundStore(ctx, pcid, storeRQ, rspBody, req.OnCStore); err != nil {
				return matches, err
			}
			continue
		}

		rspDS, err := drainBody(rspBody)
		if err != nil {
			return matches, fmt.Errorf("ae: read C-GET response dataset: %w", err)
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
			ident, err := godicom.DecodeDataset(rspDS, godicom.UID(pc.TransferSyntax))
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
			ident, err := godicom.DecodeDataset(rspDS, godicom.UID(transferSyntax))
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

func (a *Association) handleInboundStore(ctx context.Context, pcid byte, rq *dimse.CStoreRQ, body *msgBody, onStore StoreHandler) error {
	// Whatever the handler does with the dataset, the socket has to be left at
	// the next message: on the pull side that means reading to the last
	// fragment, which is what Close does. A panicking handler is the one case
	// that does not wait — the association is no longer trustworthy, and
	// blocking on a peer that stopped sending would hide the bug.
	if body != nil {
		defer func() {
			if p := recover(); p != nil {
				body.Discard()
				panic(p)
			}
			_ = body.Close()
		}()
	}
	status := dimse.StatusSuccess
	if onStore != nil {
		ts := ""
		abstractSyntax := ""
		for _, c := range a.contexts {
			if c.ID == pcid {
				ts = c.TransferSyntax
				abstractSyntax = c.AbstractSyntax
				break
			}
		}
		// The SCU temporarily acts as Storage SCP; the same AssocInfo
		// contract applies: the association's own AE titles, and the peer's
		// address and implementation identification from the AC.
		info, _ := AssocFromContext(ctx)
		info.CallingAETitle = a.cfg.AETitle
		info.CalledAETitle = a.called
		info.RemoteAddr = a.conn.RemoteAddr()
		info.RemoteImplementationClassUID = a.peerImplClassUID
		info.RemoteImplementationVersionName = a.peerImplVersionName
		info.AbstractSyntax = abstractSyntax
		info.PresentationContextID = pcid
		info.TransferSyntax = ts
		ctx = WithAssoc(ctx, info)
		status = onStore(ctx, newInboundStoreEvent(rq, body, ts))
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
		encoded, err := data.Encode(godicom.UID(transferSyntax))
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
