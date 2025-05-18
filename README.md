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

*   **Blazing Fast Performance**: Built on `fasthttp`, one of Go's fastest HTTP engines. Utilizes `sync.Pool` for `xylium.Context` objects to minimize memory allocations and reduce GC overhead.
*   **Expressive & Intuitive API**: A feature-rich `xylium.Context` provides helpers for request parsing (path/query params, form data), response generation (JSON, XML, HTML, String, File, etc.), data binding, and validation. The API is designed to be familiar and reduce boilerplate.
*   **Idiomatic Go Context Integration**: Each `xylium.Context` carries a standard Go `context.Context` accessible via `c.GoContext()`. This enables seamless integration with libraries expecting `context.Context` for operations like database queries, external API calls, and facilitates cancellation, deadlines, and request-scoped value propagation throughout your application.
*   **Fast Radix Tree Routing**: Employs an efficient radix tree for routing, supporting static paths, named path parameters (e.g., `/users/:id`), and catch-all parameters (e.g., `/static/*filepath`). Routes are matched with clear prioritization: static > parameter > catch-all.
*   **Flexible Middleware**: Easily apply global, route group-specific, or individual route middleware using the standard `func(next xylium.HandlerFunc) xylium.HandlerFunc` pattern. Xylium includes a suite of common built-in middleware:
    *   **Logger**: Automatic request logging.
    *   **Gzip**: Response compression.
    *   **CORS**: Cross-Origin Resource Sharing header management.
    *   **CSRF**: Cross-Site Request Forgery protection with constant-time token comparison.
    *   **BasicAuth**: HTTP Basic Authentication.
    *   **RateLimiter**: Request rate limiting with configurable stores (in-memory default).
    *   **RequestID**: Injects a unique ID into each request for tracing.
    *   **Timeout**: Context-aware request timeout handling.
*   **Route Grouping**: Organize your routes effortlessly with path prefixes and apply group-scoped middleware, promoting modular application design.
*   **Centralized Error Handling**: Handlers return an `error`. Xylium's `GlobalErrorHandler` processes these errors centrally, allowing for consistent error responses and logging. Custom `xylium.HTTPError` provides full control over status codes and client-facing error messages. Panics are also recovered and handled gracefully.
*   **Operating Modes (Debug, Test, Release)**: Configure framework behavior (e.g., logging verbosity, error detail) for different environments via the `XYLIUM_MODE` environment variable or programmatically with `xylium.SetMode()`. **Debug mode is the default**, providing more verbose logging and detailed error information for development.
*   **Data Binding & Validation**: Easily bind request payloads (JSON, XML, Form, Query) to Go structs. Integrated validation powered by `go-playground/validator/v10` allows for struct field validation using tags.
*   **Comprehensive Server Configuration & Graceful Shutdown**:
    *   Full control over underlying `fasthttp.Server` settings via `xylium.ServerConfig`.
    *   Built-in graceful shutdown (`app.Start()` or `app.ListenAndServeGracefully()`) handles OS signals (SIGINT, SIGTERM), allowing active requests to complete before shutting down.
    *   Graceful shutdown also includes **automatic cleanup of internal resources**, such as default stores created by certain middleware (e.g., the in-memory store for the RateLimiter if not user-provided).
*   **Customizable & Contextual Logger**:
    *   Integrated, auto-configured logger based on Xylium's operating mode.
    *   `app.Logger()` for application-level logging (startup, general messages).
    *   `c.Logger()` for request-scoped logging, automatically including `request_id` (if `RequestID` middleware is used) and other contextual fields (e.g., `trace_id`, `span_id` if set in context).
    *   Supports detailed `xylium.LoggerConfig` for fine-grained control over `DefaultLogger` behavior (level, format, caller info, color).
    *   Can be entirely replaced with a custom `xylium.Logger` implementation.
*   **Static File Serving**: Simple and secure static file serving using `app.ServeFiles("/prefix", "./static-directory")`, with support for index files and configurable options.
*   **Minimalist yet Extendable Core**: Provides a strong, lean foundation without excessive "magic," making it easy to understand, extend, and integrate with other Go libraries.

## 💡 Philosophy

*   **Speed and Efficiency**: Leverage the power of `fasthttp` for high-throughput, low-latency applications. Minimize allocations and optimize critical paths.
*   **Developer Productivity**: Provide an API that is expressive, reduces boilerplate, and accelerates development cycles.
*   **Simplicity & Clarity**: Keep the core framework lean, easy to understand, and avoid overly complex abstractions.
*   **Flexibility & Customization**: Allow developers to customize key aspects like error handling, logging, validation, and server behavior to fit their specific needs.
*   **Robustness & Modern Go Practices**: Embrace idiomatic Go patterns, prioritize resource safety (e.g., `context.Context`, graceful shutdown), and provide tools for building reliable, production-ready applications.

## 🚀 Getting Started

### Prerequisites

*   Go version 1.24.2 or higher

### Installation

```bash
go get -u github.com/arwahdevops/xylium-core
```

### Simple Usage Example

Create a `main.go` file:

```go
package main

import (
	"net/http" // Standard Go HTTP status codes

	"github.com/arwahdevops/xylium-core/src/xylium"
	// "context" // Import if you plan to use c.GoContext() directly for advanced scenarios
	// "time"    // Import for context.WithTimeout example
)

func main() {
	// Initialize Xylium.
	// By default, Xylium starts in DebugMode.
	// The logger is auto-configured: DebugMode provides LevelDebug, caller info, and colors (if TTY).
	app := xylium.New()

	// Define a simple GET route for the root path.
	// Responds with JSON: {"message": "Hello, Xylium!", "mode": "debug"}
	app.GET("/", func(c *xylium.Context) error {
		// Access the Go context if needed for operations like database queries or external calls
		// goCtx := c.GoContext()
		// Example: db.QueryRowContext(goCtx, "SELECT ...")

		// Use the request-scoped logger. It will include 'request_id' if RequestID middleware is used.
		c.Logger().Infof("Serving root path. Request path: %s. Go context available: %v", c.Path(), c.GoContext() != nil)
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Hello, Xylium!",
			"mode":    c.RouterMode(), // c.RouterMode() gives the mode of the router handling this context
		})
	})

	// Define a route with a path parameter.
	// Example: GET /hello/John -> Responds with string: "Hello, John!"
	app.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		c.Logger().Infof("Greeting user: %s", name)
		// Example of deriving a new Go context with a deadline
		// goCtx, cancel := context.WithTimeout(c.GoContext(), 50*time.Millisecond)
		// defer cancel()
		// Use goCtx for operations that should respect this deadline
		return c.String(http.StatusOK, "Hello, %s!", name)
	})
	
	listenAddr := ":8080"
	// Use the application's base logger for startup messages.
	// This logger reflects the auto-configuration based on Xylium's operating mode.
	app.Logger().Infof("Server starting on http://localhost%s (Mode: %s)", listenAddr, app.CurrentMode())
	
	// Start the server. app.Start() provides graceful shutdown.
	// For fatal startup errors, app.Logger().Fatalf() is appropriate.
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

# To run in TestMode
XYLIUM_MODE=test go run main.go
```

You can then access:
*   `http://localhost:8080/`
*   `http://localhost:8080/hello/yourname`

### Operating Modes

Xylium supports different operating modes (`debug`, `test`, `release`) which can alter its behavior, such as logging verbosity, error message details to the client, and default configurations for some components. **The default mode is `debug`**.

You can set the mode in two ways, with the following precedence (highest to lowest):

1.  **Programmatically (Highest Priority):**
    Call `xylium.SetMode()` *before* creating your Xylium application instance (`xylium.New()`). This overrides any environment variable settings.
    ```go
    package main

    import "github.com/arwahdevops/xylium-core/src/xylium"

    func main() {
        xylium.SetMode(xylium.ReleaseMode) // or xylium.TestMode, xylium.DebugMode
        
        app := xylium.New() 
        // ... your application logic ...
        // app.CurrentMode() will now be "release"
        // app.Logger() will be configured for ReleaseMode.
    }
    ```
2.  **Environment Variable (Overrides internal default):**
    Set the `XYLIUM_MODE` environment variable before running your application:
    ```bash
    XYLIUM_MODE=release go run main.go
    ```
3.  **Internal Default (Lowest Priority):**
    If neither of the above is used, Xylium defaults to `DebugMode`.

You can check the current effective mode of a router instance using `app.CurrentMode()` or `c.RouterMode()` within a handler. Xylium's `DefaultLogger` is automatically configured based on this effective mode (e.g., log level, color output, caller info).

## 📖 Documentation

For more detailed examples and API usage, please refer to:
*   The files in the `examples/` directory within this repository (especially `unified_showcase.go` for a comprehensive demonstration).
*   The `Docs/Cookbook.md` file for concise syntax examples of common use-cases.
*   Source code comments, which provide in-depth explanations of specific functions, structs, and configurations.

(A full documentation website is planned for the future!)

## 🛠️ Contributing

Contributions are always welcome! Whether it's bug reports, feature requests, documentation improvements, or code contributions, your help is appreciated.

Please consider the following when contributing:
*   **Open an Issue:** For bugs or significant feature proposals, please open an issue first to discuss the problem or idea.
*   **Pull Requests:** For fixes and improvements, please submit a pull request.
    *   Ensure your code adheres to Go best practices and the existing style of the project.
    *   Write clear, concise commit messages.
    *   Add tests for any new functionality or bug fixes.
    *   Update documentation (README, examples, code comments) as necessary.

## 📜 License

Xylium is licensed under the [MIT License](LICENSE).
