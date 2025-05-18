package xylium

import (
	"sync" // For sync.RWMutex, sync.Once for thread-safety and one-time operations.

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
	Ctx      *fasthttp.RequestCtx // The underlying fasthttp.RequestCtx, providing access to low-level request/response details.
	Params   map[string]string    // Route parameters extracted from the URL path (e.g., {"id": "123"}).
	handlers []HandlerFunc        // The chain of handlers (middleware and route handler) to be executed for this request.
	index    int                  // The current index in the `handlers` chain, used by `c.Next()`.

	// store is a key-value store private to this request context, used for passing
	// data between middleware and handlers (e.g., user information after authentication, request ID).
	store map[string]interface{}
	mu    sync.RWMutex // Protects concurrent access to the `store` map.

	router *Router // A reference to the Xylium Router instance that routed this request. Provides access to router-level config (e.g., mode, logger).

	// queryArgs and formArgs cache parsed query and form arguments respectively
	// to avoid re-parsing on multiple accesses within the same request.
	queryArgs *fasthttp.Args
	formArgs  *fasthttp.Args

	// responseOnce ensures that certain response-related initializations (like setting
	// a default Content-Type) happen only once per request, even if response methods
	// like `c.Write` or `c.String` are called multiple times (though typically only one final
	// response method should be called).
	responseOnce sync.Once
}

// reset is called when a Context instance is released back to the pool.
// It clears all request-specific data to prepare the Context for reuse,
// preventing data leakage between requests.
func (c *Context) reset() {
	c.Ctx = nil // Clear reference to fasthttp context.
	// Clear Params map. Iterating and deleting is safer than `c.Params = nil` if the map
	// was shared, though pool.New re-creates it. This ensures it's empty.
	for k := range c.Params {
		delete(c.Params, k)
	}
	// Reset handlers slice to zero length, but keep underlying capacity for reuse.
	c.handlers = c.handlers[:0]
	c.index = -1 // Reset handler execution index.
	// Clear store map.
	for k := range c.store {
		delete(c.store, k)
	}
	c.router = nil    // Clear reference to the router.
	c.queryArgs = nil // Clear cached query arguments.
	c.formArgs = nil  // Clear cached form arguments.
	// Reset responseOnce for the next request using this context instance.
	// A new sync.Once is created because a used Once cannot be reset.
	c.responseOnce = sync.Once{}
}

// Next executes the next handler in the middleware chain.
// It is called by middleware to pass control to the subsequent middleware or the main route handler.
// If there are no more handlers, it does nothing and returns nil.
// Handlers should return `c.Next()` if they wish to continue the chain, or an error
// (or nil if they are terminating the response) if they don't.
func (c *Context) Next() error {
	c.index++ // Move to the next handler index.
	if c.index < len(c.handlers) {
		// Execute the handler at the current index.
		return c.handlers[c.index](c)
	}
	return nil // No more handlers in the chain.
}

// setRouter is an internal method used by the router to associate itself
// with the acquired context. This allows the context to access router-level
// configurations like the logger or operating mode.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response headers have been sent or if the
// response is otherwise in a state where it cannot be modified (e.g., hijacked).
// This is useful for middleware or handlers to determine if they can still
// write to the response or set headers.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false // Should not happen in a valid request lifecycle.
	}
	// Check fasthttp's indicators of a committed response.
	if c.Ctx.Hijacked() { // Connection was hijacked (e.g., for WebSockets).
		return true
	}
	resp := &c.Ctx.Response
	sc := resp.StatusCode()
	if sc == fasthttp.StatusSwitchingProtocols { // e.g., Upgrade to WebSocket.
		return true
	}
	if resp.IsBodyStream() { // Response body is being streamed.
		return true
	}
	// If a status code (other than 0 or 200 OK without body/content-length) has been set,
	// or if body has been written, or Content-Length is explicitly set,
	// consider the response (at least headers) committed.
	// Fasthttp might send headers early based on these.
	bodyLen := len(resp.Body())
	contentLengthSet := resp.Header.ContentLength() >= 0 // -1 if not set.

	// A non-zero, non-OK status code usually means headers are about to be/are sent.
	if sc != fasthttp.StatusOK && sc != 0 {
		return true
	}
	// If body has content, headers are likely sent or will be on first write.
	if bodyLen > 0 {
		return true
	}
	// If Content-Length is explicitly set (even to 0), headers are considered prepared.
	if contentLengthSet {
		return true
	}
	return false
}

// RouterMode returns the operating mode (e.g., "debug", "release") of the Xylium router
// that is handling the current request. This allows handlers and middleware to
// adapt their behavior based on the environment (e.g., more verbose errors in debug mode).
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	// Fallback if router is unexpectedly nil. This should ideally not happen.
	// Returning an empty string or a default mode like ReleaseMode can be debated.
	// Empty string indicates an issue more clearly.
	return ""
}

// Logger returns a `xylium.Logger` instance for the current request context.
//
// Key features:
// - It retrieves the base logger from the router (`c.router.Logger()`), which is already
//   configured based on Xylium's operating mode (level, color, caller info for DefaultLogger).
// - If the `RequestID` middleware has been used and a request ID is present in the
//   context store (under `ContextKeyRequestID`), this method returns a new logger instance
//   (derived from the router's base logger) that automatically includes the `request_id` field
//   in all its log entries. This is achieved by calling `baseLogger.WithFields(M{"request_id": "..."})`.
// - If no valid request ID is found, it returns the router's base logger directly.
// - Includes a fallback to a new `DefaultLogger` with a warning if the router or its logger
//   is unexpectedly nil. This is a defensive measure for robustness, though such a state
//   indicates a problem in the request lifecycle or context initialization.
func (c *Context) Logger() Logger {
	// Check for a valid router and its logger, which are essential.
	if c.router == nil || c.router.Logger() == nil {
		// This is an unexpected state. It likely indicates an issue with context
		// initialization or a call to c.Logger() outside a valid Xylium request lifecycle
		// (e.g., from a goroutine that lost its original context, or before router association).
		// Create a temporary, emergency logger to report this problem.
		emergencyLogger := NewDefaultLogger() // Assumes NewDefaultLogger() is available and functional.
		emergencyLogger.SetLevel(LevelWarn)   // Log this specific warning at WARN level.

		// Attempt to get some identifying information for the log message, if possible.
		var pathInfo string = "unknown_path (context.router or context.router.Logger() is nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil { // Check if fasthttp.RequestCtx is available.
			pathInfo = string(c.Ctx.Path())
		}

		emergencyLogger.Warnf( // Use Warnf for formatted message.
			"Context.Logger() called but c.router or c.router.Logger() is nil for request path: '%s'. "+
				"This is highly unusual and suggests an issue with context setup or lifecycle. "+
				"Returning a temporary emergency logger. Please investigate context initialization.",
			pathInfo,
		)
		return emergencyLogger // Return the emergency logger.
	}

	// Get the base logger from the router. This logger is already configured
	// (level, color, formatter, etc.) by the router based on the Xylium operating mode.
	baseLogger := c.router.Logger()

	// Check if a RequestID is available in the context store.
	// `ContextKeyRequestID` is a constant defined in `middleware_requestid.go`.
	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			// If a valid, non-empty request ID string exists, return a new logger instance
			// derived from baseLogger, with the 'request_id' field automatically included.
			// The key for the field is also taken from `ContextKeyRequestID` for consistency in logs.
			return baseLogger.WithFields(M{string(ContextKeyRequestID): requestIDString})
		}
		// Optional: Log a debug message if requestIDValue exists but is not a valid string.
		// This can help diagnose issues with how the request ID is being set or retrieved.
		// baseLogger.Debugf("Context.Logger: Found '%s' in context store, but it's not a valid non-empty string (type: %T, value: %v). Using base logger without request_id field.",
		//    ContextKeyRequestID, requestIDValue, requestIDValue)
	}

	// If no valid RequestID is found in the context store, return the router's base logger as is.
	return baseLogger
}
