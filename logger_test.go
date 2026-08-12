package gonetdicom_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/godicom-dev/gonetdicom"
)

func TestDefaultLoggerIsQuiet(t *testing.T) {
	prev := gonetdicom.DefaultLogger()
	t.Cleanup(func() { gonetdicom.SetDefaultLogger(prev) })

	gonetdicom.SetDefaultLogger(nil)
	l := gonetdicom.DefaultLogger()
	if l.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("default logger should discard")
	}
}

func TestWithLogger_ContextOverride(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := gonetdicom.WithLogger(context.Background(), l)
	got := gonetdicom.LoggerFromContext(ctx)
	got.Debug("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("expected context logger output, got %q", buf.String())
	}
}

func TestResolveLogger_OptionsWin(t *testing.T) {
	var ctxBuf, optBuf bytes.Buffer
	ctxLog := slog.New(slog.NewTextHandler(&ctxBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	optLog := slog.New(slog.NewTextHandler(&optBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := gonetdicom.WithLogger(context.Background(), ctxLog)
	got := gonetdicom.ResolveLogger(ctx, optLog)
	got.Info("from-opt")
	if strings.Contains(ctxBuf.String(), "from-opt") {
		t.Fatal("options logger should win over context")
	}
	if !strings.Contains(optBuf.String(), "from-opt") {
		t.Fatalf("expected options logger output, got %q", optBuf.String())
	}
}

func TestLoggerContext_AddsComponent(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := gonetdicom.LoggerContext(context.Background(), l, gonetdicom.ComponentAE)
	gonetdicom.LoggerFromContext(ctx).Info("assoc")
	out := buf.String()
	if !strings.Contains(out, "component=ae") {
		t.Fatalf("missing component=ae:\n%s", out)
	}
}
