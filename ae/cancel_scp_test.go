package ae_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// rawSCU is a hand-rolled SCU that writes PDUs directly, so a test can control
// how a message is split across TCP segments.
type rawSCU struct {
	t    *testing.T
	conn net.Conn
}

func dialRawSCU(t *testing.T, addr, calledAE, abstractSyntax string) *rawSCU {
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
		PresentationContexts: []pdu.PresentationContextRQ{{
			ID:               1,
			AbstractSyntax:   abstractSyntax,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
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

func (s *rawSCU) send(command, dataset []byte) {
	s.t.Helper()
	pdus, err := pdu.FragmentMessage(1, command, dataset, pdu.DefaultMaxPDULength)
	if err != nil {
		s.t.Fatalf("fragment: %v", err)
	}
	for _, p := range pdus {
		if err := pdu.Write(s.conn, p); err != nil {
			s.t.Fatalf("write: %v", err)
		}
	}
}

// sendSplit writes command as one PDU cut in two TCP segments with a gap in
// between. Any reader that treats "nothing more has arrived yet" as "no message"
// loses the tail — which is exactly what a short read deadline on the raw
// connection used to do.
func (s *rawSCU) sendSplit(command []byte, gap time.Duration) {
	s.t.Helper()
	pdus, err := pdu.FragmentMessage(1, command, nil, pdu.DefaultMaxPDULength)
	if err != nil {
		s.t.Fatalf("fragment: %v", err)
	}
	if len(pdus) != 1 {
		s.t.Fatalf("expected a single PDU, got %d", len(pdus))
	}
	b, err := pdus[0].Encode()
	if err != nil {
		s.t.Fatalf("encode: %v", err)
	}
	if _, err := s.conn.Write(b[:6]); err != nil {
		s.t.Fatalf("write header: %v", err)
	}
	time.Sleep(gap)
	if _, err := s.conn.Write(b[6:]); err != nil {
		s.t.Fatalf("write body: %v", err)
	}
}

// nextPDU reads one raw PDU, whatever its type.
func (s *rawSCU) nextPDU(timeout time.Duration) (pdu.PDU, error) {
	s.t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()
	return pdu.Read(s.conn)
}

// nextCommand reads PDUs until a complete command set arrives, returning it.
func (s *rawSCU) nextCommand(timeout time.Duration) ([]byte, error) {
	s.t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()

	var cmd []byte
	for {
		raw, err := pdu.Read(s.conn)
		if err != nil {
			return nil, err
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			return nil, fmt.Errorf("unexpected PDU %T", raw)
		}
		for _, pdv := range p.PDVs {
			if !pdv.IsCommand() {
				continue
			}
			cmd = append(cmd, pdv.Fragment()...)
			if pdv.IsLast() {
				return cmd, nil
			}
		}
	}
}

// findSCP starts a C-FIND SCP that answers with n pending matches followed by
// Success, and returns its address.
func findSCP(t *testing.T, n int) string {
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
			AETitle:                  "CANCELSCP",
			AcceptedAbstractSyntaxes: []string{ae.PatientRootQueryRetrieveInformationModelFind},
			OnCFind: func(_ context.Context, _ ae.FindRequest) []ae.FindMatch {
				out := make([]ae.FindMatch, 0, n+1)
				for i := 0; i < n; i++ {
					ident := godicom.NewDataset()
					ident.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
					ident.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, fmt.Sprintf("P%04d", i)))
					out = append(out, ae.FindMatch{Status: dimse.StatusPending, Identifier: ident})
				}
				return append(out, ae.FindMatch{Status: dimse.StatusSuccess})
			},
		})
	}()
	return ln.Addr().String()
}

func findRQCommand(t *testing.T, msgID uint16) ([]byte, []byte) {
	t.Helper()
	query := godicom.NewDataset()
	query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
	query.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "*"))
	ident, err := query.Encode(pdu.ImplicitVRLittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := (&dimse.CFindRQ{
		MessageID:           msgID,
		AffectedSOPClassUID: ae.PatientRootQueryRetrieveInformationModelFind,
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	return cmd, ident
}

// A C-CANCEL-RQ split across TCP segments must still be honoured. Peeking for it
// straight off the connection under a short deadline consumed the first segment
// and threw it away, so the cancel was silently lost.
func TestSCPHonoursCCancelSplitAcrossSegments(t *testing.T) {
	t.Parallel()

	scu := dialRawSCU(t, findSCP(t, 20000), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 7)
	scu.send(cmd, ident)

	// Wait for the stream to be under way before cancelling.
	if _, err := scu.nextCommand(5 * time.Second); err != nil {
		t.Fatalf("first response: %v", err)
	}

	cancelCmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 7}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.sendSplit(cancelCmd, 30*time.Millisecond)

	deadline := time.Now().Add(10 * time.Second)
	pendings := 0
	for time.Now().Before(deadline) {
		rsp, err := scu.nextCommand(10 * time.Second)
		if err != nil {
			t.Fatalf("after %d pendings: %v", pendings, err)
		}
		st, ok := dimse.PeekStatus(rsp)
		if !ok {
			t.Fatalf("response %d carries no status", pendings)
		}
		if dimse.IsPending(st) {
			pendings++
			continue
		}
		if st != dimse.StatusCancel {
			t.Fatalf("final status 0x%04x after %d pendings, want 0x%04x (Cancel)",
				st, pendings, dimse.StatusCancel)
		}
		return
	}
	t.Fatalf("no final response within the deadline (%d pendings seen)", pendings)
}

// After a split C-CANCEL-RQ the association must still be usable: the old peek
// left the connection desynchronised, so the next message's length prefix was
// read out of the middle of a PDU and the SCP blocked forever.
func TestSCPAssociationUsableAfterSplitCCancel(t *testing.T) {
	t.Parallel()

	scu := dialRawSCU(t, findSCP(t, 20000), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 11)
	scu.send(cmd, ident)
	if _, err := scu.nextCommand(5 * time.Second); err != nil {
		t.Fatalf("first response: %v", err)
	}

	cancelCmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 11}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.sendSplit(cancelCmd, 30*time.Millisecond)

	// Drain to the final C-FIND-RSP.
	for {
		rsp, err := scu.nextCommand(10 * time.Second)
		if err != nil {
			t.Fatalf("draining C-FIND: %v", err)
		}
		if st, ok := dimse.PeekStatus(rsp); ok && !dimse.IsPending(st) {
			break
		}
	}

	// The association must still answer an ordinary C-ECHO.
	echo, err := (&dimse.CEchoRQ{MessageID: 12, AffectedSOPClassUID: pdu.VerificationSOPClass}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	scu.send(echo, nil)
	rsp, err := scu.nextCommand(5 * time.Second)
	if err != nil {
		t.Fatalf("C-ECHO after cancel: %v", err)
	}
	if st, ok := dimse.PeekStatus(rsp); !ok || st != dimse.StatusSuccess {
		t.Fatalf("C-ECHO status=0x%04x ok=%v, want Success", st, ok)
	}
}

// Checking for a cancel must not cost wall-clock per response. The old peek
// armed a 2 ms read deadline before every match, so a large result set paid
// 2 ms × matches on top of the actual work.
func TestSCPFindStreamsWithoutPerMatchDelay(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	t.Parallel()

	const matches = 600
	const budget = 1200 * time.Millisecond // old cost: >= 2ms * 600 in deadlines alone

	scu := dialRawSCU(t, findSCP(t, matches), "CANCELSCP", ae.PatientRootQueryRetrieveInformationModelFind)
	cmd, ident := findRQCommand(t, 3)

	start := time.Now()
	scu.send(cmd, ident)
	seen := 0
	for {
		rsp, err := scu.nextCommand(30 * time.Second)
		if err != nil {
			t.Fatalf("after %d responses: %v", seen, err)
		}
		st, ok := dimse.PeekStatus(rsp)
		if !ok {
			t.Fatalf("response %d carries no status", seen)
		}
		if !dimse.IsPending(st) {
			break
		}
		seen++
	}
	elapsed := time.Since(start)

	if seen != matches {
		t.Fatalf("got %d pending responses, want %d", seen, matches)
	}
	if elapsed > budget {
		t.Fatalf("%d matches took %v (%.2f ms each), budget %v",
			matches, elapsed, float64(elapsed.Microseconds())/float64(matches)/1000, budget)
	}
}
