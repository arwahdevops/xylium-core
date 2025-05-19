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
	JSONFormatter FormatterType = "json" // Structured JSON format.
)

// DefaultTimestampFormat is the standard format for log timestamps.
const DefaultTimestampFormat = "2006-01-02T15:04:05.000Z07:00"

// ANSI escape codes for colored output in TextFormatter.
const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorReset  = "\033[0m"
)

// LogEntry is an internal struct used to gather all log data before formatting.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Fields    M      `json:"fields,omitempty"`
	Caller    string `json:"caller,omitempty"`
}

// LoggerConfig defines detailed configuration for a DefaultLogger instance.
type LoggerConfig struct {
	Level      LogLevel
	Formatter  FormatterType
	ShowCaller bool
	UseColor   bool
	Output     io.Writer
}

// DefaultLoggerConfig provides default settings for LoggerConfig.
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:      LevelInfo,
		Formatter:  TextFormatter,
		ShowCaller: false,
		UseColor:   false, // Determined by TTY in DebugMode or explicit config.
		Output:     os.Stdout,
	}
}

// DefaultLogger is Xylium's default implementation of the xylium.Logger interface.
type DefaultLogger struct {
	mu         sync.RWMutex
	out        io.Writer
	level      LogLevel
	formatter  FormatterType
	baseFields M
	showCaller bool
	useColor   bool
	bufferPool *sync.Pool // Pointer to sync.Pool
}

// NewDefaultLoggerWithConfig creates a new DefaultLogger with specified configuration.
func NewDefaultLoggerWithConfig(config LoggerConfig) *DefaultLogger {
	if config.Output == nil {
		config.Output = os.Stdout
	}
	dl := &DefaultLogger{
		out:        config.Output,
		level:      config.Level,
		formatter:  config.Formatter,
		baseFields: make(M),
		showCaller: config.ShowCaller,
		useColor:   false, // Will be correctly set by EnableColor if config.UseColor is true
		bufferPool: &sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
	if config.UseColor {
		dl.EnableColor(true) // EnableColor handles TTY check.
	}
	return dl
}

// NewDefaultLogger creates a new instance of DefaultLogger with sensible defaults.
func NewDefaultLogger() *DefaultLogger {
	return NewDefaultLoggerWithConfig(DefaultLoggerConfig())
}

// SetOutput sets the output destination for the logger. Thread-safe.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		l.out = os.Stdout
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
		l.formatter = TextFormatter
	}
}

// EnableCaller enables or disables the inclusion of caller information. Thread-safe.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the TextFormatter.
// Color is only applied if the output writer is a TTY. Thread-safe.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		l.useColor = isTerminal(l.out)
	} else {
		l.useColor = false
	}
}

// isLevelEnabledRLocked checks if a given log level is currently enabled.
// Assumes RLock is held by caller or called from a safe context.
func (l *DefaultLogger) isLevelEnabledRLocked(level LogLevel) bool {
	return level >= l.level
}

// doLog is the core internal logging method.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	l.mu.RLock()
	if !l.isLevelEnabledRLocked(level) {
		l.mu.RUnlock()
		return
	}
	currentOut := l.out
	currentFormatter := l.formatter
	currentShowCaller := l.showCaller
	currentUseColor := l.useColor
	copiedBaseFields := make(M, len(l.baseFields))
	for k, v := range l.baseFields {
		copiedBaseFields[k] = v
	}
	l.mu.RUnlock()

	entry := LogEntry{
		Timestamp: time.Now().Format(DefaultTimestampFormat),
		Level:     level.String(),
		Message:   message,
		Fields:    make(M), // Initialize for this specific entry
	}

	for k, v := range copiedBaseFields { // Populate with copied base fields
		entry.Fields[k] = v
	}

	var formatArgs []interface{}
	hasFormatSpecifier := strings.Contains(message, "%")
	messageContainsArgsToBeFormatted := false

	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok {
			for k, v := range fieldsMap {
				entry.Fields[k] = v
			}
		} else {
			formatArgs = append(formatArgs, arg)
			messageContainsArgsToBeFormatted = true
		}
	}

	if messageContainsArgsToBeFormatted {
		if hasFormatSpecifier && len(formatArgs) > 0 {
			entry.Message = fmt.Sprintf(message, formatArgs...)
		} else if len(formatArgs) > 0 {
			entry.Message = message + " " + fmt.Sprint(formatArgs...)
		}
	}

	if currentShowCaller {
		_, file, line, ok := runtime.Caller(skipFrames + 1)
		if ok {
			shortFile := file
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				shortFile = file[idx+1:]
			}
			entry.Caller = fmt.Sprintf("%s:%d", shortFile, line)
		}
	}

	buffer := l.bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	defer l.bufferPool.Put(buffer)

	switch currentFormatter {
	case JSONFormatter:
		jsonData, err := json.Marshal(entry)
		if err != nil {
			timestampFallback := time.Now().Format(DefaultTimestampFormat)
			// Use fmt.Fprintf to buffer to avoid race conditions on os.Stderr if many log lines fail
			fmt.Fprintf(buffer, `{"timestamp":"%s","level":"ERROR","message":"Failed to marshal log entry to JSON. Original message: %s"}`+"\n",
				timestampFallback, entry.Message) // entry.Message could be escaped for safety in real JSON.
			// Log the marshalling error itself to standard error for visibility.
			fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] JSON Marshal Error: %v for entry: %+v\n", err, entry)
		} else {
			buffer.Write(jsonData)
			buffer.WriteString("\n")
		}
	case TextFormatter:
		fallthrough
	default:
		buffer.WriteString(entry.Timestamp)
		buffer.WriteString(" ")
		levelStr := entry.Level
		if currentUseColor {
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
			buffer.WriteString(" ")
			fieldBytes, err := json.Marshal(entry.Fields)
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
		buffer.WriteString("\n")
	}

	l.mu.Lock() // Lock for I/O operation on potentially shared `currentOut`.
	var writeError error
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "[XYLIUM-LOGGER-ERROR] Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
		writeError = err
	}
	l.mu.Unlock()

	if level == LevelFatal {
		if writeError != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %s\n", entry.Message)
		}
		os.Exit(1)
	} else if level == LevelPanic {
		if writeError != nil {
			fmt.Fprintf(os.Stderr, "PANIC: %s\n", entry.Message)
		}
		panic(entry.Message)
	}
}

// --- Implementation of xylium.Logger interface methods ---
func (l *DefaultLogger) Printf(format string, args ...interface{}) {
	l.doLog(LevelInfo, 2, format, args...)
}
func (l *DefaultLogger) Debug(args ...interface{}) { l.doLog(LevelDebug, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Info(args ...interface{})  { l.doLog(LevelInfo, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Warn(args ...interface{})  { l.doLog(LevelWarn, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Error(args ...interface{}) { l.doLog(LevelError, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Fatal(args ...interface{}) { l.doLog(LevelFatal, 2, fmt.Sprint(args...)) }
func (l *DefaultLogger) Panic(args ...interface{}) { l.doLog(LevelPanic, 2, fmt.Sprint(args...)) }
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
	l.doLog(LevelFatal, 2, format, args...)
}
func (l *DefaultLogger) Panicf(format string, args ...interface{}) {
	l.doLog(LevelPanic, 2, format, args...)
}

// WithFields creates a new DefaultLogger instance that includes the given `fields`.
// The new logger shares the same underlying bufferPool.
// REFACTORED: bufferPool assignment moved outside struct literal.
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock()

	// Create the new logger instance, omitting bufferPool for now.
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		showCaller: l.showCaller,
		useColor:   l.useColor,
		// baseFields will be populated below
	}
	// Explicitly assign the bufferPool pointer.
	newLogger.bufferPool = l.bufferPool

	// Safely copy baseFields from l.
	combinedBaseFields := make(M, len(l.baseFields)+len(fields))
	for k, v := range l.baseFields {
		combinedBaseFields[k] = v
	}
	l.mu.RUnlock() // Unlock l after its state has been read.

	// Add the new fields to the combined map.
	for k, v := range fields {
		combinedBaseFields[k] = v
	}
	newLogger.baseFields = combinedBaseFields

	return newLogger
}

// isTerminal checks if the given io.Writer is a character device (typically a terminal).
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return false
		}
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	return false
}
