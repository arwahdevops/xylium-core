// src/xylium/types.go
package xylium

import (
	"fmt" // For LogLevel.String() formatting
	"io"  // For Logger.SetOutput parameter
)

// HandlerFunc defines the function signature for request handlers and middleware.
// It returns an error, which if non-nil, will be processed by the global error handler.
type HandlerFunc func(*Context) error

// Middleware defines the function signature for middleware.
// It takes the next HandlerFunc and returns a new HandlerFunc that typically calls next.
type Middleware func(next HandlerFunc) HandlerFunc

// --- Logger Definitions ---

// LogLevel defines the severity level of a log message.
type LogLevel int

// Log level constants.
// Lower values indicate higher verbosity (more detailed logs).
const (
	LevelDebug LogLevel = iota // LevelDebug messages, typically for development. Value: 0
	LevelInfo                  // LevelInfo messages, routine operations. Value: 1
	LevelWarn                  // LevelWarn messages, potential issues. Value: 2
	LevelError                 // LevelError messages, operational errors that might not stop the application. Value: 3
	LevelFatal                 // LevelFatal errors, application will exit after logging. Value: 4
	LevelPanic                 // LevelPanic errors, application will panic after logging. Value: 5
)

// String returns the string representation of the LogLevel.
// This is useful for log output and debugging.
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
		// Handle unknown level values gracefully to avoid panics
		// if an invalid integer is somehow cast to LogLevel.
		return fmt.Sprintf("UNKNOWN_LEVEL(%d)", l)
	}
}

// Logger is an enhanced logging interface for Xylium.
// It provides leveled logging methods and support for structured logging.
type Logger interface {
	// Printf is a basic logging method, primarily for compatibility or simple messages.
	// It's recommended to use leveled methods like Infof for better clarity.
	// Implementations will typically log this at LevelInfo.
	Printf(format string, args ...interface{})

	// Leveled logging methods without string formatting.
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	// Fatal logs the message and then calls os.Exit(1).
	Fatal(args ...interface{})
	// Panic logs the message and then calls panic().
	Panic(args ...interface{})

	// Leveled logging methods with string formatting.
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	// Fatalf logs the formatted message and then calls os.Exit(1).
	Fatalf(format string, args ...interface{})
	// Panicf logs the formatted message and then calls panic().
	Panicf(format string, args ...interface{})

	// WithFields returns a new Logger instance that includes the given fields in all
	// subsequent log entries. This is useful for adding contextual information
	// (e.g., request_id, user_id) to a set of logs.
	// The original logger is not modified, ensuring immutability for shared loggers.
	WithFields(fields M) Logger

	// SetOutput sets the output destination for the logger (e.g., os.Stdout, a file).
	SetOutput(w io.Writer)

	// SetLevel sets the minimum logging level for the logger.
	// Messages with a level lower (more verbose) than this will be discarded.
	SetLevel(level LogLevel)

	// GetLevel returns the current logging level of the logger.
	GetLevel() LogLevel
}

// M is a shorthand for map[string]interface{}, commonly used for structured log fields or JSON responses.
type M map[string]interface{}
