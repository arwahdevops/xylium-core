**Minimal Xylium Syntax Examples for Common Cases:**

These examples demonstrate the concise syntax of Xylium for common web development tasks, emphasizing ease of use and integration with Xylium's features like contextual logging.

**1. Basic "Hello World" Server:**

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Xylium's logger is auto-configured based on mode (XYLIUM_MODE env or xylium.SetMode()).
	// DebugMode (default): LevelDebug, caller info, color (if TTY).
	// ReleaseMode: LevelInfo.
	app := xylium.New()

	// It's good practice to add RequestID middleware for tracing.
	app.Use(xylium.RequestID())

	app.GET("/", func(c *xylium.Context) error {
		// c.Logger() gets a request-scoped logger.
		// It automatically includes 'xylium_request_id' if RequestID middleware is used.
		c.Logger().Debugf("Serving 'Hello, Xylium!' for path: %s", c.Path())
		return c.String(xylium.StatusOK, "Hello, Xylium!")
	})

	// Use the application's base logger for startup messages.
	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	// app.Start() includes graceful shutdown.
	if err := app.Start(":8080"); err != nil {
		// For fatal startup errors, app.Logger().Fatalf() is appropriate.
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Initialization, `RequestID` middleware, one route, one string response, server start, and basic logging.
*   **DX:** Intuitive API, auto-configured logger simplifies setup, request-scoped logging.

**2. Route with Path Parameters:**

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()
	app.Use(xylium.RequestID()) // For consistent logging with request IDs

	app.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name") // Get path parameter
		// Log with context, including the parameter value.
		c.Logger().Infof("Greeting user '%s'.", name)
		return c.String(xylium.StatusOK, "Hello, %s!", name)
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Focus on `c.Param()` and contextual logging.
*   **DX:** Clear parameter access and integrated logging.

**3. Getting Query Parameters:**

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()
	app.Use(xylium.RequestID())

	// Example: /search?query=xylium&limit=10
	app.GET("/search", func(c *xylium.Context) error {
		query := c.QueryParam("query")
		// Use Xylium's typed helper for query params with a default value.
		limit := c.QueryParamIntDefault("limit", 10) 

		// Log with specific fields for this search operation using structured logging.
		c.Logger().WithFields(xylium.M{
			"search_query": query,
			"search_limit": limit,
		}).Info("Search request processed.")

		return c.JSON(xylium.StatusOK, xylium.M{
			"searched_for": query,
			"limit_is":     limit,
		})
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Focus on `c.QueryParam()`, `c.QueryParamIntDefault()`, and structured logging with `WithFields`.
*   **DX:** Typed helpers for query params and flexible, contextual logging.

**4. Binding Request Body (JSON) to Struct and Validation:**

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

type CreateUserInput struct {
	Username string `json:"username" validate:"required,min=3"`
	Email    string `json:"email" validate:"required,email"`
}

func main() {
	app := xylium.New()
	app.Use(xylium.RequestID())

	app.POST("/users", func(c *xylium.Context) error {
		var input CreateUserInput

		if err := c.BindAndValidate(&input); err != nil {
			// BindAndValidate returns *xylium.HTTPError with details.
			// GlobalErrorHandler will log this error's specifics appropriately.
			// We can add a specific debug log here if needed for this handler.
			c.Logger().Debugf("Validation failed for user creation: %v", err)
			return err // Propagate the error to GlobalErrorHandler.
		}

		c.Logger().WithFields(xylium.M{
			"new_username": input.Username,
			"new_email":    input.Email,
		}).Info("User created successfully after validation.")

		return c.JSON(xylium.StatusCreated, xylium.M{
			"message": "User created successfully",
			"user":    input,
		})
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Focuses on `c.BindAndValidate()` and logging successful operations. Error logging for validation failures is largely handled by `GlobalErrorHandler`.
*   **DX:** Concise binding & validation, with structured error responses by default.

**5. Simple Middleware (Global):**

```go
package main

import (
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Simple request timing middleware using c.Logger().
func RequestTimerMiddleware() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			start := time.Now()
			// c.Logger() here will already have 'xylium_request_id' if RequestID middleware is used before this.
			// Adding middleware-specific context.
			logger := c.Logger().WithFields(xylium.M{"middleware": "RequestTimer"}) 

			logger.Debugf("Request received for %s %s", c.Method(), c.Path())

			err := next(c) // Call the next handler in the chain.

			latency := time.Since(start)
			statusCode := 0
			if c.Ctx != nil { // Guard against nil context if an early panic or issue occurs
			    statusCode = c.Ctx.Response.StatusCode()
			}
			
			logFields := xylium.M{
				"status_code": statusCode,
				"latency_ms":  latency.Milliseconds(),
			}

			if err != nil {
				logger.WithFields(logFields).Errorf("Request to %s %s failed after %s. Error: %v",
					c.Method(), c.Path(), latency, err)
			} else {
				logger.WithFields(logFields).Infof("Request to %s %s completed in %s.",
					c.Method(), c.Path(), latency)
			}
			return err // Propagate the error (or nil) from the next handler.
		}
	}
}

func main() {
	app := xylium.New()

	// For c.Logger() in middleware to have 'xylium_request_id', RequestID should ideally be first.
	app.Use(xylium.RequestID()) 
	app.Use(RequestTimerMiddleware)

	app.GET("/ping", func(c *xylium.Context) error {
		// This log will include 'xylium_request_id' and "middleware":"RequestTimer" if the above middleware ran.
		c.Logger().Info("Ping handler executed.")
		return c.JSON(xylium.StatusOK, xylium.M{"message": "pong"})
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Shows a practical middleware with contextual and structured logging.
*   **DX:** Demonstrates `c.Logger()` usage within middleware and the chaining mechanism.

**6. Route Grouping:**
*(Logging in this example uses `c.Logger()` within each handler, similar to individual route handlers. Group-specific middleware could also add shared log fields.)*
```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()
	app.Use(xylium.RequestID())

	v1 := app.Group("/api/v1")
	{
		v1.GET("/users", func(c *xylium.Context) error {
			c.Logger().WithFields(xylium.M{"group": "v1", "resource": "users"}).Info("Fetching users.")
			return c.JSON(xylium.StatusOK, []xylium.M{
				{"id": 1, "name": "Alice"},
				{"id": 2, "name": "Bob"},
			})
		})

		v1.GET("/products", func(c *xylium.Context) error {
			c.Logger().WithFields(xylium.M{"group": "v1", "resource": "products"}).Info("Fetching products.")
			return c.JSON(xylium.StatusOK, []xylium.M{
				{"id": 101, "name": "Laptop"},
				{"id": 102, "name": "Mouse"},
			})
		})
	}

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Demonstrates route grouping with simple contextual logging.
*   **DX:** Clear `Group()` API.

**7. Returning Errors from Handlers:**
*(Logging for successful cases or specific error conditions can be added by the handler. `GlobalErrorHandler` handles logging of returned errors from handlers/middleware.)*
```go
package main

import (
	"errors"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Assume this is a service function. It accepts a logger for its own internal logging.
func findUserByID(id string, logger xylium.Logger) (xylium.M, error) {
	// Enrich the logger with service-specific context.
	serviceLogger := logger.WithFields(xylium.M{"service_func": "findUserByID", "lookup_id": id})
	if id == "1" {
		serviceLogger.Debug("User found in service.")
		return xylium.M{"id": 1, "name": "Alice"}, nil
	}
	serviceLogger.Warn("User not found in service.")
	return nil, errors.New("user_not_found_in_service") // Return a generic error from service.
}

func main() {
	app := xylium.New()
	app.Use(xylium.RequestID())

	app.GET("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		// Enrich handler's logger with handler-specific context.
		handlerLogger := c.Logger().WithFields(xylium.M{"handler": "GetUserByID", "user_id_param": userID})
		handlerLogger.Info("Attempting to find user.")

		// Pass the enriched request-scoped logger to the service function.
		user, err := findUserByID(userID, handlerLogger)

		if err != nil {
			// Service returned an error.
			// The GlobalErrorHandler will log the specifics of the *xylium.HTTPError returned below.
			// Log here that we are about to return a specific HTTPError based on service error.
			handlerLogger.Warnf("Service returned error, preparing %d response. Original service error: %v", xylium.StatusNotFound, err)
			// Create and return a Xylium HTTPError for the client.
			// Include the original service error as the internal cause for detailed server-side logging.
			return xylium.NewHTTPError(xylium.StatusNotFound, "User with ID "+userID+" was not found by the service.").WithInternal(err)
			
			// Alternatively, if you want GlobalErrorHandler to treat the original 'err' from the service
			// as a generic 500 Internal Server Error:
			// return err
		}

		handlerLogger.Info("User found and returned successfully.")
		return c.JSON(xylium.StatusOK, user)
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Focuses on error return paths from handlers and how services can use propagated loggers.
*   **DX:** Flexible error handling, with clear separation of client-facing errors (`xylium.HTTPError`) and internal errors.

**Regarding `app.Start()` (Confirmed from previous analysis):**

Xylium uses `app.Start(":8080")` as a convenience alias for `ListenAndServeGracefully`. This is implemented in `router_server.go`.

```go
// In src/xylium/router_server.go (Xylium Core)
// ...
// Start is a convenience alias for `ListenAndServeGracefully`.
// It starts an HTTP server on the given network address `addr` and handles
// OS signals (SIGINT, SIGTERM) for a graceful shutdown.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
```
This makes server startup concise while retaining graceful shutdown capabilities.
