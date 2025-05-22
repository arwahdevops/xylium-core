// File: src/xylium/context.go
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
	responseOnce sync.Once

	// goCtx is the standard Go context.Context associated with this request.
	// It's used for cancellation signals, deadlines, and passing request-scoped values.
	goCtx context.Context
}

// reset is called when a Context instance is released back to the pool.
// It clears all request-specific data to prepare the Context for reuse.
func (c *Context) reset() {
	c.Ctx = nil // Clear fasthttp context reference.

	// Clear path parameters.
	if c.Params != nil {
		for k := range c.Params {
			delete(c.Params, k)
		}
	}
	// Note: c.Params map itself is reused, not re-made, unless it was nil.

	// Reset handlers slice.
	c.handlers = c.handlers[:0]
	c.index = -1 // Reset handler index.

	// Clear the request-scoped store.
	// mu and store maps are initialized by the pool.New or previous reset, so they should exist.
	if c.mu == nil { // Should not happen if pool.New is correct.
		c.mu = new(sync.RWMutex)
	}
	if c.store == nil { // Should not happen.
		c.store = make(map[string]interface{})
	}
	for k := range c.store {
		delete(c.store, k)
	}

	c.router = nil               // Clear router reference.
	c.queryArgs = nil            // Clear cached query arguments.
	c.formArgs = nil             // Clear cached form arguments.
	c.responseOnce = sync.Once{} // Reset sync.Once for the next use.
	c.goCtx = nil                // Clear Go context reference.
}

// Next executes the next handler in the middleware chain.
// If there are no more handlers, it does nothing and returns nil.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil
}

// setRouter associates the router with the context. Internal use by the framework.
func (c *Context) setRouter(r *Router) { // Keep unexported, called internally by router.Handler
	c.router = r
}

// ResponseCommitted checks if the response headers have been sent or if the response
// body has started to be written.
// This is useful for middleware or handlers to determine if they can still
// modify the response (e.g., set status code, headers, or write body).
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}

	// Hijacked connections are considered committed.
	if c.Ctx.Hijacked() {
		return true
	}

	resp := &c.Ctx.Response
	// StatusSwitchingProtocols implies headers have been sent for protocol upgrade.
	if resp.StatusCode() == fasthttp.StatusSwitchingProtocols {
		return true
	}

	// If the body is being streamed, headers are typically sent immediately.
	if resp.IsBodyStream() {
		// Untuk body stream, fasthttp mengirim header saat SetBodyStream dipanggil
		// atau saat pertama kali data ditulis ke stream, tergantung konfigurasinya.
		// Menganggapnya committed jika IsBodyStream true adalah asumsi yang cukup aman.
		return true
	}

	// Check if any body content has actually been written.
	// Ini adalah indikator paling kuat bahwa response sudah dimulai.
	// Gunakan metode Body() yang diekspor untuk mendapatkan []byte.
	if len(resp.Body()) > 0 { // <<< PERBAIKAN DARI resp.BodyBytes() MENJADI resp.Body()
		return true
	}

	// Jika ContentLength secara eksplisit disetel ke nilai non-negatif *dan* itu bukan nilai default 0
	// yang mungkin disetel fasthttp sebelum body ditulis, ini bisa jadi indikasi.
	// Namun, ini masih bisa ambigu. Fokus pada body yang sudah ditulis lebih aman.
	// Untuk sekarang, kita akan mengandalkan `len(resp.Body()) > 0` dan `IsBodyStream()`.

	// Pertimbangkan kasus di mana hanya header yang disetel dan statusnya adalah 204 atau 304.
	// Dalam kasus ini, tidak ada body yang diharapkan. Apakah ini "committed"?
	// Dari perspektif "tidak bisa mengubah header lagi", ya.
	// Namun, `fasthttp` mungkin masih mengizinkan perubahan header jika tidak ada body/stream.
	// Untuk konsistensi, kita akan tetap pada definisi "body ditulis atau stream dimulai atau hijack".

	return false
}

// RouterMode returns the operating mode (e.g., "debug", "release") of the Xylium router
// that is handling this context. Returns an empty string if the router is not set.
func (c *Context) RouterMode() string {
	if c.router != nil {
		return c.router.CurrentMode()
	}
	return "" // Or perhaps DebugMode as a very defensive default, though empty is clearer for "not set".
}

// Logger returns a `xylium.Logger` instance for the current request context.
// This logger is derived from the router's base logger and automatically includes
// contextual fields like `xylium_request_id`, `otel_trace_id`, and `otel_span_id` if they
// are present in the context's store.
func (c *Context) Logger() Logger {
	if c.router == nil || c.router.Logger() == nil {
		// This case should ideally not be reached in a normal request flow.
		// It might occur if Context is used outside a request cycle without proper setup.
		emergencyLogger := NewDefaultLogger() // Create a temporary logger.
		emergencyLogger.SetLevel(LevelWarn)
		var pathInfo = "unknown_path (context.router or c.router.Logger() is nil)"
		if c.Ctx != nil && c.Ctx.Path() != nil {
			pathInfo = string(c.Ctx.Path())
		}
		emergencyLogger.Warnf(
			"Context.Logger() called but c.router or c.router.Logger() is nil for request path: '%s'. "+
				"Returning a temporary emergency logger. Ensure Context is properly initialized by the router.", pathInfo)
		return emergencyLogger
	}

	baseLogger := c.router.Logger() // Get the router's configured base logger.
	logFields := M{}                // Initialize a map for contextual fields.

	// Attempt to retrieve and add standard contextual fields from the context store.
	if requestIDValue, exists := c.Get(ContextKeyRequestID); exists {
		if requestIDString, ok := requestIDValue.(string); ok && requestIDString != "" {
			logFields[string(ContextKeyRequestID)] = requestIDString
		}
	}
	// Standard keys for OpenTelemetry trace and span IDs (assuming they are set by OTel middleware).
	if traceIDVal, exists := c.Get(ContextKeyOtelTraceID); exists { // Using const from otel middleware
		if traceID, ok := traceIDVal.(string); ok && traceID != "" {
			logFields["trace_id"] = traceID // Common alias used in logs
		}
	}
	if spanIDVal, exists := c.Get(ContextKeyOtelSpanID); exists { // Using const from otel middleware
		if spanID, ok := spanIDVal.(string); ok && spanID != "" {
			logFields["span_id"] = spanID // Common alias used in logs
		}
	}

	// If any contextual fields were found, return a new logger instance enriched with these fields.
	// Otherwise, return the base logger directly.
	if len(logFields) > 0 {
		return baseLogger.WithFields(logFields)
	}
	return baseLogger
}

// GoContext returns the standard Go `context.Context` associated with this request.
// This context can be used for cancellation signals, deadlines, and passing
// request-scoped values to downstream services or libraries that support `context.Context`.
// If no Go context has been explicitly set (e.g., by middleware like Timeout or OpenTelemetry),
// it defaults to `context.Background()`.
func (c *Context) GoContext() context.Context {
	if c.goCtx == nil {
		// Default to context.Background() if c.goCtx was not initialized.
		// This ensures that c.GoContext() always returns a non-nil context.
		return context.Background()
	}
	return c.goCtx
}

// WithGoContext returns a new Xylium Context instance derived from `c`,
// but with its internal Go `context.Context` replaced by the provided `goCtx`.
// The new context (`newC`) shares the same underlying request-scoped store (`c.store`)
// and its lock (`c.mu`) with the original context `c`.
// Other fields like `Ctx` (fasthttp context), `Params`, `handlers`, `index`, `router`,
// `queryArgs`, and `formArgs` are shallow copied.
// The `responseOnce` field is re-initialized for `newC` to manage its own response state independently.
// This method is crucial for middleware (e.g., Timeout, OpenTelemetry) that need to
// propagate a modified Go context down the handler chain.
// Panics if the provided `goCtx` is nil.
func (c *Context) WithGoContext(goCtx context.Context) *Context {
	if goCtx == nil {
		panic("xylium: WithGoContext cannot be called with a nil context.Context")
	}

	// Manually construct the new Context to ensure correct shallow copying of reference types
	// and proper initialization of fields like responseOnce.
	newC := &Context{
		// Fields to shallow copy or share:
		Ctx:       c.Ctx,
		Params:    c.Params,
		handlers:  c.handlers,
		index:     c.index,
		store:     c.store,
		mu:        c.mu,
		router:    c.router,
		queryArgs: c.queryArgs,
		formArgs:  c.formArgs,

		// Fields to initialize as new/independent for newC:
		responseOnce: sync.Once{},
		goCtx:        goCtx,
	}
	return newC
}

// AppGet retrieves a value from the application-level store of the router
// associated with this context.
// Returns the value and true if the key exists, otherwise nil and false.
// This is useful for accessing shared services or configurations (e.g., database connectors)
// set globally on the router instance.
func (c *Context) AppGet(key string) (interface{}, bool) {
	if c.router == nil {
		if logger := c.Logger(); logger != nil { // Defensive check on logger
			logger.Warnf("AppGet: Attempted to get key '%s' from app store, but context's router is nil.", key)
		}
		return nil, false
	}
	return c.router.AppGet(key)
}

// MustAppGet retrieves a value from the application-level store of the router.
// It panics if the key does not exist in the application store or if the context's
// router is not set.
func (c *Context) MustAppGet(key string) interface{} {
	if c.router == nil {
		panic(fmt.Sprintf("xylium: MustAppGet called for key '%s', but context's router is nil.", key))
	}
	val, ok := c.router.AppGet(key)
	if !ok {
		panic(fmt.Sprintf("xylium: key '%s' does not exist in application store", key))
	}
	return val
}
