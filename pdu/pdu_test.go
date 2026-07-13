package pdu

import (
	"bytes"
	"testing"
)

func TestDecodeEncodeGoldenAAssociateRQ(t *testing.T) {
	rq, err := DecodeAAssociateRQ(goldenAAssociateRQ)
	if err != nil {
		t.Fatal(err)
	}
	if rq.CalledAETitle != "ANY-SCP" || rq.CallingAETitle != "ECHOSCU" {
		t.Fatalf("AE titles: called=%q calling=%q", rq.CalledAETitle, rq.CallingAETitle)
	}
	if rq.ApplicationContextName != ApplicationContextName {
		t.Fatalf("app context: %q", rq.ApplicationContextName)
	}
	if len(rq.PresentationContexts) != 1 {
		t.Fatalf("contexts: %d", len(rq.PresentationContexts))
	}
	pc := rq.PresentationContexts[0]
	if pc.ID != 1 || pc.AbstractSyntax != VerificationSOPClass {
		t.Fatalf("pc: %+v", pc)
	}
	if len(pc.TransferSyntaxes) != 1 || pc.TransferSyntaxes[0] != ImplicitVRLittleEndian {
		t.Fatalf("ts: %v", pc.TransferSyntaxes)
	}
	if rq.UserInformation.MaxLength != 16382 {
		t.Fatalf("max length: %d", rq.UserInformation.MaxLength)
	}
	if rq.UserInformation.ImplementationClassUID != "1.2.826.0.1.3680043.9.3811.0.9.0" {
		t.Fatalf("impl uid: %q", rq.UserInformation.ImplementationClassUID)
	}
	if rq.UserInformation.ImplementationVersionName != "PYNETDICOM_090" {
		t.Fatalf("impl ver: %q", rq.UserInformation.ImplementationVersionName)
	}

	got, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenAAssociateRQ) {
		t.Fatalf("roundtrip mismatch\ngot  %x\nwant %x", got, goldenAAssociateRQ)
	}
}

func TestDecodeEncodeGoldenAAssociateAC(t *testing.T) {
	ac, err := DecodeAAssociateAC(goldenAAssociateAC)
	if err != nil {
		t.Fatal(err)
	}
	if ac.CalledAETitle != "ANY-SCP" || ac.CallingAETitle != "ECHOSCU" {
		t.Fatalf("AE titles: %+v", ac)
	}
	if len(ac.PresentationContexts) != 1 || ac.PresentationContexts[0].Result != 0 {
		t.Fatalf("contexts: %+v", ac.PresentationContexts)
	}
	if ac.PresentationContexts[0].TransferSyntax != ImplicitVRLittleEndian {
		t.Fatalf("ts: %q", ac.PresentationContexts[0].TransferSyntax)
	}
	got, err := ac.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenAAssociateAC) {
		t.Fatalf("roundtrip mismatch\ngot  %x\nwant %x", got, goldenAAssociateAC)
	}
}

func TestDecodeEncodeReleaseAbort(t *testing.T) {
	rq, err := DecodeAReleaseRQ(goldenAReleaseRQ)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rq.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenAReleaseRQ) {
		t.Fatalf("release rq: %x", got)
	}

	rp, err := DecodeAReleaseRP(goldenAReleaseRP)
	if err != nil {
		t.Fatal(err)
	}
	got, err = rp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenAReleaseRP) {
		t.Fatalf("release rp: %x", got)
	}

	ab, err := DecodeAAbort(goldenAAbort)
	if err != nil {
		t.Fatal(err)
	}
	got, err = ab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenAAbort) {
		t.Fatalf("abort: got %x want %x (source=%d reason=%d)", got, goldenAAbort, ab.Source, ab.ReasonDiagnostic)
	}
}

func TestDecodeEncodeGoldenPDataTF(t *testing.T) {
	p, err := DecodePDataTF(goldenPDataTFRQ)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.PDVs) != 1 {
		t.Fatalf("pdvs: %d", len(p.PDVs))
	}
	if p.PDVs[0].ContextID != 1 || !p.PDVs[0].IsCommand() || !p.PDVs[0].IsLast() {
		t.Fatalf("pdv: %+v mch=%x", p.PDVs[0], p.PDVs[0].Value[0])
	}
	got, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenPDataTFRQ) {
		t.Fatalf("p-data rq mismatch")
	}

	p2, err := DecodePDataTF(goldenPDataTF)
	if err != nil {
		t.Fatal(err)
	}
	got, err = p2.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, goldenPDataTF) {
		t.Fatalf("p-data rsp mismatch")
	}
}

func TestReadWriteRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &AReleaseRQ{}); err != nil {
		t.Fatal(err)
	}
	p, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*AReleaseRQ); !ok {
		t.Fatalf("got %T", p)
	}
}

func TestDecodeEncodeGoldenRoleSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		uid  string
	}{
		{name: "odd CT Image Storage", raw: goldenRoleSelectionOdd, uid: "1.2.840.10008.5.1.4.1.1.2"},
		{name: "even NM Image Storage", raw: goldenRoleSelection, uid: "1.2.840.10008.5.1.4.1.1.21"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := decodeItems(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Type != ItemRoleSelection {
				t.Fatalf("items: %+v", items)
			}
			role, err := decodeRoleSelection(items[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			if role.SOPClassUID != tt.uid || role.SCURole || !role.SCPRole {
				t.Fatalf("role: %+v", role)
			}
			got, err := encodeRoleSelection(role)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.raw) {
				t.Fatalf("roundtrip\ngot  %x\nwant %x", got, tt.raw)
			}
		})
	}
}

func TestUserInformationRoleSelectionRoundtrip(t *testing.T) {
	t.Parallel()

	ui := UserInformation{
		MaxLength:                 16384,
		ImplementationClassUID:    "1.2.826.0.1.3680043.10.541.1",
		ImplementationVersionName: "GONETDICOM_001",
		RoleSelections: []RoleSelection{{
			SOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
			SCURole:     false,
			SCPRole:     true,
		}},
	}
	raw, err := encodeUserInformation(ui)
	if err != nil {
		t.Fatal(err)
	}
	items, err := decodeItems(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Type != ItemUserInformation {
		t.Fatalf("items: %+v", items)
	}
	got, err := decodeUserInformation(items[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RoleSelections) != 1 || !got.RoleSelections[0].SCPRole || got.RoleSelections[0].SCURole {
		t.Fatalf("roles: %+v", got.RoleSelections)
	}
}
