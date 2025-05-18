package xylium

import (
	"context" // For Go's context.Context
	"sync"    // For sync.RWMutex, sync.Once for thread-safety and one-time operations.

	"github.com/go-playground/validator/v10" // For default struct validation.
	"github.com/valyala/fasthttp"          // For fasthttp.RequestCtx, the underlying request context.
)

// --- Validator Management ---
// Xylium uses "github.com/go-playground/validator/v10" for struct validation by default.
// These variables and functions manage the global validator instance.

var (
	// defaultValidator holds the global validator instance used by `c.BindAndValidate`.
	// It is initialized with `validator.New()`.
	defaultValidator *validator.Validate
	// defaultValidatorLock protects concurrent access to `defaultValidator` during
	// initialization or when `SetCustomValidator` is called.
	defaultValidatorLock sync.RWMutex
)

// init initializes the defaultValidator instance when the xylium package is loaded.
// This ensures a validator is ready for use without explicit setup by the application,
// though it can be replaced using `SetCustomValidator`.
func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows an application to replace Xylium's default validator
// with a custom instance of `*validator.Validate`. This is useful for registering
// custom validation functions or using a validator with specific configurations.
// This function is thread-safe. Panics if `v` is nil.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: custom validator provided to SetCustomValidator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the currently configured global validator instance.
// This can be used by applications if they need direct access to the validator
// (e.g., for validating structs outside of `c.BindAndValidate`).
// This function is thread-safe for reads.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// --- Context Struct ---

// Context represents the context of a single HTTP request. It holds all request-specific
// information, provides methods for request parsing and response generation, manages
// middleware execution, and facilitates data sharing between handlers.
// Context instances are pooled and reused to reduce memory allocations.
type Context struct {
	// Ctx is the underlying fasthttp.RequestCtx, providing access to low-level request/response details.
	Ctx *fasthttp.RequestCtx

	// Params stores route parameters extracted from the URL path (e.g., {"id": "123"}).
	Params map[string]string

	// handlers is the chain of handlers (middleware and route handler) to be executed for this request.
	handlers []HandlerFunc
	// index is the current index in the `handlers` chain, used by `c.Next()`.
	index int

	// store is a key-value store private to this request context, used for passing
	// data between middleware and handlers (e.g., user information after authentication, request ID).
	store map[string]interface{}
	// mu protects concurrent access to the `store` map.
	mu sync.RWMutex

	// router is a reference to the Xylium Router instance that routed this request.
	// It provides access to router-level config (e.g., mode, logger).
	router *Router

	// queryArgs caches parsed query arguments to avoid re-parsing on multiple accesses.
	queryArgs *fasthttp.Args
	// formArgs caches parsed form arguments to avoid re-parsing on multiple accesses.
	formArgs *fasthttp.Args

	// responseOnce ensures that certain response-related initializations (like setting
	// a default Content-Type) happen only once per request.
	responseOnce sync.Once

	// goCtx is the standard Go context.Context associated with this request.
	// It's used for cancellation signals, deadlines, and passing request-scoped values
	// in a way that's idiomatic to Go's concurrency patterns.
	// Middleware like Timeout will operate on this context.
	goCtx context.Context
}

// reset is called when a Context instance is released back to the pool.
// It clears all request-specific data to prepare the Context for reuse,
// preventing data leakage between requests.
func (c *Context) reset() {
	c.Ctx = nil
	for k := range c.Params {
		delete(c.Params, k)
	}
	c.handlers = c.handlers[:0]
	c.index = -1
	for k := range c.store {
		delete(c.store, k)
	}
	c.router = nil
	c.queryArgs = nil
	c.formArgs = nil
	c.responseOnce = sync.Once{}
	c.goCtx = nil // Reset the Go context.Context field.
}

// Next executes the next handler in the middleware chain.
// It is called by middleware to pass control to the subsequent middleware or the main route handler.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil // No more handlers in the chain.
}

// setRouter is an internal method used by the router to associate itself
// with the acquired context.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response headers have been sent or if the
// response is otherwise in a state where it cannot be modified.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}
	if c.Ctx.Hijacked() {
		return true
	}
	resp := &c.Ctx.Response
	sc := resp.StatusCode()
	if sc == fasthttp.StatusSwitchingProtocols {
		return true
	}
	if resp.IsBodyStream() {
		return true
	}
	bodyLen := len(resp.Body())
	contentLengthSet := resp.Header.ContentLength() >= 0

	if sc != fasthttp.StatusOK && sc != 0 {
		return true
	}
	if bodyLen > 0 {
		return true
	}
	if contentLengthSet {
		return true
	}
	return false
}

// RouterMode returns the operating mode (e.g., "debug", "release") of the Xylium router
// that is handling the current request.
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	return "" // Fallback if router is unexpectedly nil.
}

// Logger returns a `xylium.Logger` instance for the current request context.
// It's configured based on Xylium's operating mode and automatically includes
// fields like `request_id`, `trace_id`, and `span_id` if they are present in the context store.
func (c *Context) Logger() Logger {
	// Check for a valid router and its logger.
	if c.router == nil || c.router.Logger() == nil {
		emergencyLogger := NewDefaultLogger() // Assumes NewDefaultLogger() is available.
		emergencyLogger.SetLevel(LevelWarn)
		var pathInfo string = "unknown_path (context.router or context.router.Logger() is nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil {
			pathInfo = string(c.Ctx.Path())
		}
		emergencyLogger.Warnf(
			"Context.Logger() called but c.router or c.router.Logger() is nil for request path: '%s'. "+
				"Returning a temporary emergency logger. Please investigate context initialization.",
			pathInfo,
		)
		return emergencyLogger
	}

	baseLogger := c.router.Logger()
	logFields := M{} // Initialize an empty map for fields to add.

	// Add RequestID if present.
	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			logFields[string(ContextKeyRequestID)] = requestIDString
		}
	}

	// Add OpenTelemetry TraceID and SpanID if present from the context store.
	// These keys ("otel_trace_id", "otel_span_id") should match what an OTel middleware sets.
	if traceIDVal, exists := c.Get("otel_trace_id"); exists {
		if traceID, ok := traceIDVal.(string); ok && traceID != "" {
			logFields["trace_id"] = traceID
		}
	}
	if spanIDVal, exists := c.Get("otel_span_id"); exists {
		if spanID, ok := spanIDVal.(string); ok && spanID != "" {
			logFields["span_id"] = spanID
		}
	}

	// If any fields were collected, return a logger derived with these fields.
	if len(logFields) > 0 {
		return baseLogger.WithFields(logFields)
	}

	// Otherwise, return the base logger as is.
	return baseLogger
}

// GoContext returns the standard Go context.Context associated with this Xylium request context.
// This context is managed by Xylium and is used for cancellation signals, deadlines,
// and passing request-scoped values across API boundaries.
// Middleware (e.g., Timeout) will operate on this context.
// It defaults to `context.Background()` if it was not properly initialized (though `acquireCtx` should prevent this).
func (c *Context) GoContext() context.Context {
	if c.goCtx == nil {
		// Defensive fallback; `acquireCtx` should always initialize `c.goCtx`.
		return context.Background()
	}
	return c.goCtx
}

// WithGoContext returns a shallow copy of the Xylium Context with its underlying
// Go `context.Context` replaced by the provided `goCtx`.
// This is useful for middleware or handlers that derive a new Go context (e.g., with a new
// value or deadline) and want this new context to be accessible via `c.GoContext()` for
// subsequent operations.
//
// The original `xylium.Context` (`c`) is not modified. The caller is responsible
// for using the returned `xylium.Context` instance.
// Panics if `goCtx` is nil.
func (c *Context) WithGoContext(goCtx context.Context) *Context {
	if goCtx == nil {
		panic("xylium: WithGoContext cannot be called with a nil context.Context")
	}
	// Create a shallow copy of the current Xylium Context.
	newC := *c
	// Set the new Go context.Context on the copied Xylium Context.
	newC.goCtx = goCtx
	return &newC
}
