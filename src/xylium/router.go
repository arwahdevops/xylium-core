package xylium

import (
	"encoding/json" // Used in ServeFiles for PathNotFound JSON response
	"fmt"           // For error formatting
	"io"            // For HTMLRenderer
	"log"           // For fallback logger in NewWithConfig
	"os"            // For fallback logger in NewWithConfig
	"path/filepath" // For ServeFiles path cleaning
	"runtime/debug" // For stack trace on panic
	"strings"       // For path manipulation

	"github.com/valyala/fasthttp" // Only for fasthttp.RequestCtx type in Handler, and status codes if not aliased
)

// HTMLRenderer defines the interface for HTML template rendering.
type HTMLRenderer interface {
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router is the main router for the xylium framework.
// It holds the routing tree, middleware, and handlers.
type Router struct {
	tree             *Tree        // Radix tree for route matching
	globalMiddleware []Middleware // Middleware applied to all routes

	// Customizable handlers for various events
	PanicHandler            HandlerFunc
	NotFoundHandler         HandlerFunc
	MethodNotAllowedHandler HandlerFunc
	GlobalErrorHandler      HandlerFunc

	serverConfig ServerConfig // Server configuration (defined in router_server.go)
	HTMLRenderer HTMLRenderer // Optional HTML renderer
}

// Logger returns the configured logger for the router.
// It returns the xylium.Logger interface.
func (r *Router) Logger() Logger {
	return r.serverConfig.Logger
}

// New creates a new Router with default configuration.
func New() *Router {
	return NewWithConfig(DefaultServerConfig()) // DefaultServerConfig from router_server.go
}

// NewWithConfig creates a new Router with the given ServerConfig.
func NewWithConfig(config ServerConfig) *Router {
	routerInstance := &Router{ // Renamed to avoid conflict with receiver 'r'
		tree:             NewTree(),
		globalMiddleware: make([]Middleware, 0),
		serverConfig:     config,
	}
	// Set default handlers (defined in router_defaults.go)
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Ensure logger is never nil
	if routerInstance.serverConfig.Logger == nil {
		routerInstance.serverConfig.Logger = log.New(os.Stderr, "[xyliumSrvFallbackInit] ", log.LstdFlags)
	}
	return routerInstance
}

// Use adds global middleware to the router.
// These are applied before any group or route-specific middleware.
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// addRoute is an internal method to register a new route.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		panic("xylium: path must begin with '/'")
	}
	r.tree.Add(method, path, handler, middlewares...)
}

// HTTP method helper functions to add routes.
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodGet, path, handler, middlewares...)
}
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPost, path, handler, middlewares...)
}
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPut, path, handler, middlewares...)
}
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodDelete, path, handler, middlewares...)
}
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPatch, path, handler, middlewares...)
}
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodHead, path, handler, middlewares...)
}
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodOptions, path, handler, middlewares...)
}

// Handler is the main fasthttp.RequestHandlerFunc for the router.
// It acquires a Context, finds the route, executes middleware and handlers,
// and manages panic recovery and error handling.
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) { // Renamed for clarity
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r)
	defer releaseCtx(c)

	var errHandler error
	currentLogger := r.Logger() // This is xylium.Logger

	defer func() {
		// Panic recovery
		if rec := recover(); rec != nil {
			currentLogger.Printf("PANIC: %v\n%s", rec, string(debug.Stack()))
			if r.PanicHandler != nil {
				c.Set("panic_recovery_info", rec)
				errHandler = r.PanicHandler(c)
			} else { // Fallback if PanicHandler is somehow nil
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic").WithInternal(fmt.Errorf("panic: %v", rec))
			}
		}

		// Global error handling for errors returned by handlers or panic recovery
		if errHandler != nil {
			if !c.ResponseCommitted() { // Only if response hasn't been sent
				if r.GlobalErrorHandler != nil {
					c.Set("handler_error_cause", errHandler)
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						currentLogger.Printf("CRITICAL: Error during global error handling: %v (original error: %v)", globalErrHandlingErr, errHandler)
						// Absolute fallback if GlobalErrorHandler itself fails
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						c.Ctx.Response.SetBodyString("Internal Server Error")
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else { // Fallback if GlobalErrorHandler is somehow nil
					currentLogger.Printf("Error (GlobalErrorHandler is nil): %v for %s %s", errHandler, c.Method(), c.Path())
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error") // Keep simple
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else { // Response already committed, just log the error
				currentLogger.Printf("Warning: Response already committed but an error was generated: %v for %s %s", errHandler, c.Method(), c.Path())
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// Log if handler chain finished without error but no response was sent (for non-HEAD requests)
			statusCode := c.Ctx.Response.StatusCode()
			if statusCode == 0 || (statusCode == StatusOK && len(c.Ctx.Response.Body()) == 0) { // StatusOK from httpstatus.go
				currentLogger.Printf("Warning: Handler chain completed for %s %s without sending a response or error.", c.Method(), c.Path())
			}
		}
	}()

	method := c.Method()
	path := c.Path()

	// Find route in the tree
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil { // Route found
		c.Params = params
		// Build the handler chain: global middleware -> route/group middleware -> main handler
		finalChain := nodeHandler
		for i := len(routeMiddleware) - 1; i >= 0; i-- { // Apply route/group middleware
			finalChain = routeMiddleware[i](finalChain)
		}
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- { // Apply global middleware
			finalChain = r.globalMiddleware[i](finalChain)
		}

		c.handlers = []HandlerFunc{finalChain} // Context's handler chain is just the final composed handler
		c.index = -1                           // Reset for c.Next()
		errHandler = c.Next()                  // Execute the chain
	} else { // Route not found
		if len(allowedMethods) > 0 { // Path exists, but method not allowed
			c.Params = params // Params might be useful for the MethodNotAllowedHandler
			if r.MethodNotAllowedHandler != nil {
				c.SetHeader("Allow", strings.Join(allowedMethods, ", "))
				errHandler = r.MethodNotAllowedHandler(c)
			} else { // Fallback
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else { // Path does not exist
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else { // Fallback
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
}

// ServeFiles serves static files from the given root directory at the specified path prefix.
// Example: r.ServeFiles("/static", "./public_html")
func (r *Router) ServeFiles(pathPrefix string, rootDir string) {
	if strings.Contains(pathPrefix, ":") || strings.Contains(pathPrefix, "*") {
		panic("xylium: pathPrefix for ServeFiles cannot contain ':' or '*'")
	}
	fileSystemRoot := filepath.Clean(rootDir)
	paramName := "filepath" // Catch-all parameter name for the file subpath

	// Normalize pathPrefix
	if pathPrefix == "/" || pathPrefix == "" {
		pathPrefix = "" // Serve from root
	} else {
		pathPrefix = "/" + strings.Trim(pathPrefix, "/")
	}

	routePath := pathPrefix + "/*" + paramName
	if pathPrefix == "" {
		routePath = "/*" + paramName
	}

	fs := &fasthttp.FS{
		Root:               fileSystemRoot,
		IndexNames:         []string{"index.html"},
		GenerateIndexPages: false, // Typically false for API backends
		AcceptByteRange:    true,
		Compress:           true,
		PathNotFound: func(ctx *fasthttp.RequestCtx) {
			he := NewHTTPError(StatusNotFound, "File not found in static assets")
			ctx.SetStatusCode(he.Code)
			ctx.SetContentType("application/json; charset=utf-8")
			//nolint:errcheck
			json.NewEncoder(ctx.Response.BodyWriter()).Encode(he.Message)
		},
	}
	fileHandler := fs.NewRequestHandler()

	r.GET(routePath, func(c *Context) error {
		requestedFileSubPath := c.Param(paramName)
		originalRequestURI := c.Ctx.Request.Header.RequestURI() // Save original URI

		pathForFS := "/" + strings.TrimPrefix(requestedFileSubPath, "/")
		c.Ctx.Request.SetRequestURI(pathForFS) // Temporarily set URI for fasthttp.FS

		fileHandler(c.Ctx) // Let fasthttp.FS handle the request

		c.Ctx.Request.SetRequestURIBytes(originalRequestURI) // Restore original URI
		return nil // fasthttp.FS handles the response
	})
}

// --- Route Grouping ---

// RouteGroup allows grouping routes with a common path prefix and middleware.
type RouteGroup struct {
	router     *Router
	prefix     string       // Path prefix for this group (e.g., "/api/v1")
	middleware []Middleware // Middleware specific to this group
}

// Group creates a new RouteGroup prefixed with the Router's path.
func (r *Router) Group(prefix string, middlewares ...Middleware) *RouteGroup {
	normalizedPrefix := ""
	if prefix != "" && prefix != "/" { // Normalize: ensure starts with '/', no trailing '/'
		normalizedPrefix = "/" + strings.Trim(prefix, "/")
	}

	groupMiddleware := make([]Middleware, len(middlewares))
	copy(groupMiddleware, middlewares) // Copy middleware to avoid external modification

	return &RouteGroup{
		router:     r,
		prefix:     normalizedPrefix,
		middleware: groupMiddleware,
	}
}

// Use adds middleware to the RouteGroup.
// Applied after router global middleware and parent group middleware, before route-specific middleware.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute is an internal helper for RouteGroup to register a route.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	pathSegment := ""
	if relativePath != "" && relativePath != "/" { // Normalize relativePath
		pathSegment = "/" + strings.Trim(relativePath, "/")
	}

	fullPath := rg.prefix + pathSegment
	if fullPath == "" { // Handle case where prefix and segment are both effectively empty
		fullPath = "/"
	}

	// Combine group middleware with route-specific middleware
	allGroupAndRouteMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, rg.middleware...)
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, middlewares...)

	rg.router.addRoute(method, fullPath, handler, allGroupAndRouteMiddleware...)
}

// HTTP method helpers for RouteGroup.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodGet, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPost, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPut, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodDelete, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPatch, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodHead, relativePath, handler, middlewares...)
}
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodOptions, relativePath, handler, middlewares...)
}

// Group creates a sub-group from an existing RouteGroup.
// Inherits prefix and middleware from the parent group.
func (rg *RouteGroup) Group(relativePath string, middlewares ...Middleware) *RouteGroup {
	pathSegment := ""
	if relativePath != "" && relativePath != "/" { // Normalize segment for sub-group
		pathSegment = "/" + strings.Trim(relativePath, "/")
	}
	newPrefix := rg.prefix + pathSegment // Combine parent prefix with new segment

	// Combine parent group middleware with new sub-group middleware
	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...) // Inherit parent's middleware
	combinedMiddleware = append(combinedMiddleware, middlewares...)   // Add new middleware

	return &RouteGroup{
		router:     rg.router, // Router instance remains the same
		prefix:     newPrefix,
		middleware: combinedMiddleware,
	}
}
