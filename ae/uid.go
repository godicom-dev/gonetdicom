package ae

import "github.com/godicom-dev/godicom/uid"

// NewInstanceUID returns a new DICOM UID suitable for SOP Instance UID,
// Transaction UID, and similar fields (godicom/uid.GenerateUID).
func NewInstanceUID() string {
	return string(uid.MustGenerateUID())
}
