# Xylium Advanced Configuration

Xylium offers several advanced configuration options that allow developers to fine-tune its behavior beyond the basic setup. This includes customizing the validation engine and deeply configuring the underlying `fasthttp` server.

## Table of Contents

*   [1. Custom Validator Instance](#1-custom-validator-instance)
    *   [1.1. Why Use a Custom Validator?](#11-why-use-a-custom-validator)
    *   [1.2. How to Set a Custom Validator](#12-how-to-set-a-custom-validator)
    *   [1.3. Example: Registering a Custom Validation Function](#13-example-registering-a-custom-validation-function)
*   [2. Advanced Fasthttp Server Settings (`xylium.ServerConfig`)](#2-advanced-fasthttp-server-settings-xyliumserverconfig)
    *   [2.1. Overview of `ServerConfig`](#21-overview-of-serverconfig)
    *   [2.2. Key `ServerConfig` Fields](#22-key-serverconfig-fields)
    *   [2.3. Example: Using `NewWithConfig`](#23-example-using-newwithconfig)

---

## 1. Custom Validator Instance

Xylium uses `go-playground/validator/v10` by default for struct validation when `c.BindAndValidate()` is called. While the default instance is suitable for many use cases, you might need to customize the validator.

### 1.1. Why Use a Custom Validator?

*   **Register Custom Validation Functions**: You may need to define and register your own validation tags (e.g., `validate:"is-awesome-string"`) with custom logic.
*   **Register Custom Type Validators**: Implement custom validation for specific data types.
*   **Modify Tag Name**: Change the struct tag name used by the validator (default is `validate`).
*   **Use a Differently Configured Validator**: You might have an existing `*validator.Validate` instance in your application with specific configurations (e.g., custom translations for error messages) that you want Xylium to use.

### 1.2. How to Set a Custom Validator

You can replace Xylium's default `*validator.Validate` instance by calling `xylium.SetCustomValidator(v *validator.Validate)` **before** you initialize your Xylium application (`xylium.New()` or `xylium.NewWithConfig()`).

```go
package main

import (
	"github.com/go-playground/validator/v10"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path
	// ... other imports
)

// myCustomValidationFunc is an example of a custom validation function.
func myCustomValidationFunc(fl validator.FieldLevel) bool {
	// Example: field must be the string "xylium_rocks"
	return fl.Field().String() == "xylium_rocks"
}

func main() {
	// 1. Create a new validator instance (or get your existing one)
	customValidator := validator.New()

	// 2. Register custom validations, type functions, etc., on this instance
	err := customValidator.RegisterValidation("must_be_xylium_rocks", myCustomValidationFunc)
	if err != nil {
		// Handle registration error (e.g., log and exit)
		panic("Failed to register custom validation: " + err.Error())
	}

	// 3. Set this as Xylium's global validator
	// THIS MUST BE CALLED BEFORE xylium.New() or xylium.NewWithConfig()
	xylium.SetCustomValidator(customValidator)

	// 4. Initialize your Xylium app
	app := xylium.New()

	// Define a struct that uses the custom validation tag
	type MyInput struct {
		SpecialField string `json:"special_field" validate:"required,must_be_xylium_rocks"`
	}

	app.POST("/validate-custom", func(c *xylium.Context) error {
		var input MyInput
		if err := c.BindAndValidate(&input); err != nil {
			// This will now use your customValidator, including 'must_be_xylium_rocks'
			return err // Let GlobalErrorHandler handle the *xylium.HTTPError
		}
		return c.JSON(http.StatusOK, xylium.M{"message": "Valid input!", "data": input})
	})

	app.Logger().Info("Starting server with custom validator.")
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Server error: %v", err)
	}
}
```
**Important**: `xylium.SetCustomValidator()` is a global setting. If called, the provided validator will be used for all `c.BindAndValidate()` calls across all router instances created thereafter in the application, unless `SetCustomValidator` is called again with a different instance.

### 1.3. Example: Registering a Custom Validation Function

The example above already demonstrates registering a custom validation function (`myCustomValidationFunc` for the tag `must_be_xylium_rocks`). Refer to the [go-playground/validator documentation](https://pkg.go.dev/github.com/go-playground/validator/v10) for more advanced customization options, such as:
*   Registering validation for specific types (`RegisterCustomTypeFunc`).
*   Registering struct-level validations (`RegisterStructValidation`).
*   Customizing error messages and translations.

## 2. Advanced Fasthttp Server Settings (`xylium.ServerConfig`)

When you create a Xylium application using `app := xylium.New()`, it uses a default server configuration (`xylium.DefaultServerConfig()`). For more control over the underlying `fasthttp.Server`, you can use `app := xylium.NewWithConfig(config xylium.ServerConfig)`.

### 2.1. Overview of `ServerConfig`

The `xylium.ServerConfig` struct (defined in `router_server.go`) allows you to configure various aspects of the `fasthttp.Server`.

```go
// Simplified from router_server.go
type ServerConfig struct {
    Name                          string        // Server name for "Server" header
    ReadTimeout                   time.Duration // Max duration for reading the entire request
    WriteTimeout                  time.Duration // Max duration for writing the_ entire response
    IdleTimeout                   time.Duration // Max duration to keep an idle keep-alive connection open
    MaxRequestBodySize            int           // Max request body size
    ReduceMemoryUsage             bool          // Reduces memory usage at the cost of higher CPU.
    Concurrency                   int           // Max number of concurrent connections
    DisableKeepalive              bool          // Disables keep-alive connections
    TCPKeepalive                  bool          // Enables TCP keep-alive periods
    TCPKeepalivePeriod            time.Duration // Duration for TCP keep-alive
    MaxConnsPerIP                 int           // Max concurrent connections from a single IP
    MaxRequestsPerConn            int           // Max requests per keep-alive connection
    GetOnly                       bool          // If true, only GET requests are accepted
    DisableHeaderNamesNormalizing bool          // If true, fasthttp won't normalize header names
    NoDefaultServerHeader         bool          // If true, "Server" header is not set automatically
    NoDefaultDate                 bool          // If true, "Date" header is not set
    NoDefaultContentType          bool          // If true, "Content-Type" is not set for text responses
    KeepHijackedConns             bool          // If true, hijacked connections are not closed on shutdown
    CloseOnShutdown               bool          // Fasthttp's option to close connections on shutdown
    StreamRequestBody             bool          // Whether to stream request bodies
    Logger                        Logger        // Xylium logger instance
    LoggerConfig                  *LoggerConfig // Detailed config for DefaultLogger if Logger is nil
    ConnState                     func(conn net.Conn, state fasthttp.ConnState) // Callback for connection state changes
    ShutdownTimeout               time.Duration // Xylium's app-level graceful shutdown timeout
}
```

### 2.2. Key `ServerConfig` Fields

*   **Timeouts (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ShutdownTimeout`)**: Crucial for server stability and resource management. `ShutdownTimeout` is Xylium's graceful shutdown timeout.
*   **Limits (`MaxRequestBodySize`, `Concurrency`, `MaxConnsPerIP`, `MaxRequestsPerConn`)**: Prevent abuse and manage server load.
*   **`Logger` / `LoggerConfig`**: Allows providing a custom logger implementation or fine-tuning the default Xylium logger. See `Logging.md` for details.
*   **`ConnState`**: A callback function that `fasthttp` calls when a connection's state changes (e.g., new connection, active, idle, hijacked). Useful for metrics or advanced connection management.
    ```go
    // Example ConnState callback
    // cfg.ConnState = func(conn net.Conn, state fasthttp.ConnState) {
    //  log.Printf("Connection %s changed state to: %s", conn.RemoteAddr().String(), state.String())
    //  // You could increment/decrement active connection counters here for metrics
    // }
    ```
*   **`ReduceMemoryUsage`**: If set to `true`, `fasthttp` tries to reduce memory allocations, which might slightly increase CPU usage. Test for your specific workload.
*   **Header Control (`DisableHeaderNamesNormalizing`, `NoDefaultServerHeader`, etc.)**: Fine-tune HTTP header behavior.

### 2.3. Example: Using `NewWithConfig`

```go
package main

import (
	"net/http"
	"time"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path
	// "github.com/valyala/fasthttp" // If you need fasthttp.ConnState constants
	// "net" // If you need net.Conn
)

func main() {
	// Customize Logger
	logCfg := xylium.DefaultLoggerConfig()
	logCfg.Level = xylium.LevelDebug
	logCfg.Formatter = xylium.JSONFormatter

	// Customize Server
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Name = "MyCustomXyliumApp/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.IdleTimeout = 90 * time.Second
	serverCfg.MaxRequestBodySize = 8 * 1024 * 1024 // 8 MB
	serverCfg.LoggerConfig = &logCfg               // Apply custom logger config
	serverCfg.ShutdownTimeout = 20 * time.Second   // App-level graceful shutdown timeout

	// Example ConnState (optional)
	// serverCfg.ConnState = func(conn net.Conn, state fasthttp.ConnState) {
	//  fmt.Printf("Conn %s State: %s\n", conn.RemoteAddr(), state)
	// }

	// Initialize Xylium with the custom server configuration
	app := xylium.NewWithConfig(serverCfg)

	app.GET("/", func(c *xylium.Context) error {
		c.Logger().Debugf("Serving root with custom server config. Server name in header: %s", serverCfg.Name)
		return c.JSON(http.StatusOK, xylium.M{"message": "Hello from highly configured Xylium!"})
	})

	app.Logger().Infof("Starting server with custom configuration on :8080. Server Name: %s", app.serverConfig.Name)
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Server error: %v", err)
	}
}
```

By leveraging these advanced configuration options, you can tailor Xylium to precisely meet the performance, security, and operational requirements of your specific application. Always refer to the `fasthttp` documentation for the most detailed explanations of its server options.
