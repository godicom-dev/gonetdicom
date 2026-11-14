package ae

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// errMalformedCommand marks a command set that arrived intact but cannot be
// parsed. The association cannot continue, and the peer is owed an A-ABORT: a
// closed socket is indistinguishable from a network failure.
var errMalformedCommand = errors.New("ae: malformed DIMSE command set")

// inbound is one item from an association's read side: either a complete DIMSE
// message, or a control PDU that ends the association, or a read error.
//
// A message's command set is complete when it arrives — it is small, and
// dispatch needs it. Its dataset is not: Body is a stream the reader keeps
// feeding while the handler consumes, so a 500 MB store costs one fragment of
// memory instead of 500 MB.
type inbound struct {
	ContextID byte
	Command   []byte
	Body      *msgBody

	// Control is non-nil when the item is a PDU rather than a DIMSE message
	// (A-RELEASE-RQ, A-ABORT, or anything unexpected). Such a PDU always ends
	// the association, so it is the last item the reader produces.
	Control pdu.PDU

	Err error
}

// assocReader owns the read side of an association's connection: one goroutine
// reads whole PDUs, reassembles DIMSE messages, and hands them over a channel.
//
// A shared reader is what makes cancel detection safe. C-FIND/C-MOVE/C-GET must
// look for an out-of-band C-CANCEL-RQ between responses, and the only way to do
// that without a second reader on the same socket is to ask the one reader
// whether something has already arrived — see pollStop. Reading directly from
// the connection instead (under a short deadline, say) consumes and discards
// however many bytes happened to be in flight, desynchronising the stream: the
// next read lands mid-PDU and takes a length prefix out of message payload.
type assocReader struct {
	ch         <-chan inbound
	stop       chan struct{}
	pending    []inbound
	maxMessage int // bytes per DIMSE message; 0 = unlimited
}

// newAssocReader starts reading conn. Call Close before closing conn.
//
// maxMessage caps the bytes of one reassembled DIMSE message (command set plus
// dataset). The reader is the only place that sees a message grow, so it is the
// only place the cap can be enforced: fragments arrive one MaxPDU at a time, and
// without a ceiling a peer can make the SCP concatenate until it runs out of
// memory, one legitimate-looking P-DATA at a time.
func newAssocReader(ctx context.Context, conn net.Conn, maxMessage int) *assocReader {
	ch := make(chan inbound, 1)
	r := &assocReader{ch: ch, stop: make(chan struct{}), maxMessage: maxMessage}
	go r.run(ctx, conn, ch)
	return r
}

func (r *assocReader) run(ctx context.Context, conn net.Conn, ch chan<- inbound) {
	defer close(ch)

	send := func(it inbound) bool {
		select {
		case ch <- it:
			return true
		case <-r.stop:
			return false
		}
	}

	var (
		cmdBuf   []byte
		body     *msgBody
		discard  bool
		inBody   bool
		pcid     byte
		msgBytes int
	)

	// abort tells whoever is reading the dataset in flight why the message is
	// not going to finish. Without it a handler blocked in Read waits for an
	// end that never comes.
	abort := func(err error) {
		if body != nil {
			body.finish(err)
			body = nil
		}
	}

	for {
		raw, err := readPDUConn(ctx, conn)
		if err != nil {
			abort(err)
			send(inbound{Err: err})
			return
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			// Release, abort, or a PDU with no meaning mid-association: the
			// caller ends the association, so stop reading.
			abort(errReaderStopped)
			send(inbound{Control: raw})
			return
		}
		for _, pdv := range p.PDVs {
			if !inBody && len(cmdBuf) == 0 {
				pcid = pdv.ContextID
			}
			frag := pdv.Fragment()
			msgBytes += len(frag)
			if r.maxMessage > 0 && msgBytes > r.maxMessage {
				err := fmt.Errorf("ae: message exceeds MaxDatasetSize (%d bytes)", r.maxMessage)
				gonetdicom.LoggerFromContext(ctx).Warn(
					"scp: peer exceeded MaxDatasetSize, ending association",
					gonetdicom.AttrPCID, pcid,
					"limit", r.maxMessage,
				)
				abort(err)
				send(inbound{Err: err})
				return
			}

			if !inBody {
				cmdBuf = append(cmdBuf, frag...)
				if !pdv.IsLast() {
					continue
				}
				hasDS, err := dimse.CommandHasDataset(cmdBuf)
				if err != nil {
					send(inbound{Err: fmt.Errorf("%w: %w", errMalformedCommand, err)})
					return
				}
				if !hasDS {
					logDIMSE(ctx, "recv", pcid, cmdBuf)
					if !send(inbound{ContextID: pcid, Command: cmdBuf}) {
						return
					}
					cmdBuf, pcid, msgBytes = nil, 0, 0
					continue
				}
				// Hand over the command set now and stream the dataset
				// behind it. Dispatch only needs the command set, so the
				// handler is already running while the first data fragment
				// is still on the wire.
				body = newMsgBody()
				logDIMSE(ctx, "recv", pcid, cmdBuf)
				if !send(inbound{ContextID: pcid, Command: cmdBuf, Body: body}) {
					body.finish(errReaderStopped)
					return
				}
				cmdBuf = nil
				inBody = true
				continue
			}

			// Handing a fragment to a closed body means the handler is done
			// with the dataset. Keep reading to the last fragment — skipping
			// it would start the next message mid-dataset — but stop copying.
			if !discard && !body.push(frag) {
				discard = true
			}
			if pdv.IsLast() {
				body.finish(nil)
				body, discard, inBody, pcid, msgBytes = nil, false, false, 0, 0
			}
		}
	}
}

// Close stops the reader. The reading goroutine may still be blocked on conn;
// closing conn afterwards releases it.
func (r *assocReader) Close() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
}

// next returns the next inbound item, blocking until one is available.
func (r *assocReader) next(ctx context.Context) inbound {
	if len(r.pending) > 0 {
		it := r.pending[0]
		r.pending = r.pending[1:]
		return it
	}
	select {
	case it, ok := <-r.ch:
		if !ok {
			return inbound{Err: fmt.Errorf("ae: association read side closed")}
		}
		return it
	case <-ctx.Done():
		return inbound{Err: ctx.Err()}
	}
}

// message returns the next inbound DIMSE message, and fails if the peer sent a
// control PDU instead. The returned body is the dataset stream; it is nil when
// the message carries no dataset, and the caller must close it.
func (r *assocReader) message(ctx context.Context) (pcid byte, command []byte, body *msgBody, err error) {
	it := r.next(ctx)
	switch {
	case it.Err != nil:
		return 0, nil, nil, it.Err
	case it.Control != nil:
		r.pending = append([]inbound{it}, r.pending...) // keep it for the association loop
		return 0, nil, nil, fmt.Errorf("%w: unexpected PDU %T while reading message", errAssociationEnding, it.Control)
	}
	return it.ContextID, it.Command, it.Body, nil
}

// errReaderStopped ends a dataset stream when the association is going away, so
// a handler blocked in Read returns instead of waiting for a message that will
// never be completed.
var errReaderStopped = errors.New("ae: association reader stopped")

// errAssociationEnding marks a read that stopped because the peer released or
// aborted, or the connection failed. A handler that is part-way through a
// multi-message operation returns nil on it instead of the error: the
// association loop owns the teardown, and it is the one that answers an
// A-RELEASE-RQ the peer is waiting on.
var errAssociationEnding = errors.New("ae: association ending")

// stopReason is why a response stream should stop sending, as reported by
// assocReader.pollStop.
type stopReason int

const (
	// stopNone: keep going.
	stopNone stopReason = iota
	// stopCancelled: a C-CANCEL-RQ for this message arrived. The operation ends
	// with a Cancel status and the association stays usable.
	stopCancelled
	// stopAssociation: the association is gone — release, abort, or a failed
	// read. Nothing may be written for the operation: PS3.8 7.2.2 lets the
	// requestor receive P-DATA while it waits for its release response and
	// requires it to ignore it, so a Cancel status now is a message the peer
	// would throw away. Returning lets the loop answer the release.
	stopAssociation
)

// pollStop reports whether the peer has asked us to stop streaming responses
// for msgID, and why. It never blocks and never leaves a partially read PDU
// behind.
//
// Anything that is not a stop stays queued in order for the next next/message
// call, so polling cannot drop a message.
func (r *assocReader) pollStop(msgID uint16) stopReason {
	// Take whatever has already arrived, bounded so a flooding peer cannot make
	// the queue grow without us ever writing a response.
	for i := 0; i < pollDrainLimit; i++ {
		select {
		case got, ok := <-r.ch:
			if !ok {
				return stopAssociation
			}
			r.pending = append(r.pending, got)
		default:
			i = pollDrainLimit // nothing more buffered
		}
	}

	for i, it := range r.pending {
		if it.Err != nil || it.Control != nil {
			return stopAssociation // left queued; the association loop reports it
		}
		if cancel, err := dimse.DecodeCCancelRQ(it.Command); err == nil &&
			cancel.MessageIDBeingRespondedTo == msgID {
			r.pending = append(r.pending[:i:i], r.pending[i+1:]...)
			return stopCancelled
		}
	}
	return stopNone
}

// pollDrainLimit bounds how many already-arrived items one pollStop absorbs.
const pollDrainLimit = 8
