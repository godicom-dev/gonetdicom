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

// UserInformation carries association negotiation user items.
type UserInformation struct {
	MaxLength                 uint32
	ImplementationClassUID    string
	ImplementationVersionName string
	RoleSelections            []RoleSelection
	UserIdentityRQ            *UserIdentityRQ // A-ASSOCIATE-RQ only
	UserIdentityAC            *UserIdentityAC // A-ASSOCIATE-AC only
}

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

	var varItems []byte
	varItems = append(varItems, encodeItem(ItemApplicationContext, []byte(appCtx))...)

	for _, pc := range p.PresentationContexts {
		data := make([]byte, 4)
		data[0] = pc.ID
		data = append(data, encodeItem(ItemAbstractSyntax, []byte(pc.AbstractSyntax))...)
		for _, ts := range pc.TransferSyntaxes {
			data = append(data, encodeItem(ItemTransferSyntax, []byte(ts))...)
		}
		varItems = append(varItems, encodeItem(ItemPresentationContextRQ, data)...)
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
	return encodeHeader(TypeAAssociateRQ, body), nil
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
	if int(6+length) != len(raw) {
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
			p.ApplicationContextName = string(it.Data)
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

	var varItems []byte
	varItems = append(varItems, encodeItem(ItemApplicationContext, []byte(appCtx))...)
	for _, pc := range p.PresentationContexts {
		data := make([]byte, 4)
		data[0] = pc.ID
		data[2] = pc.Result
		if pc.TransferSyntax != "" {
			data = append(data, encodeItem(ItemTransferSyntax, []byte(pc.TransferSyntax))...)
		}
		varItems = append(varItems, encodeItem(ItemPresentationContextAC, data)...)
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
	return encodeHeader(TypeAAssociateAC, body), nil
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
	if int(6+length) != len(raw) {
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
			p.ApplicationContextName = string(it.Data)
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
	return encodeHeader(TypeAAssociateRJ, body), nil
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
			pc.AbstractSyntax = string(s.Data)
		case ItemTransferSyntax:
			pc.TransferSyntaxes = append(pc.TransferSyntaxes, string(s.Data))
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
			pc.TransferSyntax = string(s.Data)
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
	var data []byte
	data = append(data, encodeItem(ItemMaxLength, maxLen)...)
	data = append(data, encodeItem(ItemImplementationClassUID, []byte(ui.ImplementationClassUID))...)
	if ui.ImplementationVersionName != "" {
		data = append(data, encodeItem(ItemImplementationVersion, []byte(ui.ImplementationVersionName))...)
	}
	for _, role := range ui.RoleSelections {
		item, err := encodeRoleSelection(role)
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
	return encodeItem(ItemUserInformation, data), nil
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
			ui.ImplementationClassUID = string(s.Data)
		case ItemImplementationVersion:
			ui.ImplementationVersionName = string(s.Data)
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
	body := make([]byte, 2+len(uid)+2)
	binary.BigEndian.PutUint16(body[0:2], uint16(len(uid)))
	copy(body[2:], uid)
	if role.SCURole {
		body[2+len(uid)] = 1
	}
	if role.SCPRole {
		body[2+len(uid)+1] = 1
	}
	return encodeItem(ItemRoleSelection, body), nil
}

func decodeRoleSelection(data []byte) (RoleSelection, error) {
	if len(data) < 4 {
		return RoleSelection{}, fmt.Errorf("pdu: short role selection item")
	}
	uidLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+uidLen+2 {
		return RoleSelection{}, fmt.Errorf("pdu: truncated role selection item")
	}
	uid := string(data[2 : 2+uidLen])
	return RoleSelection{
		SOPClassUID: uid,
		SCURole:     data[2+uidLen] != 0,
		SCPRole:     data[2+uidLen+1] != 0,
	}, nil
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
	return encodeItem(ItemUserIdentityRQ, body), nil
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
	body := make([]byte, 2+len(id.ServerResponse))
	binary.BigEndian.PutUint16(body[0:2], uint16(len(id.ServerResponse)))
	copy(body[2:], id.ServerResponse)
	return encodeItem(ItemUserIdentityAC, body), nil
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
