package xylium

import (
	"fmt" // For LogLevel.String() formatting.
	"io"  // For Logger.SetOutput parameter type and io.Closer.
)

// HandlerFunc defines the function signature for Xylium request handlers and also
// for the final step in a middleware chain.
type HandlerFunc func(*Context) error

// Middleware defines the function signature for Xylium middleware.
type Middleware func(next HandlerFunc) HandlerFunc

// --- Logger Definitions ---

// LogLevel defines the severity level of a log message.
type LogLevel int

// Log level constants.
const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
	LevelPanic
)

// String returns the string representation of the LogLevel.
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	case LevelPanic:
		return "PANIC"
	default:
		return fmt.Sprintf("UNKNOWN_LEVEL(%d)", l)
	}
}

// Logger is Xylium's logging interface.
type Logger interface {
	Printf(format string, args ...interface{})
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})
	Panic(args ...interface{})
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Panicf(format string, args ...interface{})
	WithFields(fields M) Logger
	SetOutput(w io.Writer)
	SetLevel(level LogLevel)
	GetLevel() LogLevel
}

// M is a shorthand type alias for `map[string]interface{}`.
type M map[string]interface{}

// --- Standard Context Keys ---
// These constants define keys used for storing well-known values in the Xylium Context store (c.store).
// Using constants helps avoid magic strings and ensures consistency.

// ContextKeyRequestID is the key used to store the request ID in the Xylium Context.
// Typically set by the RequestID middleware. Logger often picks this up.
const ContextKeyRequestID string = "xylium_request_id"

// ContextKeyOtelTraceID is the key used to store the OpenTelemetry Trace ID in the Xylium Context.
// Typically set by the OpenTelemetry (Otel) middleware.
const ContextKeyOtelTraceID string = "otel_trace_id"

// ContextKeyOtelSpanID is the key used to store the OpenTelemetry Span ID in the Xylium Context.
// Typically set by the OpenTelemetry (Otel) middleware.
const ContextKeyOtelSpanID string = "otel_span_id"

// ContextKeyPanicInfo is the key used to store the recovered panic information in the Xylium Context.
// Set by the Router's main panic recovery mechanism before calling the PanicHandler.
const ContextKeyPanicInfo string = "xylium_panic_info"

// ContextKeyErrorCause is the key used to store the original error that caused the
// GlobalErrorHandler to be invoked.
// Set by the Router before calling the GlobalErrorHandler.
const ContextKeyErrorCause string = "xylium_error_cause"

// ContextKeyCSRFToken is the default key used by the CSRF middleware to store
// the generated CSRF token in the Xylium Context store, making it available
// to handlers (e.g., for embedding in HTML forms).
// This should match `DefaultCSRFConfig.ContextTokenKey`.
const ContextKeyCSRFToken string = "csrf_token"
