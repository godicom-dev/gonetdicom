package ae

import "github.com/godicom-dev/godicom/uid"

// NewInstanceUID returns a new DICOM UID suitable for SOP Instance UID,
// Transaction UID, and similar fields (godicom/uid.GenerateUID).
func NewInstanceUID() string {
	return string(uid.MustGenerateUID())
}

// UIDStrings converts godicom UIDs to the plain strings this package's
// abstract-syntax and transfer-syntax fields hold, so callers can name a SOP
// class instead of pasting its UID:
//
//	PresentationContexts: []ae.PresentationContext{{
//		AbstractSyntax:   string(uid.CTImageStorage),
//		TransferSyntaxes: ae.UIDStrings(uid.ExplicitVRLittleEndian, uid.ImplicitVRLittleEndian),
//	}},
func UIDStrings(uids ...uid.UID) []string {
	out := make([]string, len(uids))
	for i, u := range uids {
		out[i] = string(u)
	}
	return out
}
