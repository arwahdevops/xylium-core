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
	TextFormatter FormatterType = "text"
	JSONFormatter FormatterType = "json"
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
// RENAMED: LoggerConfig (was loggerConfig)
type LoggerConfig struct {
	Level      LogLevel
	Formatter  FormatterType
	ShowCaller bool
	UseColor   bool
	Output     io.Writer
}

// DefaultLoggerConfig provides default settings for LoggerConfig.
// RENAMED: DefaultLoggerConfig (was defaultLoggerConfig)
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:      LevelInfo,
		Formatter:  TextFormatter,
		ShowCaller: false,
		UseColor:   false,
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
	bufferPool sync.Pool
}


// NewDefaultLoggerWithConfig creates a new DefaultLogger with specified configuration.
// RENAMED: NewDefaultLoggerWithConfig (was newDefaultLoggerWithConfig - although this was already correct)
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
		useColor:   false, // useColor will be resolved by EnableColor
		bufferPool: sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
	}
	if config.UseColor {
		dl.EnableColor(true)
	}
	return dl
}

// NewDefaultLogger creates a new instance of DefaultLogger with sensible defaults.
func NewDefaultLogger() *DefaultLogger {
	return NewDefaultLoggerWithConfig(DefaultLoggerConfig())
}

// SetOutput sets the output destination for the logger.
func (l *DefaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

// SetLevel sets the minimum logging level.
func (l *DefaultLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current logging level of the logger.
func (l *DefaultLogger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SetFormatter sets the output format for log entries.
func (l *DefaultLogger) SetFormatter(formatter FormatterType) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.formatter = formatter
}

// EnableCaller enables or disables the inclusion of caller information.
func (l *DefaultLogger) EnableCaller(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showCaller = enable
}

// EnableColor enables or disables colored output for the TextFormatter.
func (l *DefaultLogger) EnableColor(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if enable {
		if l.out == os.Stdout || l.out == os.Stderr {
			l.useColor = isTerminal(l.out)
		} else {
			l.useColor = true
		}
	} else {
		l.useColor = false
	}
}

// isLevelEnabled checks if a given log level is currently enabled.
func (l *DefaultLogger) isLevelEnabled(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level >= l.level
}

// doLog is the core internal logging method.
func (l *DefaultLogger) doLog(level LogLevel, skipFrames int, message string, args ...interface{}) {
	if !l.isLevelEnabled(level) {
		return
	}
	l.mu.RLock()
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
		Fields:    make(M),
	}
	for k, v := range copiedBaseFields {
		entry.Fields[k] = v
	}
	var formatArgs []interface{}
	hasFormatSpecifier := strings.Contains(message, "%")
	messageContainsArgs := false
	for _, arg := range args {
		if fieldsMap, ok := arg.(M); ok {
			for k, v := range fieldsMap {
				entry.Fields[k] = v
			}
		} else {
			formatArgs = append(formatArgs, arg)
			messageContainsArgs = true
		}
	}
	if messageContainsArgs {
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
			timestamp := time.Now().Format(DefaultTimestampFormat)
			fmt.Fprintf(buffer, `{"timestamp":"%s","level":"ERROR","message":"Failed to marshal log entry to JSON. Original message: %s"}\n`,
				timestamp, entry.Message)
			fmt.Fprintf(os.Stderr, "Xylium Logger JSON Marshal Error: %v for entry: %+v\n", err, entry)
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
			buffer.WriteString(" ")
			fieldBytes, err := json.Marshal(entry.Fields)
			if err != nil {
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
	l.mu.Lock()
	var writeError error
	if _, err := currentOut.Write(buffer.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "Xylium Logger: Failed to write log entry to primary output: %v. Original message: %s\n", err, entry.Message)
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

// WithFields creates a new DefaultLogger instance with additional fields.
func (l *DefaultLogger) WithFields(fields M) Logger {
	l.mu.RLock()
	newLogger := &DefaultLogger{
		out:        l.out,
		level:      l.level,
		formatter:  l.formatter,
		baseFields: make(M, len(l.baseFields)+len(fields)),
		showCaller: l.showCaller,
		useColor:   l.useColor,
		bufferPool: l.bufferPool,
	}
	for k, v := range l.baseFields {
		newLogger.baseFields[k] = v
	}
	l.mu.RUnlock()
	for k, v := range fields {
		newLogger.baseFields[k] = v
	}
	return newLogger
}

// isTerminal checks if the given io.Writer is a character device.
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
