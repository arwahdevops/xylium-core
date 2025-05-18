**Minimal Xylium Syntax Examples for Common Cases:**

**1. Basic "Hello World" Server:**

```go
package main

import (
	"net/http"
	// "log" // Standard log can often be replaced by app.Logger() for fatal startup errors

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Xylium's logger is auto-configured based on mode (XYLIUM_MODE env or SetMode).
	// DebugMode: LevelDebug, caller info, color (if TTY).
	// ReleaseMode: LevelInfo.
	app := xylium.New()

	app.GET("/", func(c *xylium.Context) error {
		// c.Logger() gets a request-scoped logger (includes request_id if RequestID middleware is used).
		c.Logger().Debugf("Serving 'Hello, Xylium!' for path: %s", c.Path())
		return c.String(http.StatusOK, "Hello, Xylium!")
	})

	// Use the application's base logger for startup messages.
	// This logger reflects the auto-configuration based on Xylium's mode.
	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		// For fatal startup errors, app.Logger().Fatalf() is appropriate.
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Initialization, one route, one string response, server start, and basic logging.
*   **DX:** Intuitive API, auto-configured logger simplifies setup.

**2. Route with Path Parameters:**

```go
package main

import (
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	app.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name") // Get path parameter
		// Log with context, including the parameter value.
		c.Logger().Infof("Greeting user '%s'.", name)
		return c.String(http.StatusOK, "Hello, %s!", name)
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
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	// Example: /search?query=xylium&limit=10
	app.GET("/search", func(c *xylium.Context) error {
		query := c.QueryParam("query")
		limit := c.QueryParamIntDefault("limit", 10)

		// Log with specific fields for this search operation.
		c.Logger().WithFields(xylium.M{
			"search_query": query,
			"search_limit": limit,
		}).Info("Search request processed.")

		return c.JSON(http.StatusOK, xylium.M{
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
*   **DX:** Typed helpers for query params and flexible logging.

**4. Binding Request Body (JSON) to Struct and Validation:**

```go
package main

import (
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

type CreateUserInput struct {
	Username string `json:"username" validate:"required,min=3"`
	Email    string `json:"email" validate:"required,email"`
}

func main() {
	app := xylium.New()

	app.POST("/users", func(c *xylium.Context) error {
		var input CreateUserInput

		if err := c.BindAndValidate(&input); err != nil {
			// BindAndValidate returns *xylium.HTTPError.
			// GlobalErrorHandler will log this error's details appropriately.
			// We can add a specific debug log here if needed.
			c.Logger().Debugf("Validation failed for user creation: %v", err)
			return err
		}

		c.Logger().WithFields(xylium.M{
			"new_username": input.Username,
			"new_email":    input.Email,
		}).Info("User created successfully after validation.")

		return c.JSON(http.StatusCreated, xylium.M{
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
*   **Minimal:** Focus on `c.BindAndValidate()` and logging successful operations. Error logging is largely handled by `GlobalErrorHandler`.
*   **DX:** Concise binding & validation.

**5. Simple Middleware (Global):**

```go
package main

import (
	"net/http"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Simple request timing middleware using c.Logger().
func RequestTimerMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		start := time.Now()
		// c.Logger() here will already have request_id if RequestID middleware is used before this.
		logger := c.Logger().WithFields(xylium.M{"middleware": "RequestTimer"}) // Add middleware context.

		logger.Debugf("Request received for %s %s", c.Method(), c.Path())

		err := next(c) // Call the next handler.

		latency := time.Since(start)
		logFields := xylium.M{
			"status_code": c.Ctx.Response.StatusCode(),
			"latency_ms":  latency.Milliseconds(),
		}

		if err != nil {
			logger.WithFields(logFields).Errorf("Request to %s %s failed after %s. Error: %v",
				c.Method(), c.Path(), latency, err)
		} else {
			logger.WithFields(logFields).Infof("Request to %s %s completed in %s.",
				c.Method(), c.Path(), latency)
		}
		return err
	}
}

func main() {
	app := xylium.New()

	// For c.Logger() in middleware to have request_id, RequestID should ideally be first.
	app.Use(xylium.RequestID()) // Recommended to place RequestID middleware early.
	app.Use(RequestTimerMiddleware)

	app.GET("/ping", func(c *xylium.Context) error {
		// This log will have request_id and middleware context (if any middleware adds more fields).
		c.Logger().Info("Ping handler executed.")
		return c.JSON(http.StatusOK, xylium.M{"message": "pong"})
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Shows a slightly more practical middleware with contextual logging.
*   **DX:** Demonstrates `c.Logger()` within middleware and chaining.

**6. Route Grouping:**
*(Logging in this example can be similar to individual route handlers, using `c.Logger()` within each handler.)*
```go
package main

import (
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	v1 := app.Group("/api/v1")
	{
		v1.GET("/users", func(c *xylium.Context) error {
			c.Logger().WithFields(xylium.M{"group": "v1", "resource": "users"}).Info("Fetching users.")
			return c.JSON(http.StatusOK, []xylium.M{
				{"id": 1, "name": "Alice"},
				{"id": 2, "name": "Bob"},
			})
		})

		v1.GET("/products", func(c *xylium.Context) error {
			c.Logger().WithFields(xylium.M{"group": "v1", "resource": "products"}).Info("Fetching products.")
			return c.JSON(http.StatusOK, []xylium.M{
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
*(Logging for successful cases or specific error conditions can be added. `GlobalErrorHandler` handles logging of returned errors.)*
```go
package main

import (
	"errors"
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func findUserByID(id string, logger xylium.Logger) (xylium.M, error) { // Pass logger for service-level logging
	logger = logger.WithFields(xylium.M{"service_func": "findUserByID", "lookup_id": id})
	if id == "1" {
		logger.Debug("User found in service.")
		return xylium.M{"id": 1, "name": "Alice"}, nil
	}
	logger.Warn("User not found in service.")
	return nil, errors.New("user_not_found_in_service") // Generic error
}

func main() {
	app := xylium.New()

	app.GET("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		handlerLogger := c.Logger().WithFields(xylium.M{"handler": "GetUserByID", "user_id_param": userID})
		handlerLogger.Info("Attempting to find user.")

		// Pass the request-scoped logger to the service function
		user, err := findUserByID(userID, handlerLogger)

		if err != nil {
			// GlobalErrorHandler will log the specifics of the HTTPError.
			// We log here that we are about to return a specific HTTPError.
			handlerLogger.Warnf("Service returned error, preparing 404 response. Original service error: %v", err)
			return xylium.NewHTTPError(http.StatusNotFound, "User with ID "+userID+" was not found by the service.")
			// Or, if you want GlobalErrorHandler to handle the original 'err' as a 500:
			// return err
		}

		handlerLogger.Info("User found and returned successfully.")
		return c.JSON(http.StatusOK, user)
	})

	app.Logger().Infof("Server starting on http://localhost:8080 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Focuses on error return paths. Logging demonstrates tracing through service calls.
*   **DX:** Flexible error handling.

**Regarding `app.Start()` (Confirmed):**

Xylium now uses `app.Start(":8080")` as a convenience alias for `ListenAndServeGracefully`. This is implemented in `router_server.go`.

```go
// In src/xylium/router_server.go (Xylium Core)
// ...
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
```
This makes server startup concise while retaining graceful shutdown.
