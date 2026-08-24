package ae_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// storedInstance is one C-STORE as the SCP saw it: the UID in the command set,
// the UID inside the dataset that came with it, and a field the SCU changes
// between stores so a stale copy is visible.
type storedInstance struct {
	commandUID string
	datasetUID string
	patientID  string
}

// CStore mints a SOP Instance UID when the caller supplies none. It used to write
// that UID into the caller's Dataset, which is an input, not scratch space: the
// next store of the same Dataset found the previous store's UID and reused it. A
// caller that fills one Dataset per instance — the usual shape for a converter or
// a modality simulator — sent every instance under a single identity, and an
// archive keyed by SOP Instance UID keeps one of them.
func TestCStoreDoesNotMutateCallerDataset(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		stored []storedInstance
	)
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
			AETitle:                  "UIDSCP",
			AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				got := storedInstance{commandUID: req.AffectedSOPInstanceUID}
				if req.Data != nil {
					got.datasetUID, _ = req.Data.GetString(godicom.MustTag("SOPInstanceUID"))
					got.patientID, _ = req.Data.GetString(godicom.MustTag("PatientID"))
				}
				mu.Lock()
				stored = append(stored, got)
				mu.Unlock()
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "UIDSCU",
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "UIDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// One Dataset, reused for two instances the way a caller streaming a series
	// would: no SOPInstanceUID, per-instance fields rewritten between stores.
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "Tube^HeNe"))

	var sent []string
	for _, patientID := range []string{"Test1101", "Test1102"} {
		ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, patientID))
		res, err := assoc.CStore(dialCtx, ae.StoreRequest{
			AffectedSOPClassUID: secondaryCaptureSOPClass,
			Data:                ds,
		})
		if err != nil {
			t.Fatalf("C-STORE %s: %v", patientID, err)
		}
		if res.Status != dimse.StatusSuccess {
			t.Fatalf("C-STORE %s: status 0x%04x", patientID, res.Status)
		}
		sent = append(sent, res.AffectedSOPInstanceUID)
	}

	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh

	// The caller's Dataset is theirs: it had no SOP Instance UID going in and must
	// have none coming out, whatever CStore needed for the wire.
	if got, ok := ds.GetString(godicom.MustTag("SOPInstanceUID")); ok {
		t.Errorf("caller Dataset gained SOPInstanceUID %q", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stored) != 2 {
		t.Fatalf("SCP saw %d stores, want 2", len(stored))
	}
	if stored[0].commandUID == stored[1].commandUID {
		t.Errorf("both instances stored as %q: the second overwrites the first", stored[0].commandUID)
	}
	for i, want := range []string{"Test1101", "Test1102"} {
		if stored[i].commandUID == "" {
			t.Errorf("store %d: no Affected SOP Instance UID", i)
		}
		// Command set and dataset have to name the same instance, or the SCP files
		// the data under a UID the data itself contradicts.
		if stored[i].datasetUID != stored[i].commandUID {
			t.Errorf("store %d: dataset SOPInstanceUID %q, command %q",
				i, stored[i].datasetUID, stored[i].commandUID)
		}
		if stored[i].commandUID != sent[i] {
			t.Errorf("store %d: SCP saw %q, C-STORE-RSP reported %q", i, stored[i].commandUID, sent[i])
		}
		// The copy has to be of the Dataset as it is now, not one taken earlier.
		if stored[i].patientID != want {
			t.Errorf("store %d: PatientID %q, want %q", i, stored[i].patientID, want)
		}
	}
}
