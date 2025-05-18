package xylium

import (
	"fmt" // For LogLevel.String() formatting.
	"io"  // For Logger.SetOutput parameter type.
)

// HandlerFunc defines the function signature for Xylium request handlers and also
// for the final step in a middleware chain.
// A HandlerFunc receives a `*xylium.Context` which contains request and response information.
// It must return an `error`. If a non-nil error is returned, Xylium's global error
// handler (`Router.GlobalErrorHandler`) will process it. If nil is returned, it's assumed
// the handler has successfully processed the request (potentially sending a response).
type HandlerFunc func(*Context) error

// Middleware defines the function signature for Xylium middleware.
// A Middleware function takes the `next` `HandlerFunc` in the chain as an argument
// and returns a new `HandlerFunc`. The returned handler typically performs some actions
// before and/or after calling `next(c)`.
// This pattern allows for chaining and modification of request/response flow.
type Middleware func(next HandlerFunc) HandlerFunc

// --- Logger Definitions ---

// LogLevel defines the severity level of a log message.
// Xylium's `DefaultLogger` uses these levels to filter messages; only messages
// at or above the logger's configured level will be output.
type LogLevel int

// Log level constants.
// These are ordered by severity, with lower integer values indicating higher
// verbosity (more detailed logs) and higher values indicating greater severity.
const (
	LevelDebug LogLevel = iota // LevelDebug (0): Detailed debugging information, typically for development.
	LevelInfo                  // LevelInfo (1): Routine operational messages, informational events.
	LevelWarn                  // LevelWarn (2): Warnings about potential issues or unusual, but not critical, events.
	LevelError                 // LevelError (3): Errors that occurred during operation but might not necessarily stop the application.
	LevelFatal                 // LevelFatal (4): Critical errors after which the application will exit (via os.Exit(1)).
	LevelPanic                 // LevelPanic (5): Errors that cause a panic after logging. The panic should be recovered by the framework.
)

// String returns the string representation of the LogLevel (e.g., "DEBUG", "INFO").
// This is useful for log output formatting and debugging.
// It handles unknown LogLevel values gracefully by returning a formatted string.
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
		// Fallback for unknown LogLevel values to prevent panics if an invalid integer
		// is somehow cast to LogLevel.
		return fmt.Sprintf("UNKNOWN_LEVEL(%d)", l)
	}
}

// Logger is Xylium's logging interface, designed for leveled and structured logging.
// Applications can provide their own `Logger` implementation, or use Xylium's
// `DefaultLogger`. The router's logger (`c.Logger()`) is configured based on Xylium's
// operating mode.
type Logger interface {
	// Printf is a basic logging method, primarily for compatibility or simple messages.
	// Implementations will typically log this at `LevelInfo`.
	// It's generally recommended to use more specific leveled methods like `Infof` for clarity.
	Printf(format string, args ...interface{})

	// Leveled logging methods that take variadic arguments, typically joined by `fmt.Sprint`.
	Debug(args ...interface{}) // Logs a message at LevelDebug.
	Info(args ...interface{})  // Logs a message at LevelInfo.
	Warn(args ...interface{})  // Logs a message at LevelWarn.
	Error(args ...interface{}) // Logs a message at LevelError.
	// Fatal logs the message at LevelFatal and then calls `os.Exit(1)`.
	Fatal(args ...interface{})
	// Panic logs the message at LevelPanic and then calls `panic()`.
	Panic(args ...interface{})

	// Leveled logging methods with string formatting, similar to `fmt.Printf`.
	Debugf(format string, args ...interface{}) // Logs a formatted message at LevelDebug.
	Infof(format string, args ...interface{})  // Logs a formatted message at LevelInfo.
	Warnf(format string, args ...interface{})  // Logs a formatted message at LevelWarn.
	Errorf(format string, args ...interface{}) // Logs a formatted message at LevelError.
	// Fatalf logs the formatted message at LevelFatal and then calls `os.Exit(1)`.
	Fatalf(format string, args ...interface{})
	// Panicf logs the formatted message at LevelPanic and then calls `panic()`.
	Panicf(format string, args ...interface{})

	// WithFields returns a new Logger instance that includes the given `fields` (key-value pairs)
	// in all subsequent log entries made by that new logger instance.
	// This is extremely useful for adding contextual information (e.g., request_id, user_id, tenant_id)
	// to a set of logs without modifying the original logger.
	// The original logger is not modified, ensuring immutability for shared logger instances.
	WithFields(fields M) Logger

	// SetOutput sets the output destination for the logger (e.g., `os.Stdout`, a file writer).
	// This allows redirecting log output dynamically.
	SetOutput(w io.Writer)

	// SetLevel sets the minimum logging level for this logger instance.
	// Messages with a level lower (i.e., more verbose, like Debug when level is Info)
	// than this will be discarded by the logger.
	SetLevel(level LogLevel)

	// GetLevel returns the current minimum logging level of this logger instance.
	GetLevel() LogLevel
}

// M is a shorthand type alias for `map[string]interface{}`.
// It is commonly used in Xylium for:
// - Structured log fields passed to `Logger.WithFields()` or as arguments to logging methods.
// - Constructing JSON response bodies (e.g., `c.JSON(http.StatusOK, xylium.M{"message": "success"})`).
// - Representing generic map data within the framework.
type M map[string]interface{}
