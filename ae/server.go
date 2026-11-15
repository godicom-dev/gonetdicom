package ae

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Server is an association acceptor (SCP) that keeps a registry of the
// associations it is serving.
//
// Serve(ctx, ln, cfg) is the whole of the SCP for most applications, and it has
// no handle: once started, the only way to stop it is to cancel its context.
// That is enough to run a service and not enough to operate one — "who is
// connected right now", "cut that association off", "wait until the last peer
// is gone before closing the database" are all questions about associations
// this package was already tracking internally and not exposing.
//
// A Server answers them by name:
//
//	srv := ae.NewServer(cfg)
//	go srv.Serve(ctx, ln)
//	for _, a := range srv.Associations() {
//		log.Printf("%s: %s", a.RemoteAddr(), a.Info().CallingAETitle)
//	}
//
// An association enters the registry when its connection is accepted and leaves
// it when the connection is done, so a drained registry means every peer has
// been answered and every handler has returned.
type Server struct {
	cfg ServerConfig

	mu  sync.Mutex
	reg map[*ActiveAssociation]struct{}
	// wg counts association goroutines, so Serve can wait for them instead of
	// returning while handlers still hold the application's resources.
	wg sync.WaitGroup
}

// NewServer returns an SCP configured by cfg. Nothing is listening yet: Serve
// binds the policy to a listener.
func NewServer(cfg ServerConfig) *Server {
	return &Server{cfg: cfg.withDefaults(), reg: make(map[*ActiveAssociation]struct{})}
}

// Config returns the configuration the server runs with, defaults applied.
func (s *Server) Config() ServerConfig { return s.cfg }

// Serve accepts associations on ln until ctx is cancelled or ln fails.
//
// Cancelling ctx closes the listener and every association still running, and
// Serve returns only after the last association goroutine has ended — the
// counterpart of net/http's Shutdown, and the reason to hold a *Server instead
// of calling Serve: what follows the call runs against a quiet server.
//
// A listener that fails for another reason ends Serve with that error and
// leaves the associations already running alone; they own their connections and
// end when their peers do.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	cfg := s.cfg
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentSCP)
	limiter := newAssocLimiter(cfg.MaxConcurrentAssociations)
	go func() {
		<-ctx.Done()
		// The listener is what Serve is blocked on; the associations watch ctx
		// themselves and answer it with A-ABORT (see abortOnDone), so a peer
		// still connected is told the server is going away rather than left to
		// read a closed socket as a network fault.
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
				return fmt.Errorf("ae: accept: %w", err)
			}
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			cfg.logger(ctx).Info("scp accepted connection",
				gonetdicom.AttrRemote, c.RemoteAddr().String(),
			)
			if !limiter.acquire() {
				_ = rejectOverloaded(ctx, c, cfg)
				return
			}
			defer limiter.release()
			if err := handleAssociation(ctx, c, cfg, s); err != nil && ctx.Err() == nil {
				cfg.logger(ctx).Debug("scp association ended",
					gonetdicom.AttrRemote, c.RemoteAddr().String(),
					"err", err,
				)
			}
		}(conn)
	}
}

// ListenAndServeTLS is Serve on a TLS listener bound to addr.
func (s *Server) ListenAndServeTLS(ctx context.Context, addr string) error {
	if s.cfg.TLS == nil {
		return fmt.Errorf("ae: ListenAndServeTLS requires ServerConfig.TLS")
	}
	ln, err := tls.Listen("tcp", addr, s.cfg.TLS)
	if err != nil {
		return fmt.Errorf("ae: tls listen %s: %w", addr, err)
	}
	defer ln.Close()
	s.cfg.logger(ctx).Info("scp listening (tls)", gonetdicom.AttrAddr, ln.Addr().String())
	return s.Serve(ctx, ln)
}

// Associations returns a snapshot of the associations being served, in no
// particular order.
//
// The values point at live connections: holding one after the association ends
// is harmless (Abort is a no-op then, Info still answers), but it does not keep
// anything running.
func (s *Server) Associations() []*ActiveAssociation {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ActiveAssociation, 0, len(s.reg))
	for a := range s.reg {
		out = append(out, a)
	}
	return out
}

// AbortAll ends every association being served with A-ABORT, and returns how
// many were running. Handlers notice on their next read or write; Serve returns
// once they have.
//
// This is the hammer, not the goodbye: a caller that wants the peers to know the
// association ended cleanly cancels the context and lets each of them release.
// AbortAll exists for the cases where cleanly is not on offer — a licence
// revoked, a device being serviced, a shutdown that cannot wait.
func (s *Server) AbortAll() int {
	assocs := s.Associations()
	for _, a := range assocs {
		_ = a.Abort()
	}
	return len(assocs)
}

// track registers a connection and returns its handle for the association
// goroutine to fill in. A nil *Server (the function form of Serve) tracks
// nothing and returns nil, and every method below tolerates that.
func (s *Server) track(conn net.Conn) *ActiveAssociation {
	if s == nil {
		return nil
	}
	a := &ActiveAssociation{conn: conn, since: time.Now()}
	s.mu.Lock()
	s.reg[a] = struct{}{}
	s.mu.Unlock()
	return a
}

func (s *Server) untrack(a *ActiveAssociation) {
	if s == nil || a == nil {
		return
	}
	s.mu.Lock()
	delete(s.reg, a)
	s.mu.Unlock()
}

// ActiveAssociation is one association an SCP is serving.
//
// It is a handle, not an Association: the acceptor side has no per-association
// object to expose (handlers receive an AssocInfo and the DIMSE request), and
// inventing one would suggest the caller can send requests on it. What a handle
// can honestly do is describe the peer and end the connection.
type ActiveAssociation struct {
	conn  net.Conn
	since time.Time

	mu         sync.Mutex
	info       AssocInfo
	negotiated bool
	closed     bool
	closeErr   error
}

// RemoteAddr is the peer's address. It is available before negotiation, which
// is also when it is most useful: an association that never associates is a
// connection, and this is all there is to say about it.
func (a *ActiveAssociation) RemoteAddr() net.Addr {
	if a == nil {
		return nil
	}
	return a.conn.RemoteAddr()
}

// Since is when the connection was accepted.
func (a *ActiveAssociation) Since() time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.since
}

// Info returns what the association negotiated: AE titles, peer implementation
// identification, asynchronous operations window.
//
// ok is false while the A-ASSOCIATE-RQ is still being read — an association
// that is connected but has not said who it is. It never becomes false again:
// the last values stay readable after the association ends.
func (a *ActiveAssociation) Info() (AssocInfo, bool) {
	if a == nil {
		return AssocInfo{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.info, a.negotiated
}

// Abort ends the association with A-ABORT and closes its connection.
//
// A-ABORT rather than a close, for the same reason the rest of this package
// sends one: closing a socket tells the peer the network broke, and a peer that
// believes that retries into a server that has already decided to cut it off.
// Calling it twice, or on an association that has already ended, is a no-op.
func (a *ActiveAssociation) Abort() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return a.closeErr
	}
	a.closed = true
	_ = pdu.Write(a.conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00})
	a.closeErr = a.conn.Close()
	return a.closeErr
}

// negotiatedInfo records the peer's identity once the handshake has read it.
func (a *ActiveAssociation) negotiatedInfo(info AssocInfo) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.info = info
	a.negotiated = true
	a.mu.Unlock()
}
