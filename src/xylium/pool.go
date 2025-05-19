package xylium

import (
	"context" // For Go's context.Context
	"sync"    // For sync.Pool and sync.RWMutex

	"github.com/valyala/fasthttp" // For fasthttp.RequestCtx
)

// ctxPool is a sync.Pool for Xylium Context objects.
// Reusing Context instances reduces memory allocations and GC overhead.
var ctxPool = sync.Pool{
	// New is called by the pool when it needs to create a new Context instance.
	New: func() interface{} {
		// Initialize a new Context for the pool.
		// `mu` is initialized as a pointer to a new RWMutex instance.
		// `store` and `Params` are initialized as new, empty maps.
		return &Context{
			Params: make(map[string]string),
			store:  make(map[string]interface{}),
			mu:     new(sync.RWMutex), // Initialize as a pointer to a new RWMutex
			index:  -1,
			// Other fields (handlers, router, Ctx, queryArgs, formArgs, goCtx)
			// will be set or reset by acquireCtx and/or Context.reset().
			// responseOnce is a zero-value (sync.Once{}) by default, which is correct.
		}
	},
}

// acquireCtx retrieves a `Context` instance from the `ctxPool`.
// It initializes the `Context` with the `fasthttp.RequestCtx` and sets up
// its initial Go `context.Context`.
func acquireCtx(originalFasthttpCtx *fasthttp.RequestCtx) *Context {
	// Get an existing Context from the pool or create a new one via ctxPool.New.
	c := ctxPool.Get().(*Context)

	// Associate the underlying fasthttp context.
	c.Ctx = originalFasthttpCtx

	// Initialize the Go context.Context for this request.
	// Check fasthttp's UserValue for a "parent_context" for potential chaining.
	var parentGoCtx context.Context
	parentGoCtxUserValue := originalFasthttpCtx.UserValue("parent_context")
	if parentGoCtxUserValue != nil {
		if pCtx, ok := parentGoCtxUserValue.(context.Context); ok {
			parentGoCtx = pCtx // Use the context found in UserValue as the parent.
		}
	}

	if parentGoCtx == nil {
		// Default to context.Background() if no parent_context was found or it was invalid.
		parentGoCtx = context.Background()
	}
	c.goCtx = parentGoCtx // Set the initial Go context.

	// `c.mu`, `c.store`, and `c.Params` are guaranteed to be non-nil and initialized
	// by the `ctxPool.New` function if this is a new object, or correctly
	// reset by `c.reset()` if it's a reused object.
	// `c.router` will be set by `Router.Handler` shortly after this.

	return c
}

// releaseCtx resets the provided `Context` and returns it to the `ctxPool`.
func releaseCtx(c *Context) {
	c.reset()      // Call the Context's reset method to clear its state.
	ctxPool.Put(c) // Return the cleaned Context instance to the pool.
}
