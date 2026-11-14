package gonetdicom_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/godicom-dev/gonetdicom"
)

// Every logging entry point has to tolerate a nil logger and a nil context:
// this package is called from handlers that were handed neither, and a nil
// dereference there would turn a log statement into a crash.
func TestLoggerHelpersTolerateNil(t *testing.T) {
	// A nil context reaches these helpers from callers that never had one; the
	// point of the test is that it is tolerated, so it has to be passed.
	var nilCtx context.Context

	ctx := gonetdicom.WithLogger(nilCtx, nil)
	if ctx == nil {
		t.Fatal("WithLogger(nil, nil) returned a nil context")
	}
	if l := gonetdicom.LoggerFromContext(nilCtx); l == nil {
		t.Fatal("LoggerFromContext(nil) returned nil")
	}
	if l := gonetdicom.LoggerFromContext(ctx); l == nil {
		t.Fatal("LoggerFromContext(ctx) returned nil")
	}
	if l := gonetdicom.ResolveLogger(nilCtx, nil); l == nil {
		t.Fatal("ResolveLogger(nil, nil) returned nil")
	}
	if ctx := gonetdicom.LoggerContext(nilCtx, nil, gonetdicom.ComponentAE); ctx == nil {
		t.Fatal("LoggerContext(nil, nil, ...) returned a nil context")
	}
	if l := gonetdicom.DefaultLogger(); l == nil {
		t.Fatal("DefaultLogger returned nil")
	}
}

// The precedence is explicit logger, then context, then process default. It is
// easy to reverse by accident and impossible to notice until the wrong file is
// being written to at 3 a.m.
func TestLoggerPrecedence(t *testing.T) {
	explicit := slog.New(slog.DiscardHandler).With("from", "explicit")
	fromCtx := slog.New(slog.DiscardHandler).With("from", "context")

	ctx := gonetdicom.WithLogger(context.Background(), fromCtx)
	if got := gonetdicom.ResolveLogger(ctx, explicit); got != explicit {
		t.Error("an explicit logger did not win over the context")
	}
	if got := gonetdicom.ResolveLogger(ctx, nil); got != fromCtx {
		t.Error("the context logger did not win over the default")
	}

	// SetDefaultLogger is the last resort, and nil restores the quiet default.
	previous := gonetdicom.DefaultLogger()
	t.Cleanup(func() { gonetdicom.SetDefaultLogger(previous) })
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	gonetdicom.SetDefaultLogger(custom)
	if got := gonetdicom.DefaultLogger(); got != custom {
		t.Error("SetDefaultLogger did not take effect")
	}
	gonetdicom.SetDefaultLogger(nil)
	if got := gonetdicom.DefaultLogger(); got == nil || got == custom {
		t.Error("SetDefaultLogger(nil) did not restore the quiet default")
	}
}
