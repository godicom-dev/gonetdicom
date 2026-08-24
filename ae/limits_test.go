package ae_test

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// requireClosed fails unless the SCP has ended conn within timeout.
func requireClosed(t *testing.T, conn net.Conn, timeout time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	_, err := conn.Read(buf[:])
	if err == nil {
		t.Fatal("connection still open: the SCP sent data instead of ending it")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("connection still open after %v", timeout)
	}
}

// A connection that never associates must not hold a goroutine and a socket
// forever: opening connections and saying nothing was enough to exhaust the SCP.
func TestSCPHandshakeTimeoutEndsSilentConnection(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:          "LIMITSCP",
		HandshakeTimeout: 150 * time.Millisecond,
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Not a byte is sent: the SCP must give up on its own.
	requireClosed(t, conn, 3*time.Second)
}

// An established association whose peer goes quiet must end too.
func TestSCPIdleTimeoutEndsAssociation(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:     "LIMITSCP",
		IdleTimeout: 200 * time.Millisecond,
	})
	scu := dialRawSCU(t, addr, "LIMITSCP", pdu.VerificationSOPClass)
	requireClosed(t, scu.conn, 3*time.Second)
}

// A busy SCP must say so, and must free the slot again afterwards.
func TestSCPMaxConcurrentAssociations(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                   "LIMITSCP",
		MaxConcurrentAssociations: 1,
	})
	first := dialRawSCU(t, addr, "LIMITSCP", pdu.VerificationSOPClass)

	got := associate(t, addr, associateRQ("LIMITSCP"))
	requireRejectResult(t, got, pdu.RejectResultTransient,
		pdu.RejectSourceServiceProviderPres, pdu.RejectReasonLocalLimitExceeded)

	// Dropping the first association releases its slot.
	_ = first.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		switch got := associate(t, addr, associateRQ("LIMITSCP")).(type) {
		case *pdu.AAssociateAC:
			return
		default:
			if time.Now().After(deadline) {
				t.Fatalf("slot not released: got %T", got)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// Serve documents that it runs until its context is cancelled. Cancelling used
// to close only the listener, leaving established associations running with a
// goroutine each until their peers happened to act. The association reader is
// what covers this case today; the invariant is pinned here regardless.
func TestServeCancellationEndsEstablishedAssociation(t *testing.T) {
	t.Parallel()

	addr, cancel := serveSCPCancel(t, ae.ServerConfig{AETitle: "LIMITSCP"})
	scu := dialRawSCU(t, addr, "LIMITSCP", pdu.VerificationSOPClass)

	cancel()
	requireClosed(t, scu.conn, 3*time.Second)
}

// Cancellation has to reach a connection that has not associated yet, too. This
// is the case the association reader cannot cover: nothing is watching ctx while
// the SCP waits for an A-ASSOCIATE-RQ, so without closing the connection the
// shutdown waits out HandshakeTimeout.
func TestServeCancellationEndsConnectingClient(t *testing.T) {
	t.Parallel()

	addr, cancel := serveSCPCancel(t, ae.ServerConfig{
		AETitle:          "LIMITSCP",
		HandshakeTimeout: time.Minute, // far longer than this test waits
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// No A-ASSOCIATE-RQ: give the SCP a moment to block on its read.
	time.Sleep(50 * time.Millisecond)
	cancel()
	requireClosed(t, conn, 3*time.Second)
}
