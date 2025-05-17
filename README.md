# Xylium 🚀

**Xylium is a high-performance Go web framework, built on [fasthttp](https://github.com/valyala/fasthttp), designed for speed, ease of use, and developer productivity.**

Xylium offers an expressive and familiar API (inspired by popular frameworks like Gin/Echo/Fiber) while leveraging the raw efficiency of fasthttp. If you're looking for `fasthttp`'s speed without sacrificing development comfort, Xylium might be for you!

## ✨ Key Features

*   **Blazing Fast Performance**: Built on `fasthttp`, one of Go's fastest HTTP engines. Utilizes `sync.Pool` for `Context` objects to minimize memory allocations.
*   **Expressive & Intuitive API**: A feature-rich `Context` object provides helpers for request parsing, response generation (JSON, XML, HTML, File, etc.), data binding, and validation.
*   **Fast Radix Tree Routing**: Efficient routing system supporting path parameters, wildcards (catch-all), and clear route prioritization (static > param > catch-all).
*   **Flexible Middleware**: Apply global, route group-specific, or individual route middleware using the standard `func(next HandlerFunc) HandlerFunc` pattern. Includes common middleware like Logger, Gzip, CORS, CSRF, BasicAuth, RateLimiter, and RequestID.
*   **Route Grouping**: Organize your routes effortlessly with path prefixes and group-scoped middleware.
*   **Centralized Error Handling**: Handlers return an `error`, which is elegantly processed by the `GlobalErrorHandler`. Custom `HTTPError` allows full control over error responses.
*   **Operating Modes (Debug, Test, Release)**: Configure framework behavior for different environments. Debug mode provides more verbose logging and error details.
*   **Data Binding & Validation**: Easily bind request payloads (JSON, XML, Form, Query) to Go structs and validate them using the integrated `validator/v10`.
*   **Server Configuration Management**: Full control over `fasthttp.Server` settings via `ServerConfig`, including graceful shutdown.
*   **Customizable Logger**: Integrate with your preferred logging solution through the `xylium.Logger` interface.
*   **Static File Serving**: Easy-to-use `ServeFiles` helper.
*   **Minimalist yet Extendable**: Provides a strong foundation without too much "magic," easily extendable to fit your needs.

## 💡 Philosophy

*   **Speed and Efficiency**: Leverage the power of `fasthttp` for high-performance applications.
*   **Developer Productivity**: Provide an API that reduces boilerplate and speeds up development.
*   **Simplicity**: Keep the core framework lean and easy to understand.
*   **Flexibility**: Allow customization in key areas like error handling, logging, validation, and server behavior.

## 🚀 Getting Started

### Prerequisites

*   Go version 1.24.2 or higher.

### Installation

```bash
go get -u github.com/arwahdevops/xylium-core
```

### Operating Modes

Xylium supports different operating modes (`debug`, `test`, `release`) which can alter its behavior, such as logging verbosity and error message details. The default mode is `release`.

You can set the mode in two ways:

1.  **Environment Variable (Highest Priority at startup):**
    Set the `XYLIUM_MODE` environment variable before running your application:
    ```bash
    XYLIUM_MODE=debug go run main.go
    ```
2.  **Programmatically (Before Router Initialization):**
    Call `xylium.SetMode()` before creating your router instance:
    ```go
    package main

    import "github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path

    func main() {
        xylium.SetMode(xylium.DebugMode) // or xylium.TestMode, xylium.ReleaseMode
        
        // ... initialize your Xylium router ...
        r := xylium.New() 
        // ...
    }
    ```
    The router will then operate in the specified mode. You can check the current mode using `router.CurrentMode()`.

### Simple Usage Example

```go
package main

import (
	"log"
	"net/http"
	"os"
	// "time" // Not used in this minimal example, but often needed

	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path as per your project structure
)

func main() {
	// Optionally set the mode (defaults to "release" or XYLIUM_MODE env var)
	// xylium.SetMode(xylium.DebugMode)

	// Use Go's standard logger (satisfies xylium.Logger)
	appLogger := log.New(os.Stdout, "[MyAPP] ", log.LstdFlags)

	// Server configuration
	cfg := xylium.DefaultServerConfig()
	cfg.Logger = appLogger
	cfg.Name = "MyAwesomeAPI/1.0"

	// Create a new router
	r := xylium.NewWithConfig(cfg)
	appLogger.Printf("Xylium running in mode: %s", r.CurrentMode())


	// Global middleware
	r.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Powered-By", "Xylium")
			// Example of mode-dependent behavior in middleware
			if c.RouterMode() == xylium.DebugMode {
				log.Printf("[DEBUG] Request to: %s %s", c.Method(), c.Path())
			}
			return next(c)
		}
	})

	// Simple route
	r.GET("/ping", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, xylium.M{"message": "pong", "mode": c.RouterMode()})
	})

	// Route with parameters
	r.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		return c.String(http.StatusOK, "Hello, %s! (Mode: %s)", name, c.RouterMode())
	})

	// Route group
	apiV1 := r.Group("/api/v1")
	{
		// Group-specific middleware
		apiV1.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
			return func(c *xylium.Context) error {
				// Example: simple authentication check
				if c.Header("Authorization") != "Bearer mysecrettoken" {
					// In DebugMode, the GlobalErrorHandler might provide more detailed error info.
					return xylium.NewHTTPError(http.StatusUnauthorized, "Unauthorized access to API v1")
				}
				return next(c)
			}
		})

		apiV1.GET("/users", func(c *xylium.Context) error {
			users := []xylium.M{
				{"id": "1", "name": "Alice"},
				{"id": "2", "name": "Bob"},
			}
			return c.JSON(http.StatusOK, users)
		})
	}

	// Data binding and validation
	type CreateUserRequest struct {
		Username string `json:"username" validate:"required,min=3"`
		Email    string `json:"email" validate:"required,email"`
	}

	r.POST("/users", func(c *xylium.Context) error {
		var req CreateUserRequest
		if err := c.BindAndValidate(&req); err != nil {
			// Error will automatically be an HTTPError 400 with validation details.
			// The GlobalErrorHandler might show more info in DebugMode.
			return err
		}
		// Process new user...
		return c.JSON(http.StatusCreated, xylium.M{
			"message": "User created",
			"user":    req,
		})
	})

	// Start the server
	addr := ":8080"
	appLogger.Printf("Xylium server starting on %s", addr)
	// Use ListenAndServeGracefully for better shutdown handling
	if err := r.ListenAndServeGracefully(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
```

## 📖 Documentation

For now, please refer to the example files in the `examples/` directory and the source code comments for understanding API usage.

## 🛠️ Contributing

Contributions are always welcome! Please open an *issue* for bugs or feature requests, or a *pull request* for fixes and improvements.

Please ensure to:
*   Write tests for your new code.
*   Update documentation if necessary.

## 📜 License

Xylium is licensed under the [MIT License](LICENSE).
