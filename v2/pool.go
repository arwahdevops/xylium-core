package xylium

import (
	"sync"

	"github.com/valyala/fasthttp"
)

var ctxPool = sync.Pool{
	New: func() interface{} {
		// Initialize fields that need to be non-nil when a Context is newly created by the pool.
		// Maps and slices will be made here or reset appropriately in c.reset().
		return &Context{
			Params: make(map[string]string),       // Always create a new map for params
			store:  make(map[string]interface{}), // Always create a new map for store
			index:  -1,
			// handlers slice will be reset to [:0] in c.reset()
			// router will be set by acquireCtx or Router.Handler
			// queryArgs and formArgs will be set on first access or from Ctx
			// responseOnce will be reset in c.reset()
		}
	},
}

// acquireCtx retrieves a Context from the pool and initializes it
// with the fasthttp.RequestCtx.
// The Router reference will be set by the Router.Handler after Context acquisition.
func acquireCtx(originalCtx *fasthttp.RequestCtx) *Context {
	c := ctxPool.Get().(*Context)
	c.Ctx = originalCtx // Set the actual fasthttp context
	// c.router will be set by the caller (Router.Handler)
	return c
}

// releaseCtx resets the Context and returns it to the pool for reuse.
func releaseCtx(c *Context) {
	c.reset() // Call the reset method on Context
	ctxPool.Put(c)
}
