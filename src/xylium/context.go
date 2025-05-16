package xylium

import (
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
)

var (
	defaultValidator     *validator.Validate
	defaultValidatorLock sync.RWMutex
)

func init() {
	defaultValidator = validator.New()
}

// SetCustomValidator allows replacing the default global validator instance.
func SetCustomValidator(v *validator.Validate) {
	defaultValidatorLock.Lock()
	defer defaultValidatorLock.Unlock()
	if v == nil {
		panic("xylium: validator cannot be nil")
	}
	defaultValidator = v
}

// GetValidator returns the current global validator instance.
func GetValidator() *validator.Validate {
	defaultValidatorLock.RLock()
	defer defaultValidatorLock.RUnlock()
	return defaultValidator
}

// Context represents the context of the current HTTP request.
type Context struct {
	Ctx      *fasthttp.RequestCtx
	Params   map[string]string
	handlers []HandlerFunc
	index    int
	store    map[string]interface{}
	mu       sync.RWMutex
	router   *Router

	queryArgs *fasthttp.Args
	formArgs  *fasthttp.Args

	responseOnce sync.Once
}

// reset prepares the Context instance for reuse.
func (c *Context) reset() {
	c.Ctx = nil
	for k := range c.Params {
		delete(c.Params, k)
	}
	c.handlers = c.handlers[:0]
	c.index = -1
	for k := range c.store {
		delete(c.store, k)
	}
	c.router = nil
	c.queryArgs = nil
	c.formArgs = nil
	c.responseOnce = sync.Once{}
}

// Next executes the next handler in the middleware chain.
func (c *Context) Next() error {
	c.index++
	if c.index < len(c.handlers) {
		return c.handlers[c.index](c)
	}
	return nil
}

// setRouter associates the router with the context.
func (c *Context) setRouter(r *Router) {
	c.router = r
}

// ResponseCommitted checks if the response header has already been sent.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}

	// PENGGANTI SEMENTARA (kurang akurat tapi membuat kompilasi jalan):
	// Coba kembalikan ke c.Ctx.ResponseWritten() jika Anda sudah menjalankan
	// langkah diagnosis fasthttp versi dan go clean.
	// return c.Ctx.ResponseWritten()

	// Logika pengganti sementara yang lebih baik:
	sc := c.Ctx.Response.StatusCode()
	bodyWritten := len(c.Ctx.Response.Body()) > 0
	headerContentLengthSet := c.Ctx.Response.Header.ContentLength() >= 0

	// PERBAIKAN SINTAKS: Kondisi untuk SwitchingProtocols diperbaiki.
	// Jika status code adalah 0 (default fasthttp sebelum diset), itu belum committed.
	// Jika sc adalah SwitchingProtocols, ResponseWritten() akan false, jadi kita tiru itu.
	if sc == fasthttp.StatusSwitchingProtocols {
		return false // Dalam kasus 101, ResponseWritten() biasanya false meskipun header awal sudah terkirim.
	}
	// Untuk status lain, anggap "committed" jika status sudah diset (bukan 0),
	// atau body ditulis, atau Content-Length diset.
	return sc != 0 || bodyWritten || headerContentLengthSet
}
