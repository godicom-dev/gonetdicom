package ae

import (
	"context"
	"errors"
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

// RejectError is the rejection an Admit decision asks for: the three
// A-ASSOCIATE-RJ fields of PS3.8 Tables 9-21 to 9-23, plus the reason behind
// the decision.
//
// It is the Go shape of pynetdicom's send_reject(result, source, reason) and of
// dcm4che's Association.reject(...): a value the caller builds and returns,
// rather than a call into the stack. Err says why the policy said no and
// survives errors.Is/As through Unwrap, so a caller can tell a quota rejection
// from a licence one without matching strings.
type RejectError struct {
	Result byte
	Source byte
	Reason byte
	Err    error
}

func (e *RejectError) Error() string {
	msg := fmt.Sprintf("ae: association rejected (result=%d source=%d reason=%d)",
		e.Result, e.Source, e.Reason)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap exposes Err to errors.Is and errors.As.
func (e *RejectError) Unwrap() error { return e.Err }

// RejectLocalLimit returns the "busy, try again later" rejection that both
// reference stacks send when they are at their limit: transient /
// service-provider-presentation / local-limit-exceeded.
//
//	Admit: func(ctx context.Context, info ae.AssocInfo) (func(), error) {
//		if !quota.Take(info.CallingAETitle) {
//			return nil, ae.RejectLocalLimit(fmt.Errorf("%s: quota exhausted", info.CallingAETitle))
//		}
//		return func() { quota.Put(info.CallingAETitle) }, nil
//	}
func RejectLocalLimit(err error) *RejectError {
	return &RejectError{
		Result: pdu.RejectResultTransient,
		Source: pdu.RejectSourceServiceProviderPres,
		Reason: pdu.RejectReasonLocalLimitExceeded,
		Err:    err,
	}
}

// admit consults cfg.Admit and answers the peer with the rejection it asks for.
//
// The deadline is what makes blocking inside Admit safe: a policy that queues
// instead of rejecting waits on a context cancelled at HandshakeTimeout or at
// server shutdown, so a queue cannot outlive the handshake it is queueing for.
func admit(ctx context.Context, conn net.Conn, cfg ServerConfig, info AssocInfo) (func(), error) {
	if cfg.Admit == nil {
		return nil, nil
	}
	if cfg.HandshakeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.HandshakeTimeout)
		// The socket deadline the caller set only bites on I/O, and a policy
		// blocked on a semaphore is doing none: without this the peer waits out
		// its own timeout for an answer that is never coming.
		//
		// AfterFunc is stopped before the context is cancelled, so "Admit
		// returned" deterministically wins over "the deadline fired". A
		// watcher goroutine selecting on done and ctx.Done would not: with both
		// ready, select picks at random, which closes a perfectly live
		// association.
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer func() { stop(); cancel() }()
	}

	release, err := cfg.Admit(ctx, info)
	if err == nil {
		return release, nil
	}
	if release != nil {
		release() // Admit rejected after taking something: give it back.
	}

	var rj *RejectError
	if !errors.As(err, &rj) {
		// Any other error is a policy failure rather than a protocol
		// statement: answer transient/no-reason-given so the peer retries
		// instead of concluding the endpoint is gone for good.
		rj = &RejectError{
			Result: pdu.RejectResultTransient,
			Source: pdu.RejectSourceServiceProviderACSE,
			Reason: pdu.RejectReasonNoReasonGiven,
			Err:    err,
		}
	}
	cfg.logger(ctx).Info("scp rejected association: Admit said no",
		gonetdicom.AttrRemote, info.RemoteAddr,
		gonetdicom.AttrCallingAE, info.CallingAETitle,
		gonetdicom.AttrCalledAE, info.CalledAETitle,
		"result", rj.Result, "source", rj.Source, "reason", rj.Reason,
		"err", rj.Err,
	)
	if ctx.Err() != nil {
		// Deadline fired or the server is shutting down: the socket is already
		// gone, so there is no one to answer and nothing to pretend.
		return nil, err
	}
	_ = writePDUConn(ctx, conn, &pdu.AAssociateRJ{
		Result:           rj.Result,
		Source:           rj.Source,
		ReasonDiagnostic: rj.Reason,
	})
	return nil, err
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
