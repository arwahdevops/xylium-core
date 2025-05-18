// src/xylium/default_logger.go
package xylium

import (
	"bytes"        // For pooling and building log strings
	"encoding/json" // For JSON formatting and field marshalling
	"fmt"           // For string formatting
	"io"            // For io.Writer
	"os"            // For os.Stdout, os.Stderr, os.File, os.Exit, os.ModeCharDevice
	"runtime"       // For runtime.Caller
	"strings"       // For string manipulation (e.g., file path splitting)
	"sync"          // For sync.RWMutex and sync.Pool
	"time"          // For timestamps
)

// FormatterType defines the type of output format for the logger.
type FormatterType string

// Supported formatter type constants.
const (
	TextFormatter FormatterType = "text" // Human-readable text format.
	JSONFormatter FormatterType = "json" // JSON format, suitable for structured logging systems.
)

// DefaultTimestampFormat is the standard format for log timestamps.
const DefaultTimestampFormat = "2006-01-02T15:04:05.000Z07:00" // ISO8601 with timezone.

// ANSI escape codes for colored output in TextFormatter.
const (
	colorRed    = "\033[31m" // Typically for Error, Fatal, Panic
	colorGreen  = "\033[32m" // Typically for Info
	colorYellow = "\033[33m" // Typically for Warn
	colorBlue   = "\033[34m" // Can be used for other purposes or custom fields
	colorPurple = "\033[35m" // Often used for fields in text format
	colorCyan   = "\033[36m" // Typically for Debug
	colorGray   = "\033[90m" // Often used for less prominent info like caller
	colorReset  = "\033[0m" // Resets all color attributes
)

// LogEntry is an internal struct used to gather all log data before formatting.
// This makes it easier to add new structured fields in the future.
type LogEntry struct {
	Timestamp string `json:"timestamp"`            // Time the log entry was created.
	Level     string `json:"level"`                // Log level (e.g., "INFO", "DEBUG").
	Message   string `json:"message"`              // The main log message.
	Fields    M      `json:"fields,omitempty"`     // Custom structured fields (uses xylium.M).
	Caller    string `json:"caller,omitempty"`     // Caller information (e.g., "file.go:line").
	// Additional global fields like 'service_name' could be added here
	// if they were part of a global logger configuration.
	// However, 'baseFields' in DefaultLogger handles instance-specific global fields.
}

// DefaultLogger is Xylium's default implementation of the xylium.Logger interface.
// It supports leveled logging, text and JSON formatting, structured fields,
// caller information, and colored output for terminals.
type DefaultLogger struct {
	mu         sync.RWMutex // Protects concurrent access to logger's configuration.
	out        io.Writer    // Output destination for log entries.
	level      LogLevel     // Minimum level of logs to be written.
	formatter  FormatterType // Output format (TextFormatter or JSONFormatter).
	baseFields M            // Fields to be included with every log entry from this logger instance and its children.
	showCaller bool         // If true, include caller (file:line) information in logs.
	useColor   bool         // If true, and formatter is TextFormatter, use colors for terminal output.
	bufferPool sync.Pool    // A pool of bytes.Buffer to reduce allocations for log message formatting.
}

// NewDefaultLogger creates a new instance of DefaultLogger with sensible defaults:
// - Output: os.Stdout
// - Level: LevelInfo
// - Formatter: TextFormatter
// - Caller Info: false
// - Color: false (will be auto-detected for TTY if enabled later or by router config)
func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{
		out:        os.Stdout,
		level:      LevelInfo,
		formatter:  TextFormatter,
		baseFields: make(M),
		showCaller: false,
		useColor:   false,
		bufferPool: sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
}

// SetOutput sets the output destination for the logger.
// This method is thread-safe.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

// SetLevel sets the minimum logging level. Messages below this level will be discarded.
// This method is thread-safe.
func (l *DefaultLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current logging level of the logger.
// This method is thread-safe.
func (l *DefaultLogger) GetLevel() LogLevel {
	l.mu.RLock() // Use RLock for read-only access.
	defer l.mu.RUnlock()
	return l.level
}

// SetFormatter sets the output format for log entries (TextFormatter or JSONFormatter).
// This method is thread-safe.
func (l *DefaultLogger) SetFormatter(formatter FormatterType) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.formatter = formatter
}

// EnableCaller enables or disables the inclusion of caller (file:line) information in logs.
// This method is thread-safe.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the TextFormatter.
// If 'enable' is true, it will attempt to use colors if the output destination
// (l.out) is a standard terminal (os.Stdout or os.Stderr). For other io.Writer
// types, enabling color will proceed if explicitly requested, assuming the user
// handles the color codes appropriately.
// This method is thread-safe.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		// If output is stdout or stderr, detect if it's a terminal.
		// For custom io.Writer, if user explicitly enables color, we assume they want it.
		if l.out == os.Stdout || l.out == os.Stderr {
			l.useColor = isTerminal(l.out) // isTerminal is a private helper in this file.
		} else {
			l.useColor = true // User explicitly requested color for a custom writer.
		}
	} else {
		l.useColor = false // Disable color.
	}
}

// isLevelEnabled checks if a given log level is currently enabled for this logger.
// This is a thread-safe read operation.
func (l *DefaultLogger) isLevelEnabled(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level >= l.level
}

// doLog is the core internal logging method.
// It constructs the LogEntry, formats it, and writes to the output.
// `skipFrames` indicates how many stack frames to skip to find the original caller.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	// Quick check to avoid work if level is not enabled.
	if !l.isLevelEnabled(level) {
		return
	}

	// Atomically read current logger configuration to minimize lock duration.
	l.mu.RLock()
	currentOut := l.out
	currentFormatter := l.formatter
	currentShowCaller := l.showCaller
	currentUseColor := l.useColor // This value is already TTY-aware if set by EnableColor(true)
	// Deep copy baseFields to ensure immutability from concurrent WithFields calls on parent.
	// While WithFields creates a new logger, this is an extra safeguard.
	copiedBaseFields := make(M, len(l.baseFields))
	for k, v := range l.baseFields {
		copiedBaseFields[k] = v
	}
	l.mu.RUnlock()

	// Prepare the LogEntry.
	entry := LogEntry{
		Timestamp: time.Now().Format(DefaultTimestampFormat),
		Level:     level.String(),
		Message:   message, // Initial message, may be overridden by Sprintf.
		Fields:    make(M),
	}

	// Apply base fields from this logger instance.
	for k, v := range copiedBaseFields {
		entry.Fields[k] = v
	}

	// Process arguments: separate Sprintf args from M (fields) args.
	var formatArgs []interface{}
	hasFormatSpecifier := strings.Contains(message, "%") // Basic check for Sprintf usage.
	messageContainsArgs := false

	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok {
			for k, v := range fieldsMap { // Merge fields from M argument.
				entry.Fields[k] = v
			}
		} else {
			formatArgs = append(formatArgs, arg)
			messageContainsArgs = true
		}
	}

	// Format the main message if Sprintf-style arguments were provided.
	if messageContainsArgs {
		if hasFormatSpecifier && len(formatArgs) > 0 {
			entry.Message = fmt.Sprintf(message, formatArgs...)
		} else if len(formatArgs) > 0 { // Append non-formatting args if no specifiers.
			entry.Message = message + " " + fmt.Sprint(formatArgs...)
		}
		// If !hasFormatSpecifier and len(formatArgs) == 0, message remains as is.
	}


	// Add caller information if enabled.
	if currentShowCaller {
		// The +1 for skipFrames is because doLog itself is one frame.
		// The caller of Debug(), Info(), etc. is `skipFrames` away from that.
		_, file, line, ok := runtime.Caller(skipFrames + 1)
		if ok {
			shortFile := file
			// Extract just the filename from the full path for brevity.
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				shortFile = file[idx+1:]
			}
			entry.Caller = fmt.Sprintf("%s:%d", shortFile, line)
		}
	}

	// Get a buffer from the pool for formatting the final log string.
	buffer := l.bufferPool.Get().(*bytes.Buffer)
	buffer.Reset() // Ensure the buffer is empty.
	defer l.bufferPool.Put(buffer) // Return the buffer to the pool when done.

	// Format the LogEntry based on the configured formatter.
	switch currentFormatter {
	case JSONFormatter:
		jsonData, err := json.Marshal(entry)
		if err != nil {
			// Fallback for JSON marshalling errors: log a plain error message.
			// This ensures that critical log information isn't entirely lost.
			timestamp := time.Now().Format(DefaultTimestampFormat) // Get fresh timestamp for this error log
			fmt.Fprintf(buffer, `{"timestamp":"%s","level":"ERROR","message":"Failed to marshal log entry to JSON. See Xylium server logs for details. Original message: %s"}\n`,
				timestamp, entry.Message)
			// Also log the marshalling error to stderr directly for visibility.
			fmt.Fprintf(os.Stderr, "Xylium Logger JSON Marshal Error: %v for entry: %+v\n", err, entry)
		} else {
			buffer.Write(jsonData)
			buffer.WriteString("\n") // Ensure each JSON log is on a new line.
		}
	case TextFormatter:
		fallthrough // If formatter is unknown, default to TextFormatter.
	default:
		buffer.WriteString(entry.Timestamp)
		buffer.WriteString(" ")

		levelStr := entry.Level
		// Apply color only if useColor is true (already TTY-aware).
		if currentUseColor {
			switch level {
			case LevelDebug: levelStr = colorCyan + levelStr + colorReset
			case LevelInfo:  levelStr = colorGreen + levelStr + colorReset
			case LevelWarn:  levelStr = colorYellow + levelStr + colorReset
			case LevelError, LevelFatal, LevelPanic: levelStr = colorRed + levelStr + colorReset
			}
		}
		buffer.WriteString(fmt.Sprintf("[%s]", levelStr))
		buffer.WriteString(" ")

		if entry.Caller != "" {
			callerStr := entry.Caller
			if currentUseColor {
				callerStr = colorGray + callerStr + colorReset
			}
			buffer.WriteString(fmt.Sprintf("<%s>", callerStr))
			buffer.WriteString(" ")
		}

		buffer.WriteString(entry.Message)

		if len(entry.Fields) > 0 {
			buffer.WriteString(" ") // Separator for fields.
			// Marshal fields to JSON for a compact, readable representation in text logs.
			fieldBytes, err := json.Marshal(entry.Fields)
			if err != nil { // Should be rare if M contains basic types.
				buffer.WriteString(fmt.Sprintf(" (error marshalling fields: %v)", err))
			} else {
				fieldStr := string(fieldBytes)
				if currentUseColor {
					fieldStr = colorPurple + fieldStr + colorReset
				}
				buffer.WriteString(fieldStr)
			}
		}
		buffer.WriteString("\n")
	}

	// Write the formatted log entry to the output.
	// This write operation needs to be synchronized.
	l.mu.Lock() // Lock for writing to l.out and for Fatal/Panic behavior.
	var writeError error
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		// If writing to the primary output fails, try writing an error message to os.Stderr.
		fmt.Fprintf(os.Stderr, "Xylium Logger: Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
		writeError = err // Store for fatal/panic handling
	}
	l.mu.Unlock()

	// Handle Fatal and Panic levels after attempting to log.
	if level == LevelFatal {
		if writeError != nil { // If primary log write failed, ensure fatal message hits stderr
			fmt.Fprintf(os.Stderr, "FATAL: %s\n", entry.Message)
		}
		os.Exit(1)
	} else if level == LevelPanic {
		if writeError != nil { // If primary log write failed, ensure panic message hits stderr
			fmt.Fprintf(os.Stderr, "PANIC: %s\n", entry.Message)
		}
		panic(entry.Message) // Re-panic with the original formatted message.
	}
}

// --- Implementation of xylium.Logger interface methods ---
// These methods call doLog with the appropriate level and stack frame skip count.
// skipFrames = 2: one for this method (e.g., Debug), one for doLog itself.

func (l *DefaultLogger) Printf(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}

func (l *DefaultLogger) Debug(args ...interface{}) {
	l.doLog(LevelDebug, 2, fmt.Sprint(args...))
}

func (l *DefaultLogger) Info(args ...interface{}) {
	l.doLog(LevelInfo, 2, fmt.Sprint(args...))
}

func (l *DefaultLogger) Warn(args ...interface{}) {
	l.doLog(LevelWarn, 2, fmt.Sprint(args...))
}

func (l *DefaultLogger) Error(args ...interface{}) {
	l.doLog(LevelError, 2, fmt.Sprint(args...))
}

func (l *DefaultLogger) Fatal(args ...interface{}) {
	l.doLog(LevelFatal, 2, fmt.Sprint(args...)) // doLog handles os.Exit
}

func (l *DefaultLogger) Panic(args ...interface{}) {
	l.doLog(LevelPanic, 2, fmt.Sprint(args...)) // doLog handles panic
}

func (l *DefaultLogger) Debugf(format string, args ...interface{}) {
	l.doLog(LevelDebug, 2, format, args...)
}

func (l *DefaultLogger) Infof(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}

func (l *DefaultLogger) Warnf(format string, args ...interface{}) {
	l.doLog(LevelWarn, 2, format, args...)
}

func (l *DefaultLogger) Errorf(format string, args ...interface{}) {
	l.doLog(LevelError, 2, format, args...)
}

func (l *DefaultLogger) Fatalf(format string, args ...interface{}) {
	l.doLog(LevelFatal, 2, format, args...) // doLog handles os.Exit
}

func (l *DefaultLogger) Panicf(format string, args ...interface{}) {
	l.doLog(LevelPanic, 2, format, args...) // doLog handles panic
}

// WithFields creates a new DefaultLogger instance that inherits settings from the
// parent logger but includes the additional provided fields in its baseFields.
// The original logger is not modified. This method is thread-safe.
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock() // Read lock to access parent's configuration safely.
	// Create a new logger instance.
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		baseFields: make(M, len(l.baseFields)+len(fields)), // Pre-allocate for efficiency.
		showCaller: l.showCaller,
		useColor:   l.useColor,
		bufferPool: l.bufferPool, // Share the buffer pool.
	}
	// Copy baseFields from the parent logger.
	for k, v := range l.baseFields {
		newLogger.baseFields[k] = v
	}
	l.mu.RUnlock() // Release read lock on parent.

	// Add/override with the new fields.
	// No lock needed on newLogger yet as it's not shared.
	for k, v := range fields {
		newLogger.baseFields[k] = v
	}
	return newLogger
}

// isTerminal is a private helper function to check if the given io.Writer
// is a character device, which usually indicates a TTY/terminal.
// This is used by EnableColor to determine if colors should be used for os.Stdout/os.Stderr.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return false // Error stating file, assume not a terminal.
		}
		// Check if the file mode is a character device.
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	return false // If not an *os.File, assume not a terminal.
}
