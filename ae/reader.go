package ae

import (
	"context"
	"fmt"
	"net"

	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// inbound is one item from an association's read side: either a complete DIMSE
// message, or a control PDU that ends the association, or a read error.
type inbound struct {
	ContextID byte
	Command   []byte
	Dataset   []byte

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
	ch      <-chan inbound
	stop    chan struct{}
	pending []inbound
}

// newAssocReader starts reading conn. Call Close before closing conn.
func newAssocReader(ctx context.Context, conn net.Conn) *assocReader {
	ch := make(chan inbound, 1)
	r := &assocReader{ch: ch, stop: make(chan struct{})}
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
		cmdBuf, dsBuf   []byte
		cmdDone, dsDone bool
		pcid            byte
	)
	for {
		raw, err := readPDUConn(ctx, conn)
		if err != nil {
			send(inbound{Err: err})
			return
		}
		p, ok := raw.(*pdu.PDataTF)
		if !ok {
			// Release, abort, or a PDU with no meaning mid-association: the
			// caller ends the association, so stop reading.
			send(inbound{Control: raw})
			return
		}
		for _, pdv := range p.PDVs {
			if pcid == 0 {
				pcid = pdv.ContextID
			}
			if pdv.IsCommand() {
				cmdBuf = append(cmdBuf, pdv.Fragment()...)
				if pdv.IsLast() {
					cmdDone = true
					hasDS, err := dimse.CommandHasDataset(cmdBuf)
					if err != nil {
						send(inbound{Err: err})
						return
					}
					if !hasDS {
						dsDone = true
					}
				}
			} else {
				dsBuf = append(dsBuf, pdv.Fragment()...)
				if pdv.IsLast() {
					dsDone = true
				}
			}
		}
		if !cmdDone || !dsDone {
			continue
		}
		logDIMSE(ctx, "recv", pcid, cmdBuf)
		if !send(inbound{ContextID: pcid, Command: cmdBuf, Dataset: dsBuf}) {
			return
		}
		cmdBuf, dsBuf = nil, nil
		cmdDone, dsDone = false, false
		pcid = 0
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
// control PDU instead.
func (r *assocReader) message(ctx context.Context) (pcid byte, command, dataset []byte, err error) {
	it := r.next(ctx)
	switch {
	case it.Err != nil:
		return 0, nil, nil, it.Err
	case it.Control != nil:
		r.pending = append([]inbound{it}, r.pending...) // keep it for the association loop
		return 0, nil, nil, fmt.Errorf("ae: unexpected PDU %T while reading message", it.Control)
	}
	return it.ContextID, it.Command, it.Dataset, nil
}

// pollStop reports whether the peer has asked us to stop streaming responses
// for msgID. It never blocks and never leaves a partially read PDU behind.
//
// True means either a C-CANCEL-RQ for msgID arrived, or the association is
// ending (control PDU or read error). Anything else already read stays queued
// in order for the next next/message call, so polling cannot drop a message.
func (r *assocReader) pollStop(msgID uint16) bool {
	// Take whatever has already arrived, bounded so a flooding peer cannot make
	// the queue grow without us ever writing a response.
	for i := 0; i < pollDrainLimit; i++ {
		select {
		case got, ok := <-r.ch:
			if !ok {
				return true
			}
			r.pending = append(r.pending, got)
		default:
			i = pollDrainLimit // nothing more buffered
		}
	}

	for i, it := range r.pending {
		if it.Err != nil || it.Control != nil {
			return true // left queued; the association loop reports it
		}
		if cancel, err := dimse.DecodeCCancelRQ(it.Command); err == nil &&
			cancel.MessageIDBeingRespondedTo == msgID {
			r.pending = append(r.pending[:i:i], r.pending[i+1:]...)
			return true
		}
	}
	return false
}

// pollDrainLimit bounds how many already-arrived items one pollStop absorbs.
const pollDrainLimit = 8
