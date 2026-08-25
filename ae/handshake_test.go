package ae_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// serveSCP starts cfg on a loopback listener and returns its address.
func serveSCP(t *testing.T, cfg ae.ServerConfig) string {
	t.Helper()
	addr, _ := serveSCPCancel(t, cfg)
	return addr
}

// serveSCPCancel is serveSCP plus the cancel func of the Serve context, for
// tests that need to shut the server down mid-association.
func serveSCPCancel(t *testing.T, cfg ae.ServerConfig) (string, context.CancelFunc) {
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
	go func() { _ = ae.Serve(ctx, ln, cfg) }()
	return ln.Addr().String(), cancel
}

// associateRQ builds a minimal but valid A-ASSOCIATE-RQ for calledAE.
func associateRQ(calledAE string) *pdu.AAssociateRQ {
	return &pdu.AAssociateRQ{
		CalledAETitle:          calledAE,
		CallingAETitle:         "RAWSCU",
		ApplicationContextName: pdu.ApplicationContextName,
		PresentationContexts: []pdu.PresentationContextRQ{{
			ID:               1,
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
		UserInformation: pdu.UserInformation{
			MaxLength:              pdu.DefaultMaxPDULength,
			ImplementationClassUID: "1.2.826.0.1.3680043.10.541.3",
		},
	}
}

// associate sends rq to addr and returns whatever the SCP answers with.
func associate(t *testing.T, addr string, rq *pdu.AAssociateRQ) pdu.PDU {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := pdu.Write(conn, rq); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return raw
}

func requireReject(t *testing.T, got pdu.PDU, source, reason byte) {
	t.Helper()
	requireRejectResult(t, got, pdu.RejectResultPermanent, source, reason)
}

func requireRejectResult(t *testing.T, got pdu.PDU, result, source, reason byte) {
	t.Helper()
	rj, ok := got.(*pdu.AAssociateRJ)
	if !ok {
		t.Fatalf("expected A-ASSOCIATE-RJ, got %T", got)
	}
	if rj.Result != result || rj.Source != source || rj.ReasonDiagnostic != reason {
		t.Fatalf("RJ result=%d source=%d reason=%d, want %d/%d/%d",
			rj.Result, rj.Source, rj.ReasonDiagnostic, result, source, reason)
	}
}

// An SCP must not answer to a name that is not its own: a peer aimed at the
// wrong endpoint has no other way to find out.
func TestSCPRejectsUnknownCalledAETitle(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	got := associate(t, addr, associateRQ("WRONG_AE"))
	requireReject(t, got, pdu.RejectSourceServiceUser, pdu.RejectReasonCalledAENotRecognized)
}

func TestSCPAcceptsItsOwnAndAlternativeAETitles(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:             "REAL_SCP",
		AlternativeAETitles: []string{"ALT_SCP"},
	})
	for _, called := range []string{"REAL_SCP", "ALT_SCP"} {
		got := associate(t, addr, associateRQ(called))
		if _, ok := got.(*pdu.AAssociateAC); !ok {
			t.Fatalf("called=%q: expected A-ASSOCIATE-AC, got %T", called, got)
		}
	}
}

func TestSCPAllowAnyCalledAETitle(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP", AllowAnyCalledAETitle: true})
	got := associate(t, addr, associateRQ("WHATEVER"))
	if _, ok := got.(*pdu.AAssociateAC); !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", got)
	}
}

// The AE title fields in an A-ASSOCIATE-AC are reserved: they carry back what
// the RQ sent and must not be tested. Sending the SCP's own title instead would
// break a requestor that (wrongly, but really) compares them.
func TestSCPEchoesAETitlesInAC(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP", AllowAnyCalledAETitle: true})
	rq := associateRQ("SOME_NAME")
	ac, ok := associate(t, addr, rq).(*pdu.AAssociateAC)
	if !ok {
		t.Fatal("expected an A-ASSOCIATE-AC")
	}
	if ac.CalledAETitle != rq.CalledAETitle || ac.CallingAETitle != rq.CallingAETitle {
		t.Fatalf("AC titles called=%q calling=%q, want %q / %q",
			ac.CalledAETitle, ac.CallingAETitle, rq.CalledAETitle, rq.CallingAETitle)
	}
	if ac.ProtocolVersion != pdu.ProtocolVersion1 {
		t.Fatalf("AC protocol version 0x%04x, want 0x%04x", ac.ProtocolVersion, pdu.ProtocolVersion1)
	}
}

// A requestor that does not announce version 1 is rejected by the UL provider.
func TestSCPRejectsUnsupportedProtocolVersion(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	rq := associateRQ("REAL_SCP")
	rq.ProtocolVersion = 0x0002 // some other version, version 1 not offered
	got := associate(t, addr, rq)
	requireReject(t, got, pdu.RejectSourceServiceProviderACSE, pdu.RejectReasonProtocolVersionNotSupported)
}

// Protocol-version is a bit field, so a requestor announcing version 1 *and*
// something we have never heard of still speaks version 1. Comparing the whole
// field for equality would reject it.
func TestSCPAcceptsProtocolVersionWithUnknownBits(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	rq := associateRQ("REAL_SCP")
	rq.ProtocolVersion = 0x8001 // version 1 plus a bit that means nothing yet
	got := associate(t, addr, rq)
	if _, ok := got.(*pdu.AAssociateAC); !ok {
		t.Fatalf("expected A-ASSOCIATE-AC, got %T", got)
	}
}

// A requestor that does not name itself cannot be answered: PS3.8 requires a
// real Calling AE Title, and an empty one cannot even be echoed in the AC.
func TestSCPRejectsEmptyCallingAETitle(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	rq := associateRQ("REAL_SCP")
	rq.CallingAETitle = "PLACEHOLDER" // PadAETitle refuses to encode an empty one
	raw, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(raw[26:42], "                ") // Calling AE Title field: all padding

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	requireReject(t, got, pdu.RejectSourceServiceUser, pdu.RejectReasonCallingAENotRecognized)
}

// The rejection has to reach the SCU as ErrRejected, not as a broken connection.
func TestDialReportsCalledAETitleRejection(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ae.Dial(ctx, ae.Config{AETitle: "SCU"}, addr, "WRONG_AE")
	if !errors.Is(err, ae.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}
