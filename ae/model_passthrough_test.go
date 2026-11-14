package ae_test

import (
	"context"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// A Query Model is the handler's business, not the framework's: the package
// must not need a per-model code path to serve one. This drives two models it
// has no special knowledge of through the same handler, which is what makes the
// long tail of retired and specialised models usable at all.
func TestCFindServesModelsWithoutDedicatedCode(t *testing.T) {
	t.Parallel()

	models := []string{
		ae.PatientStudyOnlyQueryRetrieveInformationModelFind,
		ae.GeneralRelevantPatientInformationQuery,
	}

	seen := make(map[string]int)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "MODELSCP",
		AcceptedAbstractSyntaxes: models,
		OnCFind: func(_ context.Context, req ae.FindRequest) []ae.FindMatch {
			seen[req.QueryModel]++
			return []ae.FindMatch{{Status: dimse.StatusSuccess}}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, model := range models {
		assoc, err := ae.Dial(ctx, ae.Config{
			AETitle: "MODELSCU",
			PresentationContexts: []ae.PresentationContext{
				{ID: byte(2*i + 1), AbstractSyntax: model, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			},
		}, addr, "MODELSCP")
		if err != nil {
			t.Fatalf("dial for %s: %v", model, err)
		}

		matches, err := assoc.CFind(ctx, ae.FindRequest{
			QueryModel:     model,
			IdentifierData: findIdentifier(),
		})
		if err != nil {
			_ = assoc.Abort()
			t.Fatalf("C-FIND %s: %v", model, err)
		}
		if len(matches) != 1 || matches[0].Status != dimse.StatusSuccess {
			t.Errorf("%s: matches = %+v", model, matches)
		}
		_ = assoc.Release(ctx)
	}

	for _, model := range models {
		if seen[model] != 1 {
			t.Errorf("handler saw %s %d times, want 1", model, seen[model])
		}
	}
}

// The same for a retrieval model: C-MOVE with a model the package has no
// constant-driven behaviour for still reaches the handler with its identifier.
func TestCMoveServesModelWithoutDedicatedCode(t *testing.T) {
	t.Parallel()

	const model = ae.PatientStudyOnlyQueryRetrieveInformationModelMove
	var (
		gotModel  string
		gotLevel  string
		gotCalled int
	)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "MOVEMODEL",
		AcceptedAbstractSyntaxes: []string{model},
		OnCMove: func(_ context.Context, req ae.MoveRequest) ae.MovePlan {
			gotCalled++
			gotModel = req.QueryModel
			if req.IdentifierData != nil {
				gotLevel, _ = req.IdentifierData.GetString(godicom.MustTag("QueryRetrieveLevel"))
			}
			// No stores and no destination: the SCP answers with counts alone,
			// which is enough to prove the request reached the handler.
			return ae.MovePlan{}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "MOVEMODELSCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: model, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, addr, "MOVEMODEL")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	id := godicom.NewDataset()
	id.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))

	if _, err := assoc.CMove(ctx, ae.MoveRequest{
		QueryModel:      model,
		MoveDestination: "SOMEWHERE",
		IdentifierData:  id,
	}); err != nil {
		t.Fatalf("C-MOVE: %v", err)
	}
	if gotCalled != 1 {
		t.Fatalf("handler called %d times, want 1", gotCalled)
	}
	if gotModel != model {
		t.Errorf("handler saw model %q", gotModel)
	}
	if gotLevel != "STUDY" {
		t.Errorf("handler saw level %q", gotLevel)
	}
}
