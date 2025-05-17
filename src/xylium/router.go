// src/xylium/router.go
package xylium

import (
	"encoding/json" // Used in ServeFiles for PathNotFound JSON response
	"fmt"           // For error formatting
	"io"            // For HTMLRenderer
	"log"           // For fallback logger if router is not configured
	"os"            // For fallback logger if router is not configured
	"path/filepath" // For path cleaning in ServeFiles
	"runtime/debug" // For stack trace on panic
	"strings"       // For path manipulation

	"github.com/valyala/fasthttp" // Used for fasthttp.RequestCtx type and status constants
)

// HTMLRenderer defines the interface for HTML template rendering.
// Users can provide their custom implementations.
type HTMLRenderer interface {
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router is the main router for the Xylium framework.
// It holds the routing tree, global middleware, custom handlers, and operating mode.
type Router struct {
	tree             *Tree        // Radix tree for route matching.
	globalMiddleware []Middleware // Middleware applied to all routes.

	// Custom handlers for various events.
	PanicHandler            HandlerFunc // Handler for recovered panics.
	NotFoundHandler         HandlerFunc // Handler for routes not found (404).
	MethodNotAllowedHandler HandlerFunc // Handler for disallowed HTTP methods on existing routes (405).
	GlobalErrorHandler      HandlerFunc // Main handler for errors returned by route handlers or middleware.

	serverConfig ServerConfig // Server configuration (defined in router_server.go).
	HTMLRenderer HTMLRenderer // Optional HTML renderer.
	instanceMode string       // Operating mode specific to this router instance.
}

// Logger returns the configured logger for the router.
// It returns the xylium.Logger interface.
func (r *Router) Logger() Logger {
	// Ensure a logger is always available.
	if r.serverConfig.Logger == nil {
		// Absolute fallback if the logger was not properly initialized.
		// This should ideally not happen if New() or NewWithConfig() is used.
		return log.New(os.Stderr, "[xyliumRouterFallbackLog] ", log.LstdFlags)
	}
	return r.serverConfig.Logger
}

// New creates a new Router instance with default configuration.
// The router's operating mode will be set based on the current global mode.
func New() *Router {
	return NewWithConfig(DefaultServerConfig())
}

// NewWithConfig creates a new Router instance with the given ServerConfig.
// The router's operating mode will be set based on the current global mode.
func NewWithConfig(config ServerConfig) *Router {
	// The global `currentGlobalMode` is already initialized by `init()` in mode.go
	// or by a previous call to `SetMode()`.
	// This router instance's mode is set from the current global mode.
	routerInstance := &Router{
		tree:             NewTree(),
		globalMiddleware: make([]Middleware, 0),
		serverConfig:     config,
		instanceMode:     Mode(), // Set instance mode from current global mode.
	}

	// After the first router instance is created, "lock" global mode changes
	// to issue a warning if SetMode() is called again.
	lockModeChanges()

	// Set default handlers (defined in router_defaults.go).
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Ensure the logger is never nil after initialization.
	if routerInstance.serverConfig.Logger == nil {
		routerInstance.serverConfig.Logger = log.New(os.Stderr, "[xyliumSrvFallbackInit] ", log.LstdFlags)
	}

	// Log the operating mode when the router is initialized,
	// especially if not in release mode (to reduce noise in production).
	if routerInstance.instanceMode != ReleaseMode {
		routerInstance.Logger().Printf("Xylium Router initialized in '%s' mode.", routerInstance.instanceMode)
	} else {
		// Optionally, a very brief log for release mode startup.
		// routerInstance.Logger().Printf("Xylium Router initialized.")
	}

	return routerInstance
}

// CurrentMode returns the operating mode of this specific router instance.
func (r *Router) CurrentMode() string {
	return r.instanceMode
}

// Use adds global middleware to the router.
// These are applied before any group or route-specific middleware.
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// addRoute is an internal method to register a new route.
// The handler and route-specific middleware are added to the routing tree.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/" // Normalize empty path to root.
	}
	if path[0] != '/' {
		panic("xylium: path must begin with '/'")
	}
	r.tree.Add(method, path, handler, middlewares...)
}

// GET registers a route for the HTTP GET method.
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodGet, path, handler, middlewares...)
}

// POST registers a route for the HTTP POST method.
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPost, path, handler, middlewares...)
}

// PUT registers a route for the HTTP PUT method.
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPut, path, handler, middlewares...)
}

// DELETE registers a route for the HTTP DELETE method.
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodDelete, path, handler, middlewares...)
}

// PATCH registers a route for the HTTP PATCH method.
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPatch, path, handler, middlewares...)
}

// HEAD registers a route for the HTTP HEAD method.
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodHead, path, handler, middlewares...)
}

// OPTIONS registers a route for the HTTP OPTIONS method.
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodOptions, path, handler, middlewares...)
}

// Handler is the main fasthttp.RequestHandlerFunc implementation for the router.
// This function is called by the fasthttp server for each incoming request.
// It's responsible for:
// 1. Acquiring a Context from the pool.
// 2. Finding a matching route.
// 3. Building and executing the middleware and handler chain.
// 4. Handling panics and returned errors.
// 5. Releasing the Context back to the pool.
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) {
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r) // Associate the router with the context.
	defer releaseCtx(c)

	var errHandler error           // Stores any error returned by the handler chain.
	currentLogger := r.Logger() // Get the configured logger for this router instance.

	defer func() {
		// This deferred block executes after the main handler chain finishes or if a panic occurs.

		// 1. Panic Recovery
		if rec := recover(); rec != nil {
			// Always log panics with stack trace to the server logs.
			currentLogger.Printf("PANIC: %v\n%s", rec, string(debug.Stack()))
			if r.PanicHandler != nil {
				c.Set("panic_recovery_info", rec) // Store panic info in context.
				// The PanicHandler will decide the client response, considering the operating mode.
				errHandler = r.PanicHandler(c)
			} else {
				// Fallback if PanicHandler is not set (should not happen with proper initialization).
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic").WithInternal(fmt.Errorf("panic: %v", rec))
			}
		}

		// 2. Global Error Handling
		// If errHandler (from panic or handler return) is not nil, process it via GlobalErrorHandler.
		if errHandler != nil {
			if !c.ResponseCommitted() { // Only if the response has not been sent to the client.
				if r.GlobalErrorHandler != nil {
					c.Set("handler_error_cause", errHandler) // Store the original error cause in context.
					// The GlobalErrorHandler will decide the client response, considering the operating mode.
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						// If GlobalErrorHandler itself fails, this is a critical situation.
						currentLogger.Printf("CRITICAL: Error during global error handling: %v (original error: %v)", globalErrHandlingErr, errHandler)
						// Absolute fallback: send a simple 500 response.
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						c.Ctx.Response.SetBodyString("Internal Server Error") // Keep it simple.
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else {
					// Fallback if GlobalErrorHandler is not set (should not happen).
					currentLogger.Printf("Error (GlobalErrorHandler is nil): %v for %s %s", errHandler, c.Method(), c.Path())
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error")
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else {
				// If the response has already been committed, nothing can be sent to the client.
				// Log the error that occurred after the response was sent.
				currentLogger.Printf("Warning: Response already committed but an error was generated: %v for %s %s", errHandler, c.Method(), c.Path())
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// 3. Response Check (if no error occurred)
			// If the handler chain completed without an error, but no response was sent (for non-HEAD requests),
			// this might indicate a bug in a handler. Log a warning in DebugMode.
			statusCode := c.Ctx.Response.StatusCode()
			// A response is considered "not sent" if status code is still 0 (fasthttp default),
			// or if status is OK (200) but the body is empty.
			if statusCode == 0 || (statusCode == StatusOK && len(c.Ctx.Response.Body()) == 0) {
				if r.CurrentMode() == DebugMode { // Log this warning only in DebugMode to reduce noise.
					currentLogger.Printf("[XYLIUM-DEBUG] Warning: Handler chain completed for %s %s without sending a response or error.", c.Method(), c.Path())
				}
			}
		}
	}() // End of main defer block.

	// Get method and path from the request.
	method := c.Method()
	path := c.Path()

	// Find the route in the tree.
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil { // Route found.
		c.Params = params // Set extracted route parameters into the context.

		// Build the handler chain: global middleware -> group middleware (if any, handled by RouteGroup.addRoute) -> route-specific middleware -> main handler.
		// Middleware is applied in reverse order (wrapping).
		finalChain := nodeHandler
		// Apply route/group middleware (these are already combined when tree.Add was called from Router or RouteGroup).
		for i := len(routeMiddleware) - 1; i >= 0; i-- {
			finalChain = routeMiddleware[i](finalChain)
		}
		// Apply global middleware.
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- {
			finalChain = r.globalMiddleware[i](finalChain)
		}

		c.handlers = []HandlerFunc{finalChain} // The context stores the final composed handler.
		c.index = -1                           // Reset index for c.Next().
		errHandler = c.Next()                  // Execute the handler chain.
	} else { // Route not found.
		if len(allowedMethods) > 0 { // Path exists, but the HTTP method is not allowed.
			c.Params = params // Parameters might be useful for the MethodNotAllowedHandler.
			if r.MethodNotAllowedHandler != nil {
				c.SetHeader("Allow", strings.Join(allowedMethods, ", ")) // Set "Allow" header as per RFC.
				errHandler = r.MethodNotAllowedHandler(c)
			} else {
				// Fallback if MethodNotAllowedHandler is not set.
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else { // Path does not exist at all.
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else {
				// Fallback if NotFoundHandler is not set.
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
}

// ServeFiles serves static files from the given root directory at the specified path prefix.
// Example: r.ServeFiles("/static", "./public_html") will serve files from "./public_html"
// when requests come to paths starting with "/static".
func (r *Router) ServeFiles(pathPrefix string, rootDir string) {
	if strings.Contains(pathPrefix, ":") || strings.Contains(pathPrefix, "*") {
		panic("xylium: pathPrefix for ServeFiles cannot contain ':' or '*'")
	}
	fileSystemRoot := filepath.Clean(rootDir) // Clean the root directory path.
	paramName := "filepath"                   // Name of the catch-all parameter for the file subpath.

	// Normalize pathPrefix.
	// If "/" or "", it means serve from the application root ("").
	// Otherwise, ensure it starts with "/" and does not end with "/".
	if pathPrefix == "/" || pathPrefix == "" {
		pathPrefix = ""
	} else {
		pathPrefix = "/" + strings.Trim(pathPrefix, "/")
	}

	// Construct the route path with a catch-all parameter.
	// e.g., "/static/*filepath" or "/*filepath" if pathPrefix is root.
	routePath := pathPrefix + "/*" + paramName
	if pathPrefix == "" {
		routePath = "/*" + paramName
	}

	// Get the router's logger for use in the PathNotFound callback.
	frameworkLogger := r.Logger()

	// Configure fasthttp.FS.
	fs := &fasthttp.FS{
		Root:               fileSystemRoot,
		IndexNames:         []string{"index.html"}, // Files to look for if the path is a directory.
		GenerateIndexPages: false,                  // Typically false for APIs; does not generate directory listings.
		AcceptByteRange:    true,                   // Allow byte range requests.
		Compress:           true,                   // Allow fasthttp to compress files (if client supports).
		PathNotFound: func(originalFasthttpCtx *fasthttp.RequestCtx) {
			// This callback is invoked by fasthttp.FS if a file is not found.
			// We will send a JSON 404 response.
			errorMsg := M{"error": "File not found in static assets"}
			he := NewHTTPError(StatusNotFound, errorMsg)

			originalFasthttpCtx.SetStatusCode(he.Code)
			originalFasthttpCtx.SetContentType("application/json; charset=utf-8")

			// Encode the error message to the response body.
			if err := json.NewEncoder(originalFasthttpCtx.Response.BodyWriter()).Encode(he.Message); err != nil {
				// If encoding fails, log this error using the framework's logger.
				if frameworkLogger != nil { // Always check if logger is available.
					frameworkLogger.Printf(
						"xylium: Error encoding JSON for PathNotFound (ServeFiles) for URI %s: %v. Client received 404 but body might be incomplete.",
						originalFasthttpCtx.RequestURI(), // The client-requested URI that led to PathNotFound.
						err,
					)
				} else {
					// Fallback if the framework logger is somehow nil.
					log.Printf(
						"[xyliumSrvFilesFallback] Error encoding JSON for PathNotFound (ServeFiles) for URI %s: %v.",
						originalFasthttpCtx.RequestURI(),
						err,
					)
				}
				// At this point, headers might have been partially sent.
				// It's safer not to attempt further modifications to the body.
			}
		},
	}
	fileHandler := fs.NewRequestHandler() // Get the request handler from fasthttp.FS.

	// Register the GET route for serving static files.
	r.GET(routePath, func(c *Context) error {
		// Get the file subpath from the route parameter.
		requestedFileSubPath := c.Param(paramName)

		// The path provided to fileHandler should be relative to FS.Root
		// and typically start with '/'.
		pathForFS := requestedFileSubPath
		if len(pathForFS) > 0 && pathForFS[0] != '/' {
			pathForFS = "/" + pathForFS
		} else if len(pathForFS) == 0 {
			// If requestedFileSubPath is empty (e.g., request to "/static/"),
			// set pathForFS to "/" to let fasthttp.FS look for an index file.
			pathForFS = "/"
		}

		// Temporarily modify the RequestURI on the fasthttp.RequestCtx to what fileHandler expects.
		c.Ctx.Request.SetRequestURI(pathForFS)

		// Let fasthttp.FS handle the request.
		fileHandler(c.Ctx)

		// Restoring the original RequestURI is generally not necessary here because
		// the Context will be reset from the pool for the next request.
		// However, if there were middleware or logic running *after* this in the same handler
		// that relied on the original RequestURI, then restoration would be needed.

		// The fasthttp.FS handler takes care of the response, so return nil.
		return nil
	})
}

// --- Route Grouping ---

// RouteGroup allows grouping routes under a common path prefix and/or with common middleware.
type RouteGroup struct {
	router     *Router      // Reference to the main Router instance.
	prefix     string       // Path prefix for this group (e.g., "/api/v1").
	middleware []Middleware // Middleware specific to this group.
}

// Group creates a new RouteGroup from the main Router.
// All routes added to this group will be prefixed with `prefix`.
// The provided `middlewares` will be applied to all routes within this group.
func (r *Router) Group(prefix string, middlewares ...Middleware) *RouteGroup {
	// Normalize the prefix: ensure it starts with '/' and does not end with '/',
	// unless the prefix is the root ("/" or "").
	normalizedPrefix := ""
	if prefix != "" && prefix != "/" {
		normalizedPrefix = "/" + strings.Trim(prefix, "/")
	} else if prefix == "/" {
		// If the prefix is explicitly "/", treat it as an empty string for consistency,
		// as both "" (root) and "/" are handled similarly in path construction.
		normalizedPrefix = ""
	}

	// Copy the middleware slice to prevent external modification.
	groupMiddleware := make([]Middleware, len(middlewares))
	copy(groupMiddleware, middlewares)

	return &RouteGroup{
		router:     r,
		prefix:     normalizedPrefix,
		middleware: groupMiddleware,
	}
}

// Use adds middleware to the RouteGroup.
// This middleware is applied after router global middleware and parent group middleware (if any),
// and before route-specific middleware.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute is an internal helper for RouteGroup to register a route.
// It combines the group's prefix with the relativePath and applies combined middleware.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	// Normalize relativePath.
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} else if relativePath == "/" && rg.prefix == "" {
		// If group prefix is root ("") and relativePath is "/", full path is "/".
		pathSegment = "/"
	} else if relativePath == "/" && rg.prefix != "" {
		// If group prefix is not root and relativePath is "/",
		// pathSegment is empty; full path will be just rg.prefix.
		pathSegment = ""
	}

	// Combine group prefix with path segment.
	// rg.prefix is already normalized (starts with '/' or is empty).
	// pathSegment is already normalized (starts with '/' or is empty, or is a single "/").
	fullPath := rg.prefix + pathSegment
	if fullPath == "" { // If both prefix and segment are effectively empty.
		fullPath = "/"
	} else if strings.HasPrefix(fullPath, "//") { // Avoid double slashes if prefix is not empty and pathSegment starts with /.
		fullPath = strings.TrimPrefix(fullPath, "/") // Remove one leading slash
		if fullPath == "" { // If result is empty (e.g. prefix "/" and pathSegment "/"), make it "/"
			fullPath = "/"
		} else {
			fullPath = "/" + fullPath // Add back the single leading slash
		}
	}


	// Combine group middleware with route-specific middleware.
	allGroupAndRouteMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, rg.middleware...) // Group middleware first.
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, middlewares...)   // Then route-specific middleware.

	// Register the route with the main router.
	rg.router.addRoute(method, fullPath, handler, allGroupAndRouteMiddleware...)
}

// GET registers an HTTP GET route for the RouteGroup.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodGet, relativePath, handler, middlewares...)
}

// POST registers an HTTP POST route for the RouteGroup.
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPost, relativePath, handler, middlewares...)
}

// PUT registers an HTTP PUT route for the RouteGroup.
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPut, relativePath, handler, middlewares...)
}

// DELETE registers an HTTP DELETE route for the RouteGroup.
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodDelete, relativePath, handler, middlewares...)
}

// PATCH registers an HTTP PATCH route for the RouteGroup.
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPatch, relativePath, handler, middlewares...)
}

// HEAD registers an HTTP HEAD route for the RouteGroup.
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodHead, relativePath, handler, middlewares...)
}

// OPTIONS registers an HTTP OPTIONS route for the RouteGroup.
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodOptions, relativePath, handler, middlewares...)
}

// Group creates a sub-group from an existing RouteGroup.
// The sub-group inherits the prefix and middleware from its parent group.
func (rg *RouteGroup) Group(relativePath string, middlewares ...Middleware) *RouteGroup {
	// Normalize the path segment for the sub-group.
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} // If relativePath is "/" or "", pathSegment remains empty; new prefix effectively same as parent's.

	newPrefix := rg.prefix + pathSegment
	if newPrefix == "" {
		newPrefix = "/" // If both parent prefix and segment are effectively empty.
	} else if strings.HasPrefix(newPrefix, "//") { // Clean up double slashes.
		newPrefix = strings.TrimPrefix(newPrefix, "/")
		if newPrefix == "" {
			newPrefix = "/"
		} else {
			newPrefix = "/" + newPrefix
		}
	}

	// Combine parent group's middleware with the new sub-group's middleware.
	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...) // Inherit parent's middleware.
	combinedMiddleware = append(combinedMiddleware, middlewares...)   // Add new middleware for the sub-group.

	return &RouteGroup{
		router:     rg.router, // The main Router instance remains the same.
		prefix:     newPrefix,
		middleware: combinedMiddleware,
	}
}
