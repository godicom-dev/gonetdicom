package ae_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// fakePeer accepts one association and then hands the connection to script,
// which drives it at the PDU level. Negotiation is real: the AC echoes the
// requestor's presentation contexts so the association establishes normally.
func fakePeer(t *testing.T, script func(conn net.Conn, rq *pdu.AAssociateRQ)) (addr string, done <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ch := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("fakePeer: accept: %v", err)
			ch <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

		raw, err := pdu.Read(conn)
		if err != nil {
			t.Errorf("fakePeer: read A-ASSOCIATE-RQ: %v", err)
			ch <- err
			return
		}
		rq, ok := raw.(*pdu.AAssociateRQ)
		if !ok {
			err := fmt.Errorf("fakePeer: expected A-ASSOCIATE-RQ, got %T", raw)
			t.Error(err)
			ch <- err
			return
		}

		var contexts []pdu.PresentationContextAC
		for _, pc := range rq.PresentationContexts {
			contexts = append(contexts, pdu.PresentationContextAC{
				ID:             pc.ID,
				Result:         0,
				TransferSyntax: pc.TransferSyntaxes[0],
			})
		}
		ac := &pdu.AAssociateAC{
			ProtocolVersion:        pdu.ProtocolVersion1,
			CalledAETitle:          rq.CalledAETitle,
			CallingAETitle:         rq.CallingAETitle,
			ApplicationContextName: rq.ApplicationContextName,
			PresentationContexts:   contexts,
			UserInformation: pdu.UserInformation{
				MaxLength:              16384,
				ImplementationClassUID: ae.ImplementationClassUID,
			},
		}
		if err := pdu.Write(conn, ac); err != nil {
			t.Errorf("fakePeer: write A-ASSOCIATE-AC: %v", err)
			ch <- err
			return
		}

		script(conn, rq)
		ch <- nil
	}()
	return ln.Addr().String(), ch
}

// PS3.8 Table 9-10 makes A-RELEASE-RQ valid in the P-DATA state: the receiver
// answers A-RELEASE-RP. A requestor that dropped it as "unexpected PDU" left the
// peer waiting for an RP that never came, and the operation blocked until the
// socket died instead of reporting that the peer had walked away.
func TestSCURepliesToReleaseWhileAwaitingResponse(t *testing.T) {
	t.Parallel()

	gotRP := make(chan struct{})
	addr, peerDone := fakePeer(t, func(conn net.Conn, rq *pdu.AAssociateRQ) {
		// Read the request's P-DATA, then release without answering it.
		if _, err := pdu.Read(conn); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := pdu.Write(conn, &pdu.AReleaseRQ{}); err != nil {
			t.Errorf("send A-RELEASE-RQ: %v", err)
			return
		}
		raw, err := pdu.Read(conn)
		if err != nil {
			t.Errorf("await A-RELEASE-RP: %v", err)
			return
		}
		if _, ok := raw.(*pdu.AReleaseRP); !ok {
			t.Errorf("got %T, want A-RELEASE-RP", raw)
			return
		}
		close(gotRP)
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "RELEASES",
		PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, "RELEASEP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	if err := assoc.CEcho(dialCtx); !errors.Is(err, ae.ErrReleased) {
		t.Fatalf("C-ECHO error = %v, want ErrReleased", err)
	}

	select {
	case <-gotRP:
	case <-time.After(5 * time.Second):
		t.Fatal("peer never received the A-RELEASE-RP")
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}

// Release collision (PS3.8 9.3.3): both sides send an A-RELEASE-RQ, each answers
// the other's with an A-RELEASE-RP, and each still waits for one of its own. The
// peer's RQ arriving during Release used to end it as an unexpected PDU.
func TestSCUReleaseCollision(t *testing.T) {
	t.Parallel()

	addr, peerDone := fakePeer(t, func(conn net.Conn, rq *pdu.AAssociateRQ) {
		raw, err := pdu.Read(conn)
		if err != nil {
			t.Errorf("await A-RELEASE-RQ: %v", err)
			return
		}
		if _, ok := raw.(*pdu.AReleaseRQ); !ok {
			t.Errorf("got %T, want A-RELEASE-RQ", raw)
			return
		}
		// Collision: our own RQ goes out before we answer theirs.
		if err := pdu.Write(conn, &pdu.AReleaseRQ{}); err != nil {
			t.Errorf("send colliding A-RELEASE-RQ: %v", err)
			return
		}
		raw, err = pdu.Read(conn)
		if err != nil {
			t.Errorf("await A-RELEASE-RP: %v", err)
			return
		}
		if _, ok := raw.(*pdu.AReleaseRP); !ok {
			t.Errorf("got %T, want A-RELEASE-RP", raw)
			return
		}
		if err := pdu.Write(conn, &pdu.AReleaseRP{}); err != nil {
			t.Errorf("send A-RELEASE-RP: %v", err)
		}
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "COLLIDES",
		PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax:   ae.StudyRootQueryRetrieveInformationModelFind,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, "COLLIDEP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer: %v", err)
	}
}
