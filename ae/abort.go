package ae

import (
	"context"
	"errors"
	"net"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// abortReasonUnrecognizedPDU is the reason-diagnostic value PS3.8 Table 9-2
// gives for a PDU whose type or encoding the receiver does not recognise.
//
// pynetdicom sends 0x00 ("reason not specified") on the same occasion, from the
// catch-all in its DUL reactor; its tests assert only that the association ends
// up aborted, not what the reason byte says. The specific value costs nothing and
// saves the operator of the peer a guess, so it is used here.
const abortReasonUnrecognizedPDU byte = 0x01

// abortSourceProvider is the abort source for a fault the presentation layer
// found, which is what an undecodable PDU is: the bytes arrived, the transport
// is healthy, and the structure inside them is not something this stack serves.
const abortSourceProvider byte = 0x02

// abortUndecodable answers a PDU that arrived but does not decode with A-ABORT,
// then returns err unchanged so the caller still reports why.
//
// It does nothing for any other error: a read that ends early is a connection
// that ended, a refused length is a limit this package set, and an expired
// deadline is the caller's own doing. Only ErrUndecodable is a statement the
// peer needs to hear.
func abortUndecodable(ctx context.Context, conn net.Conn, err error) error {
	if !errors.Is(err, pdu.ErrUndecodable) {
		return err
	}
	// The association is over either way, so a failed write changes nothing —
	// the peer that sent the bad bytes may already be gone.
	_ = writePDUConn(context.WithoutCancel(ctx), conn, &pdu.AAbort{
		Source:           abortSourceProvider,
		ReasonDiagnostic: abortReasonUnrecognizedPDU,
	})
	return err
}
