package ae_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

const secondaryCaptureSOPClass = "1.2.840.10008.5.1.4.1.1.7"

// mockSCUPeer accepts proposed contexts and handles C-ECHO / C-STORE / release.
func mockSCUPeer(t *testing.T, conn net.Conn) {
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

	acs := make([]pdu.PresentationContextAC, 0, len(rq.PresentationContexts))
	for _, pc := range rq.PresentationContexts {
		ts := pdu.ImplicitVRLittleEndian
		if len(pc.TransferSyntaxes) > 0 {
			ts = pc.TransferSyntaxes[0]
		}
		acs = append(acs, pdu.PresentationContextAC{
			ID:             pc.ID,
			Result:         0,
			TransferSyntax: ts,
		})
	}
	ac := &pdu.AAssociateAC{
		CalledAETitle:          rq.CalledAETitle,
		CallingAETitle:         rq.CallingAETitle,
		ApplicationContextName: rq.ApplicationContextName,
		PresentationContexts:   acs,
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

	var cmdBuf, dsBuf []byte
	var cmdDone, dsDone, expectDS bool

	resetMsg := func() {
		cmdBuf, dsBuf = nil, nil
		cmdDone, dsDone, expectDS = false, false, false
	}

	for {
		raw, err := pdu.Read(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("scp read: %v", err)
			}
			return
		}
		switch p := raw.(type) {
		case *pdu.PDataTF:
			for _, pdv := range p.PDVs {
				if pdv.IsCommand() {
					cmdBuf = append(cmdBuf, pdv.Fragment()...)
					if pdv.IsLast() {
						cmdDone = true
						hasDS, err := dimse.CommandHasDataset(cmdBuf)
						if err != nil {
							t.Errorf("scp command: %v", err)
							return
						}
						expectDS = hasDS
						if !expectDS {
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

			pcid := p.PDVs[0].ContextID
			// Detect message type from Command Field
			if isCEchoRQ(cmdBuf) {
				rqCmd, err := dimse.DecodeCEchoRQ(cmdBuf)
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
				if err := pdu.Write(conn, &pdu.PDataTF{PDVs: []pdu.PDV{pdu.NewCommandPDV(pcid, rspCmd)}}); err != nil {
					t.Errorf("scp write echo: %v", err)
					return
				}
			} else {
				rqCmd, err := dimse.DecodeCStoreRQ(cmdBuf)
				if err != nil {
					t.Errorf("scp decode store: %v", err)
					return
				}
				if len(dsBuf) == 0 {
					t.Errorf("scp store missing dataset")
					return
				}
				rspCmd, err := (&dimse.CStoreRSP{
					MessageIDBeingRespondedTo: rqCmd.MessageID,
					AffectedSOPClassUID:       rqCmd.AffectedSOPClassUID,
					AffectedSOPInstanceUID:    rqCmd.AffectedSOPInstanceUID,
					Status:                    dimse.StatusSuccess,
				}).Encode()
				if err != nil {
					t.Errorf("scp encode store: %v", err)
					return
				}
				if err := pdu.Write(conn, &pdu.PDataTF{PDVs: []pdu.PDV{pdu.NewCommandPDV(pcid, rspCmd)}}); err != nil {
					t.Errorf("scp write store: %v", err)
					return
				}
			}
			resetMsg()
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

func isCEchoRQ(cmd []byte) bool {
	_, err := dimse.DecodeCEchoRQ(cmd)
	return err == nil
}

func TestCEchoSCURoundtrip(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mockSCUPeer(t, server)
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
		mockSCUPeer(t, conn)
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

func TestCStoreSCURoundtrip(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mockSCUPeer(t, server)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: pdu.VerificationSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}
	assoc, err := ae.AcceptFromConn(ctx, cfg, client, "ANY-SCP")
	if err != nil {
		t.Fatalf("associate: %v", err)
	}

	// Minimal Implicit VR LE dataset: (0010,0010) PN Tube^HeNe / (0010,0020) LO Test1101
	dataset := []byte{
		0x10, 0x00, 0x10, 0x00, 0x0a, 0x00, 0x00, 0x00, 'T', 'u', 'b', 'e', '^', 'H', 'e', 'N', 'e', ' ',
		0x10, 0x00, 0x20, 0x00, 0x08, 0x00, 0x00, 0x00, 'T', 'e', 's', 't', '1', '1', '0', '1',
	}

	res, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Dataset:                dataset,
		Priority:               dimse.PriorityLow,
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if res.AffectedSOPInstanceUID != "1.2.3.4.5" {
		t.Fatalf("instance %q", res.AffectedSOPInstanceUID)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done
}

func goldenStoreDataset() []byte {
	return []byte{
		0x10, 0x00, 0x10, 0x00, 0x0a, 0x00, 0x00, 0x00, 'T', 'u', 'b', 'e', '^', 'H', 'e', 'N', 'e', ' ',
		0x10, 0x00, 0x20, 0x00, 0x08, 0x00, 0x00, 0x00, 'T', 'e', 's', 't', '1', '1', '0', '1',
	}
}

func TestCStoreSCUWithGodicomDataset(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mockSCUPeer(t, server)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ae.Config{
		AETitle: "STORESCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 3, AbstractSyntax: secondaryCaptureSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}
	assoc, err := ae.AcceptFromConn(ctx, cfg, client, "ANY-SCP")
	if err != nil {
		t.Fatalf("associate: %v", err)
	}

	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientName"), godicom.VRPN, "Tube^HeNe"))
	ds.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "Test1101"))

	res, err := assoc.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID:    secondaryCaptureSOPClass,
		AffectedSOPInstanceUID: "1.2.3.4.5",
		Data:                   ds,
	})
	if err != nil {
		t.Fatalf("C-STORE: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x", res.Status)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done
}

func TestFragmentMessageSmallMaxPDU(t *testing.T) {
	t.Parallel()
	cmd := bytes.Repeat([]byte{0xab}, 40)
	ds := bytes.Repeat([]byte{0xcd}, 50)
	pdus, err := pdu.FragmentMessage(1, cmd, ds, 20) // maxFrag = 14
	if err != nil {
		t.Fatal(err)
	}
	if len(pdus) < 4 {
		t.Fatalf("expected multiple PDUs, got %d", len(pdus))
	}
	var gotCmd, gotDS []byte
	for _, p := range pdus {
		for _, pdv := range p.PDVs {
			if pdv.IsCommand() {
				gotCmd = append(gotCmd, pdv.Fragment()...)
			} else {
				gotDS = append(gotDS, pdv.Fragment()...)
			}
		}
	}
	if !bytes.Equal(gotCmd, cmd) || !bytes.Equal(gotDS, ds) {
		t.Fatalf("reassembly mismatch")
	}
}
