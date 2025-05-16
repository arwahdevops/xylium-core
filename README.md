# Xylium 🚀

**Xylium is a high-performance Go web framework, built on [fasthttp](https://github.com/valyala/fasthttp), designed for speed, ease of use, and developer productivity.**

Xylium offers an expressive and familiar API (inspired by popular frameworks like Gin/Echo/Fiber) while leveraging the raw efficiency of fasthttp. If you're looking for `fasthttp`'s speed without sacrificing development comfort, Xylium might be for you!

## ✨ Key Features

*   **Blazing Fast Performance**: Built on `fasthttp`, one of Go's fastest HTTP engines. Utilizes `sync.Pool` for `Context` objects to minimize memory allocations.
*   **Expressive & Intuitive API**: A feature-rich `Context` object provides helpers for request parsing, response generation (JSON, XML, HTML, File, etc.), data binding, and validation.
*   **Fast Radix Tree Routing**: Efficient routing system supporting path parameters, wildcards (catch-all), and clear route prioritization (static > param > catch-all).
*   **Flexible Middleware**: Apply global, route group-specific, or individual route middleware using the standard `func(next HandlerFunc) HandlerFunc` pattern.
*   **Route Grouping**: Organize your routes effortlessly with path prefixes and group-scoped middleware.
*   **Centralized Error Handling**: Handlers return an `error`, which is elegantly processed by the `GlobalErrorHandler`. Custom `HTTPError` allows full control over error responses.
*   **Data Binding & Validation**: Easily bind request payloads (JSON, XML, Form, Query) to Go structs and validate them using the integrated `validator/v10`.
*   **Server Configuration Management**: Full control over `fasthttp.Server` settings via `ServerConfig`.
*   **Customizable Logger**: Integrate with your preferred logging solution through the `xylium.Logger` interface.
*   **Static File Serving**: Easy-to-use `ServeFiles` helper.
*   **Minimalist yet Extendable**: Provides a strong foundation without too much "magic," easily extendable to fit your needs.

## 💡 Philosophy

*   **Speed and Efficiency**: Leverage the power of `fasthttp` for high-performance applications.
*   **Developer Productivity**: Provide an API that reduces boilerplate and speeds up development.
*   **Simplicity**: Keep the core framework lean and easy to understand.
*   **Flexibility**: Allow customization in key areas like error handling, logging, and validation.

## 🚀 Getting Started

### Prerequisites

*   Go version 1.24 or higher.

### Installation

```bash
go get -u github.com/arwahdevops/xylium/src/xylium
# Or if you are using a different module structure:
# go get -u github.com/arwahdevops/xylium
```

### Simple Usage Example

```go
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/arwahdevops/xylium/src/xylium"
)

func main() {
	// Use Go's standard logger (satisfies xylium.Logger)
	appLogger := log.New(os.Stdout, "[MyAPP] ", log.LstdFlags)

	// Server configuration
	cfg := xylium.DefaultServerConfig()
	cfg.Logger = appLogger
	cfg.Name = "MyAwesomeAPI/1.0"

	// Create a new router
	r := xylium.NewWithConfig(cfg)

	// Global middleware
	r.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Powered-By", "Xylium")
			log.Printf("Request to: %s %s", c.Method(), c.Path())
			return next(c)
		}
	})

	// Simple route
	r.GET("/ping", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "pong"})
	})

	// Route with parameters
	r.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		return c.String(http.StatusOK, "Hello, %s!", name)
	})

	// Route group
	apiV1 := r.Group("/api/v1")
	{
		// Group-specific middleware
		apiV1.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
			return func(c *xylium.Context) error {
				// Example: simple authentication check
				if c.Header("Authorization") != "Bearer mysecrettoken" {
					return xylium.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
				}
				return next(c)
			}
		})

		apiV1.GET("/users", func(c *xylium.Context) error {
			users := []map[string]string{
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
			// Error will automatically be an HTTPError 400 with validation details
			return err
		}
		// Process new user...
		return c.JSON(http.StatusCreated, map[string]interface{}{
			"message": "User created",
			"user":    req,
		})
	})

	// Start the server
	addr := ":8080"
	appLogger.Printf("Xylium server running on %s", addr)
	if err := r.ListenAndServe(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
```

## 📖 Documentation

For more detailed API documentation, please refer to the [GoDoc](https://pkg.go.dev/github.com/arwahdevops/xylium).

## 🛠️ Contributing

Contributions are always welcome! Please open an *issue* for bugs or feature requests, or a *pull request* for fixes and improvements.

Please ensure to:
*   Write tests for your new code.
*   Update documentation if necessary.

## 📜 License

Xylium is licensed under the [MIT License](LICENSE).
---
