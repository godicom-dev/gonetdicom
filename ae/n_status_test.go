package ae_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

// nCommand is one N-* request built against an arbitrary SOP Class, plus the
// way to read its status back. Six services share one rule, and a table is the
// only way to keep them from drifting apart.
type nCommand struct {
	name   string
	encode func(t *testing.T, sopClass string) []byte
	status func(t *testing.T, cmd []byte) uint16
}

func nCommands() []nCommand {
	return []nCommand{
		{
			name: "N-GET",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NGetRQ{
					MessageID:               1,
					RequestedSOPClassUID:    sopClass,
					RequestedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-GET-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNGetRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-GET-RSP: %v", err)
				}
				return rsp.Status
			},
		},
		{
			name: "N-SET",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NSetRQ{
					MessageID:               1,
					RequestedSOPClassUID:    sopClass,
					RequestedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-SET-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNSetRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-SET-RSP: %v", err)
				}
				return rsp.Status
			},
		},
		{
			name: "N-CREATE",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NCreateRQ{
					MessageID:              1,
					AffectedSOPClassUID:    sopClass,
					AffectedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-CREATE-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNCreateRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-CREATE-RSP: %v", err)
				}
				return rsp.Status
			},
		},
		{
			name: "N-DELETE",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NDeleteRQ{
					MessageID:               1,
					RequestedSOPClassUID:    sopClass,
					RequestedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-DELETE-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNDeleteRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-DELETE-RSP: %v", err)
				}
				return rsp.Status
			},
		},
		{
			name: "N-ACTION",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NActionRQ{
					MessageID:               1,
					ActionTypeID:            1,
					RequestedSOPClassUID:    sopClass,
					RequestedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-ACTION-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNActionRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-ACTION-RSP: %v", err)
				}
				return rsp.Status
			},
		},
		{
			name: "N-EVENT-REPORT",
			encode: func(t *testing.T, sopClass string) []byte {
				t.Helper()
				cmd, err := (&dimse.NEventReportRQ{
					MessageID:              1,
					EventTypeID:            1,
					AffectedSOPClassUID:    sopClass,
					AffectedSOPInstanceUID: "1.2.3.4.5",
				}).Encode()
				if err != nil {
					t.Fatalf("encode N-EVENT-REPORT-RQ: %v", err)
				}
				return cmd
			},
			status: func(t *testing.T, cmd []byte) uint16 {
				t.Helper()
				rsp, err := dimse.DecodeNEventReportRSP(cmd)
				if err != nil {
					t.Fatalf("decode N-EVENT-REPORT-RSP: %v", err)
				}
				return rsp.Status
			},
		},
	}
}

// A message on the right presentation context but naming another SOP Class is
// the N-service form of the C-STORE mismatch: the answer is 0x0122 and no
// handler runs. Six services, one rule — hence one table.
func TestNServiceRejectsUnnegotiatedSOPClass(t *testing.T) {
	t.Parallel()

	const foreign = "1.2.3.4.5.6.7.8.9" // never proposed, so never accepted

	for _, tc := range nCommands() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var called atomic.Int32
			addr := serveSCP(t, ae.ServerConfig{
				AETitle:                  "NCLASSSGP",
				AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
				OnNGet: func(context.Context, ae.NGetRequest) ae.NGetResult {
					called.Add(1)
					return ae.NGetResult{Status: status.Success}
				},
				OnNSet: func(context.Context, ae.SetRequest) ae.SetResult {
					called.Add(1)
					return ae.SetResult{Status: status.Success}
				},
				OnNCreate: func(context.Context, ae.CreateRequest) ae.CreateResult {
					called.Add(1)
					return ae.CreateResult{Status: status.Success}
				},
				OnNDelete: func(context.Context, ae.DeleteRequest) ae.DeleteResult {
					called.Add(1)
					return ae.DeleteResult{Status: status.Success}
				},
				OnNAction: func(context.Context, ae.ActionRequest) (ae.ActionResult, *ae.EventReportRequest) {
					called.Add(1)
					return ae.ActionResult{Status: status.Success}, nil
				},
				OnNEventReport: func(context.Context, ae.EventReportRequest) uint16 {
					called.Add(1)
					return status.Success
				},
			})

			scu, ac := dialRawWith(t, addr, "NCLASSSGP",
				contextRQ(1, printManagementSOPClass, pdu.ImplicitVRLittleEndian),
			)
			requireContextResults(t, ac, map[byte]byte{1: pcAcceptance})

			sendOn(t, scu, 1, tc.encode(t, foreign), nil)
			cmd, err := scu.nextCommand(5 * time.Second)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if got := tc.status(t, cmd); got != status.SOPClassNotSupported {
				t.Fatalf("status=0x%04X, want 0x%04X", got, status.SOPClassNotSupported)
			}
			if n := called.Load(); n != 0 {
				t.Fatalf("handler ran %d times for an unnegotiated SOP Class", n)
			}
		})
	}
}

// A handler's status is the answer, including the failure codes it chose. The
// library must not rewrite 0x0112 into a generic processing failure: the peer's
// retry logic keys on the exact code.
func TestNGetReturnsHandlerStatus(t *testing.T) {
	t.Parallel()

	assoc, cancel := dialNService(t, ae.ServerConfig{
		AETitle:                  "NSTATUS",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNGet: func(context.Context, ae.NGetRequest) ae.NGetResult {
			return ae.NGetResult{Status: status.NoSuchObjectInstance}
		},
	})
	defer cancel()

	res, err := assoc.NGet(context.Background(), ae.NGetRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: "1.2.3.4.5",
	})
	if err != nil {
		t.Fatalf("N-GET: %v", err)
	}
	if res.Status != status.NoSuchObjectInstance {
		t.Fatalf("status=0x%04X, want 0x%04X", res.Status, status.NoSuchObjectInstance)
	}
}

// Warning statuses are not failures, and their attribute list still travels.
// A peer that treats 0xB000 as failure loses the reason it was warned about.
func TestNGetReturnsWarningWithAttributes(t *testing.T) {
	t.Parallel()

	assoc, cancel := dialNService(t, ae.ServerConfig{
		AETitle:                  "NWARN",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNGet: func(context.Context, ae.NGetRequest) ae.NGetResult {
			ds := godicom.NewDataset()
			ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "WARN^ONLY"))
			return ae.NGetResult{
				Status:                 status.CoercionOfDataElements,
				AffectedSOPClassUID:    printManagementSOPClass,
				AffectedSOPInstanceUID: "1.2.3.4.5",
				AttributeListData:      ds,
			}
		},
	})
	defer cancel()

	res, err := assoc.NGet(context.Background(), ae.NGetRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: "1.2.3.4.5",
	})
	if err != nil {
		t.Fatalf("N-GET: %v", err)
	}
	if res.Status != status.CoercionOfDataElements {
		t.Fatalf("status=0x%04X, want 0x%04X", res.Status, status.CoercionOfDataElements)
	}
	if res.AttributeListData == nil {
		t.Fatal("warning response lost its attribute list")
	}
	if v, _ := res.AttributeListData.GetString(godicom.MustTag("PatientName")); v != "WARN^ONLY" {
		t.Fatalf("PatientName=%q, want WARN^ONLY", v)
	}
}

// N-CREATE lets the SCP choose the instance UID when the request omits it. A
// handler that only sets Success must still produce a usable answer: leaving the
// UID empty would make the response unencodable.
func TestNCreateAssignsInstanceUIDWhenRequestOmitsIt(t *testing.T) {
	t.Parallel()

	assoc, cancel := dialNService(t, ae.ServerConfig{
		AETitle:                  "NCREATE",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNCreate: func(_ context.Context, req ae.CreateRequest) ae.CreateResult {
			if req.AffectedSOPInstanceUID != "" {
				t.Errorf("request carried instance UID %q, want empty", req.AffectedSOPInstanceUID)
			}
			return ae.CreateResult{Status: status.Success} // deliberately no UID
		},
	})
	defer cancel()

	res, err := assoc.NCreate(context.Background(), ae.CreateRequest{
		AffectedSOPClassUID: printManagementSOPClass,
	})
	if err != nil {
		t.Fatalf("N-CREATE: %v", err)
	}
	if res.Status != status.Success {
		t.Fatalf("status=0x%04X, want success", res.Status)
	}
	if res.AffectedSOPInstanceUID == "" {
		t.Fatal("SCP did not assign an instance UID")
	}
}

// The UID a failing handler never set must not be invented: a failure response
// naming an instance would tell the peer an object exists that does not.
func TestNCreateFailureCarriesNoInventedInstanceUID(t *testing.T) {
	t.Parallel()

	assoc, cancel := dialNService(t, ae.ServerConfig{
		AETitle:                  "NCREATEFAIL",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNCreate: func(context.Context, ae.CreateRequest) ae.CreateResult {
			return ae.CreateResult{Status: status.ProcessingFailure}
		},
	})
	defer cancel()

	res, err := assoc.NCreate(context.Background(), ae.CreateRequest{
		AffectedSOPClassUID: printManagementSOPClass,
	})
	if err != nil {
		t.Fatalf("N-CREATE: %v", err)
	}
	if res.Status != status.ProcessingFailure {
		t.Fatalf("status=0x%04X, want 0x%04X", res.Status, status.ProcessingFailure)
	}
	if res.AffectedSOPInstanceUID != "" {
		t.Fatalf("failure response carries instance UID %q, want none", res.AffectedSOPInstanceUID)
	}
}

// An N-ACTION reply dataset is the whole point of the service; dropping it turns
// a successful action into a useless acknowledgement.
func TestNActionReturnsReplyDataset(t *testing.T) {
	t.Parallel()

	assoc, cancel := dialNService(t, ae.ServerConfig{
		AETitle:                  "NACTION",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNAction: func(context.Context, ae.ActionRequest) (ae.ActionResult, *ae.EventReportRequest) {
			reply := godicom.NewDataset()
			reply.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, "1.2.3.4"))
			return ae.ActionResult{
				Status:                 status.Success,
				ActionTypeID:           1,
				AffectedSOPClassUID:    printManagementSOPClass,
				AffectedSOPInstanceUID: "1.2.3.4.5",
				ActionReplyData:        reply,
			}, nil
		},
	})
	defer cancel()

	res, err := assoc.NAction(context.Background(), ae.ActionRequest{
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: "1.2.3.4.5",
		ActionTypeID:            1,
	})
	if err != nil {
		t.Fatalf("N-ACTION: %v", err)
	}
	if res.Status != status.Success {
		t.Fatalf("status=0x%04X, want success", res.Status)
	}
	if res.ActionReplyData == nil {
		t.Fatal("action reply dataset did not arrive")
	}
	if v, _ := res.ActionReplyData.GetString(godicom.MustTag("StudyInstanceUID")); v != "1.2.3.4" {
		t.Fatalf("StudyInstanceUID=%q, want 1.2.3.4", v)
	}
}

// dialNService starts an SCP accepting the print management class and dials it,
// returning the association plus a cleanup that releases and shuts down.
func dialNService(t *testing.T, cfg ae.ServerConfig) (*ae.Association, context.CancelFunc) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServer := context.WithCancel(context.Background())
	go func() { _ = ae.Serve(serveCtx, ln, cfg) }()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "NSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   printManagementSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, ln.Addr().String(), cfg.AETitle)
	if err != nil {
		stopServer()
		_ = ln.Close()
		t.Fatalf("dial: %v", err)
	}

	shutdown := func() {
		_ = assoc.Release(context.Background())
		stopServer()
		_ = ln.Close()
	}
	var once sync.Once
	cleanup := func() { once.Do(shutdown) }
	t.Cleanup(cleanup)
	return assoc, cleanup
}
