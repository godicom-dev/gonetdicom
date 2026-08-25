package ae

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// writeMsg is called from a goroutine, so it reports rather than fatals.
func writeMsg(t *testing.T, conn net.Conn, command []byte) {
	t.Helper()
	pdus, err := pdu.FragmentMessage(1, command, nil, pdu.DefaultMaxPDULength)
	if err != nil {
		t.Errorf("fragment: %v", err)
		return
	}
	for _, p := range pdus {
		if err := pdu.Write(conn, p); err != nil {
			t.Errorf("write: %v", err)
			return
		}
	}
}

// waitPending calls pollStop until something has been read, so the assertions
// below do not race the reader goroutine.
func waitPending(t *testing.T, r *assocReader, msgID uint16) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.pollStop(msgID) {
			return true
		}
		if len(r.pending) > 0 {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("nothing arrived within the deadline")
	return false
}

// pollStop must not consume a message that is not the cancel it is looking for.
func TestAssocReaderPollStopPushesBackOtherMessages(t *testing.T) {
	t.Parallel()

	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	echo, err := (&dimse.CEchoRQ{MessageID: 9, AffectedSOPClassUID: pdu.VerificationSOPClass}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	go writeMsg(t, remote, echo)

	ctx := context.Background()
	r := newAssocReader(ctx, local)
	defer r.Close()

	if waitPending(t, r, 5) {
		t.Fatal("pollStop reported a stop for an unrelated C-ECHO-RQ")
	}
	// Polling again must not lose it either.
	if r.pollStop(5) {
		t.Fatal("second pollStop reported a stop")
	}

	_, cmd, _, err := r.message(ctx)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	got, err := dimse.DecodeCEchoRQ(cmd)
	if err != nil {
		t.Fatalf("the polled-over message was corrupted: %v", err)
	}
	if got.MessageID != 9 {
		t.Fatalf("MessageID=%d, want 9", got.MessageID)
	}
}

// A matching cancel is consumed, and the association keeps working afterwards.
func TestAssocReaderPollStopConsumesMatchingCancel(t *testing.T) {
	t.Parallel()

	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	cancelCmd, err := (&dimse.CCancelRQ{MessageIDBeingRespondedTo: 5}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	echo, err := (&dimse.CEchoRQ{MessageID: 6, AffectedSOPClassUID: pdu.VerificationSOPClass}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		writeMsg(t, remote, cancelCmd)
		writeMsg(t, remote, echo)
	}()

	ctx := context.Background()
	r := newAssocReader(ctx, local)
	defer r.Close()

	if !waitPending(t, r, 5) {
		t.Fatal("pollStop did not report the matching C-CANCEL-RQ")
	}
	for _, it := range r.pending {
		if c, err := dimse.DecodeCCancelRQ(it.Command); err == nil && c.MessageIDBeingRespondedTo == 5 {
			t.Fatal("the consumed cancel is still queued")
		}
	}

	_, cmd, _, err := r.message(ctx)
	if err != nil {
		t.Fatalf("message after cancel: %v", err)
	}
	if _, err := dimse.DecodeCEchoRQ(cmd); err != nil {
		t.Fatalf("message after cancel is not the C-ECHO-RQ: %v", err)
	}
}

// The reading goroutine must not outlive the connection.
func TestAssocReaderGoroutineExits(t *testing.T) {
	t.Parallel()

	local, remote := net.Pipe()
	defer remote.Close()

	r := newAssocReader(context.Background(), local)
	r.Close()
	_ = local.Close()

	select {
	case _, ok := <-r.ch:
		if ok {
			// A delivered item is fine as long as the channel then closes.
			select {
			case <-r.ch:
			case <-time.After(3 * time.Second):
				t.Fatal("reader goroutine still running")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader goroutine did not exit")
	}
}
