# Xylium Middleware

Middleware in Xylium are functions that can process an HTTP request before it reaches the main handler or after the main handler has executed. They are a powerful mechanism for implementing cross-cutting concerns like logging, authentication, authorization, request/response manipulation, error handling, and more.

## Table of Contents

*   [1. What is Middleware?](#1-what-is-middleware)
*   [2. Creating Custom Middleware](#2-creating-custom-middleware)
*   [3. Using Middleware](#3-using-middleware)
    *   [3.1. Global Middleware](#31-global-middleware)
    *   [3.2. Route-Specific Middleware](#32-route-specific-middleware)
    *   [3.3. Group-Specific Middleware](#33-group-specific-middleware)
*   [4. Middleware Execution Order](#4-middleware-execution-order)
*   [5. Passing Data Between Middleware and Handlers](#5-passing-data-between-middleware-and-handlers)
*   [6. Built-in Middleware](#6-built-in-middleware)
    *   [6.1. RequestID (`xylium.RequestID()`)](#61-requestid-xyliumrequestid)
    *   [6.2. Logger (Automatic)](#62-logger-automatic)
    *   [6.3. Gzip Compression (`xylium.Gzip()`)](#63-gzip-compression-xyliumgzip)
    *   [6.4. CORS (`xylium.CORS()`)](#64-cors-xyliumcors)
    *   [6.5. CSRF Protection (`xylium.CSRF()`)](#65-csrf-protection-xyliumcsrf)
    *   [6.6. BasicAuth (`xylium.BasicAuth()`)](#66-basicauth-xyliumbasicauth)
    *   [6.7. Rate Limiter (`xylium.RateLimiter()`)](#67-rate-limiter-xyliumratelimiter)
    *   [6.8. Timeout (`xylium.Timeout()`)](#68-timeout-xyliumtimeout)
    *   [6.9. OpenTelemetry (`xylium.Otel()`)](#69-opentelemetry-xyliumotel)

---

## 1. What is Middleware?

In Xylium, middleware is a function that takes a `xylium.HandlerFunc` (the next handler in the chain) and returns a new `xylium.HandlerFunc`. The signature is:

```go
type Middleware func(next HandlerFunc) HandlerFunc
```

The returned `HandlerFunc` typically does some work, then calls `next(c)` to pass control to the next middleware or the final route handler. It can also perform actions after `next(c)` returns.

**Key Capabilities of Middleware:**
*   Execute code before and/or after the main handler.
*   Modify the request (`xylium.Context`).
*   Modify the response (`xylium.Context`).
*   Short-circuit the request (e.g., return an error or response early without calling `next`).
*   Pass data to subsequent handlers using `c.Set()`.

## 2. Creating Custom Middleware

Here's an example of a simple custom middleware that logs the request method and path:

```go
package main

import (
	"time"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

// SimpleRequestLogger logs the request method, path, and processing time.
func SimpleRequestLogger() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			start := time.Now()
			logger := c.Logger().WithFields(xylium.M{"middleware": "SimpleRequestLogger"}) // Contextual logger

			logger.Debugf("Request received: %s %s", c.Method(), c.Path())

			// Call the next handler in the chain
			err := next(c)

			latency := time.Since(start)
			statusCode := c.Ctx.Response.StatusCode() // Get status code after handler execution

			logFields := xylium.M{
				"status_code": statusCode,
				"latency_ms":  latency.Milliseconds(),
			}

			if err != nil {
				// If an error was returned by 'next' (or a handler further down)
				logger.WithFields(logFields).Errorf("Request to %s %s failed after %s. Error: %v",
					c.Method(), c.Path(), latency, err)
			} else {
				logger.WithFields(logFields).Infof("Request to %s %s completed in %s.",
					c.Method(), c.Path(), latency)
			}
			return err // Propagate the error (or nil)
		}
	}
}

func main() {
	app := xylium.New()

	// Apply custom middleware globally
	app.Use(xylium.RequestID()) // Good to have RequestID before loggers
	app.Use(SimpleRequestLogger())

	app.GET("/hello", func(c *xylium.Context) error {
		return c.String(http.StatusOK, "Hello from Xylium with custom logger!")
	})

	app.Start(":8080")
}
```

## 3. Using Middleware

Middleware can be applied at different levels:

### 3.1. Global Middleware

Applied to all routes using `app.Use(middleware ...xylium.Middleware)`.

```go
app := xylium.New()
app.Use(xylium.RequestID())   // Applied first to all requests
app.Use(MyCustomAuthMiddleware()) // Applied second to all requests
// ... other global middleware ...
```

### 3.2. Route-Specific Middleware

Applied to individual routes as variadic arguments after the handler function.

```go
func SpecificAuthCheck(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error { /* ... */ return next(c) }
}
func AdminOnlyCheck(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error { /* ... */ return next(c) }
}

app.GET("/admin/dashboard", AdminDashboardHandler, SpecificAuthCheck, AdminOnlyCheck)
```

### 3.3. Group-Specific Middleware

Applied to a group of routes using `group.Use(...)` or when creating the group.

```go
apiGroup := app.Group("/api", APILoggingMiddleware)
{
	apiGroup.Use(APIVersionCheckMiddleware) // Applied to all routes in /api/* after APILoggingMiddleware

	apiGroup.GET("/users", ListUsersHandler) // Both middlewares run
	apiGroup.POST("/products", CreateProductHandler, ProductValidationMiddleware) // All three run for this route
}
```

## 4. Middleware Execution Order

Middleware execution follows a "onion" or "Russian doll" model:
1.  **Global middleware** are applied first, in the order they are registered with `app.Use()`.
2.  **Group middleware** are applied next, in the order they are registered with `group.Use()` or at group creation. If groups are nested, parent group middleware runs before child group middleware.
3.  **Route-specific middleware** are applied last, in the order they are provided in the route definition.

Within each level, the request flows "in" through each middleware until it reaches the `next(c)` call, then flows "out" as `next(c)` returns.

**Example:**
`app.Use(M1)`
`group := app.Group("/path", M2)`
`group.GET("/sub", Handler, M3)`

Execution order for `GET /path/sub`:
1.  M1 (before `next`)
2.  M2 (before `next`)
3.  M3 (before `next`)
4.  `Handler` executes
5.  M3 (after `next` returns)
6.  M2 (after `next` returns)
7.  M1 (after `next` returns)

## 5. Passing Data Between Middleware and Handlers

Middleware can pass data to subsequent middleware or to the final route handler using the `xylium.Context` store methods:
*   `c.Set(key string, value interface{})`
*   `c.Get(key string) (value interface{}, exists bool)`
*   `c.MustGet(key string) interface{}` (panics if key not found)
*   Typed getters like `c.GetString(key string)`, `c.GetInt(key string)`, etc.

```go
const UserContextKey = "authenticated_user"

func AuthMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		// ... authentication logic ...
		// Assume user is authenticated and user info is in `userInfo`
		// userInfo := map[string]string{"id": "123", "role": "admin"}
		// c.Set(UserContextKey, userInfo)
		return next(c)
	}
}

func UserProfileHandler(c *xylium.Context) error {
	// userInfo, exists := c.Get(UserContextKey)
	// if !exists {
	//     return c.Status(http.StatusForbidden).String("Access denied.")
	// }
	// typedUserInfo := userInfo.(map[string]string) // Type assertion
	// return c.JSON(http.StatusOK, xylium.M{"profile": typedUserInfo})

    // Or using MustGet if presence is guaranteed by middleware logic
    userInfo := c.MustGet(UserContextKey).(map[string]string)
    return c.JSON(http.StatusOK, xylium.M{"profile": userInfo})
}
```

## 6. Built-in Middleware

Xylium provides a suite of commonly used middleware.

### 6.1. RequestID (`xylium.RequestID()`)

*   **Purpose**: Injects a unique ID into each request for tracing and logging.
*   **Behavior**:
    *   Checks for an incoming request ID in the `X-Request-ID` header (configurable).
    *   If not present, generates a new UUID v4.
    *   Sets the ID in `c.store` with key `xylium.ContextKeyRequestID` (`"xylium_request_id"`).
    *   Sets the ID in the `X-Request-ID` response header.
*   **Usage**:
    ```go
    app.Use(xylium.RequestID())
    // Or with custom config:
    // app.Use(xylium.RequestIDWithConfig(xylium.RequestIDConfig{
    //  HeaderName: "X-Correlation-ID",
    //  Generator: func() string { return "my-custom-id-" + time.Now().String() },
    // }))
    ```
*   **Integration**: `c.Logger()` automatically includes `xylium_request_id` in log fields if this middleware is used.

### 6.2. Logger (Automatic)

Xylium's core request handling (`Router.Handler`) automatically provides request-scoped logging via `c.Logger()`. This logger inherits from `app.Logger()` and can be further enhanced by middleware like `RequestID` or `OpenTelemetry` which add contextual fields (`xylium_request_id`, `trace_id`, `span_id`) to `c.store`.

While there isn't a distinct `xylium.LoggerMiddleware()`, the logging behavior is deeply integrated. You can create custom logging middleware (like `SimpleRequestLogger` above) to control log message content and timing.

### 6.3. Gzip Compression (`xylium.Gzip()`)

*   **Purpose**: Compresses HTTP response bodies using Gzip to reduce transfer size.
*   **Behavior**:
    *   Checks `Accept-Encoding` client header.
    *   Compresses responses based on `Content-Type`, min length, and compression level.
    *   Sets `Content-Encoding: gzip` and `Vary: Accept-Encoding` headers.
*   **Usage**:
    ```go
    app.Use(xylium.Gzip()) // Uses default settings

    // With custom configuration:
    // import "github.com/valyala/fasthttp"
    // app.Use(xylium.GzipWithConfig(xylium.GzipConfig{
    //  Level:     fasthttp.CompressBestSpeed,
    //  MinLength: 1024, // Only compress if body is > 1KB
    //  ContentTypes: []string{"application/json", "text/html"},
    // }))
    ```
*   See `middleware_compress.go` for `GzipConfig` details and default content types.

### 6.4. CORS (`xylium.CORS()`)

*   **Purpose**: Handles Cross-Origin Resource Sharing (CORS) headers.
*   **Behavior**:
    *   Responds to preflight `OPTIONS` requests.
    *   Sets `Access-Control-Allow-Origin`, `Access-Control-Allow-Methods`, etc., based on configuration.
*   **Usage**:
    ```go
    app.Use(xylium.CORS()) // Uses restrictive defaults (AllowOrigins is empty by default)

    // Recommended: Configure explicitly
    // app.Use(xylium.CORSWithConfig(xylium.CORSConfig{
    //  AllowOrigins:     []string{"https://example.com", "http://localhost:3000"},
    //  AllowMethods:     []string{"GET", "POST", "PUT"},
    //  AllowCredentials: true, // If cookies/auth headers are needed
    //  MaxAge:           3600, // Cache preflight for 1 hour
    // }))
    ```
*   **Important**: `DefaultCORSConfig.AllowOrigins` is an empty slice `[]string{}`. You **must** configure `AllowOrigins` for cross-origin requests to be permitted. Setting `AllowOrigins: []string{"*"}` allows all origins (use with caution, especially if `AllowCredentials: true`).
*   See `middleware_cors.go` for `CORSConfig` details.

### 6.5. CSRF Protection (`xylium.CSRF()`)

*   **Purpose**: Protects against Cross-Site Request Forgery attacks using the Double Submit Cookie pattern (or variants based on token lookup).
*   **Behavior**:
    *   Generates a CSRF token and stores it in a cookie.
    *   Validates this token against a token submitted in a request header, form field, or query parameter for unsafe HTTP methods (POST, PUT, DELETE, PATCH).
    *   The token from the cookie is also available in `c.store` via `config.ContextTokenKey` (default "csrf_token") for easy retrieval by handlers (e.g., to embed in HTML forms).
*   **Usage**:
    ```go
    app.Use(xylium.CSRF()) // Uses defaults (e.g., cookie "_csrf_token", header "X-CSRF-Token")

    // Example with custom config for SPA that reads token from non-HTTPOnly cookie
    // app.Use(xylium.CSRFWithConfig(xylium.CSRFConfig{
    //  CookieName:     "_my_csrf_cookie",
    //  CookieHTTPOnly: false, // If JS needs to read the cookie
    //  HeaderName:     "X-XSRF-TOKEN",
    // }))
    ```
*   **Default `CookieHTTPOnly` is `true`**. If your frontend (e.g., SPA) needs to read the CSRF token from the cookie via JavaScript, you must set `config.CookieHTTPOnly = false`.
*   See `middleware_csrf.go` for `CSRFConfig` and `DefaultCSRFConfig` details.

### 6.6. BasicAuth (`xylium.BasicAuth()`)

*   **Purpose**: Implements HTTP Basic Authentication.
*   **Behavior**:
    *   Parses `Authorization: Basic <credentials>` header.
    *   Calls a user-provided `Validator` function to check username/password.
    *   If valid, stores authenticated user info (from validator) in `c.store` (default key "user").
    *   If invalid or header missing, sends 401 Unauthorized with `WWW-Authenticate` header.
*   **Usage**:
    ```go
    validatorFunc := func(username, password string, c *xylium.Context) (interface{}, bool, error) {
        if username == "admin" && password == "secret" {
            return map[string]string{"username": username, "role": "admin"}, true, nil
        }
        return nil, false, nil
    }
    // app.Use(xylium.BasicAuth(validatorFunc)) // Deprecated, prefer WithConfig

    app.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{
        Validator: validatorFunc,
        Realm:     "My Protected Area",
    }))
    ```
*   See `middleware_basicauth.go` for `BasicAuthConfig` details.

### 6.7. Rate Limiter (`xylium.RateLimiter()`)

*   **Purpose**: Limits the number of requests a client can make within a time window.
*   **Behavior**:
    *   Uses a `LimiterStore` (default `InMemoryStore`) to track request counts per key.
    *   Key defaults to client IP (`c.RealIP()`).
    *   If limit exceeded, returns 429 Too Many Requests with `Retry-After` and `X-RateLimit-*` headers.
*   **Usage**:
    ```go
    // Global rate limiter: 100 requests per minute per IP
    app.Use(xylium.RateLimiter(xylium.RateLimiterConfig{
        MaxRequests:    100,
        WindowDuration: 1 * time.Minute,
    }))

    // Route-specific rate limiter
    app.POST("/sensitive-action", SensitiveActionHandler, xylium.RateLimiter(xylium.RateLimiterConfig{
        MaxRequests:    5,
        WindowDuration: 10 * time.Minute,
        Message:        "Too many attempts on sensitive action. Please wait.",
    }))
    ```
*   Xylium automatically manages the `Close()` method of internally created `InMemoryStore` instances during graceful shutdown.
*   See `middleware_ratelimiter.go` for `RateLimiterConfig`, `LimiterStore`, and header options.

### 6.8. Timeout (`xylium.Timeout()`)

*   **Purpose**: Sets a maximum processing time for requests.
*   **Behavior**:
    *   Uses `context.WithTimeout` to create a new Go context for the request.
    *   If processing by subsequent handlers exceeds the timeout, the context is canceled.
    *   Sends 503 Service Unavailable by default.
*   **Usage**:
    ```go
    app.Use(xylium.Timeout(10 * time.Second)) // Global 10-second timeout

    // With custom configuration
    // app.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
    //  Timeout: 5 * time.Second,
    //  Message: "Sorry, your request took too long to process.",
    //  ErrorHandler: func(c *xylium.Context, err error) error {
    //      c.Logger().Errorf("Timeout occurred: %v for path %s", err, c.Path())
    //      return c.Status(http.StatusGatewayTimeout).String("Request timed out (custom handler).")
    //  },
    // }))
    ```
*   Handlers should respect `c.GoContext().Done()` if performing long operations.
*   See `middleware_timeout.go` for `TimeoutConfig` details.

### 6.9. OpenTelemetry (`xylium.Otel()`)

*   **Purpose**: Integrates with OpenTelemetry for distributed tracing.
*   **Behavior**:
    *   Extracts trace context from incoming headers.
    *   Starts a new server span for each request.
    *   Records standard HTTP semantic attributes.
    *   Injects `trace_id` and `span_id` into `xylium.Context` (keys `ContextKeyOtelTraceID`, `ContextKeyOtelSpanID`) for `c.Logger()`.
    *   Propagates the traced Go `context.Context` via `c.WithGoContext()`.
*   **Prerequisites**: Your application must initialize the OpenTelemetry SDK (TracerProvider, Exporter, global Propagator).
*   **Usage**:
    ```go
    // After OTel SDK initialization in main()
    app.Use(xylium.Otel()) // Uses global OTel provider/propagator

    // With custom configuration
    // app.Use(xylium.Otel(xylium.OpenTelemetryConfig{
    //  TracerName: "my-service-tracer",
    //  SpanNameFormatter: func(c *xylium.Context) string {
    //      return c.Method() + " " + c.MatchedRoutePattern() // Ideal if MatchedRoutePattern is available
    //  },
    //  Filter: func(c *xylium.Context) bool { return c.Path() == "/health" }, // Skip tracing health checks
    // }))
    ```
*   Refer to **`OpenTelemetry.md`** for detailed setup and usage.

By leveraging Xylium's middleware system and its built-in components, you can build robust, secure, and observable web applications efficiently.
