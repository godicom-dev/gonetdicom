package ae

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// DefaultHandshakeTimeout bounds association negotiation when
// ServerConfig.HandshakeTimeout is unset.
const DefaultHandshakeTimeout = 30 * time.Second

// assocLimiter caps concurrently handled associations. A nil limiter, or one
// built with n <= 0, is unlimited.
type assocLimiter struct {
	slots chan struct{}
}

func newAssocLimiter(n int) *assocLimiter {
	if n <= 0 {
		return &assocLimiter{}
	}
	return &assocLimiter{slots: make(chan struct{}, n)}
}

// acquire takes a slot without waiting, reporting whether it got one.
func (l *assocLimiter) acquire() bool {
	if l.slots == nil {
		return true
	}
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *assocLimiter) release() {
	if l.slots == nil {
		return
	}
	<-l.slots
}

// rejectOverloaded answers a connection that arrived while every association
// slot was busy.
//
// The A-ASSOCIATE-RQ is read first so the requestor gets an A-ASSOCIATE-RJ
// rather than a bare TCP close, which is indistinguishable from a crash. The
// read is bounded by HandshakeTimeout: this path must stay cheap, since it is
// what runs when the server is already at its limit.
func rejectOverloaded(ctx context.Context, conn net.Conn, cfg ServerConfig) error {
	defer func() { _ = conn.Close() }()
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentSCP)
	if cfg.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(cfg.HandshakeTimeout))
	}
	raw, err := readPDUConn(ctx, conn)
	if err != nil {
		return err
	}
	rq, ok := raw.(*pdu.AAssociateRQ)
	if !ok {
		return fmt.Errorf("ae: expected A-ASSOCIATE-RQ, got %T", raw)
	}
	cfg.logger(ctx).Warn("scp rejected association: at MaxConcurrentAssociations",
		gonetdicom.AttrRemote, conn.RemoteAddr().String(),
		gonetdicom.AttrCalledAE, rq.CalledAETitle,
		gonetdicom.AttrCallingAE, rq.CallingAETitle,
		"limit", cfg.MaxConcurrentAssociations,
	)
	_ = writePDUConn(ctx, conn, &pdu.AAssociateRJ{
		Result:           pdu.RejectResultTransient,
		Source:           pdu.RejectSourceServiceProviderPres,
		ReasonDiagnostic: pdu.RejectReasonLocalLimitExceeded,
	})
	return fmt.Errorf("%w: at MaxConcurrentAssociations (%d)", ErrRejected, cfg.MaxConcurrentAssociations)
}

// idleConn applies an idle timeout to every read and write.
//
// The deadline is refreshed per call rather than set once, so it bounds silence
// instead of total association lifetime: a long C-GET keeps working as long as
// bytes keep moving, while a peer that stops talking — or stops reading, which
// would otherwise block our writes forever — ends the association.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(b []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(b)
}

func (c *idleConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(b)
}

// withIdleTimeout wraps conn when an idle timeout applies.
func withIdleTimeout(conn net.Conn, idle time.Duration) net.Conn {
	if idle <= 0 {
		return conn
	}
	return &idleConn{Conn: conn, idle: idle}
}

// closeOnDone closes conn when ctx is cancelled, and returns a function that
// stops watching.
//
// Serve documents that it serves "until ctx is cancelled", but cancelling only
// closes the listener: a goroutine blocked reading an established association
// stays blocked until its peer acts. Closing the connection is what actually
// unblocks it.
func closeOnDone(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}
