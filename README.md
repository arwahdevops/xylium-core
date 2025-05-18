# Xylium 🚀

**Xylium is a high-performance Go web framework, built on [fasthttp](https://github.com/valyala/fasthttp), designed for speed, ease of use, developer productivity, and robust application development.**

Xylium offers an expressive and familiar API (inspired by popular frameworks like Gin/Echo/Fiber) while leveraging the raw efficiency of fasthttp. If you're looking for `fasthttp`'s speed without sacrificing development comfort and modern Go practices, Xylium might be for you!

## Table of Contents

*   [✨ Key Features](#-key-features)
*   [💡 Philosophy](#-philosophy)
*   [🚀 Getting Started](#-getting-started)
    *   [Prerequisites](#prerequisites)
    *   [Installation](#installation)
    *   [Simple Usage Example](#simple-usage-example)
    *   [Operating Modes](#operating-modes)
*   [📖 Documentation](#-documentation)
*   [🛠️ Contributing](#️-contributing)
*   [📜 License](#-license)

## ✨ Key Features

*   **Blazing Fast Performance**: Built on `fasthttp`, one of Go's fastest HTTP engines. Utilizes `sync.Pool` for `Context` objects to minimize memory allocations.
*   **Expressive & Intuitive API**: A feature-rich `Context` object provides helpers for request parsing, response generation (JSON, XML, HTML, File, etc.), data binding, and validation.
*   **Idiomatic Go Context Integration**: Each `xylium.Context` carries a standard Go `context.Context` (`c.GoContext()`), enabling seamless integration with libraries expecting it, and facilitating cancellation, deadlines, and request-scoped value propagation.
*   **Fast Radix Tree Routing**: Efficient routing system supporting path parameters, wildcards (catch-all), and clear route prioritization (static > param > catch-all).
*   **Flexible Middleware**: Apply global, route group-specific, or individual route middleware using the standard `func(next HandlerFunc) HandlerFunc` pattern. Includes common middleware like Logger, Gzip, CORS, CSRF (with constant-time token comparison), BasicAuth, RateLimiter, RequestID, and Timeout (context-aware).
*   **Route Grouping**: Organize your routes effortlessly with path prefixes and group-scoped middleware.
*   **Centralized Error Handling**: Handlers return an `error`, which is elegantly processed by the `GlobalErrorHandler`. Custom `HTTPError` allows full control over error responses.
*   **Operating Modes (Debug, Test, Release)**: Configure framework behavior for different environments. **Debug mode is the default**, providing more verbose logging and error details. Xylium's logger is auto-configured based on these modes.
*   **Data Binding & Validation**: Easily bind request payloads (JSON, XML, Form, Query) to Go structs and validate them using the integrated `validator/v10`.
*   **Server Configuration Management**: Full control over `fasthttp.Server` settings via `ServerConfig`, including graceful shutdown with managed cleanup of internal resources (e.g., default rate limiter stores).
*   **Customizable Logger**: Integrated, auto-configured logger with `c.Logger()` for request-scoped logging (including request/trace/span IDs) and `app.Logger()` for application-level logging. Supports detailed `LoggerConfig` for fine-grained control over `DefaultLogger` behavior, or can be replaced with custom `xylium.Logger` implementations.
*   **Static File Serving**: Easy-to-use `ServeFiles` helper with built-in security.
*   **Minimalist yet Extendable**: Provides a strong foundation without too much "magic," easily extendable to fit your needs.

## 💡 Philosophy

*   **Speed and Efficiency**: Leverage the power of `fasthttp` for high-performance applications.
*   **Developer Productivity**: Provide an API that reduces boilerplate and speeds up development.
*   **Simplicity**: Keep the core framework lean and easy to understand.
*   **Flexibility**: Allow customization in key areas like error handling, logging, validation, and server behavior.
*   **Robustness & Modern Go Practices**: Embrace idiomatic Go patterns like `context.Context` and ensure resource safety for building reliable, production-ready applications.

## 🚀 Getting Started

### Prerequisites

*   Go version 1.24.2 or higher.

### Installation

```bash
go get -u github.com/arwahdevops/xylium-core
```

### Simple Usage Example

Create a `main.go` file:

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium"
	// "context" // Import if you plan to use c.GoContext() directly
	// "time"    // Import for context.WithTimeout example
)

func main() {
	// Initialize Xylium.
	// By default, Xylium starts in DebugMode.
	// Logger is auto-configured: DebugMode (default) provides LevelDebug, caller info, and colors (if TTY).
	app := xylium.New()

	// Define a simple GET route for the root path.
	// Responds with JSON: {"message": "Hello, Xylium!", "mode": "debug"}
	app.GET("/", func(c *xylium.Context) error {
		// Access the Go context if needed for operations like database queries or external calls
		// goCtx := c.GoContext()
		// Example: db.QueryRowContext(goCtx, "SELECT ...")

		c.Logger().Infof("Serving root path with Go context: %v", c.GoContext() != nil) // Example logging
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Hello, Xylium!",
			"mode":    c.RouterMode(),
		})
	})

	// Define a route with a path parameter.
	// Example: GET /hello/John -> Responds with string: "Hello, John!"
	app.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		// Example of deriving a new Go context with a deadline
		// goCtx, cancel := context.WithTimeout(c.GoContext(), 50*time.Millisecond)
		// defer cancel()
		// Use goCtx for operations that should respect this deadline
		return c.String(http.StatusOK, "Hello, %s!", name)
	})
	
	listenAddr := ":8080"
	// Use the application's logger for startup messages.
	// It reflects the auto-configuration based on Xylium's operating mode.
	app.Logger().Infof("Server starting on http://localhost%s (Mode: %s)", listenAddr, app.CurrentMode())
	
	// Start the server. app.Start() provides graceful shutdown.
	if err := app.Start(listenAddr); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```

Run the application:

```bash
# Run the application (defaults to DebugMode)
go run main.go

# To run in ReleaseMode (info-level logging, no caller info/colors)
XYLIUM_MODE=release go run main.go
```

You can then access:
*   `http://localhost:8080/`
*   `http://localhost:8080/hello/yourname`

### Operating Modes

Xylium supports different operating modes (`debug`, `test`, `release`) which can alter its behavior, such as logging verbosity and error message details. **The default mode is `debug`**.

You can set the mode in two ways:

1.  **Environment Variable (Overrides default, but `SetMode` has higher priority):**
    Set the `XYLIUM_MODE` environment variable before running your application:
    ```bash
    XYLIUM_MODE=release go run main.go
    ```
2.  **Programmatically (Highest Priority, before Router Initialization):**
    Call `xylium.SetMode()` before creating your Xylium application instance:
    ```go
    package main

    import "github.com/arwahdevops/xylium-core/src/xylium"

    func main() {
        xylium.SetMode(xylium.ReleaseMode) // or xylium.TestMode
        
        app := xylium.New() 
        // ... your application logic ...
        // app.CurrentMode() will now be "release"
    }
    ```
    The application will then operate in the specified mode. You can check the current mode using `app.CurrentMode()`.

## 📖 Documentation

For more detailed examples and API usage, please refer to:
*   The files in the `examples/` directory within this repository (especially `unified_showcase.go`).
*   The `Docs/Minimal.md` file for common use-case syntax.
*   Source code comments for in-depth understanding of specific functions and configurations.

(Full documentation website coming soon!)

## 🛠️ Contributing

Contributions are always welcome! Please open an *issue* for bugs or feature requests, or a *pull request* for fixes and improvements.

Please ensure to:
*   Write tests for your new code.
*   Update documentation if necessary.

## 📜 License

Xylium is licensed under the [MIT License](LICENSE).
