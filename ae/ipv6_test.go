package ae_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// listenIPv6Loopback binds the IPv6 loopback, or skips the test where there is
// none. Skipping rather than failing is the honest answer: a build host with
// IPv6 disabled cannot tell you anything about this code path, and a red build
// there would read as a defect in the server.
func listenIPv6Loopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	return ln
}

// The server takes a net.Listener, so the address family is the caller's
// decision and DIMSE has no opinion about it. That is a property worth pinning
// rather than assuming: an association loop that parsed addresses, keyed a map
// by host:port, or assumed a colon appears once would work on IPv4 and break
// here — and IPv6 addresses are bracketed, so a `strings.Split(addr, ":")`
// anywhere in the path would show up as a wrong peer or a failed association
// rather than an obvious error.
func TestServeOnIPv6Listener(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen net.Addr
	)
	addr, _ := serveOn(t, listenIPv6Loopback(t), ae.ServerConfig{
		AETitle:                  "V6SCP",
		AcceptedAbstractSyntaxes: []string{pdu.VerificationSOPClass, secondaryCaptureSOPClass},
		OnCStore: func(ctx context.Context, _ ae.StoreEvent) uint16 {
			info, _ := ae.AssocFromContext(ctx)
			mu.Lock()
			seen = info.RemoteAddr
			mu.Unlock()
			return dimse.StatusSuccess
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "V6SCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: pdu.VerificationSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, addr, "V6SCP")
	if err != nil {
		t.Fatalf("dial over IPv6: %v", err)
	}
	defer func() { _ = assoc.Close() }()

	if err := assoc.CEcho(ctx); err != nil {
		t.Fatalf("C-ECHO over IPv6: %v", err)
	}
	res, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5.6",
		Dataset:                goldenStoreDataset(),
	})
	if err != nil {
		t.Fatalf("C-STORE over IPv6: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("C-STORE status 0x%04x", res.Status)
	}

	mu.Lock()
	peer := seen
	mu.Unlock()
	if peer == nil {
		t.Fatal("handler saw no peer address")
	}
	host, _, err := net.SplitHostPort(peer.String())
	if err != nil {
		t.Fatalf("peer address %q does not split as host:port: %v", peer, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		t.Fatalf("peer host %q does not parse as an IP", host)
	}
	if ip.To4() != nil {
		t.Fatalf("peer host %q is IPv4, so this association did not use the IPv6 listener", host)
	}
	if !ip.IsLoopback() {
		t.Fatalf("peer host %q is not loopback", host)
	}
}

// The rejection path builds its answer from the peer's presentation contexts
// and the configured policy, and logs the peer's address. It has to behave the
// same when that address is bracketed: an RJ that only appears for IPv4 peers
// would be found by exactly one kind of site, at the worst time.
func TestAdmitRejectsIPv6Peer(t *testing.T) {
	t.Parallel()

	addr, _ := serveOn(t, listenIPv6Loopback(t), ae.ServerConfig{
		AETitle: "V6LIMIT",
		Admit: func(context.Context, ae.AssocInfo) (func(), error) {
			return nil, ae.RejectLocalLimit(nil)
		},
	})

	got := associate(t, addr, associateRQ("V6LIMIT"))
	requireRejectResult(t, got, pdu.RejectResultTransient,
		pdu.RejectSourceServiceProviderPres, pdu.RejectReasonLocalLimitExceeded)
}
