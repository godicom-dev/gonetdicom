package gonetdicom

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// Standard slog attribute keys for gonetdicom debug logs.
// Content aims at pynetdicom debug_logger coverage (association, PDU, DIMSE).
const (
	AttrComponent    = "component"
	AttrCallingAE    = "calling_ae"
	AttrCalledAE     = "called_ae"
	AttrAddr         = "addr"
	AttrRemote       = "remote"
	AttrTLS          = "tls"
	AttrContexts     = "contexts"
	AttrDirection    = "direction" // "send" | "recv"
	AttrPDUType      = "pdu_type"
	AttrPDUTypeName  = "pdu_type_name"
	AttrPCID         = "pc_id"
	AttrCommandField = "command_field"
	AttrCommandName  = "command_name"
	AttrMessageID    = "message_id"
	AttrStatus       = "status"
	AttrMethod       = "method"
	AttrURL          = "url"
	AttrHTTPStatus   = "http_status"
)

// Component values for AttrComponent.
const (
	ComponentAE       = "ae"
	ComponentSCP      = "scp"
	ComponentPDU      = "pdu"
	ComponentDIMSE    = "dimse"
	ComponentDICOMweb = "dicomweb"
)

type loggerContextKey struct{}

var defaultLogger atomic.Pointer[slog.Logger]

func init() {
	defaultLogger.Store(slog.New(slog.DiscardHandler))
}

// SetDefaultLogger sets the process-wide fallback logger used when neither
// context nor Config/Client supply one. Pass nil to restore DiscardHandler (quiet).
// Prefer Config.Logger, Client.Logger, or WithLogger for call-scoped logging.
func SetDefaultLogger(l *slog.Logger) {
	if l == nil {
		defaultLogger.Store(slog.New(slog.DiscardHandler))
		return
	}
	defaultLogger.Store(l)
}

// DefaultLogger returns the process-wide fallback logger.
func DefaultLogger() *slog.Logger {
	if l := defaultLogger.Load(); l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}

// WithLogger returns a child context that carries l for gonetdicom operations.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil {
		l = slog.New(slog.DiscardHandler)
	}
	return context.WithValue(ctx, loggerContextKey{}, l)
}

// LoggerFromContext returns the logger stored by WithLogger, or DefaultLogger
// when none is present.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return DefaultLogger()
}

// ResolveLogger picks opt over context, then default.
func ResolveLogger(ctx context.Context, opt *slog.Logger) *slog.Logger {
	if opt != nil {
		return opt
	}
	return LoggerFromContext(ctx)
}

// LoggerContext builds a context carrying the resolved logger and a
// component-scoped child logger.
func LoggerContext(parent context.Context, opt *slog.Logger, component string) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	l := ResolveLogger(parent, opt).With(AttrComponent, component)
	return WithLogger(parent, l)
}
