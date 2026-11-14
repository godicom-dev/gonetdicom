package pdu

import (
	"encoding/binary"
	"fmt"
)

// PresentationContextRQ is a proposed presentation context in A-ASSOCIATE-RQ.
type PresentationContextRQ struct {
	ID               byte
	AbstractSyntax   string
	TransferSyntaxes []string
}

// PresentationContextAC is an accepted/rejected context in A-ASSOCIATE-AC.
type PresentationContextAC struct {
	ID             byte
	Result         byte // 0 = acceptance
	TransferSyntax string
}

// RoleSelection is an SCP/SCU Role Selection Negotiation sub-item (0x54).
// Aligned with pynetdicom SCP_SCU_RoleSelectionNegotiation.
type RoleSelection struct {
	SOPClassUID string
	SCURole     bool
	SCPRole     bool
}

// UserIdentityRQ is a User Identity Negotiation request sub-item (0x58).
// Aligned with pynetdicom UserIdentitySubItemRQ.
type UserIdentityRQ struct {
	Type                      byte
	PositiveResponseRequested bool
	PrimaryField              []byte
	SecondaryField            []byte // used when Type == UserIdentityUsernamePasscode
}

// UserIdentityAC is a User Identity Negotiation response sub-item (0x59).
// Aligned with pynetdicom UserIdentitySubItemAC.
type UserIdentityAC struct {
	ServerResponse []byte
}

// AsyncOperationsWindow is an Asynchronous Operations Window Negotiation
// sub-item (0x53), PS3.8 D.3.3.3.
//
// MaxInvoked is how many operations the sender may have outstanding at once,
// MaxPerformed how many it will execute. Zero means unlimited, which is why the
// type is a struct with an explicit nil on UserInformation: "not negotiated" and
// "negotiated with 0" are different answers.
type AsyncOperationsWindow struct {
	MaxInvoked   uint16
	MaxPerformed uint16
}

// Unlimited reports whether the window is the protocol's "no limit" value.
func (w AsyncOperationsWindow) Unlimited() bool {
	return w.MaxInvoked == 0 && w.MaxPerformed == 0
}

// ExtendedNegotiation is a SOP Class Extended Negotiation sub-item (0x56),
// PS3.8 D.3.3.5. Info is the Service Class Application Information, whose
// meaning is defined by the SOP Class rather than by DIMSE.
type ExtendedNegotiation struct {
	SOPClassUID string
	Info        []byte
}

// CommonExtendedNegotiation is a SOP Class Common Extended Negotiation
// sub-item (0x57), PS3.8 D.3.3.6.
//
// It answers, for one SOP Class, two questions that the presentation contexts
// cannot: which Service Class the class is used under, and which more general
// SOP Classes it may stand in for. Storage Commitment is the driving case —
// the class a peer stores under and the Service Class that commits to it are
// different, and a requestor needs to know the pairing before it sends
// anything.
//
// ServiceClassUID is required by the encoding even when empty would read as
// "no service class": the field has a length, and a zero length is not the
// protocol's way of saying absent — omitting the whole sub-item is.
type CommonExtendedNegotiation struct {
	SOPClassUID     string
	ServiceClassUID string
	// RelatedGeneralSOPClasses are SOP Classes this one may be matched to
	// when a peer queries for the general class (PS3.4 C.4.1.1.3).
	RelatedGeneralSOPClasses []string
}

// UserInformation carries association negotiation user items.
type UserInformation struct {
	MaxLength                  uint32
	ImplementationClassUID     string
	ImplementationVersionName  string
	AsyncOperationsWindow      *AsyncOperationsWindow
	ExtendedNegotiations       []ExtendedNegotiation
	CommonExtendedNegotiations []CommonExtendedNegotiation
	RoleSelections             []RoleSelection
	UserIdentityRQ             *UserIdentityRQ // A-ASSOCIATE-RQ only
	UserIdentityAC             *UserIdentityAC // A-ASSOCIATE-AC only
}

// ProtocolVersion1 is bit 0 of the Protocol-version field, identifying DICOM
// Upper Layer protocol version 1 — the only version defined.
//
// The field is a bit field with one bit per version (PS3.8 9.3.2), so an
// acceptor tests bit 0 rather than comparing the whole field: a requestor that
// also announces some future version still speaks version 1.
const ProtocolVersion1 uint16 = 0x0001

// A-ASSOCIATE-RJ Result values (PS3.8 Table 9-21).
const (
	RejectResultPermanent byte = 0x01
	RejectResultTransient byte = 0x02
)

// A-ASSOCIATE-RJ Source values (PS3.8 Table 9-21). The Source decides how the
// Reason/Diag byte is interpreted.
const (
	RejectSourceServiceUser         byte = 0x01 // DICOM UL service-user
	RejectSourceServiceProviderACSE byte = 0x02 // service-provider, ACSE related
	RejectSourceServiceProviderPres byte = 0x03 // service-provider, presentation related
)

// A-ASSOCIATE-RJ Reason/Diag values (PS3.8 Table 9-21), grouped by the Source
// they belong to. Values repeat across sources, so pick the one matching the
// Source being sent.
const (
	// Source = RejectSourceServiceUser.
	RejectReasonNoReasonGiven          byte = 0x01
	RejectReasonAppContextNotSupported byte = 0x02
	RejectReasonCallingAENotRecognized byte = 0x03
	RejectReasonCalledAENotRecognized  byte = 0x07

	// Source = RejectSourceServiceProviderACSE.
	RejectReasonProtocolVersionNotSupported byte = 0x02

	// Source = RejectSourceServiceProviderPres.
	RejectReasonTemporaryCongestion byte = 0x01
	RejectReasonLocalLimitExceeded  byte = 0x02
)

// AAssociateRQ is an A-ASSOCIATE-RQ PDU.
type AAssociateRQ struct {
	ProtocolVersion        uint16
	CalledAETitle          string
	CallingAETitle         string
	ApplicationContextName string
	PresentationContexts   []PresentationContextRQ
	UserInformation        UserInformation
}

func (p *AAssociateRQ) Type() byte { return TypeAAssociateRQ }

// Encode serializes the PDU.
func (p *AAssociateRQ) Encode() ([]byte, error) {
	called, err := PadAETitle(p.CalledAETitle)
	if err != nil {
		return nil, fmt.Errorf("called AE: %w", err)
	}
	calling, err := PadAETitle(p.CallingAETitle)
	if err != nil {
		return nil, fmt.Errorf("calling AE: %w", err)
	}
	appCtx := p.ApplicationContextName
	if appCtx == "" {
		appCtx = ApplicationContextName
	}

	varItems, err := appendItem(nil, ItemApplicationContext, []byte(appCtx))
	if err != nil {
		return nil, err
	}
	for _, pc := range p.PresentationContexts {
		data := make([]byte, 4)
		data[0] = pc.ID
		if data, err = appendItem(data, ItemAbstractSyntax, []byte(pc.AbstractSyntax)); err != nil {
			return nil, err
		}
		for _, ts := range pc.TransferSyntaxes {
			if data, err = appendItem(data, ItemTransferSyntax, []byte(ts)); err != nil {
				return nil, err
			}
		}
		if varItems, err = appendItem(varItems, ItemPresentationContextRQ, data); err != nil {
			return nil, err
		}
	}

	ui, err := encodeUserInformation(p.UserInformation)
	if err != nil {
		return nil, err
	}
	varItems = append(varItems, ui...)

	body := make([]byte, 68+len(varItems))
	ver := p.ProtocolVersion
	if ver == 0 {
		ver = 1
	}
	binary.BigEndian.PutUint16(body[0:2], ver)
	copy(body[4:20], called[:])
	copy(body[20:36], calling[:])
	copy(body[68:], varItems)
	return encodeHeader(TypeAAssociateRQ, body)
}

// DecodeAAssociateRQ parses an A-ASSOCIATE-RQ PDU.
func DecodeAAssociateRQ(raw []byte) (*AAssociateRQ, error) {
	if len(raw) < 74 {
		return nil, fmt.Errorf("pdu: A-ASSOCIATE-RQ too short: %d", len(raw))
	}
	if raw[0] != TypeAAssociateRQ {
		return nil, fmt.Errorf("%w: got 0x%02x want A-ASSOCIATE-RQ", ErrUnexpectedType, raw[0])
	}
	length := binary.BigEndian.Uint32(raw[2:6])
	if 6+uint64(length) != uint64(len(raw)) {
		return nil, fmt.Errorf("pdu: A-ASSOCIATE-RQ length mismatch")
	}
	p := &AAssociateRQ{
		ProtocolVersion:        binary.BigEndian.Uint16(raw[6:8]),
		CalledAETitle:          TrimAETitle(raw[10:26]),
		CallingAETitle:         TrimAETitle(raw[26:42]),
		ApplicationContextName: ApplicationContextName,
	}
	items, err := decodeItems(raw[74:])
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		switch it.Type {
		case ItemApplicationContext:
			p.ApplicationContextName = trimUIDPadding(it.Data)
		case ItemPresentationContextRQ:
			pc, err := decodePresentationContextRQ(it.Data)
			if err != nil {
				return nil, err
			}
			p.PresentationContexts = append(p.PresentationContexts, pc)
		case ItemUserInformation:
			ui, err := decodeUserInformation(it.Data)
			if err != nil {
				return nil, err
			}
			p.UserInformation = ui
		}
	}
	return p, nil
}

// AAssociateAC is an A-ASSOCIATE-AC PDU.
type AAssociateAC struct {
	ProtocolVersion        uint16
	CalledAETitle          string
	CallingAETitle         string
	ApplicationContextName string
	PresentationContexts   []PresentationContextAC
	UserInformation        UserInformation
}

func (p *AAssociateAC) Type() byte { return TypeAAssociateAC }

// Encode serializes the PDU.
func (p *AAssociateAC) Encode() ([]byte, error) {
	called, err := PadAETitle(p.CalledAETitle)
	if err != nil {
		return nil, fmt.Errorf("called AE: %w", err)
	}
	calling, err := PadAETitle(p.CallingAETitle)
	if err != nil {
		return nil, fmt.Errorf("calling AE: %w", err)
	}
	appCtx := p.ApplicationContextName
	if appCtx == "" {
		appCtx = ApplicationContextName
	}

	varItems, err := appendItem(nil, ItemApplicationContext, []byte(appCtx))
	if err != nil {
		return nil, err
	}
	for _, pc := range p.PresentationContexts {
		data := make([]byte, 4)
		data[0] = pc.ID
		data[2] = pc.Result
		if pc.TransferSyntax != "" {
			if data, err = appendItem(data, ItemTransferSyntax, []byte(pc.TransferSyntax)); err != nil {
				return nil, err
			}
		}
		if varItems, err = appendItem(varItems, ItemPresentationContextAC, data); err != nil {
			return nil, err
		}
	}
	ui, err := encodeUserInformation(p.UserInformation)
	if err != nil {
		return nil, err
	}
	varItems = append(varItems, ui...)

	body := make([]byte, 68+len(varItems))
	ver := p.ProtocolVersion
	if ver == 0 {
		ver = 1
	}
	binary.BigEndian.PutUint16(body[0:2], ver)
	copy(body[4:20], called[:])
	copy(body[20:36], calling[:])
	copy(body[68:], varItems)
	return encodeHeader(TypeAAssociateAC, body)
}

// DecodeAAssociateAC parses an A-ASSOCIATE-AC PDU.
func DecodeAAssociateAC(raw []byte) (*AAssociateAC, error) {
	if len(raw) < 74 {
		return nil, fmt.Errorf("pdu: A-ASSOCIATE-AC too short: %d", len(raw))
	}
	if raw[0] != TypeAAssociateAC {
		return nil, fmt.Errorf("%w: got 0x%02x want A-ASSOCIATE-AC", ErrUnexpectedType, raw[0])
	}
	length := binary.BigEndian.Uint32(raw[2:6])
	if 6+uint64(length) != uint64(len(raw)) {
		return nil, fmt.Errorf("pdu: A-ASSOCIATE-AC length mismatch")
	}
	p := &AAssociateAC{
		ProtocolVersion:        binary.BigEndian.Uint16(raw[6:8]),
		CalledAETitle:          TrimAETitle(raw[10:26]),
		CallingAETitle:         TrimAETitle(raw[26:42]),
		ApplicationContextName: ApplicationContextName,
	}
	items, err := decodeItems(raw[74:])
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		switch it.Type {
		case ItemApplicationContext:
			p.ApplicationContextName = trimUIDPadding(it.Data)
		case ItemPresentationContextAC:
			pc, err := decodePresentationContextAC(it.Data)
			if err != nil {
				return nil, err
			}
			p.PresentationContexts = append(p.PresentationContexts, pc)
		case ItemUserInformation:
			ui, err := decodeUserInformation(it.Data)
			if err != nil {
				return nil, err
			}
			p.UserInformation = ui
		}
	}
	return p, nil
}

// AAssociateRJ is an A-ASSOCIATE-RJ PDU.
type AAssociateRJ struct {
	Result           byte
	Source           byte
	ReasonDiagnostic byte
}

func (p *AAssociateRJ) Type() byte { return TypeAAssociateRJ }

// Encode serializes the PDU.
func (p *AAssociateRJ) Encode() ([]byte, error) {
	body := []byte{0x00, p.Result, p.Source, p.ReasonDiagnostic}
	return encodeHeader(TypeAAssociateRJ, body)
}

// DecodeAAssociateRJ parses an A-ASSOCIATE-RJ PDU.
func DecodeAAssociateRJ(raw []byte) (*AAssociateRJ, error) {
	if len(raw) != 10 {
		return nil, fmt.Errorf("pdu: A-ASSOCIATE-RJ length %d", len(raw))
	}
	if raw[0] != TypeAAssociateRJ {
		return nil, fmt.Errorf("%w: got 0x%02x want A-ASSOCIATE-RJ", ErrUnexpectedType, raw[0])
	}
	return &AAssociateRJ{
		Result:           raw[7],
		Source:           raw[8],
		ReasonDiagnostic: raw[9],
	}, nil
}

func decodePresentationContextRQ(data []byte) (PresentationContextRQ, error) {
	if len(data) < 4 {
		return PresentationContextRQ{}, fmt.Errorf("pdu: short presentation context RQ")
	}
	pc := PresentationContextRQ{ID: data[0]}
	subs, err := decodeItems(data[4:])
	if err != nil {
		return PresentationContextRQ{}, err
	}
	for _, s := range subs {
		switch s.Type {
		case ItemAbstractSyntax:
			pc.AbstractSyntax = trimUIDPadding(s.Data)
		case ItemTransferSyntax:
			pc.TransferSyntaxes = append(pc.TransferSyntaxes, trimUIDPadding(s.Data))
		}
	}
	return pc, nil
}

func decodePresentationContextAC(data []byte) (PresentationContextAC, error) {
	if len(data) < 4 {
		return PresentationContextAC{}, fmt.Errorf("pdu: short presentation context AC")
	}
	pc := PresentationContextAC{ID: data[0], Result: data[2]}
	subs, err := decodeItems(data[4:])
	if err != nil {
		return PresentationContextAC{}, err
	}
	for _, s := range subs {
		if s.Type == ItemTransferSyntax {
			pc.TransferSyntax = trimUIDPadding(s.Data)
			break
		}
	}
	return pc, nil
}

func encodeUserInformation(ui UserInformation) ([]byte, error) {
	if ui.ImplementationClassUID == "" {
		return nil, fmt.Errorf("pdu: missing implementation class UID")
	}
	maxLen := make([]byte, 4)
	binary.BigEndian.PutUint32(maxLen, ui.MaxLength)
	data, err := appendItem(nil, ItemMaxLength, maxLen)
	if err != nil {
		return nil, err
	}
	if data, err = appendItem(data, ItemImplementationClassUID, []byte(ui.ImplementationClassUID)); err != nil {
		return nil, err
	}
	if ui.ImplementationVersionName != "" {
		if data, err = appendItem(data, ItemImplementationVersion, []byte(ui.ImplementationVersionName)); err != nil {
			return nil, err
		}
	}
	for _, role := range ui.RoleSelections {
		item, err := encodeRoleSelection(role)
		if err != nil {
			return nil, err
		}
		data = append(data, item...)
	}
	if ui.AsyncOperationsWindow != nil {
		body := make([]byte, 4)
		binary.BigEndian.PutUint16(body[0:2], ui.AsyncOperationsWindow.MaxInvoked)
		binary.BigEndian.PutUint16(body[2:4], ui.AsyncOperationsWindow.MaxPerformed)
		if data, err = appendItem(data, ItemAsyncOperationsWindow, body); err != nil {
			return nil, err
		}
	}
	for _, en := range ui.ExtendedNegotiations {
		if en.SOPClassUID == "" {
			return nil, fmt.Errorf("pdu: extended negotiation missing SOP Class UID")
		}
		sop := []byte(en.SOPClassUID)
		if len(sop) > maxItemLength {
			return nil, errTooLong("extended negotiation SOP Class UID", len(sop), maxItemLength)
		}
		if len(en.Info) > maxItemLength {
			return nil, errTooLong("extended negotiation info", len(en.Info), maxItemLength)
		}
		body := make([]byte, 2, 2+len(sop)+len(en.Info))
		binary.BigEndian.PutUint16(body[0:2], uint16(len(sop)))
		body = append(body, sop...)
		body = append(body, en.Info...)
		if data, err = appendItem(data, ItemSOPClassExtendedNegotiation, body); err != nil {
			return nil, err
		}
	}
	for _, ce := range ui.CommonExtendedNegotiations {
		item, err := encodeCommonExtendedNegotiation(ce)
		if err != nil {
			return nil, err
		}
		data = append(data, item...)
	}
	if ui.UserIdentityRQ != nil {
		item, err := encodeUserIdentityRQ(*ui.UserIdentityRQ)
		if err != nil {
			return nil, err
		}
		data = append(data, item...)
	}
	if ui.UserIdentityAC != nil {
		item, err := encodeUserIdentityAC(*ui.UserIdentityAC)
		if err != nil {
			return nil, err
		}
		data = append(data, item...)
	}
	return encodeItem(ItemUserInformation, data)
}

func decodeUserInformation(data []byte) (UserInformation, error) {
	var ui UserInformation
	subs, err := decodeItems(data)
	if err != nil {
		return ui, err
	}
	for _, s := range subs {
		switch s.Type {
		case ItemMaxLength:
			if len(s.Data) != 4 {
				return ui, fmt.Errorf("pdu: bad max length item")
			}
			ui.MaxLength = binary.BigEndian.Uint32(s.Data)
		case ItemImplementationClassUID:
			ui.ImplementationClassUID = trimUIDPadding(s.Data)
		case ItemImplementationVersion:
			ui.ImplementationVersionName = string(s.Data)
		case ItemAsyncOperationsWindow:
			if len(s.Data) != 4 {
				return ui, fmt.Errorf("pdu: bad asynchronous operations window item: %d bytes", len(s.Data))
			}
			ui.AsyncOperationsWindow = &AsyncOperationsWindow{
				MaxInvoked:   binary.BigEndian.Uint16(s.Data[0:2]),
				MaxPerformed: binary.BigEndian.Uint16(s.Data[2:4]),
			}
		case ItemSOPClassExtendedNegotiation:
			en, err := decodeExtendedNegotiation(s.Data)
			if err != nil {
				return ui, err
			}
			ui.ExtendedNegotiations = append(ui.ExtendedNegotiations, en)
		case ItemSOPClassCommonExtendedNegotiation:
			ce, err := decodeCommonExtendedNegotiation(s.Data)
			if err != nil {
				return ui, err
			}
			ui.CommonExtendedNegotiations = append(ui.CommonExtendedNegotiations, ce)
		case ItemRoleSelection:
			role, err := decodeRoleSelection(s.Data)
			if err != nil {
				return ui, err
			}
			ui.RoleSelections = append(ui.RoleSelections, role)
		case ItemUserIdentityRQ:
			id, err := decodeUserIdentityRQ(s.Data)
			if err != nil {
				return ui, err
			}
			ui.UserIdentityRQ = &id
		case ItemUserIdentityAC:
			id, err := decodeUserIdentityAC(s.Data)
			if err != nil {
				return ui, err
			}
			ui.UserIdentityAC = &id
		}
	}
	return ui, nil
}

func encodeRoleSelection(role RoleSelection) ([]byte, error) {
	if role.SOPClassUID == "" {
		return nil, fmt.Errorf("pdu: role selection missing SOP Class UID")
	}
	if !role.SCURole && !role.SCPRole {
		return nil, fmt.Errorf("pdu: SCU and SCP roles cannot both be false for %q", role.SOPClassUID)
	}
	uid := []byte(role.SOPClassUID)
	if len(uid) > maxItemLength {
		return nil, errTooLong("role selection SOP Class UID", len(uid), maxItemLength)
	}
	body := make([]byte, 2+len(uid)+2)
	binary.BigEndian.PutUint16(body[0:2], uint16(len(uid)))
	copy(body[2:], uid)
	if role.SCURole {
		body[2+len(uid)] = 1
	}
	if role.SCPRole {
		body[2+len(uid)+1] = 1
	}
	return encodeItem(ItemRoleSelection, body)
}

func decodeRoleSelection(data []byte) (RoleSelection, error) {
	if len(data) < 4 {
		return RoleSelection{}, fmt.Errorf("pdu: short role selection item")
	}
	uidLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+uidLen+2 {
		return RoleSelection{}, fmt.Errorf("pdu: truncated role selection item")
	}
	// The role bytes sit after the UID at the length the peer declared, so the
	// offsets below stay on uidLen even though the UID itself may be a byte
	// shorter once its padding is off.
	uid := trimUIDPadding(data[2 : 2+uidLen])
	return RoleSelection{
		SOPClassUID: uid,
		SCURole:     data[2+uidLen] != 0,
		SCPRole:     data[2+uidLen+1] != 0,
	}, nil
}

// decodeExtendedNegotiation parses a SOP Class Extended Negotiation sub-item:
// a two-byte UID length, the UID, then opaque Service Class Application
// Information.
func decodeExtendedNegotiation(data []byte) (ExtendedNegotiation, error) {
	if len(data) < 2 {
		return ExtendedNegotiation{}, fmt.Errorf("pdu: short extended negotiation item")
	}
	uidLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+uidLen {
		return ExtendedNegotiation{}, fmt.Errorf("pdu: truncated extended negotiation UID")
	}
	return ExtendedNegotiation{
		SOPClassUID: trimUIDPadding(data[2 : 2+uidLen]),
		// Copied: the caller keeps the PDU buffer only as long as it needs to.
		Info: append([]byte(nil), data[2+uidLen:]...),
	}, nil
}

// encodeCommonExtendedNegotiation builds a SOP Class Common Extended
// Negotiation sub-item (PS3.8 D.3.3.6): the item header, then SOP Class UID,
// Service Class UID and the related-general list, each preceded by its own
// two-byte length.
//
// The Related General SOP Class Identification field's length covers the whole
// nested list rather than counting UIDs, so it is built first and measured
// once. UIDs go in without the trailing null PS3.5 9.1 would add — D.3.3.6
// asks for the unpadded form — and a peer that pads anyway is handled by
// trimUIDPadding on the way back in.
func encodeCommonExtendedNegotiation(ce CommonExtendedNegotiation) ([]byte, error) {
	if ce.SOPClassUID == "" {
		return nil, fmt.Errorf("pdu: common extended negotiation missing SOP Class UID")
	}
	if ce.ServiceClassUID == "" {
		return nil, fmt.Errorf("pdu: common extended negotiation missing Service Class UID for %q", ce.SOPClassUID)
	}
	sop := []byte(ce.SOPClassUID)
	svc := []byte(ce.ServiceClassUID)
	for _, uid := range []struct {
		name string
		data []byte
	}{{"SOP Class UID", sop}, {"Service Class UID", svc}} {
		if len(uid.data) > maxItemLength {
			return nil, errTooLong("common extended negotiation "+uid.name, len(uid.data), maxItemLength)
		}
	}
	related := make([]byte, 0, 32)
	for _, uid := range ce.RelatedGeneralSOPClasses {
		if uid == "" {
			return nil, fmt.Errorf("pdu: empty related general SOP Class UID for %q", ce.SOPClassUID)
		}
		if len(uid) > maxItemLength {
			return nil, errTooLong("related general SOP Class UID", len(uid), maxItemLength)
		}
		related = binary.BigEndian.AppendUint16(related, uint16(len(uid)))
		related = append(related, uid...)
	}
	if len(related) > maxItemLength {
		return nil, errTooLong("related general SOP Class list", len(related), maxItemLength)
	}
	body := make([]byte, 0, 6+len(sop)+len(svc)+len(related))
	body = binary.BigEndian.AppendUint16(body, uint16(len(sop)))
	body = append(body, sop...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(svc)))
	body = append(body, svc...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(related)))
	body = append(body, related...)
	return encodeItem(ItemSOPClassCommonExtendedNegotiation, body)
}

// decodeCommonExtendedNegotiation parses a SOP Class Common Extended
// Negotiation sub-item. Every length is a claim by the peer, so each one is
// checked against what is actually there before it is used as an offset; a
// short item is an error rather than a shorter answer.
func decodeCommonExtendedNegotiation(data []byte) (CommonExtendedNegotiation, error) {
	var ce CommonExtendedNegotiation
	// Two length-prefixed UIDs and one length field, before any UID bytes.
	if len(data) < 6 {
		return ce, fmt.Errorf("pdu: short common extended negotiation item: %d bytes", len(data))
	}
	sopLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+sopLen+2 {
		return ce, fmt.Errorf("pdu: truncated common extended negotiation SOP Class UID")
	}
	ce.SOPClassUID = trimUIDPadding(data[2 : 2+sopLen])
	off := 2 + sopLen
	svcLen := int(binary.BigEndian.Uint16(data[off : off+2]))
	off += 2
	if len(data) < off+svcLen+2 {
		return ce, fmt.Errorf("pdu: truncated common extended negotiation Service Class UID")
	}
	ce.ServiceClassUID = trimUIDPadding(data[off : off+svcLen])
	off += svcLen
	relatedLen := int(binary.BigEndian.Uint16(data[off : off+2]))
	off += 2
	if len(data) < off+relatedLen {
		return ce, fmt.Errorf("pdu: truncated common extended negotiation related general SOP Class list")
	}
	related := data[off : off+relatedLen]
	for len(related) > 0 {
		if len(related) < 2 {
			return ce, fmt.Errorf("pdu: short related general SOP Class UID length")
		}
		n := int(binary.BigEndian.Uint16(related[0:2]))
		if len(related) < 2+n {
			return ce, fmt.Errorf("pdu: truncated related general SOP Class UID")
		}
		ce.RelatedGeneralSOPClasses = append(ce.RelatedGeneralSOPClasses, trimUIDPadding(related[2:2+n]))
		related = related[2+n:]
	}
	return ce, nil
}

func encodeUserIdentityRQ(id UserIdentityRQ) ([]byte, error) {
	if id.Type < UserIdentityUsername || id.Type > UserIdentityJWT {
		return nil, fmt.Errorf("pdu: invalid user identity type %d", id.Type)
	}
	if len(id.PrimaryField) == 0 {
		return nil, fmt.Errorf("pdu: user identity primary field required")
	}
	sec := id.SecondaryField
	if id.Type != UserIdentityUsernamePasscode {
		sec = nil
	}
	// Both fields carry a credential the caller supplied — a Kerberos ticket, a
	// SAML assertion, a JWT — and each has its own 16-bit length ahead of the
	// item's.
	if len(id.PrimaryField) > maxItemLength {
		return nil, errTooLong("user identity primary field", len(id.PrimaryField), maxItemLength)
	}
	if len(sec) > maxItemLength {
		return nil, errTooLong("user identity secondary field", len(sec), maxItemLength)
	}
	body := make([]byte, 0, 6+len(id.PrimaryField)+len(sec))
	body = append(body, id.Type)
	if id.PositiveResponseRequested {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	plen := make([]byte, 2)
	binary.BigEndian.PutUint16(plen, uint16(len(id.PrimaryField)))
	body = append(body, plen...)
	body = append(body, id.PrimaryField...)
	slen := make([]byte, 2)
	binary.BigEndian.PutUint16(slen, uint16(len(sec)))
	body = append(body, slen...)
	body = append(body, sec...)
	return encodeItem(ItemUserIdentityRQ, body)
}

func decodeUserIdentityRQ(data []byte) (UserIdentityRQ, error) {
	if len(data) < 6 {
		return UserIdentityRQ{}, fmt.Errorf("pdu: short user identity RQ item")
	}
	plen := int(binary.BigEndian.Uint16(data[2:4]))
	if len(data) < 4+plen+2 {
		return UserIdentityRQ{}, fmt.Errorf("pdu: truncated user identity RQ primary field")
	}
	off := 4 + plen
	slen := int(binary.BigEndian.Uint16(data[off : off+2]))
	if len(data) < off+2+slen {
		return UserIdentityRQ{}, fmt.Errorf("pdu: truncated user identity RQ secondary field")
	}
	id := UserIdentityRQ{
		Type:                      data[0],
		PositiveResponseRequested: data[1] != 0,
		PrimaryField:              append([]byte(nil), data[4:4+plen]...),
	}
	if slen > 0 {
		id.SecondaryField = append([]byte(nil), data[off+2:off+2+slen]...)
	}
	return id, nil
}

func encodeUserIdentityAC(id UserIdentityAC) ([]byte, error) {
	if len(id.ServerResponse) > maxItemLength {
		return nil, errTooLong("user identity server response", len(id.ServerResponse), maxItemLength)
	}
	body := make([]byte, 2+len(id.ServerResponse))
	binary.BigEndian.PutUint16(body[0:2], uint16(len(id.ServerResponse)))
	copy(body[2:], id.ServerResponse)
	return encodeItem(ItemUserIdentityAC, body)
}

func decodeUserIdentityAC(data []byte) (UserIdentityAC, error) {
	if len(data) < 2 {
		return UserIdentityAC{}, fmt.Errorf("pdu: short user identity AC item")
	}
	rlen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+rlen {
		return UserIdentityAC{}, fmt.Errorf("pdu: truncated user identity AC response")
	}
	return UserIdentityAC{
		ServerResponse: append([]byte(nil), data[2:2+rlen]...),
	}, nil
}
