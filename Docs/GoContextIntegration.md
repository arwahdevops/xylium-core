# Xylium Go Context Integration

Xylium is designed to work seamlessly with Go's standard `context.Context`. Each `xylium.Context` (the request context) embeds and manages a Go `context.Context`, enabling robust handling of deadlines, cancellation signals, and request-scoped values throughout your application, including interactions with downstream services.

## Table of Contents

*   [1. Overview of Go `context.Context` in Xylium](#1-overview-of-go-contextcontext-in-xylium)
*   [2. Accessing the Go Context (`c.GoContext()`)](#2-accessing-the-go-context-cgocontext)
*   [3. Propagating Go Context to Downstream Services](#3-propagating-go-context-to-downstream-services)
*   [4. Using `context.WithTimeout` or `context.WithCancel` in Handlers](#4-using-contextwithtimeout-or-contextwithcancel-in-handlers)
    *   [4.1. Timeout Example](#41-timeout-example)
    *   [4.2. Cancellation Example](#42-cancellation-example)
*   [5. Xylium Middleware and Go Context](#5-xylium-middleware-and-go-context)
    *   [5.1. Timeout Middleware](#51-timeout-middleware)
    *   [5.2. OpenTelemetry Middleware (via Connector)](#52-opentelemetry-middleware-via-connector)
*   [6. Replacing the Go Context in `xylium.Context` (`c.WithGoContext()`)](#6-replacing-the-go-context-in-xyliumcontext-cwithgocontext)
*   [7. Passing Request-Scoped Values via Go Context (Advanced)](#7-passing-request-scoped-values-via-go-context-advanced)

---

## 1. Overview of Go `context.Context` in Xylium

The standard Go `context.Context` is a powerful tool for managing request lifecycles, especially in concurrent and distributed systems. It provides:
*   **Cancellation Signals**: Allows different parts of an application (e.g., a handler and its downstream calls) to be notified if an operation should be aborted (e.g., client disconnects, timeout).
*   **Deadlines**: Sets a time by which an operation must complete.
*   **Request-Scoped Values**: A way to carry request-specific data across API boundaries and between goroutines, although this should be used sparingly for truly request-scoped data, not for passing optional parameters to functions.

Xylium integrates this by:
1.  Initializing each `xylium.Context` with a base Go `context.Context` (typically `context.Background()` or one derived by middleware or `fasthttp`'s `UserValue`).
2.  Providing `c.GoContext()` to access this Go context.
3.  Allowing middleware (like Timeout or OpenTelemetry via connectors) to derive new Go contexts (e.g., with timeouts or trace information) and associate them with the `xylium.Context` for subsequent handlers using `c.WithGoContext()`.

## 2. Accessing the Go Context (`c.GoContext()`)

Within any Xylium handler or middleware, you can retrieve the current Go `context.Context` associated with the request using `c.GoContext()`.

```go
package main

import (
	"context"
	"errors" // For errors.Is
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
			// Use Xylium's status constants
			return xylium.NewHTTPError(xylium.StatusGatewayTimeout, "Database operation timed out.")
		}
		c.Logger().Errorf("Database query failed: %v", err)
		return xylium.NewHTTPError(xylium.StatusInternalServerError, "Failed to query database.")
	}

	return c.String(xylium.StatusOK, "DB Result: "+result)
}

func main() {
	app := xylium.New()
	// Example: Add Timeout middleware which will set a deadline on c.GoContext()
	app.Use(xylium.Timeout(100 * time.Millisecond)) // Ensure Timeout middleware is correctly configured
	app.GET("/data", MyHandler)
	// app.Start(":8080") // Assuming this is defined for a runnable example
}
```

## 3. Propagating Go Context to Downstream Services

It is crucial to pass the `c.GoContext()` to any functions or client libraries that perform I/O operations (database queries, HTTP calls to other services, etc.) and support `context.Context`. This ensures that if the request is cancelled or times out, these downstream operations can also be cancelled, preventing resource leaks and unnecessary work.

```go
import (
	"context"
	"errors"   // For errors.Is
	"net/http" // For http.NewRequestWithContext and http.Client
	// "github.com/arwahdevops/xylium-core/src/xylium" // Assuming Xylium context is available
)

// externalServiceCall makes an HTTP request to another service
func externalServiceCall(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{} // Use a shared client (e.g., http.DefaultClient or a custom one) in real apps
	return client.Do(req)
}

func CallExternalAPIHandler(c *xylium.Context) error {
	goCtx := c.GoContext() // Get the (potentially timed-out or traced) Go context

	resp, err := externalServiceCall(goCtx, "https://api.example.com/data")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			c.Logger().Warn("Call to external API timed out.")
			return xylium.NewHTTPError(xylium.StatusGatewayTimeout, "External API call timed out.")
		}
		// Handle other errors like network issues, etc.
		c.Logger().Errorf("External API call failed: %v", err)
		return xylium.NewHTTPError(xylium.StatusBadGateway, "Failed to communicate with external service.")
	}
	defer resp.Body.Close() // Always close the response body

	// Process response body...
	// responseBody, _ := io.ReadAll(resp.Body)
	return c.String(xylium.StatusOK, "External API response received.") // Use resp.StatusCode if dynamic
}
```
Most modern Go libraries for databases (`database/sql`), HTTP clients (`net/http`), gRPC, etc., provide `Context`-aware methods (e.g., `db.QueryRowContext`, `http.NewRequestWithContext`).

## 4. Using `context.WithTimeout` or `context.WithCancel` in Handlers

While Xylium's `Timeout` middleware can set an overall request timeout, you might need finer-grained control within a handler for specific operations or sections of code.

### 4.1. Timeout Example

If you want to apply a shorter timeout to a specific part of your handler logic:

```go
// import (
// 	"context"
// 	"time"
// 	"errors"
// 	"github.com/arwahdevops/xylium-core/src/xylium"
// )

// func performCriticalSubOperation(ctx context.Context, data string) (string, error) {
// 	select {
// 	case <-time.After(30 * time.Millisecond): // Simulate work
// 		return "Sub-operation success for " + data, nil
// 	case <-ctx.Done():
// 		return "", ctx.Err()
// 	}
// }

func ComplexOperationHandler(c *xylium.Context) error {
	parentGoCtx := c.GoContext()

	// Create a new context with a 50ms timeout for a specific sub-operation
	opCtx, opCancel := context.WithTimeout(parentGoCtx, 50*time.Millisecond)
	defer opCancel() // IMPORTANT: Always call cancel to release resources associated with opCtx

	result, err := performCriticalSubOperation(opCtx, "some_data")
	if err != nil {
	    if errors.Is(err, context.DeadlineExceeded) {
	        c.Logger().Warn("Critical sub-operation timed out.")
	        // Handle sub-operation timeout specifically, e.g., return a specific error or default value
			return xylium.NewHTTPError(xylium.StatusRequestTimeout, "A part of the operation timed out.")
	    }
	    // Handle other sub-operation errors
		c.Logger().Errorf("Critical sub-operation failed: %v", err)
		return xylium.NewHTTPError(xylium.StatusInternalServerError, "Sub-operation failed.")
	}
	c.Logger().Infof("Sub-operation result: %s", result)

	// Continue with other logic, possibly using parentGoCtx or another derived context
	return c.String(xylium.StatusOK, "Complex operation finished.")
}
```

### 4.2. Cancellation Example

You can create a cancellable context if you need to manually trigger cancellation for a group of goroutines spawned by your handler, for instance.

```go
// import (
// 	"context"
// 	"sync"
// 	"time"
// 	"github.com/arwahdevops/xylium-core/src/xylium"
// )

// func taskThatRespectsContext(ctx context.Context, id int, logger xylium.Logger) {
// 	logger.Debugf("Task %d started, listening to context.", id)
// 	select {
// 	case <-time.After(1 * time.Second): // Simulate work
// 		logger.Infof("Task %d completed.", id)
// 	case <-ctx.Done(): // Context cancelled
// 		logger.Warnf("Task %d cancelled: %v", id, ctx.Err())
// 	}
// }

func BackgroundTasksHandler(c *xylium.Context) error {
	parentGoCtx := c.GoContext()

	// Create a cancellable context for background tasks
	tasksCtx, tasksCancel := context.WithCancel(parentGoCtx)
	defer tasksCancel() // Ensure cancellation signal is sent if handler exits early or for any other reason

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int, ctx context.Context, logger xylium.Logger) { // Pass logger for goroutine
			defer wg.Done()
			taskThatRespectsContext(ctx, id, logger)
		}(i, tasksCtx, c.Logger().WithFields(xylium.M{"task_goroutine_id": i})) // Pass tasksCtx and a derived logger
	}

	// Example: If some condition requires cancelling all tasks early:
	// if someErrorConditionMet {
	//     c.Logger().Warn("Error condition met, cancelling background tasks.")
	//     tasksCancel() // Signal all goroutines using tasksCtx to cancel
	// }

	wg.Wait() // Wait for all tasks to complete or be cancelled
	return c.String(xylium.StatusOK, "Background tasks processed.")
}
```

## 5. Xylium Middleware and Go Context

Several built-in Xylium middlewares leverage Go context:

### 5.1. Timeout Middleware

`xylium.Timeout(duration)` or `xylium.TimeoutWithConfig(...)` creates a new Go context derived from the incoming `c.GoContext()`, but with the specified timeout. This new timed context is then set as the `c.GoContext()` for all subsequent handlers in the chain using `c.WithGoContext()`. If the timeout is exceeded, `c.GoContext().Done()` will be closed for handlers using the timed context.

### 5.2. OpenTelemetry Middleware (via Connector)

Integration with OpenTelemetry for distributed tracing is typically handled by a dedicated connector like `xylium-otel`. This connector provides middleware that:
1.  Extracts trace context from incoming headers.
2.  Starts a new OpenTelemetry span.
3.  Creates a new Go `context.Context` associated with this span.
4.  Propagates this new Go context as `c.GoContext()` to subsequent handlers using `c.WithGoContext()`.
This allows you to create child spans within your handlers using `otel.Tracer(...).Start(c.GoContext(), "child-span-name")`. Refer to the documentation for the specific OpenTelemetry connector for details (e.g., `xylium-otel` README).

## 6. Replacing the Go Context in `xylium.Context` (`c.WithGoContext()`)

If a middleware or handler needs to provide a new Go `context.Context` (e.g., one with a new timeout, cancellation, or an OTel span) to subsequent handlers, it should:
1.  Create the new Go context (e.g., `ctxWithDeadline, cancel := context.WithDeadline(currentGoCtx, deadline)`).
2.  Create a new Xylium context using `newXyliumCtx := c.WithGoContext(newGoCtx)`.
3.  Call `next(newXyliumCtx)`.
4.  Remember to call the `cancel` function (from `context.WithDeadline`, `context.WithTimeout`, or `context.WithCancel`) using `defer` to release resources.

`c.WithGoContext()` creates a shallow copy of the `xylium.Context` but replaces its internal Go context. This ensures that `c.GoContext()` in downstream handlers returns the modified Go context, while sharing the underlying request store and fasthttp context.

```go
// Custom middleware that adds a specific deadline to the Go context
func MyDeadlineMiddleware(deadline time.Time) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error { // c is the original Xylium context
			currentGoCtx := c.GoContext()
			ctxWithDeadline, cancel := context.WithDeadline(currentGoCtx, deadline)
			defer cancel() // Ensure the cancel function for ctxWithDeadline is called

			// Create a new Xylium context that carries the deadline-aware Go context
			xyliumCtxWithDeadline := c.WithGoContext(ctxWithDeadline)

			// Call next handler in the chain with the new Xylium context
			return next(xyliumCtxWithDeadline)
		}
	}
}
```

## 7. Passing Request-Scoped Values via Go Context (Advanced)

The Go `context.Context` API allows passing request-scoped values using `context.WithValue(parentCtx, key, value)`. While Xylium provides `c.Set()` and `c.Get()` for passing data within its own context store (which is generally preferred for Xylium-specific data like user information after auth), `context.WithValue` can be useful for:
*   Propagating values that are specifically expected by downstream libraries or functions that only accept a `context.Context` (e.g., certain gRPC interceptors or database drivers).
*   Situations where data needs to cross goroutine boundaries created directly from the Go context.

**Caution:**
*   Use unexported custom types for context keys to avoid collisions (e.g., `type myCustomKey string`).
*   `context.WithValue` should be used sparingly, primarily for transporting request-scoped data that is critical for the execution flow across API boundaries and goroutines, not as a general-purpose parameter passing mechanism. For data shared between Xylium middleware and handlers, `c.Set()`/`c.Get()` is often more direct.

```go
// Define an unexported type for the context key to avoid collisions
type myCustomKeyType string

// Define the actual key
const MyCustomValueKey myCustomKeyType = "myCustomValueKeyFromGoContext"

func ValueInjectingMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		currentGoCtx := c.GoContext()
		// Add a value to the Go context
		ctxWithValue := context.WithValue(currentGoCtx, MyCustomValueKey, "some_request_specific_data_via_go_context")

		// Create a new Xylium context with the Go context that carries the value
		xyliumCtxWithValue := c.WithGoContext(ctxWithValue)
		return next(xyliumCtxWithValue)
	}
}

func ValueConsumingHandler(c *xylium.Context) error {
	goCtx := c.GoContext() // This will be ctxWithValue from the middleware
	
	customValue, ok := goCtx.Value(MyCustomValueKey).(string)
	if !ok {
		c.Logger().Warn("Custom value not found in Go context or wrong type.")
		// Handle missing value, perhaps return an error or default
		return c.String(xylium.StatusOK, "Custom value not found in Go context.")
	} else {
		c.Logger().Infof("Custom value from Go context: %s", customValue)
		return c.String(xylium.StatusOK, "Value from Go context: "+customValue)
	}
}
```

By properly utilizing Go's `context.Context` through Xylium's integration, you can build more resilient, manageable, and observable applications. Remember that `c.GoContext()` provides the Go context, while `c.WithGoContext()` is used by middleware to propagate modified Go contexts.
