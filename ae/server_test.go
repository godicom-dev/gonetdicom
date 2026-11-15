package ae_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// The acceptor can state its transfer-syntax preference per SOP Class, which is
// what an SCP that decodes some classes and forwards others needs. One global
// list cannot say "implicit VR for the two I parse, anything for the rest":
// accepting everything would claim a decode the handler cannot do, and
// accepting nothing would refuse bytes this server is happy to file.
func TestSCPAcceptsContextsPerSOPClass(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	mr := string(uid.MRImageStorage)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "PERCLASSSPC",
		AcceptedContexts: []ae.PresentationContext{
			{AbstractSyntax: ct, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{AbstractSyntax: mr, TransferSyntaxes: []string{"*"}},
		},
	})
	got := associate(t, addr, acceptRQ("PERCLASSSPC",
		// CT: implicit only, which is what this server decodes.
		contextRQ(1, ct, pdu.ImplicitVRLittleEndian),
		// CT again, JPEG: same class, refused syntax — 4, not 3.
		contextRQ(3, ct, string(uid.JPEG2000Lossless)),
		// MR: "*" accepts whatever was proposed, because the handler files
		// the bytes instead of parsing them.
		contextRQ(5, mr, string(uid.JPEG2000Lossless)),
		// A class nobody listed and no wildcard covers: 3.
		contextRQ(7, string(uid.SecondaryCaptureImageStorage), pdu.ImplicitVRLittleEndian),
	))
	requireContextResults(t, got, map[byte]byte{
		1: pcAcceptance,
		3: pcTransferSyntaxesNotSup,
		5: pcAcceptance,
		7: pcAbstractSyntaxNotSupport,
	})
}

// A "*" entry is the class-level counterpart of the syntax wildcard: classes
// not named in the list fall back to it. Without one, AcceptedContexts is a
// closed set, which is the safe default — a private SOP Class the caller has
// never heard of is not accepted by accident.
func TestSCPAcceptedContextsWildcardCoversUnlistedClasses(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	mr := string(uid.MRImageStorage)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "WILDCARDSCP",
		AcceptedContexts: []ae.PresentationContext{
			{AbstractSyntax: ct, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{AbstractSyntax: "*", TransferSyntaxes: []string{"*"}},
		},
	})
	got := associate(t, addr, acceptRQ("WILDCARDSCP",
		contextRQ(1, ct, string(uid.JPEG2000Lossless)), // CT is named: explicit list wins
		contextRQ(3, mr, string(uid.JPEG2000Lossless)), // MR is not: the "*" entry covers it
	))
	requireContextResults(t, got, map[byte]byte{
		1: pcTransferSyntaxesNotSup,
		3: pcAcceptance,
	})
}

// Verification answers C-ECHO, which is how a peer checks it is talking to the
// endpoint it thinks it is. A list that forgets to name it must not turn that
// check into a context error.
func TestSCPAcceptedContextsAlwaysServeVerification(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "VERIFYSCP",
		AcceptedContexts: []ae.PresentationContext{
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})
	got := associate(t, addr, acceptRQ("VERIFYSCP",
		contextRQ(1, pdu.VerificationSOPClass, pdu.ImplicitVRLittleEndian),
	))
	requireContextResults(t, got, map[byte]byte{1: pcAcceptance})
}

// An association where some contexts were refused still stands, and the SCU can
// report which ones and why. Without this the caller sees a working association
// and a later ErrNoContext for a class it believes it proposed.
func TestSCUReportsRejectedContexts(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	mwl := string(uid.ModalityWorklistInformationModelFind)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "REJSCP",
		AcceptedAbstractSyntaxes: []string{ct},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "REJSCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: ct, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: mwl, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, addr, "REJSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(ctx) }()

	if got := len(assoc.Contexts()); got != 1 {
		t.Fatalf("accepted contexts = %d, want 1", got)
	}
	rejected := assoc.RejectedContexts()
	if len(rejected) != 1 {
		t.Fatalf("rejected contexts = %d (%v), want 1", len(rejected), rejected)
	}
	if rejected[0].AbstractSyntax != mwl {
		t.Errorf("rejected class = %q, want %q", rejected[0].AbstractSyntax, mwl)
	}
	if rejected[0].Result != pcAbstractSyntaxNotSupport {
		t.Errorf("result = %d, want %d (abstract syntax not supported)", rejected[0].Result, pcAbstractSyntaxNotSupport)
	}
	if rejected[0].ID != 3 {
		t.Errorf("rejected context ID = %d, want 3", rejected[0].ID)
	}
}

// When nothing at all is accepted there is no Association to ask, so the answer
// has to be in the error: which classes were refused, and whether the peer does
// not serve them (3) or does not serve them in a syntax either side can read
// (4). Both used to read as "no presentation context accepted".
func TestSCUDialExplainsARejectedAssociation(t *testing.T) {
	t.Parallel()

	ct := string(uid.CTImageStorage)
	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                  "NOCONTEXTSCP",
		AcceptedAbstractSyntaxes: []string{ct},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ae.Dial(ctx, ae.Config{
		AETitle: "NOCONTEXTSCU",
		PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax:   ct,
			TransferSyntaxes: []string{string(uid.JPEG2000Lossless)},
		}},
	}, addr, "NOCONTEXTSCP")
	if err == nil {
		t.Fatal("dial succeeded, want a refusal")
	}
	if !errors.Is(err, ae.ErrNoContext) {
		t.Fatalf("error = %v, want it to be ErrNoContext", err)
	}
	var refusal *ae.NoContextError
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v (%T), want *ae.NoContextError", err, err)
	}
	if len(refusal.Rejected) != 1 || refusal.Rejected[0].Result != pcTransferSyntaxesNotSup {
		t.Fatalf("rejected = %v, want one context with result %d", refusal.Rejected, pcTransferSyntaxesNotSup)
	}
	if !strings.Contains(err.Error(), "transfer syntaxes not supported") {
		t.Errorf("error text %q does not name the reason", err)
	}
}

// serveServer starts a Server on a listener of its own and returns a handle to
// it. The server is cancelled and drained at the end of the test.
func serveServer(t *testing.T, cfg ae.ServerConfig) (*ae.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := ae.NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v, want nil after cancellation", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return within 5s of its context being cancelled")
		}
	})
	return srv, ln.Addr().String()
}

// waitForAssociation polls the registry until one association shows up. The
// accept and the handshake happen on another goroutine, so "immediately after
// Dial returns" is not a guarantee the registry can offer.
func waitForAssociation(t *testing.T, srv *ae.Server) *ae.ActiveAssociation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if assocs := srv.Associations(); len(assocs) > 0 {
			return assocs[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no association appeared in the registry within 5s")
	return nil
}

// A Server reports what it is serving, and can end one association without
// taking the server down. The peer has to see an A-ABORT rather than a closed
// socket: a close says the network broke, and a peer that believes that retries
// into a server that has already decided to cut it off.
func TestServerTracksAndAbortsAnAssociation(t *testing.T) {
	t.Parallel()

	srv, addr := serveServer(t, ae.ServerConfig{AETitle: "TRACKSCP"})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := pdu.Write(conn, associateRQ("TRACKSCP")); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if _, err := pdu.Read(conn); err != nil {
		t.Fatalf("read AC: %v", err)
	}

	assoc := waitForAssociation(t, srv)
	info, ok := assoc.Info()
	if !ok {
		t.Fatal("registry reports a negotiated association as not negotiated")
	}
	if info.CallingAETitle != "RAWSCU" || info.CalledAETitle != "TRACKSCP" {
		t.Errorf("info = %q -> %q, want RAWSCU -> TRACKSCP", info.CallingAETitle, info.CalledAETitle)
	}
	if assoc.RemoteAddr() == nil {
		t.Error("RemoteAddr is nil on a live association")
	}
	if assoc.Since().IsZero() {
		t.Error("Since is zero on a live association")
	}

	if err := assoc.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := assoc.Abort(); err != nil {
		t.Errorf("second Abort returned %v, want nil", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	next, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("reading the peer's answer after Abort: %v", err)
	}
	if _, ok := next.(*pdu.AAbort); !ok {
		t.Fatalf("peer received %T, want A-ABORT", next)
	}

	waitForNoAssociations(t, srv)
}

// Close is part of the contract: an association that has ended leaves the
// registry, so a drained registry means every peer has been answered.
func waitForNoAssociations(t *testing.T, srv *ae.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.Associations()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%d associations still registered 5s after they ended", len(srv.Associations()))
}

// AbortAll is the shutdown hammer: every association ends with A-ABORT, and
// Serve returns once the last handler has finished — not when the listener
// closes, which would leave work running behind a function that has returned.
func TestServerAbortAllEndsEveryAssociation(t *testing.T) {
	t.Parallel()

	srv, addr := serveServer(t, ae.ServerConfig{AETitle: "ABORTALLSCP"})

	var conns []net.Conn
	for i := 0; i < 3; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := pdu.Write(conn, associateRQ("ABORTALLSCP")); err != nil {
			t.Fatalf("write RQ: %v", err)
		}
		if _, err := pdu.Read(conn); err != nil {
			t.Fatalf("read AC: %v", err)
		}
		conns = append(conns, conn)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(srv.Associations()) < len(conns) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := len(srv.Associations()); got != len(conns) {
		t.Fatalf("registry holds %d associations, want %d", got, len(conns))
	}

	if n := srv.AbortAll(); n != len(conns) {
		t.Errorf("AbortAll ended %d associations, want %d", n, len(conns))
	}
	for i, conn := range conns {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		next, err := pdu.Read(conn)
		if err != nil {
			t.Fatalf("connection %d: reading after AbortAll: %v", i, err)
		}
		if _, ok := next.(*pdu.AAbort); !ok {
			t.Errorf("connection %d received %T, want A-ABORT", i, next)
		}
	}
	waitForNoAssociations(t, srv)
}

// Shutting the server down tells the peer, instead of closing the socket under
// it: a close reads as a network fault, and a peer that believes that retries
// into a server that is not there. pynetdicom's AE.shutdown aborts each active
// association before shutting the server down for the same reason.
func TestServerShutdownAbortsAssociations(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := ae.NewServer(ae.ServerConfig{AETitle: "SHUTDOWNSCP"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); _ = ln.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := pdu.Write(conn, associateRQ("SHUTDOWNSCP")); err != nil {
		t.Fatalf("write RQ: %v", err)
	}
	if _, err := pdu.Read(conn); err != nil {
		t.Fatalf("read AC: %v", err)
	}
	waitForAssociation(t, srv)

	cancel()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	next, err := pdu.Read(conn)
	if err != nil {
		t.Fatalf("reading the peer's answer after shutdown: %v", err)
	}
	if _, ok := next.(*pdu.AAbort); !ok {
		t.Fatalf("peer received %T, want A-ABORT", next)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	if left := len(srv.Associations()); left != 0 {
		t.Errorf("%d associations still registered after Serve returned", left)
	}
}

// The function form has no registry to leak into: Serve(ctx, ln, cfg) keeps
// working, and a NewServer that is never served holds nothing.
func TestNewServerStartsEmpty(t *testing.T) {
	t.Parallel()

	srv := ae.NewServer(ae.ServerConfig{AETitle: "EMPTYSCP"})
	if got := srv.Associations(); len(got) != 0 {
		t.Fatalf("a fresh server holds %d associations", len(got))
	}
	if srv.Config().AETitle != "EMPTYSCP" {
		t.Errorf("Config().AETitle = %q, want EMPTYSCP", srv.Config().AETitle)
	}
	if srv.Config().HandshakeTimeout != ae.DefaultHandshakeTimeout {
		t.Errorf("Config() does not carry the applied defaults: HandshakeTimeout = %v", srv.Config().HandshakeTimeout)
	}
}
