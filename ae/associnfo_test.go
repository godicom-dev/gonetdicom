package ae_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestAssocFromContextAbsent(t *testing.T) {
	t.Parallel()

	if _, ok := ae.AssocFromContext(context.Background()); ok {
		t.Fatal("expected ok=false for a context without AssocInfo")
	}
}

// Every DIMSE handler runs with the association context attached: peer AE
// titles from the A-ASSOCIATE-RQ, the remote address, and per message the
// presentation context ID and its negotiated transfer syntax (issue #50).
func TestServeCStoreAssocContext(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{secondaryCaptureSOPClass},
			OnCStore: func(hctx context.Context, ev ae.StoreEvent) uint16 {
				info, ok := ae.AssocFromContext(hctx)
				if !ok {
					t.Error("AssocFromContext: not attached")
					return dimse.StatusSuccess
				}
				if info.CallingAETitle != "STORESCU" {
					t.Errorf("CallingAETitle=%q", info.CallingAETitle)
				}
				if info.CalledAETitle != "STORESCP" {
					t.Errorf("CalledAETitle=%q", info.CalledAETitle)
				}
				if info.RemoteAddr == nil {
					t.Error("RemoteAddr is nil")
				} else if _, _, err := net.SplitHostPort(info.RemoteAddr.String()); err != nil {
					t.Errorf("RemoteAddr=%q: %v", info.RemoteAddr, err)
				}
				if info.PresentationContextID != 1 {
					t.Errorf("PresentationContextID=%d", info.PresentationContextID)
				}
				if info.TransferSyntax != pdu.ImplicitVRLittleEndian {
					t.Errorf("TransferSyntax=%q", info.TransferSyntax)
				}
				if info.AbstractSyntax != secondaryCaptureSOPClass {
					t.Errorf("AbstractSyntax=%q", info.AbstractSyntax)
				}
				if info.RemoteImplementationClassUID != ae.ImplementationClassUID {
					t.Errorf("RemoteImplementationClassUID=%q", info.RemoteImplementationClassUID)
				}
				if info.RemoteImplementationVersionName != ae.ImplementationVersionName {
					t.Errorf("RemoteImplementationVersionName=%q", info.RemoteImplementationVersionName)
				}
				calls.Add(1)
				return dimse.StatusSuccess
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "STORESCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	res, err := assoc.CStore(dialCtx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls: %d", calls.Load())
	}
	cancel()
	<-errCh
}

// The context is attached per dispatch, not per service: a C-FIND handler
// sees the same snapshot as a C-STORE handler, with the abstract syntax of
// the query model it was dispatched on.
func TestServeCFindAssocContext(t *testing.T) {
	t.Parallel()

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
			AETitle:                  "FINDSCP",
			AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelFind},
			OnCFind: func(hctx context.Context, _ ae.FindRequest) []ae.FindMatch {
				info, ok := ae.AssocFromContext(hctx)
				if !ok {
					t.Error("AssocFromContext: not attached")
				}
				if info.CallingAETitle != "FINDSCU" || info.CalledAETitle != "FINDSCP" {
					t.Errorf("AE titles: %q -> %q", info.CallingAETitle, info.CalledAETitle)
				}
				if info.AbstractSyntax != ae.PatientRootQueryRetrieveInformationModelFind {
					t.Errorf("AbstractSyntax=%q", info.AbstractSyntax)
				}
				if info.TransferSyntax != pdu.ImplicitVRLittleEndian {
					t.Errorf("TransferSyntax=%q", info.TransferSyntax)
				}
				if info.PresentationContextID != 1 {
					t.Errorf("PresentationContextID=%d", info.PresentationContextID)
				}
				return []ae.FindMatch{{Status: dimse.StatusSuccess}}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "FINDSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.PatientRootQueryRetrieveInformationModelFind,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), "FINDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
	if _, err := assoc.CFind(dialCtx, ae.FindRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
		IdentifierData: query,
	}); err != nil {
		t.Fatalf("C-FIND: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}

// The SCU-side C-GET sub-operation store handler is the same StoreHandler and
// gets the same AssocInfo contract: the association's own AE titles, and the
// peer's address and implementation identification from the A-ASSOCIATE-AC.
func TestCGetStoreAssocContext(t *testing.T) {
	t.Parallel()

	ctUID := string(uid.CTImageStorage)
	sopInstance := "1.2.840.10008.1.2.3.4.5"

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
				ds := godicom.NewDataset()
				ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, ctUID))
				ds.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))
				return ae.GetPlan{
					Stores: []ae.StoreRequest{{
						AffectedSOPClassUID:    ctUID,
						AffectedSOPInstanceUID: sopInstance,
						Data:                   ds,
					}},
					Responses: []ae.RetrieveMatch{{
						Status:        dimse.StatusSuccess,
						SubOperations: dimse.SubOperations{Completed: 1, Present: true},
					}},
				}
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	var got atomic.Int32
	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "IMAGE"))
	query.Set(godicom.NewDataElement(godicom.MustTag("SOPInstanceUID"), godicom.VRUI, sopInstance))

	if _, err := assoc.CGet(dialCtx, ae.GetRequest{
		QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
		IdentifierData: query,
		OnCStore: func(hctx context.Context, ev ae.StoreEvent) uint16 {
			info, ok := ae.AssocFromContext(hctx)
			if !ok {
				t.Error("AssocFromContext: not attached to the sub-operation store")
			}
			// The association's requestor is this SCU, so the AE titles are
			// local -> peer, and the peer details come from the AC.
			if info.CallingAETitle != "GETSCU" || info.CalledAETitle != "GETSCP" {
				t.Errorf("AE titles: %q -> %q", info.CallingAETitle, info.CalledAETitle)
			}
			if info.RemoteAddr == nil {
				t.Error("RemoteAddr is nil")
			}
			if info.RemoteImplementationClassUID != ae.ImplementationClassUID {
				t.Errorf("RemoteImplementationClassUID=%q", info.RemoteImplementationClassUID)
			}
			if info.RemoteImplementationVersionName != ae.ImplementationVersionName {
				t.Errorf("RemoteImplementationVersionName=%q", info.RemoteImplementationVersionName)
			}
			if info.AbstractSyntax != ctUID {
				t.Errorf("AbstractSyntax=%q", info.AbstractSyntax)
			}
			if info.TransferSyntax != pdu.ImplicitVRLittleEndian {
				t.Errorf("TransferSyntax=%q", info.TransferSyntax)
			}
			got.Add(1)
			return dimse.StatusSuccess
		},
	}); err != nil {
		t.Fatalf("C-GET: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("sub-operation stores: %d", got.Load())
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}
