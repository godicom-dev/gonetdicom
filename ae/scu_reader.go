package ae

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/godicom-dev/gonetdicom/pdu"
)

// pduReader owns the SCU's read side: one goroutine reads whole PDUs off the
// connection and hands them over a channel.
//
// Reading in the caller's goroutine — which is what this replaces — made the
// caller's context deadline a *socket* deadline, so an expiry in the middle of
// a PDU left the connection at an arbitrary byte offset: the rest of that PDU
// is still in flight and the next read takes a length prefix out of payload.
// From there nothing can be exchanged except A-ABORT, and `Release` fails too,
// because its first read is the garbage. The PDU being read belongs to whoever
// is reading it, so the goroutine finishes it even when the caller has gone;
// the caller is left free to stop waiting at a message boundary, which is the
// only cancellation the protocol actually supports.
type pduReader struct {
	ch   chan pdu.PDU
	stop chan struct{}
	once sync.Once

	// err is the error that ended the reader. Every return in run sets it
	// before deferring the close of ch, so a caller that saw ch close is
	// guaranteed to see it — no "unknown reason" fallback is needed, and none
	// is written here to hide a future path that forgets.
	err error
}

func newPDUReader(ctx context.Context, conn net.Conn) *pduReader {
	r := &pduReader{ch: make(chan pdu.PDU, 1), stop: make(chan struct{})}
	// The context is taken without its cancellation and deadline: an
	// association outlives the call that established it, and a Dial context is
	// usually a short one that the caller defers cancelling. Only its values —
	// the logger — are wanted here.
	go r.run(context.WithoutCancel(ctx), conn)
	return r
}

func (r *pduReader) run(ctx context.Context, conn net.Conn) {
	defer close(r.ch)
	for {
		p, err := pdu.Read(conn)
		if err != nil {
			r.err = readError(err)
			return
		}
		logPDU(ctx, "recv", p)
		select {
		case r.ch <- p:
		case <-r.stop:
			r.err = errReaderStopped
			return
		}
	}
}

// readError is the error a caller sees for a connection that stopped producing
// PDUs, with the framing failures kept distinguishable from a clean close.
func readError(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("ae: connection closed: %w", err)
	}
	return fmt.Errorf("ae: read PDU: %w", err)
}

// Close stops the reader. A goroutine blocked on the connection is released by
// closing that connection afterwards.
func (r *pduReader) Close() {
	r.once.Do(func() { close(r.stop) })
}
