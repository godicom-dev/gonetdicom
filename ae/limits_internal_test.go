package ae

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// closeRecorder is a net.Conn whose only observable is whether Close ran.
// closeOnDone never reads or writes, so the embedded nil Conn is not touched.
type closeRecorder struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeRecorder) Close() error {
	c.closed.Store(true)
	return nil
}

// Stopping the watcher has to win outright. The previous implementation closed
// a channel and let a goroutine select against ctx.Done; stop could return
// before that goroutine was scheduled, and a cancel in the gap found both
// ready and closed the connection at random. That is the shutdown a peer reads
// as EOF instead of A-ABORT.
func TestCloseOnDoneStopPreventsLaterCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &closeRecorder{}
		ctx, cancel := context.WithCancel(context.Background())

		stop := closeOnDone(ctx, conn)
		stop()
		stop() // the defer calls it again after the explicit stop
		cancel()
		synctest.Wait()

		if conn.closed.Load() {
			t.Fatal("stopped watcher closed the connection when the context was cancelled")
		}
	})
}

// Before an association exists there is nothing to abort, so cancellation
// still has to close the socket. Otherwise a peer that never sent an
// A-ASSOCIATE-RQ holds the shutdown until HandshakeTimeout.
func TestCloseOnDoneClosesWhenNotStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &closeRecorder{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		closeOnDone(ctx, conn)
		cancel()
		synctest.Wait()

		if !conn.closed.Load() {
			t.Fatal("watcher left the connection open after cancellation")
		}
	})
}
