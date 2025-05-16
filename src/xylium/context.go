package xylium

import (
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
)

var (
	defaultValidator     *validator.Validate
	defaultValidatorLock sync.RWMutex
)

func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows replacing the default global validator instance.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: validator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the current global validator instance.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// Context represents the context of the current HTTP request.
type Context struct {
	Ctx      *fasthttp.RequestCtx
	Params   map[string]string
	handlers []HandlerFunc
	index    int
	store    map[string]interface{}
	mu       sync.RWMutex
	router   *Router

	queryArgs *fasthttp.Args
	formArgs  *fasthttp.Args

	responseOnce sync.Once
}

// reset prepares the Context instance for reuse.
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
}

// Next executes the next handler in the middleware chain.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil
}

// setRouter associates the router with the context.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response header has already been sent.
// This implementation infers the committed state based on fasthttp.Response properties,
// as fasthttp.RequestCtx.ResponseWritten() is not available in fasthttp v1.62.0.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}

	// If fasthttp.RequestCtx.Hijacked() is true, the connection is no longer
	// under fasthttp's control for standard HTTP responses.
	// We should consider it "committed" from Xylium's perspective to prevent
	// global error handlers from trying to write a standard HTTP error.
	if c.Ctx.Hijacked() {
		return true
	}

	// Get response properties
	resp := &c.Ctx.Response // Pointer to fasthttp.Response
	sc := resp.StatusCode()

	// For 101 Switching Protocols, if the status is set,
	// we assume headers are sent or about to be sent and control will be passed.
	// Treat as committed to prevent Xylium's error handler interference.
	if sc == fasthttp.StatusSwitchingProtocols {
		return true // Or check if any header has been set for 101.
		             // For simplicity, if status is 101, assume it's on its way to being committed.
	}

	// If a body stream is set, fasthttp will handle writing, assume committed.
	if resp.IsBodyStream() {
		return true
	}

	bodyLen := len(resp.Body())
	contentLengthSet := resp.Header.ContentLength() >= 0 // -1 if not set, -2 if chunked

	// Heuristics:
	// - Status code is set (and not a default 200 that might be implicit before body).
	// - Body has been written.
	// - Content-Length has been explicitly set.
	// Note: fasthttp.Response.Header.disableRedirectFollowing is an internal field.
	// fasthttp sets status code to 0 initially. It gets set to 200 by default if body is written.

	if sc != fasthttp.StatusOK && sc != 0 { // Any non-OK, non-zero status code implies headers are likely set.
		return true
	}
	if bodyLen > 0 { // If body is written, headers are definitely sent.
		return true
	}
	if contentLengthSet { // If Content-Length is set, headers are likely configured.
		return true
	}

	// If status is 200 (default or explicit) but no body and no content-length,
	// it's likely nothing has been sent yet.
	// If status is 0, nothing has been sent.
	return false
}
