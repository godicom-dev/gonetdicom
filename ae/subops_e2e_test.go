package ae_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// A C-GET SCP performs the C-STORE sub-operations itself, so the counts and the
// aggregate status are its own accounting (PS3.4 C.4.3.1.3.2). Decoding the
// C-STORE-RSP and throwing the status away meant a retrieval whose stores all
// failed answered 0x0000 with whatever counts the handler had guessed.
func TestCGetAggregatesSubOperationStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		storeStatuses  []uint16
		wantStatus     uint16
		wantCompleted  uint16
		wantFailed     uint16
		wantWarning    uint16
		wantFailedUIDs int
	}{
		{
			name:          "all stored",
			storeStatuses: []uint16{dimse.StatusSuccess, dimse.StatusSuccess},
			wantStatus:    dimse.StatusSuccess, wantCompleted: 2,
		},
		{
			name:          "one failed",
			storeStatuses: []uint16{dimse.StatusSuccess, 0xC211},
			wantStatus:    dimse.StatusWarning, wantCompleted: 1, wantFailed: 1,
			wantFailedUIDs: 1,
		},
		{
			name:          "all failed",
			storeStatuses: []uint16{0xC211, dcmstatus.OutOfResources},
			wantStatus:    dcmstatus.UnableToPerformSubOperations, wantFailed: 2,
			wantFailedUIDs: 2,
		},
		{
			name:          "all warned",
			storeStatuses: []uint16{0xB000, 0xB006},
			wantStatus:    dimse.StatusWarning, wantWarning: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctUID := string(uid.CTImageStorage)
			instances := make([]string, len(tc.storeStatuses))
			for i := range instances {
				instances[i] = fmt.Sprintf("1.2.826.0.1.3680043.10.541.1.%d", i+1)
			}

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
					AETitle: "GETSCP",
					AcceptedAbstractSyntaxes: []string{
						ae.PatientRootQueryRetrieveInformationModelGet,
						ctUID,
					},
					RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, true, true)},
					OnCGet: func(_ context.Context, _ ae.GetRequest) ae.GetPlan {
						plan := ae.GetPlan{}
						for _, inst := range instances {
							ds := godicom.NewDataset()
							ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
							ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, inst))
							ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "DOE^JOHN"))
							plan.Stores = append(plan.Stores, ae.StoreRequest{
								AffectedSOPClassUID:    ctUID,
								AffectedSOPInstanceUID: inst,
								Data:                   ds,
							})
						}
						// No Responses: the framework reports the outcome.
						return plan
					},
				})
			}()

			dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer dialCancel()

			assoc, err := ae.Dial(dialCtx, ae.Config{
				AETitle: "GETSCU",
				PresentationContexts: []ae.PresentationContext{
					{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
					{ID: 3, AbstractSyntax: ctUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
				},
				RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, false, true)},
			}, ln.Addr().String(), "GETSCP")
			if err != nil {
				t.Fatalf("dial: %v", err)
			}

			// Priority carries the instance index, so the C-STORE order and the
			// requested statuses line up.
			query := godicom.NewDataset()
			query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))

			matches, err := assoc.CGet(dialCtx, ae.GetRequest{
				QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
				IdentifierData: query,
				OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
					for i, inst := range instances {
						if ev.AffectedSOPInstanceUID == inst {
							return tc.storeStatuses[i]
						}
					}
					t.Errorf("unexpected instance %q", ev.AffectedSOPInstanceUID)
					return dimse.StatusSuccess
				},
			})
			if err != nil {
				t.Fatalf("C-GET: %v", err)
			}
			if len(matches) == 0 {
				t.Fatal("no responses")
			}

			final := matches[len(matches)-1]
			if final.Status != tc.wantStatus {
				t.Errorf("final status 0x%04X, want 0x%04X", final.Status, tc.wantStatus)
			}
			if got := final.SubOperations; got.Completed != tc.wantCompleted ||
				got.Failed != tc.wantFailed || got.Warning != tc.wantWarning ||
				got.Remaining != 0 || !got.Present {
				t.Errorf("counts=%+v, want completed=%d failed=%d warning=%d",
					got, tc.wantCompleted, tc.wantFailed, tc.wantWarning)
			}
			if n := len(failedUIDs(final.Identifier)); n != tc.wantFailedUIDs {
				t.Errorf("Failed SOP Instance UID List holds %d UIDs, want %d", n, tc.wantFailedUIDs)
			}

			if err := assoc.Release(dialCtx); err != nil {
				t.Fatalf("release: %v", err)
			}
			cancel()
			<-errCh
		})
	}
}

// The same accounting on the C-MOVE side, where the SCP is the SCU of the
// sub-operations. A warning used to be tallied as a failure, so a transfer that
// stored every instance raised as warning was reported as 0xA702 — "unable to
// perform sub-operations" for a transfer that in fact worked.
func TestCMoveAggregatesSubOperationStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		storeStatuses  []uint16
		wantStatus     uint16
		wantCompleted  uint16
		wantFailed     uint16
		wantWarning    uint16
		wantFailedUIDs int
	}{
		{
			name:          "all warned",
			storeStatuses: []uint16{0xB000},
			wantStatus:    dimse.StatusWarning, wantWarning: 1,
		},
		{
			name:          "all failed",
			storeStatuses: []uint16{0xC211},
			wantStatus:    dcmstatus.UnableToPerformSubOperations, wantFailed: 1,
			wantFailedUIDs: 1,
		},
		{
			name:          "stored and warned",
			storeStatuses: []uint16{dimse.StatusSuccess, 0xB000},
			wantStatus:    dimse.StatusWarning, wantCompleted: 1, wantWarning: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctUID := string(uid.CTImageStorage)
			instances := make([]string, len(tc.storeStatuses))
			for i := range instances {
				instances[i] = fmt.Sprintf("1.2.826.0.1.3680043.10.541.9.%d", i+1)
			}
			statusOf := make(map[string]uint16, len(instances))
			for i, inst := range instances {
				statusOf[inst] = tc.storeStatuses[i]
			}

			storeLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer storeLn.Close()

			moveLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer moveLn.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 2)
			go func() {
				errCh <- ae.Serve(ctx, storeLn, ae.ServerConfig{
					AETitle:                  "STORESCP",
					AcceptedAbstractSyntaxes: []string{ctUID},
					OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
						status, ok := statusOf[ev.AffectedSOPInstanceUID]
						if !ok {
							t.Errorf("unexpected instance %q", ev.AffectedSOPInstanceUID)
							return dimse.StatusSuccess
						}
						return status
					},
				})
			}()
			go func() {
				errCh <- ae.Serve(ctx, moveLn, ae.ServerConfig{
					AETitle:                  "MOVESCP",
					AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelMove},
					MoveDestinations: map[string]ae.MoveDestination{
						"STORESCP": {Addr: storeLn.Addr().String()},
					},
					OnCMove: func(_ context.Context, _ ae.MoveRequest) ae.MovePlan {
						plan := ae.MovePlan{}
						for _, inst := range instances {
							ds := godicom.NewDataset()
							ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
							ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, inst))
							plan.Stores = append(plan.Stores, ae.StoreRequest{
								AffectedSOPClassUID:    ctUID,
								AffectedSOPInstanceUID: inst,
								Data:                   ds,
							})
						}
						return plan
					},
				})
			}()

			dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer dialCancel()

			assoc, err := ae.Dial(dialCtx, ae.Config{
				AETitle: "MOVESCU",
				PresentationContexts: []ae.PresentationContext{{
					ID:               1,
					AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelMove,
					TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
				}},
			}, moveLn.Addr().String(), "MOVESCP")
			if err != nil {
				t.Fatalf("dial: %v", err)
			}

			query := godicom.NewDataset()
			query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))

			matches, err := assoc.CMove(dialCtx, ae.MoveRequest{
				QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
				MoveDestination: "STORESCP",
				IdentifierData:  query,
			})
			if err != nil {
				t.Fatalf("C-MOVE: %v", err)
			}
			if len(matches) < 2 {
				t.Fatalf("responses=%d", len(matches))
			}

			final := matches[len(matches)-1]
			if final.Status != tc.wantStatus {
				t.Errorf("final status 0x%04X, want 0x%04X", final.Status, tc.wantStatus)
			}
			if got := final.SubOperations; got.Completed != tc.wantCompleted ||
				got.Failed != tc.wantFailed || got.Warning != tc.wantWarning ||
				got.Remaining != 0 || !got.Present {
				t.Errorf("counts=%+v, want completed=%d failed=%d warning=%d",
					got, tc.wantCompleted, tc.wantFailed, tc.wantWarning)
			}
			if n := len(failedUIDs(final.Identifier)); n != tc.wantFailedUIDs {
				t.Errorf("Failed SOP Instance UID List holds %d UIDs, want %d", n, tc.wantFailedUIDs)
			}

			if err := assoc.Release(dialCtx); err != nil {
				t.Fatalf("release: %v", err)
			}
			cancel()
			<-errCh
			<-errCh
		})
	}
}

// failedUIDs reads the Failed SOP Instance UID List out of a retrieve response
// identifier. godicom hands a UI value list back either as a typed MultiValue or
// as the raw backslash-delimited string, so both shapes are split here.
func failedUIDs(ds *godicom.Dataset) []string {
	if ds == nil {
		return nil
	}
	elem, ok := ds.Get(godicom.MustTag("FailedSOPInstanceUIDList"))
	if !ok {
		return nil
	}
	var values []string
	switch v := elem.Value.(type) {
	case *godicom.MultiValue[string]:
		if v != nil {
			values = v.Values()
		}
	case *godicom.MultiValue[uid.UID]:
		if v != nil {
			for _, u := range v.Values() {
				values = append(values, string(u))
			}
		}
	case []string:
		values = v
	case string:
		values = []string{v}
	case uid.UID:
		values = []string{string(v)}
	}
	out := make([]string, 0, len(values))
	for _, s := range values {
		out = append(out, strings.Split(s, "\\")...)
	}
	return out
}
