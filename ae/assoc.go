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

// Config configures an Application Entity for outbound associations.
type Config struct {
	AETitle                   string
	MaxPDULength              uint32
	ImplementationClassUID    string
	ImplementationVersionName string
	DialTimeout               time.Duration
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
	return c
}

// Association is an established DIMSE association (SCU role).
type Association struct {
	conn   net.Conn
	cfg    Config
	called string
	ac     *pdu.AAssociateAC
	nextID uint16
}

// Dial associates with addr (host:port) as an SCU requesting Verification.
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
// Useful for tests (net.Pipe) and custom dialers.
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
	rq := &pdu.AAssociateRQ{
		CalledAETitle:          a.called,
		CallingAETitle:         a.cfg.AETitle,
		ApplicationContextName: pdu.ApplicationContextName,
		PresentationContexts: []pdu.PresentationContextRQ{{
			ID:               1,
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
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
		a.ac = p
		if a.verificationContextID() == 0 {
			return fmt.Errorf("%w: verification not accepted", ErrNoContext)
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

func (a *Association) verificationContextID() byte {
	if a.ac == nil {
		return 0
	}
	for _, pc := range a.ac.PresentationContexts {
		if pc.Result == 0 && pc.ID != 0 {
			return pc.ID
		}
	}
	return 0
}

// CEcho sends a C-ECHO-RQ and waits for a successful C-ECHO-RSP.
func (a *Association) CEcho(ctx context.Context) error {
	pcid := a.verificationContextID()
	if pcid == 0 {
		return ErrNoContext
	}
	msgID := a.nextID
	a.nextID++
	if a.nextID == 0 {
		a.nextID = 1
	}

	cmd, err := (&dimse.CEchoRQ{MessageID: msgID}).Encode()
	if err != nil {
		return err
	}
	tf := &pdu.PDataTF{PDVs: []pdu.PDV{pdu.NewCommandPDV(pcid, cmd)}}
	if err := a.writePDU(ctx, tf); err != nil {
		return err
	}

	raw, err := a.readPDU(ctx)
	if err != nil {
		return err
	}
	pd, ok := raw.(*pdu.PDataTF)
	if !ok {
		if ab, ok := raw.(*pdu.AAbort); ok {
			return fmt.Errorf("%w: source=%d reason=%d", ErrAborted, ab.Source, ab.ReasonDiagnostic)
		}
		return fmt.Errorf("ae: unexpected PDU %T for C-ECHO", raw)
	}
	if len(pd.PDVs) == 0 || !pd.PDVs[0].IsCommand() {
		return fmt.Errorf("ae: C-ECHO response missing command PDV")
	}
	rsp, err := dimse.DecodeCEchoRSP(pd.PDVs[0].Fragment())
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

// Release performs association release and closes the connection.
func (a *Association) Release(ctx context.Context) error {
	if a.conn == nil {
		return nil
	}
	defer a.closeConn()
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
	defer a.closeConn()
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
		defer a.conn.SetWriteDeadline(time.Time{})
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
		defer a.conn.SetReadDeadline(time.Time{})
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
