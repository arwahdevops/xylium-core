// src/xylium/middleware_requestid.go
package xylium

import (
	"github.com/google/uuid" // For generating UUIDs as default request IDs.
)

// DefaultRequestIDHeader is the default HTTP header name used for request IDs.
// Common alternatives include "X-Correlation-ID".
const DefaultRequestIDHeader = "X-Request-ID"

// ContextKeyRequestID is the key used to store the request ID in the xylium.Context store.
// It's crucial that this key is consistent across the framework, especially where
// the logger (c.Logger()) attempts to retrieve it.
// Using a string type for the key.
const ContextKeyRequestID string = "xylium_request_id"

// RequestIDConfig defines the configuration options for the RequestID middleware.
type RequestIDConfig struct {
	// Generator is a function that generates unique request IDs.
	// If nil, a UUID v4 generator will be used by default.
	// The generator should produce strings.
	Generator func() string

	// HeaderName is the name of the HTTP header to check for an incoming request ID
	// and to set on the outgoing response.
	// If empty, DefaultRequestIDHeader ("X-Request-ID") will be used.
	HeaderName string
}

// RequestID returns a new RequestID middleware with default configuration.
// - Default generator: UUID v4.
// - Default header name: "X-Request-ID".
func RequestID() Middleware {
	// Pass an empty RequestIDConfig to use all default values.
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a new RequestID middleware with the provided configuration.
func RequestIDWithConfig(config RequestIDConfig) Middleware {
	// Apply default generator if none is provided.
	if config.Generator == nil {
		config.Generator = func() string {
			return uuid.NewString() // Generate a new UUID v4 string.
		}
	}

	// Apply default header name if none is provided.
	if config.HeaderName == "" {
		config.HeaderName = DefaultRequestIDHeader
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// 1. Try to get an existing request ID from the incoming request header.
			//    This allows propagation of request IDs from upstream services or clients.
			requestID := c.Header(config.HeaderName)

			// 2. If no request ID is found in the header, generate a new one.
			if requestID == "" {
				requestID = config.Generator()
			}

			// 3. Set the request ID in the Xylium Context's store.
			//    This makes it available to other middleware and handlers,
			//    and importantly, to c.Logger() for inclusion in logs.
			//    Use the globally defined ContextKeyRequestID for consistency.
			c.Set(ContextKeyRequestID, requestID)

			// 4. Set the request ID in the response header.
			//    This allows the client or downstream services to correlate logs
			//    with this specific request.
			c.SetHeader(config.HeaderName, requestID)

			// 5. Proceed to the next handler in the chain.
			return next(c)
		}
	}
}
