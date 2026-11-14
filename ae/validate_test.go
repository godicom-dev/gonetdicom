package ae_test

import (
	"context"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

func identifierWithLevel(level string) *godicom.Dataset {
	ds := godicom.NewDataset()
	if level != "" {
		ds.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, level))
	}
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "*"))
	return ds
}

// The level check is the one rule every Query/Retrieve SCP needs and almost
// none writes: without it a request that omits Query Retrieve Level reaches a
// handler that cannot answer it.
func TestQueryRetrieveLevelValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		levels []string
		ident  *godicom.Dataset
		want   uint16
	}{
		{"no identifier", nil, nil, status.IdentifierDoesNotMatchSOPClass},
		{"missing level", nil, identifierWithLevel(""), status.IdentifierDoesNotMatchSOPClass},
		{"level present, any accepted", nil, identifierWithLevel("SERIES"), 0},
		{"level allowed", []string{"PATIENT", "STUDY"}, identifierWithLevel("STUDY"), 0},
		{"level not allowed", []string{"PATIENT", "STUDY"}, identifierWithLevel("IMAGE"), status.IdentifierDoesNotMatchSOPClass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ae.QueryRetrieveLevelValidator(tc.levels...)(context.Background(), ae.IdentifierRequest{
				Operation:  ae.OperationFind,
				Identifier: tc.ident,
			})
			if got != tc.want {
				t.Fatalf("status = 0x%04x, want 0x%04x", got, tc.want)
			}
		})
	}
}

// A refused C-FIND must not reach the handler: the answer is 0xA900 and no
// matches, because the SCP never got as far as querying.
func TestIdentifierValidatorRefusesFind(t *testing.T) {
	t.Parallel()

	var called int
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelFind},
		IdentifierValidator:      ae.QueryRetrieveLevelValidator("PATIENT", "STUDY"),
		OnCFind: func(context.Context, ae.FindRequest) []ae.FindMatch {
			called++
			return []ae.FindMatch{{Status: dimse.StatusSuccess}}
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	matches, err := assoc.CFind(ctx, ae.FindRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
		IdentifierData: identifierWithLevel("SERIES"),
	})
	if err != nil {
		t.Fatalf("CFind: %v", err)
	}
	if len(matches) != 1 || matches[0].Status != status.IdentifierDoesNotMatchSOPClass {
		t.Fatalf("matches = %+v, want a single 0xA900", matches)
	}
	if called != 0 {
		t.Fatalf("handler ran %d times for a refused identifier", called)
	}

	// The association is still usable: a refusal is a status, not a fault.
	if _, err := assoc.CFind(ctx, ae.FindRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
		IdentifierData: identifierWithLevel("STUDY"),
	}); err != nil {
		t.Fatalf("second CFind: %v", err)
	}
	if called != 1 {
		t.Fatalf("handler ran %d times, want 1", called)
	}
}

// C-MOVE and C-GET answer the same way, and their own handler is skipped too.
func TestIdentifierValidatorRefusesRetrieval(t *testing.T) {
	t.Parallel()

	var moveCalls, getCalls int
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{
			ae.PatientRootQueryRetrieveInformationModelMove,
			ae.PatientRootQueryRetrieveInformationModelGet,
		},
		IdentifierValidator: ae.QueryRetrieveLevelValidator("PATIENT"),
		OnCMove: func(context.Context, ae.MoveRequest) ae.MovePlan {
			moveCalls++
			return ae.MovePlan{}
		},
		OnCGet: func(context.Context, ae.GetRequest) ae.GetPlan {
			getCalls++
			return ae.GetPlan{}
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	moves, err := assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
		MoveDestination: "ANYWHERE",
		IdentifierData:  identifierWithLevel("STUDY"),
	})
	if err != nil {
		t.Fatalf("CMove: %v", err)
	}
	if len(moves) == 0 || moves[len(moves)-1].Status != status.IdentifierDoesNotMatchSOPClass {
		t.Fatalf("C-MOVE matches = %+v, want 0xA900", moves)
	}

	gets, err := assoc.CGet(ctx, ae.GetRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
		IdentifierData: identifierWithLevel("STUDY"),
	})
	if err != nil {
		t.Fatalf("CGet: %v", err)
	}
	if len(gets) == 0 || gets[len(gets)-1].Status != status.IdentifierDoesNotMatchSOPClass {
		t.Fatalf("C-GET matches = %+v, want 0xA900", gets)
	}

	if moveCalls != 0 || getCalls != 0 {
		t.Fatalf("handlers ran (move=%d get=%d) despite a refused identifier", moveCalls, getCalls)
	}
}
