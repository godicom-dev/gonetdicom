package ae_test

import (
	"context"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

// The table is data, so pin it: a level that drifts from PS3.4 C.6.2.1.1 turns
// QueryModelValidator into a validator that refuses legal requests.
func TestQueryHierarchyFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model  string
		levels []ae.QueryLevel
	}{
		{
			ae.PatientRootQueryRetrieveInformationModelFind,
			[]ae.QueryLevel{
				{Name: ae.LevelPatient, UniqueKey: tag.PatientID},
				{Name: ae.LevelStudy, UniqueKey: tag.StudyInstanceUID},
				{Name: ae.LevelSeries, UniqueKey: tag.SeriesInstanceUID},
				{Name: ae.LevelImage, UniqueKey: tag.SOPInstanceUID},
			},
		},
		{
			ae.StudyRootQueryRetrieveInformationModelGet,
			[]ae.QueryLevel{
				{Name: ae.LevelStudy, UniqueKey: tag.StudyInstanceUID},
				{Name: ae.LevelSeries, UniqueKey: tag.SeriesInstanceUID},
				{Name: ae.LevelImage, UniqueKey: tag.SOPInstanceUID},
			},
		},
		{
			ae.PatientStudyOnlyQueryRetrieveInformationModelMove,
			[]ae.QueryLevel{
				{Name: ae.LevelPatient, UniqueKey: tag.PatientID},
				{Name: ae.LevelStudy, UniqueKey: tag.StudyInstanceUID},
			},
		},
	}
	for _, tc := range tests {
		h, ok := ae.QueryHierarchyFor(tc.model)
		if !ok {
			t.Fatalf("%s: no hierarchy", tc.model)
		}
		if h.Model != tc.model {
			t.Errorf("%s: Model = %q", tc.model, h.Model)
		}
		if len(h.Levels) != len(tc.levels) {
			t.Fatalf("%s: %d levels, want %d (%v)", tc.model, len(h.Levels), len(tc.levels), h.Levels)
		}
		for i, want := range tc.levels {
			if h.Levels[i] != want {
				t.Errorf("%s level %d = %+v, want %+v", tc.model, i, h.Levels[i], want)
			}
		}
	}

	// Every level of a model is reachable by name, and a level from another
	// model is not.
	h, _ := ae.QueryHierarchyFor(ae.StudyRootQueryRetrieveInformationModelFind)
	if _, ok := h.Level(ae.LevelPatient); ok {
		t.Error("Study Root answers a PATIENT level")
	}
	if _, ok := h.Level(ae.LevelImage); !ok {
		t.Error("Study Root has no IMAGE level")
	}

	// A model with no table is a "no", not an error: the handler owns it.
	if _, ok := ae.QueryHierarchyFor(ae.GeneralRelevantPatientInformationQuery); ok {
		t.Error("a Relevant Patient Information model was tabled")
	}
	if _, ok := ae.QueryHierarchyFor(""); ok {
		t.Error("an empty UID was tabled")
	}

	// The returned slice is a copy; a caller sorting it must not reorder the
	// table for everyone else.
	h.Levels[0], h.Levels[2] = h.Levels[2], h.Levels[0]
	h2, _ := ae.QueryHierarchyFor(ae.StudyRootQueryRetrieveInformationModelFind)
	if h2.Levels[0].Name != ae.LevelStudy {
		t.Errorf("hierarchy table was mutated: %v", h2.Levels)
	}
}

func qrIdentifier(level string, keys ...tag.Tag) *godicom.Dataset {
	ds := godicom.NewDataset()
	if level != "" {
		ds.Set(godicom.NewDataElement(tag.QueryRetrieveLevel, godicom.VRCS, level))
	}
	for _, k := range keys {
		ds.Set(godicom.NewDataElement(k, godicom.VRUI, "1.2.3"))
	}
	return ds
}

// What separates this validator from QueryRetrieveLevelValidator is that it
// knows each model's levels: a mismatch is refused even though the level
// itself is a legal value, and a retrieve has to name its subject.
func TestQueryModelValidator(t *testing.T) {
	t.Parallel()

	study := qrIdentifier(ae.LevelStudy, tag.PatientID, tag.StudyInstanceUID)
	series := qrIdentifier(ae.LevelSeries, tag.StudyInstanceUID, tag.SeriesInstanceUID)
	seriesNoKey := qrIdentifier(ae.LevelSeries, tag.StudyInstanceUID)
	emptyKey := godicom.NewDataset()
	emptyKey.Set(godicom.NewDataElement(tag.QueryRetrieveLevel, godicom.VRCS, ae.LevelStudy))
	emptyKey.Set(godicom.NewDataElement(tag.StudyInstanceUID, godicom.VRUI, ""))
	byName := godicom.NewDataset()
	byName.Set(godicom.NewDataElement(tag.QueryRetrieveLevel, godicom.VRCS, ae.LevelStudy))
	byName.Set(godicom.NewDataElement(tag.PatientName, godicom.VRPN, "DOE^JANE"))
	paddedLevel := godicom.NewDataset()
	paddedLevel.Set(godicom.NewDataElement(tag.QueryRetrieveLevel, godicom.VRCS, ae.LevelStudy+" "))
	paddedLevel.Set(godicom.NewDataElement(tag.StudyInstanceUID, godicom.VRUI, "1.2.3"))

	tests := []struct {
		name   string
		levels []string
		req    ae.IdentifierRequest
		want   uint16
	}{
		{
			name: "no identifier",
			req:  ae.IdentifierRequest{Operation: ae.OperationFind, QueryModel: ae.PatientRootQueryRetrieveInformationModelFind},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "no level",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelFind,
				Identifier: qrIdentifier("", tag.PatientID),
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "level the model defines",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelFind,
				Identifier: study,
			},
			want: 0,
		},
		{
			name: "PATIENT level on Study Root",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.StudyRootQueryRetrieveInformationModelFind,
				Identifier: qrIdentifier(ae.LevelPatient, tag.PatientID),
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "SERIES level on Patient/Study Only",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.PatientStudyOnlyQueryRetrieveInformationModelFind,
				Identifier: series,
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name:   "allow-list excludes a level the model defines",
			levels: []string{ae.LevelPatient},
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelFind,
				Identifier: study,
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "C-FIND needs no unique key",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.StudyRootQueryRetrieveInformationModelFind,
				Identifier: byName,
			},
			want: 0,
		},
		{
			name: "C-MOVE carries the level's unique key",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationMove,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelMove,
				Identifier: series,
			},
			want: 0,
		},
		{
			name: "C-MOVE without the level's unique key",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationMove,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelMove,
				Identifier: seriesNoKey,
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "C-GET with an empty unique key",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationGet,
				QueryModel: ae.StudyRootQueryRetrieveInformationModelGet,
				Identifier: emptyKey,
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "C-MOVE at PATIENT level needs Patient ID",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationMove,
				QueryModel: ae.PatientRootQueryRetrieveInformationModelMove,
				Identifier: qrIdentifier(ae.LevelPatient),
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
		{
			name: "padded level value",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationGet,
				QueryModel: ae.StudyRootQueryRetrieveInformationModelGet,
				Identifier: paddedLevel,
			},
			want: 0,
		},
		{
			name: "unknown model is not judged",
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.GeneralRelevantPatientInformationQuery,
				Identifier: qrIdentifier(ae.LevelPatient, tag.PatientID),
			},
			want: 0,
		},
		{
			name:   "allow-list still applies to an unknown model",
			levels: []string{ae.LevelStudy},
			req: ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				QueryModel: ae.GeneralRelevantPatientInformationQuery,
				Identifier: qrIdentifier(ae.LevelPatient, tag.PatientID),
			},
			want: status.IdentifierDoesNotMatchSOPClass,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ae.QueryModelValidator(tc.levels...)(context.Background(), tc.req)
			if got != tc.want {
				t.Fatalf("status = 0x%04x, want 0x%04x", got, tc.want)
			}
		})
	}
}

// The rule the model table adds on the wire: a C-MOVE that names no Series
// Instance UID gets 0xA900 instead of a success with zero sub-operations, and
// the association survives the refusal.
func TestQueryModelValidatorRefusesRetrieveWithoutKey(t *testing.T) {
	t.Parallel()

	var moveCalls int
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelMove},
		IdentifierValidator:      ae.QueryModelValidator(),
		OnCMove: func(context.Context, ae.MoveRequest) ae.MovePlan {
			moveCalls++
			return ae.MovePlan{}
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	moves, err := assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "ANYWHERE",
		IdentifierData:  qrIdentifier(ae.LevelSeries, tag.StudyInstanceUID),
	})
	if err != nil {
		t.Fatalf("CMove: %v", err)
	}
	if len(moves) == 0 || moves[len(moves)-1].Status != status.IdentifierDoesNotMatchSOPClass {
		t.Fatalf("matches = %+v, want 0xA900", moves)
	}
	if moveCalls != 0 {
		t.Fatalf("handler ran %d times for a retrieve with no subject", moveCalls)
	}

	ok, err := assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "ANYWHERE",
		IdentifierData:  qrIdentifier(ae.LevelSeries, tag.StudyInstanceUID, tag.SeriesInstanceUID),
	})
	if err != nil {
		t.Fatalf("second CMove: %v", err)
	}
	if len(ok) == 0 || ok[len(ok)-1].Status != dimse.StatusSuccess {
		t.Fatalf("matches = %+v, want success", ok)
	}
	if moveCalls != 1 {
		t.Fatalf("handler ran %d times, want 1", moveCalls)
	}
}
