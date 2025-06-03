package xylium

import (
	"bytes"         // For pooling and using bytes.Buffer for log entry formatting.
	"encoding/json" // For marshalling log entries to JSON and structured fields.
	"fmt"           // For formatting log messages and error strings.
	"io"            // For io.Writer interface used for log output.
	"os"            // For os.Stdout as default log output and os.Stderr for critical errors.
	"runtime"       // For runtime.Caller to get file and line number for logs.
	"strings"       // For string manipulation, e.g., in caller info formatting.
	"sync"          // For sync.RWMutex to protect logger configuration and sync.Pool.
	"time"          // For timestamping log entries.
)

// FormatterType defines the type of output format for the `DefaultLogger`.
// It determines how log entries are structured and presented in the output.
type FormatterType string

// Supported formatter type constants for `DefaultLogger`.
const (
	// TextFormatter produces human-readable, line-based log output.
	// It typically includes a timestamp, level, caller (optional), message, and
	// structured fields (marshalled as a JSON string). It supports colored output
	// when writing to a TTY (terminal).
	TextFormatter FormatterType = "text"
	// JSONFormatter produces structured JSON log output. Each log entry is a
	// single JSON object containing fields like timestamp, level, message,
	// structured fields, and caller (optional). This format is ideal for
	// log aggregation and analysis systems (e.g., ELK stack, Splunk, CloudWatch Logs).
	JSONFormatter FormatterType = "json"
)

// DefaultTimestampFormat is the standard string format used by `DefaultLogger`
// for timestamping log entries. It adheres to RFC3339 with millisecond precision
// and includes the timezone offset.
// Example: "2006-01-02T15:04:05.000Z07:00"
const DefaultTimestampFormat = "2006-01-02T15:04:05.000Z07:00"

// ANSI escape codes for colored output in TextFormatter.
// These are used to enhance readability in terminal environments when `UseColor` is true
// and the output is a TTY.
const (
	colorRed    = "\033[31m" // Typically used for ERROR, FATAL, PANIC levels.
	colorGreen  = "\033[32m" // Typically used for INFO level.
	colorYellow = "\033[33m" // Typically used for WARN level.
	colorBlue   = "\033[34m" // Available, currently unused by default.
	colorPurple = "\033[35m" // Typically used for structured fields in TextFormatter.
	colorCyan   = "\033[36m" // Typically used for DEBUG level.
	colorGray   = "\033[90m" // Typically used for caller information in TextFormatter.
	colorReset  = "\033[0m"  // Resets any active color.
)

// LogEntry is an internal struct used by `DefaultLogger` to aggregate all data
// for a single log event before it is formatted and written to the output.
// When `JSONFormatter` is used, an instance of `LogEntry` is marshalled to JSON.
type LogEntry struct {
	// Timestamp is the time the log entry was created, formatted as a string
	// according to `DefaultTimestampFormat`.
	Timestamp string `json:"timestamp"`
	// Level is the string representation of the log level (e.g., "INFO", "DEBUG", "ERROR").
	Level string `json:"level"`
	// Message is the primary human-readable log message.
	Message string `json:"message"`
	// Fields contains optional structured key-value pairs associated with this log entry.
	// These are added via `logger.WithFields()` or by passing a `xylium.M` map
	// as an argument to logging methods. It is omitted from JSON output if empty.
	Fields M `json:"fields,omitempty"`
	// Caller contains information about the source code location (file and line number)
	// where the log call was made. It is included if `LoggerConfig.ShowCaller` is true.
	// It is omitted from JSON output if empty or not enabled.
	Caller string `json:"caller,omitempty"`
}

// LoggerConfig defines the detailed configuration options for a `DefaultLogger` instance.
// It allows fine-grained control over various aspects of logging behavior.
// This struct is typically provided via `ServerConfig.LoggerConfig` when creating a
// Xylium router if a `DefaultLogger` is to be used.
type LoggerConfig struct {
	// Level specifies the minimum `LogLevel` that this logger will output.
	// Messages with a level lower than this will be suppressed.
	// Example: `xylium.LevelInfo`, `xylium.LevelDebug`.
	Level LogLevel
	// Formatter determines the output format of log entries.
	// It can be `xylium.TextFormatter` or `xylium.JSONFormatter`.
	Formatter FormatterType
	// ShowCaller, if true, includes the source file name and line number of the
	// log call in each log entry. This can be useful for debugging but may have
	// a slight performance overhead.
	ShowCaller bool
	// UseColor, if true, enables ANSI color codes for `TextFormatter` output.
	// Colors are only applied if the `Output` writer is a TTY (terminal) and
	// `UseColor` is true. It has no effect with `JSONFormatter`.
	UseColor bool
	// Output is the `io.Writer` to which log entries will be written.
	// Common values are `os.Stdout`, `os.Stderr`, or a file opened for writing.
	// If nil, `DefaultLogger` will default to `os.Stdout`.
	Output io.Writer
}

// DefaultLoggerConfig returns a new `LoggerConfig` instance initialized with
// sensible default settings for `DefaultLogger`. These defaults provide a
// balanced starting point for general application logging.
//
// Default values:
//   - Level: `xylium.LevelInfo` (logs Info, Warn, Error, Fatal, Panic).
//   - Formatter: `xylium.TextFormatter` (human-readable text).
//   - ShowCaller: `false` (caller information is not included by default).
//   - UseColor: `false` (colors disabled by default; Xylium's router initialization
//     may enable it for `DebugMode` if output is a TTY and not overridden here).
//   - Output: `os.Stdout`.
//
// These defaults can be overridden by Xylium's operating mode settings or by
// explicitly providing a `LoggerConfig` when creating a router or logger.
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:      LevelInfo,
		Formatter:  TextFormatter,
		ShowCaller: false,
		UseColor:   false, // Will be auto-adjusted by Xylium based on mode/TTY if not explicitly set by user.
		Output:     os.Stdout,
	}
}

// DefaultLogger is Xylium's standard, built-in implementation of the `xylium.Logger` interface.
// It provides flexible and efficient logging capabilities, including:
//   - Leveled logging (Debug, Info, Warn, Error, Fatal, Panic).
//   - Structured logging with key-value fields (`WithFields` or passing `xylium.M`).
//   - Support for both human-readable text (`TextFormatter`) and structured JSON (`JSONFormatter`) output.
//   - Optional inclusion of caller information (file and line number).
//   - Optional colored output for `TextFormatter` when writing to a terminal (TTY).
//   - Thread-safe operations for concurrent logging from multiple goroutines.
//   - Use of a `sync.Pool` for internal `bytes.Buffer` instances to reduce memory allocations
//     during log entry formatting.
type DefaultLogger struct {
	mu         sync.RWMutex  // Protects concurrent access to logger configuration fields (out, level, etc.).
	out        io.Writer     // The output writer where log entries are sent.
	level      LogLevel      // The current minimum log level for this logger instance.
	formatter  FormatterType // The current log output formatter (TextFormatter or JSONFormatter).
	baseFields M             // A map of fields to include in every log entry generated by this logger instance.
	showCaller bool          // Flag indicating whether to include caller information.
	useColor   bool          // Flag indicating whether to use colored output (for TextFormatter on TTY).
	bufferPool *sync.Pool    // Pool of `*bytes.Buffer` used for formatting log entries to reduce allocations.
}

// NewDefaultLoggerWithConfig creates a new `DefaultLogger` instance configured with the
// settings provided in the `config` argument.
//
// If `config.Output` is nil, the logger will default to writing to `os.Stdout`.
// Color usage (`config.UseColor`) is only effectively enabled if `config.UseColor` is true
// AND the `config.Output` writer is determined to be a TTY (terminal).
func NewDefaultLoggerWithConfig(config LoggerConfig) *DefaultLogger {
	if config.Output == nil {
		config.Output = os.Stdout // Default to standard output if no writer is provided.
	}
	dl := &DefaultLogger{
		out:        config.Output,
		level:      config.Level,
		formatter:  config.Formatter,
		baseFields: make(M), // Initialize baseFields; can be populated later via WithFields.
		showCaller: config.ShowCaller,
		useColor:   false, // Initial state; EnableColor will set based on TTY and config.UseColor.
		bufferPool: &sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
	// Attempt to enable color based on config.UseColor and TTY detection.
	// The EnableColor method handles the TTY check internally.
	dl.EnableColor(config.UseColor)
	return dl
}

// NewDefaultLogger creates a new instance of `DefaultLogger` using the default settings
// returned by `DefaultLoggerConfig()`.
// This provides a quick way to get a logger with standard behavior.
// For custom configuration, use `NewDefaultLoggerWithConfig`.
func NewDefaultLogger() *DefaultLogger {
	return NewDefaultLoggerWithConfig(DefaultLoggerConfig())
}

// SetOutput sets the output destination `io.Writer` for the logger.
// All subsequent log entries will be written to this writer.
// If `w` is nil, `os.Stdout` is used as the default output.
// This method is thread-safe.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		l.out = os.Stdout
		return
	}
	l.out = w
	// Re-evaluate color usage if output changes, as TTY status might change.
	// This assumes that if color was desired (l.useColor was true from a previous EnableColor(true) call),
	// it should be re-enabled if the new output is a TTY.
	// If l.useColor was false (e.g., EnableColor(false) was called), it remains false.
	if l.useColor { // Only re-check TTY if color was previously meant to be on.
		l.useColor = isTerminal(l.out)
	}
}

// SetLevel sets the minimum logging `LogLevel` for this logger instance.
// Log messages with a severity level lower than the specified `level` will be suppressed.
// For example, if `level` is `LevelInfo`, `Debug` messages will not be output.
// This method is thread-safe.
func (l *DefaultLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current minimum logging `LogLevel` of this logger instance.
// This method is thread-safe.
func (l *DefaultLogger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SetFormatter sets the output format for log entries generated by this logger.
// Valid options are `xylium.TextFormatter` or `xylium.JSONFormatter`.
// If an invalid `formatter` type is provided, it defaults to `TextFormatter`.
// This method is thread-safe.
func (l *DefaultLogger) SetFormatter(formatter FormatterType) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch formatter {
	case TextFormatter, JSONFormatter:
		l.formatter = formatter
	default:
		// Fallback to TextFormatter if an unknown formatter type is given.
		// Optionally, log a warning here if an invalid formatter is provided.
		l.formatter = TextFormatter
	}
}

// EnableCaller enables or disables the inclusion of caller information (source file name
// and line number) in log entries. When enabled, finding the origin of a log
// message is easier, but it incurs a small performance cost due to `runtime.Caller`.
// This method is thread-safe.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the `TextFormatter`.
// If `enable` is true, color will be used if the logger's output `io.Writer`
// is a TTY (terminal). If `enable` is false, or if the output is not a TTY,
// color will be disabled. This setting has no effect on `JSONFormatter`.
// This method is thread-safe.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		// Color is only truly active if enabled AND output is a terminal.
		l.useColor = isTerminal(l.out)
	} else {
		l.useColor = false
	}
}

// isLevelEnabledRLocked is an internal helper that checks if a given `LogLevel`
// is currently enabled for output by this logger instance.
// It assumes the caller already holds at least a read lock (`l.mu.RLock()`) on the logger.
func (l *DefaultLogger) isLevelEnabledRLocked(level LogLevel) bool {
	return level >= l.level
}

// doLog is the core internal method responsible for processing and formatting log entries.
// It performs the following steps:
//  1. Checks if the given `level` is enabled based on the logger's current minimum level.
//  2. Acquires a `bytes.Buffer` from a `sync.Pool` for efficient formatting.
//  3. Constructs a `LogEntry` struct with timestamp, level, and the initial message.
//  4. Merges any `baseFields` (from `WithFields`) into the `LogEntry.Fields`.
//  5. Processes variadic `args`:
//     - If an argument is of type `xylium.M`, its key-value pairs are merged into `LogEntry.Fields`.
//     - Other arguments are treated as formatting arguments for the `message` string (if it contains format specifiers).
//  6. If `showCaller` is enabled, retrieves and formats caller information (file:line) and adds it to `LogEntry.Caller`.
//  7. Formats the complete `LogEntry` into the `bytes.Buffer` according to the configured `formatter` (`TextFormatter` or `JSONFormatter`).
//     - `TextFormatter` applies colors if `useColor` is true and output is a TTY.
//     - `JSONFormatter` marshals the `LogEntry` to a JSON string.
//  8. Writes the formatted log entry from the buffer to the logger's `out` (output writer).
//  9. Handles `LevelFatal` (calls `os.Exit(1)`) and `LevelPanic` (calls `panic()`) after logging.
//
// 10. Returns the buffer to the pool.
//
// Parameters:
//   - `level` (LogLevel): The severity level of this log message.
//   - `skipFrames` (int): The number of stack frames to skip when determining the caller
//     information via `runtime.Caller`. This is used to ensure the reported caller is
//     the line of code that invoked the public logging method (e.g., `Info`, `Errorf`),
//     not an internal helper like `doLog`.
//   - `message` (string): The log message, which can be a format string if `args` are provided for formatting.
//   - `args` (...interface{}): Optional arguments. These can include values for formatting `message`,
//     and/or `xylium.M` maps for adding structured fields to the log entry.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	l.mu.RLock() // Acquire read lock to safely access logger configuration (level, formatter, etc.).
	if !l.isLevelEnabledRLocked(level) {
		l.mu.RUnlock() // Release lock if level is suppressed.
		return
	}
	// Copy current configuration values while under RLock to avoid holding the lock
	// during potentially blocking I/O operations or complex formatting.
	currentOut := l.out
	currentFormatter := l.formatter
	currentShowCaller := l.showCaller
	currentUseColor := l.useColor
	// Deep copy baseFields to prevent race conditions if WithFields is called concurrently
	// while this log operation is in progress.
	copiedBaseFields := make(M, len(l.baseFields))
	for k, v := range l.baseFields {
		copiedBaseFields[k] = v
	}
	l.mu.RUnlock() // Release read lock.

	// Prepare the LogEntry struct that will hold all data for this log event.
	entry := LogEntry{
		Timestamp: time.Now().Format(DefaultTimestampFormat),
		Level:     level.String(),
		Message:   message, // Initial message; may be formatted later if args are for formatting.
		Fields:    make(M), // Initialize Fields map for this specific entry.
	}

	// Populate entry.Fields with the copied base fields from the logger instance.
	for k, v := range copiedBaseFields {
		entry.Fields[k] = v
	}

	// Process variadic arguments (`args`):
	// Separate arguments intended for message formatting from `xylium.M` maps (for structured fields).
	var formatArgs []interface{}
	messageContainsFormatSpecifiers := strings.Contains(message, "%") // Check if message is a format string.
	hasArgsForFormatting := false

	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok {
			// If an argument is of type xylium.M, merge its key-value pairs into entry.Fields.
			// Fields from `arg.(M)` will overwrite any existing fields in `entry.Fields` (including baseFields)
			// if they have the same key. This is typical structured logging behavior.
			for k, v := range fieldsMap {
				entry.Fields[k] = v
			}
		} else {
			// Otherwise, the argument is considered to be for formatting the main `message` string.
			formatArgs = append(formatArgs, arg)
			hasArgsForFormatting = true
		}
	}

	// Format the main `entry.Message` if formatting arguments were provided.
	if hasArgsForFormatting {
		if messageContainsFormatSpecifiers && len(formatArgs) > 0 {
			// If message contains '%' and we have args, format it.
			entry.Message = fmt.Sprintf(message, formatArgs...)
		} else if len(formatArgs) > 0 {
			// If no '%' in message, but args exist, append them (space-separated).
			// This matches behavior of `fmt.Sprint` for multiple args.
			entry.Message = message + " " + fmt.Sprint(formatArgs...)
		}
		// If `!messageContainsFormatSpecifiers` and `len(formatArgs) == 0`, `entry.Message` remains as originally passed.
	}

	// Add caller information (file:line) to `entry.Caller` if `currentShowCaller` is enabled.
	if currentShowCaller {
		// `skipFrames + 1`: +1 to account for `doLog` itself in the call stack.
		// The `skipFrames` argument should be set by the public logging methods (e.g., Info, Errorf)
		// to point to their own call site.
		_, file, line, ok := runtime.Caller(skipFrames + 1)
		if ok {
			// Format the caller string. Attempt to shorten the file path for readability.
			shortFile := file
			// Example: "/path/to/project/module/file.go" -> "module/file.go" or "file.go"
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				// Try to get one directory level up for more context, e.g., "module/file.go".
				if prevIdx := strings.LastIndex(file[:idx], "/"); prevIdx != -1 {
					shortFile = file[prevIdx+1:]
				} else {
					// If no second-to-last slash, just take the filename.
					shortFile = file[idx+1:]
				}
			}
			entry.Caller = fmt.Sprintf("%s:%d", shortFile, line)
		}
	}

	// Acquire a buffer from the pool for formatting the log entry.
	buffer := l.bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()                 // Ensure the buffer is empty for reuse.
	defer l.bufferPool.Put(buffer) // Return the buffer to the pool when `doLog` exits.

	// Format the `LogEntry` into the `buffer` based on the `currentFormatter`.
	switch currentFormatter {
	case JSONFormatter:
		// Marshal the entire LogEntry struct to JSON.
		jsonData, err := json.Marshal(entry)
		if err != nil {
			// Critical: Failed to marshal the log entry itself to JSON.
			// Log a fallback error message (also in JSON if possible, or plain text).
			timestampFallback := time.Now().Format(DefaultTimestampFormat)
			fallbackEntry := struct { // Anonymous struct for fallback JSON.
				Timestamp       string `json:"timestamp"`
				Level           string `json:"level"`
				Error           string `json:"error"`
				OriginalMessage string `json:"original_message,omitempty"`
			}{
				Timestamp:       timestampFallback,
				Level:           "ERROR", // Log this marshalling failure as an ERROR.
				Error:           "Internal logger error: Failed to marshal original log entry to JSON.",
				OriginalMessage: entry.Message, // Include original message for context.
			}
			jsonFallbackData, fallbackErr := json.Marshal(fallbackEntry)
			if fallbackErr != nil {
				// Extremely unlikely: if marshalling the fallback also fails, write a plain text error.
				// This indicates a severe issue, possibly with the `encoding/json` package or memory.
				fmt.Fprintf(buffer, `{"timestamp":"%s","level":"ERROR","error":"CRITICAL: Failed to marshal even fallback log entry. Original log marshal error: %v"}`+"\n",
					timestampFallback, err)
			} else {
				buffer.Write(jsonFallbackData)
				buffer.WriteString("\n") // Ensure JSON entries are newline-terminated.
			}
			// Also log the original marshalling error to standard error for system visibility,
			// as the primary log output might be compromised.
			fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] JSON Marshal Error for log entry: %v. Original Message: %s\n", err, entry.Message)
		} else {
			// Successfully marshalled the original LogEntry.
			buffer.Write(jsonData)
			buffer.WriteString("\n") // Ensure JSON entries are newline-terminated for stream processing.
		}
	case TextFormatter: // TextFormatter is also the default if currentFormatter is invalid.
		fallthrough
	default:
		// Format as human-readable text.
		buffer.WriteString(entry.Timestamp)
		buffer.WriteString(" ") // Separator.

		levelStr := entry.Level
		if currentUseColor { // Apply ANSI color to the level string if enabled.
			switch level {
			case LevelDebug:
				levelStr = colorCyan + levelStr + colorReset
			case LevelInfo:
				levelStr = colorGreen + levelStr + colorReset
			case LevelWarn:
				levelStr = colorYellow + levelStr + colorReset
			case LevelError, LevelFatal, LevelPanic:
				levelStr = colorRed + levelStr + colorReset
			}
		}
		buffer.WriteString(fmt.Sprintf("[%s]", levelStr)) // Enclose level in brackets.
		buffer.WriteString(" ")

		if entry.Caller != "" { // Add caller information if present.
			callerStr := entry.Caller
			if currentUseColor {
				callerStr = colorGray + callerStr + colorReset // Color for caller info.
			}
			buffer.WriteString(fmt.Sprintf("<%s>", callerStr)) // Enclose caller in angle brackets.
			buffer.WriteString(" ")
		}

		buffer.WriteString(entry.Message) // The main log message.

		if len(entry.Fields) > 0 { // Add structured fields if any.
			buffer.WriteString(" ") // Separator for fields.
			// Marshal the `entry.Fields` map to a JSON string for text output.
			fieldBytes, err := json.Marshal(entry.Fields)
			if err != nil {
				// If marshalling fields fails, include an error message in the log.
				buffer.WriteString(fmt.Sprintf("(error marshalling fields: %v)", err))
			} else {
				fieldStr := string(fieldBytes)
				if currentUseColor {
					fieldStr = colorPurple + fieldStr + colorReset // Color for fields JSON.
				}
				buffer.WriteString(fieldStr)
			}
		}
		buffer.WriteString("\n") // Ensure log entry is newline-terminated.
	}

	// Write the formatted log entry from the buffer to the configured output writer.
	// This I/O operation is protected by a lock on the logger instance (`l.mu`)
	// to ensure thread-safety if multiple goroutines log to the same `DefaultLogger`
	// instance that shares an output writer (e.g., os.Stdout).
	l.mu.Lock()          // Acquire lock for writing to `currentOut`.
	var writeError error // To store error from writing, for Fatal/Panic.
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		// If writing to the primary output fails (e.g., disk full, broken pipe),
		// attempt to write an error message to `os.Stderr` for visibility.
		fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
		writeError = err // Store the error for potential use by Fatal/Panic.
	}
	l.mu.Unlock() // Release lock.

	// Handle `LevelFatal` and `LevelPanic` after attempting to log the message.
	if level == LevelFatal {
		if writeError != nil {
			// If logging the fatal message failed, ensure the message is printed to os.Stderr before exiting.
			fmt.Fprintf(os.Stderr, "FATAL: %s\n", entry.Message)
		}
		os.Exit(1) // Terminate the application.
	} else if level == LevelPanic {
		if writeError != nil {
			// If logging the panic message failed, print to os.Stderr before panicking.
			fmt.Fprintf(os.Stderr, "PANIC: %s\n", entry.Message)
		}
		panic(entry.Message) // Trigger a panic.
	}
}

// Printf logs a message at `LevelInfo` using `fmt.Sprintf` style formatting.
// Implements the `xylium.Logger` interface.
// The `skipFrames` argument for `doLog` is 2 to correctly capture the caller of `Printf`.
func (l *DefaultLogger) Printf(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}

// Debug logs a message at `LevelDebug`. Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Debug(args ...interface{}) {
	// `fmt.Sprint` handles multiple arguments by space-separating them.
	l.doLog(LevelDebug, 2, fmt.Sprint(args...))
}

// Info logs a message at `LevelInfo`. Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Info(args ...interface{}) {
	l.doLog(LevelInfo, 2, fmt.Sprint(args...))
}

// Warn logs a message at `LevelWarn`. Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Warn(args ...interface{}) {
	l.doLog(LevelWarn, 2, fmt.Sprint(args...))
}

// Error logs a message at `LevelError`. Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Error(args ...interface{}) {
	l.doLog(LevelError, 2, fmt.Sprint(args...))
}

// Fatal logs a message at `LevelFatal`, then calls `os.Exit(1)` to terminate
// the application. Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Fatal(args ...interface{}) {
	l.doLog(LevelFatal, 2, fmt.Sprint(args...))
}

// Panic logs a message at `LevelPanic`, then calls `panic()` with the message.
// Arguments are handled by `fmt.Sprint`.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Panic(args ...interface{}) {
	l.doLog(LevelPanic, 2, fmt.Sprint(args...))
}

// Debugf logs a formatted message at `LevelDebug` using `fmt.Sprintf` style.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Debugf(format string, args ...interface{}) {
	l.doLog(LevelDebug, 2, format, args...)
}

// Infof logs a formatted message at `LevelInfo` using `fmt.Sprintf` style.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Infof(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}

// Warnf logs a formatted message at `LevelWarn` using `fmt.Sprintf` style.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Warnf(format string, args ...interface{}) {
	l.doLog(LevelWarn, 2, format, args...)
}

// Errorf logs a formatted message at `LevelError` using `fmt.Sprintf` style.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Errorf(format string, args ...interface{}) {
	l.doLog(LevelError, 2, format, args...)
}

// Fatalf logs a formatted message at `LevelFatal` using `fmt.Sprintf` style,
// then calls `os.Exit(1)` to terminate the application.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Fatalf(format string, args ...interface{}) {
	l.doLog(LevelFatal, 2, format, args...)
}

// Panicf logs a formatted message at `LevelPanic` using `fmt.Sprintf` style,
// then calls `panic()` with the formatted message.
// Implements the `xylium.Logger` interface.
func (l *DefaultLogger) Panicf(format string, args ...interface{}) {
	l.doLog(LevelPanic, 2, format, args...)
}

// WithFields creates a new `DefaultLogger` instance that includes the given `fields`
// (`xylium.M` map) as base_fields for all subsequent log entries made with the new logger.
// These `fields` are merged with any `baseFields` already present in the original logger;
// if keys conflict, the new `fields` take precedence.
//
// The new logger instance inherits its configuration (output writer, level, formatter,
// caller settings, color settings) from the original logger (`l`). It also shares the
// same underlying `bufferPool` for efficiency.
//
// This method is thread-safe and allows for creating context-specific loggers
// without modifying the original logger instance. It implements the `xylium.Logger` interface.
//
// Example:
//
//	requestLogger := app.Logger().WithFields(xylium.M{"request_id": "xyz123"})
//	requestLogger.Info("Processing request.") // Log entry will include "request_id":"xyz123"
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock() // Acquire read lock to safely access the original logger's configuration.

	// Create a new logger instance. It inherits most configuration settings
	// (output, level, formatter, showCaller, useColor) from the original logger `l`.
	// It also shares the same bufferPool to maintain efficiency.
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		showCaller: l.showCaller,
		useColor:   l.useColor,
		bufferPool: l.bufferPool, // Share the buffer pool with the parent.
	}

	// Create a new `baseFields` map for the `newLogger`.
	// The size is estimated for efficiency. It starts with a copy of the original
	// logger's `baseFields`, and then the new `fields` are merged in.
	combinedBaseFields := make(M, len(l.baseFields)+len(fields))
	for k, v := range l.baseFields { // Copy existing base fields from the parent logger.
		combinedBaseFields[k] = v
	}
	l.mu.RUnlock() // Release read lock after copying necessary state from the parent.

	// Add or overwrite fields from the `fields` argument into `combinedBaseFields`.
	// Fields from the `fields` argument take precedence over existing base fields if keys conflict.
	for k, v := range fields {
		combinedBaseFields[k] = v
	}
	newLogger.baseFields = combinedBaseFields // Assign the merged fields to the new logger.

	return newLogger
}

// isTerminal checks if the given `io.Writer` (`w`) is a character device,
// which typically indicates that it's a terminal (TTY) capable of displaying
// ANSI color codes. This function is used by `EnableColor` to determine if
// colored output should actually be activated for `TextFormatter`.
//
// It specifically checks if `w` is an `*os.File` and then inspects its file mode.
// If `w` is not an `*os.File`, or if `f.Stat()` fails, it conservatively returns `false`.
func isTerminal(w io.Writer) bool {
	// Check if the writer is an *os.File type.
	if f, ok := w.(*os.File); ok {
		// Get file statistics.
		stat, err := f.Stat()
		if err != nil {
			// If f.Stat() fails (e.g., file closed, permissions), assume not a terminal.
			return false
		}
		// Check if the file mode includes the os.ModeCharDevice bit.
		// For terminals, (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice will be true.
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	// If w is not an *os.File (e.g., a bytes.Buffer, network connection),
	// assume it's not a terminal capable of ANSI colors.
	return false
}
