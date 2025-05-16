package xylium

// HandlerFunc defines the function signature for request handlers and middleware.
// It returns an error, which if non-nil, will be processed by the global error handler.
type HandlerFunc func(*Context) error

// Middleware defines the function signature for middleware.
// It takes the next HandlerFunc and returns a new HandlerFunc that typically calls next.
type Middleware func(next HandlerFunc) HandlerFunc

// Logger is a generic logging interface.
// Framework users can provide their own implementation (e.g., stdlib log.Logger, or fasthttp.Logger).
type Logger interface {
	Printf(format string, args ...interface{})
}
