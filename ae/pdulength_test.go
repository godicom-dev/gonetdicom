package ae_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

// dialRawMaxPDU opens an association announcing maxLength as the most this
// requestor can receive, which is the only thing that governs how the acceptor
// fragments its replies.
func dialRawMaxPDU(t *testing.T, addr, calledAE string, maxLength uint32, ctxs ...pdu.PresentationContextRQ) *rawSCU {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rq := acceptRQ(calledAE, ctxs...)
	rq.UserInformation.MaxLength = maxLength
	if err := pdu.Write(conn, rq); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read AC: %v", err)
	}
	if _, ok := raw.(*pdu.AAssociateAC); !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", raw)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return &rawSCU{t: t, conn: conn}
}

// readCommandAndDataset reads P-DATA PDUs until both the command set and, when
// the command announces one, its dataset have arrived. It returns the PDUs and
// the reassembled dataset.
func readCommandAndDataset(t *testing.T, s *rawSCU, timeout time.Duration) ([]*pdu.PDataTF, []byte) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var (
		pdus    []*pdu.PDataTF
		cmdBuf  []byte
		dataset []byte
		cmdDone bool
		dsDone  bool
	)
	for !cmdDone || !dsDone {
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out waiting for a complete message (%d PDUs so far)", len(pdus))
		}
		raw, err := s.nextPDU(time.Until(deadline))
		if err != nil {
			t.Fatalf("read PDU: %v", err)
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			t.Fatalf("expected P-DATA-TF, got %T", raw)
		}
		pdus = append(pdus, p)
		for _, pdv := range p.PDVs {
			if !pdv.IsCommand() {
				dataset = append(dataset, pdv.Fragment()...)
				if pdv.IsLast() {
					dsDone = true
				}
				continue
			}
			cmdBuf = append(cmdBuf, pdv.Fragment()...)
			if !pdv.IsLast() {
				continue
			}
			cmdDone = true
			// Whether to wait for a dataset is the command's statement, not a
			// guess: a response with no dataset ends right here.
			hasDS, err := dimse.CommandHasDataset(cmdBuf)
			if err != nil {
				t.Fatalf("parse command set: %v", err)
			}
			dsDone = !hasDS
		}
	}
	return pdus, dataset
}

// The Maximum Length Received in the AC is a promise the acceptor has to make
// from its own configuration, not from whatever the requestor asked for.
func TestSCPAdvertisesConfiguredMaxPDULength(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  uint32
		want uint32
	}{
		{name: "explicit", cfg: 8192, want: 8192},
		{name: "default", cfg: 0, want: pdu.DefaultMaxPDULength},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := serveSCP(t, ae.ServerConfig{AETitle: "PDUSCP", MaxPDULength: tc.cfg})
			got := associate(t, addr, associateRQ("PDUSCP"))
			ac, ok := got.(*pdu.AAssociateAC)
			if !ok {
				t.Fatalf("expected A-ASSOCIATE-AC, got %T", got)
			}
			if ac.UserInformation.MaxLength != tc.want {
				t.Fatalf("AC Maximum Length=%d, want %d", ac.UserInformation.MaxLength, tc.want)
			}
		})
	}
}

// bigNGetSCP returns an SCP whose N-GET answer carries size bytes of pixel data,
// so the reply cannot fit in one small PDU.
func bigNGetSCP(t *testing.T, size int) string {
	t.Helper()
	return serveSCP(t, ae.ServerConfig{
		AETitle:                  "BIGPDU",
		AcceptedAbstractSyntaxes: []string{printManagementSOPClass},
		OnNGet: func(context.Context, ae.NGetRequest) ae.NGetResult {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i)
			}
			ds := godicom.NewDataset()
			ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, payload))
			return ae.NGetResult{
				Status:                 status.Success,
				AffectedSOPClassUID:    printManagementSOPClass,
				AffectedSOPInstanceUID: "1.2.3.4.5",
				AttributeListData:      ds,
			}
		},
	})
}

// Every PDU the acceptor sends must fit the requestor's Maximum Length Received.
// On a real network an oversized PDU is dropped by the peer's buffer, and the
// association dies with no DIMSE error to explain it.
func TestSCPFragmentsResponsesToPeerMaxLength(t *testing.T) {
	t.Parallel()

	const (
		peerMax = 512
		payload = 4096
	)
	addr := bigNGetSCP(t, payload)
	scu := dialRawMaxPDU(t, addr, "BIGPDU", peerMax,
		contextRQ(1, printManagementSOPClass, pdu.ImplicitVRLittleEndian))

	cmd, err := (&dimse.NGetRQ{
		MessageID:               1,
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: "1.2.3.4.5",
	}).Encode()
	if err != nil {
		t.Fatalf("encode N-GET-RQ: %v", err)
	}
	sendOn(t, scu, 1, cmd, nil)

	pdus, dataset := readCommandAndDataset(t, scu, 5*time.Second)
	if len(pdus) < 2 {
		t.Fatalf("reply arrived in %d PDUs; %d bytes cannot fit in %d", len(pdus), payload, peerMax)
	}
	for i, p := range pdus {
		b, err := p.Encode()
		if err != nil {
			t.Fatalf("PDU %d encode: %v", i, err)
		}
		// The Maximum Length Received bounds the PDU body, header excluded.
		if body := len(b) - 6; body > peerMax {
			t.Fatalf("PDU %d body is %d bytes, peer accepts at most %d", i, body, peerMax)
		}
	}
	if len(dataset) == 0 {
		t.Fatal("no dataset arrived")
	}
}

// A requestor announcing 0 says it has no limit; the acceptor must then send the
// message whole rather than fragmenting to a guessed default.
func TestSCPSendsOnePDUWhenPeerDeclaresNoLimit(t *testing.T) {
	t.Parallel()

	const payload = 4096
	addr := bigNGetSCP(t, payload)
	scu := dialRawMaxPDU(t, addr, "BIGPDU", 0,
		contextRQ(1, printManagementSOPClass, pdu.ImplicitVRLittleEndian))

	cmd, err := (&dimse.NGetRQ{
		MessageID:               1,
		RequestedSOPClassUID:    printManagementSOPClass,
		RequestedSOPInstanceUID: "1.2.3.4.5",
	}).Encode()
	if err != nil {
		t.Fatalf("encode N-GET-RQ: %v", err)
	}
	sendOn(t, scu, 1, cmd, nil)

	pdus, dataset := readCommandAndDataset(t, scu, 5*time.Second)
	if len(dataset) < payload {
		t.Fatalf("dataset is %d bytes, want at least %d", len(dataset), payload)
	}
	// The command set and the dataset may travel in separate PDUs; what must not
	// happen is the 4 KiB dataset being split, which is what a guessed default
	// limit would cause.
	datasetPDUs := 0
	for _, p := range pdus {
		for _, pdv := range p.PDVs {
			if !pdv.IsCommand() {
				datasetPDUs++
				break
			}
		}
	}
	if datasetPDUs != 1 {
		t.Fatalf("dataset spanned %d PDUs, want 1 when the peer declares no maximum", datasetPDUs)
	}
}

// Being liberal in what we accept: we advertise a small maximum, but a peer that
// sends a larger PDU is still readable. Refusing it would break real senders,
// and the documented read ceiling (pdu.MaxPDUReadLength) already bounds memory.
func TestSCPAcceptsPDULargerThanItAdvertised(t *testing.T) {
	t.Parallel()

	const (
		advertised = 512
		dataset    = 8192
	)
	var stored atomic.Int64
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "SMALLADV",
		MaxPDULength:             advertised,
		AcceptedAbstractSyntaxes: []string{string(uid.CTImageStorage)},
		OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
			n, err := io.Copy(io.Discard, ev.Stream())
			if err != nil {
				t.Errorf("read dataset: %v", err)
			}
			stored.Store(n)
			return status.Success
		},
	})

	scu := dialRawMaxPDU(t, addr, "SMALLADV", pdu.DefaultMaxPDULength,
		contextRQ(1, string(uid.CTImageStorage), pdu.ImplicitVRLittleEndian))

	cmd, err := (&dimse.CStoreRQ{
		MessageID:              1,
		Priority:               dimse.PriorityMedium,
		AffectedSOPClassUID:    string(uid.CTImageStorage),
		AffectedSOPInstanceUID: "1.2.3.4.5",
	}).Encode()
	if err != nil {
		t.Fatalf("encode C-STORE-RQ: %v", err)
	}
	payload := make([]byte, dataset)
	sendOn(t, scu, 1, cmd, payload)

	if got := readStoreStatus(t, scu); got != status.Success {
		t.Fatalf("status=0x%04X, want success", got)
	}
	if got := stored.Load(); got != dataset {
		t.Fatalf("handler read %d bytes, want %d", got, dataset)
	}
}
