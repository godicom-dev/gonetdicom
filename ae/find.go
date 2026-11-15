package ae

import (
	"context"
	"fmt"
	"iter"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// Common Query/Retrieve Information Model Find SOP Classes.
//
// The package does not interpret a Query Model: the identifier is passed to the
// handler and the handler decides what the model means. These constants exist
// so callers stop spelling UIDs by hand; accepting one is still a
// ServerConfig.AcceptedAbstractSyntaxes decision.
const (
	PatientRootQueryRetrieveInformationModelFind = string(uid.PatientRootQueryRetrieveInformationModelFind)
	StudyRootQueryRetrieveInformationModelFind   = string(uid.StudyRootQueryRetrieveInformationModelFind)
	// PatientStudyOnlyQueryRetrieveInformationModelFind is the retired
	// Patient/Study Only model, kept because peers still send it.
	PatientStudyOnlyQueryRetrieveInformationModelFind = string(uid.PatientStudyOnlyQueryRetrieveInformationModelFind)

	// Relevant Patient Information Query models (PS3.4 C.4.2), C-FIND only.
	GeneralRelevantPatientInformationQuery  = string(uid.GeneralRelevantPatientInformationQuery)
	BreastImagingRelevantPatientInformation = string(uid.BreastImagingRelevantPatientInformationQuery)
	CardiacRelevantPatientInformation       = string(uid.CardiacRelevantPatientInformationQuery)
)

// FindRequest is a C-FIND-RQ payload.
//
// Provide Identifier (encoded) or IdentifierData (godicom Dataset). IdentifierData
// takes precedence when both are set.
type FindRequest struct {
	QueryModel     string // Affected SOP Class UID (information model)
	Identifier     []byte
	IdentifierData *godicom.Dataset
	// Priority requests a service priority from the peer, which may ignore it.
	// The zero value is dimse.PriorityMedium — normal priority.
	Priority uint16
	// MessageID, when non-zero, is used as the C-FIND-RQ Message ID (for C-CANCEL).
	MessageID uint16
}

// FindMatch is one C-FIND-RSP (Pending or final).
//
// A match — a response that carries an Identifier — must be
// dimse.StatusPending. The zero status is Success, which is terminal: a handler
// that yields matches without setting Status ends the query on the first one,
// and the requestor sees a successful query with no results. The SCP cannot
// infer the intent, because "Pending with a dataset" and "Success" are different
// answers rather than two spellings of one.
type FindMatch struct {
	Status     uint16
	Identifier *godicom.Dataset // non-nil for Pending responses with a dataset
}

// FindSeq yields matches in order and stops after the last one.
//
// It is the return value of a handler that already has the whole list — the
// shape every handler had before the iterator contract — and the building
// block of FindMatches. Yielding through it is the same act as yielding
// directly: when the framework is no longer reading, the loop ends.
func FindSeq(matches []FindMatch) iter.Seq2[FindMatch, error] {
	return func(yield func(FindMatch, error) bool) {
		for _, m := range matches {
			if !yield(m, nil) {
				return
			}
		}
	}
}

// FindMatches adapts a list of matches to a FindHandler. It is the one-line
// migration for a handler that built its results eagerly:
//
//	// before
//	OnCFind: func(ctx context.Context, req ae.FindRequest) []ae.FindMatch { … }
//	// after
//	OnCFind: ae.FindMatches(list)
func FindMatches(matches []FindMatch) FindHandler {
	return func(context.Context, FindRequest) iter.Seq2[FindMatch, error] {
		return FindSeq(matches)
	}
}

// CFind sends a C-FIND-RQ and collects responses until Success/Failure/Cancel.
func (a *Association) CFind(ctx context.Context, req FindRequest) ([]FindMatch, error) {
	end, err := a.beginOp("C-FIND")
	if err != nil {
		return nil, err
	}
	defer end()
	if req.QueryModel == "" {
		return nil, fmt.Errorf("ae: C-FIND missing Query Model")
	}
	pc, ok := a.contextByAbstract(req.QueryModel)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoContext, req.QueryModel)
	}

	payload := req.Identifier
	if req.IdentifierData != nil {
		encoded, err := req.IdentifierData.Encode(godicom.UID(pc.TransferSyntax))
		if err != nil {
			return nil, fmt.Errorf("ae: encode identifier: %w", err)
		}
		payload = encoded
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("ae: C-FIND missing identifier")
	}

	msgID := req.MessageID
	if msgID == 0 {
		msgID = a.nextMessageID()
	}
	cmd, err := (&dimse.CFindRQ{
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
			ident, err := godicom.DecodeDataset(rspDS, godicom.UID(pc.TransferSyntax))
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
