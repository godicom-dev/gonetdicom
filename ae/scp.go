package ae

import (
	"context"
	"fmt"
	"net"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// StoreHandler handles an incoming C-STORE-RQ on an SCP association.
// Return a DIMSE status (0x0000 = success).
type StoreHandler func(ctx context.Context, req StoreRequest) uint16

// FindHandler handles an incoming C-FIND-RQ. Return Pending matches followed by
// a final Success/Failure/Cancel response (without Identifier).
type FindHandler func(ctx context.Context, req FindRequest) []FindMatch

// MoveHandler handles an incoming C-MOVE-RQ. Return Pending responses followed
// by a final status. Sub-operation C-STORE to MoveDestination is out of scope
// for this MVP (status/sub-op counts only).
type MoveHandler func(ctx context.Context, req MoveRequest) []RetrieveMatch

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
}

// ServerConfig configures an Association acceptor (SCP).
type ServerConfig struct {
	AETitle                   string
	MaxPDULength              uint32
	ImplementationClassUID    string
	ImplementationVersionName string
	// AcceptedAbstractSyntaxes lists SOP Class UIDs the SCP will accept
	// (plus Verification is always accepted for C-ECHO).
	AcceptedAbstractSyntaxes []string
	OnCStore                 StoreHandler
	OnCFind                  FindHandler
	OnCMove                  MoveHandler
	OnCGet                   GetHandler
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
			_ = handleAssociation(ctx, c, cfg)
		}(conn)
	}
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

	allowed := map[string]struct{}{
		pdu.VerificationSOPClass: {},
	}
	for _, uid := range cfg.AcceptedAbstractSyntaxes {
		allowed[uid] = struct{}{}
	}

	var acContexts []pdu.PresentationContextAC
	accepted := map[byte]acceptedContext{}
	for _, pc := range rq.PresentationContexts {
		_, ok := allowed[pc.AbstractSyntax]
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
		acContexts = append(acContexts, pdu.PresentationContextAC{
			ID:             pc.ID,
			Result:         0,
			TransferSyntax: ts,
		})
		accepted[pc.ID] = acceptedContext{
			AbstractSyntax: pc.AbstractSyntax,
			TransferSyntax: ts,
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
		},
	}
	if err := pdu.Write(conn, ac); err != nil {
		return err
	}

	peerMax := rq.UserInformation.MaxLength
	return scpLoop(ctx, conn, cfg, accepted, peerMax)
}

func scpLoop(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32) error {
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
			if err := scpHandleMessage(ctx, conn, cfg, accepted, peerMax, pcid, cmdBuf, dsBuf); err != nil {
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

func scpHandleMessage(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, cmd, ds []byte) error {
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
		return scpHandleMove(ctx, conn, cfg, accepted, peerMax, pcid, moveRQ, ds)
	}

	if getRQ, err := dimse.DecodeCGetRQ(cmd); err == nil {
		return scpHandleGet(ctx, conn, cfg, accepted, peerMax, pcid, getRQ, ds)
	}

	rq, err := dimse.DecodeCStoreRQ(cmd)
	if err != nil {
		return fmt.Errorf("ae: unsupported DIMSE command: %w", err)
	}
	status := uint16(0x0122) // SOP class not supported
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
			Status:                    0x0122,
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

	for _, m := range matches {
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
	}
	return nil
}

func scpHandleMove(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CMoveRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	if !ok || ac.AbstractSyntax != rq.AffectedSOPClassUID {
		rsp, err := (&dimse.CMoveRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    0x0122,
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

	var matches []RetrieveMatch
	if cfg.OnCMove != nil {
		matches = cfg.OnCMove(ctx, MoveRequest{
			QueryModel:      rq.AffectedSOPClassUID,
			MoveDestination: rq.MoveDestination,
			Identifier:      ds,
			IdentifierData:  ident,
			Priority:        rq.Priority,
		})
	} else {
		matches = []RetrieveMatch{{Status: dimse.StatusSuccess}}
	}
	if len(matches) == 0 {
		matches = []RetrieveMatch{{Status: dimse.StatusSuccess}}
	}
	return writeRetrieveResponses(conn, pcid, peerMax, ac.TransferSyntax, rq.MessageID, rq.AffectedSOPClassUID, true, matches)
}

func scpHandleGet(ctx context.Context, conn net.Conn, cfg ServerConfig, accepted map[byte]acceptedContext, peerMax uint32, pcid byte, rq *dimse.CGetRQ, ds []byte) error {
	ac, ok := accepted[pcid]
	if !ok || ac.AbstractSyntax != rq.AffectedSOPClassUID {
		rsp, err := (&dimse.CGetRSP{
			MessageIDBeingRespondedTo: rq.MessageID,
			AffectedSOPClassUID:       rq.AffectedSOPClassUID,
			Status:                    0x0122,
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
			payload, err = m.Identifier.Encode(transferSyntax)
			if err != nil {
				return fmt.Errorf("ae: encode retrieve identifier: %w", err)
			}
		}
		if err := writeMessage(conn, pcid, rsp, payload, peerMax); err != nil {
			return err
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
