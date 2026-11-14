package ae

import (
	"context"
	"strings"

	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/status"
)

// The Query Retrieve Level values of PS3.4 C.6.2.1.1.
const (
	LevelPatient = "PATIENT"
	LevelStudy   = "STUDY"
	LevelSeries  = "SERIES"
	LevelImage   = "IMAGE"
)

// QueryLevel is one level of a Query/Retrieve Information Model: the Query
// Retrieve Level value that names it and the attribute that identifies an
// entity at that level.
//
// The unique key is the one attribute that has to be present for a retrieval at
// this level to have a subject — Series Instance UID cannot be guessed from a
// Patient Name. It is also the key a match returns, which is why the level
// tables need nothing else from the data dictionary.
type QueryLevel struct {
	Name      string
	UniqueKey tag.Tag
}

// QueryHierarchy is the level structure of one Query/Retrieve Information
// Model, root first.
type QueryHierarchy struct {
	Model  string
	Levels []QueryLevel
}

// Level returns the entry for name.
func (h QueryHierarchy) Level(name string) (QueryLevel, bool) {
	for _, l := range h.Levels {
		if l.Name == name {
			return l, true
		}
	}
	return QueryLevel{}, false
}

// The three models worth tabling. The specialised and retired ones (Composite
// Instance Root, Hanging Protocol, Colour Palette, Inventory, the Relevant
// Patient Information models) are not listed: a model with no table is passed
// to the handler unexamined, which is the same policy as not listing its SOP
// Class in AcceptedAbstractSyntaxes. Their identifiers also have little in
// common with this shape — the Relevant Patient models have no Query Retrieve
// Level at all.
var (
	patientRootLevels = []QueryLevel{
		{LevelPatient, tag.PatientID},
		{LevelStudy, tag.StudyInstanceUID},
		{LevelSeries, tag.SeriesInstanceUID},
		{LevelImage, tag.SOPInstanceUID},
	}
	studyRootLevels = []QueryLevel{
		{LevelStudy, tag.StudyInstanceUID},
		{LevelSeries, tag.SeriesInstanceUID},
		{LevelImage, tag.SOPInstanceUID},
	}
	patientStudyOnlyLevels = []QueryLevel{
		{LevelPatient, tag.PatientID},
		{LevelStudy, tag.StudyInstanceUID},
	}
)

var queryHierarchies = map[string]QueryHierarchy{
	PatientRootQueryRetrieveInformationModelFind: {PatientRootQueryRetrieveInformationModelFind, patientRootLevels},
	PatientRootQueryRetrieveInformationModelMove: {PatientRootQueryRetrieveInformationModelMove, patientRootLevels},
	PatientRootQueryRetrieveInformationModelGet:  {PatientRootQueryRetrieveInformationModelGet, patientRootLevels},

	StudyRootQueryRetrieveInformationModelFind: {StudyRootQueryRetrieveInformationModelFind, studyRootLevels},
	StudyRootQueryRetrieveInformationModelMove: {StudyRootQueryRetrieveInformationModelMove, studyRootLevels},
	StudyRootQueryRetrieveInformationModelGet:  {StudyRootQueryRetrieveInformationModelGet, studyRootLevels},

	PatientStudyOnlyQueryRetrieveInformationModelFind: {PatientStudyOnlyQueryRetrieveInformationModelFind, patientStudyOnlyLevels},
	PatientStudyOnlyQueryRetrieveInformationModelMove: {PatientStudyOnlyQueryRetrieveInformationModelMove, patientStudyOnlyLevels},
	PatientStudyOnlyQueryRetrieveInformationModelGet:  {PatientStudyOnlyQueryRetrieveInformationModelGet, patientStudyOnlyLevels},
}

// QueryHierarchyFor returns the level structure of a Query/Retrieve
// Information Model. The second result is false for a model this package has no
// table for, which is not an error: those models are the handler's business.
func QueryHierarchyFor(model string) (QueryHierarchy, bool) {
	h, ok := queryHierarchies[model]
	if !ok {
		return QueryHierarchy{}, false
	}
	h.Levels = append([]QueryLevel(nil), h.Levels...)
	return h, true
}

// QueryModelValidator returns a validator that checks an identifier against the
// level structure of the model it was written for.
//
// It answers more than QueryRetrieveLevelValidator because it knows which
// levels a model defines:
//
//   - The identifier must name a level the model answers. A PATIENT level
//     C-FIND on the Study Root model, or a SERIES level request on the retired
//     Patient/Study Only model, cannot be served by any handler, and 0xA900 is
//     the status that says so.
//   - When levels is non-empty the level must also be one of them. This is the
//     same allow-list QueryRetrieveLevelValidator takes, and it is applied even
//     for a model with no table.
//   - C-MOVE and C-GET must carry the unique key of the level they retrieve
//     (Patient ID, Study, Series or SOP Instance UID). A retrieve that does not
//     name its subject retrieves nothing, and the peer would otherwise get a
//     success with zero sub-operations. C-FIND is exempt: a STUDY level query by
//     Patient Name is a legal query, because matching is the handler's answer to
//     give.
//
// A model with no table is not refused: the level allow-list still applies, but
// the hierarchy and key checks are skipped, because guessing a structure is how
// a validator turns a legal request into a 0xA900. That keeps the hook usable
// for the long tail of models while doing the checks that are certain.
//
// Presence is all the key check claims: a value of "*" is not a missing key.
// Whether a value identifies anything is a question only the handler can answer.
func QueryModelValidator(levels ...string) IdentifierValidator {
	allowed := make(map[string]struct{}, len(levels))
	for _, l := range levels {
		allowed[l] = struct{}{}
	}
	return func(_ context.Context, req IdentifierRequest) uint16 {
		if req.Identifier == nil {
			return status.IdentifierDoesNotMatchSOPClass
		}
		level, ok := req.Identifier.GetString(tag.QueryRetrieveLevel)
		level = strings.TrimSpace(level)
		if !ok || level == "" {
			return status.IdentifierDoesNotMatchSOPClass
		}
		if len(allowed) > 0 {
			if _, ok := allowed[level]; !ok {
				return status.IdentifierDoesNotMatchSOPClass
			}
		}

		h, known := QueryHierarchyFor(req.QueryModel)
		if !known {
			return 0
		}
		entry, ok := h.Level(level)
		if !ok {
			return status.IdentifierDoesNotMatchSOPClass
		}
		if req.Operation == OperationFind {
			return 0
		}
		if key, ok := req.Identifier.GetString(entry.UniqueKey); !ok || strings.TrimSpace(key) == "" {
			return status.IdentifierDoesNotMatchSOPClass
		}
		return 0
	}
}
