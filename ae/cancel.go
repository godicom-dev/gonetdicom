package ae

import (
	"context"
	"fmt"

	"github.com/godicom-dev/gonetdicom/dimse"
)

// CCancel sends a C-CANCEL-RQ for an outstanding C-FIND / C-MOVE / C-GET.
// Mirrors pynetdicom Association.send_c_cancel.
//
// Provide contextID and/or queryModel (abstract syntax). queryModel resolves
// the presentation context when contextID is 0.
//
// Safe to call concurrently with a blocked CFind/CMove/CGet on the same
// association (net.Conn supports concurrent Read+Write). The peer SCP notices
// the cancel between two of its responses, so expect to receive some further
// pending responses before the final Cancel status.
func (a *Association) CCancel(ctx context.Context, messageID uint16, contextID byte, queryModel string) error {
	pcid := contextID
	if pcid == 0 {
		if queryModel == "" {
			return fmt.Errorf("ae: C-CANCEL requires ContextID or QueryModel")
		}
		pc, ok := a.contextByAbstract(queryModel)
		if !ok {
			return fmt.Errorf("%w: %s", ErrNoContext, queryModel)
		}
		pcid = pc.ID
	}
	cmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: messageID}).Encode()
	if err != nil {
		return err
	}
	return a.sendMessage(ctx, pcid, cmd, nil)
}
