package ae

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"math"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// StoreHandler handles an incoming C-STORE-RQ on an SCP association.
// Return a DIMSE status (dcmstatus.Success / dimse.StatusSuccess on success).
type StoreHandler func(ctx context.Context, ev StoreEvent) uint16

// FindHandler handles an incoming C-FIND-RQ. It returns an iterator over the
// responses, in the order they should go on the wire: Pending matches,
// optionally followed by one terminal Success/Failure/Cancel response.
//
// The iterator is the Go spelling of pynetdicom's C-FIND generator
// (service_class.py:296-390): the handler body runs on the association's
// goroutine, one item at a time, and the framework has written the previous
// response before it asks for the next. That is where the memory goes — a
// query over a large archive is answered without the whole result set ever
// existing — and it is also why a handler needs no buffer, no goroutine and no
// send queue of its own.
//
// Returning without a terminal response ends the operation with Success, which
// is what pynetdicom does when the generator is exhausted (:398-402).
//
// ctx is the handler's to use for as long as it runs. The framework cancels it
// when the peer stops the operation — context.Cause reports ErrPeerCancelled
// or ErrPeerReleased (cancel.go) — and the yield returns false at the same
// time, so a handler that is producing results stops at its next yield. Both
// are noticed between two items, which is the granularity the protocol offers:
// a stop that arrives while one item is being produced takes effect when that
// item is offered, not inside the call that produced it — the association's own
// context is the only thing that interrupts a call already under way.
// Handler errors become 0xC311 (status.FindUnableToProcess).
type FindHandler func(ctx context.Context, req FindRequest) iter.Seq2[FindMatch, error]

// MoveHandler handles an incoming C-MOVE-RQ. Return a MovePlan with Stores to
// C-STORE to MoveDestination and/or explicit Responses.
type MoveHandler func(ctx context.Context, req MoveRequest) MovePlan

// MovePlan is the SCP response plan for a C-MOVE-RQ.
//
// Destination is the first yield of pynetdicom's evt.EVT_C_MOVE handler
// (pynetdicom/_handlers.py doc_handle_move): the address and port of the
// Move Destination. Nil keeps the ServerConfig.MoveDestinations lookup.
// A non-nil Destination with an empty Addr is an unknown destination, and
// the SCP responds with status 0xA801, the same as yielding (None, None).
//
// When Stores is non-empty and the destination is known, the SCP opens a
// Storage SCU association and performs C-STORE sub-operations. Each distinct
// SOP Class and stored transfer syntax is one presentation context offering
// only that syntax, as qrscp does with build_context(sop_class_uid,
// transfer_syntax_uid). If Responses is empty, pending and final C-MOVE-RSP
// statuses are derived from the store outcomes.
type MovePlan struct {
	Destination *MoveDestination
	Stores      []StoreRequest
	Responses   []RetrieveMatch
}

// MoveDestination is an outbound Storage SCP endpoint for C-MOVE sub-operations.
type MoveDestination struct {
	Addr      string // host:port
	CalledAE  string // if empty, use the Move Destination AE Title
	CallingAE string // if empty, use ServerConfig.AETitle
	// MaxAssociations is the maximum number of parallel Storage SCU associations
	// used for C-MOVE C-STORE sub-operations. 0 or 1 uses a single association
	// (sequential stores). Values >1 fan out stores across associations — a
	// Go concurrency advantage over typical single-threaded DIMSE stacks.
	MaxAssociations int
}

// GetPlan is the whole list of sub-operations a C-GET handler used to return:
// its stores are sent as C-STORE-RQ on the same association (SCU role), then
// its responses are sent as C-GET-RSP. PlannedGet adapts one to the handler
// contract, which is what a handler that enumerates eagerly still returns.
type GetPlan struct {
	Stores    []StoreRequest
	Responses []RetrieveMatch
}

type acceptedContext struct {
	AbstractSyntax string
	TransferSyntax string
	AsSCU          bool // local (acceptor) roles
	AsSCP          bool
}

// ServerConfig configures an Association acceptor (SCP).
type ServerConfig struct {
	AETitle                   string
	MaxPDULength              uint32
	ImplementationClassUID    string
	ImplementationVersionName string
	// AlternativeAETitles are further Called AE Titles this SCP answers to,
	// for an SCP published under more than one name.
	AlternativeAETitles []string
	// AllowAnyCalledAETitle accepts an association whatever Called AE Title the
	// requestor asked for.
	//
	// By default the Called AE Title must be AETitle or one of
	// AlternativeAETitles; anything else is rejected with A-ASSOCIATE-RJ
	// (called-AE-title-not-recognized). Set this only for a deliberately
	// promiscuous SCP — accepting any name makes it impossible for a peer to
	// detect that it is talking to the wrong endpoint.
	AllowAnyCalledAETitle bool
	// AcceptedAbstractSyntaxes lists SOP Class UIDs the SCP will accept
	// (plus Verification is always accepted for C-ECHO). Prefer
	// AllStorageSOPClasses for a generic Storage SCP (pynetdicom
	// AllStoragePresentationContexts), or a class's SOPClasses for any other
	// service — ServiceClassStorageCommitment.SOPClasses() and the like.
	// Include "*" only when you intentionally accept any peer-proposed
	// abstract syntax (including private UIDs).
	//
	// Every class listed here is offered the same syntaxes, from
	// AcceptedTransferSyntaxes. A server that wants to refuse a syntax for one
	// class and allow it for another writes AcceptedContexts instead.
	AcceptedAbstractSyntaxes []string
	// AcceptedTransferSyntaxes lists the transfer syntaxes this SCP accepts,
	// most preferred first. Empty means DefaultTransferSyntaxes, which is what
	// godicom can decode. A context whose syntaxes do not intersect is answered
	// with result 4 (transfer syntaxes not supported) — accepting a syntax we
	// cannot read would be a claim the dataset handler has to disprove.
	//
	// Include "*" to accept whatever the requestor proposes. That is the right
	// setting for a pass-through SCP (StoreEvent.Stream is enough to forward or
	// file the bytes), and the wrong one for a handler that calls Decode.
	AcceptedTransferSyntaxes []string
	// AcceptedContexts states the accepted presentation contexts a SOP Class at
	// a time, each with its own transfer-syntax preference — pynetdicom's
	// add_supported_context(uid, transfer_syntaxes). It replaces
	// AcceptedAbstractSyntaxes and AcceptedTransferSyntaxes rather than adding
	// to them, because a class-at-a-time list is the more specific statement:
	// when both are set, this one decides.
	//
	// It exists for the SCP that does not treat every class the same way. A
	// store that parses CT and MR and only forwards everything else wants
	// implicit VR for the first two and "*" for the rest:
	//
	//	AcceptedContexts: []PresentationContext{
	//		{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
	//		{AbstractSyntax: string(uid.MRImageStorage), TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
	//		{AbstractSyntax: "*", TransferSyntaxes: []string{"*"}},
	//	},
	//
	// An entry with no TransferSyntaxes uses AcceptedTransferSyntaxes (or the
	// default set). Verification stays accepted for C-ECHO unless it is listed
	// with syntaxes of its own. A class that is neither listed nor covered by
	// "*" is answered with result 3, and a class whose list does not intersect
	// what the requestor proposed with result 4 — the same two answers the
	// flat configuration gives, decided per class instead of globally.
	AcceptedContexts []PresentationContext
	OnCStore         StoreHandler
	OnCFind          FindHandler
	// IdentifierValidator checks a C-FIND / C-MOVE / C-GET identifier before
	// the handler runs and returns a DIMSE status (0 accepts, 0xA900 refuses).
	// Nil means every identifier reaches the handler unexamined.
	//
	// PS3.4 C.4.1.1.3 puts the "identifier does not match the SOP Class" answer
	// on the SCP, but the rule that decides it is model-specific — which is why
	// this is a hook rather than a framework table. QueryRetrieveLevelValidator
	// covers the check most services need; QueryModelValidator also knows each
	// model's levels and that a retrieval has to name the entity it retrieves.
	IdentifierValidator IdentifierValidator
	OnCMove             MoveHandler
	OnCGet              GetHandler
	OnNAction           NActionHandler
	OnNEventReport      EventReportHandler
	OnNGet              NGetHandler
	OnNSet              NSetHandler
	OnNCreate           NCreateHandler
	OnNDelete           NDeleteHandler
	// MoveDestinations maps Move Destination AE Title → Storage SCP endpoint.
	// Used when MovePlan.Destination is nil. A handler that looks the peer up
	// itself sets MovePlan.Destination and leaves this map unused.
	MoveDestinations map[string]MoveDestination
	// RoleSelections lists SOP Classes for which this SCP supports non-default
	// SCP/SCU roles (values are the *acceptor* capabilities). Empty means
	// default roles only (requestor=SCU, acceptor=SCP) and no role reply items.
	RoleSelections []pdu.RoleSelection
	// OnUserIdentity verifies a User Identity RQ item. Nil means identity is
	// ignored and the association is accepted without an AC response item
	// (pynetdicom default when no EVT_USER_ID handler is bound).
	OnUserIdentity UserIdentityHandler
	// AsyncOperationsWindow is the window this SCP advertises when a requestor
	// proposes an Asynchronous Operations Window. Nil answers 1/1 — one
	// operation invoked and performed at a time — because that is exactly what
	// the dispatch loop does; advertising more would promise concurrency the
	// server does not deliver.
	//
	// Set it only if you know your handlers tolerate overlapping invocations,
	// and note the loop is still ordered: the value describes the window the
	// peer may use, not a scheduler this package implements.
	AsyncOperationsWindow *pdu.AsyncOperationsWindow
	// ExtendedNegotiation answers a requestor's SOP Class Extended Negotiation
	// (PS3.8 D.3.3.5). It is called once per proposed SOP Class with that
	// class's Service Class Application Information; returning ok=false omits
	// the answer for that class, which is how an SCP declines extended
	// negotiation for a class it handles the DIMSE way.
	ExtendedNegotiation func(sopClassUID string, info []byte) ([]byte, bool)
	// CommonExtendedNegotiation decides, per proposed SOP Class, whether this
	// SCP confirms the requestor's SOP Class Common Extended Negotiation
	// statement (PS3.8 D.3.3.6). Returning true echoes the statement back,
	// which is how a Storage Commitment provider announces that it commits to
	// the storage classes it serves.
	//
	// Nil sends no such item, which the protocol reads as "no common
	// extensions here" rather than a refusal: the class is still handled the
	// plain DIMSE way. Called only for classes the requestor named, on the
	// association's goroutine, before any DIMSE message is read.
	CommonExtendedNegotiation func(pdu.CommonExtendedNegotiation) bool
	// TLS, when non-nil, wraps accepted connections with TLS (use with ServeTLS
	// or a tls.Listener). Ignored by Serve on a plain listener.
	TLS *tls.Config
	// HandshakeTimeout bounds association negotiation: how long a connection may
	// take to send its A-ASSOCIATE-RQ and receive the reply. 0 uses
	// DefaultHandshakeTimeout, negative means no limit.
	//
	// Unlike IdleTimeout this defaults to a real value, because a connection that
	// has not associated yet costs a goroutine and a socket while having proved
	// nothing: without a bound, opening connections and staying silent is enough
	// to exhaust the server.
	HandshakeTimeout time.Duration
	// IdleTimeout bounds each PDU read and write on an established association.
	// 0 (the default) means no limit.
	//
	// The deadline is per read/write, so it measures silence rather than total
	// association lifetime: an association exchanging PDUs stays up indefinitely,
	// one whose peer stops talking (or stops reading) ends after IdleTimeout.
	IdleTimeout time.Duration
	// MaxConcurrentAssociations caps how many associations are handled at once.
	// 0 (the default) means no limit.
	//
	// A connection arriving while the cap is reached is rejected with a transient
	// A-ASSOCIATE-RJ (local-limit-exceeded), which tells the requestor to retry
	// later; closing the connection would leave it guessing.
	MaxConcurrentAssociations int
	// MaxDatasetSize caps the bytes one DIMSE message may carry, command set
	// and dataset together. 0 (the default) means no limit.
	//
	// This is a hard ceiling on what a single association can make the SCP
	// buffer, and the process bound follows from arithmetic the SCP already
	// owns: MaxConcurrentAssociations × MaxDatasetSize. There is deliberately no
	// global byte budget — a shared budget lets one large sender stall every
	// other association, and it would buy nothing the two numbers above do not
	// already pin down.
	//
	// Exceeding it ends the association (the peer is misbehaving or the limit is
	// wrong); there is no DIMSE status that means "your message was too big".
	MaxDatasetSize int
	// Admit decides whether an association may proceed. It runs once per
	// association, after the A-ASSOCIATE-RQ is read and the AE titles are
	// checked, with AssocInfo describing the requestor. User identity is not
	// verified yet: an Admit decision sees what the peer claims, not what it
	// proved. Return nil to accept. Return a *RejectError to reject with that
	// result/source/reason; return any other error to reject as transient /
	// service-provider-ACSE / no-reason-given.
	//
	// The returned func, if non-nil, runs once when the association ends — the
	// same shape as the cancel func from context.WithCancel, and the place a
	// slot taken during admission is given back. It also runs when a handler
	// panics.
	//
	// This hook replaces the per-AE and per-host limits the other stacks expose
	// as configuration (dcm4che Device.setLimitAssociationsInitiatedBy,
	// pynetdicom's AE.maximum_associations): who gets in is policy, and the
	// application is the only layer that knows about tenants, licences and
	// maintenance windows. Two shapes cover everything:
	//
	//	// Reject fast when a per-AE quota is used up.
	//	Admit: func(ctx context.Context, info ae.AssocInfo) (func(), error) {
	//		key := info.CallingAETitle
	//		if !quota.TryTake(key) {
	//			return nil, ae.RejectLocalLimit(fmt.Errorf("%s: quota exhausted", key))
	//		}
	//		return func() { quota.Put(key) }, nil
	//	}
	//
	//	// Or queue: block until a slot frees up. Nothing is rejected, and no
	//	// goroutine is parked on a job queue — this one just waits.
	//	Admit: func(ctx context.Context, info ae.AssocInfo) (func(), error) {
	//		if err := slots.Acquire(ctx, 1); err != nil { // semaphore.Weighted
	//			return nil, err
	//		}
	//		return func() { slots.Release(1) }, nil
	//	}
	//
	// Blocking is safe: Admit runs on the association's own goroutine, so the
	// accept loop and every other association keep running. Its context is
	// cancelled when HandshakeTimeout expires or the server shuts down, so a
	// queue can never outlive the handshake.
	Admit func(ctx context.Context, info AssocInfo) (release func(), err error)
	// Logger receives SCP lifecycle and debug events.
	// Nil falls back to context (WithLogger) then DiscardHandler (quiet).
	Logger *slog.Logger
}

func (c ServerConfig) withDefaults() ServerConfig {
	if c.AETitle == "" {
		c.AETitle = "GONETSCP"
	}
	if c.MaxPDULength == 0 {
		c.MaxPDULength = pdu.DefaultMaxPDULength
	}
	if c.ImplementationClassUID == "" {
		c.ImplementationClassUID = ImplementationClassUID
	}
	if c.ImplementationVersionName == "" {
		c.ImplementationVersionName = ImplementationVersionName
	}
	switch {
	case c.HandshakeTimeout == 0:
		c.HandshakeTimeout = DefaultHandshakeTimeout
	case c.HandshakeTimeout < 0:
		c.HandshakeTimeout = 0 // explicitly unbounded
	}
	if c.IdleTimeout < 0 {
		c.IdleTimeout = 0
	}
	return c
}

func (c ServerConfig) logger(ctx context.Context) *slog.Logger {
	return resolveAELogger(ctx, c.Logger)
}

// Serve accepts associations on ln until ctx is cancelled. Cancelling closes
// the listener and every association still running, and Serve returns once they
// have ended.
//
// It is the shorthand for a server nobody needs a handle on; NewServer returns
// one that can report and end the associations it is serving.
func Serve(ctx context.Context, ln net.Listener, cfg ServerConfig) error {
	return NewServer(cfg).Serve(ctx, ln)
}

// ListenAndServeTLS listens on addr with cfg.TLS and serves associations.
func ListenAndServeTLS(ctx context.Context, addr string, cfg ServerConfig) error {
	return NewServer(cfg).ListenAndServeTLS(ctx, addr)
}

func handleAssociation(ctx context.Context, conn net.Conn, cfg ServerConfig, srv *Server) error {
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentSCP)

	// The registry entry covers the whole connection, including the handshake:
	// a peer that is taking its time to send an A-ASSOCIATE-RQ holds a slot and
	// is exactly what an operator looking at a full server wants to see.
	assoc := srv.track(conn)
	defer srv.untrack(assoc)

	// Before an association exists there is nothing to abort, so a shutdown
	// just closes the connection — that is what unblocks a peer that never sent
	// an A-ASSOCIATE-RQ and is sitting in a read this package cannot interrupt.
	// Once the association is up, the defer below takes over and says goodbye
	// properly.
	stopHandshakeWatch := closeOnDone(ctx, conn)
	defer stopHandshakeWatch()
	defer func() {
		// The server's context being cancelled ends this association, and the
		// peer is told so. Closing the socket instead reads at the far end as a
		// network fault — the one thing a peer cannot tell apart from a pulled
		// cable, and therefore the thing it retries.
		if ctx.Err() != nil {
			_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00})
		}
		_ = conn.Close()
	}()

	// Declared up here so the panic handler below can name the peer; the
	// closure reads whatever value info holds at the moment of the panic.
	var info AssocInfo
	// A panic is a bug, not a DIMSE outcome. Report it with its stack and end
	// this association: A-ABORT is the only honest answer, because after an
	// unexpected panic the SCP cannot vouch for its own state, and a DIMSE
	// failure status would claim the association is still usable. The process
	// and every other association keep running — the same line net/http takes
	// per connection in conn.serve.
	//
	// This is the whole of our panic handling: one defer at the boundary,
	// instead of a recover() around every handler. Handlers signal expected
	// failures by returning a status; panicking through a status code would
	// merge the two kinds of failure that Go keeps apart.
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		cfg.logger(ctx).Error("scp: recovered panic, aborting association",
			gonetdicom.AttrRemote, conn.RemoteAddr().String(),
			gonetdicom.AttrCallingAE, info.CallingAETitle,
			"panic", p, "stack", string(debug.Stack()),
		)
		_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02})
	}()

	// Negotiation runs under one deadline covering the whole handshake; it is
	// cleared before the association proper starts.
	if cfg.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(cfg.HandshakeTimeout))
	}

	raw, err := readPDUConn(ctx, conn)
	if err != nil {
		// Nothing is negotiated yet, so there is no association to end — but a
		// PDU that does not decode is still worth an A-ABORT: it is the only way
		// to tell the peer that its bytes, rather than the network, were the
		// problem.
		return abortUndecodable(ctx, conn, err)
	}
	rq, ok := raw.(*pdu.AAssociateRQ)
	if !ok {
		_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
		return fmt.Errorf("ae: expected A-ASSOCIATE-RQ, got %T", raw)
	}

	// Negotiation answers are decided once, before either the AssocInfo
	// snapshot or the A-ASSOCIATE-AC is built, so the two can never disagree.
	answers := answerUserItems(cfg, rq.UserInformation)

	// Peer identity is on the context from here on, so OnUserIdentity and
	// every DIMSE handler can authenticate and audit the requestor
	// (AssocFromContext). Message-level fields are layered per message in
	// scpHandleMessage.
	info = AssocInfo{
		CallingAETitle: rq.CallingAETitle,
		CalledAETitle:  rq.CalledAETitle,
		RemoteAddr:     conn.RemoteAddr(),
		// The requestor's implementation identification ("" when it sent
		// none): the software identity dcm4che exposes as
		// Association.getRemoteImplClassUID / getRemoteImplVersionName.
		RemoteImplementationClassUID:    rq.UserInformation.ImplementationClassUID,
		RemoteImplementationVersionName: rq.UserInformation.ImplementationVersionName,
		AsyncOperationsWindow:           answers.AsyncOps,
	}
	ctx = WithAssoc(ctx, info)
	assoc.negotiatedInfo(info)

	if err := checkProtocolVersion(ctx, conn, cfg, rq); err != nil {
		return err
	}
	if err := checkAETitles(ctx, conn, cfg, rq); err != nil {
		return err
	}

	// Admission runs before user identity verification: quota and licensing
	// decisions are about whether there is room, and there is no reason to
	// spend verification work on a peer we are about to bounce anyway.
	release, err := admit(ctx, conn, cfg, info)
	if err != nil {
		return err
	}
	if release != nil {
		defer release()
	}

	var userIdentityAC *pdu.UserIdentityAC
	if rq.UserInformation.UserIdentityRQ != nil && cfg.OnUserIdentity != nil {
		ok, resp := cfg.OnUserIdentity(ctx, *rq.UserInformation.UserIdentityRQ)
		if !ok {
			_ = writePDUConn(ctx, conn, &pdu.AAssociateRJ{
				Result:           pdu.RejectResultTransient,
				Source:           pdu.RejectSourceServiceProviderACSE,
				ReasonDiagnostic: pdu.RejectReasonNoReasonGiven, // pynetdicom
			})
			return fmt.Errorf("%w: user identity verification failed", ErrRejected)
		}
		req := rq.UserInformation.UserIdentityRQ
		if req.PositiveResponseRequested && len(resp) > 0 &&
			req.Type >= pdu.UserIdentityKerberos && req.Type <= pdu.UserIdentityJWT {
			userIdentityAC = &pdu.UserIdentityAC{ServerResponse: append([]byte(nil), resp...)}
		}
	}

	accepted := map[byte]acceptedContext{}
	contexts := cfg.acceptedContexts()
	rqRoles := roleMap(rq.UserInformation.RoleSelections)
	acRoles := roleMap(cfg.RoleSelections)
	var acContexts []pdu.PresentationContextAC
	var replyRoles []pdu.RoleSelection

	for _, pc := range rq.PresentationContexts {
		syntaxes, ok := contexts.syntaxesFor(pc.AbstractSyntax)
		if !ok || len(pc.TransferSyntaxes) == 0 {
			acContexts = append(acContexts, pdu.PresentationContextAC{
				ID:     pc.ID,
				Result: 3, // abstract syntax not supported
			})
			continue
		}
		ts, ok := negotiateTransferSyntax(pc.TransferSyntaxes, syntaxes)
		if !ok {
			acContexts = append(acContexts, pdu.PresentationContextAC{
				ID:     pc.ID,
				Result: 4, // transfer syntaxes not supported
			})
			continue
		}

		asSCU, asSCP := false, true // default acceptor roles
		result := byte(0)
		if rqRole, rqOK := rqRoles[pc.AbstractSyntax]; rqOK {
			if acRole, acOK := acRoles[pc.AbstractSyntax]; acOK {
				out := negotiateRoles(rqRole.SCURole, rqRole.SCPRole, true, acRole.SCURole, acRole.SCPRole, true)
				asSCU, asSCP = out.AcSCU, out.AcSCP
				if !out.ReqSCU && !out.ReqSCP {
					result = 1 // user rejection
				} else {
					replyRoles = append(replyRoles, replyRole(pc.AbstractSyntax, rqRole, acRole))
				}
			}
			// else: proposed but acceptor has no configured support → default, no reply
		}

		acContexts = append(acContexts, pdu.PresentationContextAC{
			ID:             pc.ID,
			Result:         result,
			TransferSyntax: ts,
		})
		if result == 0 {
			accepted[pc.ID] = acceptedContext{
				AbstractSyntax: pc.AbstractSyntax,
				TransferSyntax: ts,
				AsSCU:          asSCU,
				AsSCP:          asSCP,
			}
		}
	}

	ac := &pdu.AAssociateAC{
		// The two AE title fields are reserved in an A-ASSOCIATE-AC: PS3.8 has
		// them sent back exactly as received and not tested by the receiver.
		// Substituting cfg.AETitle here would look more informative and be less
		// conformant; the Called AE Title is validated above instead.
		CalledAETitle:          rq.CalledAETitle,
		CallingAETitle:         rq.CallingAETitle,
		ProtocolVersion:        pdu.ProtocolVersion1,
		ApplicationContextName: rq.ApplicationContextName,
		PresentationContexts:   acContexts,
		UserInformation: pdu.UserInformation{
			MaxLength:                  cfg.MaxPDULength,
			ImplementationClassUID:     cfg.ImplementationClassUID,
			ImplementationVersionName:  cfg.ImplementationVersionName,
			AsyncOperationsWindow:      answers.AsyncOps,
			ExtendedNegotiations:       answers.ExtendedNegotiations,
			CommonExtendedNegotiations: answers.CommonExtended,
			RoleSelections:             replyRoles,
			UserIdentityAC:             userIdentityAC,
		},
	}
	if err := writePDUConn(ctx, conn, ac); err != nil {
		return err
	}
	// The association exists from here on, so ending it is the defer's job: it
	// can say goodbye before closing, which the handshake watcher cannot.
	stopHandshakeWatch()

	// Negotiation is over: drop its deadline and let IdleTimeout (if any) govern
	// the association from here on.
	if cfg.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Time{})
	}
	conn = withIdleTimeout(conn, cfg.IdleTimeout)

	peerMax := rq.UserInformation.MaxLength
	// One reader owns the read side from here on; Close must run before the
	// deferred conn.Close so the reading goroutine cannot be left blocked.
	r := newAssocReader(ctx, conn, cfg.MaxDatasetSize)
	defer r.Close()
	return scpLoop(ctx, conn, r, cfg, accepted, peerMax, rq.CallingAETitle)
}

// answerUserItems builds the acceptor's answers to the optional user-information
// negotiation items.
//
// Asynchronous operations: PS3.8 9.3.3.3.2 requires an acceptor that received a
// window to answer with its own, and 1/1 is the honest answer for a dispatch
// loop that takes one message at a time. Answering nothing would leave the
// requestor unable to tell whether the window applies.
//
// Extended negotiation: an answer exists per SOP Class, and a class the SCP
// handles without extended semantics simply gets none — absence is the
// protocol's way of saying "no extended negotiation here".
//
// Common extended negotiation is the same shape: the sub-item is a statement
// about how the SCP uses the class, so the acceptor either confirms the
// requestor's statement or says nothing. Nothing is not a refusal — the class
// stays usable the plain DIMSE way, which is the answer for every SCP that
// does not care which Service Class its storage classes belong to.
func answerUserItems(cfg ServerConfig, ui pdu.UserInformation) userItemAnswers {
	var out userItemAnswers
	if ui.AsyncOperationsWindow != nil {
		if cfg.AsyncOperationsWindow != nil {
			w := *cfg.AsyncOperationsWindow
			out.AsyncOps = &w
		} else {
			out.AsyncOps = &pdu.AsyncOperationsWindow{MaxInvoked: 1, MaxPerformed: 1}
		}
	}
	if cfg.CommonExtendedNegotiation != nil {
		for _, proposed := range ui.CommonExtendedNegotiations {
			if !cfg.CommonExtendedNegotiation(proposed) {
				continue
			}
			out.CommonExtended = append(out.CommonExtended, pdu.CommonExtendedNegotiation{
				SOPClassUID:              proposed.SOPClassUID,
				ServiceClassUID:          proposed.ServiceClassUID,
				RelatedGeneralSOPClasses: append([]string(nil), proposed.RelatedGeneralSOPClasses...),
			})
		}
	}
	if cfg.ExtendedNegotiation == nil || len(ui.ExtendedNegotiations) == 0 {
		return out
	}
	answers := make([]pdu.ExtendedNegotiation, 0, len(ui.ExtendedNegotiations))
	for _, proposed := range ui.ExtendedNegotiations {
		info, ok := cfg.ExtendedNegotiation(proposed.SOPClassUID, proposed.Info)
		if !ok {
			continue
		}
		answers = append(answers, pdu.ExtendedNegotiation{
			SOPClassUID: proposed.SOPClassUID,
			Info:        info,
		})
	}
	out.ExtendedNegotiations = answers
	return out
}

// userItemAnswers is the acceptor's answers to the optional user-information
// negotiation items. It is one struct rather than three return values because
// all three travel together: they are decided in one pass, used in one
// A-ASSOCIATE-AC, and the associate-step snapshot of AsyncOps has to agree with
// what the AC says.
type userItemAnswers struct {
	AsyncOps             *pdu.AsyncOperationsWindow
	ExtendedNegotiations []pdu.ExtendedNegotiation
	CommonExtended       []pdu.CommonExtendedNegotiation
}

// drainInbound reads and discards whatever the peer has already sent, without
// waiting for anything that has not arrived.
//
// Closing a socket that still holds unread data makes the stack send RST rather
// than FIN, and a reset discards the peer's receive buffer — including the
// A-RELEASE-RP that was just written into it. That is reachable in practice:
// PS3.8 7.2.2 forbids the requestor from sending anything but A-ABORT after
// A-RELEASE-RQ, but a requestor that was already answering a sub-operation
// sends that response anyway, and closing promptly would turn a correct release
// into "connection reset by peer" at the far end.
//
// The read deadline is in the past, so this costs a couple of non-blocking reads
// and never adds latency: a well-behaved peer has sent nothing, the first read
// returns a timeout, and the association closes as before.
func drainInbound(conn net.Conn) {
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		return
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	buf := make([]byte, 4096)
	// Bounded so a peer that keeps writing cannot hold the connection open.
	for i := 0; i < 64; i++ {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

func scpLoop(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, callingAE string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		it := r.next(ctx)
		switch {
		case it.Err != nil:
			// A command set that arrived but does not parse is a protocol error,
			// and A-ABORT is the only way to say so (PS3.8 9.3.4). Closing the
			// socket instead reads as a network fault, so the peer retries the
			// same bytes against a server that will reject them identically.
			//
			// The same goes one layer down: a PDU that does not decode at all —
			// an unknown type, a body its type does not explain — reaches here
			// through the reader, and gets the same answer.
			if errors.Is(it.Err, errMalformedCommand) {
				_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
			}
			return abortUndecodable(ctx, conn, it.Err)
		case it.Control != nil:
			switch p := it.Control.(type) {
			case *pdu.AReleaseRQ:
				if err := writePDUConn(ctx, conn, &pdu.AReleaseRP{}); err != nil {
					return err
				}
				drainInbound(conn)
				return nil
			case *pdu.AAbort:
				return nil
			default:
				_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
				return fmt.Errorf("ae: unexpected PDU %T", p)
			}
		}
		if err := scpHandleMessage(ctx, conn, r, cfg, accepted, peerMax, it.ContextID, it.Command, it.Body, callingAE); err != nil {
			return err
		}
	}
}

// scpHandleMessage dispatches one received DIMSE message on Command Field
// (0000,0100).
//
// Dispatch must not be done by trying each decoder in turn: the decoders only
// reject a Command Field that is present and wrong, so a command set with no
// Command Field at all satisfies every one of them and would be handled as
// whichever was tried first.
func scpHandleMessage(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, cmd []byte, body *msgBody, callingAE string) error {
	// Closing is unconditional on the way out: a handler that ignored the
	// dataset, and a handler that panicked, both leave fragments unread, and
	// the reader has to be told to stop keeping them. On the panic path there
	// is no waiting — the association is going away anyway, and a peer that
	// stopped mid-message would turn a crash into a hang.
	if body != nil {
		defer func() {
			if p := recover(); p != nil {
				body.Discard()
				panic(p)
			}
			_ = body.Close()
		}()
	}

	// Layer the message-level context onto the association identity already
	// present, so every handler below sees the complete AssocInfo.
	info, _ := AssocFromContext(ctx)
	info.PresentationContextID = pcid
	if ac, ok := accepted[pcid]; ok {
		info.AbstractSyntax = ac.AbstractSyntax
		info.TransferSyntax = ac.TransferSyntax
	}
	ctx = WithAssoc(ctx, info)

	// A command set we cannot parse has no Message ID to answer and no status
	// that means "your bytes made no sense", so the association ends with
	// A-ABORT (PS3.8 9.3.4, and what pynetdicom does on a decode failure).
	// Closing the socket instead would leave the peer guessing whether the
	// problem was ours or the network's.
	abort := func(err error) error {
		_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
		return fmt.Errorf("ae: malformed DIMSE message: %w", err)
	}

	field, err := dimse.PeekCommandField(cmd)
	if err != nil {
		return abort(err)
	}

	// C-STORE is the only request whose dataset can be large enough to matter,
	// so it keeps the stream. Every other command carries an identifier, which
	// is small, and reading it whole leaves those decoders as they were.
	var ds []byte
	if field != dimse.CommandCStoreRQ && body != nil {
		ds, err = body.Bytes()
		if err != nil {
			return fmt.Errorf("ae: read message dataset: %w", err)
		}
	}

	switch field {
	case dimse.CommandCEchoRQ:
		echoRQ, err := dimse.DecodeCEchoRQ(cmd)
		if err != nil {
			return abort(err)
		}
		// C-ECHO-RQ names no SOP Class: the Verification class is implied by
		// the presentation context it arrived on. Answering success on a
		// context the acceptor never accepted would tell the peer that an
		// association it cannot actually use is working.
		//
		// A context accepted for some other class is left alone: pynetdicom's
		// Verification SCP answers success there too, and the peer's own
		// binding is the only thing that can be wrong.
		status := dimse.StatusSuccess
		if _, ok := accepted[pcid]; !ok {
			status = dcmstatus.SOPClassNotSupported
		}
		rsp, err := (&dimse.CEchoRSP{
			MessageIDBeingRespondedTo: echoRQ.MessageID,
			Status:                    status,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)

	case dimse.CommandCStoreRQ:
		rq, err := dimse.DecodeCStoreRQ(cmd)
		if err != nil {
			return abort(err)
		}
		status := dcmstatus.SOPClassNotSupported
		if abs, ok := accepted[pcid]; ok && abs.AbstractSyntax == rq.AffectedSOPClassUID {
			if cfg.OnCStore != nil {
				status = cfg.OnCStore(ctx, newInboundStoreEvent(rq, body, abs.TransferSyntax))
			} else {
				status = dimse.StatusSuccess
			}
		}
		// Finish the dataset before answering: the rest of it is still on the
		// wire, and only once it has been consumed does the reader know
		// whether it hit the size cap. Answering first would claim we read a
		// message that was actually cut short.
		if body != nil {
			_ = body.Close()
		}
		if err := body.readErr(); err != nil {
			// No PDU here: the message never completed, so there is no state
			// to abort from, and the peer must not read a half-conversation.
			return err
		}
		rsp, err := (&dimse.CStoreRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			AffectedSOPInstanceUID:    rq.AffectedSOPInstanceUID,
			Status:                    status,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)

	case dimse.CommandCFindRQ:
		rq, err := dimse.DecodeCFindRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleFind(ctx, conn, r, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandCMoveRQ:
		rq, err := dimse.DecodeCMoveRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleMove(ctx, conn, r, cfg, accepted, peerMax, pcid, rq, ds, callingAE)

	case dimse.CommandCGetRQ:
		rq, err := dimse.DecodeCGetRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleGet(ctx, conn, r, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandCCancelRQ:
		// A cancel for an operation that is no longer running: the response
		// stream consumes the ones it is waiting for (see assocReader.pollStop),
		// so anything reaching here is late. Ignoring it is what PS3.7 allows;
		// tearing the association down over it is not.
		if c, err := dimse.DecodeCCancelRQ(cmd); err == nil {
			cfg.logger(ctx).Debug("ignoring C-CANCEL-RQ for an operation that is not running",
				gonetdicom.AttrMessageID, c.MessageIDBeingRespondedTo)
		}
		return nil

	case dimse.CommandNActionRQ:
		rq, err := dimse.DecodeNActionRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNAction(ctx, conn, r, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandNEventReportRQ:
		rq, err := dimse.DecodeNEventReportRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNEventReport(ctx, conn, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandNGetRQ:
		rq, err := dimse.DecodeNGetRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNGet(ctx, conn, cfg, accepted, peerMax, pcid, rq)

	case dimse.CommandNSetRQ:
		rq, err := dimse.DecodeNSetRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNSet(ctx, conn, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandNCreateRQ:
		rq, err := dimse.DecodeNCreateRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNCreate(ctx, conn, cfg, accepted, peerMax, pcid, rq, ds)

	case dimse.CommandNDeleteRQ:
		rq, err := dimse.DecodeNDeleteRQ(cmd)
		if err != nil {
			return abort(err)
		}
		return scpHandleNDelete(ctx, conn, cfg, accepted, peerMax, pcid, rq)

	default:
		_ = writePDUConn(ctx, conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
		return fmt.Errorf("ae: unsupported DIMSE command %s (0x%04x)", dimse.CommandName(field), field)
	}
}

func scpHandleFind(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CFindRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	if !ok || ac.AbstractSyntax != rq.AffectedSOPClassUID {
		rsp, err := (&dimse.CFindRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    dcmstatus.SOPClassNotSupported,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
		if err != nil {
			return fmt.Errorf("ae: decode C-FIND identifier: %w", err)
		}
		ident = decoded
	}
	if status := cfg.validateIdentifier(ctx, OperationFind, rq.AffectedSOPClassUID, ident, ac.TransferSyntax); status != 0 {
		return refuseFindIdentifier(ctx, conn, pcid, peerMax, rq.MessageID, rq.AffectedSOPClassUID, status)
	}

	// The handler gets a cancellable context of its own: a peer cancel or the
	// end of the association reaches it as a cause, not as a boolean it has to
	// remember to poll (cancel.go).
	hctx, stopHandler := context.WithCancelCause(ctx)
	defer stopHandler(nil)

	// A handler that yields nothing is a query with no matches, and one that
	// is absent answers Success; both end below with a bare final response,
	// which is what pynetdicom does when the generator is empty (:398-402).
	seq := FindSeq(nil)
	if cfg.OnCFind != nil {
		if handlerSeq := cfg.OnCFind(hctx, FindRequest{
			QueryModel:     rq.AffectedSOPClassUID,
			Identifier:     ds,
			IdentifierData: ident,
			Priority:       rq.Priority,
		}); handlerSeq != nil {
			seq = handlerSeq
		}
	}

	for m, handlerErr := range seq {
		// A stop is checked before every response, the first one included:
		// the request can be cancelled while the handler is still scanning,
		// and a match produced after that point is one the peer has said it
		// will discard.
		switch r.pollStop(rq.MessageID) {
		case stopAssociation:
			stopHandler(ErrPeerReleased)
			return nil
		case stopCancelled:
			stopHandler(ErrPeerCancelled)
			return writeFindRSP(ctx, conn, pcid, peerMax, rq, dimse.StatusCancel, nil)
		}

		if handlerErr != nil {
			// pynetdicom answers a handler that raises with 0xC311 and ends
			// the operation (:311). The error itself stays local: a Q/R
			// response has nowhere to put it, and the peer's remedy is the
			// same either way.
			return writeFindRSP(ctx, conn, pcid, peerMax, rq, dcmstatus.FindUnableToProcess, nil)
		}

		hasDS := m.Identifier != nil && dimse.IsPending(m.Status)
		var payload []byte
		if hasDS {
			encoded, err := m.Identifier.Encode(godicom.UID(ac.TransferSyntax))
			if err != nil || len(encoded) == 0 {
				// :376 — the identifier will not encode, so this match cannot
				// be sent and the first response that would have carried it
				// is the failure itself.
				return writeFindRSP(ctx, conn, pcid, peerMax, rq, dcmstatus.FindIdentifierNotEncodable, nil)
			}
			payload = encoded
		}
		if err := writeFindRSP(ctx, conn, pcid, peerMax, rq, m.Status, payload); err != nil {
			return err
		}
		if !dimse.IsPending(m.Status) {
			// Only a Pending status keeps a C-FIND open (PS3.4 Table C.4-2:
			// 0xFF00 and 0xFF01). Anything else the handler yielded — Success,
			// Failure, Cancel — is the last response of the operation.
			return nil
		}
	}

	// The handler ran out of matches without saying how the operation ends.
	return writeFindRSP(ctx, conn, pcid, peerMax, rq, dimse.StatusSuccess, nil)
}

// writeFindRSP sends one C-FIND-RSP for rq. A non-nil payload is the response
// Identifier and sets the CommandDataSetType that announces it.
func writeFindRSP(ctx context.Context, conn net.Conn, pcid byte, peerMax uint32, rq *dimse.CFindRQ, status uint16, payload []byte) error {
	rsp, err := (&dimse.CFindRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		AffectedSOPClassUID:       rq.AffectedSOPClassUID,
		Status:                    status,
		HasDataset:                payload != nil,
	}).Encode()
	if err != nil {
		return fmt.Errorf("ae: encode C-FIND-RSP: %w", err)
	}
	return writeMessage(ctx, conn, pcid, rsp, payload, peerMax)
}

func scpHandleMove(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CMoveRQ, ds []byte, callingAE string) error {
	ac, ok := accepted[pcid]
	if !ok || ac.AbstractSyntax != rq.AffectedSOPClassUID {
		rsp, err := (&dimse.CMoveRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    dcmstatus.SOPClassNotSupported,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
		if err != nil {
			return fmt.Errorf("ae: decode C-MOVE identifier: %w", err)
		}
		ident = decoded
	}
	if status := cfg.validateIdentifier(ctx, OperationMove, rq.AffectedSOPClassUID, ident, ac.TransferSyntax); status != 0 {
		return refuseMoveIdentifier(ctx, conn, pcid, peerMax, rq.MessageID, rq.AffectedSOPClassUID, status)
	}

	var plan MovePlan
	if cfg.OnCMove != nil {
		plan = cfg.OnCMove(ctx, MoveRequest{
			QueryModel:      rq.AffectedSOPClassUID,
			MoveDestination: rq.MoveDestination,
			Identifier:      ds,
			IdentifierData:  ident,
			Priority:        rq.Priority,
		})
	}
	if plan.Destination != nil && plan.Destination.Addr == "" {
		// pynetdicom service_class._c_move returns 0xA801 when the first
		// yield is (None, None), before it asks how many matches there are.
		return writeRetrieveResponses(ctx, conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, []RetrieveMatch{{
			Status: dcmstatus.MoveDestinationUnknown,
		}})
	}
	if len(plan.Stores) > 0 {
		if err := scpPerformMoveStores(ctx, conn, r, cfg, peerMax, pcid, ac.TransferSyntax, rq, callingAE, plan.Destination, plan.Stores); err != nil {
			fail := []RetrieveMatch{{
				Status: dcmstatus.MoveDestinationUnknown,
				SubOperations: dimse.SubOperations{
					Failed: subOpCount(len(plan.Stores)), Present: true,
				},
			}}
			if len(plan.Responses) > 0 {
				fail = plan.Responses
			}
			return writeRetrieveResponses(ctx, conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, fail)
		}
		return nil
	}
	if len(plan.Responses) == 0 {
		plan.Responses = []RetrieveMatch{{Status: dimse.StatusSuccess}}
	}
	return writeRetrieveResponses(ctx, conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, plan.Responses)
}

// subOpCount narrows a sub-operation count to the US field that carries it
// (PS3.7 Table 9.1-4), saturating at 65535 rather than wrapping.
//
// Saturating loses precision; wrapping loses monotonicity, and monotonicity is
// what an SCU reads these counts for. A Remaining that goes 0 then 65535 is not
// an inaccurate progress report, it is a contradictory one.
func subOpCount(n int) uint16 {
	if n > math.MaxUint16 {
		return math.MaxUint16
	}
	if n < 0 {
		return 0
	}
	return uint16(n)
}

// movePresentationContexts is qrscp handle_move:
// contexts = list({build_context(sop_class_uid, transfer_syntax_uid)}).
// build_context(sop, ts) offers only that transfer syntax. A StoreRequest
// with no TransferSyntax offers Implicit VR Little Endian, which is the
// syntax this path proposed before a stored syntax was available.
func movePresentationContexts(stores []StoreRequest) ([]PresentationContext, error) {
	type pair struct{ sop, ts string }
	seen := map[pair]struct{}{}
	var pcs []PresentationContext
	for _, store := range stores {
		if store.AffectedSOPClassUID == "" {
			continue
		}
		ts := store.TransferSyntax
		if ts == "" {
			ts = pdu.ImplicitVRLittleEndian
		}
		key := pair{store.AffectedSOPClassUID, ts}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		// ID left unset: Dial assigns the free odd IDs.
		pcs = append(pcs, PresentationContext{
			AbstractSyntax:   store.AffectedSOPClassUID,
			TransferSyntaxes: []string{ts},
		})
	}
	if len(pcs) == 0 {
		return nil, fmt.Errorf("ae: C-MOVE stores missing SOP Class UID")
	}
	return pcs, nil
}

func scpPerformMoveStores(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, peerMax uint32, pcid byte, transferSyntax string, rq *dimse.CMoveRQ, moveOriginatorAE string, override *MoveDestination, stores []StoreRequest) error {
	var dest MoveDestination
	var ok bool
	if override != nil {
		dest = *override
		ok = dest.Addr != ""
	} else {
		dest, ok = cfg.MoveDestinations[rq.MoveDestination]
	}
	if !ok || dest.Addr == "" {
		return fmt.Errorf("ae: unknown Move Destination %q", rq.MoveDestination)
	}
	called := dest.CalledAE
	if called == "" {
		called = rq.MoveDestination
	}
	calling := dest.CallingAE
	if calling == "" {
		calling = cfg.AETitle
	}

	pcs, err := movePresentationContexts(stores)
	if err != nil {
		return err
	}

	prepared := make([]StoreRequest, len(stores))
	for i, store := range stores {
		if store.MoveOriginatorApplicationEntityTitle == "" {
			store.MoveOriginatorApplicationEntityTitle = moveOriginatorAE
		}
		if store.MoveOriginatorMessageID == 0 {
			store.MoveOriginatorMessageID = rq.MessageID
		}
		prepared[i] = store
	}

	// Counted in int, narrowed only where the wire demands it. The four
	// Sub-Operations counts are US on the wire (PS3.7 Table 9.1-4), so 65535 is a
	// real ceiling — but C-MOVE itself has no such limit, and counting in uint16
	// meant the ceiling was crossed silently and in the wrong direction: at 65536
	// stores `total` was 0, so the first Pending response told the SCU nothing was
	// outstanding, and the next one computed total-done as 0-1 and said 65535 were.
	// An SCU treating Remaining == 0 as the end of the transfer stopped there.
	total := len(prepared)
	if total > math.MaxUint16 {
		cfg.logger(ctx).Warn("scp C-MOVE sub-operation counts saturated",
			gonetdicom.AttrMessageID, rq.MessageID,
			"sub_operations", total,
			"limit", math.MaxUint16,
		)
	}
	var (
		mu       sync.Mutex
		results  = newSubOperations(total)
		writeErr error
	)

	writePending := func() error {
		s := results.counts()
		s.Remaining = subOpCount(total - results.attempted())
		return writeRetrieveResponses(ctx, conn, pcid, peerMax, transferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, []RetrieveMatch{{
			Status:        dimse.StatusPending,
			SubOperations: s,
		}})
	}

	if err := writePending(); err != nil {
		return err
	}

	// record tallies one sub-operation and reports progress. The status is
	// classified, not merely compared against Success: a warning means the
	// instance arrived, and counting it as a failure turned a transfer worth
	// 0xB000 into 0xA702 — telling the SCU nothing was retrieved when in fact
	// every instance was.
	record := func(store StoreRequest, res *StoreResult, err error) {
		mu.Lock()
		defer mu.Unlock()
		if writeErr != nil {
			return
		}
		var status uint16
		if res != nil {
			status = res.Status
		}
		results.record(store.AffectedSOPInstanceUID, err, status)
		writeErr = writePending()
	}

	maxAssoc := dest.MaxAssociations
	if maxAssoc <= 0 {
		maxAssoc = 1
	}
	if maxAssoc > len(prepared) {
		maxAssoc = len(prepared)
	}

	dialCfg := Config{
		AETitle:              calling,
		PresentationContexts: pcs,
		TLS:                  cfg.TLS,
		Logger:               cfg.Logger,
	}

	// A C-MOVE's sub-operations run on a different association from the one the
	// request arrived on, so a C-CANCEL-RQ or an A-RELEASE-RQ lands on `r` while
	// this function is busy dialling and storing. Between two sub-operations is
	// the only place one can be honoured: PS3.4 C.4.2.1.4 requires the batch to
	// stop, not the operation to be abandoned — the C-STORE already on the wire
	// is finished, and what comes after it is never started.
	var stopped atomic.Int32
	stopSeen := func() stopReason { return stopReason(stopped.Load()) }
	rememberStop := func(reason stopReason) {
		if reason != stopNone {
			stopped.CompareAndSwap(0, int32(reason))
		}
	}
	pollStopBeforeStore := func() stopReason {
		if reason := stopSeen(); reason != stopNone {
			return reason
		}
		reason := r.pollStop(rq.MessageID)
		rememberStop(reason)
		return reason
	}

	if maxAssoc == 1 {
		assoc, err := Dial(ctx, dialCfg, dest.Addr, called)
		if err != nil {
			return fmt.Errorf("ae: dial Move Destination: %w", err)
		}
		defer func() { _ = assoc.Release(ctx) }()
	serial:
		for _, store := range prepared {
			switch pollStopBeforeStore() {
			case stopAssociation:
				return nil
			case stopCancelled:
				break serial
			}
			res, err := assoc.CStore(ctx, store)
			record(store, res, err)
			if writeErr != nil {
				return writeErr
			}
		}
	} else {
		// Handed over one at a time, by a dispatcher that polls before each
		// hand-off. Pre-filling the channel — which is what this did — puts the
		// whole remainder beyond the reach of a stop: the workers would take it
		// from a buffer that was filled before the stop arrived.
		jobs := make(chan StoreRequest)
		var wg sync.WaitGroup
		var live atomic.Int32
		live.Store(int32(maxAssoc))
		workersGone := make(chan struct{})

		for w := 0; w < maxAssoc; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if live.Add(-1) == 0 {
						close(workersGone)
					}
				}()
				assoc, err := Dial(ctx, dialCfg, dest.Addr, called)
				if err != nil {
					return
				}
				defer func() { _ = assoc.Release(ctx) }()
				for store := range jobs {
					// The job may have been handed over before the stop
					// arrived; starting it now would be a sub-operation the
					// peer asked us not to begin.
					if stopSeen() != stopNone {
						return
					}
					res, err := assoc.CStore(ctx, store)
					record(store, res, err)
				}
			}()
		}

	dispatch:
		for _, store := range prepared {
			if pollStopBeforeStore() != stopNone {
				break
			}
			select {
			case jobs <- store:
			case <-workersGone:
				break dispatch
			}
		}
		close(jobs)
		wg.Wait()
	}

	mu.Lock()
	reason := stopSeen()
	if reason == stopNone {
		// A worker whose Dial failed never reached its stores; those are
		// failures too, or the counts would not add up to the number of
		// matches. A batch the requestor stopped is the opposite case: the
		// sub-operations it never got to are Remaining, not Failed (PS3.4
		// Table C.4-3), and calling them failures would tell the SCU that
		// instances it still wants could not be stored.
		results.recordMissing()
	}
	final := results.finalStatus()
	subOps := results.counts()
	failedList := results.failedIdentifier()
	untried := total - results.attempted()
	mu.Unlock()

	switch reason {
	case stopAssociation:
		// The association is going away and the peer is waiting for its
		// A-RELEASE-RP: the loop owns that, so nothing is written from here —
		// including the error from a Pending response that failed to go out
		// because the peer had already stopped reading.
		return nil
	case stopCancelled:
		return writeRetrieveResponses(ctx, conn, pcid, peerMax, transferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true,
			[]RetrieveMatch{results.cancelMatch(untried)})
	}
	if writeErr != nil {
		return writeErr
	}

	// The status is decided on the real counts, not the saturated ones: a batch
	// where everything past 65535 failed is still a warning, whatever the reported
	// number of failures says.
	return writeRetrieveResponses(ctx, conn, pcid, peerMax, transferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, []RetrieveMatch{{
		Status:        final,
		SubOperations: subOps,
		Identifier:    failedList,
	}})
}

func scpHandleGet(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CGetRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	if !ok || ac.AbstractSyntax != rq.AffectedSOPClassUID {
		rsp, err := (&dimse.CGetRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    dcmstatus.SOPClassNotSupported,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
		if err != nil {
			return fmt.Errorf("ae: decode C-GET identifier: %w", err)
		}
		ident = decoded
	}
	if status := cfg.validateIdentifier(ctx, OperationGet, rq.AffectedSOPClassUID, ident, ac.TransferSyntax); status != 0 {
		return refuseGetIdentifier(ctx, conn, pcid, peerMax, rq.MessageID, rq.AffectedSOPClassUID, status)
	}

	hctx, stopHandler := context.WithCancelCause(ctx)
	defer stopHandler(nil)

	// A handler that is absent answers Success, and one that returns a nil
	// iterator says the same thing: there is nothing to retrieve and nothing to
	// account for.
	var seq iter.Seq2[*RetrieveOp, error]
	if cfg.OnCGet != nil {
		seq = cfg.OnCGet(hctx, GetRequest{
			QueryModel:     rq.AffectedSOPClassUID,
			Identifier:     ds,
			IdentifierData: ident,
			Priority:       rq.Priority,
		})
	}
	results := newSubOperations(0)
	var (
		sawPlan    bool
		storeMsgID uint16 = 1
	)
	write := func(m RetrieveMatch) error {
		return writeRetrieveResponses(ctx, conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, false, []RetrieveMatch{m})
	}
	if seq == nil {
		return write(RetrieveMatch{Status: dimse.StatusSuccess})
	}
	// cancel ends the operation the way the peer asked: the sub-operations it
	// never got to are remaining, not failed (PS3.4 C.4.3.1.4).
	cancel := func() error {
		stopHandler(ErrPeerCancelled)
		return write(results.cancelMatch(results.total - results.attempted()))
	}

	for op, handlerErr := range seq {
		if handlerErr != nil {
			// pynetdicom answers a C-GET handler that raises with 0xC411 and
			// ends the operation (:1777).
			return write(RetrieveMatch{Status: dcmstatus.GetUnableToProcess})
		}
		if op == nil || op.Plan == nil && op.Store == nil && op.Result == nil {
			// An item that names no operation cannot be acted on, and the
			// sub-operation count is what this most likely is: pynetdicom
			// answers a first yield it cannot read with 0xC413 (:1739).
			return write(RetrieveMatch{Status: dcmstatus.GetUnrecognizedOperation})
		}
		switch {
		case op.Plan != nil:
			// Every count in every response is computed from the plan, so it
			// has to arrive once, before anything else, and be a number the
			// four US fields can carry.
			if sawPlan || op.Plan.Total < 0 {
				return write(RetrieveMatch{Status: dcmstatus.GetUnrecognizedOperation})
			}
			if op.Plan.Total > math.MaxUint16 {
				return write(RetrieveMatch{Status: dcmstatus.GetTooManySubOperations})
			}
			results = newSubOperations(op.Plan.Total)
			sawPlan = true
		case op.Store != nil:
			if !sawPlan {
				return write(RetrieveMatch{Status: dcmstatus.GetUnrecognizedOperation})
			}
			// Between two sub-operations is the only place a stop can be
			// honoured: the C-STORE-RQ already on the wire has to be answered,
			// or the next read would be one message behind.
			switch r.pollStop(rq.MessageID) {
			case stopAssociation:
				stopHandler(ErrPeerReleased)
				return nil
			case stopCancelled:
				return cancel()
			}
			status, cancelled, err := scpPerformGetStore(ctx, conn, r, accepted, peerMax, *op.Store, storeMsgID, rq.MessageID)
			if err != nil {
				if errors.Is(err, errAssociationEnding) {
					return nil
				}
				return err
			}
			storeMsgID++
			results.record(op.Store.AffectedSOPInstanceUID, nil, status)
			op.Outcome = StoreOutcome{Status: status}
			if cancelled {
				return cancel()
			}
		case op.Result != nil:
			// The counts and the aggregate status are this package's to
			// report, so a response is reconciled as it is written rather than
			// after the fact.
			m := results.reconcileMatch(*op.Result)
			if err := write(m); err != nil {
				return err
			}
			if !dimse.IsPending(m.Status) {
				return nil
			}
		}
	}

	// The handler ran out of items without saying how the operation ends.
	// Without stores there is nothing to account for and the answer is a bare
	// Success; with them, the aggregate and the counts are the measurement.
	if results.total == 0 {
		return write(RetrieveMatch{Status: dimse.StatusSuccess})
	}
	for _, m := range results.reconcile(nil) {
		if err := write(m); err != nil {
			return err
		}
	}
	return nil
}

// scpPerformGetStore writes one C-STORE-RQ for a C-GET on the association the
// request arrived on, and returns the status the peer answered with.
//
// cancelled reports that a C-CANCEL for the C-GET overtook the response. The
// response to a store already on the wire is owed, so the cancel is noted and
// honoured at the next sub-operation boundary; decoding it as a C-STORE-RSP is
// what used to fail the association.
func scpPerformGetStore(ctx context.Context, conn net.Conn, r *assocReader, accepted map[byte]acceptedContext, peerMax uint32, store StoreRequest, storeMsgID, getMsgID uint16) (status uint16, cancelled bool, err error) {
	storePCID, storeTS, ok := contextByAbstractAccepted(accepted, store.AffectedSOPClassUID)
	if !ok {
		return 0, false, fmt.Errorf("ae: C-GET store missing presentation context for %s", store.AffectedSOPClassUID)
	}
	payload := store.Dataset
	if store.Data != nil {
		encoded, err := store.Data.Encode(godicom.UID(storeTS))
		if err != nil {
			return 0, false, fmt.Errorf("ae: encode C-GET store dataset: %w", err)
		}
		payload = encoded
	}
	cmd, err := (&dimse.CStoreRQ{
		MessageID:                            storeMsgID,
		Priority:                             store.Priority,
		AffectedSOPClassUID:                  store.AffectedSOPClassUID,
		AffectedSOPInstanceUID:               store.AffectedSOPInstanceUID,
		MoveOriginatorApplicationEntityTitle: store.MoveOriginatorApplicationEntityTitle,
		MoveOriginatorMessageID:              store.MoveOriginatorMessageID,
		HasMoveOriginator:                    store.MoveOriginatorApplicationEntityTitle != "",
	}).Encode()
	if err != nil {
		return 0, false, err
	}
	if err := writeMessage(ctx, conn, storePCID, cmd, payload, peerMax); err != nil {
		return 0, false, err
	}
	for {
		_, rspCmd, _, err := r.message(ctx)
		if err != nil {
			return 0, false, err
		}
		if c, err := dimse.DecodeCCancelRQ(rspCmd); err == nil && c.MessageIDBeingRespondedTo == getMsgID {
			cancelled = true
			continue
		}
		rsp, err := dimse.DecodeCStoreRSP(rspCmd)
		if err != nil {
			return 0, false, fmt.Errorf("ae: decode C-STORE-RSP during C-GET: %w", err)
		}
		return rsp.Status, cancelled, nil
	}
}

// writeRetrieveResponses encodes and writes a C-MOVE/C-GET response stream.
//
// Stopping is the caller's decision, not this function's: a C-FIND polls
// between matches, a C-GET between sub-operations, and both have somewhere to
// put the answer. Here there is no such place — the responses arrive as a
// materialised slice and go out back to back, in microseconds, so a poll
// between two of them would be a race rather than an interruption: the peer's
// cancel cannot arrive in the window between two writes that are already
// queued. Polling here also had no caller it could fire for, since every
// caller passes one response at a time.
func writeRetrieveResponses(ctx context.Context, conn net.Conn, pcid byte, peerMax uint32, transferSyntax string, msgID uint16, sopClass string, isMove bool, matches []RetrieveMatch) error {
	for _, m := range matches {
		hasDS := m.Identifier != nil
		var (
			rsp []byte
			err error
		)
		if isMove {
			rsp, err = (&dimse.CMoveRSP{
				MessageIDBeingRespondedTo: msgID,
				AffectedSOPClassUID:       sopClass,
				Status:                    m.Status,
				HasDataset:                hasDS,
				SubOperations:             m.SubOperations,
			}).Encode()
		} else {
			rsp, err = (&dimse.CGetRSP{
				MessageIDBeingRespondedTo: msgID,
				AffectedSOPClassUID:       sopClass,
				Status:                    m.Status,
				HasDataset:                hasDS,
				SubOperations:             m.SubOperations,
			}).Encode()
		}
		if err != nil {
			return err
		}
		var payload []byte
		if hasDS {
			payload, err = m.Identifier.Encode(godicom.UID(transferSyntax))
			if err != nil {
				return fmt.Errorf("ae: encode retrieve identifier: %w", err)
			}
		}
		if err := writeMessage(ctx, conn, pcid, rsp, payload, peerMax); err != nil {
			return err
		}
		if !dimse.IsPending(m.Status) {
			return nil
		}
	}
	return nil
}

func contextByAbstractAccepted(accepted map[byte]acceptedContext, uid string) (byte, string, bool) {
	for id, ac := range accepted {
		if ac.AbstractSyntax == uid {
			return id, ac.TransferSyntax, true
		}
	}
	return 0, "", false
}

func writeMessage(ctx context.Context, conn net.Conn, pcid byte, command, dataset []byte, maxPDU uint32) error {
	logDIMSE(ctx, "send", pcid, command)
	warnUnexpectedStatus(ctx, command)
	pdus, err := pdu.FragmentMessage(pcid, command, dataset, maxPDU)
	if err != nil {
		return err
	}
	for _, p := range pdus {
		if err := writePDUConn(ctx, conn, p); err != nil {
			return err
		}
	}
	return nil
}

func scpHandleNAction(ctx context.Context, conn net.Conn, r *assocReader, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NActionRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	status := dcmstatus.SOPClassNotSupported
	result := ActionResult{
		Status:                 status,
		ActionTypeID:           rq.ActionTypeID,
		AffectedSOPClassUID:    rq.RequestedSOPClassUID,
		AffectedSOPInstanceUID: rq.RequestedSOPInstanceUID,
	}
	var push *EventReportRequest
	if ok && ac.AbstractSyntax == rq.RequestedSOPClassUID {
		var info *godicom.Dataset
		if len(ds) > 0 {
			decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
			if err != nil {
				return fmt.Errorf("ae: decode action information: %w", err)
			}
			info = decoded
		}
		if cfg.OnNAction != nil {
			result, push = cfg.OnNAction(ctx, ActionRequest{
				RequestedSOPClassUID:    rq.RequestedSOPClassUID,
				RequestedSOPInstanceUID: rq.RequestedSOPInstanceUID,
				ActionTypeID:            rq.ActionTypeID,
				ActionInformation:       ds,
				ActionInformationData:   info,
			})
		} else {
			result.Status = dimse.StatusSuccess
		}
		if result.AffectedSOPClassUID == "" {
			result.AffectedSOPClassUID = rq.RequestedSOPClassUID
		}
		if result.AffectedSOPInstanceUID == "" {
			result.AffectedSOPInstanceUID = rq.RequestedSOPInstanceUID
		}
		if result.ActionTypeID == 0 {
			result.ActionTypeID = rq.ActionTypeID
		}
	}

	reply := result.ActionReply
	if result.ActionReplyData != nil {
		if !ok {
			return fmt.Errorf("ae: cannot encode action reply without accepted context")
		}
		encoded, err := result.ActionReplyData.Encode(godicom.UID(ac.TransferSyntax))
		if err != nil {
			return fmt.Errorf("ae: encode action reply: %w", err)
		}
		reply = encoded
	}
	rsp, err := (&dimse.NActionRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		ActionTypeID:              result.ActionTypeID,
		AffectedSOPClassUID:       result.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    result.AffectedSOPInstanceUID,
		Status:                    result.Status,
		HasDataset:                len(reply) > 0,
	}).Encode()
	if err != nil {
		return err
	}
	if err := writeMessage(ctx, conn, pcid, rsp, reply, peerMax); err != nil {
		return err
	}
	if push == nil {
		return nil
	}
	if push.AsyncDestination != nil && push.AsyncDestination.Addr != "" {
		dest := *push.AsyncDestination
		report := *push
		report.AsyncDestination = nil
		go scpSendEventReportNewAssoc(cfg, dest, report)
		return nil
	}
	return scpSendEventReport(ctx, conn, r, accepted, peerMax, push)
}

func scpHandleNEventReport(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NEventReportRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	status := dcmstatus.SOPClassNotSupported
	if ok && ac.AbstractSyntax == rq.AffectedSOPClassUID {
		var info *godicom.Dataset
		if len(ds) > 0 {
			decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
			if err != nil {
				return fmt.Errorf("ae: decode event information: %w", err)
			}
			info = decoded
		}
		if cfg.OnNEventReport != nil {
			status = cfg.OnNEventReport(ctx, EventReportRequest{
				AffectedSOPClassUID:    rq.AffectedSOPClassUID,
				AffectedSOPInstanceUID: rq.AffectedSOPInstanceUID,
				EventTypeID:            rq.EventTypeID,
				EventInformation:       ds,
				EventInformationData:   info,
			})
		} else {
			status = dimse.StatusSuccess
		}
	}
	rsp, err := (&dimse.NEventReportRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		EventTypeID:               rq.EventTypeID,
		AffectedSOPClassUID:       rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    rq.AffectedSOPInstanceUID,
		Status:                    status,
	}).Encode()
	if err != nil {
		return err
	}
	return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
}

func scpSendEventReport(ctx context.Context, conn net.Conn, r *assocReader, accepted map[byte]acceptedContext, peerMax uint32, req *EventReportRequest) error {
	pcid, ts, ok := contextByAbstractAccepted(accepted, req.AffectedSOPClassUID)
	if !ok {
		return fmt.Errorf("ae: no context for N-EVENT-REPORT %s", req.AffectedSOPClassUID)
	}
	payload := req.EventInformation
	if req.EventInformationData != nil {
		encoded, err := req.EventInformationData.Encode(godicom.UID(ts))
		if err != nil {
			return fmt.Errorf("ae: encode event information: %w", err)
		}
		payload = encoded
	}
	cmd, err := (&dimse.NEventReportRQ{
		MessageID:              1,
		EventTypeID:            req.EventTypeID,
		AffectedSOPClassUID:    req.AffectedSOPClassUID,
		AffectedSOPInstanceUID: req.AffectedSOPInstanceUID,
		HasDataset:             len(payload) > 0,
	}).Encode()
	if err != nil {
		return err
	}
	if err := writeMessage(ctx, conn, pcid, cmd, payload, peerMax); err != nil {
		return err
	}
	_, rspCmd, _, err := r.message(ctx)
	if err != nil {
		return err
	}
	if _, err := dimse.DecodeNEventReportRSP(rspCmd); err != nil {
		return fmt.Errorf("ae: decode N-EVENT-REPORT-RSP: %w", err)
	}
	return nil
}

func scpSendEventReportNewAssoc(cfg ServerConfig, dest EventReportDestination, req EventReportRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentSCP)
	log := cfg.logger(ctx)

	calling := dest.CallingAE
	if calling == "" {
		calling = cfg.AETitle
	}
	called := dest.CalledAE
	if called == "" {
		log.Error("async N-EVENT-REPORT missing CalledAE")
		return
	}
	if req.AffectedSOPClassUID == "" {
		log.Error("async N-EVENT-REPORT missing Affected SOP Class UID")
		return
	}

	assoc, err := Dial(ctx, Config{
		AETitle: calling,
		PresentationContexts: []PresentationContext{{
			ID:               1,
			AbstractSyntax:   req.AffectedSOPClassUID,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
		TLS:    cfg.TLS,
		Logger: cfg.Logger,
	}, dest.Addr, called)
	if err != nil {
		log.Error("async N-EVENT-REPORT dial failed", gonetdicom.AttrAddr, dest.Addr, "err", err)
		return
	}
	defer func() { _ = assoc.Release(ctx) }()

	if _, err := assoc.NEventReport(ctx, req); err != nil {
		log.Error("async N-EVENT-REPORT failed", "err", err)
	}
}

func scpHandleNGet(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NGetRQ) error {
	ac, ok := accepted[pcid]
	result := NGetResult{
		Status:                 dcmstatus.SOPClassNotSupported,
		AffectedSOPClassUID:    rq.RequestedSOPClassUID,
		AffectedSOPInstanceUID: rq.RequestedSOPInstanceUID,
	}
	if ok && ac.AbstractSyntax == rq.RequestedSOPClassUID {
		if cfg.OnNGet != nil {
			result = cfg.OnNGet(ctx, NGetRequest{
				RequestedSOPClassUID:    rq.RequestedSOPClassUID,
				RequestedSOPInstanceUID: rq.RequestedSOPInstanceUID,
				AttributeIdentifierList: rq.AttributeIdentifierList,
			})
		} else {
			result.Status = StatusProcessingFailure
		}
		if result.AffectedSOPClassUID == "" {
			result.AffectedSOPClassUID = rq.RequestedSOPClassUID
		}
		if result.AffectedSOPInstanceUID == "" {
			result.AffectedSOPInstanceUID = rq.RequestedSOPInstanceUID
		}
	}
	return writeNAttributeRSP(ctx, conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
		result.AffectedSOPClassUID, result.AffectedSOPInstanceUID,
		result.AttributeList, result.AttributeListData, encodeNGetRSP)
}

func scpHandleNSet(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NSetRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	result := SetResult{
		Status:                 dcmstatus.SOPClassNotSupported,
		AffectedSOPClassUID:    rq.RequestedSOPClassUID,
		AffectedSOPInstanceUID: rq.RequestedSOPInstanceUID,
	}
	if ok && ac.AbstractSyntax == rq.RequestedSOPClassUID {
		var mod *godicom.Dataset
		if len(ds) > 0 {
			decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
			if err != nil {
				return fmt.Errorf("ae: decode modification list: %w", err)
			}
			mod = decoded
		}
		if cfg.OnNSet != nil {
			result = cfg.OnNSet(ctx, SetRequest{
				RequestedSOPClassUID:    rq.RequestedSOPClassUID,
				RequestedSOPInstanceUID: rq.RequestedSOPInstanceUID,
				ModificationList:        ds,
				ModificationListData:    mod,
			})
		} else {
			result.Status = StatusProcessingFailure
		}
		if result.AffectedSOPClassUID == "" {
			result.AffectedSOPClassUID = rq.RequestedSOPClassUID
		}
		if result.AffectedSOPInstanceUID == "" {
			result.AffectedSOPInstanceUID = rq.RequestedSOPInstanceUID
		}
	}
	return writeNAttributeRSP(ctx, conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
		result.AffectedSOPClassUID, result.AffectedSOPInstanceUID,
		result.AttributeList, result.AttributeListData, encodeNSetRSP)
}

func scpHandleNCreate(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NCreateRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	result := CreateResult{
		Status:                 dcmstatus.SOPClassNotSupported,
		AffectedSOPClassUID:    rq.AffectedSOPClassUID,
		AffectedSOPInstanceUID: rq.AffectedSOPInstanceUID,
	}
	if ok && ac.AbstractSyntax == rq.AffectedSOPClassUID {
		var attrs *godicom.Dataset
		if len(ds) > 0 {
			decoded, err := godicom.DecodeDataset(ds, godicom.UID(ac.TransferSyntax))
			if err != nil {
				return fmt.Errorf("ae: decode N-CREATE attribute list: %w", err)
			}
			attrs = decoded
		}
		if cfg.OnNCreate != nil {
			result = cfg.OnNCreate(ctx, CreateRequest{
				AffectedSOPClassUID:    rq.AffectedSOPClassUID,
				AffectedSOPInstanceUID: rq.AffectedSOPInstanceUID,
				AttributeList:          ds,
				AttributeListData:      attrs,
			})
		} else {
			result.Status = StatusProcessingFailure
		}
		if result.AffectedSOPClassUID == "" {
			result.AffectedSOPClassUID = rq.AffectedSOPClassUID
		}
		if result.AffectedSOPInstanceUID == "" {
			result.AffectedSOPInstanceUID = rq.AffectedSOPInstanceUID
		}
		if result.AffectedSOPInstanceUID == "" && result.AttributeListData != nil {
			if v, ok := result.AttributeListData.GetString(godicom.MustTag("SOPInstanceUID")); ok && v != "" {
				result.AffectedSOPInstanceUID = v
			}
		}
		// Success/Warning responses require an Affected SOP Instance UID (Part 7).
		// If the handler left it empty, mint one (Go convenience vs failing the association).
		if result.AffectedSOPInstanceUID == "" && (result.Status == dcmstatus.Success || result.Status == dimse.StatusSuccess) {
			result.AffectedSOPInstanceUID = NewInstanceUID()
		}
	}
	// Success and warning responses must name the created instance (Part 7);
	// the branch above mints one when the handler left it empty. A failure
	// names none: the request was refused, so there is no object to point at.
	return writeNAttributeRSP(ctx, conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
		result.AffectedSOPClassUID, result.AffectedSOPInstanceUID,
		result.AttributeList, result.AttributeListData, encodeNCreateRSP)
}

func scpHandleNDelete(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NDeleteRQ) error {
	ac, ok := accepted[pcid]
	result := DeleteResult{
		Status:                 dcmstatus.SOPClassNotSupported,
		AffectedSOPClassUID:    rq.RequestedSOPClassUID,
		AffectedSOPInstanceUID: rq.RequestedSOPInstanceUID,
	}
	if ok && ac.AbstractSyntax == rq.RequestedSOPClassUID {
		if cfg.OnNDelete != nil {
			result = cfg.OnNDelete(ctx, DeleteRequest{
				RequestedSOPClassUID:    rq.RequestedSOPClassUID,
				RequestedSOPInstanceUID: rq.RequestedSOPInstanceUID,
			})
		} else {
			result.Status = StatusProcessingFailure
		}
		if result.AffectedSOPClassUID == "" {
			result.AffectedSOPClassUID = rq.RequestedSOPClassUID
		}
		if result.AffectedSOPInstanceUID == "" {
			result.AffectedSOPInstanceUID = rq.RequestedSOPInstanceUID
		}
	}
	rsp, err := (&dimse.NDeleteRSP{
		MessageIDBeingRespondedTo: rq.MessageID,
		AffectedSOPClassUID:       result.AffectedSOPClassUID,
		AffectedSOPInstanceUID:    result.AffectedSOPInstanceUID,
		Status:                    result.Status,
	}).Encode()
	if err != nil {
		return err
	}
	return writeMessage(ctx, conn, pcid, rsp, nil, peerMax)
}

type nAttrEncoder func(msgID uint16, status uint16, classUID, instanceUID string, hasDS bool) ([]byte, error)

func encodeNGetRSP(msgID uint16, status uint16, classUID, instanceUID string, hasDS bool) ([]byte, error) {
	return (&dimse.NGetRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       classUID,
		AffectedSOPInstanceUID:    instanceUID,
		Status:                    status,
		HasDataset:                hasDS,
	}).Encode()
}

func encodeNSetRSP(msgID uint16, status uint16, classUID, instanceUID string, hasDS bool) ([]byte, error) {
	return (&dimse.NSetRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       classUID,
		AffectedSOPInstanceUID:    instanceUID,
		Status:                    status,
		HasDataset:                hasDS,
	}).Encode()
}

func encodeNCreateRSP(msgID uint16, status uint16, classUID, instanceUID string, hasDS bool) ([]byte, error) {
	return (&dimse.NCreateRSP{
		MessageIDBeingRespondedTo: msgID,
		AffectedSOPClassUID:       classUID,
		AffectedSOPInstanceUID:    instanceUID,
		Status:                    status,
		HasDataset:                hasDS,
	}).Encode()
}

func writeNAttributeRSP(ctx context.Context, conn net.Conn, peerMax uint32, pcid byte, ok bool, ac acceptedContext, msgID uint16, status uint16, classUID, instanceUID string, list []byte, listData *godicom.Dataset, enc nAttrEncoder) error {
	payload := list
	if listData != nil {
		if !ok {
			return fmt.Errorf("ae: cannot encode attribute list without accepted context")
		}
		encoded, err := listData.Encode(godicom.UID(ac.TransferSyntax))
		if err != nil {
			return fmt.Errorf("ae: encode attribute list: %w", err)
		}
		payload = encoded
	}
	rsp, err := enc(msgID, status, classUID, instanceUID, len(payload) > 0)
	if err != nil {
		return err
	}
	return writeMessage(ctx, conn, pcid, rsp, payload, peerMax)
}
