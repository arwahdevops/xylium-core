// src/xylium/context.go
package xylium

import (
	"sync" // For sync.RWMutex, sync.Once

	"github.com/go-playground/validator/v10" // For defaultValidator
	"github.com/valyala/fasthttp"          // For fasthttp.RequestCtx
)

// --- Validator (no changes needed from previous versions) ---
var (
	defaultValidator     *validator.Validate
	defaultValidatorLock sync.RWMutex
)

func init() {
	defaultValidator = validator.New()
}

func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: validator cannot be nil")
	}
	defaultValidator = v
}

func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// --- Context Struct (no changes needed) ---
// Context represents the context of the current HTTP request.
type Context struct {
	Ctx      *fasthttp.RequestCtx
	Params   map[string]string
	handlers []HandlerFunc
	index    int
	store    map[string]interface{}
	mu       sync.RWMutex // Protects 'store'.
	router   *Router      // Reference to the router.

	queryArgs *fasthttp.Args
	formArgs  *fasthttp.Args

	responseOnce sync.Once
}

// reset (no changes needed from previous versions)
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

// Next (no changes needed)
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil
}

// setRouter (no changes needed)
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted (no changes needed)
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil { return false }
	if c.Ctx.Hijacked() { return true }
	resp := &c.Ctx.Response
	sc := resp.StatusCode()
	if sc == fasthttp.StatusSwitchingProtocols { return true }
	if resp.IsBodyStream() { return true }
	bodyLen := len(resp.Body())
	contentLengthSet := resp.Header.ContentLength() >= 0
	if sc != fasthttp.StatusOK && sc != 0 { return true }
	if bodyLen > 0 { return true }
	if contentLengthSet { return true }
	return false
}

// RouterMode (no changes needed)
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	return "" // Or default to ReleaseMode if router is unexpectedly nil.
}

// Logger returns a xylium.Logger instance for the current request context.
// If the RequestID middleware has been used and a request ID is present in the
// context store, this method returns a new logger instance (derived from the
// router's base logger) that automatically includes the 'request_id' field
// in all its log entries. Otherwise, it returns the router's base logger.
// It includes a fallback to a new DefaultLogger if the router or its logger is nil,
// which should ideally not happen in a normal request lifecycle.
func (c *Context) Logger() Logger {
	// Check for a valid router and its logger.
	if c.router == nil || c.router.Logger() == nil {
		// This is an unexpected state, likely indicating an issue with context initialization
		// or a call to c.Logger() outside a valid Xylium request lifecycle.
		// Create a temporary, emergency logger.
		emergencyLogger := NewDefaultLogger() // Assumes NewDefaultLogger() is available.
		emergencyLogger.SetLevel(LevelWarn)   // Log this warning at WARN level.

		// Attempt to get some identifying information for the log message.
		var pathInfo string = "unknown_path (router/logger_is_nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil {
			pathInfo = string(c.Ctx.Path())
		}

		emergencyLogger.Warnf(
			"Context.Logger() called but c.router or c.router.Logger() is nil for request to '%s'. "+
				"This is highly unusual. Returning a temporary emergency logger. "+
				"Please check context initialization.",
			pathInfo,
		)
		return emergencyLogger
	}

	// Get the base logger from the router. This logger is already configured
	// (level, color, etc.) by the router based on the Xylium operating mode.
	baseLogger := c.router.Logger()

	// Check if a RequestID is available in the context store.
	// ContextKeyRequestID is a const defined in middleware_requestid.go.
	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			// If a valid request ID exists, return a new logger instance
			// with the 'request_id' field automatically included.
			// The key for the field is also taken from ContextKeyRequestID for consistency.
			return baseLogger.WithFields(M{string(ContextKeyRequestID): requestIDString})
		}
		// If requestIDValue exists but is not a valid string, log a debug message (optional)
		// and fall through to return the baseLogger.
		// baseLogger.Debugf("Context.Logger: Found ContextKeyRequestID but it's not a valid string: %T, %v", requestIDValue, requestIDValue)
	}

	// If no valid RequestID is found, return the router's base logger.
	return baseLogger
}
