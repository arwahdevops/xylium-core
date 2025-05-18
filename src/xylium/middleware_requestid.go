package xylium

import (
	"github.com/google/uuid" // For generating UUIDs as default request IDs.
)

// DefaultRequestIDHeader is the default HTTP header name used for request IDs.
// Clients or upstream services (like load balancers) might provide a request ID
// using this header. If present, Xylium will use it; otherwise, a new ID is generated.
// This header is also set on the outgoing response.
// Common alternatives include "X-Correlation-ID".
const DefaultRequestIDHeader = "X-Request-ID"

// ContextKeyRequestID is the key used to store the request ID in the xylium.Context store (`c.store`).
// It's crucial that this key is consistent across the framework, especially where
// the logger (`c.Logger()`) attempts to retrieve the request ID to include it in log entries.
// Using a specific string type for the key enhances type safety if used directly,
// though string literal is common.
const ContextKeyRequestID string = "xylium_request_id" // Public constant for reliable access.

// RequestIDConfig defines the configuration options for the RequestID middleware.
// It allows customization of the request ID generation and the HTTP header used.
type RequestIDConfig struct {
	// Generator is a function that generates unique request IDs.
	// If nil, a UUID v4 generator (`uuid.NewString()`) will be used by default.
	// The generator should produce strings.
	Generator func() string

	// HeaderName is the name of the HTTP header to check for an incoming request ID
	// and to set on the outgoing response.
	// If empty, `DefaultRequestIDHeader` ("X-Request-ID") will be used.
	HeaderName string
}

// RequestID returns a new RequestID middleware with default configuration:
// - Default ID generator: UUID v4.
// - Default header name: "X-Request-ID".
// This middleware ensures that every request has a unique ID associated with it,
// which is useful for tracing, logging, and debugging across distributed systems.
func RequestID() Middleware {
	// Pass an empty RequestIDConfig to use all default values,
	// which are then applied by RequestIDWithConfig.
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a new RequestID middleware with the provided configuration.
// This allows for custom ID generation logic or different header names.
func RequestIDWithConfig(config RequestIDConfig) Middleware {
	// Apply default ID generator if none is provided in the configuration.
	if config.Generator == nil {
		config.Generator = func() string {
			return uuid.NewString() // Generate a new UUID v4 string.
		}
	}

	// Apply default header name if none is provided in the configuration.
	if config.HeaderName == "" {
		config.HeaderName = DefaultRequestIDHeader
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// 1. Attempt to retrieve an existing request ID from the incoming request's header.
			//    This allows propagation of request IDs from upstream services or clients,
			//    maintaining a consistent trace across a distributed system.
			requestID := c.Header(config.HeaderName)

			// 2. If no request ID is found in the header (i.e., it's an initial request
			//    to this service or upstream didn't provide one), generate a new ID.
			if requestID == "" {
				requestID = config.Generator()
			}

			// 3. Store the determined (or generated) request ID in the Xylium Context's store.
			//    This makes the request ID available to:
			//    - Subsequent middleware and the final route handler via `c.Get(ContextKeyRequestID)`.
			//    - Importantly, `c.Logger()`, which automatically includes this ID in log entries
			//      if `ContextKeyRequestID` is found in the store.
			c.Set(ContextKeyRequestID, requestID)

			// 4. Set the request ID in the response header.
			//    This allows the client or downstream services to correlate their logs or actions
			//    with this specific request processed by the Xylium application.
			c.SetHeader(config.HeaderName, requestID)

			// 5. Proceed to the next handler in the middleware chain or the final route handler.
			return next(c)
		}
	}
}
