// src/xylium/router.go
package xylium

import (
	"encoding/json" // Used in ServeFiles for PathNotFound JSON response.
	"fmt"           // For error formatting.
	"io"            // For HTMLRenderer interface.
	"log"           // For fallback logger if router is not configured with a custom one, and for log.LstdFlags etc.
	"os"            // For os.Stderr used by the fallback logger.
	"path/filepath" // For path cleaning and manipulation in ServeFiles.
	"runtime/debug" // For capturing stack traces on panic.
	"strings"       // For string manipulation (path normalization, joining).

	"github.com/valyala/fasthttp" // The underlying HTTP engine; used for fasthttp.RequestCtx and status constants.
)

// HTMLRenderer defines the interface for HTML template rendering.
// Applications can provide their own custom implementations (e.g., using standard html/template, pongo2, etc.)
// to integrate their preferred templating engine with Xylium's c.HTML() method.
type HTMLRenderer interface {
	// Render renders an HTML template.
	// Parameters:
	//  - w: The io.Writer to which the rendered HTML should be written (typically the response body writer).
	//  - name: The name of the template file to render.
	//  - data: The data to be passed to the template.
	//  - c: The Xylium Context, providing access to request-specific information if needed by the renderer.
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router is the main request router for the Xylium framework.
// It holds the routing tree (for efficient path matching), global middleware,
// custom handlers for events like panics or 404s, server configuration,
// an optional HTML renderer, and its own operating mode (derived from global settings).
type Router struct {
	tree             *Tree        // Radix tree for efficient route matching and parameter extraction.
	globalMiddleware []Middleware // Middleware applied to all routes handled by this router.

	// Custom handlers for various framework-level events.
	PanicHandler            HandlerFunc // Handler invoked when a panic is recovered during request processing.
	NotFoundHandler         HandlerFunc // Handler invoked when no route matches the request path (HTTP 404).
	MethodNotAllowedHandler HandlerFunc // Handler invoked when a route matches the path but not the HTTP method (HTTP 405).
	GlobalErrorHandler      HandlerFunc // Central handler for errors returned by route handlers or middleware.

	serverConfig ServerConfig // Configuration for the underlying fasthttp.Server.
	HTMLRenderer HTMLRenderer // Optional HTML template renderer.
	instanceMode string       // Operating mode (e.g., "debug", "release") specific to this router instance.
	                             // This is set at router creation time based on Xylium's global mode.
}

// Logger returns the configured logger for this router instance.
// It ensures that a logger is always available, falling back to a standard Go logger
// if no custom logger was provided in the ServerConfig.
//
// Returns:
//   - Logger: The xylium.Logger interface implementation.
func (r *Router) Logger() Logger {
	if r.serverConfig.Logger == nil {
		// Absolute fallback if the logger was not properly initialized (e.g., if ServerConfig was manually created without a logger).
		// This should ideally not be reached if New() or NewWithConfig() (with DefaultServerConfig) is used.
		return log.New(os.Stderr, "[XyliumRouter-FallbackLog] ", log.LstdFlags|log.Lshortfile)
	}
	return r.serverConfig.Logger
}

// New creates a new Router instance with default server configuration.
// The router's operating mode will be set based on the current global Xylium mode
// at the time of this call.
func New() *Router {
	return NewWithConfig(DefaultServerConfig())
}

// NewWithConfig creates a new Router instance with the provided ServerConfig.
// The router's operating mode (`instanceMode`) is determined by calling `Mode()`
// at the time of creation, reflecting the current global Xylium operating mode.
// This function also notifies the Xylium mode management system that a router instance
// has been created, which is used by `SetMode()` to issue warnings if the global
// mode is changed after router instantiation.
//
// Parameters:
//   - config: A ServerConfig struct containing settings for the underlying fasthttp server
//             and router-specific configurations like the logger.
//
// Returns:
//   - *Router: A pointer to the newly created Router instance.
func NewWithConfig(config ServerConfig) *Router {
	// Determine the effective global Xylium mode at the moment this router is created.
	// This mode will be adopted by this specific router instance.
	effectiveMode := Mode() // Calls Mode() from src/xylium/mode.go

	routerInstance := &Router{
		tree:             NewTree(),                // Initialize a new radix tree for routing.
		globalMiddleware: make([]Middleware, 0),    // Initialize an empty slice for global middleware.
		serverConfig:     config,                   // Store the provided server configuration.
		instanceMode:     effectiveMode,            // Set this router's operating mode.
	}

	// Notify the global Xylium mode management system that a router instance has now been created.
	// This allows `SetMode()` to log a warning if the global mode is changed
	// *after* this point, as existing routers (like this one) won't pick up that change.
	notifyRouterCreated() // From src/xylium/mode.go

	// Set default handlers for common scenarios (404, 405, panics, global errors).
	// These are defined in router_defaults.go and can be overridden by the user.
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Ensure that the router always has a non-nil logger.
	// If the provided ServerConfig didn't include a logger, use a fallback.
	if routerInstance.serverConfig.Logger == nil {
		routerInstance.serverConfig.Logger = log.New(os.Stderr, "[XyliumServer-FallbackInitLog] ", log.LstdFlags|log.Lshortfile)
	}

	// Log the initialization of this router instance, including the operating mode it has adopted.
	// This uses the router's own configured logger.
	routerInstance.Logger().Printf("Xylium Router initialized (Adopting Mode: %s)", routerInstance.instanceMode)

	return routerInstance
}

// CurrentMode returns the operating mode (e.g., "debug", "release", "test")
// of this specific router instance. This mode is fixed at the time of router creation.
func (r *Router) CurrentMode() string {
	return r.instanceMode
}

// Use adds one or more global middleware functions to the router's middleware chain.
// These middleware will be executed for every request handled by this router,
// before any group-specific or route-specific middleware.
// Middleware are applied in the order they are added.
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// addRoute is an internal helper method to register a new route in the router's radix tree.
// It normalizes the path and associates the handler and any route-specific middleware with it.
//
// Parameters:
//   - method: The HTTP method (e.g., "GET", "POST").
//   - path: The URL path pattern for the route.
//   - handler: The HandlerFunc to execute for this route.
//   - middlewares: Optional route-specific middleware to apply before the handler.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/" // Normalize an empty path to the root path "/".
	}
	if path[0] != '/' {
		// All paths must start with a forward slash.
		panic("xylium: path must begin with '/' (e.g., \"/users\")")
	}
	// The tree's Add method will handle further normalization (like removing trailing slashes).
	r.tree.Add(method, path, handler, middlewares...)
}

// GET registers a new route for the HTTP GET method.
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodGet, path, handler, middlewares...)
}

// POST registers a new route for the HTTP POST method.
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPost, path, handler, middlewares...)
}

// PUT registers a new route for the HTTP PUT method.
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPut, path, handler, middlewares...)
}

// DELETE registers a new route for the HTTP DELETE method.
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodDelete, path, handler, middlewares...)
}

// PATCH registers a new route for the HTTP PATCH method.
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPatch, path, handler, middlewares...)
}

// HEAD registers a new route for the HTTP HEAD method.
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodHead, path, handler, middlewares...)
}

// OPTIONS registers a new route for the HTTP OPTIONS method.
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodOptions, path, handler, middlewares...)
}

// Handler is the core fasthttp.RequestHandlerFunc implementation for the Xylium router.
// This function is invoked by the underlying fasthttp server for each incoming HTTP request.
// Its responsibilities include:
// 1. Acquiring a `Context` object from a pool.
// 2. Associating this router instance with the context.
// 3. Finding a matching route in the radix tree based on the request method and path.
// 4. Assembling and executing the complete middleware chain (global -> group (if any) -> route -> handler).
// 5. Recovering from any panics that occur during request processing.
// 6. Processing any errors returned by the handler chain via the `GlobalErrorHandler`.
// 7. Ensuring the `Context` object is released back to the pool after the request is handled.
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) {
	// Acquire a Context from the pool and initialize it.
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r) // Link this router instance to the context.
	// Defer releasing the context back to the pool to ensure it happens even if panics occur.
	defer releaseCtx(c)

	var errHandler error           // Variable to store any error returned by the handler chain or panic recovery.
	currentLogger := r.Logger() // Get the router's configured logger.

	// Deferred function for centralized panic recovery and error handling.
	defer func() {
		// This block executes after the main handler chain completes or if a panic is recovered.

		// 1. Panic Recovery:
		if rec := recover(); rec != nil {
			// A panic occurred. Log it with a stack trace.
			currentLogger.Printf("PANIC: %v\n%s", rec, string(debug.Stack())) // Always log full stack for panics.
			if r.PanicHandler != nil {
				c.Set("panic_recovery_info", rec) // Make panic info available to the PanicHandler.
				// Invoke the configured PanicHandler to determine the client response.
				errHandler = r.PanicHandler(c)
			} else {
				// Fallback if PanicHandler is somehow not set (shouldn't happen with New/NewWithConfig).
				// Create a standard HTTPError for internal server error.
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic.").WithInternal(fmt.Errorf("panic: %v", rec))
			}
		}

		// 2. Global Error Handling for errors returned by handlers or the PanicHandler:
		if errHandler != nil {
			// Only attempt to send an error response if one hasn't already been committed.
			if !c.ResponseCommitted() {
				if r.GlobalErrorHandler != nil {
					c.Set("handler_error_cause", errHandler) // Make the original error available to GlobalErrorHandler.
					// Invoke the GlobalErrorHandler to format and send the client response.
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						// If GlobalErrorHandler itself fails, this is a critical unrecoverable state for this request.
						currentLogger.Printf("CRITICAL: Error occurred within GlobalErrorHandler itself: %v (while handling original error: %v)", globalErrHandlingErr, errHandler)
						// Send an absolute fallback plain text 500 response.
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						c.Ctx.Response.SetBodyString("Internal Server Error")
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else {
					// Fallback if GlobalErrorHandler is not set (highly unlikely).
					currentLogger.Printf("Error (GlobalErrorHandler is nil): %v for %s %s. Sending fallback 500.", errHandler, c.Method(), c.Path())
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error")
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else {
				// If the response has already been committed, we can't send a new error response.
				// Log the error that occurred after the response was sent.
				currentLogger.Printf("Warning: Response already committed, but an error was generated post-commitment: %v for %s %s", errHandler, c.Method(), c.Path())
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// 3. Sanity Check for Unhandled Responses (if no error occurred):
			// If the handler chain completed without error, but no response was sent (and it's not a HEAD request),
			// this might indicate a bug in a handler (e.g., forgot to call c.JSON, c.String, etc.).
			statusCode := c.Ctx.Response.StatusCode()
			bodyLen := len(c.Ctx.Response.Body())
			contentLengthHeader := c.Ctx.Response.Header.ContentLength() // -1 if not set, -2 if chunked

			// A response might be considered "not sent" if status is 0 (fasthttp default before anything is set),
			// or if status is 200 OK but body is empty and Content-Length isn't explicitly set to a positive value.
			// (StatusNoContent (204) is a valid empty response and should not trigger this warning).
			isEffectivelyEmptyResponse := (statusCode == 0) || (statusCode == StatusOK && bodyLen == 0 && contentLengthHeader <= 0)

			if isEffectivelyEmptyResponse && statusCode != StatusNoContent {
				// Log this warning only in DebugMode to avoid excessive noise in production/test.
				if r.CurrentMode() == DebugMode {
					currentLogger.Printf("[XYLIUM-DEBUG] Warning: Handler chain for %s %s completed without sending a response body or error (Status: %d, BodyLen: %d, ContentLength: %d). Ensure handlers explicitly send a response.",
						c.Method(), c.Path(), statusCode, bodyLen, contentLengthHeader)
				}
			}
		}
	}() // End of main defer block.

	// --- Main Request Processing Logic ---
	method := c.Method() // Get HTTP method from context.
	path := c.Path()     // Get request path from context.

	// Find the matching route in the radix tree.
	// `params` will contain extracted URL parameters.
	// `allowedMethods` is used for 405 Method Not Allowed responses.
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil { // Route found for the given path and method.
		c.Params = params // Store extracted path parameters in the context.

		// Build the complete handler chain by applying middleware in the correct order:
		// Global Middleware (outermost) -> Group Middleware -> Route-Specific Middleware -> Actual Handler (innermost).
		// Middleware is applied by wrapping, so iteration is in reverse.
		finalChain := nodeHandler // Start with the route's actual handler.

		// Apply route-specific and group-specific middleware (already combined by tree.Add or group.addRoute).
		for i := len(routeMiddleware) - 1; i >= 0; i-- {
			finalChain = routeMiddleware[i](finalChain)
		}
		// Apply global middleware registered with the router.
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- {
			finalChain = r.globalMiddleware[i](finalChain)
		}

		// Set up the context for execution via c.Next().
		c.handlers = []HandlerFunc{finalChain} // The context stores the final, fully composed handler.
		c.index = -1                           // Reset handler index for the c.Next() call.
		errHandler = c.Next()                  // Execute the handler chain.
	} else { // No handler found for this specific method and path.
		if len(allowedMethods) > 0 {
			// Route path exists, but not for the requested HTTP method (405 Method Not Allowed).
			c.Params = params // Parameters might be relevant for the 405 handler.
			if r.MethodNotAllowedHandler != nil {
				c.SetHeader("Allow", strings.Join(allowedMethods, ", ")) // Set "Allow" header as per RFC 7231.
				errHandler = r.MethodNotAllowedHandler(c)
			} else {
				// Fallback if MethodNotAllowedHandler is not custom-set.
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else {
			// Path does not exist in the routing tree at all (404 Not Found).
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else {
				// Fallback if NotFoundHandler is not custom-set.
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
}

// ServeFiles serves static files from a given filesystem root directory, mounted at a specified URL path prefix.
// For example, `r.ServeFiles("/static", "./public_html")` will serve files from the
// "./public_html" directory when requests are made to URLs starting with "/static".
// It uses fasthttp.FS for efficient file serving.
//
// Parameters:
//   - urlPathPrefix: The URL prefix where the static files will be accessible (e.g., "static", "assets/content").
//                    Leading/trailing slashes are handled. If empty or "/", files are served from application root.
//   - fileSystemRoot: The root directory on the filesystem containing the static files.
func (r *Router) ServeFiles(urlPathPrefix string, fileSystemRoot string) {
	if strings.Contains(urlPathPrefix, ":") || strings.Contains(urlPathPrefix, "*") {
		panic("xylium: urlPathPrefix for ServeFiles cannot contain ':' or '*' (reserved for parameters/wildcards)")
	}
	// Clean the filesystem root path for security and consistency.
	cleanedFileSystemRoot := filepath.Clean(fileSystemRoot)
	// Define the name for the catch-all parameter that will capture the file subpath.
	catchAllParamName := "filepath" // This is the "name" part of `*name` in the route.

	// Normalize the URL path prefix for routing tree registration.
	// Xylium's tree expects prefixes like "static" or "group/subgroup" (without leading/trailing slashes if not root).
	// An empty string "" signifies serving from the application root "/".
	normalizedUrlPathPrefix := ""
	if urlPathPrefix != "" && urlPathPrefix != "/" {
		normalizedUrlPathPrefix = strings.Trim(urlPathPrefix, "/")
	} // If urlPathPrefix is "/" or "" (empty), normalizedUrlPathPrefix remains "", treated as root.

	// Construct the route path pattern for the radix tree.
	// e.g., if normalizedUrlPathPrefix is "static", routePath becomes "/static/*filepath".
	// if normalizedUrlPathPrefix is "", routePath becomes "/*filepath".
	routePath := ""
	if normalizedUrlPathPrefix == "" { // Serving from application root
		routePath = "/*" + catchAllParamName
	} else {
		routePath = "/" + normalizedUrlPathPrefix + "/*" + catchAllParamName
	}

	// Get the router's logger for use in the fasthttp.FS PathNotFound callback.
	frameworkLogger := r.Logger()

	// Configure fasthttp.FS for serving files.
	fs := &fasthttp.FS{
		Root:               cleanedFileSystemRoot,  // Set the filesystem root directory.
		IndexNames:         []string{"index.html"}, // Default file to serve if a directory path is requested.
		GenerateIndexPages: false,                  // Disable automatic directory listing generation (safer for APIs).
		AcceptByteRange:    true,                   // Enable support for HTTP byte range requests.
		Compress:           true,                   // Allow fasthttp to compress static files (e.g., Gzip) if client supports.
		PathNotFound: func(originalFasthttpCtx *fasthttp.RequestCtx) {
			// This custom callback is invoked by fasthttp.FS if a requested file is not found within its Root.
			// We send a JSON 404 response to maintain API consistency.
			// Note: M, NewHTTPError, StatusNotFound are from the current 'xylium' package.
			errorMsg := M{"error": "The requested static asset was not found."}
			httpErr := NewHTTPError(StatusNotFound, errorMsg)

			originalFasthttpCtx.SetStatusCode(httpErr.Code)
			originalFasthttpCtx.SetContentType("application/json; charset=utf-8")

			if err := json.NewEncoder(originalFasthttpCtx.Response.BodyWriter()).Encode(httpErr.Message); err != nil {
				logMsg := fmt.Sprintf(
					"Xylium ServeFiles: CRITICAL - Error encoding JSON for PathNotFound callback (URI: %s): %v. Client received 404 but body might be malformed.",
					string(originalFasthttpCtx.RequestURI()), err, // Convert byte slice URI to string
				)
				if frameworkLogger != nil {
					frameworkLogger.Printf(logMsg)
				} else {
					log.Println(logMsg) // Absolute fallback.
				}
			}
		},
	}
	// Get the fasthttp request handler from the configured fasthttp.FS instance.
	fileServerHandler := fs.NewRequestHandler()

	// Register a GET route in Xylium to handle requests for static files.
	// Note: c is *Context from the current 'xylium' package.
	r.GET(routePath, func(c *Context) error {
		// Extract the requested file's subpath from the catch-all route parameter.
		requestedFileSubPath := c.Param(catchAllParamName)

		// The path provided to fasthttp.FS.NewRequestHandler (via c.Ctx.Request.SetRequestURI)
		// must be relative to FS.Root and must start with '/'.
		pathForFasthttpFS := "/" + requestedFileSubPath
		// Clean the path to resolve any ".." or "." and remove redundant slashes.
		pathForFasthttpFS = filepath.Clean(pathForFasthttpFS)
		// Ensure it still starts with "/" after cleaning, as Clean might remove it if path becomes ".".
		if !strings.HasPrefix(pathForFasthttpFS, "/") && pathForFasthttpFS != "." { // "." becomes "/"
			pathForFasthttpFS = "/" + pathForFasthttpFS
		} else if pathForFasthttpFS == "." { // if subpath was empty or just slashes, Clean results in "."
			pathForFasthttpFS = "/" // Serve index file from root of fs.Root
		}

		// Temporarily set the RequestURI on the underlying fasthttp.RequestCtx.
		// The fileServerHandler uses this URI to determine which file to serve relative to its Root.
		c.Ctx.Request.SetRequestURI(pathForFasthttpFS)

		// Delegate the request handling to fasthttp's efficient file server.
		fileServerHandler(c.Ctx)

		// The fasthttp.FS handler manages the entire response.
		// Thus, the Xylium handler should return nil, indicating it has completed.
		return nil
	})
}

// --- Route Grouping ---

// RouteGroup allows for organizing routes under a common URL path prefix
// and/or with a shared set of middleware. This helps in structuring larger applications.
type RouteGroup struct {
	router     *Router      // Reference to the main Xylium Router instance.
	prefix     string       // The URL path prefix for all routes within this group (e.g., "/api/v1").
	middleware []Middleware // Middleware functions specific to this group.
}

// Group creates a new RouteGroup nested under the main Router.
// All routes added to this group will be automatically prefixed with the `urlPrefix`.
// Any provided `middlewares` will be applied to all routes within this group,
// after global router middleware and before any route-specific middleware.
//
// Parameters:
//   - urlPrefix: The common URL path prefix for this group (e.g., "api/v1", "/admin").
//                Leading/trailing slashes are handled. An empty or "/" prefix means the group is at the root.
//   - middlewares: Optional middleware to apply to all routes in this group.
//
// Returns:
//   - *RouteGroup: A pointer to the newly created RouteGroup.
func (r *Router) Group(urlPrefix string, middlewares ...Middleware) *RouteGroup {
	// Normalize the prefix: should start with '/' if not empty, and generally not end with '/',
	// unless the prefix itself is just "/" (which is treated as an empty prefix for joining logic).
	normalizedPrefix := ""
	if urlPrefix != "" && urlPrefix != "/" {
		normalizedPrefix = "/" + strings.Trim(urlPrefix, "/")
	} // If urlPrefix is "/" or "" (empty), normalizedPrefix remains "", effectively the root for path concatenation.

	// Copy the provided middleware slice to ensure the group has its own independent slice.
	groupMiddleware := make([]Middleware, 0, len(middlewares)) // Pre-allocate with capacity.
	groupMiddleware = append(groupMiddleware, middlewares...)

	return &RouteGroup{
		router:     r,                        // Link back to the main router instance.
		prefix:     normalizedPrefix,         // Store the normalized prefix.
		middleware: groupMiddleware,          // Store group-specific middleware.
	}
}

// Use adds one or more middleware functions to the RouteGroup's middleware chain.
// This middleware is applied *after* any global router middleware and *after* any middleware
// from parent groups (if this is a nested group), but *before* any middleware defined
// specifically for an individual route within this group.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute is an internal helper for RouteGroup to register a route.
// It combines the group's prefix with the route's relativePath and applies
// the combined middleware (group's own middleware + route-specific middleware)
// when adding the route to the main router's radix tree.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	// Normalize the relativePath for joining with the group's prefix.
	// A relativePath of "/" or "" means the route is at the group's prefix.
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} else if relativePath == "/" && rg.prefix == "" {
		// If group prefix is root ("") and relativePath is "/", the full path is just "/".
		pathSegment = "/"
	} // If relativePath is "/" and group prefix is not "", pathSegment remains effectively empty;
	  // the full path will become just the group's prefix (e.g., group "/api" + GET "/" -> "/api").

	// Combine the group's prefix with the normalized path segment.
	fullPath := rg.prefix + pathSegment
	if fullPath == "" {
		// Handles cases like Router.Group("") with GET("/") or Router.Group("/") with GET("").
		fullPath = "/"
	}
	// Further path cleaning (like duplicate slashes or trailing slashes if not root)
	// is handled by the tree's Add method and its use of splitPathOptimized.

	// Combine group-specific middleware with route-specific middleware.
	// Group middleware are applied first (outer stack), then route-specific middleware (inner stack).
	allApplicableMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allApplicableMiddleware = append(allApplicableMiddleware, rg.middleware...) // Group's own middleware.
	allApplicableMiddleware = append(allApplicableMiddleware, middlewares...)   // Route's specific middleware.

	// Register the fully constructed route (path and combined middleware) with the main router.
	rg.router.addRoute(method, fullPath, handler, allApplicableMiddleware...)
}

// GET registers an HTTP GET route within the RouteGroup.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodGet, relativePath, handler, middlewares...)
}

// POST registers an HTTP POST route within the RouteGroup.
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPost, relativePath, handler, middlewares...)
}

// PUT registers an HTTP PUT route within the RouteGroup.
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPut, relativePath, handler, middlewares...)
}

// DELETE registers an HTTP DELETE route within the RouteGroup.
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodDelete, relativePath, handler, middlewares...)
}

// PATCH registers an HTTP PATCH route within the RouteGroup.
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPatch, relativePath, handler, middlewares...)
}

// HEAD registers an HTTP HEAD route within the RouteGroup.
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodHead, relativePath, handler, middlewares...)
}

// OPTIONS registers an HTTP OPTIONS route within the RouteGroup.
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodOptions, relativePath, handler, middlewares...)
}

// Group creates a new sub-RouteGroup nested under an existing RouteGroup.
// The sub-group inherits the URL prefix and middleware from its parent group.
// Any middleware provided to this call will be appended to the inherited middleware stack.
//
// Parameters:
//   - relativePathPrefix: The path prefix for this sub-group, relative to the parent group's prefix.
//                         (e.g., if parent is "/api", and this is "users", new prefix is "/api/users").
//   - middlewares: Optional middleware to apply specifically to this sub-group and its children.
//
// Returns:
//   - *RouteGroup: A pointer to the newly created sub-RouteGroup.
func (rg *RouteGroup) Group(relativePathPrefix string, middlewares ...Middleware) *RouteGroup {
	// Normalize the path segment for the sub-group, relative to the parent group.
	pathSegment := ""
	if relativePathPrefix != "" && relativePathPrefix != "/" {
		pathSegment = "/" + strings.Trim(relativePathPrefix, "/")
	} // If relativePathPrefix is "/" or "", pathSegment remains empty;
	  // the new sub-group's prefix effectively starts where the parent's prefix ends.

	// Construct the full prefix for the new sub-group by appending to the parent's prefix.
	newFullPrefix := rg.prefix + pathSegment
	if newFullPrefix == "" { // If parent prefix and new segment are both effectively empty.
		newFullPrefix = "/"
	}
	// Final cleaning (e.g., duplicate slashes) handled by tree.Add.

	// Combine the parent group's middleware with the middleware provided for this new sub-group.
	// Parent's middleware are applied first, then the sub-group's own middleware.
	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...) // Inherit parent's middleware.
	combinedMiddleware = append(combinedMiddleware, middlewares...)   // Add new middleware for this sub-group.

	return &RouteGroup{
		router:     rg.router,          // The main Xylium Router instance remains the same.
		prefix:     newFullPrefix,      // The fully resolved and normalized prefix for the sub-group.
		middleware: combinedMiddleware, // The combined middleware stack for the sub-group.
	}
}
