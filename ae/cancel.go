package ae

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// CCancel sends a C-CANCEL-RQ for an outstanding C-FIND / C-MOVE / C-GET.
// Mirrors pynetdicom Association.send_c_cancel.
//
// Provide contextID and/or queryModel (abstract syntax). queryModel resolves
// the presentation context when contextID is 0.
//
// Safe to call concurrently with a blocked CFind/CMove/CGet on the same
// association (net.Conn supports concurrent Read+Write).
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

// peekCancelRQ tries to read a pending C-CANCEL-RQ for msgID without blocking long.
// Returns true if a matching cancel was consumed from the connection.
func peekCancelRQ(conn net.Conn, msgID uint16) bool {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Millisecond))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	raw, err := pdu.Read(conn)
	if err != nil {
		return false
	}
	p, ok := raw.(*pdu.PDataTF)
	if !ok {
		return false
	}
	var cmdBuf []byte
	cmdDone := false
	for _, pdv := range p.PDVs {
		if !pdv.IsCommand() {
			continue
		}
		cmdBuf = append(cmdBuf, pdv.Fragment()...)
		if pdv.IsLast() {
			cmdDone = true
		}
	}
	if !cmdDone {
		return false
	}
	cancel, err := dimse.DecodeCCancelRQ(cmdBuf)
	if err != nil {
		return false
	}
	return cancel.MessageIDBeingRespondedTo == msgID
}
