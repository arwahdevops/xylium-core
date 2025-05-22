// File: src/xylium/testing_helpers.go
package xylium

// File ini berisi fungsi-fungsi helper yang dimaksudkan untuk digunakan
// HANYA dalam unit test internal dari package xylium.
// Fungsi-fungsi ini mungkin mengakses field atau metode yang tidak diekspor
// dari struct xylium untuk memfasilitasi pengujian.
// JANGAN DIGUNAKAN di luar konteks testing package xylium.

import (
	"github.com/valyala/fasthttp"
	"io"  // Ditambahkan untuk io.Discard
	"log" // Ditambahkan untuk membungkam log standar
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
	// yang mungkin sudah terisi.
	// `mu` dan `store` diinisialisasi seperti di pool.
	return &Context{
		Ctx:    fasthttpCtx,
		Params: params,
		store:  make(map[string]interface{}),
		mu:     new(sync.RWMutex),
		index:  -1,
		// router, handlers, queryArgs, formArgs, goCtx akan default ke nil/zero
		// kecuali jika diperlukan dan diisi secara spesifik oleh tes,
		// atau melalui helper lain seperti SetRouterForTesting.
	}
}

// SetRouterForTesting allows setting the unexported router field of a Context
// for testing purposes. This is useful for testing Context methods that depend
// on the router, such as c.Logger() or c.AppGet().
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) SetRouterForTesting(r *Router) {
	c.router = r
}

// SetHandlersForTesting allows setting the unexported handlers field and index
// for testing middleware execution logic via c.Next().
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) SetHandlersForTesting(handlers []HandlerFunc) {
	c.handlers = handlers
	c.index = -1 // Reset index for a new chain
}

// GetContextStoreForTesting provides direct, unlocked access to the context's
// internal store map.
// WARNING: This function is intended for internal testing of the xylium package only
// and bypasses the context's mutex. Use with extreme caution, primarily for
// inspection or setup where concurrent access is not a concern during the test.
// Prefer using c.Set() and c.Get() for normal store interactions in tests.
func (c *Context) GetContextStoreForTesting() map[string]interface{} {
	return c.store
}

// NewRouterForTesting creates a minimal Router instance suitable for basic testing
// where a full server setup is not required, but a Router instance is needed (e.g., for Context.Logger).
// It now temporarily silences standard Go logging during its own initialization
// to reduce noise in test outputs.
// WARNING: This function is intended for internal testing of the xylium package only.
func NewRouterForTesting() *Router {
	// Simpan output logger standar Go asli
	originalStdLogOutput := log.Writer()
	// Bungkam logger standar Go (yang dipakai xylium.SetMode dan xylium.NewWithConfig untuk bootstrap)
	log.SetOutput(io.Discard)

	// Simpan mode asli jika ada
	originalMode := Mode()
	// Set mode ke TestMode untuk mengurangi verbosity dari logger Xylium itu sendiri
	// kecuali jika mode sudah disetel secara eksplisit untuk tes tertentu.
	// Jika Anda ingin mode default (Debug) dari Xylium, komentari baris SetMode ini.
	// Namun, untuk kebanyakan unit test, TestMode lebih baik untuk logger Xylium.
	SetMode(TestMode) // Ini akan menghasilkan log "Xylium global operating mode explicitly set..." jika mode berubah

	cfg := DefaultServerConfig()
	// updateGlobalModeFromEnvOnRouterInit() dipanggil di dalam NewWithConfig
	// NewWithConfig juga akan menghasilkan log INFO tentang inisialisasi router.
	// Logger dari router ini akan menggunakan konfigurasi TestMode.
	router := NewWithConfig(cfg)

	// Kembalikan mode dan logger standar Go
	SetMode(originalMode) // Mengembalikan ke mode sebelum tes ini jika berubah
	log.SetOutput(originalStdLogOutput)

	return router
}

// FasthttpCtxGetQueryArgsForTesting directly accesses and returns the QueryArgs from fasthttp.RequestCtx.
// This can be useful if you need to manipulate or inspect fasthttp.Args directly in tests
// rather than through Xylium's c.QueryParam() abstraction, though generally c.QueryParam()
// or c.Bind() are preferred.
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) FasthttpCtxGetQueryArgsForTesting() *fasthttp.Args {
	if c.Ctx == nil {
		return nil
	}
	return c.Ctx.QueryArgs()
}

// FasthttpCtxGetPostArgsForTesting directly accesses and returns the PostArgs from fasthttp.RequestCtx.
// Useful for direct manipulation or inspection in tests.
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) FasthttpCtxGetPostArgsForTesting() *fasthttp.Args {
	if c.Ctx == nil {
		return nil
	}
	// Memastikan PostArgs diparsing jika belum
	_ = c.Ctx.PostArgs()
	return c.Ctx.PostArgs()
}

// GetParamsForTesting returns a copy of the internal Params map.
// Useful for inspecting path parameters set on the context during tests.
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) GetParamsForTesting() map[string]string {
	if c.Params == nil {
		return nil
	}
	// Return a copy to prevent external modification of the internal map
	paramsCopy := make(map[string]string, len(c.Params))
	for k, v := range c.Params {
		paramsCopy[k] = v
	}
	return paramsCopy
}

// GetContextResponseOnceForTesting exposes the responseOnce field for advanced testing scenarios,
// for instance, to check if it has been triggered or to manually trigger it if necessary
// in a controlled test environment.
// WARNING: This function is intended for internal testing of the xylium package only.
// Manipulating sync.Once directly is generally not recommended outside of careful testing.
func (c *Context) GetContextResponseOnceForTesting() *sync.Once {
	return &c.responseOnce
}
