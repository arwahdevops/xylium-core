# Xylium Server Basics

This document covers the fundamental aspects of creating, configuring, and running a Xylium web server. It includes setting up a basic server, configuring ports, understanding operating modes, enabling HTTPS, and implementing graceful shutdown.

## Table of Contents

*   [1. Creating and Running a Basic Server](#1-creating-and-running-a-basic-server)
*   [2. Configuring Server Port](#2-configuring-server-port)
*   [3. Understanding and Setting Operating Modes](#3-understanding-and-setting-operating-modes)
    *   [3.1. Available Modes](#31-available-modes)
    *   [3.2. How Modes Affect Behavior](#32-how-modes-affect-behavior)
    *   [3.3. Setting the Operating Mode](#33-setting-the-operating-mode)
    *   [3.4. Checking the Current Mode](#34-checking-the-current-mode)
*   [4. Enabling HTTPS (TLS)](#4-enabling-https-tls)
    *   [4.1. Using Certificate Files](#41-using-certificate-files)
    *   [4.2. Using Embedded Certificates](#42-using-embedded-certificates)
*   [5. Graceful Shutdown](#5-graceful-shutdown)
    *   [5.1. How it Works](#51-how-it-works)
    *   [5.2. Implementation](#52-implementation)
    *   [5.3. Resource Cleanup](#53-resource-cleanup)
    *   [5.4. Configuration](#54-configuration)

---

## 1. Creating and Running a Basic Server

The simplest Xylium server involves initializing a new Xylium application, defining a route, and starting the server.

```go
package main

import (
	"net/http" // Standard Go HTTP status codes

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Initialize Xylium. By default, it starts in DebugMode.
	// The logger is auto-configured based on the mode.
	app := xylium.New()

	// Define a simple GET route.
	app.GET("/", func(c *xylium.Context) error {
		// Use the request-scoped logger.
		c.Logger().Infof("Serving root path. Request ID: %s", c.MustGet(xylium.ContextKeyRequestID)) // Assuming RequestID middleware is used
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Hello, Xylium!",
			"mode":    c.RouterMode(),
		})
	})

	listenAddr := ":8080"
	// Use the application's base logger for startup messages.
	app.Logger().Infof("Server starting on http://localhost%s (Mode: %s)", listenAddr, app.CurrentMode())

	// Start the server. app.Start() provides graceful shutdown.
	if err := app.Start(listenAddr); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```

To run this server:
1.  Save the code as `main.go`.
2.  Run `go get github.com/arwahdevops/xylium-core` (if you haven't already).
3.  Execute `go run main.go`.
4.  Access `http://localhost:8080/` in your browser or API client.

Xylium uses `app.Start(addr)` by default, which is an alias for `app.ListenAndServeGracefully(addr)`, providing built-in graceful shutdown capabilities (see [Section 5](#5-graceful-shutdown)).

## 2. Configuring Server Port

The server port is specified as part of the address string passed to `app.Start()`, `app.ListenAndServe()`, or other `ListenAndServe` variants.

```go
// Start server on port 8080 on all available network interfaces
app.Start(":8080")

// Start server on port 8000, only on localhost
app.Start("localhost:8000")

// Start server on a Unix domain socket (ensure proper permissions)
// app.Start("unix:/tmp/xylium.sock") // Fasthttp supports this
```

You can make the port configurable, for example, using environment variables:

```go
import (
	"os"
	// ... other imports
)

func main() {
	// ... app initialization ...

	port := os.Getenv("XYLIUM_PORT")
	if port == "" {
		port = "8080" // Default port
	}
	listenAddr := ":" + port

	app.Logger().Infof("Server starting on port %s", port)
	if err := app.Start(listenAddr); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```

## 3. Understanding and Setting Operating Modes

Xylium supports different operating modes that can alter its behavior, such as logging verbosity, error message details, and default configurations for some components.

### 3.1. Available Modes

Xylium defines the following modes as string constants:
*   `xylium.DebugMode` ("debug"): For development. Enables verbose logging (LevelDebug, caller info, colors if TTY), detailed error messages to the client. **This is the default mode.**
*   `xylium.TestMode` ("test"): For automated testing. Typically has verbose logging (LevelDebug, no color) but might disable other development aids.
*   `xylium.ReleaseMode` ("release"): For production. Configures less verbose logging (LevelInfo, no caller info/colors) and generic error messages to clients to avoid exposing internal details.

### 3.2. How Modes Affect Behavior

The primary impact of the operating mode is on:
1.  **Default Logger Configuration**:
    *   **DebugMode**: Log level set to `LevelDebug`, caller info enabled, colored output enabled (if TTY).
    *   **TestMode**: Log level set to `LevelDebug`, caller info enabled, colored output disabled.
    *   **ReleaseMode**: Log level set to `LevelInfo`, caller info disabled, colored output disabled.
2.  **Error Handling**:
    *   `defaultGlobalErrorHandler` may include more detailed debug information (e.g., internal error messages, panic values) in the JSON response to the client when in `DebugMode`. In `ReleaseMode`, responses are more generic.
3.  **Middleware Defaults**: Some middleware might have slightly different default behaviors based on the mode, though this is less common. For example, `CookieSecure` in `CSRFConfig` defaults to `true` but might be overridden for local HTTP development (often handled by explicit configuration rather than mode).

### 3.3. Setting the Operating Mode

The operating mode is determined with the following precedence (highest to lowest):

1.  **Programmatically via `xylium.SetMode()` (Highest Priority):**
    Call `xylium.SetMode(modeString)` *before* creating your Xylium application instance (`xylium.New()` or `xylium.NewWithConfig()`).
    ```go
    import "github.com/arwahdevops/xylium-core/src/xylium"

    func main() {
        xylium.SetMode(xylium.ReleaseMode) // Or xylium.TestMode, xylium.DebugMode

        app := xylium.New()
        // app.CurrentMode() will now be "release"
        // app.Logger() will be configured for ReleaseMode.
        // ...
    }
    ```

2.  **Environment Variable `XYLIUM_MODE` (Read at Router Initialization):**
    Set the `XYLIUM_MODE` environment variable. This is checked when `xylium.New()` or `xylium.NewWithConfig()` is called. This can override a value set by the environment variable at package initialization if, for example, `.env` files are loaded after package `init()` but before `xylium.New()`.
    ```bash
    XYLIUM_MODE=release go run main.go
    ```

3.  **Environment Variable `XYLIUM_MODE` (Read at Package Initialization):**
    The `XYLIUM_MODE` environment variable is also read when the `xylium` package is first imported. This allows setting the mode very early.

4.  **Internal Default (Lowest Priority):**
    If none of the above methods set the mode, Xylium defaults to `xylium.DebugMode`.

Xylium logs how the mode was determined during its bootstrap phase (e.g., "[XYLIUM-BOOTSTRAP] Mode set to 'debug' from internal default...").

### 3.4. Checking the Current Mode

You can retrieve the effective mode of a router instance:
*   `app.CurrentMode()`: Returns the mode of the application instance.
*   `c.RouterMode()`: Within a handler, returns the mode of the router handling the current context.
*   `xylium.Mode()`: Returns the current global Xylium mode.

```go
func MyHandler(c *xylium.Context) error {
    if c.RouterMode() == xylium.DebugMode {
        c.Logger().Debug("This is a debug mode specific log.")
    }
    // ...
    return c.String(http.StatusOK, "Mode is: "+c.RouterMode())
}
```

## 4. Enabling HTTPS (TLS)

Xylium supports HTTPS through `fasthttp`'s TLS capabilities.

### 4.1. Using Certificate Files

If you have a certificate and private key file:

```go
// In main.go
// ... app initialization ...
app.GET("/", func(c *xylium.Context) error {
    return c.String(http.StatusOK, "Hello, secure Xylium!")
})

certFile := "path/to/your/server.crt" // Or .pem
keyFile := "path/to/your/server.key"  // Or .pem
listenAddr := ":8443"

app.Logger().Infof("Starting HTTPS server on %s", listenAddr)
// Use ListenAndServeTLSGracefully for graceful shutdown with TLS
if err := app.ListenAndServeTLSGracefully(listenAddr, certFile, keyFile); err != nil {
    app.Logger().Fatalf("Error starting HTTPS server: %v", err)
}
```
Ensure `certFile` and `keyFile` paths are correct and readable by the application.

### 4.2. Using Embedded Certificates

You can also embed certificate and key data directly into your Go binary, for example, using Go 1.16's `embed` package.

```go
import (
	"embed"
	// ... other imports
)

//go:embed certs/server.crt
var certData []byte

//go:embed certs/server.key
var keyData []byte

func main() {
	// ... app initialization ...
	app.GET("/", func(c *xylium.Context) error {
		return c.String(http.StatusOK, "Hello, embedded TLS Xylium!")
	})

	listenAddr := ":8443"
	app.Logger().Infof("Starting HTTPS server with embedded certs on %s", listenAddr)

	// Use ListenAndServeTLSEmbedGracefully for graceful shutdown
	if err := app.ListenAndServeTLSEmbedGracefully(listenAddr, certData, keyData); err != nil {
		app.Logger().Fatalf("Error starting embedded HTTPS server: %v", err)
	}
}
```
This approach simplifies deployment as you don't need to manage separate certificate files alongside your executable.

## 5. Graceful Shutdown

Graceful shutdown allows your server to stop accepting new connections while giving active requests a chance to complete before the server process exits. This prevents abrupt disconnections and data loss.

### 5.1. How it Works

Xylium's graceful shutdown mechanism:
1.  Listens for OS interrupt signals (`syscall.SIGINT` for Ctrl+C, `syscall.SIGTERM` for termination requests).
2.  Upon receiving a signal, it initiates the shutdown of the underlying `fasthttp` server.
3.  `fasthttp` stops accepting new connections and waits for existing connections to complete, up to a certain timeout.
4.  Xylium then closes any internally managed resources (e.g., default `InMemoryStore` for `RateLimiter`).

### 5.2. Implementation

Xylium provides several methods for starting the server with graceful shutdown enabled:
*   `app.Start(addr string) error`: This is the recommended and simplest way. It's an alias for `app.ListenAndServeGracefully(addr)`.
*   `app.ListenAndServeGracefully(addr string) error`: Explicitly starts an HTTP server with graceful shutdown.
*   `app.ListenAndServeTLSGracefully(addr, certFile, keyFile string) error`: For HTTPS with certificate files.
*   `app.ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error`: For HTTPS with embedded certificates.

Example using `app.Start()`:
```go
// main.go
// ...
func main() {
    app := xylium.New()
    // ... define routes ...

    listenAddr := ":8080"
    app.Logger().Infof("Server starting gracefully on %s", listenAddr)

    if err := app.Start(listenAddr); err != nil { // Start() handles graceful shutdown
        app.Logger().Fatalf("Failed to start server: %v", err)
    }
    // Code here will only be reached after server has shut down.
    app.Logger().Info("Server has shut down gracefully.")
}
```

### 5.3. Resource Cleanup

During graceful shutdown, after the `fasthttp` server has attempted to shut down, Xylium calls an internal method `closeInternalResources()`. This method is responsible for cleaning up resources that were created and managed internally by Xylium's components.

Currently, this includes:
*   **Internally Created Rate Limiter Stores**: If you use `xylium.RateLimiter` without providing your own `LimiterStore` instance, Xylium creates a default `xylium.InMemoryStore`. This store's `Close()` method will be called during graceful shutdown to stop its cleanup goroutine and clear its data.

If you provide your own custom `LimiterStore` or other resources that need explicit cleanup, you are responsible for managing their lifecycle and closing them appropriately (e.g., using `defer` in your `main` function or custom shutdown hooks).

### 5.4. Configuration

Graceful shutdown behavior can be influenced by `xylium.ServerConfig`:

*   `ShutdownTimeout (time.Duration)`: This is a Xylium-specific timeout for the *entire* graceful shutdown process, including the `fasthttp` server shutdown and Xylium's internal resource cleanup. If the `fasthttp` server itself takes too long to shut down (e.g., due to very long-lived connections), this timeout ensures the application eventually exits.
    *   Default: 15 seconds (from `DefaultServerConfig()`).
    *   Example:
        ```go
        cfg := xylium.DefaultServerConfig()
        cfg.ShutdownTimeout = 30 * time.Second // Set app-level shutdown timeout
        app := xylium.NewWithConfig(cfg)
        // ...
        app.Start(":8080")
        ```

*   `CloseOnShutdown (bool)`: This is a `fasthttp.Server` option. If true (default), `fasthttp` actively closes client connections when `server.Shutdown()` is called. If false, it waits for them to complete naturally or hit their idle timeout. Xylium's `ShutdownTimeout` acts as an overarching limit.

By understanding these server basics, you can effectively launch, manage, and safely terminate your Xylium applications.
