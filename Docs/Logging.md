# Xylium Logging

Xylium provides a flexible and powerful logging system designed for both development clarity and production efficiency. It features an application-level logger, request-scoped contextual loggers, structured logging capabilities, and easy configuration.

## Table of Contents

*   [1. Overview of Xylium's Logger](#1-overview-of-xyliums-logger)
    *   [1.1. `xylium.Logger` Interface](#11-xyliumlogger-interface)
    *   [1.2. `xylium.DefaultLogger`](#12-xyliumdefaultlogger)
*   [2. Application-Level Logging (`app.Logger()`)](#2-application-level-logging-applogger)
*   [3. Request-Scoped Logging (`c.Logger()`)](#3-request-scoped-logging-clogger)
*   [4. Structured Logging with Fields (`WithFields`)](#4-structured-logging-with-fields-withfields)
*   [5. Log Levels](#5-log-levels)
*   [6. Configuring the Default Logger](#6-configuring-the-default-logger)
    *   [6.1. Automatic Configuration via Operating Modes](#61-automatic-configuration-via-operating-modes)
    *   [6.2. Manual Configuration (`xylium.LoggerConfig`)](#62-manual-configuration-xyliumloggerconfig)
    *   [6.3. Setting Output, Level, Formatter, etc., Dynamically](#63-setting-output-level-formatter-etc-dynamically)
*   [7. Using a Custom Logger Implementation](#7-using-a-custom-logger-implementation)
*   [8. Log Output Formats](#8-log-output-formats)
    *   [8.1. Text Formatter](#81-text-formatter)
    *   [8.2. JSON Formatter](#82-json-formatter)

---

## 1. Overview of Xylium's Logger

Xylium's logging is built around the `xylium.Logger` interface, with `xylium.DefaultLogger` being the standard implementation.

### 1.1. `xylium.Logger` Interface

The `xylium.Logger` interface (defined in `types.go`) provides methods for leveled logging (Debug, Info, Warn, Error, Fatal, Panic) and structured logging:

```go
type Logger interface {
    Printf(format string, args ...interface{}) // Typically logs at Info level
    Debug(args ...interface{})
    Info(args ...interface{})
    Warn(args ...interface{})
    Error(args ...interface{})
    Fatal(args ...interface{}) // Logs then calls os.Exit(1)
    Panic(args ...interface{}) // Logs then calls panic()

    Debugf(format string, args ...interface{})
    Infof(format string, args ...interface{})
    Warnf(format string, args ...interface{})
    Errorf(format string, args ...interface{})
    Fatalf(format string, args ...interface{})
    Panicf(format string, args ...interface{})

    WithFields(fields M) Logger // Returns a new logger with added structured fields
    SetOutput(w io.Writer)
    SetLevel(level LogLevel)
    GetLevel() LogLevel
}
```
`xylium.M` is an alias for `map[string]interface{}`.

### 1.2. `xylium.DefaultLogger`

`xylium.DefaultLogger` is Xylium's built-in implementation. Key features:
*   Supports log levels.
*   Supports Text and JSON output formats.
*   Can show caller information (file and line number).
*   Provides colored output for Text format when writing to a TTY (typically in DebugMode).
*   Uses a `sync.Pool` for log entry buffers to reduce allocations.
*   Thread-safe.

## 2. Application-Level Logging (`app.Logger()`)

When you create a Xylium application instance (`app := xylium.New()`), it comes with a pre-configured logger accessible via `app.Logger()`. This logger is suitable for application-wide messages, such as startup information, general system events, or errors occurring outside a specific request context.

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New() // Logger is auto-configured based on Xylium mode

	// Use the application's base logger
	appLogger := app.Logger()
	appLogger.Infof("Application starting up in %s mode.", app.CurrentMode())

	// Example of using different log levels
	appLogger.Debug("This is a debug message for the app.") // Might not show in ReleaseMode
	appLogger.Warn("A potential issue has been detected during startup.")

	// ... define routes ...
	app.GET("/", func(c *xylium.Context) error { /* ... */ return nil })

	if err := app.Start(":8080"); err != nil {
		appLogger.Fatalf("Server failed to start: %v", err) // Fatalf logs and exits
	}
}
```
The configuration of `app.Logger()` (level, format, color, caller info) is automatically determined by Xylium's operating mode (Debug, Test, Release) unless a custom logger or specific `LoggerConfig` is provided during app initialization.

## 3. Request-Scoped Logging (`c.Logger()`)

Within a Xylium handler or middleware, `c.Logger()` provides a request-scoped logger. This logger is derived from `app.Logger()` but can be enriched with contextual information specific to the current request.

**Automatic Contextual Fields:**
*   **`xylium_request_id`**: If `xylium.RequestID()` middleware is used, this field is automatically added.
*   **`trace_id`**, **`span_id`**: If `xylium.Otel()` middleware is used for OpenTelemetry tracing, these fields are automatically added.

```go
// Inside a handler or middleware
func MyHandler(c *xylium.Context) error {
	// c.Logger() automatically includes fields like 'xylium_request_id'
	requestLogger := c.Logger()
	requestLogger.Infof("Processing request for path: %s", c.Path())

	userID := c.Param("userID")
	if userID != "" {
		// Add more context to the logger for this specific operation
		userScopedLogger := requestLogger.WithFields(xylium.M{"user_id": userID})
		userScopedLogger.Debug("Fetching data for user.")
		// ...
	}
	return c.String(http.StatusOK, "Processed.")
}
```
This ensures that logs related to a specific request are easily identifiable and correlated.

## 4. Structured Logging with Fields (`WithFields`)

Both `app.Logger()` and `c.Logger()` support structured logging via the `WithFields(fields xylium.M) Logger` method. This returns a *new* logger instance that will include the provided key-value pairs in all subsequent log entries.

```go
func ProcessOrderHandler(c *xylium.Context) error {
	orderID := c.Param("orderID")
	customerID := c.QueryParam("customerID")

	// Create a logger with specific fields for this operation
	opLogger := c.Logger().WithFields(xylium.M{
		"operation":   "process_order",
		"order_id":    orderID,
		"customer_id": customerID,
	})

	opLogger.Info("Starting order processing.")
	// ... order processing logic ...
	if err := someOrderServiceCall(); err != nil {
		opLogger.WithFields(xylium.M{"service_error": err.Error()}).Error("Order service call failed.")
		return xylium.NewHTTPError(http.StatusInternalServerError, "Failed to process order.")
	}

	opLogger.Info("Order processed successfully.")
	return c.String(http.StatusOK, "Order processed.")
}
```
When using the JSON formatter, these fields will appear as a nested JSON object. With the Text formatter, they are typically appended as a JSON string.

## 5. Log Levels

Xylium's `DefaultLogger` supports the following log levels, ordered from most verbose to most critical:

*   `xylium.LevelDebug`
*   `xylium.LevelInfo`
*   `xylium.LevelWarn`
*   `xylium.LevelError`
*   `xylium.LevelFatal` (logs the message then calls `os.Exit(1)`)
*   `xylium.LevelPanic` (logs the message then calls `panic()`)

The logger will only output messages that are at or above its configured `LogLevel`. For example, if the level is `LevelInfo`, `Debug` messages will be suppressed.

## 6. Configuring the Default Logger

If you use `xylium.DefaultLogger` (which is the default for `app.Logger()` if no custom logger is provided), you can configure its behavior.

### 6.1. Automatic Configuration via Operating Modes

As mentioned, Xylium's operating mode (`DebugMode`, `TestMode`, `ReleaseMode`) automatically sets sensible defaults for the logger:
*   **DebugMode**: `LevelDebug`, shows caller, uses color (if TTY).
*   **TestMode**: `LevelDebug`, shows caller, no color.
*   **ReleaseMode**: `LevelInfo`, no caller, no color.

This is often sufficient for many applications.

### 6.2. Manual Configuration (`xylium.LoggerConfig`)

You can provide a `xylium.LoggerConfig` when creating a Xylium app with `xylium.NewWithConfig()` to customize the `DefaultLogger` explicitly. This overrides parts of the mode-based automatic configuration.

```go
// In main.go
logCfg := xylium.DefaultLoggerConfig() // Start with defaults
logCfg.Level = xylium.LevelInfo
logCfg.Formatter = xylium.JSONFormatter // Use JSON output
logCfg.ShowCaller = true
logCfg.UseColor = false // Explicitly disable color even in DebugMode

serverCfg := xylium.DefaultServerConfig()
serverCfg.LoggerConfig = &logCfg // Assign the logger config

app := xylium.NewWithConfig(serverCfg)

app.Logger().Info("Application started with custom logger configuration.")
// Output will be JSON, Info level, with caller info, no color.
```
If `serverCfg.Logger` is set to a custom logger instance, `serverCfg.LoggerConfig` is ignored.

`xylium.LoggerConfig` fields:
*   `Level (LogLevel)`: The minimum log level.
*   `Formatter (FormatterType)`: `xylium.TextFormatter` or `xylium.JSONFormatter`.
*   `ShowCaller (bool)`: Whether to include file:line of the log call.
*   `UseColor (bool)`: Whether to use ANSI colors for TextFormatter (if output is TTY).
*   `Output (io.Writer)`: Where to write logs (default `os.Stdout`).

### 6.3. Setting Output, Level, Formatter, etc., Dynamically

You can also change some properties of a `DefaultLogger` instance after it has been created:

```go
logger := app.Logger() // Assuming app.Logger() returns a *xylium.DefaultLogger

// Change log level at runtime
logger.SetLevel(xylium.LevelDebug)

// Change output destination (e.g., to a file)
// logFile, _ := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
// if dl, ok := logger.(*xylium.DefaultLogger); ok { // Type assert to DefaultLogger
//     dl.SetOutput(logFile)
//     dl.EnableColor(false) // Disable color when writing to a file
// }

// Change formatter
if dl, ok := logger.(*xylium.DefaultLogger); ok {
    dl.SetFormatter(xylium.JSONFormatter)
}
```
Note: Methods like `SetFormatter`, `EnableCaller`, `EnableColor` are specific to `xylium.DefaultLogger` and require a type assertion if you have a `xylium.Logger` interface.

## 7. Using a Custom Logger Implementation

If `DefaultLogger` doesn't meet your needs (e.g., you want to integrate with a different logging library like Zap or Logrus), you can provide your own implementation of the `xylium.Logger` interface.

1.  **Create your custom logger struct and implement all methods of `xylium.Logger`.**
2.  **Provide an instance of your custom logger in `xylium.ServerConfig` when creating the app:**

```go
// Example with a hypothetical custom logger
// type MyCustomLogger struct { /* ... fields ... */ }
// func (m *MyCustomLogger) Infof(format string, args ...interface{}) { /* ... impl ... */ }
// ... implement all other xylium.Logger methods ...

// func NewMyCustomLogger() *MyCustomLogger { /* ... */ }

// In main.go
// customLogImpl := NewMyCustomLogger() // Your custom logger instance

// serverCfg := xylium.DefaultServerConfig()
// serverCfg.Logger = customLogImpl // Assign your custom logger

// app := xylium.NewWithConfig(serverCfg)

// app.Logger() will now return your customLogImpl.
// c.Logger() will also be derived from it.
```
When a custom logger is provided, Xylium's automatic mode-based configuration and `LoggerConfig` are bypassed for that logger. Your custom logger is responsible for its own configuration.

## 8. Log Output Formats

`xylium.DefaultLogger` supports two primary output formats:

### 8.1. Text Formatter (`xylium.TextFormatter`)

Human-readable, line-based format. Supports color and caller information.
**Example (DebugMode, TTY):**
```
2023-10-27T12:00:00.123Z [DEBUG] <main.go:42> Request received {"middleware":"SimpleRequestLogger","xylium_request_id":"uuid-abc-123"}
2023-10-27T12:00:00.125Z [INFO]  <main.go:50> User 'alice' logged in. {"component":"auth_service","user_id":"alice"}
```
*   Timestamp
*   Level (colored if `UseColor` is true and output is TTY)
*   Caller (file:line, colored gray, if `ShowCaller` is true)
*   Message
*   Fields (marshalled as JSON, colored purple, if any)

### 8.2. JSON Formatter (`xylium.JSONFormatter`)

Structured JSON output, ideal for log aggregation systems (ELK, Splunk, etc.).
**Example:**
```json
{"timestamp":"2023-10-27T12:00:00.123Z","level":"DEBUG","message":"Request received","fields":{"middleware":"SimpleRequestLogger","xylium_request_id":"uuid-abc-123"},"caller":"main.go:42"}
{"timestamp":"2023-10-27T12:00:00.125Z","level":"INFO","message":"User 'alice' logged in.","fields":{"component":"auth_service","user_id":"alice"},"caller":"main.go:50"}
```
*   `timestamp`: Log entry timestamp.
*   `level`: Log level string (e.g., "INFO").
*   `message`: The log message.
*   `fields` (optional): A JSON object containing all structured fields.
*   `caller` (optional): File and line number if `ShowCaller` is true.

By understanding these logging features, you can effectively monitor and debug your Xylium applications in various environments.
