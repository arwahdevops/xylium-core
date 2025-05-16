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

	// responseOnce ensures that certain response-related initializations
	// (like setting a default Content-Type) happen only once.
	responseOnce sync.Once
}

// reset prepares the Context instance for reuse.
func (c *Context) reset() {
	c.Ctx = nil
	// Clear Params map
	for k := range c.Params {
		delete(c.Params, k)
	}
	// Reset handlers slice without reallocating if capacity is sufficient
	c.handlers = c.handlers[:0]
	c.index = -1
	// Clear store map
	for k := range c.store {
		delete(c.store, k)
	}
	c.router = nil
	c.queryArgs = nil // Will be re-populated from Ctx if needed
	c.formArgs = nil  // Will be re-populated from Ctx if needed
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
// This method relies on fasthttp's internal state.
func (c *Context) ResponseCommitted() bool {
	if c.Ctx == nil {
		return false
	}
	// According to fasthttp v1.62.0 documentation,
	// ResponseWritten() is a method of *fasthttp.RequestCtx.
	return c.Ctx.ResponseWritten()
}
