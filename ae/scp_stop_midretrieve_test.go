package ae_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// dialRawSCUContexts is dialRawSCU with caller-chosen presentation contexts, for
// operations that need more than one — a C-GET carries its own model and the
// storage class it is about to receive instances of.
func dialRawSCUContexts(t *testing.T, addr, calledAE string, pcs []pdu.PresentationContextRQ) *rawSCU {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	called, err := pdu.PadAETitle(calledAE)
	if err != nil {
		t.Fatal(err)
	}
	calling, err := pdu.PadAETitle("RAWSCU")
	if err != nil {
		t.Fatal(err)
	}
	rq := &pdu.AAssociateRQ{
		CalledAETitle:          string(called[:]),
		CallingAETitle:         string(calling[:]),
		ApplicationContextName: pdu.ApplicationContextName,
		PresentationContexts:   pcs,
		UserInformation: pdu.UserInformation{
			MaxLength:              pdu.DefaultMaxPDULength,
			ImplementationClassUID: "1.2.826.0.1.3680043.10.541.3",
		},
	}
	if err := pdu.Write(conn, rq); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read AC: %v", err)
	}
	if _, ok := raw.(*pdu.AAssociateAC); !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", raw)
	}
	return &rawSCU{t: t, conn: conn}
}

// nextItem reads until either a control PDU or a complete command set arrives.
// Datasets are consumed but not returned: this peer answers sub-operations
// without looking at what they carry.
func (s *rawSCU) nextItem(timeout time.Duration) (pdu.PDU, []byte, error) {
	s.t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, err
	}
	defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()

	var cmd []byte
	for {
		raw, err := pdu.Read(s.conn)
		if err != nil {
			return nil, nil, err
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			return raw, nil, nil
		}
		for _, pdv := range p.PDVs {
			if !pdv.IsCommand() {
				continue
			}
			cmd = append(cmd, pdv.Fragment()...)
			if pdv.IsLast() {
				return nil, cmd, nil
			}
		}
	}
}

// getSCP serves C-GET with n one-instance sub-operations and answers the
// operation with Success once they are done.
func getSCP(t *testing.T, n int) string {
	t.Helper()
	return getSCPWith(t, n, []ae.RetrieveMatch{{Status: dimse.StatusSuccess}})
}

// getSCPWith is getSCP with the handler's own responses, for tests that need
// the operation to still be sending when they interrupt it.
func getSCPWith(t *testing.T, n int, responses []ae.RetrieveMatch) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
	})
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "GETSCP",
			AcceptedAbstractSyntaxes: []string{
				ae.PatientRootQueryRetrieveInformationModelGet,
				secondaryCaptureSOPClass,
			},
			OnCGet: func(_ context.Context, _ ae.GetRequest) ae.GetPlan {
				stores := make([]ae.StoreRequest, 0, n)
				for i := 0; i < n; i++ {
					stores = append(stores, ae.StoreRequest{
						AffectedSOPClassUID:    secondaryCaptureSOPClass,
						AffectedSOPInstanceUID: fmt.Sprintf("1.2.826.0.1.3680043.10.541.77.%d", i),
						Dataset:                []byte{0x08, 0x00, 0x05, 0x00, 0x43, 0x53, 0x00, 0x00},
					})
				}
				return ae.GetPlan{Stores: stores, Responses: responses}
			},
		})
	}()
	return ln.Addr().String()
}

func getSCU(t *testing.T, addr string) *rawSCU {
	t.Helper()
	return dialRawSCUContexts(t, addr, "GETSCP", []pdu.PresentationContextRQ{
		{ID: 1, AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
	})
}

func sendCGet(t *testing.T, scu *rawSCU, msgID uint16) {
	t.Helper()
	cmd, err := (&dimse.CGetRQ{
		MessageID:           msgID,
		AffectedSOPClassUID: ae.PatientRootQueryRetrieveInformationModelGet,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	// A C-GET identifier naming a patient; the handler ignores it.
	ident := []byte{0x08, 0x00, 0x05, 0x00, 0x50, 0x41, 0x54, 0x49, 0x45, 0x4e, 0x54, 0x00}
	scu.send(cmd, ident)
}

// storeResponse answers a C-STORE-RQ with Success.
func storeResponse(t *testing.T, scu *rawSCU, rq *dimse.CStoreRQ) {
	t.Helper()
	rsp, err := (&dimse.CStoreRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		AffectedSOPClassUID:       rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    rq.AffectedSOPInstanceUID,
		Status:                    dimse.StatusSuccess,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(rsp, nil)
}

// A peer that releases in the middle of a C-GET has stopped listening for
// sub-operations: PS3.8 7.2.2 lets it receive P-DATA while it waits for the
// release response and requires it to ignore it. The association must therefore
// be released. Before this, the A-RELEASE-RQ was reported as an unexpected PDU
// between two sub-operations and the connection was dropped without the
// A-RELEASE-RP the peer was waiting for.
func TestSCPReleasesMidCGet(t *testing.T) {
	t.Parallel()

	scu := getSCU(t, getSCP(t, 20))
	sendCGet(t, scu, 7)

	control, cmd, err := scu.nextItem(5 * time.Second)
	if err != nil {
		t.Fatalf("read first sub-operation: %v", err)
	}
	first, err := dimse.DecodeCStoreRQ(cmd)
	if control != nil || err != nil {
		t.Fatalf("first sub-operation: control=%T err=%v", control, err)
	}
	if first.MessageID != 1 {
		t.Fatalf("first C-STORE-RQ message id = %d, want 1", first.MessageID)
	}
	storeResponse(t, scu, first)

	if err := pdu.Write(scu.conn, &pdu.AReleaseRQ{}); err != nil {
		t.Fatalf("send A-RELEASE-RQ: %v", err)
	}

	// Anything already on the wire arrives first — a store the SCP had
	// committed to sending before it noticed. It is answered, because leaving
	// it unanswered would leave the SCP's read side one message behind.
	stores := 1
	for {
		control, cmd, err := scu.nextItem(10 * time.Second)
		if err != nil {
			t.Fatalf("after %d sub-operations: %v", stores, err)
		}
		if _, ok := control.(*pdu.AReleaseRP); ok {
			return
		}
		if control != nil {
			t.Fatalf("got %T, want A-RELEASE-RP", control)
		}
		rq, err := dimse.DecodeCStoreRQ(cmd)
		if err != nil {
			t.Fatalf("got a non-C-STORE message after the release: %v", err)
		}
		stores++
		if stores > 3 {
			t.Fatalf("SCP kept sending sub-operations after the release (%d so far)", stores)
		}
		storeResponse(t, scu, rq)
	}
}

// C-CANCEL that arrives between two sub-operations stops the operation where it
// is. Before, the cancel was decoded as a C-STORE-RSP by the store loop, which
// failed the association and left the SCU with neither a final response nor a
// usable connection.
func TestSCPCancelBetweenSubOperations(t *testing.T) {
	t.Parallel()

	scu := getSCU(t, getSCP(t, 5))
	sendCGet(t, scu, 9)

	control, cmd, err := scu.nextItem(5 * time.Second)
	if err != nil || control != nil {
		t.Fatalf("read first sub-operation: control=%T err=%v", control, err)
	}
	first, err := dimse.DecodeCStoreRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	storeResponse(t, scu, first)

	cancel, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 9}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(cancel, nil)

	stored := 1
	for {
		control, cmd, err := scu.nextItem(10 * time.Second)
		if err != nil {
			t.Fatalf("after %d sub-operations: %v", stored, err)
		}
		if control != nil {
			t.Fatalf("got %T, want a C-GET-RSP", control)
		}
		if rq, err := dimse.DecodeCStoreRQ(cmd); err == nil {
			stored++
			// A sub-operation already on the wire when the cancel arrived has
			// to be answered, so one more is legitimate; a fifth would mean the
			// cancel was ignored.
			if stored > 4 {
				t.Fatalf("SCP kept storing after the cancel (%d of 5)", stored)
			}
			storeResponse(t, scu, rq)
			continue
		}
		rsp, err := dimse.DecodeCGetRSP(cmd)
		if err != nil {
			t.Fatalf("decode final response: %v", err)
		}
		if rsp.Status != dimse.StatusCancel {
			t.Fatalf("final status 0x%04x, want 0x%04x (Cancel)", rsp.Status, dimse.StatusCancel)
		}
		// PS3.4 C.4.3.1.5: the untried sub-operations are remaining, and the
		// ones already stored are completed — not failed.
		if rsp.SubOperations.Completed != uint16(stored) {
			t.Fatalf("Completed = %d, want %d", rsp.SubOperations.Completed, stored)
		}
		if rsp.SubOperations.Remaining != uint16(5-stored) {
			t.Fatalf("Remaining = %d, want %d", rsp.SubOperations.Remaining, 5-stored)
		}
		if rsp.SubOperations.Failed != 0 {
			t.Fatalf("Failed = %d, want 0", rsp.SubOperations.Failed)
		}
		break
	}

	// The association survives: a cancel is not a reason to drop it.
	echo, err := (&dimse.CEchoRQ{MessageID: 33, AffectedSOPClassUID: pdu.VerificationSOPClass}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(echo, nil)
	control, cmd, err = scu.nextItem(5 * time.Second)
	if err != nil || control != nil {
		t.Fatalf("C-ECHO after cancel: control=%T err=%v", control, err)
	}
	if status, _ := dimse.PeekStatus(cmd); status != dimse.StatusSuccess {
		t.Fatalf("C-ECHO status 0x%04x after cancel, want Success", status)
	}
}

// A cancel can overtake the response to the sub-operation already in flight.
// That response is still owed and has to be consumed before the operation ends,
// or every later message on the association would be read one behind.
func TestSCPCancelOvertakesStoreResponse(t *testing.T) {
	t.Parallel()

	scu := getSCU(t, getSCP(t, 5))
	sendCGet(t, scu, 11)

	control, cmd, err := scu.nextItem(5 * time.Second)
	if err != nil || control != nil {
		t.Fatalf("read first sub-operation: control=%T err=%v", control, err)
	}
	rq, err := dimse.DecodeCStoreRQ(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 11}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(cancel, nil)
	storeResponse(t, scu, rq)

	control, cmd, err = scu.nextItem(10 * time.Second)
	if err != nil || control != nil {
		t.Fatalf("final response: control=%T err=%v", control, err)
	}
	if _, err := dimse.DecodeCStoreRQ(cmd); err == nil {
		t.Fatal("SCP sent another sub-operation after the cancel")
	}
	final, err := dimse.DecodeCGetRSP(cmd)
	if err != nil {
		t.Fatalf("decode final response: %v", err)
	}
	if final.Status != dimse.StatusCancel {
		t.Fatalf("final status 0x%04x, want 0x%04x (Cancel)", final.Status, dimse.StatusCancel)
	}
	// The store that was in flight completed, so it counts — a cancel stops the
	// work, it does not undo it.
	if final.SubOperations.Completed != 1 {
		t.Fatalf("Completed = %d, want 1", final.SubOperations.Completed)
	}
	if final.SubOperations.Remaining != 4 {
		t.Fatalf("Remaining = %d, want 4", final.SubOperations.Remaining)
	}
}

// C-FIND streams pending responses from the handler and polls between them; a
// release there must be answered the same way as anywhere else.
func TestSCPReleasesDuringCFind(t *testing.T) {
	t.Parallel()

	scu := dialRawSCU(t, findSCP(t, 20000), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 47)
	scu.send(cmd, ident)
	if _, err := scu.nextCommand(5 * time.Second); err != nil {
		t.Fatalf("first response: %v", err)
	}

	if err := pdu.Write(scu.conn, &pdu.AReleaseRQ{}); err != nil {
		t.Fatalf("send A-RELEASE-RQ: %v", err)
	}
	// Responses already written before the release reached the SCP are in the
	// socket buffer and arrive first: that is not the SCP ignoring us. The
	// bound is therefore on bytes in flight (2000 responses of ~50 bytes is
	// more than any socket buffer) rather than on scheduling luck — a smaller
	// one failed on CI runners that had ~20 queued.
	for seen := 0; ; {
		control, cmd, err := scu.nextItem(10 * time.Second)
		if err != nil {
			t.Fatalf("await A-RELEASE-RP: %v", err)
		}
		if _, ok := control.(*pdu.AReleaseRP); ok {
			return
		}
		if control != nil {
			t.Fatalf("got %T, want A-RELEASE-RP", control)
		}
		seen++
		if seen > 2000 {
			t.Fatalf("SCP kept matching after the release (%d responses)", seen)
		}
		if status, ok := dimse.PeekStatus(cmd); !ok || !dimse.IsPending(status) {
			t.Fatalf("response after the release has status 0x%04x, want Pending or silence", status)
		}
	}
}
