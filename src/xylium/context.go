// src/xylium/context.go
package xylium

import (
	"sync" // For sync.RWMutex, sync.Once

	"github.com/go-playground/validator/v10" // For defaultValidator
	"github.com/valyala/fasthttp"          // For fasthttp.RequestCtx
)

var (
	defaultValidator     *validator.Validate
	defaultValidatorLock sync.RWMutex
)

func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows replacing the default global validator instance.
// It should be called before any validation occurs, typically during application setup.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: validator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the current global validator instance.
// This can be used for custom validation logic outside of BindAndValidate.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// Context represents the context of the current HTTP request. It holds request and
// response information, path parameters, a data store, and a reference to the router.
type Context struct {
	Ctx      *fasthttp.RequestCtx   // The underlying fasthttp request context.
	Params   map[string]string      // Stores URL path parameters.
	handlers []HandlerFunc          // The chain of handlers to be executed for the request.
	index    int                    // Current index in the handlers chain.
	store    map[string]interface{} // A key-value store for passing data between middleware/handlers.
	mu       sync.RWMutex           // Mutex to protect concurrent access to the 'store'.
	router   *Router                // Reference to the router that handled this request.

	// Cached arguments to avoid re-parsing.
	queryArgs *fasthttp.Args // Cached query arguments.
	formArgs  *fasthttp.Args // Cached form arguments.

	// responseOnce ensures that certain response-related operations (like setting default content type)
	// happen only once.
	responseOnce sync.Once
}

// reset prepares the Context instance for reuse when it's put back into the pool.
// It clears or resets all fields to their initial state.
func (c *Context) reset() {
	c.Ctx = nil
	// Clear Params map. Iterate and delete to ensure underlying array might be GC'd if large.
	for k := range c.Params {
		delete(c.Params, k)
	}
	// Reset handlers slice without reallocating if possible.
	c.handlers = c.handlers[:0]
	c.index = -1
	// Clear store map.
	for k := range c.store {
		delete(c.store, k)
	}
	c.router = nil    // Remove reference to the router.
	c.queryArgs = nil // Clear cached query args.
	c.formArgs = nil  // Clear cached form args.
	// Reset responseOnce for the next use of this context object.
	c.responseOnce = sync.Once{}
}

// Next executes the next handler in the middleware chain for the current request.
// It's primarily used within middleware to pass control to the next middleware or handler.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil // No more handlers in the chain.
}

// setRouter (unexported) associates the router with the context.
// This is called internally when the context is acquired.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response header has already been sent.
// This implementation infers the committed state based on fasthttp.Response properties,
// as fasthttp.RequestCtx.ResponseWritten() is not directly available.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false // Context not properly initialized.
	}

	// If the connection is hijacked, fasthttp no longer controls standard HTTP responses.
	// Consider it "committed" from Xylium's perspective to prevent error handlers
	// from attempting to write a standard HTTP error.
	if c.Ctx.Hijacked() {
		return true
	}

	resp := &c.Ctx.Response // Pointer to the fasthttp.Response object.
	sc := resp.StatusCode()

	// For 101 Switching Protocols, if the status is set, we assume headers are sent
	// or are about to be sent, and control will be passed to another protocol handler.
	if sc == fasthttp.StatusSwitchingProtocols {
		return true
	}

	// If a body stream is set, fasthttp will handle writing; assume committed.
	if resp.IsBodyStream() {
		return true
	}

	bodyLen := len(resp.Body())
	contentLengthSet := resp.Header.ContentLength() >= 0 // -1 if not set, -2 if chunked.

	// Heuristics:
	// - A non-default status code (not 0 or 200 if no body) implies headers are likely set.
	// - If the body has been written, headers are definitely sent.
	// - If Content-Length has been explicitly set, headers are likely configured.
	if sc != fasthttp.StatusOK && sc != 0 {
		return true
	}
	if bodyLen > 0 {
		return true
	}
	if contentLengthSet {
		return true
	}

	// If status is 200 (default or explicit) but no body and no content-length,
	// or if status is 0, it's likely nothing has been sent yet.
	return false
}

// RouterMode returns the operating mode of the router associated with this context.
// Returns an empty string if the router is not set on the context (which should not
// happen in a normal request lifecycle handled by the framework).
// If an empty string is returned, the caller might default to ReleaseMode.
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	// This case should ideally not be reached if context is always properly
	// initialized by the router. Returning ReleaseMode or an empty string are options.
	// Empty string makes it explicit that the router info was missing.
	return ""
}
