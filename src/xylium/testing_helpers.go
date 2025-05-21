package xylium

// Ini adalah file untuk helper yang hanya digunakan untuk testing.

import (
	"github.com/valyala/fasthttp"
	"sync"
)

// NewContextForTest creates a new Context instance for testing purposes.
// It allows setting unexported fields that would normally be set by the router or pool.
// WARNING: This function is intended for internal testing of the xylium package only
// and its signature or behavior might change without notice.
func NewContextForTest(params map[string]string, fasthttpCtx *fasthttp.RequestCtx) *Context {
	if fasthttpCtx == nil {
		// Sediakan fasthttp.RequestCtx minimal jika nil,
		// karena beberapa metode konteks mungkin mengaksesnya.
		fasthttpCtx = &fasthttp.RequestCtx{}
	}
	if params == nil {
		params = make(map[string]string)
	}
	// Inisialisasi mirip dengan yang ada di pool.go New, tapi dengan params
	return &Context{
		Ctx:    fasthttpCtx,
		Params: params, // Mengisi field yang tidak diekspor `Params`
		store:  make(map[string]interface{}),
		mu:     new(sync.RWMutex),
		index:  -1,
		// router, handlers, queryArgs, formArgs, goCtx akan default ke nil/zero
		// kecuali jika diperlukan dan diisi secara spesifik oleh tes.
	}
}

// NewContextWithRouterForTest adalah varian yang juga menerima router,
// berguna untuk menguji metode seperti c.Logger() atau c.AppGet()
func NewContextWithRouterForTest(params map[string]string, fasthttpCtx *fasthttp.RequestCtx, router *Router) *Context {
	ctx := NewContextForTest(params, fasthttpCtx)
	ctx.router = router // Mengisi field yang tidak diekspor `router`
	return ctx
}
