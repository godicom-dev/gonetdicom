// Package ae provides DICOM Application Entity association (SCU) helpers.
package ae

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// Implementation identification for gonetdicom.
const (
	ImplementationClassUID    = "1.2.826.0.1.3680043.10.541.1"
	ImplementationVersionName = "GONETDICOM_001"
)

// ErrRejected is returned when the peer rejects the association.
var ErrRejected = errors.New("ae: association rejected")

// ErrAborted is returned when the peer aborts the association.
var ErrAborted = errors.New("ae: association aborted")

// ErrReleased is returned when the peer releases the association while a
// request is outstanding. The release is honoured — an A-RELEASE-RP goes back
// to the peer — and the operation that was waiting reports this instead of an
// answer that will never arrive.
var ErrReleased = errors.New("ae: association released by peer")

// ErrNoContext is returned when no accepted presentation context is available.
var ErrNoContext = errors.New("ae: no accepted presentation context")

// ErrIdleTimeout is returned when no PDU arrived within Config.IdleTimeout.
//
// It is a bound on the wait, not a verdict on the association: the socket is
// untouched, so whatever the peer eventually sends is still framed and can be
// read by the next call — or skipped by Release.
var ErrIdleTimeout = errors.New("ae: no PDU within IdleTimeout")

// ErrOperationInFlight is returned when an association is asked to start a
// second operation, or to release, while another one is still waiting for its
// final response.
//
// An *Association has one read side and no message demultiplexer: every
// operation reads the responses it asked for, in the caller's goroutine. A
// second reader on the same connection does not conflict politely — it takes
// whichever PDUs arrive first, so one operation gets the other's answer and the
// message ids no longer line up. Failing the second caller is the only outcome
// that leaves the first one correct.
//
// Release is included because PS3.8 7.2.2 says a requestor "shall not issue any
// further primitives other than an A-ABORT request primitive" once it has asked
// to release: a release is a statement about the association, and an operation
// that is still running has to be stopped on its own terms first (cancel its
// context, or C-CANCEL, or Abort).
var ErrOperationInFlight = errors.New("ae: operation already in flight")

// PresentationContext proposes an abstract syntax and transfer syntaxes.
type PresentationContext struct {
	ID               byte
	AbstractSyntax   string
	TransferSyntaxes []string
}

// Config configures an Application Entity for outbound associations.
type Config struct {
	AETitle                   string
	MaxPDULength              uint32
	ImplementationClassUID    string
	ImplementationVersionName string
	DialTimeout               time.Duration
	// IdleTimeout bounds each PDU read and write when the caller's context has
	// no deadline; ErrIdleTimeout reports the read side. Zero means no extra
	// idle deadline (context-only). A read that times out does not damage the
	// association: the read side owns the PDU it was in the middle of, so the
	// stream stays framed for the next call.
	IdleTimeout time.Duration
	// TLS, when non-nil, dials with TLS (DICOM over TLS). ServerName should be set
	// when verifying certificates.
	TLS *tls.Config
	// Logger receives association lifecycle and debug events.
	// Nil falls back to context (WithLogger) then DiscardHandler (quiet).
	Logger *slog.Logger
	// PresentationContexts to propose. If empty, Verification only is proposed.
	PresentationContexts []PresentationContext
	// RoleSelections are SCP/SCU Role Selection items proposed in A-ASSOCIATE-RQ
	// (pynetdicom build_role). Empty means default roles (requestor=SCU, acceptor=SCP).
	RoleSelections []pdu.RoleSelection
	// UserIdentity, when non-nil, is proposed as a User Identity Negotiation RQ item.
	UserIdentity *pdu.UserIdentityRQ
	// AsyncOperationsWindow, when non-nil, is proposed as an Asynchronous
	// Operations Window Negotiation item. The acceptor answers with the window
	// it will honour; read the result with Association.AsyncOperationsWindow.
	AsyncOperationsWindow *pdu.AsyncOperationsWindow
	// ExtendedNegotiations are proposed as SOP Class Extended Negotiation
	// items, one per SOP Class, carrying that class's application information
	// (pynetdicom add_requested_extended_negotiation). The acceptor's answers
	// are read with Association.ExtendedNegotiation.
	ExtendedNegotiations []pdu.ExtendedNegotiation
	// CommonExtendedNegotiations are proposed as SOP Class Common Extended
	// Negotiation items (PS3.8 D.3.3.6), one per SOP Class, naming the Service
	// Class it is used under and the general classes it may be matched to.
	// The acceptor's answers are read with
	// Association.CommonExtendedNegotiation.
	CommonExtendedNegotiations []pdu.CommonExtendedNegotiation
}

func (c Config) withDefaults() Config {
	if c.AETitle == "" {
		c.AETitle = "GONETSCU"
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
	if c.DialTimeout == 0 {
		c.DialTimeout = 30 * time.Second
	}
	if len(c.PresentationContexts) == 0 {
		c.PresentationContexts = []PresentationContext{{
			ID:               1,
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}}
	}
	return c
}

func (c Config) logger(ctx context.Context) *slog.Logger {
	return resolveAELogger(ctx, c.Logger)
}

// AcceptedContext is a presentation context accepted by the peer.
type AcceptedContext struct {
	ID             byte
	AbstractSyntax string
	TransferSyntax string
	// AsSCU / AsSCP are the negotiated roles for the local AE (requestor) on this context.
	// Defaults: AsSCU=true, AsSCP=false when no role selection was negotiated.
	AsSCU bool
	AsSCP bool
}

// Association is an established DIMSE association (SCU role).
//
// It carries one DIMSE operation at a time: each method sends its request and
// then reads responses until the final one, so two of them running at once
// interleave PDUs on the same connection and neither peer can make sense of the
// result. Parallel work needs parallel associations.
//
// CCancel, Abort and Close are the deliberate exceptions. All three exist to
// reach an operation that is already blocked, so all three are safe to call from
// another goroutine.
type Association struct {
	conn net.Conn
	cfg  Config
	// called, peerMax, contexts and userIdentityResponse are written by negotiate
	// before the Association is handed to the caller, and only read afterwards.
	called               string
	peerMax              uint32
	contexts             []AcceptedContext
	userIdentityResponse []byte
	// peerImplClassUID / peerImplVersionName are the acceptor's implementation
	// identification from the A-ASSOCIATE-AC, the SCU-side counterpart of what
	// AssocInfo carries for an SCP handler (dcm4che's getRemoteImplClassUID).
	peerImplClassUID    string
	peerImplVersionName string
	// peerAsyncOps / peerExtNeg / peerCommonExt are the acceptor's answers to
	// the negotiation items this SCU proposed. Nil and empty mean "not
	// negotiated": the peer either did not answer or has nothing for that SOP
	// Class.
	peerAsyncOps  *pdu.AsyncOperationsWindow
	peerExtNeg    map[string][]byte
	peerCommonExt map[string]pdu.CommonExtendedNegotiation
	// nextID is atomic so Message IDs stay unique even when a caller ignores the
	// contract above: a lost increment puts two outstanding requests under one ID,
	// and the responses to them can no longer be told apart.
	nextID atomic.Uint32
	// closed makes the teardown methods idempotent. conn itself is never cleared —
	// a goroutine unwinding out of readPDU still clears its deadline through it,
	// and clearing the field turned a concurrent Abort into a nil-pointer panic
	// instead of the interruption it was asked for.
	closed atomic.Bool
	// opMu guards opInFlight, the name of the operation that owns the read
	// side. Empty means idle. See beginOp.
	opMu       sync.Mutex
	opInFlight string
	// reader owns the socket's read side. It is set before the Association is
	// handed to the caller and never replaced, so the pointer itself is safe to
	// read from any goroutine.
	reader *pduReader
}

// beginOp claims the association's read side for one operation and returns the
// func that gives it back. A caller that gets an error must not touch the
// connection.
//
// The claim is a string rather than a counter because the useful error is "C-GET
// is already waiting for its response", and because there is nothing to count:
// nesting never happens, since no operation calls another.
func (a *Association) beginOp(op string) (func(), error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.opInFlight != "" {
		return nil, fmt.Errorf("%w (%s is in progress)", ErrOperationInFlight, a.opInFlight)
	}
	a.opInFlight = op
	return func() {
		a.opMu.Lock()
		a.opInFlight = ""
		a.opMu.Unlock()
	}, nil
}

// Dial associates with addr (host:port) as an SCU.
// When cfg.TLS is set, the connection is established with TLS.
func Dial(ctx context.Context, cfg Config, addr, calledAE string) (*Association, error) {
	cfg = cfg.withDefaults()
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentAE)
	if calledAE == "" {
		return nil, fmt.Errorf("ae: empty called AE title")
	}

	var (
		conn net.Conn
		err  error
	)
	if cfg.TLS != nil {
		d := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: cfg.DialTimeout},
			Config:    cfg.TLS,
		}
		conn, err = d.DialContext(ctx, "tcp", addr)
	} else {
		d := net.Dialer{Timeout: cfg.DialTimeout}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("ae: dial %s: %w", addr, err)
	}

	assoc := &Association{conn: conn, cfg: cfg, called: calledAE}
	assoc.reader = newPDUReader(ctx, conn)
	if err := assoc.negotiate(ctx); err != nil {
		_ = assoc.Close()
		return nil, err
	}
	cfg.logger(ctx).Info("association established",
		gonetdicom.AttrCallingAE, cfg.AETitle,
		gonetdicom.AttrCalledAE, calledAE,
		gonetdicom.AttrAddr, addr,
		gonetdicom.AttrTLS, cfg.TLS != nil,
		gonetdicom.AttrContexts, len(assoc.contexts),
	)
	return assoc, nil
}

// AcceptFromConn completes association as SCU using an already-connected conn.
func AcceptFromConn(ctx context.Context, cfg Config, conn net.Conn, calledAE string) (*Association, error) {
	cfg = cfg.withDefaults()
	ctx = gonetdicom.LoggerContext(ctx, cfg.Logger, gonetdicom.ComponentAE)
	if calledAE == "" {
		return nil, fmt.Errorf("ae: empty called AE title")
	}
	assoc := &Association{conn: conn, cfg: cfg, called: calledAE}
	assoc.reader = newPDUReader(ctx, conn)
	if err := assoc.negotiate(ctx); err != nil {
		// The connection belongs to the caller, but the reader is ours: stop it
		// so a goroutine parked on a hand-off does not outlive the failure.
		assoc.reader.Close()
		return nil, err
	}
	return assoc, nil
}

func (a *Association) negotiate(ctx context.Context) error {
	proposed, err := buildPresentationContexts(a.cfg.PresentationContexts)
	if err != nil {
		return err
	}
	pcs := make([]pdu.PresentationContextRQ, 0, len(proposed))
	byID := make(map[byte]PresentationContext, len(proposed))
	for _, pc := range proposed {
		byID[pc.ID] = pc
		pcs = append(pcs, pdu.PresentationContextRQ{
			ID:               pc.ID,
			AbstractSyntax:   pc.AbstractSyntax,
			TransferSyntaxes: pc.TransferSyntaxes,
		})
	}

	rq := &pdu.AAssociateRQ{
		CalledAETitle:          a.called,
		CallingAETitle:         a.cfg.AETitle,
		ApplicationContextName: pdu.ApplicationContextName,
		PresentationContexts:   pcs,
		UserInformation: pdu.UserInformation{
			MaxLength:                  a.cfg.MaxPDULength,
			ImplementationClassUID:     a.cfg.ImplementationClassUID,
			ImplementationVersionName:  a.cfg.ImplementationVersionName,
			AsyncOperationsWindow:      a.cfg.AsyncOperationsWindow,
			ExtendedNegotiations:       a.cfg.ExtendedNegotiations,
			CommonExtendedNegotiations: a.cfg.CommonExtendedNegotiations,
			RoleSelections:             a.cfg.RoleSelections,
			UserIdentityRQ:             a.cfg.UserIdentity,
		},
	}
	if err := a.writePDU(ctx, rq); err != nil {
		return err
	}
	raw, err := a.readPDU(ctx)
	if err != nil {
		return err
	}
	switch p := raw.(type) {
	case *pdu.AAssociateAC:
		a.peerMax = p.UserInformation.MaxLength
		a.peerImplClassUID = p.UserInformation.ImplementationClassUID
		a.peerImplVersionName = p.UserInformation.ImplementationVersionName
		a.peerAsyncOps = p.UserInformation.AsyncOperationsWindow
		for _, en := range p.UserInformation.ExtendedNegotiations {
			if a.peerExtNeg == nil {
				a.peerExtNeg = make(map[string][]byte, len(p.UserInformation.ExtendedNegotiations))
			}
			a.peerExtNeg[en.SOPClassUID] = en.Info
		}
		for _, ce := range p.UserInformation.CommonExtendedNegotiations {
			if a.peerCommonExt == nil {
				a.peerCommonExt = make(map[string]pdu.CommonExtendedNegotiation, len(p.UserInformation.CommonExtendedNegotiations))
			}
			a.peerCommonExt[ce.SOPClassUID] = ce
		}
		if p.UserInformation.UserIdentityAC != nil {
			a.userIdentityResponse = append([]byte(nil), p.UserInformation.UserIdentityAC.ServerResponse...)
		}
		acRoles := roleMap(p.UserInformation.RoleSelections)
		rqRoles := roleMap(a.cfg.RoleSelections)
		for _, ac := range p.PresentationContexts {
			if ac.Result != 0 {
				continue
			}
			prop, ok := byID[ac.ID]
			if !ok {
				continue
			}
			ctxAcc := AcceptedContext{
				ID:             ac.ID,
				AbstractSyntax: prop.AbstractSyntax,
				TransferSyntax: ac.TransferSyntax,
				AsSCU:          true,
				AsSCP:          false,
			}
			rqRole, rqOK := rqRoles[prop.AbstractSyntax]
			acRole, acOK := acRoles[prop.AbstractSyntax]
			if rqOK && acOK {
				out := negotiateRoles(rqRole.SCURole, rqRole.SCPRole, true, acRole.SCURole, acRole.SCPRole, true)
				ctxAcc.AsSCU = out.ReqSCU
				ctxAcc.AsSCP = out.ReqSCP
			} else if rqOK {
				// Proposed but acceptor omitted reply → default requestor roles.
				ctxAcc.AsSCU = true
				ctxAcc.AsSCP = false
			}
			a.contexts = append(a.contexts, ctxAcc)
		}
		if len(a.contexts) == 0 {
			return fmt.Errorf("%w: no presentation context accepted", ErrNoContext)
		}
		return nil
	case *pdu.AAssociateRJ:
		return fmt.Errorf("%w: result=%d source=%d reason=%d", ErrRejected, p.Result, p.Source, p.ReasonDiagnostic)
	case *pdu.AAbort:
		return fmt.Errorf("%w: source=%d reason=%d", ErrAborted, p.Source, p.ReasonDiagnostic)
	default:
		return fmt.Errorf("ae: unexpected PDU %T during associate", p)
	}
}

// Contexts returns accepted presentation contexts.
func (a *Association) Contexts() []AcceptedContext {
	return append([]AcceptedContext(nil), a.contexts...)
}

// AsyncOperationsWindow returns the window the acceptor granted, and false when
// it answered with no window at all. A window whose both values are 0 is the
// protocol's "unlimited" (pdu.AsyncOperationsWindow.Unlimited); 1/1 is the
// synchronous default.
func (a *Association) AsyncOperationsWindow() (pdu.AsyncOperationsWindow, bool) {
	if a.peerAsyncOps == nil {
		return pdu.AsyncOperationsWindow{}, false
	}
	return *a.peerAsyncOps, true
}

// ExtendedNegotiation returns the acceptor's Service Class Application
// Information for sopClassUID, and false when it answered nothing for that
// class. The slice is a copy: the association keeps its own.
func (a *Association) ExtendedNegotiation(sopClassUID string) ([]byte, bool) {
	info, ok := a.peerExtNeg[sopClassUID]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), info...), true
}

// CommonExtendedNegotiation returns the acceptor's SOP Class Common Extended
// Negotiation for sopClassUID, and false when it answered nothing for that
// class.
//
// An answer here means the acceptor confirmed the class is used under that
// Service Class; silence means it does not negotiate common extensions for the
// class at all, which is not a refusal — the class is still usable the plain
// DIMSE way.
func (a *Association) CommonExtendedNegotiation(sopClassUID string) (pdu.CommonExtendedNegotiation, bool) {
	ce, ok := a.peerCommonExt[sopClassUID]
	if !ok {
		return pdu.CommonExtendedNegotiation{}, false
	}
	// The slice is a copy: the association keeps its own.
	ce.RelatedGeneralSOPClasses = append([]string(nil), ce.RelatedGeneralSOPClasses...)
	return ce, true
}

// UserIdentityResponse returns the Server Response from a User Identity AC item,
// if the peer included one. Nil means no response item.
func (a *Association) UserIdentityResponse() []byte {
	if a.userIdentityResponse == nil {
		return nil
	}
	return append([]byte(nil), a.userIdentityResponse...)
}

func (a *Association) contextByAbstract(uid string) (AcceptedContext, bool) {
	for _, c := range a.contexts {
		if c.AbstractSyntax == uid {
			return c, true
		}
	}
	return AcceptedContext{}, false
}

// contextForStore matches pynetdicom Association._get_valid_context for a
// C-STORE. An empty transfer syntax keeps the first context for the SOP
// Class. Otherwise an exact transfer syntax wins. Uncompressed little-endian
// syntaxes may substitute for each other (implicit VR, explicit VR, deflated);
// a compressed syntax requires an exact match.
func (a *Association) contextForStore(sop, ts string) (AcceptedContext, bool) {
	if ts == "" {
		return a.contextByAbstract(sop)
	}
	var convertible AcceptedContext
	var haveConvertible bool
	for _, c := range a.contexts {
		if c.AbstractSyntax != sop {
			continue
		}
		if c.TransferSyntax == ts {
			return c, true
		}
		if uncompressedLittleEndian(ts) && uncompressedLittleEndian(c.TransferSyntax) && !haveConvertible {
			convertible = c
			haveConvertible = true
		}
	}
	if haveConvertible {
		return convertible, true
	}
	return AcceptedContext{}, false
}

func uncompressedLittleEndian(ts string) bool {
	switch ts {
	case pdu.ImplicitVRLittleEndian, explicitVRLittleEndian, deflatedExplicitVRLittleEndian:
		return true
	default:
		return false
	}
}

const (
	explicitVRLittleEndian         = "1.2.840.10008.1.2.1"
	deflatedExplicitVRLittleEndian = "1.2.840.10008.1.2.1.99"
)

// nextMessageID hands out the Message ID for one request. IDs start at 1 and
// skip 0 on wrap, as pynetdicom does.
func (a *Association) nextMessageID() uint16 {
	for {
		if id := uint16(a.nextID.Add(1)); id != 0 {
			return id
		}
	}
}

// CEcho sends a C-ECHO-RQ and waits for a successful C-ECHO-RSP.
func (a *Association) CEcho(ctx context.Context) error {
	end, err := a.beginOp("C-ECHO")
	if err != nil {
		return err
	}
	defer end()
	pc, ok := a.contextByAbstract(pdu.VerificationSOPClass)
	if !ok {
		return fmt.Errorf("%w: verification", ErrNoContext)
	}
	msgID := a.nextMessageID()
	cmd, err := (&dimse.CEchoRQ{MessageID: msgID}).Encode()
	if err != nil {
		return err
	}
	if err := a.sendMessage(ctx, pc.ID, cmd, nil); err != nil {
		return err
	}
	rspCmd, _, err := a.recvMessage(ctx)
	if err != nil {
		return err
	}
	rsp, err := dimse.DecodeCEchoRSP(rspCmd)
	if err != nil {
		return fmt.Errorf("ae: decode C-ECHO-RSP: %w", err)
	}
	if rsp.MessageIDBeingRespondedTo != msgID {
		return fmt.Errorf("ae: C-ECHO message id mismatch: got %d want %d", rsp.MessageIDBeingRespondedTo, msgID)
	}
	if rsp.Status != dimse.StatusSuccess {
		return fmt.Errorf("ae: C-ECHO status 0x%04x", rsp.Status)
	}
	return nil
}

func (a *Association) logCtx(ctx context.Context) context.Context {
	return gonetdicom.LoggerContext(ctx, a.cfg.Logger, gonetdicom.ComponentAE)
}

func (a *Association) sendMessage(ctx context.Context, pcid byte, command, dataset []byte) error {
	ctx = a.logCtx(ctx)
	logDIMSE(ctx, "send", pcid, command)
	pdus, err := pdu.FragmentMessage(pcid, command, dataset, a.peerMax)
	if err != nil {
		return err
	}
	for _, p := range pdus {
		if err := a.writePDU(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// recvMessage is recvMessagePC with the dataset read whole. Responses carry
// identifiers, which are small; the streaming path exists for C-STORE-RQ.
func (a *Association) recvMessage(ctx context.Context) ([]byte, []byte, error) {
	_, command, body, err := a.recvMessagePC(ctx)
	if err != nil {
		return nil, nil, err
	}
	if body == nil {
		return command, nil, nil
	}
	defer func() { _ = body.Close() }()
	ds, err := body.Bytes()
	if err != nil {
		return nil, nil, err
	}
	return command, ds, nil
}

// recvMessagePC reads one DIMSE message. The command set is returned whole —
// dispatch needs it — and the dataset comes back as a body that pulls from the
// socket as the handler reads, so a C-GET that stores a 500 MB series moves it
// through one fragment of memory instead of assembling it first.
func (a *Association) recvMessagePC(ctx context.Context) (pcid byte, command []byte, body *msgBody, err error) {
	ctx = a.logCtx(ctx)
	var (
		cmdBuf  []byte
		pending []pdu.PDV
	)

	pull := func() ([]byte, bool, error) {
		for len(pending) == 0 {
			raw, err := a.readPDU(ctx)
			if err != nil {
				return nil, false, err
			}
			switch p := raw.(type) {
			case *pdu.PDataTF:
				pending = p.PDVs
			case *pdu.AAbort:
				return nil, false, fmt.Errorf("%w: source=%d reason=%d", ErrAborted, p.Source, p.ReasonDiagnostic)
			default:
				return nil, false, fmt.Errorf("ae: unexpected PDU %T while receiving dataset", raw)
			}
		}
		pdv := pending[0]
		pending = pending[1:]
		return pdv.Fragment(), pdv.IsLast(), nil
	}

	for {
		raw, err := a.readPDU(ctx)
		if err != nil {
			return 0, nil, nil, err
		}
		switch p := raw.(type) {
		case *pdu.PDataTF:
			for i, pdv := range p.PDVs {
				if pcid == 0 {
					pcid = pdv.ContextID
				}
				cmdBuf = append(cmdBuf, pdv.Fragment()...)
				if !pdv.IsLast() {
					continue
				}
				hasDS, err := dimse.CommandHasDataset(cmdBuf)
				if err != nil {
					return 0, nil, nil, fmt.Errorf("ae: command set: %w", err)
				}
				logDIMSE(ctx, "recv", pcid, cmdBuf)
				if !hasDS {
					return pcid, cmdBuf, nil, nil
				}
				// Fragments of the dataset that shared this P-DATA with the
				// command set are already in hand; queue them so the pull
				// reads them before touching the socket again.
				pending = append(pending, p.PDVs[i+1:]...)
				return pcid, cmdBuf, newPullBody(pull), nil
			}
		case *pdu.AAbort:
			return 0, nil, nil, fmt.Errorf("%w: source=%d reason=%d", ErrAborted, p.Source, p.ReasonDiagnostic)
		case *pdu.AReleaseRQ:
			// PS3.8 Table 9-10: A-RELEASE-RQ is valid in the P-DATA state, and
			// the receiver answers A-RELEASE-RP. Dropping it as an unexpected
			// PDU left the peer waiting for an RP that never came and the
			// association dangling until the OS closed the socket.
			if err := a.writePDU(ctx, &pdu.AReleaseRP{}); err != nil {
				return 0, nil, nil, fmt.Errorf("%w (replying A-RELEASE-RP: %w)", ErrReleased, err)
			}
			_ = a.closeConn()
			a.cfg.logger(ctx).Info("peer released the association",
				gonetdicom.AttrCallingAE, a.cfg.AETitle,
				gonetdicom.AttrCalledAE, a.called,
			)
			return 0, nil, nil, ErrReleased
		default:
			return 0, nil, nil, fmt.Errorf("ae: unexpected PDU %T while receiving message", p)
		}
	}
}

// Release performs association release and closes the connection.
//
// It fails with ErrOperationInFlight while an operation is still waiting for
// its answer: PS3.8 7.2.2 allows a requestor nothing but A-ABORT after it asks
// to release, so a release cannot be used to stop a running C-FIND/C-GET.
// Cancel the operation first (its context, or CCancel), then release.
func (a *Association) Release(ctx context.Context) error {
	if a.closed.Load() {
		return nil
	}
	end, err := a.beginOp("A-RELEASE")
	if err != nil {
		return err
	}
	defer end()
	ctx = a.logCtx(ctx)
	defer func() { _ = a.closeConn() }()
	if err := a.writePDU(ctx, &pdu.AReleaseRQ{}); err != nil {
		return err
	}
	// Release collision (PS3.8 9.3.3): both sides sent an A-RELEASE-RQ, so both
	// answer the other's with an A-RELEASE-RP and both still wait for one. The
	// peer's RQ turning up here was reported as an unexpected PDU, which failed
	// the release and left the association to be torn down by the socket.
	//
	// P-DATA is skipped rather than fatal. 7.2.2 is explicit that the requestor
	// "may receive P-DATA Indication primitives" while it waits, and that it
	// must not answer them — the only primitive it may still issue is A-ABORT.
	// So a peer that was mid-retrieve when we asked to release keeps sending
	// for a while, and treating the first fragment as an error turned an
	// ordinary race into a failed release. Each pass consumes one PDU, and a
	// peer that goes quiet is bounded by ctx and IdleTimeout, the same bound
	// every other read in this package has.
	for {
		raw, err := a.readPDU(ctx)
		if err != nil {
			return err
		}
		switch p := raw.(type) {
		case *pdu.AReleaseRP:
			a.cfg.logger(ctx).Info("association released",
				gonetdicom.AttrCallingAE, a.cfg.AETitle,
				gonetdicom.AttrCalledAE, a.called,
			)
			return nil
		case *pdu.AReleaseRQ:
			if err := a.writePDU(ctx, &pdu.AReleaseRP{}); err != nil {
				return err
			}
		case *pdu.AAbort:
			return ErrAborted
		case *pdu.PDataTF:
			continue
		default:
			return fmt.Errorf("ae: unexpected PDU %T for release", p)
		}
	}
}

// Abort sends A-ABORT and closes the connection.
func (a *Association) Abort() error {
	if a.closed.Load() {
		return nil
	}
	defer func() { _ = a.closeConn() }()
	_ = pdu.Write(a.conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00})
	a.cfg.logger(context.Background()).Warn("association aborted",
		gonetdicom.AttrCallingAE, a.cfg.AETitle,
		gonetdicom.AttrCalledAE, a.called,
	)
	return nil
}

// Close closes the underlying connection without release negotiation.
func (a *Association) Close() error {
	return a.closeConn()
}

func (a *Association) closeConn() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Stop the reader before the connection: it may be parked handing a PDU
	// over, and closing the channel under it would leave that goroutine there.
	if a.reader != nil {
		a.reader.Close()
	}
	return a.conn.Close()
}

func (a *Association) writePDU(ctx context.Context, p pdu.PDU) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = a.logCtx(ctx)
	deadline, ok := a.pduDeadline(ctx)
	if ok {
		_ = a.conn.SetWriteDeadline(deadline)
		defer func() { _ = a.conn.SetWriteDeadline(time.Time{}) }()
	}
	logPDU(ctx, "send", p)
	if err := pdu.Write(a.conn, p); err != nil {
		return fmt.Errorf("ae: write PDU: %w", err)
	}
	return nil
}

// readPDU returns the next PDU, or an error if ctx or IdleTimeout runs out
// first.
//
// Expiry does not touch the connection. The PDU the reader is part-way through
// still belongs to the reader, which finishes it and queues it for whoever
// reads next; that is what keeps the byte stream aligned across a cancelled
// operation, and why Release — which skips P-DATA — works afterwards.
func (a *Association) readPDU(ctx context.Context) (pdu.PDU, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A nil channel blocks forever, so the idle case costs nothing when it is
	// not configured.
	var expired <-chan time.Time
	if a.cfg.IdleTimeout > 0 {
		timer := time.NewTimer(a.cfg.IdleTimeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case p, ok := <-a.reader.ch:
		if !ok {
			return nil, a.reader.err
		}
		return p, nil
	case <-expired:
		return nil, ErrIdleTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *Association) pduDeadline(ctx context.Context) (time.Time, bool) {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline, true
	}
	if a.cfg.IdleTimeout > 0 {
		return time.Now().Add(a.cfg.IdleTimeout), true
	}
	return time.Time{}, false
}
