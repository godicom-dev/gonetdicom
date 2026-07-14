package ae

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
)

// StoreHandler handles an incoming C-STORE-RQ on an SCP association.
// Return a DIMSE status (dcmstatus.Success / dimse.StatusSuccess on success).
type StoreHandler func(ctx context.Context, req StoreRequest) uint16

// FindHandler handles an incoming C-FIND-RQ. Return Pending matches followed by
// a final Success/Failure/Cancel response (without Identifier).
type FindHandler func(ctx context.Context, req FindRequest) []FindMatch

// MoveHandler handles an incoming C-MOVE-RQ. Return a MovePlan with Stores to
// C-STORE to MoveDestination and/or explicit Responses.
type MoveHandler func(ctx context.Context, req MoveRequest) MovePlan

// MovePlan is the SCP response plan for a C-MOVE-RQ.
//
// When Stores is non-empty and ServerConfig.MoveDestinations resolves the
// Move Destination, the SCP opens a Storage SCU association and performs
// C-STORE sub-operations. If Responses is empty, pending/final C-MOVE-RSP
// statuses are derived from store outcomes.
type MovePlan struct {
	Stores    []StoreRequest
	Responses []RetrieveMatch
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

// GetPlan is the SCP response plan for a C-GET-RQ.
//
// Stores are sent as C-STORE-RQ on the same association (SCU role), then
// Responses are sent as C-GET-RSP.
type GetPlan struct {
	Stores    []StoreRequest
	Responses []RetrieveMatch
}

// GetHandler handles an incoming C-GET-RQ.
type GetHandler func(ctx context.Context, req GetRequest) GetPlan

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
	// AcceptedAbstractSyntaxes lists SOP Class UIDs the SCP will accept
	// (plus Verification is always accepted for C-ECHO).
	// Include "*" to accept any abstract syntax the peer proposes
	// (generic Storage SCP; mirrors pynetdicom AllStoragePresentationContexts usage).
	AcceptedAbstractSyntaxes []string
	OnCStore                 StoreHandler
	OnCFind                  FindHandler
	OnCMove                  MoveHandler
	OnCGet                   GetHandler
	OnNAction                NActionHandler
	OnNEventReport           EventReportHandler
	OnNGet                   NGetHandler
	OnNSet                   NSetHandler
	OnNCreate                NCreateHandler
	OnNDelete                NDeleteHandler
	// MoveDestinations maps Move Destination AE Title → Storage SCP endpoint.
	// Required to perform real C-STORE sub-operations during C-MOVE.
	MoveDestinations map[string]MoveDestination
	// RoleSelections lists SOP Classes for which this SCP supports non-default
	// SCP/SCU roles (values are the *acceptor* capabilities). Empty means
	// default roles only (requestor=SCU, acceptor=SCP) and no role reply items.
	RoleSelections []pdu.RoleSelection
	// OnUserIdentity verifies a User Identity RQ item. Nil means identity is
	// ignored and the association is accepted without an AC response item
	// (pynetdicom default when no EVT_USER_ID handler is bound).
	OnUserIdentity UserIdentityHandler
	// TLS, when non-nil, wraps accepted connections with TLS (use with ServeTLS
	// or a tls.Listener). Ignored by Serve on a plain listener.
	TLS *tls.Config
	// Logger receives optional SCP lifecycle events. Nil disables logging.
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
	return c
}

func (c ServerConfig) log() *slog.Logger {
	return c.Logger
}

// Serve accepts associations on ln until ctx is cancelled.
func Serve(ctx context.Context, ln net.Listener, cfg ServerConfig) error {
	cfg = cfg.withDefaults()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("ae: accept: %w", err)
			}
		}
		go func(c net.Conn) {
			if log := cfg.log(); log != nil {
				log.Info("ae: scp accepted connection", "remote", c.RemoteAddr().String())
			}
			_ = handleAssociation(ctx, c, cfg)
		}(conn)
	}
}

// ListenAndServeTLS listens on addr with cfg.TLS and serves associations.
func ListenAndServeTLS(ctx context.Context, addr string, cfg ServerConfig) error {
	if cfg.TLS == nil {
		return fmt.Errorf("ae: ListenAndServeTLS requires ServerConfig.TLS")
	}
	ln, err := tls.Listen("tcp", addr, cfg.TLS)
	if err != nil {
		return fmt.Errorf("ae: tls listen %s: %w", addr, err)
	}
	defer ln.Close()
	if log := cfg.withDefaults().log(); log != nil {
		log.Info("ae: scp listening (tls)", "addr", ln.Addr().String())
	}
	return Serve(ctx, ln, cfg)
}

func handleAssociation(ctx context.Context, conn net.Conn, cfg ServerConfig) error {
	defer func() { _ = conn.Close() }()

	raw, err := pdu.Read(conn)
	if err != nil {
		return err
	}
	rq, ok := raw.(*pdu.AAssociateRQ)
	if !ok {
		_ = pdu.Write(conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
		return fmt.Errorf("ae: expected A-ASSOCIATE-RQ, got %T", raw)
	}

	var userIdentityAC *pdu.UserIdentityAC
	if rq.UserInformation.UserIdentityRQ != nil && cfg.OnUserIdentity != nil {
		ok, resp := cfg.OnUserIdentity(*rq.UserInformation.UserIdentityRQ)
		if !ok {
			_ = pdu.Write(conn, &pdu.AAssociateRJ{
				Result:           0x02, // transient
				Source:           0x02, // ACSE
				ReasonDiagnostic: 0x01, // no reason given (pynetdicom)
			})
			return fmt.Errorf("%w: user identity verification failed", ErrRejected)
		}
		req := rq.UserInformation.UserIdentityRQ
		if req.PositiveResponseRequested && len(resp) > 0 &&
			req.Type >= pdu.UserIdentityKerberos && req.Type <= pdu.UserIdentityJWT {
			userIdentityAC = &pdu.UserIdentityAC{ServerResponse: append([]byte(nil), resp...)}
		}
	}

	allowed := map[string]struct{}{
		pdu.VerificationSOPClass: {},
	}
	acceptAll := false
	for _, uid := range cfg.AcceptedAbstractSyntaxes {
		if uid == "*" {
			acceptAll = true
			continue
		}
		allowed[uid] = struct{}{}
	}

	var acContexts []pdu.PresentationContextAC
	accepted := map[byte]acceptedContext{}
	rqRoles := roleMap(rq.UserInformation.RoleSelections)
	acRoles := roleMap(cfg.RoleSelections)
	var replyRoles []pdu.RoleSelection

	for _, pc := range rq.PresentationContexts {
		_, ok := allowed[pc.AbstractSyntax]
		if acceptAll {
			ok = true
		}
		if !ok || len(pc.TransferSyntaxes) == 0 {
			acContexts = append(acContexts, pdu.PresentationContextAC{
				ID:     pc.ID,
				Result: 3, // abstract syntax not supported
			})
			continue
		}
		ts := pc.TransferSyntaxes[0]
		for _, cand := range pc.TransferSyntaxes {
			if cand == pdu.ImplicitVRLittleEndian {
				ts = cand
				break
			}
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
		CalledAETitle:          rq.CalledAETitle,
		CallingAETitle:         rq.CallingAETitle,
		ApplicationContextName: rq.ApplicationContextName,
		PresentationContexts:   acContexts,
		UserInformation: pdu.UserInformation{
			MaxLength:                 cfg.MaxPDULength,
			ImplementationClassUID:    cfg.ImplementationClassUID,
			ImplementationVersionName: cfg.ImplementationVersionName,
			RoleSelections:            replyRoles,
			UserIdentityAC:            userIdentityAC,
		},
	}
	if err := pdu.Write(conn, ac); err != nil {
		return err
	}

	peerMax := rq.UserInformation.MaxLength
	return scpLoop(ctx, conn, cfg, accepted, peerMax, rq.CallingAETitle)
}

func scpLoop(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, callingAE string) error {
	var cmdBuf, dsBuf []byte
	var cmdDone, dsDone bool
	var pcid byte

	reset := func() {
		cmdBuf, dsBuf = nil, nil
		cmdDone, dsDone = false, false
		pcid = 0
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := pdu.Read(conn)
		if err != nil {
			return err
		}
		switch p := raw.(type) {
		case *pdu.PDataTF:
			for _, pdv := range p.PDVs {
				if pcid == 0 {
					pcid = pdv.ContextID
				}
				if pdv.IsCommand() {
					cmdBuf = append(cmdBuf, pdv.Fragment()...)
					if pdv.IsLast() {
						cmdDone = true
						hasDS, err := dimse.CommandHasDataset(cmdBuf)
						if err != nil {
							return err
						}
						if !hasDS {
							dsDone = true
						}
					}
				} else {
					dsBuf = append(dsBuf, pdv.Fragment()...)
					if pdv.IsLast() {
						dsDone = true
					}
				}
			}
			if !cmdDone || !dsDone {
				continue
			}
			if err := scpHandleMessage(ctx, conn, cfg, accepted, peerMax, pcid, cmdBuf, dsBuf, callingAE); err != nil {
				return err
			}
			reset()
		case *pdu.AReleaseRQ:
			return pdu.Write(conn, &pdu.AReleaseRP{})
		case *pdu.AAbort:
			return nil
		default:
			_ = pdu.Write(conn, &pdu.AAbort{Source: 0x02, ReasonDiagnostic: 0x00})
			return fmt.Errorf("ae: unexpected PDU %T", p)
		}
	}
}

func scpHandleMessage(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, cmd, ds []byte, callingAE string) error {
	if echoRQ, err := dimse.DecodeCEchoRQ(cmd); err == nil {
		rsp, err := (&dimse.CEchoRSP{
			MessageIDBeingRespondedTo: echoRQ.MessageID,
			Status:                    dimse.StatusSuccess,
		}).Encode()
		if err != nil {
			return err
		}
		return writeMessage(conn, pcid, rsp, nil, peerMax)
	}

	if findRQ, err := dimse.DecodeCFindRQ(cmd); err == nil {
		return scpHandleFind(ctx, conn, cfg, accepted, peerMax, pcid, findRQ, ds)
	}

	if moveRQ, err := dimse.DecodeCMoveRQ(cmd); err == nil {
		return scpHandleMove(ctx, conn, cfg, accepted, peerMax, pcid, moveRQ, ds, callingAE)
	}

	if getRQ, err := dimse.DecodeCGetRQ(cmd); err == nil {
		return scpHandleGet(ctx, conn, cfg, accepted, peerMax, pcid, getRQ, ds)
	}

	if actionRQ, err := dimse.DecodeNActionRQ(cmd); err == nil {
		return scpHandleNAction(ctx, conn, cfg, accepted, peerMax, pcid, actionRQ, ds)
	}

	if erRQ, err := dimse.DecodeNEventReportRQ(cmd); err == nil {
		return scpHandleNEventReport(ctx, conn, cfg, accepted, peerMax, pcid, erRQ, ds)
	}

	if nGetRQ, err := dimse.DecodeNGetRQ(cmd); err == nil {
		return scpHandleNGet(ctx, conn, cfg, accepted, peerMax, pcid, nGetRQ)
	}

	if nSetRQ, err := dimse.DecodeNSetRQ(cmd); err == nil {
		return scpHandleNSet(ctx, conn, cfg, accepted, peerMax, pcid, nSetRQ, ds)
	}

	if nCreateRQ, err := dimse.DecodeNCreateRQ(cmd); err == nil {
		return scpHandleNCreate(ctx, conn, cfg, accepted, peerMax, pcid, nCreateRQ, ds)
	}

	if nDeleteRQ, err := dimse.DecodeNDeleteRQ(cmd); err == nil {
		return scpHandleNDelete(ctx, conn, cfg, accepted, peerMax, pcid, nDeleteRQ)
	}

	rq, err := dimse.DecodeCStoreRQ(cmd)
	if err != nil {
		return fmt.Errorf("ae: unsupported DIMSE command: %w", err)
	}
	status := dcmstatus.SOPClassNotSupported
	if abs, ok := accepted[pcid]; ok && abs.AbstractSyntax == rq.AffectedSOPClassUID {
		if cfg.OnCStore != nil {
			status = cfg.OnCStore(ctx, StoreRequest{
				AffectedSOPClassUID:                  rq.AffectedSOPClassUID,
				AffectedSOPInstanceUID:               rq.AffectedSOPInstanceUID,
				Dataset:                              ds,
				Priority:                             rq.Priority,
				MoveOriginatorApplicationEntityTitle: rq.MoveOriginatorApplicationEntityTitle,
				MoveOriginatorMessageID:              rq.MoveOriginatorMessageID,
			})
		} else {
			status = dimse.StatusSuccess
		}
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
	return writeMessage(conn, pcid, rsp, nil, peerMax)
}

func scpHandleFind(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CFindRQ, ds []byte) error {
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
		return writeMessage(conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
		if err != nil {
			return fmt.Errorf("ae: decode C-FIND identifier: %w", err)
		}
		ident = decoded
	}

	var matches []FindMatch
	if cfg.OnCFind != nil {
		matches = cfg.OnCFind(ctx, FindRequest{
			QueryModel:     rq.AffectedSOPClassUID,
			Identifier:     ds,
			IdentifierData: ident,
			Priority:       rq.Priority,
		})
	} else {
		matches = []FindMatch{{Status: dimse.StatusSuccess}}
	}
	if len(matches) == 0 {
		matches = []FindMatch{{Status: dimse.StatusSuccess}}
	}

	for i, m := range matches {
		if i > 0 && peekCancelRQ(conn, rq.MessageID) {
			rsp, err := (&dimse.CFindRSP{
				MessageIDBeingRespondedTo: rq.MessageID,
				AffectedSOPClassUID:       rq.AffectedSOPClassUID,
				Status:                    dimse.StatusCancel,
			}).Encode()
			if err != nil {
				return err
			}
			return writeMessage(conn, pcid, rsp, nil, peerMax)
		}
		hasDS := m.Identifier != nil && dimse.IsPending(m.Status)
		rsp, err := (&dimse.CFindRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    m.Status,
			HasDataset:                hasDS,
		}).Encode()
		if err != nil {
			return err
		}
		var payload []byte
		if hasDS {
			payload, err = m.Identifier.Encode(ac.TransferSyntax)
			if err != nil {
				return fmt.Errorf("ae: encode C-FIND identifier: %w", err)
			}
		}
		if err := writeMessage(conn, pcid, rsp, payload, peerMax); err != nil {
			return err
		}
		if !dimse.IsPending(m.Status) {
			return nil
		}
	}
	return nil
}

func scpHandleMove(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CMoveRQ, ds []byte, callingAE string) error {
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
		return writeMessage(conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
		if err != nil {
			return fmt.Errorf("ae: decode C-MOVE identifier: %w", err)
		}
		ident = decoded
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
	if len(plan.Stores) > 0 {
		if err := scpPerformMoveStores(ctx, conn, cfg, peerMax, pcid, ac.TransferSyntax, rq, callingAE, plan.Stores); err != nil {
			fail := []RetrieveMatch{{
				Status: dcmstatus.MoveDestinationUnknown,
				SubOperations: dimse.SubOperations{
					Failed: uint16(len(plan.Stores)), Present: true,
				},
			}}
			if len(plan.Responses) > 0 {
				fail = plan.Responses
			}
			return writeRetrieveResponses(conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, fail)
		}
		return nil
	}
	if len(plan.Responses) == 0 {
		plan.Responses = []RetrieveMatch{{Status: dimse.StatusSuccess}}
	}
	return writeRetrieveResponses(conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, plan.Responses)
}

func scpPerformMoveStores(ctx context.Context, conn net.Conn, cfg ServerConfig, peerMax uint32, pcid byte, transferSyntax string, rq *dimse.CMoveRQ, moveOriginatorAE string, stores []StoreRequest) error {
	dest, ok := cfg.MoveDestinations[rq.MoveDestination]
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

	seen := map[string]struct{}{}
	var pcs []PresentationContext
	id := byte(1)
	for _, s := range stores {
		if s.AffectedSOPClassUID == "" {
			continue
		}
		if _, ok := seen[s.AffectedSOPClassUID]; ok {
			continue
		}
		seen[s.AffectedSOPClassUID] = struct{}{}
		pcs = append(pcs, PresentationContext{
			ID:               id,
			AbstractSyntax:   s.AffectedSOPClassUID,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		})
		id += 2
	}
	if len(pcs) == 0 {
		return fmt.Errorf("ae: C-MOVE stores missing SOP Class UID")
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

	total := uint16(len(prepared))
	var (
		mu                         sync.Mutex
		completed, failed, warning uint16
		done                       uint16
		writeErr                   error
	)

	writePending := func(remaining, completed, failed, warning uint16) error {
		return writeRetrieveResponses(conn, pcid, peerMax, transferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, []RetrieveMatch{{
			Status: dimse.StatusPending,
			SubOperations: dimse.SubOperations{
				Remaining: remaining, Completed: completed, Failed: failed, Warning: warning, Present: true,
			},
		}})
	}

	if err := writePending(total, 0, 0, 0); err != nil {
		return err
	}

	record := func(ok bool) {
		mu.Lock()
		defer mu.Unlock()
		if writeErr != nil {
			return
		}
		done++
		if ok {
			completed++
		} else {
			failed++
		}
		writeErr = writePending(total-done, completed, failed, warning)
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

	if maxAssoc == 1 {
		assoc, err := Dial(ctx, dialCfg, dest.Addr, called)
		if err != nil {
			return fmt.Errorf("ae: dial Move Destination: %w", err)
		}
		defer func() { _ = assoc.Release(ctx) }()
		for _, store := range prepared {
			res, err := assoc.CStore(ctx, store)
			record(err == nil && res != nil && res.Status == dimse.StatusSuccess)
			if writeErr != nil {
				return writeErr
			}
		}
	} else {
		jobs := make(chan StoreRequest, len(prepared))
		for _, store := range prepared {
			jobs <- store
		}
		close(jobs)

		var wg sync.WaitGroup
		for w := 0; w < maxAssoc; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assoc, err := Dial(ctx, dialCfg, dest.Addr, called)
				if err != nil {
					return
				}
				defer func() { _ = assoc.Release(ctx) }()
				for store := range jobs {
					res, err := assoc.CStore(ctx, store)
					record(err == nil && res != nil && res.Status == dimse.StatusSuccess)
				}
			}()
		}
		wg.Wait()
		if writeErr != nil {
			return writeErr
		}
		mu.Lock()
		if remaining := total - done; remaining > 0 {
			failed += remaining
		}
		mu.Unlock()
	}

	mu.Lock()
	c, f, w := completed, failed, warning
	mu.Unlock()

	final := dimse.StatusSuccess
	if f > 0 && c > 0 {
		final = dimse.StatusWarning
	} else if f > 0 {
		final = dcmstatus.UnableToPerformSubOperations
	}
	return writeRetrieveResponses(conn, pcid, peerMax, transferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, []RetrieveMatch{{
		Status: final,
		SubOperations: dimse.SubOperations{
			Remaining: 0, Completed: c, Failed: f, Warning: w, Present: true,
		},
	}})
}

func scpHandleGet(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CGetRQ, ds []byte) error {
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
		return writeMessage(conn, pcid, rsp, nil, peerMax)
	}

	var ident *godicom.Dataset
	if len(ds) > 0 {
		decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
		if err != nil {
			return fmt.Errorf("ae: decode C-GET identifier: %w", err)
		}
		ident = decoded
	}

	var plan GetPlan
	if cfg.OnCGet != nil {
		plan = cfg.OnCGet(ctx, GetRequest{
			QueryModel:     rq.AffectedSOPClassUID,
			Identifier:     ds,
			IdentifierData: ident,
			Priority:       rq.Priority,
		})
	}
	if len(plan.Responses) == 0 {
		plan.Responses = []RetrieveMatch{{Status: dimse.StatusSuccess}}
	}

	var storeMsgID uint16 = 1
	for _, store := range plan.Stores {
		storePCID, storeTS, ok := contextByAbstractAccepted(accepted, store.AffectedSOPClassUID)
		if !ok {
			return fmt.Errorf("ae: C-GET store missing presentation context for %s", store.AffectedSOPClassUID)
		}
		payload := store.Dataset
		if store.Data != nil {
			encoded, err := store.Data.Encode(storeTS)
			if err != nil {
				return fmt.Errorf("ae: encode C-GET store dataset: %w", err)
			}
			payload = encoded
		}
		priority := store.Priority
		if priority == 0 {
			priority = dimse.PriorityLow
		}
		cmd, err := (&dimse.CStoreRQ{
			MessageID:                            storeMsgID,
			Priority:                             priority,
			AffectedSOPClassUID:                  store.AffectedSOPClassUID,
			AffectedSOPInstanceUID:               store.AffectedSOPInstanceUID,
			MoveOriginatorApplicationEntityTitle: store.MoveOriginatorApplicationEntityTitle,
			MoveOriginatorMessageID:              store.MoveOriginatorMessageID,
			HasMoveOriginator:                    store.MoveOriginatorApplicationEntityTitle != "",
		}).Encode()
		if err != nil {
			return err
		}
		storeMsgID++
		if err := writeMessage(conn, storePCID, cmd, payload, peerMax); err != nil {
			return err
		}
		rspCmd, _, err := readMessage(conn)
		if err != nil {
			return err
		}
		if _, err := dimse.DecodeCStoreRSP(rspCmd); err != nil {
			return fmt.Errorf("ae: decode C-STORE-RSP during C-GET: %w", err)
		}
	}

	return writeRetrieveResponses(conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, false, plan.Responses)
}

func writeRetrieveResponses(conn net.Conn, pcid byte, peerMax uint32, transferSyntax string, msgID uint16, sopClass string, isMove bool, matches []RetrieveMatch) error {
	for i, m := range matches {
		if i > 0 && peekCancelRQ(conn, msgID) {
			var (
				rsp []byte
				err error
			)
			if isMove {
				rsp, err = (&dimse.CMoveRSP{
					MessageIDBeingRespondedTo: msgID,
					AffectedSOPClassUID:       sopClass,
					Status:                    dimse.StatusCancel,
				}).Encode()
			} else {
				rsp, err = (&dimse.CGetRSP{
					MessageIDBeingRespondedTo: msgID,
					AffectedSOPClassUID:       sopClass,
					Status:                    dimse.StatusCancel,
				}).Encode()
			}
			if err != nil {
				return err
			}
			return writeMessage(conn, pcid, rsp, nil, peerMax)
		}
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
			payload, err = m.Identifier.Encode(transferSyntax)
			if err != nil {
				return fmt.Errorf("ae: encode retrieve identifier: %w", err)
			}
		}
		if err := writeMessage(conn, pcid, rsp, payload, peerMax); err != nil {
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

func readMessage(conn net.Conn) (command, dataset []byte, err error) {
	var (
		cmdBuf  []byte
		dsBuf   []byte
		cmdDone bool
		dsDone  bool
	)
	for {
		raw, err := pdu.Read(conn)
		if err != nil {
			return nil, nil, err
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			return nil, nil, fmt.Errorf("ae: unexpected PDU %T while reading message", raw)
		}
		for _, pdv := range p.PDVs {
			if pdv.IsCommand() {
				cmdBuf = append(cmdBuf, pdv.Fragment()...)
				if pdv.IsLast() {
					cmdDone = true
					hasDS, err := dimse.CommandHasDataset(cmdBuf)
					if err != nil {
						return nil, nil, err
					}
					if !hasDS {
						dsDone = true
					}
				}
			} else {
				dsBuf = append(dsBuf, pdv.Fragment()...)
				if pdv.IsLast() {
					dsDone = true
				}
			}
		}
		if cmdDone && dsDone {
			return cmdBuf, dsBuf, nil
		}
	}
}

func writeMessage(conn net.Conn, pcid byte, command, dataset []byte, maxPDU uint32) error {
	pdus, err := pdu.FragmentMessage(pcid, command, dataset, maxPDU)
	if err != nil {
		return err
	}
	for _, p := range pdus {
		if err := pdu.Write(conn, p); err != nil {
			return err
		}
	}
	return nil
}

func scpHandleNAction(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NActionRQ, ds []byte) error {
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
			decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
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
		encoded, err := result.ActionReplyData.Encode(ac.TransferSyntax)
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
	if err := writeMessage(conn, pcid, rsp, reply, peerMax); err != nil {
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
	return scpSendEventReport(conn, accepted, peerMax, push)
}

func scpHandleNEventReport(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.NEventReportRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	status := dcmstatus.SOPClassNotSupported
	if ok && ac.AbstractSyntax == rq.AffectedSOPClassUID {
		var info *godicom.Dataset
		if len(ds) > 0 {
			decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
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
	return writeMessage(conn, pcid, rsp, nil, peerMax)
}

func scpSendEventReport(conn net.Conn, accepted map[byte]acceptedContext, peerMax uint32, req *EventReportRequest) error {
	pcid, ts, ok := contextByAbstractAccepted(accepted, req.AffectedSOPClassUID)
	if !ok {
		return fmt.Errorf("ae: no context for N-EVENT-REPORT %s", req.AffectedSOPClassUID)
	}
	payload := req.EventInformation
	if req.EventInformationData != nil {
		encoded, err := req.EventInformationData.Encode(ts)
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
	if err := writeMessage(conn, pcid, cmd, payload, peerMax); err != nil {
		return err
	}
	rspCmd, _, err := readMessage(conn)
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

	calling := dest.CallingAE
	if calling == "" {
		calling = cfg.AETitle
	}
	called := dest.CalledAE
	if called == "" {
		if log := cfg.log(); log != nil {
			log.Error("ae: async N-EVENT-REPORT missing CalledAE")
		}
		return
	}
	if req.AffectedSOPClassUID == "" {
		if log := cfg.log(); log != nil {
			log.Error("ae: async N-EVENT-REPORT missing Affected SOP Class UID")
		}
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
		if log := cfg.log(); log != nil {
			log.Error("ae: async N-EVENT-REPORT dial failed", "addr", dest.Addr, "err", err)
		}
		return
	}
	defer func() { _ = assoc.Release(ctx) }()

	if _, err := assoc.NEventReport(ctx, req); err != nil {
		if log := cfg.log(); log != nil {
			log.Error("ae: async N-EVENT-REPORT failed", "err", err)
		}
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
	return writeNAttributeRSP(conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
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
			decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
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
	return writeNAttributeRSP(conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
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
			decoded, err := godicom.DecodeDataset(ds, ac.TransferSyntax)
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
	}
	if result.AffectedSOPInstanceUID == "" {
		return fmt.Errorf("ae: N-CREATE-RSP missing Affected SOP Instance UID")
	}
	return writeNAttributeRSP(conn, peerMax, pcid, ok, ac, rq.MessageID, result.Status,
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
	return writeMessage(conn, pcid, rsp, nil, peerMax)
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

func writeNAttributeRSP(conn net.Conn, peerMax uint32, pcid byte, ok bool, ac acceptedContext, msgID uint16, status uint16, classUID, instanceUID string, list []byte, listData *godicom.Dataset, enc nAttrEncoder) error {
	payload := list
	if listData != nil {
		if !ok {
			return fmt.Errorf("ae: cannot encode attribute list without accepted context")
		}
		encoded, err := listData.Encode(ac.TransferSyntax)
		if err != nil {
			return fmt.Errorf("ae: encode attribute list: %w", err)
		}
		payload = encoded
	}
	rsp, err := enc(msgID, status, classUID, instanceUID, len(payload) > 0)
	if err != nil {
		return err
	}
	return writeMessage(conn, pcid, rsp, payload, peerMax)
}
