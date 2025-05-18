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
	TextFormatter FormatterType = "text" // Human-readable text format.
	JSONFormatter FormatterType = "json" // Structured JSON format, suitable for log processing systems.
)

// DefaultTimestampFormat is the standard format for log timestamps, adhering to RFC3339 with milliseconds.
const DefaultTimestampFormat = "2006-01-02T15:04:05.000Z07:00"

// ANSI escape codes for colored output in TextFormatter.
const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m" // Often unused by default, but available.
	colorPurple = "\033[35m" // Used for fields in text format.
	colorCyan   = "\033[36m" // Used for DEBUG level in text format.
	colorGray   = "\033[90m" // Used for caller info in text format.
	colorReset  = "\033[0m"
)

// LogEntry is an internal struct used to gather all log data before formatting.
// It is exported to allow potential custom formatters to leverage this structure,
// though direct use by end-users is not typical.
type LogEntry struct {
	Timestamp string `json:"timestamp"`          // Timestamp of the log entry.
	Level     string `json:"level"`              // Severity level of the log (e.g., "INFO", "ERROR").
	Message   string `json:"message"`            // The main log message.
	Fields    M      `json:"fields,omitempty"`   // Additional structured key-value data.
	Caller    string `json:"caller,omitempty"`   // File and line number of the log call site (if enabled).
}

// LoggerConfig defines detailed configuration for a DefaultLogger instance.
type LoggerConfig struct {
	Level      LogLevel      // Minimum log level to output.
	Formatter  FormatterType // Output format (TextFormatter or JSONFormatter).
	ShowCaller bool          // Whether to include caller file/line information.
	UseColor   bool          // Whether to use ANSI colors for TextFormatter (if output is a TTY).
	Output     io.Writer     // Destination for log output (e.g., os.Stdout, a file).
}

// DefaultLoggerConfig provides default settings for LoggerConfig.
// These defaults are applied if a LoggerConfig is not fully specified
// or if NewDefaultLogger is called without explicit configuration.
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:      LevelInfo,       // Default to Info level.
		Formatter:  TextFormatter,   // Default to human-readable text.
		ShowCaller: false,           // Caller info off by default for performance.
		UseColor:   false,           // Color off by default; enabled in DebugMode if TTY.
		Output:     os.Stdout,       // Default output to standard out.
	}
}

// DefaultLogger is Xylium's default implementation of the xylium.Logger interface.
// It supports leveled logging, structured fields, and multiple output formats (text/JSON).
type DefaultLogger struct {
	mu         sync.RWMutex  // Protects access to logger configuration fields (out, level, etc.).
	out        io.Writer     // The output destination for log entries.
	level      LogLevel      // The current minimum log level.
	formatter  FormatterType // The current output formatter.
	baseFields M             // Fields to include in every log entry from this logger instance.
	showCaller bool          // Whether to include caller information.
	useColor   bool          // Whether to use colored output for TextFormatter.
	bufferPool sync.Pool     // Pool of bytes.Buffer to reduce allocations during formatting.
}

// NewDefaultLoggerWithConfig creates a new DefaultLogger with specified configuration.
// This is the primary constructor used internally by Xylium when auto-configuring loggers.
func NewDefaultLoggerWithConfig(config LoggerConfig) *DefaultLogger {
	if config.Output == nil { // Ensure output is never nil.
		config.Output = os.Stdout
	}
	dl := &DefaultLogger{
		out:        config.Output,
		level:      config.Level,
		formatter:  config.Formatter,
		baseFields: make(M), // Initialize with empty base fields.
		showCaller: config.ShowCaller,
		useColor:   false, // Initialized to false; EnableColor will correctly set it based on TTY.
		bufferPool: sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
	if config.UseColor { // If config explicitly requests color, attempt to enable it.
		dl.EnableColor(true) // EnableColor handles TTY check.
	}
	return dl
}

// NewDefaultLogger creates a new instance of DefaultLogger with sensible defaults.
// It uses DefaultLoggerConfig internally.
func NewDefaultLogger() *DefaultLogger {
	return NewDefaultLoggerWithConfig(DefaultLoggerConfig())
}

// SetOutput sets the output destination for the logger. Thread-safe.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil { // Prevent setting output to nil.
		l.out = os.Stdout // Fallback to stdout if nil is provided.
		return
	}
	l.out = w
}

// SetLevel sets the minimum logging level. Thread-safe.
func (l *DefaultLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current logging level of the logger. Thread-safe.
func (l *DefaultLogger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SetFormatter sets the output format for log entries. Thread-safe.
func (l *DefaultLogger) SetFormatter(formatter FormatterType) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch formatter {
	case TextFormatter, JSONFormatter:
		l.formatter = formatter
	default:
		// If an invalid formatter is provided, default to TextFormatter.
		// Optionally, log a warning here.
		l.formatter = TextFormatter
	}
}

// EnableCaller enables or disables the inclusion of caller information (file and line number). Thread-safe.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the TextFormatter.
// Color is only applied if the output writer is a TTY (terminal). Thread-safe.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		// Check if the current output is a terminal supporting color.
		// isTerminal will check for os.Stdout or os.Stderr.
		// If output is custom, color might be enabled if `enable` is true.
		l.useColor = isTerminal(l.out)
	} else {
		l.useColor = false
	}
}

// isLevelEnabled checks if a given log level is currently enabled (i.e., at or above the logger's set level).
// Assumes RLock is held by caller or called from a context where it's safe.
func (l *DefaultLogger) isLevelEnabledRLocked(level LogLevel) bool {
	return level >= l.level
}

// doLog is the core internal logging method. It constructs and formats the log entry, then writes it.
// `skipFrames` is used to adjust runtime.Caller to get the correct call site.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	l.mu.RLock() // Read lock for accessing logger's current configuration.
	if !l.isLevelEnabledRLocked(level) {
		l.mu.RUnlock()
		return
	}
	// Copy current configuration values under RLock to use them after unlocking.
	currentOut := l.out
	currentFormatter := l.formatter
	currentShowCaller := l.showCaller
	currentUseColor := l.useColor
	// Deep copy baseFields to avoid modification races if WithFields is used concurrently.
	copiedBaseFields := make(M, len(l.baseFields))
	for k, v := range l.baseFields {
		copiedBaseFields[k] = v
	}
	l.mu.RUnlock() // Release RLock after copying config.

	// Prepare the log entry.
	entry := LogEntry{
		Timestamp: time.Now().Format(DefaultTimestampFormat),
		Level:     level.String(),
		Message:   message, // Initial message, may be formatted later.
		Fields:    make(M),   // Initialize empty fields for this specific entry.
	}

	// Populate entry.Fields with copiedBaseFields.
	for k, v := range copiedBaseFields {
		entry.Fields[k] = v
	}

	// Process variadic arguments: separate format arguments from field maps (xylium.M).
	var formatArgs []interface{}
	hasFormatSpecifier := strings.Contains(message, "%") // Check for format specifiers like %s, %d.
	messageContainsArgsToBeFormatted := false

	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok { // If argument is xylium.M, merge into entry.Fields.
			for k, v := range fieldsMap {
				entry.Fields[k] = v
			}
		} else { // Otherwise, it's an argument for message formatting.
			formatArgs = append(formatArgs, arg)
			messageContainsArgsToBeFormatted = true
		}
	}

	// Format the main message if arguments were provided for it.
	if messageContainsArgsToBeFormatted {
		if hasFormatSpecifier && len(formatArgs) > 0 {
			entry.Message = fmt.Sprintf(message, formatArgs...)
		} else if len(formatArgs) > 0 {
			// If no format specifiers but args exist, append them (like fmt.Sprint).
			entry.Message = message + " " + fmt.Sprint(formatArgs...)
		}
		// If message has specifiers but no args, or no specifiers and no args, entry.Message remains as is.
	}

	// Add caller information if enabled.
	if currentShowCaller {
		// skipFrames+1: +1 to account for this doLog function itself.
		_, file, line, ok := runtime.Caller(skipFrames + 1)
		if ok {
			// Get short file name (e.g., "main.go" from "/path/to/project/main.go").
			shortFile := file
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				shortFile = file[idx+1:]
			}
			entry.Caller = fmt.Sprintf("%s:%d", shortFile, line)
		}
	}

	// Get a buffer from the pool for formatting the output.
	buffer := l.bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()          // Ensure buffer is clean.
	defer l.bufferPool.Put(buffer) // Return buffer to pool when done.

	// Format the log entry based on the configured formatter.
	switch currentFormatter {
	case JSONFormatter:
		jsonData, err := json.Marshal(entry)
		if err != nil {
			// Fallback for JSON marshalling errors: log error to os.Stderr and format a simple error message.
			timestamp := time.Now().Format(DefaultTimestampFormat) // Use fresh timestamp for error message.
			fmt.Fprintf(buffer, `{"timestamp":"%s","level":"ERROR","message":"Failed to marshal log entry to JSON. Original message: %s"}`+"\n",
				timestamp, entry.Message) // Intentionally not escaping entry.Message here for simplicity in fallback.
			// Log the marshalling error itself to standard error for visibility.
			fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] JSON Marshal Error: %v for entry: %+v\n", err, entry)
		} else {
			buffer.Write(jsonData)
			buffer.WriteString("\n") // Ensure newline for JSON entries.
		}
	case TextFormatter:
		fallthrough // TextFormatter is the default if formatter is unrecognized.
	default:
		buffer.WriteString(entry.Timestamp)
		buffer.WriteString(" ")

		levelStr := entry.Level
		if currentUseColor { // Apply color to level string if enabled.
			switch level {
			case LevelDebug: levelStr = colorCyan + levelStr + colorReset
			case LevelInfo:  levelStr = colorGreen + levelStr + colorReset
			case LevelWarn:  levelStr = colorYellow + levelStr + colorReset
			case LevelError, LevelFatal, LevelPanic: levelStr = colorRed + levelStr + colorReset
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

		buffer.WriteString(entry.Message) // The main formatted message.

		if len(entry.Fields) > 0 { // Append fields if any.
			buffer.WriteString(" ") // Separator for fields.
			fieldBytes, err := json.Marshal(entry.Fields) // Fields are always marshalled as JSON for text format.
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

	// Write the formatted log entry to the output.
	// This part needs a WLock because it's an I/O operation on potentially shared `currentOut`.
	l.mu.Lock()
	var writeError error
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		// If writing to primary output fails, write a fallback message to os.Stderr.
		fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
		writeError = err // Store error for Fatal/Panic handling.
	}
	l.mu.Unlock()

	// Handle Fatal and Panic levels after attempting to log.
	if level == LevelFatal {
		if writeError != nil { // Ensure message is seen even if primary log write failed.
			fmt.Fprintf(os.Stderr, "FATAL: %s\n", entry.Message)
		}
		os.Exit(1)
	} else if level == LevelPanic {
		if writeError != nil { // Ensure message is seen.
			fmt.Fprintf(os.Stderr, "PANIC: %s\n", entry.Message)
		}
		panic(entry.Message) // Re-panic with the original message.
	}
}

// --- Implementation of xylium.Logger interface methods ---
// These methods call doLog with the appropriate level and frame skip count.
// Frame skip count is 2: 1 for the Xf wrapper (e.g., Debugf), 1 for doLog itself.

func (l *DefaultLogger) Printf(format string, args ...interface{}) { l.doLog(LevelInfo, 2, format, args...) }
func (l *DefaultLogger) Debug(args ...interface{})  { l.doLog(LevelDebug, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Info(args ...interface{})   { l.doLog(LevelInfo, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Warn(args ...interface{})   { l.doLog(LevelWarn, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Error(args ...interface{})  { l.doLog(LevelError, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Fatal(args ...interface{})  { l.doLog(LevelFatal, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Panic(args ...interface{})  { l.doLog(LevelPanic, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Debugf(format string, args ...interface{}) { l.doLog(LevelDebug, 2, format, args...) }
func (l *DefaultLogger) Infof(format string, args ...interface{})  { l.doLog(LevelInfo, 2, format, args...) }
func (l *DefaultLogger) Warnf(format string, args ...interface{})  { l.doLog(LevelWarn, 2, format, args...) }
func (l *DefaultLogger) Errorf(format string, args ...interface{}) { l.doLog(LevelError, 2, format, args...) }
func (l *DefaultLogger) Fatalf(format string, args ...interface{}) { l.doLog(LevelFatal, 2, format, args...) }
func (l *DefaultLogger) Panicf(format string, args ...interface{}) { l.doLog(LevelPanic, 2, format, args...) }

// WithFields creates a new DefaultLogger instance that includes the given `fields`
// in all subsequent log entries. The original logger is not modified.
// This allows for creating context-specific loggers.
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock() // Read lock for accessing current logger's state.
	// Create a new logger instance, copying configuration from the parent.
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		baseFields: make(M, len(l.baseFields)+len(fields)), // Pre-allocate for combined fields.
		showCaller: l.showCaller,
		useColor:   l.useColor,
		bufferPool: l.bufferPool, // Share the same buffer pool.
	}
	// Copy existing base fields from the parent logger.
	for k, v := range l.baseFields {
		newLogger.baseFields[k] = v
	}
	l.mu.RUnlock() // Release lock after copying parent's state.

	// Add the new fields to the new logger's baseFields.
	// This does not require a lock on newLogger as it's not yet shared.
	for k, v := range fields {
		newLogger.baseFields[k] = v
	}
	return newLogger
}

// isTerminal checks if the given io.Writer is a character device (typically a terminal).
// This is used to determine if colored output should be enabled for TextFormatter.
func isTerminal(w io.Writer) bool {
	// Check if the writer is an os.File.
	if f, ok := w.(*os.File); ok {
		// Get file statistics.
		stat, err := f.Stat()
		if err != nil {
			return false // Error getting stats, assume not a terminal.
		}
		// Check if the file mode indicates a character device.
		// This is a common way to detect terminals on Unix-like systems.
		// On Windows, this check might behave differently or require os-specific logic,
		// but for basic TTY detection, it's often sufficient.
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	// If not an os.File, assume not a terminal for automatic color.
	// User can still force color via LoggerConfig.UseColor = true for custom writers.
	return false
}
