// src/xylium/middleware_requestid.go
package xylium

import (
	"github.com/google/uuid" // For generating UUIDs as default request IDs.
)

// DefaultRequestIDHeader is the default HTTP header name used for request IDs.
const DefaultRequestIDHeader = "X-Request-ID"

// ContextKeyRequestID IS REMOVED FROM HERE. It's now defined in types.go.
// const ContextKeyRequestID string = "xylium_request_id" // REMOVE THIS LINE

// RequestIDConfig defines the configuration options for the RequestID middleware.
type RequestIDConfig struct {
	Generator  func() string
	HeaderName string
}

// RequestID returns a new RequestID middleware with default configuration.
func RequestID() Middleware {
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a new RequestID middleware with the provided configuration.
func RequestIDWithConfig(config RequestIDConfig) Middleware {
	if config.Generator == nil {
		config.Generator = func() string {
			return uuid.NewString()
		}
	}
	if config.HeaderName == "" {
		config.HeaderName = DefaultRequestIDHeader
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			requestID := c.Header(config.HeaderName)
			if requestID == "" {
				requestID = config.Generator()
			}

			// Use the globally defined ContextKeyRequestID from types.go (implicitly, as it's in the same package)
			c.Set(ContextKeyRequestID, requestID)
			c.SetHeader(config.HeaderName, requestID)

			return next(c)
		}
	}
}
