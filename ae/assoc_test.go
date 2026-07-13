package ae_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// mockVerificationSCP accepts Verification and answers C-ECHO, then release.
func mockVerificationSCP(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()

	raw, err := pdu.Read(conn)
	if err != nil {
		t.Errorf("scp read RQ: %v", err)
		return
	}
	rq, ok := raw.(*pdu.AAssociateRQ)
	if !ok {
		t.Errorf("scp expected A-ASSOCIATE-RQ, got %T", raw)
		return
	}

	ac := &pdu.AAssociateAC{
		CalledAETitle:          rq.CalledAETitle,
		CallingAETitle:         rq.CallingAETitle,
		ApplicationContextName: rq.ApplicationContextName,
		PresentationContexts: []pdu.PresentationContextAC{{
			ID:             rq.PresentationContexts[0].ID,
			Result:         0,
			TransferSyntax: pdu.ImplicitVRLittleEndian,
		}},
		UserInformation: pdu.UserInformation{
			MaxLength:                 16384,
			ImplementationClassUID:    "1.2.826.0.1.3680043.10.541.2",
			ImplementationVersionName: "MOCKSCP_001",
		},
	}
	if err := pdu.Write(conn, ac); err != nil {
		t.Errorf("scp write AC: %v", err)
		return
	}

	for {
		raw, err := pdu.Read(conn)
		if err != nil {
			if err != io.EOF {
				t.Errorf("scp read: %v", err)
			}
			return
		}
		switch p := raw.(type) {
		case *pdu.PDataTF:
			if len(p.PDVs) == 0 || !p.PDVs[0].IsCommand() {
				t.Errorf("scp: expected command PDV")
				return
			}
			rqCmd, err := dimse.DecodeCEchoRQ(p.PDVs[0].Fragment())
			if err != nil {
				t.Errorf("scp decode echo: %v", err)
				return
			}
			rspCmd, err := (&dimse.CEchoRSP{
				MessageIDBeingRespondedTo: rqCmd.MessageID,
				Status:                    dimse.StatusSuccess,
			}).Encode()
			if err != nil {
				t.Errorf("scp encode echo: %v", err)
				return
			}
			tf := &pdu.PDataTF{PDVs: []pdu.PDV{pdu.NewCommandPDV(p.PDVs[0].ContextID, rspCmd)}}
			if err := pdu.Write(conn, tf); err != nil {
				t.Errorf("scp write echo: %v", err)
				return
			}
		case *pdu.AReleaseRQ:
			if err := pdu.Write(conn, &pdu.AReleaseRP{}); err != nil {
				t.Errorf("scp release: %v", err)
			}
			return
		case *pdu.AAbort:
			return
		default:
			t.Errorf("scp unexpected PDU %T", p)
			return
		}
	}
}

func TestCEchoSCURoundtrip(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mockVerificationSCP(t, server)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assoc, err := ae.AcceptFromConn(ctx, ae.Config{AETitle: "ECHOSCU"}, client, "ANY-SCP")
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	if err := assoc.CEcho(ctx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done
}

func TestDialLocalListener(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		mockVerificationSCP(t, conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assoc, err := ae.Dial(ctx, ae.Config{AETitle: "ECHOSCU"}, ln.Addr().String(), "ANY-SCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := assoc.CEcho(ctx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done
}
