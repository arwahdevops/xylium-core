package xylium

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FormatterType defines the type of output format for the logger.
type FormatterType string

// Supported formatter type constants.
const (
	TextFormatter FormatterType = "text" // TextFormatter produces human-readable, line-based log output.
	JSONFormatter FormatterType = "json" // JSONFormatter produces structured JSON log output, suitable for log aggregation systems.
)

// DefaultTimestampFormat is the standard format used for log entry timestamps.
// It adheres to RFC3339 with milliseconds precision.
const DefaultTimestampFormat = "2006-01-02T15:04:05.000Z07:00"

// ANSI escape codes for colored output in TextFormatter.
// These are used to enhance readability in terminal environments.
const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m" // Unused currently, but available.
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorReset  = "\033[0m"
)

// LogEntry is an internal struct used to gather all log data before formatting.
// This structure is marshalled to JSON when JSONFormatter is used.
type LogEntry struct {
	Timestamp string `json:"timestamp"`        // Timestamp of the log entry, formatted according to DefaultTimestampFormat.
	Level     string `json:"level"`            // String representation of the log level (e.g., "INFO", "DEBUG").
	Message   string `json:"message"`          // The main log message.
	Fields    M      `json:"fields,omitempty"` // Optional structured key-value fields associated with the log entry.
	Caller    string `json:"caller,omitempty"` // Optional caller information (file:line), if enabled.
}

// LoggerConfig defines detailed configuration options for a DefaultLogger instance.
// It allows customization of log level, output format, caller information, color usage,
// and the output writer.
type LoggerConfig struct {
	Level      LogLevel      // Minimum log level to output (e.g., LevelInfo, LevelDebug).
	Formatter  FormatterType // Output format (TextFormatter or JSONFormatter).
	ShowCaller bool          // Whether to include caller (file:line) information in logs.
	UseColor   bool          // Whether to use ANSI colors for TextFormatter (effective if Output is a TTY).
	Output     io.Writer     // Destination for log output (e.g., os.Stdout, a file).
}

// DefaultLoggerConfig provides a new LoggerConfig instance with sensible default settings.
// Default Level: LevelInfo.
// Default Formatter: TextFormatter.
// Default ShowCaller: false.
// Default UseColor: false (will be auto-enabled for TTY in DebugMode if not overridden).
// Default Output: os.Stdout.
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:      LevelInfo,
		Formatter:  TextFormatter,
		ShowCaller: false,
		UseColor:   false,
		Output:     os.Stdout,
	}
}

// DefaultLogger is Xylium's standard implementation of the xylium.Logger interface.
// It provides leveled, structured logging with support for text and JSON formats,
// caller information, and colored output for terminals. It uses a sync.Pool for
// internal buffers to reduce memory allocations.
type DefaultLogger struct {
	mu         sync.RWMutex  // Protects concurrent access to logger configuration fields.
	out        io.Writer     // The output writer for log entries.
	level      LogLevel      // The current minimum log level.
	formatter  FormatterType // The current log output formatter.
	baseFields M             // Fields to include in every log entry from this logger instance.
	showCaller bool          // Whether to include caller information.
	useColor   bool          // Whether to use colored output (for TextFormatter on TTY).
	bufferPool *sync.Pool    // Pool of bytes.Buffer to reduce allocations for formatting.
}

// NewDefaultLoggerWithConfig creates a new DefaultLogger instance with the specified configuration.
// If config.Output is nil, it defaults to os.Stdout.
// Color usage (config.UseColor) is only enabled if the output writer is a TTY.
func NewDefaultLoggerWithConfig(config LoggerConfig) *DefaultLogger {
	if config.Output == nil {
		config.Output = os.Stdout // Default to standard output if none provided.
	}
	dl := &DefaultLogger{
		out:        config.Output,
		level:      config.Level,
		formatter:  config.Formatter,
		baseFields: make(M), // Initialize baseFields, can be populated by WithFields.
		showCaller: config.ShowCaller,
		useColor:   false, // Initial state; EnableColor will set based on TTY and config.UseColor.
		bufferPool: &sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
	if config.UseColor {
		dl.EnableColor(true) // EnableColor method handles the TTY check.
	}
	return dl
}

// NewDefaultLogger creates a new instance of DefaultLogger using the settings
// provided by DefaultLoggerConfig().
func NewDefaultLogger() *DefaultLogger {
	return NewDefaultLoggerWithConfig(DefaultLoggerConfig())
}

// SetOutput sets the output destination for the logger. This method is thread-safe.
// If w is nil, os.Stdout is used as the default.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		l.out = os.Stdout
		return
	}
	l.out = w
}

// SetLevel sets the minimum logging level for this logger instance.
// Messages with a level lower than this will be suppressed. This method is thread-safe.
func (l *DefaultLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current logging level of the logger. This method is thread-safe.
func (l *DefaultLogger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SetFormatter sets the output format for log entries (TextFormatter or JSONFormatter).
// If an invalid formatter type is provided, it defaults to TextFormatter.
// This method is thread-safe.
func (l *DefaultLogger) SetFormatter(formatter FormatterType) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch formatter {
	case TextFormatter, JSONFormatter:
		l.formatter = formatter
	default:
		l.formatter = TextFormatter // Default to text if an unknown type is given.
	}
}

// EnableCaller enables or disables the inclusion of caller information (file and line number)
// in log entries. This method is thread-safe.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the TextFormatter.
// Color is only actually applied if the logger's output writer is a TTY (terminal)
// and `enable` is true. This method is thread-safe.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		l.useColor = isTerminal(l.out) // Check if output is a terminal.
	} else {
		l.useColor = false
	}
}

// isLevelEnabledRLocked checks if a given log level is currently enabled for output.
// This is an internal helper and assumes the caller holds at least a read lock (l.mu.RLock()).
func (l *DefaultLogger) isLevelEnabledRLocked(level LogLevel) bool {
	return level >= l.level
}

// doLog is the core internal logging method that processes and formats log entries.
// It handles level checking, message formatting, field aggregation, caller info,
// and outputting the final log string according to the configured formatter.
// `skipFrames` is used to adjust `runtime.Caller` to get the correct call site.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	l.mu.RLock() // Acquire read lock to safely access logger configuration.
	if !l.isLevelEnabledRLocked(level) {
		l.mu.RUnlock()
		return // Suppress log if level is below configured minimum.
	}
	// Copy current configuration under RLock to avoid holding lock during I/O.
	currentOut := l.out
	currentFormatter := l.formatter
	currentShowCaller := l.showCaller
	currentUseColor := l.useColor
	// Deep copy baseFields to prevent race conditions if WithFields is called concurrently.
	copiedBaseFields := make(M, len(l.baseFields))
	for k, v := range l.baseFields {
		copiedBaseFields[k] = v
	}
	l.mu.RUnlock() // Release read lock.

	// Prepare the log entry.
	entry := LogEntry{
		Timestamp: time.Now().Format(DefaultTimestampFormat),
		Level:     level.String(),
		Message:   message, // Initial message, may be formatted later.
		Fields:    make(M), // Initialize fields for this specific entry.
	}

	// Populate entry.Fields with the copied base fields.
	for k, v := range copiedBaseFields {
		entry.Fields[k] = v
	}

	// Process variadic arguments: separate format arguments from M (fields map).
	var formatArgs []interface{}
	messageContainsFormatSpecifiers := strings.Contains(message, "%")
	hasArgsForFormatting := false

	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok { // If arg is M, merge its fields.
			for k, v := range fieldsMap {
				entry.Fields[k] = v
			}
		} else { // Otherwise, it's an argument for message formatting.
			formatArgs = append(formatArgs, arg)
			hasArgsForFormatting = true
		}
	}

	// Format the main message if arguments were provided for it.
	if hasArgsForFormatting {
		if messageContainsFormatSpecifiers && len(formatArgs) > 0 {
			entry.Message = fmt.Sprintf(message, formatArgs...)
		} else if len(formatArgs) > 0 { // Append if no format specifiers but args exist.
			entry.Message = message + " " + fmt.Sprint(formatArgs...)
		}
		// If !messageContainsFormatSpecifiers and len(formatArgs) == 0, entry.Message remains as is.
	}

	// Add caller information if enabled.
	if currentShowCaller {
		// skipFrames + 1: +1 to account for doLog itself.
		_, file, line, ok := runtime.Caller(skipFrames + 1)
		if ok {
			shortFile := file
			// Get just the filename from the full path.
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				idx = strings.LastIndex(file[:idx], "/") // One more level up for better context in some cases
				if idx != -1 {
					shortFile = file[idx+1:]
				} else {
					shortFile = file[strings.LastIndex(file, "/")+1:]
				}
			}
			entry.Caller = fmt.Sprintf("%s:%d", shortFile, line)
		}
	}

	// Get a buffer from the pool for formatting the log entry.
	buffer := l.bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()                 // Clear buffer for reuse.
	defer l.bufferPool.Put(buffer) // Return buffer to pool when done.

	// Format the entry based on the configured formatter.
	switch currentFormatter {
	case JSONFormatter:
		jsonData, err := json.Marshal(entry)
		if err != nil { // Handle error marshalling the original log entry
			timestampFallback := time.Now().Format(DefaultTimestampFormat)
			// Create a structured fallback log entry to be marshalled to JSON.
			fallbackEntry := struct {
				Timestamp       string `json:"timestamp"`
				Level           string `json:"level"`
				Error           string `json:"error"`
				OriginalMessage string `json:"original_message,omitempty"` // Include original message for context.
			}{
				Timestamp:       timestampFallback,
				Level:           "ERROR", // Log the marshalling failure as an ERROR.
				Error:           "Failed to marshal original log entry to JSON.",
				OriginalMessage: entry.Message,
			}
			jsonFallbackData, fallbackErr := json.Marshal(fallbackEntry)
			if fallbackErr != nil {
				// Extremely unlikely, but if marshalling the fallback also fails, write a plain text error.
				fmt.Fprintf(buffer, "{\"timestamp\":\"%s\",\"level\":\"ERROR\",\"error\":\"CRITICAL: Failed to marshal even fallback log entry. Original log marshal error: %v\"}\n",
					timestampFallback, err)
			} else {
				buffer.Write(jsonFallbackData)
				buffer.WriteString("\n") // Ensure newline for JSON stream.
			}
			// Log the marshalling error itself to standard error for system visibility.
			fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] JSON Marshal Error for log entry: %v. Original Entry (approx): %+v. Original Message: %s\n", err, entry, entry.Message)
		} else {
			buffer.Write(jsonData)
			buffer.WriteString("\n") // Ensure newline for JSON stream.
		}
	case TextFormatter:
		fallthrough // TextFormatter is the default.
	default:
		buffer.WriteString(entry.Timestamp)
		buffer.WriteString(" ")
		levelStr := entry.Level
		if currentUseColor { // Apply color to level string if enabled.
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
		buffer.WriteString(fmt.Sprintf("[%s]", levelStr))
		buffer.WriteString(" ")
		if entry.Caller != "" { // Add caller info if present.
			callerStr := entry.Caller
			if currentUseColor {
				callerStr = colorGray + callerStr + colorReset
			}
			buffer.WriteString(fmt.Sprintf("<%s>", callerStr))
			buffer.WriteString(" ")
		}
		buffer.WriteString(entry.Message) // The main log message.
		if len(entry.Fields) > 0 {        // Add structured fields if any.
			buffer.WriteString(" ")
			fieldBytes, err := json.Marshal(entry.Fields) // Marshal fields to JSON.
			if err != nil {
				buffer.WriteString(fmt.Sprintf("(error marshalling fields: %v)", err))
			} else {
				fieldStr := string(fieldBytes)
				if currentUseColor {
					fieldStr = colorPurple + fieldStr + colorReset
				}
				buffer.WriteString(fieldStr)
			}
		}
		buffer.WriteString("\n") // Ensure newline.
	}

	// Write the formatted log entry to the output writer.
	// Lock for I/O operation as `currentOut` might be shared (e.g., os.Stdout).
	l.mu.Lock()
	var writeError error
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		// If writing to primary output fails, try to write an error message to os.Stderr.
		fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
		writeError = err // Store the error for Fatal/Panic handling.
	}
	l.mu.Unlock()

	// Handle Fatal and Panic levels after attempting to log.
	if level == LevelFatal {
		if writeError != nil { // If logging failed, print to stderr before exiting.
			fmt.Fprintf(os.Stderr, "FATAL: %s\n", entry.Message)
		}
		os.Exit(1)
	} else if level == LevelPanic {
		if writeError != nil { // If logging failed, print to stderr before panicking.
			fmt.Fprintf(os.Stderr, "PANIC: %s\n", entry.Message)
		}
		panic(entry.Message)
	}
}

// Printf logs a message at LevelInfo using a format string and arguments.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Printf(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...) // skipFrames = 2 to get caller of Printf.
}

// Debug logs a message at LevelDebug. Arguments are handled by fmt.Sprint.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Debug(args ...interface{}) {
	l.doLog(LevelDebug, 2, fmt.Sprint(args...))
}

// Info logs a message at LevelInfo. Arguments are handled by fmt.Sprint.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Info(args ...interface{}) {
	l.doLog(LevelInfo, 2, fmt.Sprint(args...))
}

// Warn logs a message at LevelWarn. Arguments are handled by fmt.Sprint.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Warn(args ...interface{}) {
	l.doLog(LevelWarn, 2, fmt.Sprint(args...))
}

// Error logs a message at LevelError. Arguments are handled by fmt.Sprint.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Error(args ...interface{}) {
	l.doLog(LevelError, 2, fmt.Sprint(args...))
}

// Fatal logs a message at LevelFatal, then calls os.Exit(1).
// Arguments are handled by fmt.Sprint. Implements the xylium.Logger interface.
func (l *DefaultLogger) Fatal(args ...interface{}) {
	l.doLog(LevelFatal, 2, fmt.Sprint(args...))
}

// Panic logs a message at LevelPanic, then calls panic().
// Arguments are handled by fmt.Sprint. Implements the xylium.Logger interface.
func (l *DefaultLogger) Panic(args ...interface{}) {
	l.doLog(LevelPanic, 2, fmt.Sprint(args...))
}

// Debugf logs a formatted message at LevelDebug.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Debugf(format string, args ...interface{}) {
	l.doLog(LevelDebug, 2, format, args...)
}

// Infof logs a formatted message at LevelInfo.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Infof(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}

// Warnf logs a formatted message at LevelWarn.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Warnf(format string, args ...interface{}) {
	l.doLog(LevelWarn, 2, format, args...)
}

// Errorf logs a formatted message at LevelError.
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Errorf(format string, args ...interface{}) {
	l.doLog(LevelError, 2, format, args...)
}

// Fatalf logs a formatted message at LevelFatal, then calls os.Exit(1).
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Fatalf(format string, args ...interface{}) {
	l.doLog(LevelFatal, 2, format, args...)
}

// Panicf logs a formatted message at LevelPanic, then calls panic().
// Implements the xylium.Logger interface.
func (l *DefaultLogger) Panicf(format string, args ...interface{}) {
	l.doLog(LevelPanic, 2, format, args...)
}

// WithFields creates a new DefaultLogger instance that includes the given `fields`
// as base fields for all subsequent log entries made with the new logger.
// The new logger inherits configuration (output, level, formatter, etc.) from the
// original logger and shares the same underlying bufferPool for efficiency.
// This method is thread-safe. Implements the xylium.Logger interface.
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock() // Read lock to safely access current logger's configuration.

	// Create a new logger instance, inheriting most configuration.
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		showCaller: l.showCaller,
		useColor:   l.useColor,
		bufferPool: l.bufferPool, // Share the buffer pool.
	}

	// Create a new baseFields map for the new logger.
	// It starts with a copy of the original logger's baseFields,
	// then adds/overwrites with the new `fields`.
	combinedBaseFields := make(M, len(l.baseFields)+len(fields))
	for k, v := range l.baseFields { // Copy existing base fields.
		combinedBaseFields[k] = v
	}
	l.mu.RUnlock() // Release lock after reading original logger's state.

	// Add/overwrite with the new fields provided to WithFields.
	for k, v := range fields {
		combinedBaseFields[k] = v
	}
	newLogger.baseFields = combinedBaseFields

	return newLogger
}

// isTerminal checks if the given io.Writer is a character device, which typically
// indicates it's a terminal capable of displaying ANSI color codes.
// This is used by EnableColor to determine if colored output should be activated.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return false // If stat fails, assume not a terminal.
		}
		// Check if the file mode includes os.ModeCharDevice.
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	return false // Not an *os.File, assume not a terminal.
}
