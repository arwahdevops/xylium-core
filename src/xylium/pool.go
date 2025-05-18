package xylium

import (
	"context" // For Go's context.Context
	"sync"    // For sync.Pool, used for pooling Context objects.

	"github.com/valyala/fasthttp" // For fasthttp.RequestCtx.
)

// ctxPool is a sync.Pool for Xylium Context objects.
// Using a pool significantly reduces memory allocations and garbage collection overhead
// by reusing Context instances across multiple requests. This is a common performance
// optimization in high-performance web frameworks.
var ctxPool = sync.Pool{
	// New is called by the pool when it needs to create a new Context instance
	// (e.g., if the pool is empty or all existing instances are in use).
	New: func() interface{} {
		// Initialize fields that need to be non-nil and have a fresh state
		// when a Context is newly created or retrieved from the pool after reset.
		// The `c.reset()` method will further ensure other fields are cleared.
		return &Context{
			// Maps should be initialized here to ensure they are not nil.
			// `c.reset()` will clear their contents but not necessarily nil them.
			Params: make(map[string]string),       // Path parameters.
			store:  make(map[string]interface{}), // Request-scoped key-value store.

			index: -1, // Initial index for handler chain execution.

			// `handlers` slice will be reset to `[:0]` in `c.reset()`, keeping capacity.
			// `router` will be set by `acquireCtx` or `Router.Handler`.
			// `Ctx` (fasthttp.RequestCtx) will be set by `acquireCtx`.
			// `queryArgs` and `formArgs` are lazily initialized or set from `Ctx`.
			// `responseOnce` (sync.Once) is reset in `c.reset()` by creating a new instance.
			// `goCtx` will be initialized in `acquireCtx` when the context is obtained
			// for a new request.
		}
	},
}

// acquireCtx retrieves a `Context` instance from the `ctxPool`.
// It then initializes the `Context` with the provided `fasthttp.RequestCtx` from
// the current HTTP request and sets up its initial Go `context.Context`.
// The `Router` reference is typically set by the `Router.Handler` method shortly
// after the context is acquired.
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
		// default to context.Background(). This ensures c.goCtx is always non-nil.
		parentGoCtx = context.Background()
	}
	c.goCtx = parentGoCtx // Set the initial Go context for this Xylium.Context.

	// The router (`c.router`) is usually set by the caller (e.g., `Router.Handler`)
	// immediately after acquiring the context, as it's needed for mode, logger, etc.
	// `c.reset()` should have already prepared other fields like Params, store, index.
	return c
}

// releaseCtx resets the provided `Context` instance and returns it to the `ctxPool`.
// Resetting involves clearing all request-specific data (see `Context.reset()`)
// to ensure the Context is clean and ready for reuse by another request,
// preventing data leakage.
// This function is called at the end of each request processing cycle, typically
// in a defer statement in `Router.Handler`.
func releaseCtx(c *Context) {
	c.reset()      // Call the Context's reset method to clear its state.
	ctxPool.Put(c) // Return the cleaned Context instance to the pool.
}
