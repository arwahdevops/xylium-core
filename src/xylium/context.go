package xylium

import (
	"context" // For Go's context.Context
	"sync"    // For sync.RWMutex, sync.Once for thread-safety and one-time operations.

	"github.com/go-playground/validator/v10" // For default struct validation.
	"github.com/valyala/fasthttp"            // For fasthttp.RequestCtx, the underlying request context.
)

// --- Validator Management ---

var (
	// defaultValidator holds the global validator instance used by `c.BindAndValidate`.
	defaultValidator *validator.Validate
	// defaultValidatorLock protects concurrent access to `defaultValidator`.
	defaultValidatorLock sync.RWMutex
)

// init initializes the defaultValidator instance when the xylium package is loaded.
func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows an application to replace Xylium's default validator.
// Panics if `v` is nil.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: custom validator provided to SetCustomValidator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the currently configured global validator instance.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// --- Context Struct ---

// Context represents the context of a single HTTP request.
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
	// data between middleware and handlers. It is protected by `mu`.
	store map[string]interface{}
	// mu protects concurrent access to the `store` map.
	// It is a pointer to allow Context to be shallow copied (e.g., in WithGoContext)
	// while sharing the same lock and underlying store map instance.
	mu *sync.RWMutex

	// router is a reference to the Xylium Router instance that routed this request.
	router *Router

	// queryArgs caches parsed query arguments to avoid re-parsing on multiple accesses.
	queryArgs *fasthttp.Args
	// formArgs caches parsed form arguments to avoid re-parsing on multiple accesses.
	formArgs *fasthttp.Args

	// responseOnce ensures that certain response-related initializations (like setting
	// a default Content-Type) happen only once per request.
	responseOnce sync.Once // This field caused the go vet warning if Context was copied directly.

	// goCtx is the standard Go context.Context associated with this request.
	// It's used for cancellation signals, deadlines, and passing request-scoped values.
	goCtx context.Context
}

// reset is called when a Context instance is released back to the pool.
// It clears all request-specific data to prepare the Context for reuse.
func (c *Context) reset() {
	c.Ctx = nil

	if c.Params != nil {
		for k := range c.Params {
			delete(c.Params, k)
		}
	} else {
		c.Params = make(map[string]string)
	}

	c.handlers = c.handlers[:0]
	c.index = -1

	if c.mu == nil {
		c.mu = new(sync.RWMutex)
	}
	// No lock needed here if reset is guaranteed to be called non-concurrently for this instance.
	if c.store == nil {
		c.store = make(map[string]interface{})
	}
	for k := range c.store {
		delete(c.store, k)
	}

	c.router = nil
	c.queryArgs = nil
	c.formArgs = nil
	c.responseOnce = sync.Once{} // Reset sync.Once for the next use.
	c.goCtx = nil
}

// Next executes the next handler in the middleware chain.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil
}

// setRouter associates the router with the context. Internal use.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response headers have been sent.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}
	if c.Ctx.Hijacked() {
		return true
	}
	resp := &c.Ctx.Response
	if resp.StatusCode() == fasthttp.StatusSwitchingProtocols {
		return true
	}
	if resp.IsBodyStream() {
		return true
	}
	if len(resp.Body()) > 0 {
		return true
	}
	if resp.Header.ContentLength() >= 0 {
		return true
	}
	return false
}

// RouterMode returns the operating mode of the Xylium router.
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	return ""
}

// Logger returns a `xylium.Logger` instance for the current request context.
func (c *Context) Logger() Logger {
	if c.router == nil || c.router.Logger() == nil {
		emergencyLogger := NewDefaultLogger()
		emergencyLogger.SetLevel(LevelWarn)
		var pathInfo = "unknown_path (context.router or c.router.Logger() is nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil {
			pathInfo = string(c.Ctx.Path())
		}
		emergencyLogger.Warnf(
			"Context.Logger() called but c.router or c.router.Logger() is nil for request path: '%s'. "+
				"Returning a temporary emergency logger.", pathInfo)
		return emergencyLogger
	}

	baseLogger := c.router.Logger()
	logFields := M{}

	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			logFields[string(ContextKeyRequestID)] = requestIDString
		}
	}
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

	if len(logFields) > 0 {
		return baseLogger.WithFields(logFields)
	}
	return baseLogger
}

// GoContext returns the standard Go context.Context associated with this request.
func (c *Context) GoContext() context.Context {
	if c.goCtx == nil {
		return context.Background()
	}
	return c.goCtx
}

// WithGoContext returns a Xylium Context derived from c, with its
// Go `context.Context` replaced by `goCtx`.
// The new context (`newC`) shares the same `store` map and `mu` (RWMutex) instance
// as the original context `c`.
// `responseOnce` is re-initialized for `newC` to manage its own response state.
// Other fields like Ctx, Params, handlers, index, router, queryArgs, formArgs
// are shallow copied.
// Panics if `goCtx` is nil.
func (c *Context) WithGoContext(goCtx context.Context) *Context {
	if goCtx == nil {
		panic("xylium: WithGoContext cannot be called with a nil context.Context")
	}

	// Manually construct the new Context to avoid copying sync.Once value.
	newC := &Context{
		// Shallow copy or share these fields:
		Ctx:       c.Ctx,
		Params:    c.Params,    // Map is a reference type, shared
		handlers:  c.handlers,  // Slice header is copied, underlying array shared
		index:     c.index,     // Value copy
		store:     c.store,     // Map is a reference type, shared
		mu:        c.mu,        // Pointer to RWMutex is copied, RWMutex instance shared
		router:    c.router,    // Pointer, shared
		queryArgs: c.queryArgs, // Pointer, shared
		formArgs:  c.formArgs,  // Pointer, shared

		// Initialize these fields as new/independent for newC:
		responseOnce: sync.Once{}, // New sync.Once instance
		goCtx:        goCtx,       // The new Go context
	}

	return newC
}
