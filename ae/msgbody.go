package ae

import (
	"bytes"
	"io"
	"sync"
)

// msgBody is the dataset half of one received DIMSE message, handed to the
// handler as a stream instead of as a []byte assembled in memory.
//
// A C-STORE of a 500 MB enhanced CT used to cost 500 MB of resident memory per
// association, all of it live at once. dcm4che avoids that with PDVInputStream
// and a buffer the caller manages; pynetdicom reads the whole thing into a
// BytesIO. Both pay for streaming with machinery. Here the machinery is a
// channel with room for one fragment:
//
//	reader goroutine --frags(cap 1)--> handler's io.Reader
//
// That single slot is the entire backpressure design. The reader can stay at
// most one P-DATA ahead of the handler; a handler that stops reading stops the
// reader, with no queue, no high-water mark, and no policy to tune. Peak
// memory for a message becomes one fragment, whatever the dataset size.
//
// Contract: a body is either read to EOF or closed. Close does not abandon the
// socket — the association reader must still consume fragments up to the last
// one or the next message would begin mid-dataset. Close means "stop keeping
// what is left", and the reader discards the remainder without copying.
type msgBody struct {
	frags    chan []byte
	done     chan struct{}
	finished chan struct{}

	// pull, when set, makes Read drive the socket directly instead of
	// receiving pushed fragments. The SCP side needs push — one goroutine
	// reads for the whole association so it can watch for C-CANCEL. The SCU
	// side is already synchronous, so a callback is enough and no second
	// goroutine is needed.
	pull     func() (frag []byte, last bool, err error)
	pullDone bool

	cur    []byte
	mu     sync.Mutex
	finErr error

	closeOnce sync.Once
	finOnce   sync.Once
}

func newMsgBody() *msgBody {
	return &msgBody{
		frags:    make(chan []byte, 1),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// newPullBody builds a body that reads the socket on demand instead of
// receiving pushed fragments. The SCU side uses it: it is synchronous anyway,
// so a callback is enough and a reader goroutine would only add a way to
// desynchronise the connection.
func newPullBody(pull func() ([]byte, bool, error)) *msgBody {
	b := newMsgBody()
	b.pull = pull
	return b
}

// Read implements io.Reader over the fragments the association reader pushes.
func (b *msgBody) Read(p []byte) (int, error) {
	for len(b.cur) == 0 {
		if b.pull != nil {
			if b.pullDone {
				return 0, io.EOF
			}
			frag, last, err := b.pull()
			if err != nil {
				return 0, err
			}
			b.cur = frag
			b.pullDone = last
			continue
		}
		frag, ok := <-b.frags
		if !ok {
			// finErr is written before close(frags), so a receiver that saw
			// the close also sees the error.
			b.mu.Lock()
			err := b.finErr
			b.mu.Unlock()
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		b.cur = frag
	}
	n := copy(p, b.cur)
	b.cur = b.cur[n:]
	return n, nil
}

// push hands one fragment to the reader side. It reports false once the body
// has been closed, which tells the association reader to discard the rest of
// the message: it still reads to the last fragment, it just stops copying.
func (b *msgBody) push(frag []byte) bool {
	select {
	case b.frags <- frag:
		return true
	case <-b.done:
		return false
	}
}

// finish delivers the terminal state: a read error, or a clean end of message.
// The association reader calls it exactly once per body, after the last
// fragment, and it is what unblocks a handler waiting for the next one.
func (b *msgBody) finish(err error) {
	b.finOnce.Do(func() {
		b.mu.Lock()
		b.finErr = err
		b.mu.Unlock()
		close(b.frags)
		close(b.finished)
	})
}

// readErr reports why the dataset stopped early, if it did: an oversized
// message, a network error, the association going away. A handler that ignored
// the stream still has to know, because answering "success" to a message the
// library had to truncate would be a lie.
func (b *msgBody) readErr() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.finErr
}

// Discard stops the handler's interest without waiting for the reader to
// finish. It exists for the panic path: the association is about to be torn
// down, so waiting for a peer that may have stopped sending would hang instead
// of crashing, and a panic must not turn into a hang.
func (b *msgBody) Discard() {
	b.closeOnce.Do(func() { close(b.done) })
}

// Close releases the handler's interest in whatever is left. Safe to call more
// than once, and safe to call without ever reading.
//
// Closing is what makes the next message parseable, so it is also where the
// wait happens: on the push side it returns once the association reader has
// reached the last fragment, discarding it if nobody read it; on the pull side
// it finishes the read itself. Either way the socket is left between messages,
// and any error the reader hit while the handler was not looking has already
// been recorded by then — see readErr.
func (b *msgBody) Close() error {
	b.closeOnce.Do(func() {
		close(b.done)
		if b.pull != nil {
			for !b.pullDone {
				_, last, err := b.pull()
				if err != nil {
					break
				}
				b.pullDone = last
			}
			return
		}
		<-b.finished
	})
	return nil
}

// Bytes drains the body. Convenient for identifiers, which are small, and for
// callers that genuinely want the dataset in memory.
func (b *msgBody) Bytes() ([]byte, error) {
	return io.ReadAll(b)
}

// drainBody reads a message body whole and releases it. Used where the dataset
// is an identifier or event information: small, and wanted contiguous.
func drainBody(b *msgBody) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	defer func() { _ = b.Close() }()
	return b.Bytes()
}

// bodyFromBytes wraps bytes already in memory as a finished body.
func bodyFromBytes(b []byte) *msgBody {
	body := newMsgBody()
	if len(b) > 0 {
		body.push(b)
	}
	body.finish(nil)
	return body
}

// bodyReader is what StoreEvent.Stream returns; nil bodies read as empty rather
// than panicking, so a malformed store surfaces as an empty dataset at Decode.
func bodyReader(b *msgBody) io.Reader {
	if b == nil {
		return bytes.NewReader(nil)
	}
	return b
}
