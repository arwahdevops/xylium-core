package xylium

import (
	"fmt" // For LogLevel.String() formatting.
	"io"  // For Logger.SetOutput parameter type and io.Closer.
)

// HandlerFunc defines the standard function signature for Xylium request handlers.
// It is also the signature for the final step in a middleware chain.
//
// A `HandlerFunc` receives a `*xylium.Context` which encapsulates all request-specific
// information and provides methods for response generation. It should return an `error`
// if request processing fails. This error will typically be handled by Xylium's
// `GlobalErrorHandler`. Returning `nil` indicates successful processing.
type HandlerFunc func(*Context) error

// Middleware defines the standard function signature for Xylium middleware.
// Middleware are functions that can process an HTTP request before it reaches the
// main handler or after the main handler has executed. They form a chain where
// each middleware calls the `next` `HandlerFunc` to pass control.
//
// A `Middleware` function takes the `next HandlerFunc` in the chain as an argument
// and must return a new `HandlerFunc` that typically:
//  1. Performs some operations before calling `next(c)`.
//  2. Calls `next(c)`.
//  3. Performs some operations after `next(c)` returns.
//
// It can also short-circuit the chain by not calling `next(c)` and directly
// returning an error or sending a response.
type Middleware func(next HandlerFunc) HandlerFunc

// --- Logger Definitions ---

// LogLevel defines the severity level of a log message. It is used by Xylium's
// logging system (e.g., `DefaultLogger`) to control which messages are outputted.
// Log levels are ordered from most verbose (Debug) to most critical (Panic).
type LogLevel int

// Log level constants define the standard severity levels for logging.
const (
	LevelDebug LogLevel = iota // DebugLevel logs are typically verbose messages useful for development and detailed tracing.
	LevelInfo                  // InfoLevel logs are informational messages about the normal operation of the application.
	LevelWarn                  // WarnLevel logs indicate potential issues or unusual situations that are not necessarily errors.
	LevelError                 // ErrorLevel logs report errors that occurred during request processing or application operation but may not be fatal.
	LevelFatal                 // FatalLevel logs report critical errors. After logging a Fatal message, the `DefaultLogger` will call `os.Exit(1)`.
	LevelPanic                 // PanicLevel logs report critical errors. After logging a Panic message, the `DefaultLogger` will call `panic()`.
)

// String returns the uppercase string representation of the `LogLevel`.
// For example, `LevelDebug.String()` returns "DEBUG".
// If the log level is unknown, it returns "UNKNOWN_LEVEL(value)".
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
		// Return a descriptive string for unknown log levels.
		return fmt.Sprintf("UNKNOWN_LEVEL(%d)", l)
	}
}

// Logger defines the interface for Xylium's logging system.
// Implementations of this interface (like `DefaultLogger`) provide methods for
// leveled logging (Debug, Info, Warn, Error, Fatal, Panic) and structured logging
// capabilities via `WithFields`.
//
// The interface allows for replacing Xylium's `DefaultLogger` with a custom
// logging solution if needed, by providing a compatible implementation.
type Logger interface {
	// Printf logs a message at LevelInfo using a format string and arguments.
	// This is similar to `log.Printf` but at a fixed Info level.
	Printf(format string, args ...interface{})

	// Debug logs a message at LevelDebug. Arguments are handled by `fmt.Sprint`.
	Debug(args ...interface{})
	// Info logs a message at LevelInfo. Arguments are handled by `fmt.Sprint`.
	Info(args ...interface{})
	// Warn logs a message at LevelWarn. Arguments are handled by `fmt.Sprint`.
	Warn(args ...interface{})
	// Error logs a message at LevelError. Arguments are handled by `fmt.Sprint`.
	Error(args ...interface{})
	// Fatal logs a message at LevelFatal, then the logger implementation should
	// typically terminate the application (e.g., `DefaultLogger` calls `os.Exit(1)`).
	// Arguments are handled by `fmt.Sprint`.
	Fatal(args ...interface{})
	// Panic logs a message at LevelPanic, then the logger implementation should
	// typically trigger a panic (e.g., `DefaultLogger` calls `panic()`).
	// Arguments are handled by `fmt.Sprint`.
	Panic(args ...interface{})

	// Debugf logs a formatted message at LevelDebug using `fmt.Sprintf` style.
	Debugf(format string, args ...interface{})
	// Infof logs a formatted message at LevelInfo using `fmt.Sprintf` style.
	Infof(format string, args ...interface{})
	// Warnf logs a formatted message at LevelWarn using `fmt.Sprintf` style.
	Warnf(format string, args ...interface{})
	// Errorf logs a formatted message at LevelError using `fmt.Sprintf` style.
	Errorf(format string, args ...interface{})
	// Fatalf logs a formatted message at LevelFatal using `fmt.Sprintf` style,
	// then the logger implementation should typically terminate the application.
	Fatalf(format string, args ...interface{})
	// Panicf logs a formatted message at LevelPanic using `fmt.Sprintf` style,
	// then the logger implementation should typically trigger a panic.
	Panicf(format string, args ...interface{})

	// WithFields returns a new `Logger` instance that includes the given `fields`
	// (a `xylium.M` map) in all subsequent log entries made with the new logger.
	// This is used for structured logging, allowing context-specific key-value
	// data to be consistently logged. The original logger is not modified.
	WithFields(fields M) Logger

	// SetOutput sets the output destination `io.Writer` for the logger.
	SetOutput(w io.Writer)
	// SetLevel sets the minimum `LogLevel` for the logger.
	SetLevel(level LogLevel)
	// GetLevel returns the current `LogLevel` of the logger.
	GetLevel() LogLevel
}

// M is a convenient type alias for `map[string]interface{}`, commonly used for
// passing structured data, such as fields to the logger (`logger.WithFields(M{...})`),
// data for JSON responses (`c.JSON(StatusOK, M{...})`), or context for HTML templates.
type M map[string]interface{}

// --- Standard Context Keys ---
// These string constants define well-known keys used for storing and retrieving
// specific pieces of information within a `xylium.Context`'s request-scoped store (`c.store`).
// Using these constants helps avoid "magic strings" in application code and middleware,
// promoting consistency and reducing the risk of typos.

// ContextKeyRequestID is the key used in `c.store` to hold the unique request identifier
// string for the current HTTP request.
// This ID is typically generated and set by the `xylium.RequestID` middleware.
// Xylium's contextual logger (`c.Logger()`) automatically picks up this value
// if present and includes it in log entries (often as `xylium_request_id`).
const ContextKeyRequestID string = "xylium_request_id"

// ContextKeyOtelTraceID is the key used in `c.store` to hold the OpenTelemetry Trace ID
// string associated with the current request.
// This is typically set by an OpenTelemetry integration middleware (e.g., from a
// `xylium-otel` connector). `c.Logger()` may also include this as `trace_id` in logs.
const ContextKeyOtelTraceID string = "otel_trace_id"

// ContextKeyOtelSpanID is the key used in `c.store` to hold the OpenTelemetry Span ID
// string for the current active span within the request.
// This is also typically set by an OpenTelemetry integration middleware.
// `c.Logger()` may also include this as `span_id` in logs.
const ContextKeyOtelSpanID string = "otel_span_id"

// ContextKeyPanicInfo is the key used in `c.store` to store the information (the value
// passed to `panic()`) recovered by Xylium's main request handler when a panic
// occurs during request processing.
// This value is set *before* the `Router.PanicHandler` is invoked, allowing the
// panic handler to access details about the panic.
const ContextKeyPanicInfo string = "xylium_panic_info"

// ContextKeyErrorCause is the key used in `c.store` to store the original `error`
// that caused the `Router.GlobalErrorHandler` to be invoked. This could be an error
// returned by a route handler, a middleware, or the `Router.PanicHandler`.
// This value is set *before* the `GlobalErrorHandler` is called.
const ContextKeyErrorCause string = "xylium_error_cause"

// ContextKeyCSRFToken is the default key used by the `xylium.CSRF` middleware to
// store the generated CSRF token (intended for the *next* request's validation)
// in the *current* request's `xylium.Context` store (`c.store`).
// Handlers processing the current request (e.g., rendering an HTML form) can use
// `c.Get(ContextKeyCSRFToken)` to retrieve this token and embed it in the response,
// so it can be submitted by the client with a subsequent state-changing request.
// This key's value can be customized via `CSRFConfig.ContextTokenKey`.
const ContextKeyCSRFToken string = "csrf_token"

// Note: `ConfiguredCSRFErrorHandlerErrorKey` is defined in `middleware_csrf.go` as it's specific to that middleware's
// internal communication with a custom error handler. It's not a general-purpose context key.
