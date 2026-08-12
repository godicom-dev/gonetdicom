package dimse_test

import (
	"testing"

	"github.com/godicom-dev/gonetdicom/dimse"
)

func TestPeekCommandFieldAndName(t *testing.T) {
	t.Parallel()
	cmd, err := (&dimse.CEchoRQ{MessageID: 7}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	field, err := dimse.PeekCommandField(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if field != dimse.CommandCEchoRQ {
		t.Fatalf("field=%04x", field)
	}
	if got := dimse.CommandName(field); got != "C-ECHO-RQ" {
		t.Fatalf("name=%q", got)
	}
	msgID, ok := dimse.PeekMessageID(cmd)
	if !ok || msgID != 7 {
		t.Fatalf("message id=%d ok=%v", msgID, ok)
	}
}

func TestPeekStatus(t *testing.T) {
	t.Parallel()
	cmd, err := (&dimse.CEchoRSP{MessageIDBeingRespondedTo: 1, Status: dimse.StatusSuccess}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := dimse.PeekStatus(cmd)
	if !ok || st != dimse.StatusSuccess {
		t.Fatalf("status=%04x ok=%v", st, ok)
	}
	if dimse.FormatStatus(st) != "0x0000" {
		t.Fatalf("format=%s", dimse.FormatStatus(st))
	}
}
