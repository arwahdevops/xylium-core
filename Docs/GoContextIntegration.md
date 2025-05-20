# Xylium Go Context Integration

Xylium is designed to work seamlessly with Go's standard `context.Context`. Each `xylium.Context` (the request context) embeds and manages a Go `context.Context`, enabling robust handling of deadlines, cancellation signals, and request-scoped values throughout your application, including interactions with downstream services.

## Table of Contents

*   [1. Overview of Go `context.Context` in Xylium](#1-overview-of-go-contextcontext-in-xylium)
*   [2. Accessing the Go Context (`c.GoContext()`)](#2-accessing-the-go-context-c.gocontext)
*   [3. Propagating Go Context to Downstream Services](#3-propagating-go-context-to-downstream-services)
*   [4. Using `context.WithTimeout` or `context.WithCancel` in Handlers](#4-using-contextwithtimeout-or-contextwithcancel-in-handlers)
    *   [4.1. Timeout Example](#41-timeout-example)
    *   [4.2. Cancellation Example](#42-cancellation-example)
*   [5. Xylium Middleware and Go Context](#5-xylium-middleware-and-go-context)
    *   [5.1. Timeout Middleware](#51-timeout-middleware)
    *   [5.2. OpenTelemetry Middleware](#52-opentelemetry-middleware)
*   [6. Replacing the Go Context in `xylium.Context` (`c.WithGoContext()`)](#6-replacing-the-go-context-in-xyliumcontext-c.withgocontext)
*   [7. Passing Request-Scoped Values via Go Context (Advanced)](#7-passing-request-scoped-values-via-go-context-advanced)

---

## 1. Overview of Go `context.Context` in Xylium

The standard Go `context.Context` is a powerful tool for managing request lifecycles, especially in concurrent and distributed systems. It provides:
*   **Cancellation Signals**: Allows different parts of an application (e.g., a handler and its downstream calls) to be notified if an operation should be aborted (e.g., client disconnects, timeout).
*   **Deadlines**: Sets a time by which an operation must complete.
*   **Request-Scoped Values**: A way to carry request-specific data across API boundaries and between goroutines, although this should be used sparingly for truly request-scoped data, not for passing optional parameters to functions.

Xylium integrates this by:
1.  Initializing each `xylium.Context` with a base Go `context.Context` (typically `context.Background()` or one derived by middleware).
2.  Providing `c.GoContext()` to access this Go context.
3.  Allowing middleware (like Timeout or OpenTelemetry) to derive new Go contexts (e.g., with timeouts or trace information) and associate them with the `xylium.Context` for subsequent handlers using `c.WithGoContext()`.

## 2. Accessing the Go Context (`c.GoContext()`)

Within any Xylium handler or middleware, you can retrieve the current Go `context.Context` associated with the request using `c.GoContext()`.

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Assume dbQuery is a function that accepts a context.Context
func dbQuery(ctx context.Context, query string) (string, error) {
	// Simulate a database query that respects context cancellation
	select {
	case <-time.After(50 * time.Millisecond): // Simulate work
		return "Query result for: " + query, nil
	case <-ctx.Done(): // If context is cancelled (e.g., timeout)
		return "", ctx.Err() // Return context error (DeadlineExceeded or Canceled)
	}
}

func MyHandler(c *xylium.Context) error {
	// Get the Go context from Xylium's context
	goCtx := c.GoContext()

	// Use goCtx for operations that should be cancellable or respect deadlines
	result, err := dbQuery(goCtx, "SELECT * FROM users")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			c.Logger().Warn("Database query timed out.")
			return xylium.NewHTTPError(http.StatusGatewayTimeout, "Database operation timed out.")
		}
		c.Logger().Errorf("Database query failed: %v", err)
		return xylium.NewHTTPError(http.StatusInternalServerError, "Failed to query database.")
	}

	return c.String(http.StatusOK, "DB Result: "+result)
}

func main() {
	app := xylium.New()
	// Example: Add Timeout middleware which will set a deadline on c.GoContext()
	app.Use(xylium.Timeout(100 * time.Millisecond))
	app.GET("/data", MyHandler)
	app.Start(":8080")
}
```

## 3. Propagating Go Context to Downstream Services

It is crucial to pass the `c.GoContext()` to any functions or client libraries that perform I/O operations (database queries, HTTP calls to other services, etc.) and support `context.Context`. This ensures that if the request is cancelled or times out, these downstream operations can also be cancelled, preventing resource leaks and unnecessary work.

```go
import (
	"context"
	"net/http" // For http.NewRequestWithContext
	// ...
)

// externalServiceCall makes an HTTP request to another service
func externalServiceCall(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{} // Use a shared client in real apps
	return client.Do(req)
}

func CallExternalAPIHandler(c *xylium.Context) error {
	goCtx := c.GoContext() // Get the (potentially timed-out or traced) Go context

	resp, err := externalServiceCall(goCtx, "https://api.example.com/data")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			c.Logger().Warn("Call to external API timed out.")
			return xylium.NewHTTPError(http.StatusGatewayTimeout, "External API call timed out.")
		}
		// Handle other errors like network issues, etc.
		c.Logger().Errorf("External API call failed: %v", err)
		return xylium.NewHTTPError(http.StatusBadGateway, "Failed to communicate with external service.")
	}
	defer resp.Body.Close()

	// Process response body...
	// responseBody, _ := io.ReadAll(resp.Body)
	return c.String(resp.StatusCode, "External API response received.")
}
```
Most modern Go libraries for databases (`database/sql`), HTTP clients (`net/http`), gRPC, etc., provide `Context`-aware methods (e.g., `db.QueryRowContext`, `http.NewRequestWithContext`).

## 4. Using `context.WithTimeout` or `context.WithCancel` in Handlers

While Xylium's `Timeout` middleware can set an overall request timeout, you might need finer-grained control within a handler for specific operations or sections of code.

### 4.1. Timeout Example

If you want to apply a shorter timeout to a specific part of your handler logic:

```go
func ComplexOperationHandler(c *xylium.Context) error {
	parentGoCtx := c.GoContext()

	// Create a new context with a 50ms timeout for a specific sub-operation
	opCtx, opCancel := context.WithTimeout(parentGoCtx, 50*time.Millisecond)
	defer opCancel() // IMPORTANT: Always call cancel to release resources

	// Perform the sub-operation using opCtx
	// result, err := performCriticalSubOperation(opCtx, ...)
	// if err != nil {
	//     if errors.Is(err, context.DeadlineExceeded) {
	//         // Handle sub-operation timeout specifically
	//     }
	//     // Handle other sub-operation errors
	// }

	// Continue with other logic, possibly using parentGoCtx or another derived context
	return c.String(http.StatusOK, "Complex operation finished.")
}
```

### 4.2. Cancellation Example

You can create a cancellable context if you need to manually trigger cancellation for a group of goroutines spawned by your handler, for instance.

```go
func BackgroundTasksHandler(c *xylium.Context) error {
	parentGoCtx := c.GoContext()

	// Create a cancellable context for background tasks
	tasksCtx, tasksCancel := context.WithCancel(parentGoCtx)
	defer tasksCancel() // Ensure cancellation signal is sent if handler exits early

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int, ctx context.Context) {
			defer wg.Done()
			// taskThatRespectsContext(ctx, id)
			c.Logger().Debugf("Task %d started, listening to context.", id)
			select {
			case <-time.After(1 * time.Second): // Simulate work
				c.Logger().Infof("Task %d completed.", id)
			case <-ctx.Done():
				c.Logger().Warnf("Task %d cancelled: %v", id, ctx.Err())
			}
		}(i, tasksCtx) // Pass tasksCtx to each goroutine
	}

	// If some condition requires cancelling all tasks early:
	// if someErrorCondition {
	//     c.Logger().Warn("Error condition met, cancelling background tasks.")
	//     tasksCancel() // Signal all goroutines using tasksCtx to cancel
	// }

	wg.Wait() // Wait for all tasks to complete or be cancelled
	return c.String(http.StatusOK, "Background tasks processed.")
}
```

## 5. Xylium Middleware and Go Context

Several built-in Xylium middlewares leverage Go context:

### 5.1. Timeout Middleware

`xylium.Timeout(duration)` or `xylium.TimeoutWithConfig(...)` creates a new Go context derived from the incoming `c.GoContext()`, but with the specified timeout. This new timed context is then set as the `c.GoContext()` for all subsequent handlers in the chain. If the timeout is exceeded, `c.GoContext().Done()` will be closed.

### 5.2. OpenTelemetry Middleware

`xylium.Otel(...)` extracts trace context from incoming headers and starts a new OpenTelemetry span. The Go context associated with this new span is then propagated as `c.GoContext()` to subsequent handlers. This allows you to create child spans within your handlers using `otel.Tracer(...).Start(c.GoContext(), "child-span-name")`. See `OpenTelemetry.md` for details.

## 6. Replacing the Go Context in `xylium.Context` (`c.WithGoContext()`)

If a middleware or handler needs to provide a new Go `context.Context` (e.g., one with a new timeout, cancellation, or OTel span) to subsequent handlers, it should use `newXyliumCtx := c.WithGoContext(newGoCtx)`. Then, `next(newXyliumCtx)` should be called.

`c.WithGoContext()` creates a shallow copy of the `xylium.Context` but replaces its internal Go context. This ensures that `c.GoContext()` in downstream handlers returns the modified Go context.

```go
// Custom middleware that adds a specific deadline to the Go context
func MyDeadlineMiddleware(deadline time.Time) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			currentGoCtx := c.GoContext()
			ctxWithDeadline, cancel := context.WithDeadline(currentGoCtx, deadline)
			defer cancel()

			// Create a new Xylium context with the deadline-aware Go context
			xyliumCtxWithDeadline := c.WithGoContext(ctxWithDeadline)

			// Call next with the new Xylium context
			return next(xyliumCtxWithDeadline)
		}
	}
}
```

## 7. Passing Request-Scoped Values via Go Context (Advanced)

The Go `context.Context` API allows passing request-scoped values using `context.WithValue(parentCtx, key, value)`. While Xylium provides `c.Set()` and `c.Get()` for passing data within its own context store (which is generally preferred for Xylium-specific data), `context.WithValue` can be useful for:
*   Propagating values that are specifically expected by downstream libraries or functions that only accept a `context.Context`.
*   Situations where data needs to cross goroutine boundaries created from the Go context.

**Caution:**
*   Use unexported custom types for context keys to avoid collisions.
*   `context.WithValue` should be used sparingly, primarily for transporting request-scoped data that is critical for the execution flow across API boundaries, not as a general-purpose parameter passing mechanism.

```go
type myCustomKeyType string // Unexported type for context key

const MyCustomValueKey myCustomKeyType = "myCustomValueKey"

func ValueInjectingMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		currentGoCtx := c.GoContext()
		// Add a value to the Go context
		ctxWithValue := context.WithValue(currentGoCtx, MyCustomValueKey, "some_request_specific_data")

		xyliumCtxWithValue := c.WithGoContext(ctxWithValue)
		return next(xyliumCtxWithValue)
	}
}

func ValueConsumingHandler(c *xylium.Context) error {
	goCtx := c.GoContext()
	customValue, ok := goCtx.Value(MyCustomValueKey).(string)
	if !ok {
		c.Logger().Warn("Custom value not found in Go context or wrong type.")
		// Handle missing value
	} else {
		c.Logger().Infof("Custom value from Go context: %s", customValue)
	}
	return c.String(http.StatusOK, "Value processed.")
}
```

By properly utilizing Go's `context.Context` through Xylium's integration, you can build more resilient, manageable, and observable applications.
