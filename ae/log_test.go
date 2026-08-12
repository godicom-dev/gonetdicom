package ae_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/ae"
)

func TestDial_DebugLogsPDUAndDIMSE(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "DBG SCP",
			Logger:  logger,
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "DBGSCU",
		Logger:  logger,
	}, ln.Addr().String(), "DBGSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := assoc.CEcho(dialCtx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh

	out := logBuf.String()
	for _, want := range []string{
		"component=ae",
		"association established",
		"pdu_type_name=A-ASSOCIATE-RQ",
		"command_name=C-ECHO-RQ",
		"association released",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDial_ContextLogger(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = ae.Serve(ctx, ln, ae.ServerConfig{AETitle: "CTXSCP"}) }()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dialCtx, dialCancel := context.WithTimeout(gonetdicom.WithLogger(context.Background(), logger), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{AETitle: "CTXSCU"}, ln.Addr().String(), "CTXSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := assoc.CEcho(dialCtx); err != nil {
		t.Fatal(err)
	}
	_ = assoc.Release(dialCtx)
	cancel()

	if !strings.Contains(logBuf.String(), "association established") {
		t.Fatalf("expected context logger output:\n%s", logBuf.String())
	}
}
