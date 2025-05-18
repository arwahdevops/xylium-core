// src/xylium/router.go
package xylium

import (
	"encoding/json" // For ServeFiles PathNotFound JSON response
	"fmt"           // For error formatting
	"io"            // For HTMLRenderer interface and isTerminalWriter check
	"path/filepath" // For path cleaning and manipulation in ServeFiles
	"runtime/debug" // For capturing stack traces on panic
	"strings"       // For string manipulation (path normalization, joining)

	"github.com/valyala/fasthttp" // The underlying HTTP engine
)

// HTMLRenderer defines the interface for HTML template rendering.
type HTMLRenderer interface {
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router is the main request router for the Xylium framework.
type Router struct {
	tree             *Tree        // Radix tree for efficient route matching.
	globalMiddleware []Middleware // Middleware applied to all routes.

	PanicHandler            HandlerFunc // Handler for recovered panics.
	NotFoundHandler         HandlerFunc // Handler for 404 Not Found.
	MethodNotAllowedHandler HandlerFunc // Handler for 405 Method Not Allowed.
	GlobalErrorHandler      HandlerFunc // Central handler for errors from handlers/middleware.

	serverConfig ServerConfig // Configuration for the underlying fasthttp.Server.
	HTMLRenderer HTMLRenderer // Optional HTML template renderer.
	instanceMode string       // Operating mode (e.g., "debug", "release") of this router instance.
}

// Logger returns the configured logger for this router instance.
// This logger is configured (level, color, etc.) based on the Xylium operating mode
// during router initialization if it's a DefaultLogger.
func (r *Router) Logger() Logger {
	// serverConfig.Logger is guaranteed to be non-nil by NewWithConfig.
	return r.serverConfig.Logger
}

// New creates a new Router instance with default server configuration.
// The logger will be automatically configured based on the Xylium operating mode.
func New() *Router {
	return NewWithConfig(DefaultServerConfig())
}

// NewWithConfig creates a new Router instance with the provided ServerConfig.
// It automatically configures the DefaultLogger based on Xylium's operating mode.
func NewWithConfig(config ServerConfig) *Router {
	// Determine the effective Xylium operating mode.
	// This considers environment variables and explicit SetMode calls.
	updateGlobalModeFromEnvOnRouterInit()
	effectiveMode := Mode()

	// Ensure a logger is present in the configuration.
	if config.Logger == nil {
		// If no logger was provided in the config, create a new DefaultLogger.
		config.Logger = NewDefaultLogger()
		// Log a warning using this newly created logger's default settings.
		// This message will use DefaultLogger's initial level (e.g., LevelInfo).
		config.Logger.Warnf("ServerConfig was provided without a Logger. Initializing with a new DefaultLogger.")
	}

	// --- Automatic Logger Configuration ---
	// If the logger is Xylium's DefaultLogger, configure it based on the effective operating mode.
	if defaultLog, ok := config.Logger.(*DefaultLogger); ok {
		// Note: Methods like SetLevel, EnableCaller, EnableColor on DefaultLogger are thread-safe.
		switch effectiveMode {
		case DebugMode:
			defaultLog.SetLevel(LevelDebug)
			defaultLog.EnableCaller(true)  // Show file:line in debug.
			defaultLog.EnableColor(true)  // EnableColor will internally check for TTY.
		case TestMode:
			defaultLog.SetLevel(LevelDebug) // Tests often benefit from debug-level verbosity.
			defaultLog.EnableCaller(true)  // Caller info can be useful in test logs.
			defaultLog.EnableColor(false) // Colors usually not needed for test logs.
		case ReleaseMode:
			defaultLog.SetLevel(LevelInfo)  // Sensible default for production.
			defaultLog.EnableCaller(false) // Avoid overhead of caller info in production.
			defaultLog.EnableColor(false) // No colors in production logs typically.
			// Note: We do NOT automatically change the formatter (e.g., to JSON).
			// Users should explicitly configure the formatter if they need JSON for production.
			// The default TextFormatter is fine for many scenarios.
		}
		// Optional: Log that auto-configuration was applied (can be noisy).
		// defaultLog.Debugf("DefaultLogger automatically configured for Xylium '%s' mode.", effectiveMode)
	} else {
		// If a custom logger implementation is provided, skip automatic configuration.
		// Log a warning that auto-configuration was skipped. This uses the custom logger itself.
		config.Logger.Warnf(
			"A custom logger (type: %T) was provided. Automatic Xylium mode-based logger configuration (level, caller, color) will be skipped.",
			config.Logger,
		)
	}

	// Initialize the router instance.
	routerInstance := &Router{
		tree:             NewTree(),                // Initialize the routing tree.
		globalMiddleware: make([]Middleware, 0),    // Initialize an empty slice for global middleware.
		serverConfig:     config,                   // Use the (potentially logger-updated) config.
		instanceMode:     effectiveMode,            // Set this router's operating mode.
	}

	// Set default handlers for common framework events.
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Log router initialization using the now fully configured logger.
	// The modeSource global variable from mode.go indicates how the mode was determined.
	routerInstance.Logger().Infof("Xylium Router initialized (Adopting Mode: %s, Determined By: %s)", routerInstance.instanceMode, modeSource)

	return routerInstance
}

// CurrentMode returns the operating mode (e.g., "debug", "release", "test")
// of this specific router instance. This mode is fixed at the time of router creation.
func (r *Router) CurrentMode() string {
	return r.instanceMode
}

// Use adds one or more global middleware functions to the router's middleware chain.
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// addRoute is an internal helper method to register a new route.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		panic("xylium: path must begin with '/' (e.g., \"/users\")")
	}
	r.tree.Add(method, path, handler, middlewares...)
}

// --- HTTP Method Route Registration ---
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware)     { r.addRoute(MethodGet, path, handler, middlewares...) }
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware)    { r.addRoute(MethodPost, path, handler, middlewares...) }
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware)     { r.addRoute(MethodPut, path, handler, middlewares...) }
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware)  { r.addRoute(MethodDelete, path, handler, middlewares...) }
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware)   { r.addRoute(MethodPatch, path, handler, middlewares...) }
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware)    { r.addRoute(MethodHead, path, handler, middlewares...) }
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) { r.addRoute(MethodOptions, path, handler, middlewares...) }

// Handler is the core fasthttp.RequestHandlerFunc for the Xylium router.
// It manages the request lifecycle, including context pooling, routing,
// middleware execution, panic recovery, and error handling.
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) {
	// Acquire a Context from the pool and initialize it.
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r) // Link this router instance to the context.
	defer releaseCtx(c) // Ensure context is released back to the pool.

	var errHandler error // To store errors from the handler chain or panic recovery.

	// Get the request-scoped logger from the context.
	// This logger will include 'request_id' if the RequestID middleware has run.
	requestScopedLogger := c.Logger()

	// Deferred function for panic recovery and global error handling.
	defer func() {
		// 1. Panic Recovery
		if rec := recover(); rec != nil {
			// Log the panic with stack trace using the request-scoped logger.
			requestScopedLogger.Errorf("PANIC: %v\n%s", rec, string(debug.Stack()))
			if r.PanicHandler != nil {
				c.Set("panic_recovery_info", rec) // Make panic info available to PanicHandler.
				errHandler = r.PanicHandler(c)   // PanicHandler will use c.Logger().
			} else {
				// Fallback if PanicHandler is somehow not set.
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic.").WithInternal(fmt.Errorf("panic: %v", rec))
			}
		}

		// 2. Global Error Handling (for errors from handlers or PanicHandler)
		if errHandler != nil {
			if !c.ResponseCommitted() { // Only handle if response hasn't been sent.
				if r.GlobalErrorHandler != nil {
					c.Set("handler_error_cause", errHandler) // Make original error available.
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						// Critical: GlobalErrorHandler itself failed. Log and send a plain 500.
						requestScopedLogger.Errorf("CRITICAL: Error within GlobalErrorHandler: %v (while handling original error: %v)", globalErrHandlingErr, errHandler)
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						c.Ctx.Response.SetBodyString("Internal Server Error")
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else {
					// Fallback if GlobalErrorHandler is not set (highly unlikely).
					requestScopedLogger.Errorf("Error (GlobalErrorHandler is nil): %v for %s %s. Sending fallback 500.", errHandler, c.Method(), c.Path())
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error")
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else {
				// Response already sent, but an error occurred afterwards. Log it.
				requestScopedLogger.Warnf("Response already committed, but an error was generated post-commitment: %v for %s %s", errHandler, c.Method(), c.Path())
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// 3. Sanity Check: Handler chain finished without error but also without sending a response.
			statusCode := c.Ctx.Response.StatusCode()
			bodyLen := len(c.Ctx.Response.Body())
			contentLengthHeader := c.Ctx.Response.Header.ContentLength()
			// A response is considered effectively empty if status is 0 (fasthttp default),
			// or 200 OK with no body and no explicit positive Content-Length.
			// StatusNoContent (204) is a valid empty response.
			isEffectivelyEmptyResponse := (statusCode == 0) || (statusCode == StatusOK && bodyLen == 0 && contentLengthHeader <= 0)

			if isEffectivelyEmptyResponse && statusCode != StatusNoContent {
				// Log this warning only in DebugMode to avoid noise.
				if r.CurrentMode() == DebugMode {
					requestScopedLogger.Debugf(
						"Handler for %s %s completed without sending response body or error (Status: %d, BodyLen: %d, ContentLength: %d). Ensure handlers explicitly send a response.",
						c.Method(), c.Path(), statusCode, bodyLen, contentLengthHeader,
					)
				}
			}
		}
	}() // End of deferred function.

	// --- Main Request Processing Logic ---
	method := c.Method()
	path := c.Path()

	// Find the matching route in the radix tree.
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil { // Route found for the method and path.
		c.Params = params // Store extracted path parameters in the context.

		// Build the complete handler chain: Global -> Group (if any) -> Route -> Actual Handler.
		// Middleware is applied by wrapping, so iteration is in reverse.
		finalChain := nodeHandler
		// Route-specific and group-specific middleware (already combined by tree/group).
		for i := len(routeMiddleware) - 1; i >= 0; i-- {
			finalChain = routeMiddleware[i](finalChain)
		}
		// Global middleware.
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- {
			finalChain = r.globalMiddleware[i](finalChain)
		}

		// Set up context for execution via c.Next().
		c.handlers = []HandlerFunc{finalChain}
		c.index = -1          // Reset handler index for c.Next().
		errHandler = c.Next() // Execute the handler chain.

	} else { // No handler found for this specific method and path.
		if len(allowedMethods) > 0 {
			// Path exists, but not for the requested HTTP method (405 Method Not Allowed).
			c.Params = params // Parameters might be relevant for the 405 handler.
			if r.MethodNotAllowedHandler != nil {
				c.SetHeader("Allow", strings.Join(allowedMethods, ", ")) // Set "Allow" header.
				errHandler = r.MethodNotAllowedHandler(c)
			} else {
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else {
			// Path does not exist in the routing tree at all (404 Not Found).
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else {
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
}

// ServeFiles serves static files from a given filesystem root directory.
func (r *Router) ServeFiles(urlPathPrefix string, fileSystemRoot string) {
	if strings.Contains(urlPathPrefix, ":") || strings.Contains(urlPathPrefix, "*") {
		panic("xylium: urlPathPrefix for ServeFiles cannot contain ':' or '*' (reserved for parameters/wildcards)")
	}
	cleanedFileSystemRoot := filepath.Clean(fileSystemRoot)
	catchAllParamName := "filepath" // Name for the catch-all parameter.

	normalizedUrlPathPrefix := ""
	if urlPathPrefix != "" && urlPathPrefix != "/" {
		normalizedUrlPathPrefix = strings.Trim(urlPathPrefix, "/")
	}

	routePath := ""
	if normalizedUrlPathPrefix == "" { // Serving from application root.
		routePath = "/*" + catchAllParamName
	} else {
		routePath = "/" + normalizedUrlPathPrefix + "/*" + catchAllParamName
	}

	// Get the router's base logger for the fasthttp.FS PathNotFound callback,
	// as this callback operates outside a Xylium request Context.
	routerBaseLogger := r.Logger()

	fs := &fasthttp.FS{
		Root:               cleanedFileSystemRoot,
		IndexNames:         []string{"index.html"}, // Default file for directory requests.
		GenerateIndexPages: false,                  // Disable directory listing (safer).
		AcceptByteRange:    true,                   // Support byte range requests.
		Compress:           true,                   // Allow fasthttp to compress files.
		PathNotFound: func(originalFasthttpCtx *fasthttp.RequestCtx) {
			// Custom handler for files not found by fasthttp.FS.
			errorMsg := M{"error": "The requested static asset was not found."}
			// Manually set response as there's no Xylium Context here.
			originalFasthttpCtx.SetStatusCode(StatusNotFound)
			originalFasthttpCtx.SetContentType("application/json; charset=utf-8")

			if err := json.NewEncoder(originalFasthttpCtx.Response.BodyWriter()).Encode(errorMsg); err != nil {
				// Log critical error if JSON encoding fails for the 404 message.
				routerBaseLogger.Errorf(
					"Xylium ServeFiles: CRITICAL - Error encoding JSON for PathNotFound (URI: %s): %v.",
					string(originalFasthttpCtx.RequestURI()), err,
				)
			}
		},
	}
	fileServerHandler := fs.NewRequestHandler() // Get the fasthttp handler.

	// Register a GET route in Xylium for these static files.
	r.GET(routePath, func(c *Context) error {
		requestedFileSubPath := c.Param(catchAllParamName)

		// Path for fasthttp.FS must be relative to FS.Root and start with '/'.
		pathForFasthttpFS := "/" + requestedFileSubPath
		pathForFasthttpFS = filepath.Clean(pathForFasthttpFS) // Clean up ".." etc.
		if !strings.HasPrefix(pathForFasthttpFS, "/") && pathForFasthttpFS != "." {
			pathForFasthttpFS = "/" + pathForFasthttpFS
		} else if pathForFasthttpFS == "." { // If subpath was empty or just slashes.
			pathForFasthttpFS = "/" // Serve index file from fs.Root.
		}

		// Temporarily set RequestURI for fasthttp.FS handler.
		// originalURI := c.Ctx.RequestURI() // Save original if needed for restoration.
		c.Ctx.Request.SetRequestURI(pathForFasthttpFS)

		fileServerHandler(c.Ctx) // Delegate to fasthttp's file server.

		// c.Ctx.Request.SetRequestURIBytes(originalURI) // Restore original URI if necessary.

		return nil // fasthttp.FS handles the entire response.
	})
}

// --- Route Grouping ---

// RouteGroup allows organizing routes under a common path prefix and/or shared middleware.
type RouteGroup struct {
	router     *Router      // Reference to the main Xylium Router.
	prefix     string       // URL path prefix for this group.
	middleware []Middleware // Middleware specific to this group.
}

// Group creates a new RouteGroup.
func (r *Router) Group(urlPrefix string, middlewares ...Middleware) *RouteGroup {
	normalizedPrefix := ""
	if urlPrefix != "" && urlPrefix != "/" {
		normalizedPrefix = "/" + strings.Trim(urlPrefix, "/")
	}
	// Ensure groupMiddleware is a new slice.
	groupMiddleware := make([]Middleware, len(middlewares))
	copy(groupMiddleware, middlewares)

	return &RouteGroup{
		router:     r,
		prefix:     normalizedPrefix,
		middleware: groupMiddleware,
	}
}

// Use adds middleware to the RouteGroup.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute is an internal helper for RouteGroup to register routes.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} else if relativePath == "/" && rg.prefix == "" {
		pathSegment = "/"
	}

	fullPath := rg.prefix + pathSegment
	if fullPath == "" {
		fullPath = "/"
	}

	allApplicableMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allApplicableMiddleware = append(allApplicableMiddleware, rg.middleware...)
	allApplicableMiddleware = append(allApplicableMiddleware, middlewares...)

	rg.router.addRoute(method, fullPath, handler, allApplicableMiddleware...)
}

// HTTP method registrations for RouteGroup.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware)     { rg.addRoute(MethodGet, relativePath, handler, middlewares...) }
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware)    { rg.addRoute(MethodPost, relativePath, handler, middlewares...) }
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware)     { rg.addRoute(MethodPut, relativePath, handler, middlewares...) }
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware)  { rg.addRoute(MethodDelete, relativePath, handler, middlewares...) }
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware)   { rg.addRoute(MethodPatch, relativePath, handler, middlewares...) }
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware)    { rg.addRoute(MethodHead, relativePath, handler, middlewares...) }
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) { rg.addRoute(MethodOptions, relativePath, handler, middlewares...) }

// Group creates a new sub-RouteGroup nested under an existing RouteGroup.
func (rg *RouteGroup) Group(relativePathPrefix string, middlewares ...Middleware) *RouteGroup {
	pathSegment := ""
	if relativePathPrefix != "" && relativePathPrefix != "/" {
		pathSegment = "/" + strings.Trim(relativePathPrefix, "/")
	}
	newFullPrefix := rg.prefix + pathSegment
	if newFullPrefix == "" {
		newFullPrefix = "/"
	}

	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...)
	combinedMiddleware = append(combinedMiddleware, middlewares...)

	return &RouteGroup{
		router:     rg.router,
		prefix:     newFullPrefix,
		middleware: combinedMiddleware,
	}
}

// isTerminalWriter is a helper to check if an io.Writer is a terminal.
// This is used by NewWithConfig to decide on coloring for DefaultLogger.
// It's defined here to be co-located with its usage in NewWithConfig if not made public elsewhere.
// Alternatively, DefaultLogger.EnableColor could be made smarter to do this check internally.
// (As per our previous discussion, DefaultLogger.EnableColor now handles this internally,
// so this helper might not be strictly needed here if NewWithConfig just calls .EnableColor(true))
// For clarity, if DefaultLogger.EnableColor handles the TTY check, this can be removed from router.go.
// Let's assume DefaultLogger.EnableColor is smart, so we don't need this helper here.
/*
func isTerminalWriter(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return false
		}
		return (stat.Mode() & os.ModeCharDevice) == os.ModeCharDevice
	}
	return false
}
*/
