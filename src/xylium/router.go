package xylium

import (
	"encoding/json" // For ServeFiles PathNotFound JSON response.
	"fmt"           // For error formatting and path/panic messages.
	"io"            // For HTMLRenderer interface and io.Closer.
	"os"            // For os.Stdout in logger config adjustments (NewWithConfig).
	"path/filepath" // For path cleaning and manipulation in ServeFiles.
	"runtime/debug" // For capturing stack traces on panic.
	"strings"       // For string manipulation (path normalization, joining).
	"sync"          // For sync.RWMutex and sync.Mutex.

	"github.com/valyala/fasthttp" // The underlying HTTP engine.
)

// HTMLRenderer defines the interface for HTML template rendering within Xylium.
// Applications can implement this interface to integrate their preferred HTML templating engine
// (e.g., Go's standard `html/template`, Pongo2, Amber) with Xylium's `c.HTML()` response method.
type HTMLRenderer interface {
	// Render is responsible for rendering the specified template and writing its output
	// to the provided `io.Writer` (which will be the HTTP response body writer).
	//
	// Parameters:
	//   - w (io.Writer): The writer to which the rendered HTML should be written.
	//   - name (string): The name or path of the template to render. The interpretation
	//                    of this name is up to the specific renderer implementation.
	//   - data (interface{}): The data context to be passed to the template during rendering.
	//                         This can be any type (e.g., map[string]interface{}, struct).
	//   - c (*Context): The current `xylium.Context`. This allows the renderer to access
	//                   request-specific information if needed (e.g., request ID, user session),
	//                   although direct access to response writing via `c` should be avoided
	//                   as `w` is the designated output.
	//
	// Returns:
	//   - error: An error if template rendering fails, nil otherwise.
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router is the central component of the Xylium framework, responsible for routing
// incoming HTTP requests to their appropriate handlers. It manages route registration,
// middleware execution, server configuration, application-level shared resources,
// and centralized error and panic handling.
type Router struct {
	// tree is the radix tree used for efficient matching of URL paths to handlers.
	// It supports static paths, named parameters, and catch-all parameters.
	tree *Tree
	// globalMiddleware is a slice of `Middleware` functions that are applied to
	// every request handled by this router, before any group-specific or
	// route-specific middleware.
	globalMiddleware []Middleware

	// PanicHandler is invoked when a panic is recovered during the processing of a request
	// (e.g., in a handler or middleware). If not explicitly set by the user,
	// Xylium uses `defaultPanicHandler`.
	PanicHandler HandlerFunc
	// NotFoundHandler is invoked when no registered route matches the requested URL path.
	// If not set, Xylium uses `defaultNotFoundHandler` (responds with HTTP 404).
	NotFoundHandler HandlerFunc
	// MethodNotAllowedHandler is invoked when a route matches the requested URL path,
	// but not the HTTP method used in the request. The router automatically sets the
	// "Allow" header with permitted methods before calling this handler.
	// If not set, Xylium uses `defaultMethodNotAllowedHandler` (responds with HTTP 405).
	MethodNotAllowedHandler HandlerFunc
	// GlobalErrorHandler is the central handler for processing all errors returned by
	// route handlers, middleware, or the `PanicHandler`. It is responsible for
	// logging the error and sending an appropriate HTTP response to the client.
	// If not set, Xylium uses `defaultGlobalErrorHandler`.
	GlobalErrorHandler HandlerFunc

	// serverConfig holds the configuration for the underlying `fasthttp.Server`
	// and Xylium-specific server operational settings.
	serverConfig ServerConfig
	// HTMLRenderer is an optional instance that implements the `HTMLRenderer` interface.
	// If set, it enables the use of `c.HTML()` for rendering HTML templates.
	HTMLRenderer HTMLRenderer
	// instanceMode stores the operating mode (e.g., "debug", "release", "test")
	// for this specific router instance. This mode influences behaviors like
	// default logger configuration and error reporting verbosity.
	instanceMode string

	// appStore is a thread-safe key-value store for application-level shared data.
	// It allows handlers and middleware to access global services or configurations
	// (e.g., database connectors, API clients) managed by the router.
	// Access is protected by `appStoreMux`.
	appStore map[string]interface{}
	// appStoreMux is a read-write mutex that protects concurrent access to `appStore`.
	appStoreMux sync.RWMutex

	// closers stores instances that implement `io.Closer`. These are resources
	// (e.g., database connection pools, file handles) that need to be explicitly
	// closed during the application's graceful shutdown process.
	// Resources are added here if they are set via `AppSet` and implement `io.Closer`,
	// or if they are explicitly registered via `RegisterCloser`.
	// Access is protected by `closersMux`.
	closers []io.Closer
	// closersMux is a mutex that protects concurrent access to the `closers` slice.
	closersMux sync.Mutex

	// internalRateLimitStores holds `LimiterStore` instances that are created internally
	// by Xylium (e.g., the default `InMemoryStore` for `RateLimiter` middleware if no
	// custom store is provided). These stores are registered here to ensure they are
	// properly closed during graceful shutdown.
	// Access is protected by `internalRateLimitStoresMux`.
	internalRateLimitStores []LimiterStore
	// internalRateLimitStoresMux is a mutex protecting `internalRateLimitStores`.
	internalRateLimitStoresMux sync.Mutex
}

// Logger returns the configured `xylium.Logger` instance for this router.
// This logger is used for application-wide logging (e.g., startup messages,
// internal router events) and serves as the base for request-scoped loggers
// obtained via `c.Logger()`.
//
// The logger is automatically configured during router initialization (`NewWithConfig`)
// based on Xylium's operating mode (Debug, Test, Release) and any `LoggerConfig`
// provided in `ServerConfig`, if a `DefaultLogger` is being used. If a custom
// `Logger` implementation is supplied via `ServerConfig.Logger`, that instance
// is returned directly and is responsible for its own configuration.
//
// This method is guaranteed to return a non-nil `Logger`.
func (r *Router) Logger() Logger {
	// r.serverConfig.Logger is guaranteed to be non-nil by NewWithConfig's initialization logic.
	return r.serverConfig.Logger
}

// New creates a new `Router` instance with default server configuration (`DefaultServerConfig()`).
// The router's logger will be automatically configured based on the current global Xylium
// operating mode (which can be influenced by the `XYLIUM_MODE` environment variable
// or `xylium.SetMode()`).
//
// This is a convenient way to get a Xylium application up and running quickly
// with sensible defaults. For more control over server settings, use `NewWithConfig`.
func New() *Router {
	return NewWithConfig(DefaultServerConfig())
}

// NewWithConfig creates a new `Router` instance with the provided `ServerConfig`.
// This function is the primary way to initialize a Xylium application with custom settings.
//
// It performs several crucial initialization steps:
//  1. Updates and determines the effective Xylium operating mode (`DebugMode`, `TestMode`, `ReleaseMode`)
//     by considering `xylium.SetMode()` calls and the `XYLIUM_MODE` environment variable.
//  2. Initializes and configures the router's logger (`r.serverConfig.Logger`):
//     - If `config.Logger` is nil, a `DefaultLogger` is created. Its settings (level,
//     formatter, color, caller info, output) are determined by a combination of the
//     effective operating mode and any overrides provided in `config.LoggerConfig`.
//     The precedence for `DefaultLogger` settings is generally:
//     `config.LoggerConfig` (if field explicitly set) > mode-based defaults > `DefaultLoggerConfig()` base defaults.
//     - If `config.Logger` is already a non-nil `xylium.Logger` instance, it is used directly,
//     and `config.LoggerConfig` is ignored. The custom logger is responsible for its own configuration.
//  3. Sets default handlers for `NotFoundHandler`, `MethodNotAllowedHandler`, `PanicHandler`,
//     and `GlobalErrorHandler`. These can be overridden by the user after router creation.
//  4. Initializes internal stores for application-level data (`appStore`) and closable resources.
//
// The returned `Router` instance is ready for route registration and server startup.
func NewWithConfig(config ServerConfig) *Router {
	// Ensure the global Xylium mode is up-to-date, considering .env files loaded
	// after package init but before router creation.
	updateGlobalModeFromEnvOnRouterInit()
	effectiveMode := Mode() // Get the final effective mode for this router instance.

	// --- Logger Initialization and Configuration ---
	// This block ensures r.serverConfig.Logger is always non-nil.
	if config.Logger == nil {
		// Start with Xylium's base default logger configuration.
		baseLogCfg := DefaultLoggerConfig()

		// If the user provided a specific LoggerConfig, merge its settings into baseLogCfg.
		// Fields not set in userProvidedLogCfg will retain values from baseLogCfg.
		if config.LoggerConfig != nil {
			userProvidedLogCfg := *config.LoggerConfig
			if userProvidedLogCfg.Output != nil {
				baseLogCfg.Output = userProvidedLogCfg.Output
			}
			if userProvidedLogCfg.Formatter != "" { // Ensure formatter is a valid FormatterType.
				baseLogCfg.Formatter = userProvidedLogCfg.Formatter
			}
			// Level, ShowCaller, UseColor will be handled with precedence below.
		}

		// Now, apply mode-based defaults to the (potentially user-modified) baseLogCfg.
		// This creates the initial `finalLogCfg`.
		finalLogCfg := baseLogCfg
		switch effectiveMode {
		case DebugMode:
			finalLogCfg.Level = LevelDebug
			finalLogCfg.ShowCaller = true
			finalLogCfg.UseColor = true // Attempt to use color in DebugMode.
		case TestMode:
			finalLogCfg.Level = LevelDebug
			finalLogCfg.ShowCaller = true
			finalLogCfg.UseColor = false // No color for automated tests.
		case ReleaseMode:
			finalLogCfg.Level = LevelInfo
			finalLogCfg.ShowCaller = false
			finalLogCfg.UseColor = false
		}

		// If LoggerConfig was provided by the user, let its explicitly set fields
		// override the mode-based defaults or the initial baseLogCfg values.
		// We check if the user's config field differs from the *original* DefaultLoggerConfig's
		// field value to determine if it was an intentional override by the user.
		if config.LoggerConfig != nil {
			userProvidedLogCfg := *config.LoggerConfig
			originalDefaults := DefaultLoggerConfig() // For comparison against user's intent.

			if userProvidedLogCfg.Level != originalDefaults.Level {
				finalLogCfg.Level = userProvidedLogCfg.Level
			}
			if userProvidedLogCfg.ShowCaller != originalDefaults.ShowCaller {
				finalLogCfg.ShowCaller = userProvidedLogCfg.ShowCaller
			}
			if userProvidedLogCfg.UseColor != originalDefaults.UseColor {
				finalLogCfg.UseColor = userProvidedLogCfg.UseColor
			}
			// Formatter and Output were already merged from userProvidedLogCfg into baseLogCfg,
			// which then became finalLogCfg. So, they are correctly prioritized.
		}

		// Ensure Output is not nil; default to os.Stdout if it somehow ended up nil.
		if finalLogCfg.Output == nil {
			finalLogCfg.Output = os.Stdout
		}

		// Create the DefaultLogger with the finalized configuration.
		config.Logger = NewDefaultLoggerWithConfig(finalLogCfg)
		// Log that DefaultLogger is being used and how it was configured.
		config.Logger.Debugf("Router using DefaultLogger, configured. EffectiveMode: %s, FinalLoggerConfig: %+v", effectiveMode, finalLogCfg)
	} else {
		// A custom logger was provided. Log this and skip automatic configuration.
		config.Logger.Warnf(
			"A custom logger (type: %T) was provided via ServerConfig.Logger. Automatic Xylium mode-based and LoggerConfig-based logger configuration is skipped for this logger.",
			config.Logger,
		)
	}
	// At this point, config.Logger is guaranteed to be non-nil.

	// Initialize the Router instance with the (potentially modified) config.
	routerInstance := &Router{
		tree:                    NewTree(),                    // Initialize the radix tree for routing.
		globalMiddleware:        make([]Middleware, 0),        // Initialize slice for global middleware.
		serverConfig:            config,                       // Store the final server configuration.
		instanceMode:            effectiveMode,                // Store the determined operating mode.
		appStore:                make(map[string]interface{}), // Initialize the application-level store.
		closers:                 make([]io.Closer, 0),         // Initialize slice for closable resources.
		internalRateLimitStores: make([]LimiterStore, 0),      // Initialize slice for internal stores.
	}

	// Set default framework handlers. Users can override these after router creation.
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Log router initialization details. `modeSource` is a global variable from mode.go.
	routerInstance.Logger().Infof("Xylium Router initialized (Adopting Mode: %s, Determined By: %s)", routerInstance.instanceMode, modeSource)
	return routerInstance
}

// CurrentMode returns the operating mode (e.g., "debug", "release", "test")
// of this specific `Router` instance. This mode is determined during router
// initialization and influences various framework behaviors.
func (r *Router) CurrentMode() string {
	return r.instanceMode
}

// Use adds one or more `Middleware` functions to the router's global middleware chain.
// Global middleware are executed for every request handled by this router, in the order
// they are added, before any group-specific or route-specific middleware.
//
// Example:
//
//	app.Use(xylium.RequestID()) // Applied first
//	app.Use(myCustomLoggerMiddleware, myAuthMiddleware) // Applied subsequently
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// AppSet stores a key-value pair in the application-level store (`r.appStore`).
// This store is managed by the `Router` instance and is shared across all requests
// handled by it. It's suitable for storing global resources like database connection
// pools, service clients, or application-wide configurations.
//
// If the provided `value` implements the `io.Closer` interface, it is automatically
// registered with the router (via `r.RegisterCloser`) to be closed during the
// application's graceful shutdown sequence. This helps manage the lifecycle of
// shared resources.
//
// This method is thread-safe.
func (r *Router) AppSet(key string, value interface{}) {
	r.appStoreMux.Lock()
	r.appStore[key] = value
	r.appStoreMux.Unlock()

	// If the value implements io.Closer, register it for graceful shutdown.
	if closer, ok := value.(io.Closer); ok {
		r.RegisterCloser(closer)
	}
}

// AppGet retrieves a value from the application-level store (`r.appStore`) by its key.
//
// Parameters:
//   - `key` (string): The key of the value to retrieve.
//
// Returns:
//   - `interface{}`: The value associated with the key, if found.
//   - `bool`: True if the key exists in the application store, false otherwise.
//
// This method is thread-safe.
func (r *Router) AppGet(key string) (interface{}, bool) {
	r.appStoreMux.RLock()
	defer r.appStoreMux.RUnlock()
	if r.appStore == nil { // Defensive check, though appStore is initialized in NewWithConfig.
		return nil, false
	}
	val, ok := r.appStore[key]
	return val, ok
}

// RegisterCloser explicitly registers an instance that implements `io.Closer`
// to be closed during the router's graceful shutdown sequence (`closeApplicationResources`).
// This is useful for managing the lifecycle of resources that might not be stored
// in the `appStore` (via `AppSet`) but still require cleanup when the application terminates
// (e.g., a global logger file writer, a custom background worker pool).
//
// If the provided `closer` is nil, this method does nothing.
// This method is thread-safe.
func (r *Router) RegisterCloser(closer io.Closer) {
	if closer == nil {
		return
	}
	r.closersMux.Lock()
	defer r.closersMux.Unlock()
	// Optional: Could add a check for duplicates if that's a concern,
	// though closing multiple times might be benign for some Closer implementations.
	r.closers = append(r.closers, closer)
	r.Logger().Debugf("Resource (type %T) explicitly registered for graceful shutdown.", closer)
}

// addInternalStore registers a `LimiterStore` instance (typically created internally
// by Xylium, e.g., the default `InMemoryStore` for `RateLimiter` middleware) with the router.
// This method is unexported and intended for internal framework use.
// It ensures these internally managed stores are properly closed during graceful shutdown.
//
// This method is thread-safe and checks for duplicate registrations of the same store instance.
func (r *Router) addInternalStore(store LimiterStore) {
	if store == nil {
		return
	}
	r.internalRateLimitStoresMux.Lock()
	defer r.internalRateLimitStoresMux.Unlock()

	// Check for duplicates to avoid multiple registrations of the same store instance.
	for _, existingStore := range r.internalRateLimitStores {
		if existingStore == store { // Compare by pointer equality for instances.
			r.Logger().Debugf("Internal LimiterStore (type %T, instance %p) already registered; registration skipped.", store, store)
			return
		}
	}
	r.internalRateLimitStores = append(r.internalRateLimitStores, store)
	r.Logger().Debugf("Internally created LimiterStore (type %T, instance %p) registered for graceful shutdown.", store, store)
}

// addRoute is an internal helper method to register a new route in the router's radix tree.
// It normalizes the provided path, associates the `handler` and route-specific `middlewares`,
// and adds it to the tree for the specified HTTP `method`.
//
// Parameters:
//   - `method` (string): The HTTP method (e.g., "GET", "POST"), which will be uppercased.
//   - `path` (string): The URL path pattern. Must begin with "/". Trailing slashes
//     (except for the root path "/") are typically removed by `Tree.Add`.
//   - `handler` (HandlerFunc): The main request handler for this route.
//   - `middlewares` (...Middleware): Optional route-specific middleware.
//
// Panics if `path` does not start with "/" or if `handler` is nil.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/" // Default to root path if an empty path string is provided.
	}
	if path[0] != '/' {
		// Path patterns must be absolute (start with '/').
		panic(fmt.Sprintf("xylium: path must begin with '/' (e.g., \"/users\" or \"/\"), got \"%s\"", path))
	}
	// `r.tree.Add` will handle further normalization (like trailing slashes) and
	// will panic if the handler is nil or if the route is a duplicate.
	r.tree.Add(method, path, handler, middlewares...)
}

// GET registers a new route for GET requests to the given `path`.
// The `handler` will be executed when a GET request matches this path.
// Optional route-specific `middlewares` can also be provided.
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodGet, path, handler, middlewares...)
}

// POST registers a new route for POST requests to the given `path`.
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPost, path, handler, middlewares...)
}

// PUT registers a new route for PUT requests to the given `path`.
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPut, path, handler, middlewares...)
}

// DELETE registers a new route for DELETE requests to the given `path`.
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodDelete, path, handler, middlewares...)
}

// PATCH registers a new route for PATCH requests to the given `path`.
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPatch, path, handler, middlewares...)
}

// HEAD registers a new route for HEAD requests to the given `path`.
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodHead, path, handler, middlewares...)
}

// OPTIONS registers a new route for OPTIONS requests to the given `path`.
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodOptions, path, handler, middlewares...)
}

// Handler is the core request handler function that Xylium provides to the
// underlying `fasthttp.Server`. It is invoked by `fasthttp` for every incoming request.
//
// Its responsibilities include:
//  1. Acquiring a `xylium.Context` from a pool for the current `fasthttp.RequestCtx`.
//  2. Associating this router instance with the `xylium.Context`.
//  3. Ensuring the `xylium.Context` is released back to the pool after request processing.
//  4. Implementing panic recovery:
//     - If a panic occurs in any handler or middleware, it recovers the panic.
//     - Logs the panic details (including stack trace).
//     - Invokes the router's configured `PanicHandler` (or `defaultPanicHandler`).
//  5. Finding the appropriate route in the radix tree based on the request method and path.
//  6. Constructing the full middleware chain (global, group-level, route-specific).
//  7. Executing the handler chain via `c.Next()`.
//  8. Handling errors returned from the handler chain:
//     - If an error is returned, it is passed to the router's `GlobalErrorHandler`
//     (or `defaultGlobalErrorHandler`) for centralized processing and response generation.
//  9. Handling special cases:
//     - If no route matches the path, `NotFoundHandler` is invoked.
//     - If a path matches but not the HTTP method, `MethodNotAllowedHandler` is invoked
//     (after setting the "Allow" header).
//  10. Ensuring a response is sent or logging a warning if a handler completes
//     without committing a response (in DebugMode, for non-HEAD requests without No Content status).
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) {
	// Acquire a Xylium Context from the pool and initialize it.
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r) // Associate this router with the context.
	// Defer releasing the context back to the pool ensures it happens even on panic.
	defer releaseCtx(c)

	var errHandler error              // To store any error from the handler chain or panic handler.
	requestScopedLogger := c.Logger() // Get the request-scoped logger early.

	// Centralized panic and error handling for the entire request lifecycle.
	defer func() {
		if rec := recover(); rec != nil {
			// A panic occurred. Log it with stack trace.
			requestScopedLogger.Errorf("PANIC RECOVERED: %v\nStack Trace:\n%s", rec, string(debug.Stack()))
			// If a PanicHandler is configured, invoke it.
			if r.PanicHandler != nil {
				// Store panic info in context for the PanicHandler to access.
				c.Set(ContextKeyPanicInfo, rec) // Use defined constant for context key.
				errHandler = r.PanicHandler(c)  // PanicHandler might return an error itself.
			} else {
				// This branch should ideally not be reached if defaultPanicHandler is always set.
				// Fallback to a generic HTTPError if PanicHandler is somehow nil.
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic.").WithInternal(fmt.Errorf("panic: %v", rec)) //nolint:goerr113
			}
		}

		// After panic recovery (if any) and normal handler execution, process any `errHandler`.
		if errHandler != nil {
			// If a response hasn't already been committed by a handler/middleware,
			// let the GlobalErrorHandler process `errHandler` and send a response.
			if !c.ResponseCommitted() {
				if r.GlobalErrorHandler != nil {
					// Store the error cause in context for GlobalErrorHandler.
					c.Set(ContextKeyErrorCause, errHandler) // Use defined constant.
					// Invoke the GlobalErrorHandler.
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						// Critical: The GlobalErrorHandler itself failed.
						// Send a minimal, hardcoded error response directly.
						requestScopedLogger.Errorf(
							"CRITICAL: Error occurred within GlobalErrorHandler: %v (while handling original error: %v). Request: %s %s",
							globalErrHandlingErr, errHandler, c.Method(), c.Path(),
						)
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						// Use a more specific message than the generic fasthttp one.
						c.Ctx.Response.SetBodyString("Internal Server Error - Critical failure in global error handler.")
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else {
					// This branch should not be reached if defaultGlobalErrorHandler is always set.
					// Fallback if GlobalErrorHandler is somehow nil.
					requestScopedLogger.Errorf(
						"CRITICAL: GlobalErrorHandler is nil. Error: %v for request %s %s. Sending raw 500 response.",
						errHandler, c.Method(), c.Path(),
					)
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error - No global error handler configured.")
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else {
				// Response was already committed, but an error was generated afterwards
				// (e.g., by a middleware after `next(c)` returned, or PanicHandler for a late panic).
				// Log this situation, as the error cannot be sent to the client.
				requestScopedLogger.Warnf(
					"Response already committed for %s %s, but an error was generated post-commitment: %v. This error cannot be sent to the client.",
					c.Method(), c.Path(), errHandler,
				)
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// No error occurred, but the response was not committed by any handler.
			// This might be intentional for HEAD requests or if `c.NoContent()` was used for
			// statuses like 204 or 304.
			statusCode := c.Ctx.Response.StatusCode()
			isNoContentStatus := (statusCode == StatusNoContent || statusCode == StatusNotModified)

			// Check if the response body is effectively empty.
			// fasthttp sets ContentLength to -1 if not specified and body is empty before actual send.
			// A ContentLength of 0 also means empty body.
			bodyLen := len(c.Ctx.Response.Body())
			contentLenHeader := c.Ctx.Response.Header.ContentLength()
			isResponseEffectivelyEmpty := (bodyLen == 0 && (contentLenHeader == 0 || contentLenHeader == -1))

			// Log a debug message if a non-error, non-HEAD, non-"No Content" status request
			// completes without writing a response body. This can help catch unintentional omissions.
			if !isNoContentStatus && isResponseEffectivelyEmpty && statusCode < StatusBadRequest {
				if r.CurrentMode() == DebugMode {
					requestScopedLogger.Debugf(
						"Handler for %s %s (Status: %d) completed without writing a response body or calling c.NoContent(). "+
							"Ensure handlers explicitly send a response or use c.NoContent() if no body is intended.",
						c.Method(), c.Path(), statusCode,
					)
				}
				// In this case, fasthttp might send a default empty response with the set status code.
				// Xylium does not automatically send a "default" body here.
			}
		}
	}() // End of deferred error/panic handling logic.

	// --- Main Request Processing Logic ---
	method := c.Method() // Get request method.
	path := c.Path()     // Get request path.

	// Find the route in the radix tree.
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil {
		// Route found for the method and path.
		c.Params = params // Set extracted path parameters on the context.

		// Construct the full handler chain: global -> group (if any, handled by tree) -> route-specific -> main handler.
		// `routeMiddleware` from tree.Find already includes group middleware in the correct order.
		finalChain := nodeHandler // Start with the main route handler.
		// Apply route-specific middleware (in reverse order to build the chain).
		for i := len(routeMiddleware) - 1; i >= 0; i-- {
			finalChain = routeMiddleware[i](finalChain)
		}
		// Apply global middleware (also in reverse order).
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- {
			finalChain = r.globalMiddleware[i](finalChain)
		}

		c.handlers = []HandlerFunc{finalChain} // Set the fully constructed chain.
		c.index = -1                           // Reset handler index for c.Next().
		errHandler = c.Next()                  // Execute the handler chain.
	} else {
		// No direct handler found for the method and path.
		if len(allowedMethods) > 0 {
			// Path matched, but not for this HTTP method (405 Method Not Allowed).
			c.Params = params // Path parameters might still be relevant for the 405 handler.
			if r.MethodNotAllowedHandler != nil {
				// Set "Allow" header with the list of methods that *are* allowed for this path.
				c.SetHeader("Allow", strings.Join(allowedMethods, ", "))
				errHandler = r.MethodNotAllowedHandler(c)
			} else { // Fallback if MethodNotAllowedHandler is somehow nil.
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else {
			// No route matched the path at all (404 Not Found).
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else { // Fallback if NotFoundHandler is somehow nil.
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
	// The deferred function will handle `errHandler`.
}

// ServeFiles serves static files from a given filesystem root directory (`fileSystemRoot`)
// under a specified URL path prefix (`urlPathPrefix`).
//
// This method configures a route (typically `GET urlPathPrefix/*filepath`) to handle
// requests for static assets. It uses `fasthttp.FS` for efficient file serving,
// which includes support for:
//   - Serving `index.html` for directory requests (if `fasthttp.FS.IndexNames` is configured,
//     Xylium uses `{"index.html"}` by default).
//   - Setting appropriate `Content-Type` headers based on file extensions.
//   - Handling HTTP byte range requests (`Accept-Ranges` header).
//   - Gzip compression for eligible file types (if `fasthttp.FS.Compress` is enabled,
//     which it is by default in Xylium's usage).
//
// If a requested file is not found within the `fileSystemRoot`, Xylium's custom
// `PathNotFound` handler (configured for `fasthttp.FS`) will respond with a
// JSON `404 Not Found` error, maintaining consistency with API error responses.
//
// Parameters:
//   - `urlPathPrefix` (string): The URL path prefix under which files will be served.
//     Example: If "/static", requests like "/static/css/style.css" will be handled.
//     To serve from the root URL path (e.g., for SPAs), use "/" or "".
//     Ensure this prefix does not conflict with other API routes.
//   - `fileSystemRoot` (string): The absolute or relative path to the directory on the
//     server's filesystem that contains the static files to be served.
//
// Panics:
//   - If `urlPathPrefix` contains route parameters (segments starting with ':' or '*').
//   - If `fileSystemRoot` is an invalid path or cannot be resolved to an absolute path.
//
// A warning is logged if `fileSystemRoot` does not exist at the time of configuration,
// though the route will still be registered.
func (r *Router) ServeFiles(urlPathPrefix string, fileSystemRoot string) {
	if strings.Contains(urlPathPrefix, ":") || strings.Contains(urlPathPrefix, "*") {
		panic("xylium: urlPathPrefix for ServeFiles cannot contain route parameters ':' or '*'")
	}

	// Clean and resolve the filesystem root path.
	cleanedFileSystemRoot, err := filepath.Abs(filepath.Clean(fileSystemRoot))
	if err != nil {
		panic(fmt.Sprintf("xylium: ServeFiles could not determine absolute path for fileSystemRoot '%s': %v", fileSystemRoot, err))
	}
	// Check if the directory exists and log a warning if not.
	if _, statErr := os.Stat(cleanedFileSystemRoot); os.IsNotExist(statErr) {
		r.Logger().Warnf("ServeFiles: The specified fileSystemRoot directory '%s' (resolved to '%s') does not exist. Static file serving for URL prefix '%s' might not work as expected until the directory is created.",
			fileSystemRoot, cleanedFileSystemRoot, urlPathPrefix)
	}

	// Normalize the URL path prefix.
	// Ensures it starts with "/" and does not have a trailing "/" unless it's the root.
	normalizedUrlPathPrefix := "/" + strings.Trim(urlPathPrefix, "/")
	if urlPathPrefix == "/" || urlPathPrefix == "" { // Handle serving from root.
		normalizedUrlPathPrefix = "/"
	}

	// Define the catch-all parameter name for the file path segment.
	catchAllParamName := "filepath"
	// Construct the route path pattern for the radix tree.
	routePath := ""
	if normalizedUrlPathPrefix == "/" {
		// Example: GET /*filepath
		routePath = "/*" + catchAllParamName
	} else {
		// Example: GET /static/*filepath
		routePath = normalizedUrlPathPrefix + "/*" + catchAllParamName
	}

	// Get the router's base logger. It's guaranteed to be non-nil.
	routerBaseLogger := r.Logger()

	// Configure fasthttp.FS for serving files.
	fs := &fasthttp.FS{
		Root:               cleanedFileSystemRoot,  // Serve files from this directory.
		IndexNames:         []string{"index.html"}, // Serve "index.html" for directory requests.
		GenerateIndexPages: false,                  // Do not auto-generate directory listings.
		AcceptByteRange:    true,                   // Support byte range requests.
		Compress:           true,                   // Enable Gzip compression for eligible files.
		PathNotFound: func(originalFasthttpCtx *fasthttp.RequestCtx) {
			// Custom handler for when a file is not found by fasthttp.FS.
			// This provides a Xylium-style JSON error response.
			errorMsg := M{"error": "The requested static asset was not found."}
			// Get the path fasthttp attempted to serve, for logging.
			assetPath := string(originalFasthttpCtx.Path()) // Path relative to FS.Root.

			// Use a logger derived from the router's base logger for this callback,
			// as it doesn't have a full Xylium Context.
			fsLogger := routerBaseLogger // routerBaseLogger is already non-nil.
			fsLogger.Warnf(
				"ServeFiles: Static asset not found by fasthttp.FS. Request URI: %s, FS Attempted Path (relative to root): %s, FS Root: %s",
				string(originalFasthttpCtx.RequestURI()), assetPath, cleanedFileSystemRoot,
			)

			// Send a 404 Not Found response with a JSON body.
			originalFasthttpCtx.SetStatusCode(StatusNotFound)
			originalFasthttpCtx.SetContentType("application/json; charset=utf-8")
			if err := json.NewEncoder(originalFasthttpCtx.Response.BodyWriter()).Encode(errorMsg); err != nil {
				// Critical error: if JSON encoding itself fails. Log to primary logger.
				fsLogger.Errorf(
					"ServeFiles: CRITICAL - Error encoding JSON for PathNotFound response (asset path: %s): %v.",
					assetPath, err,
				)
				// Fallback to plain text if JSON fails.
				originalFasthttpCtx.SetBodyString(`{"error":"Static asset not found, and error occurred generating JSON response."}`)
			}
		},
	}
	// Get the fasthttp request handler from the configured fasthttp.FS.
	fileServerHandler := fs.NewRequestHandler()

	// Register a GET route with the catch-all pattern to handle static file requests.
	r.GET(routePath, func(c *Context) error {
		// Extract the filepath part from the catch-all parameter.
		requestedFileSubPath := c.Param(catchAllParamName)

		// fasthttp.FS expects the RequestURI to be the path relative to its Root.
		// We need to adjust the context's RequestURI for fasthttp.FS to work correctly,
		// then restore it afterwards so Xylium's logging/other features see the original URI.
		// Path must start with '/' for fasthttp.FS. Clean it to prevent traversal issues.
		pathForFasthttpFS := "/" + filepath.Clean("/"+requestedFileSubPath)

		originalURI := c.Ctx.Request.RequestURI()      // Save original URI.
		c.Ctx.Request.SetRequestURI(pathForFasthttpFS) // Set URI for fasthttp.FS.

		fileServerHandler(c.Ctx) // Let fasthttp.FS handle the request.

		c.Ctx.Request.SetRequestURIBytes(originalURI) // Restore original URI.
		return nil                                    // Indicate request handled; fasthttp.FS sent the response.
	})

	r.Logger().Debugf("Static file serving configured for URL prefix '%s' from filesystem root '%s' via route '%s'",
		normalizedUrlPathPrefix, cleanedFileSystemRoot, routePath)
}

// RouteGroup provides a way to organize routes under a common URL path prefix
// and/or apply a shared set of `Middleware` to all routes within that group.
// Groups can be nested to create more complex routing structures.
type RouteGroup struct {
	router     *Router      // Reference to the parent Router instance.
	prefix     string       // The URL path prefix for this group.
	middleware []Middleware // Middleware specific to this group.
}

// Group creates a new `RouteGroup` with the given `urlPrefix`.
// Optional `middlewares` can be provided, which will be applied to all routes
// defined within this group and any of its sub-groups. These group middlewares
// are executed after any global middlewares (from `router.Use()`) and before
// any route-specific middlewares.
//
// The `urlPrefix` is normalized (e.g., leading/trailing slashes are handled).
//
// Example:
//
//	apiV1 := app.Group("/api/v1", apiAuthMiddleware)
//	apiV1.GET("/users", listUsersHandler) // Path: /api/v1/users, runs apiAuthMiddleware
func (r *Router) Group(urlPrefix string, middlewares ...Middleware) *RouteGroup {
	// Normalize the prefix: ensure it starts with "/" and doesn't have a trailing "/"
	// unless it's the root group "/".
	normalizedPrefix := "/" + strings.Trim(urlPrefix, "/")
	if urlPrefix == "/" || urlPrefix == "" {
		normalizedPrefix = "/" // Root group.
	}

	// Copy middlewares to a new slice for the group to avoid modification issues.
	groupMiddleware := make([]Middleware, len(middlewares))
	copy(groupMiddleware, middlewares)

	return &RouteGroup{
		router:     r,                // Link back to the main router.
		prefix:     normalizedPrefix, // Store the normalized prefix.
		middleware: groupMiddleware,  // Store the group-specific middleware.
	}
}

// Use adds one or more `Middleware` functions to this `RouteGroup`.
// These middlewares will be applied to all routes registered directly on this group
// or on any of its sub-groups, after any middleware from parent groups or global
// middleware, and before any route-specific middleware.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute is an internal helper for `RouteGroup` to register a route.
// It constructs the full path by prepending the group's prefix to the `relativePath`
// and combines the group's middleware with any route-specific `middlewares`
// before adding the route to the main router's tree.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	// Normalize the relative path for the route within the group.
	normalizedRelativePath := "/" + strings.Trim(relativePath, "/")
	if relativePath == "/" || relativePath == "" { // Handler for the group's root.
		normalizedRelativePath = "/"
	}

	// Construct the full absolute path for the route.
	var fullPath string
	if rg.prefix == "/" { // If the group is at the root.
		if normalizedRelativePath == "/" {
			fullPath = "/" // e.g., group "/".GET("/") -> path "/"
		} else {
			fullPath = normalizedRelativePath // e.g., group "/".GET("/users") -> path "/users"
		}
	} else { // If the group has a non-root prefix.
		if normalizedRelativePath == "/" {
			fullPath = rg.prefix // e.g., group "/api".GET("/") -> path "/api"
		} else {
			// e.g., group "/api".GET("/users") -> path "/api/users"
			// Ensure no double slash if relativePath already starts with one (handled by trim).
			fullPath = rg.prefix + normalizedRelativePath
		}
	}

	// Combine group middleware with route-specific middleware.
	// Group middleware runs first, then route-specific.
	allApplicableMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allApplicableMiddleware = append(allApplicableMiddleware, rg.middleware...)
	allApplicableMiddleware = append(allApplicableMiddleware, middlewares...)

	// Add the route to the main router's tree with the full path and combined middleware.
	rg.router.addRoute(method, fullPath, handler, allApplicableMiddleware...)
}

// GET registers a new GET request handler within this `RouteGroup`.
// The `relativePath` is appended to the group's prefix to form the full route path.
// Group middleware and any provided route-specific `middlewares` are applied.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodGet, relativePath, handler, middlewares...)
}

// POST registers a new POST request handler within this `RouteGroup`.
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPost, relativePath, handler, middlewares...)
}

// PUT registers a new PUT request handler within this `RouteGroup`.
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPut, relativePath, handler, middlewares...)
}

// DELETE registers a new DELETE request handler within this `RouteGroup`.
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodDelete, relativePath, handler, middlewares...)
}

// PATCH registers a new PATCH request handler within this `RouteGroup`.
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPatch, relativePath, handler, middlewares...)
}

// HEAD registers a new HEAD request handler within this `RouteGroup`.
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodHead, relativePath, handler, middlewares...)
}

// OPTIONS registers a new OPTIONS request handler within this `RouteGroup`.
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodOptions, relativePath, handler, middlewares...)
}

// Group creates a new sub-`RouteGroup` nested within the current `RouteGroup`.
// The `relativePathPrefix` is appended to the current group's prefix to form the
// prefix for the new sub-group.
// All middleware from the current (parent) group are inherited by the sub-group
// and will be executed before any `middlewares` provided directly to this sub-group
// or its routes.
//
// Example:
//
//	adminGroup := app.Group("/admin", adminAuthMiddleware)
//	userManagement := adminGroup.Group("/users", userSpecificMiddleware)
//	// userManagement now has prefix "/admin/users" and runs adminAuthMiddleware then userSpecificMiddleware.
func (rg *RouteGroup) Group(relativePathPrefix string, middlewares ...Middleware) *RouteGroup {
	// Normalize the relative prefix for the new sub-group.
	normalizedRelativePrefix := "/" + strings.Trim(relativePathPrefix, "/")
	if relativePathPrefix == "/" || relativePathPrefix == "" {
		normalizedRelativePrefix = "/"
	}

	// Construct the full absolute prefix for the new sub-group.
	var newFullPrefix string
	if rg.prefix == "/" { // If current group is root.
		if normalizedRelativePrefix == "/" {
			newFullPrefix = "/"
		} else {
			newFullPrefix = normalizedRelativePrefix
		}
	} else { // Current group has a non-root prefix.
		if normalizedRelativePrefix == "/" {
			newFullPrefix = rg.prefix
		} else {
			newFullPrefix = rg.prefix + normalizedRelativePrefix
		}
	}

	// Combine middleware: parent group's middleware first, then new sub-group's middleware.
	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...)
	combinedMiddleware = append(combinedMiddleware, middlewares...)

	return &RouteGroup{
		router:     rg.router,          // Link back to the main router.
		prefix:     newFullPrefix,      // Set the full prefix for the new sub-group.
		middleware: combinedMiddleware, // Set the combined middleware.
	}
}
