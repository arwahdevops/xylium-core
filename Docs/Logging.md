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
    *   [6.2. Manual Configuration (`xylium.ServerConfig.LoggerConfig`)](#62-manual-configuration-xyliumserverconfigloggerconfig)
    *   [6.3. Setting Output, Level, Formatter, etc., Dynamically on `DefaultLogger`](#63-setting-output-level-formatter-etc-dynamically-on-defaultlogger)
*   [7. Using a Custom Logger Implementation (`xylium.ServerConfig.Logger`)](#7-using-a-custom-logger-implementation-xyliumserverconfiglogger)
*   [8. Log Output Formats](#8-log-output-formats)
    *   [8.1. Text Formatter](#81-text-formatter)
    *   [8.2. JSON Formatter](#82-json-formatter)

---

## 1. Overview of Xylium's Logger

Xylium's logging is built around the `xylium.Logger` interface, with `xylium.DefaultLogger` being the standard implementation.

### 1.1. `xylium.Logger` Interface

The `xylium.Logger` interface (defined in `types.go`) provides methods for leveled logging (Debug, Info, Warn, Error, Fatal, Panic) and structured logging:

```go
// From src/xylium/types.go
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
*   Supports configurable log levels.
*   Supports Text and JSON output formats.
*   Can show caller information (file and line number).
*   Provides colored output for Text format when writing to a TTY (typically in DebugMode, configurable).
*   Uses a `sync.Pool` for log entry buffers to reduce allocations.
*   Thread-safe.

## 2. Application-Level Logging (`app.Logger()`)

When you create a Xylium application instance (`app := xylium.New()` or `app := xylium.NewWithConfig(...)`), it comes with a pre-configured logger accessible via `app.Logger()`. This logger is suitable for application-wide messages, such as startup information, general system events, or errors occurring outside a specific request context.

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Logger is auto-configured based on Xylium mode (e.g., XYLIUM_MODE env var or xylium.SetMode()).
	// If specific LoggerConfig is provided via xylium.NewWithConfig(), that further refines the logger.
	app := xylium.New() 

	// Use the application's base logger
	appLogger := app.Logger()
	appLogger.Infof("Application starting up in '%s' mode.", app.CurrentMode())

	// Example of using different log levels
	appLogger.Debug("This is a debug message for the app.") // Might not show in ReleaseMode by default.
	appLogger.Warn("A potential issue has been detected during startup.")

	// ... define routes ...
	app.GET("/", func(c *xylium.Context) error { 
		c.Logger().Info("Root handler called.")
		return c.String(xylium.StatusOK, "Hello from Xylium app logger example") 
	})

	if err := app.Start(":8080"); err != nil {
		appLogger.Fatalf("Server failed to start: %v", err) // Fatalf logs and exits
	}
}
```
The configuration of `app.Logger()` (level, format, color, caller info) is determined by a combination of Xylium's operating mode and any `LoggerConfig` provided during app initialization with `xylium.NewWithConfig()`. See [Section 6](#6-configuring-the-default-logger) for details.

## 3. Request-Scoped Logging (`c.Logger()`)

Within a Xylium handler or middleware, `c.Logger()` provides a request-scoped logger. This logger is derived from `app.Logger()` but is automatically enriched with contextual information specific to the current request if corresponding middleware are used.

**Automatic Contextual Fields:**
*   **`xylium_request_id`**: If `xylium.RequestID()` middleware is used, this field (using `xylium.ContextKeyRequestID`) is automatically added.
*   **`trace_id`**, **`span_id`**: If `xylium.Otel()` middleware (from `xylium-otel` connector) is used for OpenTelemetry tracing, these fields are automatically added.

```go
// Inside a handler or middleware
// import "net/http" // No, use xylium.StatusOK

func MyHandler(c *xylium.Context) error {
	// c.Logger() automatically includes fields like 'xylium_request_id'
	// if the RequestID middleware is active.
	requestLogger := c.Logger()
	requestLogger.Infof("Processing request for path: %s", c.Path())

	userID := c.Param("userID")
	if userID != "" {
		// Add more context to the logger for this specific operation
		userScopedLogger := requestLogger.WithFields(xylium.M{"user_id": userID})
		userScopedLogger.Debug("Fetching data for user.")
		// ...
	}
	return c.String(xylium.StatusOK, "Processed.")
}
```
This ensures that logs related to a specific request are easily identifiable and correlated.

## 4. Structured Logging with Fields (`WithFields`)

Both `app.Logger()` and `c.Logger()` (if they are `*xylium.DefaultLogger` or implement `WithFields` similarly) support structured logging via the `WithFields(fields xylium.M) Logger` method. This returns a *new* logger instance that will include the provided key-value pairs in all subsequent log entries.

```go
// import "net/http" // No, use xylium.StatusInternalServerError, xylium.StatusOK

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
	// Assume someOrderServiceCall() is defined
	// if err := someOrderServiceCall(); err != nil {
	//	opLogger.WithFields(xylium.M{"service_error": err.Error()}).Error("Order service call failed.")
	//	return xylium.NewHTTPError(xylium.StatusInternalServerError, "Failed to process order.")
	// }

	opLogger.Info("Order processed successfully.")
	return c.String(xylium.StatusOK, "Order processed.")
}
```
When using the JSON formatter, these fields will typically appear as a nested JSON object (e.g., under a "fields" key). With the Text formatter, they are usually appended as a JSON string representation of the fields map.

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

If you use `xylium.DefaultLogger` (which is the default for `app.Logger()` if no custom logger is provided via `ServerConfig.Logger`), you can configure its behavior.

### 6.1. Automatic Configuration via Operating Modes

Xylium's operating mode (`DebugMode`, `TestMode`, `ReleaseMode`) sets initial defaults for the `DefaultLogger` if one is being created by Xylium:
*   **DebugMode**: Log level `LevelDebug`, `ShowCaller` true, `UseColor` true (if output is TTY).
*   **TestMode**: Log level `LevelDebug`, `ShowCaller` true, `UseColor` false.
*   **ReleaseMode**: Log level `LevelInfo`, `ShowCaller` false, `UseColor` false.

### 6.2. Manual Configuration (`xylium.ServerConfig.LoggerConfig`)

When creating a Xylium app with `xylium.NewWithConfig()`, you can provide a `xylium.LoggerConfig` via `ServerConfig.LoggerConfig`. This allows you to fine-tune the `DefaultLogger` that Xylium creates **if `ServerConfig.Logger` itself is `nil`**.

The settings in `LoggerConfig` will **override** the defaults set by the operating mode.

```go
// In main.go
// import "os" // For os.Stdout if needed

func main() {
	// Define custom logger configuration
	logCfg := xylium.DefaultLoggerConfig() // Start with Xylium's base defaults
	logCfg.Level = xylium.LevelInfo
	logCfg.Formatter = xylium.JSONFormatter // Use JSON output
	logCfg.ShowCaller = true
	// logCfg.UseColor = false // Explicitly disable color if needed
	// logCfg.Output = os.Stdout // Default, or set to a file, etc.

	// Define server configuration
	serverCfg := xylium.DefaultServerConfig()
	// If serverCfg.Logger is nil (which it is by default), Xylium will create a DefaultLogger.
	// The LoggerConfig below will then be applied to that DefaultLogger.
	serverCfg.LoggerConfig = &logCfg 

	app := xylium.NewWithConfig(serverCfg)

	app.Logger().Info("Application started with custom logger configuration.")
	// Output will be JSON, Info level (or as set by logCfg.Level), with caller info if logCfg.ShowCaller is true.
	// ...
	// app.Start(":8080")
}
```

`xylium.LoggerConfig` fields:
*   `Level (LogLevel)`: The minimum log level.
*   `Formatter (FormatterType)`: `xylium.TextFormatter` or `xylium.JSONFormatter`.
*   `ShowCaller (bool)`: Whether to include file:line of the log call.
*   `UseColor (bool)`: Whether to use ANSI colors for TextFormatter (effective if `Output` is a TTY).
*   `Output (io.Writer)`: Where to write logs (default `os.Stdout`).

If `ServerConfig.Logger` is set to a custom logger instance (see [Section 7](#7-using-a-custom-logger-implementation-xyliumserverconfiglogger)), `ServerConfig.LoggerConfig` is **ignored**.

### 6.3. Setting Output, Level, Formatter, etc., Dynamically on `DefaultLogger`

If you have an instance of `*xylium.DefaultLogger`, you can change some of its properties at runtime using its specific methods.

```go
// Assume app.Logger() returns a *xylium.DefaultLogger or can be asserted to it.
// defaultLog, ok := app.Logger().(*xylium.DefaultLogger)
// if !ok {
//     app.Logger().Warn("app.Logger() is not a *xylium.DefaultLogger, cannot change settings dynamically.")
//     return
// }

// Change log level at runtime
// defaultLog.SetLevel(xylium.LevelDebug)

// Change output destination (e.g., to a file)
// import "os"
// logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
// if err == nil {
//     defaultLog.SetOutput(logFile)
//     defaultLog.EnableColor(false) // Typically disable color when writing to a file.
// } else {
//	   app.Logger().Errorf("Failed to open log file: %v", err)
// }


// Change formatter
// defaultLog.SetFormatter(xylium.JSONFormatter)
```
Note: Methods like `SetFormatter`, `EnableCaller`, `EnableColor` are specific to `*xylium.DefaultLogger`. If you only have a `xylium.Logger` interface, you'll need to type-assert to access them.

## 7. Using a Custom Logger Implementation (`xylium.ServerConfig.Logger`)

If `DefaultLogger` doesn't meet your needs (e.g., you want to integrate with a different logging library like Zap or Logrus), you can provide your own implementation of the `xylium.Logger` interface.

1.  **Create your custom logger struct and implement all methods of `xylium.Logger`.**
2.  **Provide an instance of your custom logger in `ServerConfig.Logger` when creating the app:**

```go
// Example with a hypothetical custom logger
// type MyCustomLogger struct { /* ... fields for your chosen logging library ... */ }
// func (m *MyCustomLogger) Infof(format string, args ...interface{}) { /* ... your impl ... */ }
// // ... implement all other xylium.Logger methods ...

// func NewMyCustomLogger() *MyCustomLogger { /* ... initialize your logger ... */ }

// In main.go
// myCustomLogImpl := NewMyCustomLogger() // Your custom logger instance

// serverCfg := xylium.DefaultServerConfig()
// serverCfg.Logger = myCustomLogImpl // Assign your custom logger instance here

// app := xylium.NewWithConfig(serverCfg)

// app.Logger() will now return your myCustomLogImpl.
// c.Logger() in handlers will also be derived from it (if your custom logger's WithFields creates a new instance).
```
When a custom logger is provided via `ServerConfig.Logger`, Xylium's automatic mode-based configuration and `ServerConfig.LoggerConfig` are **bypassed** for that logger. Your custom logger is entirely responsible for its own configuration and behavior.

## 8. Log Output Formats

`xylium.DefaultLogger` supports two primary output formats:

### 8.1. Text Formatter (`xylium.TextFormatter`)

Human-readable, line-based format. Supports color and caller information.
**Example (DebugMode, TTY, ShowCaller true):**
```
2023-10-27T12:00:00.123Z [DEBUG] <main.go:42> Request received {"middleware":"SimpleRequestLogger","xylium_request_id":"uuid-abc-123"}
2023-10-27T12:00:00.125Z [INFO]  <main.go:50> User 'alice' logged in. {"component":"auth_service","user_id":"alice"}
```
*   Timestamp
*   Level (colored if `UseColor` is true and output is TTY)
*   Caller (file:line, colored gray, if `ShowCaller` is true)
*   Message
*   Fields (marshalled as a JSON string, colored purple, if any and `UseColor` is true)

### 8.2. JSON Formatter (`xylium.JSONFormatter`)

Structured JSON output, ideal for log aggregation systems (ELK, Splunk, CloudWatch Logs, etc.).
**Example (ShowCaller true):**
```json
{"timestamp":"2023-10-27T12:00:00.123Z","level":"DEBUG","message":"Request received","fields":{"middleware":"SimpleRequestLogger","xylium_request_id":"uuid-abc-123"},"caller":"main.go:42"}
{"timestamp":"2023-10-27T12:00:00.125Z","level":"INFO","message":"User 'alice' logged in.","fields":{"component":"auth_service","user_id":"alice"},"caller":"main.go:50"}
```
*   `timestamp`: Log entry timestamp.
*   `level`: Log level string (e.g., "INFO", "DEBUG").
*   `message`: The log message.
*   `fields` (optional): A JSON object containing all structured fields added via `WithFields` or passed as `xylium.M` to log methods.
*   `caller` (optional): File and line number if `ShowCaller` is true.

By understanding these logging features, you can effectively monitor and debug your Xylium applications in various environments.
