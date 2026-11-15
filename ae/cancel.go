package ae

import (
	"context"
	"errors"
	"fmt"

	"github.com/godicom-dev/gonetdicom/dimse"
)

// Why an SCP handler was stopped, as delivered to the context it was given.
//
// pynetdicom hands a handler an is_cancelled() callable that it has to poll
// (service_class.py:283). A handler here is given a context instead, and the
// framework cancels it at the moment it observes the stop — between two items
// of the iterator the handler is producing, because that is the only point at
// which the framework is running. Read the reason with context.Cause.
var (
	// ErrPeerCancelled reports that the peer sent a C-CANCEL-RQ for this
	// operation. Whatever the handler has already produced stands; it should
	// stop producing more rather than finish a scan whose results are thrown
	// away.
	ErrPeerCancelled = errors.New("ae: operation cancelled by peer")

	// ErrPeerReleased reports that the association ended — release, abort, or
	// a failed read — while the handler was still running. Nothing it produces
	// from here on can be sent, and the peer is waiting for its release
	// response rather than for this operation.
	ErrPeerReleased = errors.New("ae: association ended during operation")
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
