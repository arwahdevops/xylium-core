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
    *   [6.2. Logger (Automatic Integration)](#62-logger-automatic-integration)
    *   [6.3. Gzip Compression (`xylium.Gzip()`)](#63-gzip-compression-xyliumgzip)
    *   [6.4. CORS (`xylium.CORS()`)](#64-cors-xyliumcors)
    *   [6.5. CSRF Protection (`xylium.CSRF()`)](#65-csrf-protection-xyliumcsrf)
    *   [6.6. BasicAuth (`xylium.BasicAuth()`)](#66-basicauth-xyliumbasicauth)
    *   [6.7. Rate Limiter (`xylium.RateLimiter()`)](#67-rate-limiter-xyliumratelimiter)
    *   [6.8. Timeout (`xylium.Timeout()`)](#68-timeout-xyliumtimeout)
    *   [6.9. OpenTelemetry (via `xylium-otel` Connector)](#69-opentelemetry-via-xylium-otel-connector)

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
	"net/http" // For http.StatusOK
	"time"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

// SimpleRequestLogger logs the request method, path, and processing time.
func SimpleRequestLogger() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			start := time.Now()
			// c.Logger() automatically includes fields like 'xylium_request_id' if RequestID middleware is used before this.
			logger := c.Logger().WithFields(xylium.M{"middleware": "SimpleRequestLogger"})

			logger.Debugf("Request received: %s %s", c.Method(), c.Path())

			// Call the next handler in the chain
			err := next(c)

			latency := time.Since(start)
			// Get status code after handler execution.
			// Ensure c.Ctx is not nil if handler might not proceed (e.g. panic)
			statusCode := 0
			if c.Ctx != nil { // Guard against nil context if an early panic or issue occurs
			    statusCode = c.Ctx.Response.StatusCode()
			}


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

	// Start server (error handling omitted for brevity in this example part)
	// if err := app.Start(":8080"); err != nil { app.Logger().Fatal(err) }
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
// app.Use(MyCustomAuthMiddleware()) // Applied second to all requests
// ... other global middleware ...
```

### 3.2. Route-Specific Middleware

Applied to individual routes as variadic arguments after the handler function.

```go
// func SpecificAuthCheck(next xylium.HandlerFunc) xylium.HandlerFunc {
// 	return func(c *xylium.Context) error { /* ... */ return next(c) }
// }
// func AdminOnlyCheck(next xylium.HandlerFunc) xylium.HandlerFunc {
// 	return func(c *xylium.Context) error { /* ... */ return next(c) }
// }
// func AdminDashboardHandler(c *xylium.Context) error { /* ... */ return nil }

// app.GET("/admin/dashboard", AdminDashboardHandler, SpecificAuthCheck, AdminOnlyCheck)
```

### 3.3. Group-Specific Middleware

Applied to a group of routes using `group.Use(...)` or when creating the group.

```go
// func APILoggingMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc { /* ... */ return next }
// func APIVersionCheckMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc { /* ... */ return next }
// func ListUsersHandler(c *xylium.Context) error { /* ... */ return nil }
// func CreateProductHandler(c *xylium.Context) error { /* ... */ return nil }
// func ProductValidationMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc { /* ... */ return next }


// apiGroup := app.Group("/api", APILoggingMiddleware)
// {
// 	apiGroup.Use(APIVersionCheckMiddleware) // Applied to all routes in /api/* after APILoggingMiddleware

// 	apiGroup.GET("/users", ListUsersHandler) // Both middlewares run
// 	apiGroup.POST("/products", CreateProductHandler, ProductValidationMiddleware) // All three run for this route
// }
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
// Define a context key (best practice to use unexported type or well-known constants)
// const UserContextKey = "authenticated_user" // Assume this is defined elsewhere (e.g., types.go or a consts package)

// func AuthMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
// 	return func(c *xylium.Context) error {
// 		// ... authentication logic ...
// 		// Assume user is authenticated and user info is in `userInfo`
// 		// userInfo := map[string]string{"id": "123", "role": "admin"}
// 		// c.Set(UserContextKey, userInfo) // Use your defined key
// 		return next(c)
// 	}
// }

// func UserProfileHandler(c *xylium.Context) error {
// 	// userInfo, exists := c.Get(UserContextKey)
// 	// if !exists {
// 	//     return c.Status(http.StatusForbidden).String("Access denied.")
// 	// }
// 	// typedUserInfo := userInfo.(map[string]string) // Type assertion
// 	// return c.JSON(http.StatusOK, xylium.M{"profile": typedUserInfo})

//     // Or using MustGet if presence is guaranteed by middleware logic
//     // userInfo := c.MustGet(UserContextKey).(map[string]string)
//     // return c.JSON(http.StatusOK, xylium.M{"profile": userInfo})
//     return nil // Placeholder
// }
```
*(Refer to `types.go` for standard context keys like `xylium.ContextKeyRequestID`.)*

## 6. Built-in Middleware

Xylium provides a suite of commonly used middleware.

### 6.1. RequestID (`xylium.RequestID()`)

*   **Purpose**: Injects a unique ID into each request for tracing and logging.
*   **Behavior**:
    *   Checks for an incoming request ID in the `X-Request-ID` header (configurable via `RequestIDConfig.HeaderName`).
    *   If not present, generates a new UUID v4 (configurable via `RequestIDConfig.Generator`).
    *   Sets the ID in `c.store` with key `xylium.ContextKeyRequestID`.
    *   Sets the ID in the response header (using the configured `HeaderName`).
*   **Usage**:
    ```go
    app.Use(xylium.RequestID())
    // Or with custom config:
    // app.Use(xylium.RequestIDWithConfig(xylium.RequestIDConfig{
    //  HeaderName: "X-Correlation-ID",
    //  Generator: func() string { return "my-custom-id-" + time.Now().String() },
    // }))
    ```
*   **Integration**: `c.Logger()` automatically includes `xylium_request_id` in log fields if this middleware is used and the key matches `xylium.ContextKeyRequestID`.

### 6.2. Logger (Automatic Integration)

Xylium's core request handling (`Router.Handler`) automatically provides request-scoped logging via `c.Logger()`. This logger inherits from `app.Logger()` and can be further enhanced by middleware like `RequestID` or an OTel connector which add contextual fields (e.g., `xylium_request_id`, `otel_trace_id`, `otel_span_id`) to `c.store`.

While there isn't a distinct `xylium.LoggerMiddleware()` to *enable* basic logging (it's integrated), you can create custom logging middleware (like `SimpleRequestLogger` in [Section 2](#2-creating-custom-middleware)) to control log message content, format, and timing more specifically around request lifecycles.

### 6.3. Gzip Compression (`xylium.Gzip()`)

*   **Purpose**: Compresses HTTP response bodies using Gzip to reduce transfer size.
*   **Behavior**:
    *   Checks the `Accept-Encoding` client header for "gzip" support.
    *   Compresses responses if the `Content-Type` is eligible (see defaults or configure with `GzipConfig.ContentTypes`) and the response body length meets `GzipConfig.MinLength` (default 0, meaning all sizes are eligible if type matches).
    *   Sets `Content-Encoding: gzip` and `Vary: Accept-Encoding` response headers.
*   **Usage**:
    ```go
    app.Use(xylium.Gzip()) // Uses default settings (default compression level, default content types)

    // With custom configuration:
    // import "github.com/valyala/fasthttp" // For fasthttp.CompressBestSpeed etc.
    // app.Use(xylium.GzipWithConfig(xylium.GzipConfig{
    //  Level:     fasthttp.CompressBestSpeed,
    //  MinLength: 1024, // Only compress if body is > 1KB
    //  ContentTypes: []string{"application/json", "text/html", "application/vnd.api+json"},
    // }))
    ```
*   Refer to `middleware_compress.go` for `GzipConfig` details and default compressible content types.

### 6.4. CORS (`xylium.CORS()`)

*   **Purpose**: Handles Cross-Origin Resource Sharing (CORS) headers, enabling or restricting cross-origin requests.
*   **Behavior**:
    *   Responds to preflight `OPTIONS` requests.
    *   Sets `Access-Control-Allow-Origin` (ACAO), `Access-Control-Allow-Methods`, `Access-Control-Allow-Headers`, etc., based on the configuration.
*   **Usage**:
    ```go
    // Using default config - IMPORTANT: Default AllowOrigins is EMPTY, so no cross-origin requests are allowed.
    // app.Use(xylium.CORS())

    // Recommended: Configure explicitly for your needs.
    app.Use(xylium.CORSWithConfig(xylium.CORSConfig{
     AllowOrigins:     []string{"https://example.com", "http://localhost:3000"},
     AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
     AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token"},
     AllowCredentials: true, // If cookies/auth headers from different origins are needed.
     MaxAge:           3600, // Cache preflight response for 1 hour (in seconds).
    }))
    ```
*   **Security Note**: `DefaultCORSConfig.AllowOrigins` is an empty slice `[]string{}`. You **must** configure `AllowOrigins` for any cross-origin requests to be permitted. Setting `AllowOrigins: []string{"*"}` allows all origins, which should be used with extreme caution, especially if `AllowCredentials: true` (as browsers will block `ACAO: *` with credentials).
*   Refer to `middleware_cors.go` for all `CORSConfig` options.

### 6.5. CSRF Protection (`xylium.CSRF()`)

*   **Purpose**: Protects against Cross-Site Request Forgery attacks, typically using the Double Submit Cookie pattern.
*   **Behavior**:
    *   On GET/HEAD/OPTIONS/TRACE requests (or other configured safe methods), it generates a CSRF token, sets it in a cookie (e.g., `_csrf_token`), and stores it in `c.store` (key `xylium.ContextKeyCSRFToken` by default, or `CSRFConfig.ContextTokenKey`). This token can be embedded in HTML forms by the handler.
    *   On unsafe HTTP methods (e.g., POST, PUT, DELETE, PATCH), it validates the token from the cookie against a token submitted by the client (e.g., in a request header like `X-CSRF-Token` or a form field like `_csrf`).
    *   If validation fails, it calls an error handler (defaulting to HTTP 403 Forbidden).
*   **Usage**:
    ```go
    // Using default config (cookie "_csrf_token", header "X-CSRF-Token", form "_csrf")
    // app.Use(xylium.CSRF())

    // Example with custom config (e.g., for SPA that reads token from a non-HTTPOnly cookie)
    secureCookie := true // Should be true in production (HTTPS)
    httpOnlyCookie := false // Set to false if JavaScript needs to read the cookie
    if xylium.Mode() != xylium.ReleaseMode {
        // For local HTTP development, Secure flag might need to be false
        // secureCookie = false
    }
    app.Use(xylium.CSRFWithConfig(xylium.CSRFConfig{
     CookieName:     "_my_app_csrf_token",
     CookieHTTPOnly: &httpOnlyCookie, // JavaScript can read this cookie
     CookieSecure:   &secureCookie,   // Send only over HTTPS
     HeaderName:     "X-XSRF-TOKEN",  // Common header for SPAs (e.g., Angular, Axios)
     // TokenLookup: "header:X-XSRF-TOKEN,form:csrf_token_field", // Explicit lookup order
    }))
    ```
*   **Important `CookieHTTPOnly`**: The default for `CSRFConfig.CookieHTTPOnly` (via `DefaultCSRFConfig`) is `true`. If your frontend JavaScript needs to read the CSRF token from the cookie (common in SPAs to send it back in a header), you **must** configure `CookieHTTPOnly` to `false` (e.g., `myHttpOnly := false; cfg.CookieHTTPOnly = &myHttpOnly`).
*   **Token Availability**: The CSRF token for the *next* request is available in the *current* request's context via `c.Get(config.ContextTokenKey)`. Handlers can use this to embed the token in HTML forms.
*   Refer to `middleware_csrf.go` for all `CSRFConfig` options and details on `DefaultCSRFConfig`.

### 6.6. BasicAuth (`xylium.BasicAuth()`)

*   **Purpose**: Implements HTTP Basic Authentication (RFC 7617).
*   **Behavior**:
    *   Parses the `Authorization: Basic <credentials>` header.
    *   Calls a user-provided `Validator` function to check the username and password.
    *   If valid, optionally stores authenticated user information (returned by the validator) in `c.store` (default key `"user"`, configurable via `BasicAuthConfig.ContextUserKey`).
    *   If invalid, header missing, or malformed, it calls an error handler. The default error handler sends an HTTP 401 Unauthorized response with a `WWW-Authenticate: Basic realm="..."` header.
*   **Usage**:
    ```go
    // Validator function: (username, password, context) -> (userInfo, isValid, error)
    myAuthValidator := func(username, password string, c *xylium.Context) (interface{}, bool, error) {
        if username == "admin" && password == "secretP@ssw0rd" {
            // Store user details in context if needed by handlers
            return map[string]string{"username": username, "role": "administrator"}, true, nil
        }
        return nil, false, nil // Invalid credentials
        // return nil, false, errors.New("database error") // If validator itself failed
    }

    app.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{
        Validator: myAuthValidator,
        Realm:     "My Protected Application Area",
        // ContextUserKey: "authedUser", // Optional: customize context key
        // ErrorHandler: func(c *xylium.Context) error { // Optional: custom error response
        //     return c.Status(http.StatusForbidden).String("Custom auth failure message.")
        // },
    }))
    ```
*   The `xylium.BasicAuth(validatorFunc)` shorthand is deprecated; prefer `BasicAuthWithConfig`.
*   Refer to `middleware_basicauth.go` for `BasicAuthConfig` details.

### 6.7. Rate Limiter (`xylium.RateLimiter()`)

*   **Purpose**: Limits the number of requests a client can make within a specific time window.
*   **Behavior**:
    *   Uses a `LimiterStore` (default is an `InMemoryStore` managed by Xylium) to track request counts per key.
    *   The key defaults to the client's IP address (`c.RealIP()`), configurable via `RateLimiterConfig.KeyGenerator`.
    *   If the limit is exceeded, it returns an HTTP 429 Too Many Requests response with `Retry-After` and `X-RateLimit-*` headers (configurable).
*   **Usage**:
    ```go
    import "time"

    // Global rate limiter: 100 requests per minute per IP.
    // The InMemoryStore created here will be managed by Xylium's graceful shutdown.
    app.Use(xylium.RateLimiter(xylium.RateLimiterConfig{
        MaxRequests:    100,
        WindowDuration: 1 * time.Minute,
        Message:        "Global rate limit exceeded. Please try again later.",
        // LoggerForStore: app.Logger(), // Provide app logger to internal store
    }))

    // Route-specific rate limiter for a sensitive action
    // func SensitiveActionHandler(c *xylium.Context) error { /* ... */ return nil }
    // app.POST("/sensitive-action", SensitiveActionHandler, xylium.RateLimiter(xylium.RateLimiterConfig{
    //     MaxRequests:    5,
    //     WindowDuration: 10 * time.Minute,
    //     Message:        "Too many attempts on this sensitive action. Please wait a few minutes.",
    // }))
    ```
*   If you use multiple `RateLimiter` middlewares with the default `InMemoryStore` (i.e., `config.Store` is `nil`), each will create its own store instance. Xylium's graceful shutdown will attempt to close all such internally created stores. For shared rate limit state across different limiters, create a single `LimiterStore` instance and pass it to each `RateLimiterConfig`.
*   Refer to `middleware_ratelimiter.go` for `RateLimiterConfig`, `LimiterStore` interface, `InMemoryStore` details, and header customization options.

### 6.8. Timeout (`xylium.Timeout()`)

*   **Purpose**: Sets a maximum processing time for requests handled by subsequent handlers in the chain.
*   **Behavior**:
    *   Uses `context.WithTimeout` to create a new Go `context.Context` for the request, which is then propagated via `c.WithGoContext()`.
    *   If processing by `next(c)` exceeds the timeout, `c.GoContext().Done()` is closed.
    *   An error handler is invoked (default sends HTTP 503 Service Unavailable).
*   **Usage**:
    ```go
    import "time"

    // Global 10-second timeout for all requests.
    app.Use(xylium.Timeout(10 * time.Second))

    // With custom configuration:
    // app.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
    //  Timeout: 5 * time.Second,
    //  Message: "Sorry, your request took too long and was cancelled.", // Custom string message
    //  // Message: func(c *xylium.Context) string { // Custom func message
    //  //     return "Timeout on: " + c.Path()
    //  // },
    //  ErrorHandler: func(c *xylium.Context, err error) error { // Custom error handler
    //      c.Logger().Errorf("Request timed out: %v for path %s", err, c.Path())
    //      // Ensure response is not already committed if handler wrote something before timeout
    //      if !c.ResponseCommitted() {
    //          return c.Status(http.StatusGatewayTimeout).JSON(xylium.M{"error": "Request timed out (custom handler)"})
    //      }
    //      return err // Propagate original timeout error if response already sent
    //  },
    // }))
    ```
*   Handlers performing long-running operations should respect `c.GoContext().Done()` to abort early if the context is cancelled.
*   Refer to `middleware_timeout.go` for `TimeoutConfig` details.

### 6.9. OpenTelemetry (via `xylium-otel` Connector)

*   **Purpose**: Integrates with OpenTelemetry for distributed tracing.
*   **Behavior**: This functionality is now managed by the dedicated `xylium-otel` connector. The connector provides middleware to automatically instrument requests, manage OTel SDK setup (TracerProvider, Exporter, etc.), and integrate with Xylium's context and logger.
*   **Usage**:
    1.  Install the connector: `go get github.com/arwahdevops/xylium-otel` (adjust path if needed).
    2.  Initialize the `xyliumotel.Connector` in your application.
    3.  Use `otelConnector.OtelMiddleware()` to apply the tracing middleware.
*   **Refer to**:
    *   The documentation for the **[`xylium-otel` connector](LINK_TO_XYLIUM_OTEL_README)** for detailed setup, configuration, and usage instructions.
    *   `Docs/XyliumConnectors.md` for an overview of Xylium's connector philosophy.
    *   `Docs/OpenTelemetry.md` in Xylium Core for general concepts on how Xylium supports OTel integration.

By leveraging Xylium's middleware system and its built-in components (or dedicated connectors), you can build robust, secure, and observable web applications efficiently.
