// src/xylium/middleware_cors.go
package xylium

import (
	"strconv" // For converting MaxAge int to string.
	"strings" // For string joining and manipulation.
	// "net/http" and "time" are not directly needed here anymore.
)

// CORSConfig defines the configuration for the CORS (Cross-Origin Resource Sharing) middleware.
type CORSConfig struct {
	// AllowOrigins specifies a list of origins that are allowed to make cross-site requests.
	// An origin is typically a scheme, host, and port (e.g., "https://example.com:8080").
	// - A value of `[]string{"*"}` allows all origins (use with caution, especially with credentials).
	// - Specific origins: `[]string{"https://mydomain.com", "http://localhost:3000"}`.
	// Default: `[]string{"*"}`.
	AllowOrigins []string

	// AllowMethods specifies a list of HTTP methods that are allowed when accessing the resource.
	// Used in the "Access-Control-Allow-Methods" header for preflight requests.
	// Default: `[]string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "HEAD", "PATCH"}`.
	AllowMethods []string

	// AllowHeaders specifies a list of HTTP headers that can be used when making the actual request.
	// Used in the "Access-Control-Allow-Headers" header for preflight requests.
	// Default: `[]string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"}`.
	// (Note: DefaultRequestIDHeader from Xylium might be useful to add here if clients send it).
	AllowHeaders []string

	// ExposeHeaders specifies a list of response headers that browsers are allowed to access.
	// Used in the "Access-Control-Expose-Headers" header.
	// Default: `[]string{}` (empty list).
	ExposeHeaders []string

	// AllowCredentials indicates whether the response to the request can be exposed when the credentials flag is true.
	// Used in the "Access-Control-Allow-Credentials" header.
	// If true, AllowOrigins cannot be `[]string{"*"}` (browsers enforce this); specific origins must be listed.
	// Default: false.
	AllowCredentials bool

	// MaxAge indicates how long (in seconds) the results of a preflight request (OPTIONS) can be cached.
	// A value of 0 means no caching. Used in the "Access-Control-Max-Age" header.
	// Default: 0 (no caching of preflight requests).
	MaxAge int
}

// DefaultCORSConfig provides a common, permissive default configuration for CORS.
// It's recommended to tailor this to specific security requirements for production.
var DefaultCORSConfig = CORSConfig{
	AllowOrigins:     []string{"*"}, // Allows all origins.
	AllowMethods:     []string{MethodGet, MethodPost, MethodPut, MethodDelete, MethodOptions, MethodHead, MethodPatch},
	AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", DefaultRequestIDHeader /* Consider adding Xylium's default request ID header */},
	ExposeHeaders:    []string{DefaultRequestIDHeader /* Expose Xylium's request ID header */},
	AllowCredentials: false,
	MaxAge:           0, // No preflight caching by default.
}

// CORS returns a new CORS middleware with the default configuration.
func CORS() Middleware {
	return CORSWithConfig(DefaultCORSConfig)
}

// CORSWithConfig returns a new CORS middleware with the provided custom configuration.
// It normalizes the configuration and sets up the logic for handling CORS headers.
func CORSWithConfig(config CORSConfig) Middleware {
	// --- Normalize and Prepare Configuration ---
	// If a field in the provided config is empty or zero, use the default value from DefaultCORSConfig.
	if len(config.AllowOrigins) == 0 {
		config.AllowOrigins = DefaultCORSConfig.AllowOrigins
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = DefaultCORSConfig.AllowMethods
	}
	if len(config.AllowHeaders) == 0 {
		config.AllowHeaders = DefaultCORSConfig.AllowHeaders
	}
	if len(config.ExposeHeaders) == 0 {
		// ExposeHeaders might legitimately be empty, so only default if DefaultCORSConfig has some.
		// For now, we will allow it to be empty if user provides empty.
		// If you want to always merge DefaultCORSConfig.ExposeHeaders, that's an option.
		// config.ExposeHeaders = DefaultCORSConfig.ExposeHeaders
	}
	// MaxAge default is 0 (no caching). If user specifies a value, it will be used.
	// AllowCredentials default is false. If user specifies a value, it will be used.

	// Pre-compile header values for efficiency.
	// These strings are sent in response headers.
	allowMethodsStr := strings.Join(config.AllowMethods, ", ")
	allowHeadersStr := strings.Join(config.AllowHeaders, ", ")
	exposeHeadersStr := strings.Join(config.ExposeHeaders, ", ")
	maxAgeStr := strconv.Itoa(config.MaxAge) // Convert MaxAge (int) to string.

	// --- The Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Get request-scoped logger.
			requestOrigin := c.Header("Origin") // Get the Origin header from the incoming request.

			// If there's no Origin header, it's not a CORS request (or a non-browser client).
			// Proceed to the next handler without adding CORS headers.
			if requestOrigin == "" {
				logger.Debugf("CORS: No 'Origin' header found. Not a CORS request, skipping CORS processing for %s %s.", c.Method(), c.Path())
				return next(c)
			}

			logger.Debugf("CORS: Processing request from Origin '%s' for %s %s.", requestOrigin, c.Method(), c.Path())

			// --- Determine Allowed Origin ---
			var allowedOrigin = "" // The origin to set in "Access-Control-Allow-Origin".

			// Case 1: Wildcard "*" is configured in AllowOrigins.
			isWildcardAllowed := false
			for _, o := range config.AllowOrigins {
				if o == "*" {
					isWildcardAllowed = true
					break
				}
			}

			if isWildcardAllowed {
				// If "*" is allowed AND credentials are NOT required, then "*" can be used.
				// Browsers do not allow "Access-Control-Allow-Origin: *" if credentials are true.
				if !config.AllowCredentials {
					allowedOrigin = "*"
					logger.Debugf("CORS: Wildcard origin '*' allowed and credentials not required. Setting ACAO to '*'.")
				} else {
					// If "*" is in the list but credentials ARE required, "*" cannot be used.
					// The request origin must specifically match one of the other listed origins.
					// We fall through to check for an exact match.
					logger.Debugf("CORS: Wildcard origin '*' is configured, but credentials are required. ACAO '*' cannot be used. Checking for exact origin match.")
				}
			}

			// Case 2: If wildcard wasn't used (or couldn't be used due to credentials), check for an exact match.
			if allowedOrigin == "" { // Only if not already set to "*"
				for _, o := range config.AllowOrigins {
					if o == requestOrigin {
						allowedOrigin = requestOrigin // Exact match found.
						logger.Debugf("CORS: Origin '%s' matches configured allowed origin. Setting ACAO to '%s'.", requestOrigin, allowedOrigin)
						break
					}
				}
			}

			// If no allowed origin could be determined (neither wildcard nor exact match),
			// then this origin is not permitted.
			// For security, do not send any "Access-Control-Allow-Origin" header.
			// The browser will then block the cross-origin request.
			if allowedOrigin == "" {
				logger.Warnf("CORS: Origin '%s' is not in the allowed list: %v. Denying CORS request for %s %s by not setting ACAO header.",
					requestOrigin, config.AllowOrigins, c.Method(), c.Path())
				// It's important *not* to set ACAO. Proceeding to next handler
				// allows the actual resource handler to run, but the browser will block
				// the response from being read by the cross-origin script if ACAO is missing/mismatched.
				// Some frameworks might choose to return a 403 here, but that can leak info.
				// Standard behavior is to let the request proceed and rely on browser enforcement.
				c.SetHeader("Vary", "Origin") // Still good practice to set Vary: Origin.
				return next(c)
			}

			// --- Handle Preflight (OPTIONS) Requests ---
			if c.Method() == MethodOptions {
				logger.Debugf("CORS: Handling preflight (OPTIONS) request for Origin '%s', Path %s.", requestOrigin, c.Path())

				// ACAO header.
				c.SetHeader("Access-Control-Allow-Origin", allowedOrigin)

				// Vary header is important for caching proxies.
				// Response to OPTIONS can vary based on these request headers.
				c.SetHeader("Vary", "Origin")
				if c.Header("Access-Control-Request-Method") != "" {
					c.SetHeader("Vary", "Access-Control-Request-Method") // Append, fasthttp handles multi-value
				}
				if c.Header("Access-Control-Request-Headers") != "" {
					c.SetHeader("Vary", "Access-Control-Request-Headers") // Append
				}

				// Allow configured methods.
				c.SetHeader("Access-Control-Allow-Methods", allowMethodsStr)
				logger.Debugf("CORS: Preflight: Setting ACAM to: '%s'", allowMethodsStr)

				// Allow configured headers.
				// If Access-Control-Request-Headers is present, we can reflect it if configured,
				// or just send all allowed headers. Standard practice is to send all allowed.
				c.SetHeader("Access-Control-Allow-Headers", allowHeadersStr)
				logger.Debugf("CORS: Preflight: Setting ACAH to: '%s'", allowHeadersStr)

				// Handle credentials.
				if config.AllowCredentials {
					c.SetHeader("Access-Control-Allow-Credentials", "true")
					logger.Debugf("CORS: Preflight: Setting ACAC to 'true'.")
				}

				// Handle MaxAge for preflight caching.
				if config.MaxAge > 0 {
					c.SetHeader("Access-Control-Max-Age", maxAgeStr)
					logger.Debugf("CORS: Preflight: Setting ACMA to '%s' seconds.", maxAgeStr)
				}

				// For preflight requests, we respond with 204 No Content and do not call next(c).
				// This indicates the preflight check is successful.
				return c.NoContent(StatusNoContent) // Using Xylium's StatusNoContent.
			}

			// --- Handle Actual (Non-OPTIONS) CORS Requests ---
			logger.Debugf("CORS: Handling actual (%s) request for Origin '%s', Path %s.", c.Method(), requestOrigin, c.Path())

			// Set "Access-Control-Allow-Origin" for the actual request.
			c.SetHeader("Access-Control-Allow-Origin", allowedOrigin)
			// Always set Vary: Origin for actual requests too.
			c.SetHeader("Vary", "Origin")


			// Set "Access-Control-Allow-Credentials" if configured.
			if config.AllowCredentials {
				c.SetHeader("Access-Control-Allow-Credentials", "true")
				logger.Debugf("CORS: Actual: Setting ACAC to 'true'.")
			}

			// Set "Access-Control-Expose-Headers" if configured.
			if len(config.ExposeHeaders) > 0 {
				c.SetHeader("Access-Control-Expose-Headers", exposeHeadersStr)
				logger.Debugf("CORS: Actual: Setting ACEH to: '%s'", exposeHeadersStr)
			}

			// Proceed to the next handler in the chain for the actual resource.
			return next(c)
		}
	}
}
