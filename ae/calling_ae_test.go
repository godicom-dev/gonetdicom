package ae_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// A list of calling AE Titles is the "who may address me" half of what the
// Called AE Title check does for "which name is mine". pynetdicom spells the
// same field require_calling_aet.
func TestSCPRejectsUnlistedCallingAETitle(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                 "REAL_SCP",
		RequiredCallingAETitles: []string{"ALLOWED_AE"},
	})
	rq := associateRQ("REAL_SCP")
	rq.CallingAETitle = "STRANGER"
	requireReject(t, associate(t, addr, rq),
		pdu.RejectSourceServiceUser, pdu.RejectReasonCallingAENotRecognized)
}

// The titles are compared after padding is stripped: an AE title is sixteen
// bytes padded with spaces on the wire, and a list written in Go never is.
func TestSCPAcceptsListedCallingAETitle(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                 "REAL_SCP",
		RequiredCallingAETitles: []string{"ALLOWED_AE", "OTHER_AE"},
	})
	rq := associateRQ("REAL_SCP")
	rq.CallingAETitle = "OTHER_AE" // encoded padded to sixteen bytes
	if _, ok := associate(t, addr, rq).(*pdu.AAssociateAC); !ok {
		t.Fatal("expected an A-ASSOCIATE-AC for a listed calling AE title")
	}
}

// An empty list is not "serve nobody": it is the default, which serves any peer
// that names itself. A configuration bug that emptied the list must not turn the
// SCP into one nothing can talk to.
func TestSCPAcceptsAnyCallingAETitleWhenUnset(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{AETitle: "REAL_SCP"})
	rq := associateRQ("REAL_SCP")
	rq.CallingAETitle = "ANYONE"
	if _, ok := associate(t, addr, rq).(*pdu.AAssociateAC); !ok {
		t.Fatal("expected an A-ASSOCIATE-AC when no calling AE titles are required")
	}
}

// The rejection is the requestor's to read, not a dropped connection.
func TestDialReportsCallingAETitleRejection(t *testing.T) {
	t.Parallel()

	addr := serveSCP(t, ae.ServerConfig{
		AETitle:                 "REAL_SCP",
		RequiredCallingAETitles: []string{"ALLOWED_AE"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ae.Dial(ctx, ae.Config{AETitle: "STRANGER"}, addr, "REAL_SCP")
	if err == nil {
		t.Fatal("Dial succeeded against an SCP that does not serve this calling AE")
	}
	if !errors.Is(err, ae.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}
