package xylium

import (
	"context" // For Go's context.Context, used in initializing c.goCtx.
	"sync"    // For sync.Pool, used for pooling Context objects.

	"github.com/valyala/fasthttp" // For fasthttp.RequestCtx, the underlying request context.
)

// ctxPool is a sync.Pool for Xylium Context objects.
// Using a pool significantly reduces memory allocations and garbage collection overhead
// by reusing Context instances across multiple requests. This is a common performance
// optimization in high-performance web frameworks.
var ctxPool = sync.Pool{
	// New is called by the pool when it needs to create a new Context instance
	// (e.g., if the pool is empty or all existing instances are in use).
	New: func() interface{} {
		// Initialize fields that need to be non-nil and have a consistent fresh state
		// when a Context is newly created. The `c.reset()` method will further ensure
		// other fields are cleared or reset when a Context is released back to the pool.
		return &Context{
			// Params (path parameters) and store (request-scoped key-value data)
			// are initialized as empty maps. Their contents will be cleared by `c.reset()`,
			// but the maps themselves are reused to avoid re-allocation.
			Params: make(map[string]string),
			store:  make(map[string]interface{}),

			// index for handler chain execution is initialized to -1.
			index: -1,

			// Other fields like `handlers` (slice), `router` (pointer),
			// `Ctx` (fasthttp.RequestCtx), `queryArgs`, `formArgs`, `goCtx` (Go context),
			// and `responseOnce` (sync.Once) are either:
			//  - Set during `acquireCtx` (e.g., `Ctx`, `goCtx`, `router` by Router.Handler).
			//  - Lazily initialized (e.g., `queryArgs`, `formArgs`).
			//  - Reset thoroughly in `c.reset()` (e.g., `handlers` slice to [:0], `responseOnce` to new).
		}
	},
}

// acquireCtx retrieves a `Context` instance from the `ctxPool`.
// It then initializes the `Context` with the provided `fasthttp.RequestCtx` from
// the current HTTP request and sets up its initial Go `context.Context`.
// The `Router` reference (`c.router`) is typically set by the `Router.Handler` method
// shortly after this function is called.
// This function is called at the beginning of each request processing cycle.
func acquireCtx(originalFasthttpCtx *fasthttp.RequestCtx) *Context {
	// Get an existing Context from the pool or create a new one via ctxPool.New.
	c := ctxPool.Get().(*Context)

	// Associate the underlying fasthttp context with this Xylium Context.
	c.Ctx = originalFasthttpCtx

	// Initialize the Go context.Context for this Xylium.Context.
	// By default, it starts with context.Background().
	// We check fasthttp's UserValue for a "parent_context" key. This allows Xylium
	// to chain its context if an outer layer (e.g., another fasthttp middleware
	// not part of Xylium, or a server managing contexts) has already set a Go context there.
	var parentGoCtx context.Context
	if uv := originalFasthttpCtx.UserValue("parent_context"); uv != nil {
		if pCtx, ok := uv.(context.Context); ok {
			parentGoCtx = pCtx // Use the context found in UserValue as the parent.
		}
	}

	if parentGoCtx == nil {
		// If no "parent_context" was found or it wasn't a valid context.Context,
		// default to context.Background(). This ensures c.goCtx is always non-nil
		// and provides a root context for the request lifecycle within Xylium.
		parentGoCtx = context.Background()
	}
	c.goCtx = parentGoCtx // Set the initial Go context for this Xylium.Context.

	// Note: The `c.router` field is not set here. It is the responsibility of the
	// calling code (typically `Router.Handler`) to set the router reference on the
	// acquired context (using `c.setRouter(r)`).
	// Other fields like Params, store, index should have been prepared by `c.reset()`
	// from the previous use, or initialized by `ctxPool.New` if it's a brand new context.
	return c
}

// releaseCtx resets the provided `Context` instance and returns it to the `ctxPool`.
// Resetting involves clearing all request-specific data (see `Context.reset()`)
// to ensure the Context is clean and ready for reuse by another request,
// preventing data leakage between requests.
// This function is called at the end of each request processing cycle, typically
// in a defer statement in `Router.Handler`.
func releaseCtx(c *Context) {
	c.reset()      // Call the Context's reset method to clear its state.
	ctxPool.Put(c) // Return the cleaned Context instance to the pool.
}
