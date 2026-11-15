package ae_test

import (
	"context"
	"errors"
	"iter"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// explicitVR is stated as a literal because pdu names only the implicit one:
// the two are the pair that negotiates for each other, which is the whole point
// of these tests.
const explicitVR = "1.2.840.10008.1.2.1"

// captureDataset encodes a small dataset in ts, for the pre-encoded paths.
func captureDataset(t *testing.T, ts string) []byte {
	t.Helper()
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, secondaryCaptureSOPClass))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5.60"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "SYNTAX^TEST"))
	raw, err := ds.Encode(godicom.UID(ts))
	if err != nil {
		t.Fatalf("encode %s: %v", ts, err)
	}
	return raw
}

// dialStoreSyntaxes dials the SCP offering one storage context with the given
// syntaxes, in the order a requestor that can accept either would list them.
func dialStoreSyntaxes(t *testing.T, addr string, syntaxes ...string) *ae.Association {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "SYNTAXSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   secondaryCaptureSOPClass,
			TransferSyntaxes: syntaxes,
		}},
	}, addr, "SYNTAXSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = assoc.Close() })
	return assoc
}

// The bug this refuses: the acceptor offers only Explicit VR Little Endian, so
// an Implicit VR context is "convertible" and contextForStore picks it. The
// bytes are Implicit VR, and sending them on a context that announces Explicit
// VR means the receiver parses a length prefix as a VR — a corrupt instance
// stored with a Success, which is worse than a failed store.
func TestCStoreRefusesBytesInAnotherSyntax(t *testing.T) {
	t.Parallel()

	reached := make(chan struct{}, 1)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SYNTAXSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		AcceptedTransferSyntaxes: []string{explicitVR},
		OnCStore: func(_ context.Context, _ ae.StoreEvent) uint16 {
			reached <- struct{}{}
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSyntaxes(t, addr, explicitVR)
	_, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.60",
		Dataset:                captureDataset(t, pdu.ImplicitVRLittleEndian),
		TransferSyntax:         pdu.ImplicitVRLittleEndian,
	})
	if !errors.Is(err, ae.ErrTransferSyntaxMismatch) {
		t.Fatalf("err=%v, want ErrTransferSyntaxMismatch", err)
	}
	select {
	case <-reached:
		t.Fatal("a store the library refused still reached the peer")
	case <-time.After(100 * time.Millisecond):
	}
}

// A godicom Dataset is the answer to the refusal above: it is encoded into
// whatever the context declares, so the conversion happens on the way out
// rather than being refused.
func TestCStoreEncodesDataIntoTheNegotiatedSyntax(t *testing.T) {
	t.Parallel()

	type seen struct {
		ts   string
		name string
	}
	got := make(chan seen, 1)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SYNTAXSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		AcceptedTransferSyntaxes: []string{explicitVR},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			ds, err := ev.Decode()
			if err != nil {
				t.Errorf("decode: %v", err)
				return dcmstatus.CannotUnderstand
			}
			name, _ := ds.GetString(godicom.MustTag("PatientName"))
			got <- seen{ts: ev.TransferSyntax, name: name}
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSyntaxes(t, addr, explicitVR)
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, secondaryCaptureSOPClass))
	ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, "1.2.3.4.5.61"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "SYNTAX^TEST"))
	res, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.61",
		Data:                   ds,
	})
	if err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status %#x", res.Status)
	}
	s := <-got
	if s.ts != explicitVR {
		t.Fatalf("store arrived as %s, want %s", s.ts, explicitVR)
	}
	if s.name != "SYNTAX^TEST" {
		t.Fatalf("receiver read PatientName %q", s.name)
	}
}

// The ordinary case still works: bytes and context agree, so the bytes go out
// untouched and the receiver reads them under the syntax it was told.
func TestCStoreSendsBytesOnAMatchingContext(t *testing.T) {
	t.Parallel()

	got := make(chan string, 1)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SYNTAXSCP",
		MaxDatasetSize:           8 << 20,
		AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			ds, err := ev.Decode()
			if err != nil {
				t.Errorf("decode: %v", err)
				return dcmstatus.CannotUnderstand
			}
			name, _ := ds.GetString(godicom.MustTag("PatientName"))
			got <- ev.TransferSyntax + " " + name
			return dimse.StatusSuccess
		},
	})

	assoc := dialStoreSyntaxes(t, addr, pdu.ImplicitVRLittleEndian)
	if _, err := assoc.CStore(context.Background(), ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.62",
		Dataset:                captureDataset(t, pdu.ImplicitVRLittleEndian),
		TransferSyntax:         pdu.ImplicitVRLittleEndian,
	}); err != nil {
		t.Fatalf("CStore: %v", err)
	}
	if s := <-got; s != pdu.ImplicitVRLittleEndian+" SYNTAX^TEST" {
		t.Fatalf("received %q", s)
	}
}

// startGetSCPOffering is startGetSCP with the storage syntaxes the requestor
// proposes spelled out, since the C-GET SCP chooses among them.
func startGetSCPOffering(t *testing.T, handler ae.GetHandler, storeSyntaxes ...string) *ae.Association {
	t.Helper()

	ctUID := string(uid.CTImageStorage)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "GETSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelGet,
				ctUID,
			},
			AcceptedTransferSyntaxes: storeSyntaxes,
			RoleSelections:           []pdu.RoleSelection{ae.BuildRole(ctUID, true, true)},
			OnCGet:                   handler,
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return")
		}
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "GETSCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: ctUID, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 5, AbstractSyntax: ctUID, TransferSyntaxes: []string{explicitVR}},
		},
		RoleSelections: []pdu.RoleSelection{ae.BuildRole(ctUID, false, true)},
	}, ln.Addr().String(), "GETSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = assoc.Abort() })
	return assoc
}

// A C-GET store is sent on the context that carries its syntax, not on
// whichever context the accepted map happens to yield first. Before this, an
// association proposing one SOP Class in two storage syntaxes sent the stores
// in a syntax that varied between runs.
func TestCGetStoresOnTheSyntaxTheHandlerDeclared(t *testing.T) {
	t.Parallel()

	ctUID := string(uid.CTImageStorage)
	got := make(chan string, 1)
	assoc := startGetSCPOffering(t, func(_ context.Context, _ ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil) {
				return
			}
			yield(&ae.RetrieveOp{Store: &ae.StoreRequest{
				AffectedSOPClassUID:    ctUID,
				AffectedSOPInstanceUID: "1.2.3.4.5.63",
				Dataset:                captureDataset(t, explicitVR),
				TransferSyntax:         explicitVR,
			}}, nil)
		}
	}, pdu.ImplicitVRLittleEndian, explicitVR)

	matches := cget(t, assoc, func(_ context.Context, ev ae.StoreEvent) uint16 {
		ds, err := ev.Decode()
		if err != nil {
			t.Errorf("decode: %v", err)
			return dcmstatus.CannotUnderstand
		}
		name, _ := ds.GetString(godicom.MustTag("PatientName"))
		got <- ev.TransferSyntax + " " + name
		return dimse.StatusSuccess
	})
	if last := matches[len(matches)-1]; last.Status != dimse.StatusSuccess {
		t.Fatalf("final status %#x", last.Status)
	}
	if s := <-got; s != explicitVR+" SYNTAX^TEST" {
		t.Fatalf("store arrived as %q, want the declared explicit syntax", s)
	}
}

// A store whose syntax no accepted context carries cannot be performed. It is
// counted failed — PS3.4 Table C.4-3 has the SCP report the instance the peer
// may retry — and the association survives it, which is the part that used to
// be a dead connection.
func TestCGetCountsAStoreWithNoContextAsFailed(t *testing.T) {
	t.Parallel()

	ctUID := string(uid.CTImageStorage)
	jpeg := "1.2.840.10008.1.2.4.50"
	// The first C-GET offers a store in a syntax the requestor never proposed;
	// the second offers one it did. The same association serves both, which is
	// the statement that a sub-operation no context can carry is accounted for
	// rather than turned into a broken connection.
	calls := 0
	assoc := startGetSCPOffering(t, func(_ context.Context, _ ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
		return func(yield func(*ae.RetrieveOp, error) bool) {
			calls++
			if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: 1}}, nil) {
				return
			}
			if calls == 1 {
				yield(&ae.RetrieveOp{Store: &ae.StoreRequest{
					AffectedSOPClassUID:    ctUID,
					AffectedSOPInstanceUID: "1.2.3.4.5.64",
					Dataset:                []byte("not a dataset in any proposed syntax"),
					TransferSyntax:         jpeg,
				}}, nil)
				return
			}
			yield(&ae.RetrieveOp{Store: &ae.StoreRequest{
				AffectedSOPClassUID:    ctUID,
				AffectedSOPInstanceUID: "1.2.3.4.5.65",
				Data:                   getStoreRequest("1.2.3.4.5.65").Data,
			}}, nil)
		}
	}, pdu.ImplicitVRLittleEndian)

	delivered := 0
	matches := cget(t, assoc, func(_ context.Context, _ ae.StoreEvent) uint16 {
		delivered++
		return dimse.StatusSuccess
	})
	last := matches[len(matches)-1]
	if dimse.IsPending(last.Status) {
		t.Fatalf("operation left open: %#x", last.Status)
	}
	if last.Status == dimse.StatusSuccess {
		t.Fatalf("status %#x, want a failure the peer can see", last.Status)
	}
	if last.SubOperations.Failed == 0 {
		t.Fatalf("failed count %d, want 1", last.SubOperations.Failed)
	}
	if delivered != 0 {
		t.Fatalf("%d stores delivered, want none", delivered)
	}

	// Same association, second C-GET: the failure did not end it.
	second := cget(t, assoc, func(_ context.Context, _ ae.StoreEvent) uint16 {
		delivered++
		return dimse.StatusSuccess
	})
	if last := second[len(second)-1]; last.Status != dimse.StatusSuccess {
		t.Fatalf("second C-GET status %#x", last.Status)
	}
	if delivered != 1 {
		t.Fatalf("%d stores delivered after the recovery, want 1", delivered)
	}
}
