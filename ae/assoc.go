// Package ae provides DICOM Application Entity association (SCU) helpers.
package ae

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

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

// ErrNoContext is returned when no accepted presentation context is available.
var ErrNoContext = errors.New("ae: no accepted presentation context")

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
	// PresentationContexts to propose. If empty, Verification only is proposed.
	PresentationContexts []PresentationContext
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

// AcceptedContext is a presentation context accepted by the peer.
type AcceptedContext struct {
	ID             byte
	AbstractSyntax string
	TransferSyntax string
}

// Association is an established DIMSE association (SCU role).
type Association struct {
	conn     net.Conn
	cfg      Config
	called   string
	peerMax  uint32
	contexts []AcceptedContext
	nextID   uint16
}

// Dial associates with addr (host:port) as an SCU.
func Dial(ctx context.Context, cfg Config, addr, calledAE string) (*Association, error) {
	cfg = cfg.withDefaults()
	if calledAE == "" {
		return nil, fmt.Errorf("ae: empty called AE title")
	}

	d := net.Dialer{Timeout: cfg.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ae: dial %s: %w", addr, err)
	}

	assoc := &Association{
		conn:   conn,
		cfg:    cfg,
		called: calledAE,
		nextID: 1,
	}
	if err := assoc.negotiate(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return assoc, nil
}

// AcceptFromConn completes association as SCU using an already-connected conn.
func AcceptFromConn(ctx context.Context, cfg Config, conn net.Conn, calledAE string) (*Association, error) {
	cfg = cfg.withDefaults()
	if calledAE == "" {
		return nil, fmt.Errorf("ae: empty called AE title")
	}
	assoc := &Association{
		conn:   conn,
		cfg:    cfg,
		called: calledAE,
		nextID: 1,
	}
	if err := assoc.negotiate(ctx); err != nil {
		return nil, err
	}
	return assoc, nil
}

func (a *Association) negotiate(ctx context.Context) error {
	pcs := make([]pdu.PresentationContextRQ, 0, len(a.cfg.PresentationContexts))
	byID := make(map[byte]PresentationContext, len(a.cfg.PresentationContexts))
	for i, pc := range a.cfg.PresentationContexts {
		id := pc.ID
		if id == 0 {
			id = byte(2*i + 1) // odd IDs: 1,3,5,...
		}
		if len(pc.TransferSyntaxes) == 0 {
			pc.TransferSyntaxes = []string{pdu.ImplicitVRLittleEndian}
		}
		pc.ID = id
		byID[id] = pc
		pcs = append(pcs, pdu.PresentationContextRQ{
			ID:               id,
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
			MaxLength:                 a.cfg.MaxPDULength,
			ImplementationClassUID:    a.cfg.ImplementationClassUID,
			ImplementationVersionName: a.cfg.ImplementationVersionName,
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
		for _, ac := range p.PresentationContexts {
			if ac.Result != 0 {
				continue
			}
			prop, ok := byID[ac.ID]
			if !ok {
				continue
			}
			a.contexts = append(a.contexts, AcceptedContext{
				ID:             ac.ID,
				AbstractSyntax: prop.AbstractSyntax,
				TransferSyntax: ac.TransferSyntax,
			})
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

func (a *Association) contextByAbstract(uid string) (AcceptedContext, bool) {
	for _, c := range a.contexts {
		if c.AbstractSyntax == uid {
			return c, true
		}
	}
	return AcceptedContext{}, false
}

func (a *Association) nextMessageID() uint16 {
	id := a.nextID
	a.nextID++
	if a.nextID == 0 {
		a.nextID = 1
	}
	return id
}

// CEcho sends a C-ECHO-RQ and waits for a successful C-ECHO-RSP.
func (a *Association) CEcho(ctx context.Context) error {
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

func (a *Association) sendMessage(ctx context.Context, pcid byte, command, dataset []byte) error {
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

func (a *Association) recvMessage(ctx context.Context) (command, dataset []byte, err error) {
	_, command, dataset, err = a.recvMessagePC(ctx)
	return command, dataset, err
}

func (a *Association) recvMessagePC(ctx context.Context) (pcid byte, command, dataset []byte, err error) {
	var (
		cmdBuf   []byte
		dsBuf    []byte
		gotCmd   bool
		cmdDone  bool
		dsDone   bool
		expectDS bool
	)
	for {
		raw, err := a.readPDU(ctx)
		if err != nil {
			return 0, nil, nil, err
		}
		switch p := raw.(type) {
		case *pdu.PDataTF:
			for _, pdv := range p.PDVs {
				if pcid == 0 {
					pcid = pdv.ContextID
				}
				frag := pdv.Fragment()
				if pdv.IsCommand() {
					cmdBuf = append(cmdBuf, frag...)
					gotCmd = true
					if pdv.IsLast() {
						cmdDone = true
						hasDS, err := dimse.CommandHasDataset(cmdBuf)
						if err != nil {
							return 0, nil, nil, fmt.Errorf("ae: command set: %w", err)
						}
						expectDS = hasDS
						if !expectDS {
							dsDone = true
						}
					}
				} else {
					dsBuf = append(dsBuf, frag...)
					if pdv.IsLast() {
						dsDone = true
					}
				}
			}
			if gotCmd && cmdDone && dsDone {
				return pcid, cmdBuf, dsBuf, nil
			}
		case *pdu.AAbort:
			return 0, nil, nil, fmt.Errorf("%w: source=%d reason=%d", ErrAborted, p.Source, p.ReasonDiagnostic)
		default:
			return 0, nil, nil, fmt.Errorf("ae: unexpected PDU %T while receiving message", p)
		}
	}
}

// Release performs association release and closes the connection.
func (a *Association) Release(ctx context.Context) error {
	if a.conn == nil {
		return nil
	}
	defer func() { _ = a.closeConn() }()
	if err := a.writePDU(ctx, &pdu.AReleaseRQ{}); err != nil {
		return err
	}
	raw, err := a.readPDU(ctx)
	if err != nil {
		return err
	}
	switch raw.(type) {
	case *pdu.AReleaseRP:
		return nil
	case *pdu.AAbort:
		return ErrAborted
	default:
		return fmt.Errorf("ae: unexpected PDU %T for release", raw)
	}
}

// Abort sends A-ABORT and closes the connection.
func (a *Association) Abort() error {
	if a.conn == nil {
		return nil
	}
	defer func() { _ = a.closeConn() }()
	_ = pdu.Write(a.conn, &pdu.AAbort{Source: 0x00, ReasonDiagnostic: 0x00})
	return nil
}

// Close closes the underlying connection without release negotiation.
func (a *Association) Close() error {
	return a.closeConn()
}

func (a *Association) closeConn() error {
	if a.conn == nil {
		return nil
	}
	err := a.conn.Close()
	a.conn = nil
	return err
}

func (a *Association) writePDU(ctx context.Context, p pdu.PDU) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = a.conn.SetWriteDeadline(deadline)
		defer func() { _ = a.conn.SetWriteDeadline(time.Time{}) }()
	}
	if err := pdu.Write(a.conn, p); err != nil {
		return fmt.Errorf("ae: write PDU: %w", err)
	}
	return nil
}

func (a *Association) readPDU(ctx context.Context) (pdu.PDU, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = a.conn.SetReadDeadline(deadline)
		defer func() { _ = a.conn.SetReadDeadline(time.Time{}) }()
	}
	p, err := pdu.Read(a.conn)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("ae: connection closed: %w", err)
		}
		return nil, fmt.Errorf("ae: read PDU: %w", err)
	}
	return p, nil
}
