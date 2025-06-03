package xylium

import (
	"context" // For Go's context.Context
	"fmt"     // For fmt.Sprintf in MustGet panic message.
	"sync"    // For sync.RWMutex, sync.Once for thread-safety and one-time operations.

	"github.com/go-playground/validator/v10" // For default struct validation.
	"github.com/valyala/fasthttp"            // For fasthttp.RequestCtx, the underlying request context.
)

// --- Validator Management ---

var (
	// defaultValidator holds the global validator instance used by `c.BindAndValidate`.
	// This instance is of type `*validator.Validate` from the `go-playground/validator/v10` package.
	defaultValidator *validator.Validate
	// defaultValidatorLock protects concurrent access to `defaultValidator`.
	// This ensures that `SetCustomValidator` and `GetValidator` operations are thread-safe.
	defaultValidatorLock sync.RWMutex
)

// init initializes the defaultValidator instance with a new `validator.Validate`
// when the xylium package is loaded. This ensures a validator is always available
// unless explicitly replaced by `SetCustomValidator`.
func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows an application to replace Xylium's default `*validator.Validate` instance.
// This is useful for registering custom validation functions, translations, or using a pre-configured
// validator instance shared across the application.
//
// The provided validator `v` will be used for all subsequent `c.BindAndValidate()` calls
// across all router instances created thereafter, unless `SetCustomValidator` is called again.
//
// Panics if `v` is nil, as a nil validator cannot perform validation.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: custom validator provided to SetCustomValidator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the currently configured global `*validator.Validate` instance.
// This is the validator that Xylium will use for `c.BindAndValidate()` calls.
// This function is thread-safe.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// --- Context Struct ---

// Context represents the context of a single HTTP request within the Xylium framework.
// It encapsulates the underlying `fasthttp.RequestCtx`, route parameters, request-scoped data,
// a Go `context.Context`, and provides helper methods for request handling and response generation.
//
// Context instances are pooled and reused via `sync.Pool` to reduce memory allocations
// and improve performance. Therefore, a Context instance should not be held or used
// after the request handler it was passed to has returned.
type Context struct {
	// Ctx is the underlying `fasthttp.RequestCtx` from the fasthttp library.
	// It provides low-level access to the raw HTTP request and response details.
	// Direct manipulation of `Ctx` should be done with caution, as Xylium's helper
	// methods often provide a safer and more idiomatic interface.
	Ctx *fasthttp.RequestCtx

	// Params stores route parameters extracted from the URL path by the router.
	// For a route like `/users/:id`, if the request is `/users/123`, `Params`
	// would contain `{"id": "123"}`.
	Params map[string]string

	// handlers is the chain of `HandlerFunc` (middleware and the final route handler)
	// to be executed for the current request. This slice is populated by the router.
	handlers []HandlerFunc
	// index tracks the current position in the `handlers` chain. It is incremented
	// by `c.Next()` to execute the subsequent handler.
	index int

	// store is a key-value map private to this request context. It is used for passing
	// data between middleware and handlers (e.g., authenticated user information,
	// request-specific metrics). Access to `store` is protected by `mu`.
	store map[string]interface{}
	// mu is a read-write mutex that protects concurrent access to the `store` map.
	// It is a pointer to allow `Context` instances to be shallow-copied (e.g., by `WithGoContext`)
	// while still sharing the same lock and underlying `store` map instance, ensuring
	// data consistency across derived contexts within the same request lifecycle.
	mu *sync.RWMutex

	// router is a reference to the `xylium.Router` instance that processed this request.
	// This allows the Context to access application-level configurations and services
	// managed by the router, such as the application logger or shared data via `AppGet`.
	router *Router

	// queryArgs caches parsed URL query arguments (`fasthttp.Args`) to avoid redundant parsing
	// on multiple calls to methods like `c.QueryParam()`. It is lazily initialized.
	queryArgs *fasthttp.Args
	// formArgs caches parsed form arguments (`fasthttp.Args`) from POST/PUT request bodies
	// (e.g., from `application/x-www-form-urlencoded` or `multipart/form-data`).
	// It is lazily initialized on first access to form data.
	formArgs *fasthttp.Args

	// responseOnce ensures that certain response-related initializations, such as setting
	// a default `Content-Type` header, occur at most once per request lifecycle.
	// This prevents unintended overwrites if multiple response methods are called.
	responseOnce sync.Once

	// goCtx is the standard Go `context.Context` associated with this request.
	// It facilitates handling of deadlines, cancellation signals (e.g., from client disconnects
	// or middleware like `Timeout`), and propagation of request-scoped values to downstream
	// services or goroutines that are `context.Context`-aware.
	goCtx context.Context
}

// reset is called when a Context instance is released back to the `sync.Pool`.
// It meticulously clears all request-specific data to prepare the Context for safe reuse
// in a subsequent request, preventing data leakage between requests.
func (c *Context) reset() {
	c.Ctx = nil // Clear reference to fasthttp.RequestCtx.

	// Clear path parameters map. The map itself is reused if it exists.
	if c.Params != nil {
		for k := range c.Params {
			delete(c.Params, k)
		}
	}

	// Reset handlers slice and current handler index.
	c.handlers = c.handlers[:0] // Clears the slice while retaining underlying array capacity.
	c.index = -1                // Reset index to indicate no handlers have been run.

	// Clear the request-scoped store.
	// `c.mu` and `c.store` are initialized by the pool's New function or a previous reset,
	// so they should always be non-nil. Defensive checks are minimal here for performance
	// as this is an internal method.
	if c.mu == nil { // Should ideally not happen if pool management is correct.
		c.mu = new(sync.RWMutex)
	}
	if c.store == nil { // Should ideally not happen.
		c.store = make(map[string]interface{})
	}
	for k := range c.store {
		delete(c.store, k)
	}

	c.router = nil               // Clear reference to the router.
	c.queryArgs = nil            // Clear cached query arguments.
	c.formArgs = nil             // Clear cached form arguments.
	c.responseOnce = sync.Once{} // Reset sync.Once for the next request.
	c.goCtx = nil                // Clear Go context.Context reference.
}

// Next executes the next handler in the middleware chain for the current request.
// It increments the internal handler index and calls the corresponding `HandlerFunc`.
// If there are no more handlers in the chain (i.e., all middleware and the main
// route handler have been executed), `Next` does nothing and returns nil.
//
// Middleware should call `c.Next()` to pass control to the subsequent handler.
// If a middleware does not call `c.Next()`, it effectively short-circuits
// the request processing chain.
//
// Returns an error if the executed handler returns an error, otherwise nil.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil // No more handlers to execute.
}

// setRouter associates the `xylium.Router` with this `Context`.
// This method is intended for internal use by the framework (specifically by `Router.Handler`)
// during context initialization for a new request.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the HTTP response headers have been sent to the client
// or if any part of the response body has started to be written.
// Once a response is considered "committed," modifications to the status code
// or headers are generally not possible or may have no effect.
//
// This method is useful for middleware or complex handlers to determine if they
// can still safely modify the response (e.g., to send a custom error response).
// Xylium's `GlobalErrorHandler` also uses this check.
//
// A response is considered committed if:
//   - The connection has been hijacked (e.g., for WebSockets).
//   - The status code is `101 Switching Protocols`.
//   - The response body is being streamed (`c.Ctx.Response.IsBodyStream()` is true).
//   - Any part of the response body has actually been written (`len(c.Ctx.Response.Body()) > 0`).
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		// Should not happen in a normal request lifecycle.
		// If Ctx is nil, assume not committed for safety, though this indicates a deeper issue.
		return false
	}

	// Hijacked connections are definitely committed as control is passed elsewhere.
	if c.Ctx.Hijacked() {
		return true
	}

	resp := &c.Ctx.Response
	// StatusSwitchingProtocols implies headers have been sent for protocol upgrade.
	if resp.StatusCode() == fasthttp.StatusSwitchingProtocols {
		return true
	}

	// If the body is being streamed, headers are typically sent immediately
	// by fasthttp when SetBodyStream is called or on the first write to the stream.
	if resp.IsBodyStream() {
		return true
	}

	// The most reliable indicator: if any body content has actually been written.
	// fasthttp.Response.Body() returns the current buffered body.
	if len(resp.Body()) > 0 {
		return true
	}

	// Note: Checking ContentLength can be ambiguous as it might be set before
	// actual body writes or might be -1 (chunked) or 0 (for empty body responses
	// like 204 No Content, where headers are sent but no body).
	// Relying on the above checks (hijack, status 101, body stream, body written)
	// provides a robust determination.

	return false
}

// RouterMode returns the operating mode (e.g., "debug", "release", "test") of the
// `xylium.Router` instance that is handling the current request.
// This can be used by handlers or middleware to alter their behavior based on the
// environment (e.g., providing more detailed debug information).
//
// Returns an empty string if the router is not set on the context, though this
// should not occur in a standard request lifecycle.
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	// This indicates an issue with context setup if reached during a request.
	return ""
}

// Logger returns a `xylium.Logger` instance scoped to the current request.
// This logger is derived from the application's base logger (configured on the `Router`)
// and is automatically enriched with contextual fields if they are present in the
// context's store. Standard fields that are automatically included (if available) are:
//   - `xylium_request_id` (from `ContextKeyRequestID`, typically set by `RequestID` middleware).
//   - `trace_id` (from `ContextKeyOtelTraceID`, typically set by OpenTelemetry middleware).
//   - `span_id` (from `ContextKeyOtelSpanID`, typically set by OpenTelemetry middleware).
//
// Using `c.Logger()` ensures that log messages are consistently formatted and
// can be easily correlated to specific requests or traces.
func (c *Context) Logger() Logger {
	if c.router == nil || c.router.Logger() == nil {
		// This state indicates a severe misconfiguration or misuse of Context outside
		// a normal request cycle. Log a warning using a temporary emergency logger.
		emergencyLogger := NewDefaultLogger() // Create a temporary, minimally configured logger.
		emergencyLogger.SetLevel(LevelWarn)   // Ensure warnings are visible.
		var pathInfo = "unknown_path (Context.router or Context.router.Logger() is nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil {
			pathInfo = string(c.Ctx.Path()) // Try to get path info for better debugging.
		}
		// Log a detailed warning message.
		emergencyLogger.Warnf(
			"Context.Logger() called but c.router or c.router.Logger() is nil for request path: '%s'. "+
				"Returning a temporary emergency logger. Ensure Context is properly initialized by the router.", pathInfo)
		return emergencyLogger
	}

	baseLogger := c.router.Logger() // Get the router's configured base logger.
	logFields := M{}                // Initialize a map for contextual log fields.

	// Attempt to retrieve and add standard contextual fields from the context store.
	// These keys are defined as constants in types.go (e.g., ContextKeyRequestID).
	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			// Use the string value of ContextKeyRequestID directly as the log field key for consistency.
			logFields[string(ContextKeyRequestID)] = requestIDString
		}
	}
	// Check for OpenTelemetry trace and span IDs.
	if traceIDVal, exists := c.Get(ContextKeyOtelTraceID); exists {
		if traceID, ok := traceIDVal.(string); ok && traceID != "" {
			// Use a common, shorter alias "trace_id" in logs for better readability in log aggregation systems.
			logFields["trace_id"] = traceID
		}
	}
	if spanIDVal, exists := c.Get(ContextKeyOtelSpanID); exists {
		if spanID, ok := spanIDVal.(string); ok && spanID != "" {
			// Use "span_id" as the log field key.
			logFields["span_id"] = spanID
		}
	}

	// If any contextual fields were found, return a new logger instance enriched with these fields
	// by calling `WithFields` on the base logger.
	// Otherwise, if no contextual fields are present, return the base logger directly to avoid
	// unnecessary logger allocations.
	if len(logFields) > 0 {
		return baseLogger.WithFields(logFields)
	}
	return baseLogger
}

// GoContext returns the standard Go `context.Context` associated with this `xylium.Context`.
// This `context.Context` is used throughout Xylium for managing request lifecycle events
// such as cancellation (e.g., due to client disconnect or timeout middleware) and deadlines.
// It is also the standard way to pass request-scoped values to downstream services or
// libraries that are `context.Context`-aware (e.g., database clients, HTTP clients).
//
// If no Go context has been explicitly set on this `xylium.Context` (e.g., by middleware
// like `Timeout` or OpenTelemetry integration), `GoContext()` defaults to returning
// `context.Background()`. This ensures that `c.GoContext()` always returns a non-nil,
// valid `context.Context`.
func (c *Context) GoContext() context.Context {
	if c.goCtx == nil {
		// Default to context.Background() if c.goCtx was not initialized or was reset.
		// This guarantees that c.GoContext() never returns a nil context.
		return context.Background()
	}
	return c.goCtx
}

// WithGoContext returns a new `xylium.Context` instance derived from the receiver `c`,
// but with its internal Go `context.Context` (accessible via `newC.GoContext()`)
// replaced by the provided `goCtx`.
//
// This method is primarily intended for use by middleware that need to modify the
// Go `context.Context` for downstream handlers (e.g., `Timeout` middleware adding a deadline,
// or OpenTelemetry middleware embedding span information).
//
// The new `xylium.Context` (`newC`) performs a shallow copy of most fields from `c`,
// including the underlying `fasthttp.RequestCtx`, route parameters (`Params`),
// the handler chain (`handlers` and `index`), and the `router` reference.
// Crucially, `newC` shares the same underlying request-scoped data store (`c.store`)
// and its associated lock (`c.mu`) with the original context `c`. This allows data set
// via `c.Set()` to be visible in `newC` and vice-versa.
//
// Fields specific to response generation, like `responseOnce`, are re-initialized for `newC`
// to ensure it manages its own response state independently if it were to write a response.
// However, typically, only the final handler in the chain writes the response using the
// original `fasthttp.RequestCtx` shared by all `xylium.Context` instances for that request.
//
// Panics if the provided `goCtx` is nil, as a `xylium.Context` must always have a valid
// (non-nil) Go `context.Context`.
func (c *Context) WithGoContext(goCtx context.Context) *Context {
	if goCtx == nil {
		panic("xylium: WithGoContext cannot be called with a nil context.Context")
	}

	// Manually construct the new Context to ensure correct shallow copying of reference types
	// (like c.store, c.mu, c.Ctx) and proper initialization of fields like responseOnce.
	// This avoids issues that might arise from a simple struct copy if fields need
	// different handling (e.g., some shared, some new).
	newC := &Context{
		// Fields shallow copied or shared:
		Ctx:       c.Ctx,       // Share the fasthttp context.
		Params:    c.Params,    // Share route parameters map.
		handlers:  c.handlers,  // Share the handler chain (index will diverge if Next is called).
		index:     c.index,     // Copy current index (Next on newC will advance its own).
		store:     c.store,     // Share the underlying key-value store.
		mu:        c.mu,        // Share the mutex for the store.
		router:    c.router,    // Share the router reference.
		queryArgs: c.queryArgs, // Share cached query args (read-only after parse).
		formArgs:  c.formArgs,  // Share cached form args (read-only after parse).

		// Fields re-initialized or set specific to newC:
		responseOnce: sync.Once{}, // newC gets its own responseOnce.
		goCtx:        goCtx,       // The new Go context.Context.
	}
	return newC
}

// AppGet retrieves a value from the application-level store. This store is managed
// by the `xylium.Router` instance associated with this context and is shared across
// all requests handled by that router.
//
// It is typically used to access shared services, configurations, or connection pools
// (e.g., database connectors, template engines) that are initialized globally for
// the application and set on the router via `router.AppSet(key, value)`.
//
// Parameters:
//   - `key` (string): The key of the value to retrieve.
//
// Returns:
//   - `interface{}`: The value associated with the key, if found.
//   - `bool`: True if the key exists in the application store, false otherwise.
//
// If the context's router is not set (which should not happen in a normal request
// lifecycle), this method logs a warning and returns nil, false.
func (c *Context) AppGet(key string) (interface{}, bool) {
	if c.router == nil {
		// Log a warning if called without a router, as AppGet relies on it.
		// Use c.Logger() if available, otherwise a temporary logger.
		logger := c.Logger() // Logger() handles nil router/logger internally.
		logger.Warnf("AppGet: Attempted to get key '%s' from application store, but context's router is nil. This may indicate context misuse.", key)
		return nil, false
	}
	return c.router.AppGet(key)
}

// MustAppGet retrieves a value from the application-level store of the router.
// It is similar to `AppGet` but panics if the key does not exist in the application store
// or if the context's router is not set.
//
// This method is useful when the presence of a value in the application store is
// considered a critical invariant for the handler's operation.
//
// Parameters:
//   - `key` (string): The key of the value to retrieve.
//
// Returns:
//   - `interface{}`: The value associated with the key.
//
// Panics:
//   - If `c.router` is nil.
//   - If the `key` is not found in the application store.
func (c *Context) MustAppGet(key string) interface{} {
	if c.router == nil {
		// Use fmt.Sprintf for panic message for consistency with other MustGet panics.
		panic(fmt.Sprintf("xylium: MustAppGet called for key '%s', but context's router is nil. Ensure context is properly initialized within a Xylium request.", key))
	}
	val, ok := c.router.AppGet(key)
	if !ok {
		panic(fmt.Sprintf("xylium: key '%s' does not exist in application store (accessed via c.MustAppGet)", key))
	}
	return val
}
