package xylium

import (
	"strconv" // For converting MaxAge int to string.
	"strings" // For string joining and manipulation.
)

// CORSConfig defines the configuration for the CORS (Cross-Origin Resource Sharing) middleware.
// CORS is a mechanism that uses additional HTTP headers to tell browsers to give a web
// application running at one origin, access to selected resources from a different origin.
type CORSConfig struct {
	// AllowOrigins specifies a list of origins that are allowed to make cross-site requests.
	// An origin is typically a scheme, host, and port (e.g., "https://example.com:8080").
	// - A value of `[]string{"*"}` allows all origins. Use with caution, especially if `AllowCredentials` is true,
	//   as browsers will not permit `Access-Control-Allow-Origin: *` with credentials. In such cases,
	//   the specific origin must be reflected.
	// - Specific origins: `[]string{"https://mydomain.com", "http://localhost:3000"}`.
	// Default: `[]string{"*"}` (defined in DefaultCORSConfig).
	AllowOrigins []string

	// AllowMethods specifies a list of HTTP methods (e.g., "GET", "POST") that are allowed
	// when accessing the resource from a different origin.
	// This is primarily used in the "Access-Control-Allow-Methods" header for preflight requests.
	// Default: `[]string{MethodGet, MethodPost, MethodPut, MethodDelete, MethodOptions, MethodHead, MethodPatch}`.
	AllowMethods []string

	// AllowHeaders specifies a list of HTTP headers that can be used when making the actual request
	// from a different origin.
	// This is primarily used in the "Access-Control-Allow-Headers" header for preflight requests.
	// Default: `[]string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", DefaultRequestIDHeader}`.
	AllowHeaders []string

	// ExposeHeaders specifies a list of response headers (other than simple response headers)
	// that browsers are allowed to access from a cross-origin response.
	// Used in the "Access-Control-Expose-Headers" header.
	// Default: `[]string{DefaultRequestIDHeader}` (to expose Xylium's request ID).
	ExposeHeaders []string

	// AllowCredentials indicates whether the response to the request can be exposed to the
	// frontend JavaScript code when the request's credentials mode (e.g., `fetch`'s `credentials` option)
	// is 'include'. When true, the "Access-Control-Allow-Credentials" header is set to "true".
	// If true, `AllowOrigins` cannot be `[]string{"*"}` for the ACAO header; the specific
	// requesting origin must be explicitly allowed and reflected in ACAO.
	// Default: false.
	AllowCredentials bool

	// MaxAge indicates how long (in seconds) the results of a preflight request (OPTIONS)
	// can be cached by the browser. A value of 0 means no caching.
	// Used in the "Access-Control-Max-Age" header.
	// Default: 0 (no caching of preflight requests).
	MaxAge int
}

// DefaultCORSConfig provides a common, relatively permissive default configuration for CORS.
// It's highly recommended to tailor this configuration to specific security requirements
// for production environments, especially regarding `AllowOrigins` and `AllowCredentials`.
var DefaultCORSConfig = CORSConfig{
	AllowOrigins:     []string{"*"}, // Allows all origins by default.
	AllowMethods:     []string{MethodGet, MethodPost, MethodPut, MethodDelete, MethodOptions, MethodHead, MethodPatch},
	AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", DefaultRequestIDHeader},
	ExposeHeaders:    []string{DefaultRequestIDHeader}, // Expose Xylium's request ID header by default.
	AllowCredentials: false,
	MaxAge:           0, // No preflight caching by default.
}

// CORS returns a new CORS middleware with the default configuration (DefaultCORSConfig).
func CORS() Middleware {
	return CORSWithConfig(DefaultCORSConfig)
}

// CORSWithConfig returns a new CORS middleware with the provided custom configuration.
// It normalizes the configuration (applying defaults for unspecified fields) and sets up
// the logic for handling CORS headers for both preflight (OPTIONS) and actual requests.
func CORSWithConfig(config CORSConfig) Middleware {
	// --- Normalize and Prepare Configuration ---
	// If a field in the provided config is empty or zero (where applicable),
	// use the corresponding value from DefaultCORSConfig.
	if len(config.AllowOrigins) == 0 {
		config.AllowOrigins = DefaultCORSConfig.AllowOrigins
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = DefaultCORSConfig.AllowMethods
	}
	if len(config.AllowHeaders) == 0 {
		config.AllowHeaders = DefaultCORSConfig.AllowHeaders
	}
	// ExposeHeaders can legitimately be empty. Only default if user provides empty AND default has items.
	// For this setup, if user explicitly passes empty `ExposeHeaders`, it remains empty.
	// If `config.ExposeHeaders` was initially nil (e.g. from an uninitialized struct field),
	// and `DefaultCORSConfig.ExposeHeaders` is not empty, it would be good to apply default.
	// However, given Go's zero-value for slices is nil, not empty, this is usually fine.
	// The current behavior is: if `config.ExposeHeaders` is empty after user input, it stays empty.
	// If `DefaultCORSConfig` is the base, then its `ExposeHeaders` will be used.

	// Pre-compile header values by joining slices into comma-separated strings for efficiency.
	// These strings are sent in response headers.
	allowMethodsStr := strings.Join(config.AllowMethods, ", ")
	allowHeadersStr := strings.Join(config.AllowHeaders, ", ")
	exposeHeadersStr := strings.Join(config.ExposeHeaders, ", ")
	maxAgeStr := strconv.Itoa(config.MaxAge) // Convert MaxAge (int) to string.

	// --- The Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger()                // Get request-scoped logger for contextual logging.
			requestOrigin := c.Header("Origin") // Get the Origin header from the incoming request.

			// If there's no Origin header, it's typically not a CORS request (or a non-browser client).
			// In such cases, proceed to the next handler without adding CORS headers.
			if requestOrigin == "" {
				logger.Debugf("CORS: No 'Origin' header found. Not a CORS request, skipping CORS processing for %s %s.", c.Method(), c.Path())
				return next(c)
			}

			logger.Debugf("CORS: Processing request from Origin '%s' for %s %s.", requestOrigin, c.Method(), c.Path())

			// --- Determine Allowed Origin for Access-Control-Allow-Origin (ACAO) header ---
			var allowedOriginValue = "" // The origin string to set in the ACAO header.

			// Check if wildcard "*" is configured in AllowOrigins.
			isWildcardConfigured := false
			for _, o := range config.AllowOrigins {
				if o == "*" {
					isWildcardConfigured = true
					break
				}
			}

			if isWildcardConfigured {
				// If "*" is allowed AND credentials are NOT required, then ACAO can be "*".
				// Browsers do not allow "Access-Control-Allow-Origin: *" if credentials are true.
				if !config.AllowCredentials {
					allowedOriginValue = "*"
					logger.Debugf("CORS: Wildcard origin '*' configured and credentials NOT required. Setting ACAO to '*'.")
				} else {
					// If "*" is configured, but credentials ARE required, ACAO cannot be "*".
					// The request's Origin header must specifically match one of the other listed origins
					// (or be the only origin if "*" was the only one).
					// In this scenario, we must reflect the specific requestOrigin if it's allowed.
					// We fall through to check for an exact match of requestOrigin.
					logger.Debugf("CORS: Wildcard origin '*' configured, but credentials ARE required. ACAO '*' cannot be used. Checking for exact origin match for '%s'.", requestOrigin)
					// Try to match requestOrigin explicitly even if "*" is present, because with credentials, "*" is invalid.
					for _, o := range config.AllowOrigins {
						if o == requestOrigin {
							allowedOriginValue = requestOrigin // Exact match found.
							logger.Debugf("CORS: Origin '%s' explicitly matches configured allowed origin. Setting ACAO to '%s' (credentials required).", requestOrigin, allowedOriginValue)
							break
						}
					}
					// If no exact match and only "*" was in AllowOrigins with credentials true, then allowedOriginValue remains empty.
				}
			} else {
				// If wildcard "*" is NOT configured, check for an exact match of requestOrigin.
				for _, o := range config.AllowOrigins {
					if o == requestOrigin {
						allowedOriginValue = requestOrigin // Exact match found.
						logger.Debugf("CORS: Origin '%s' matches configured allowed origin. Setting ACAO to '%s'.", requestOrigin, allowedOriginValue)
						break
					}
				}
			}

			// If no allowed origin could be determined (neither wildcard nor exact match was suitable),
			// then this origin is not permitted. For security, do not send any ACAO header.
			// The browser will then block the cross-origin request.
			if allowedOriginValue == "" {
				logger.Warnf("CORS: Origin '%s' is not in the allowed list (%v) or incompatible with AllowCredentials. Denying CORS request for %s %s by not setting ACAO header.",
					requestOrigin, config.AllowOrigins, c.Method(), c.Path())
				// It's important *not* to set ACAO. Proceeding to the next handler
				// allows the actual resource handler to run, but the browser will block
				// the response from being read by the cross-origin script if ACAO is missing/mismatched.
				// Setting "Vary: Origin" is still good practice, as it informs caches that
				// the response might vary based on the Origin header, even if this request is denied CORS.
				c.SetHeader("Vary", "Origin")
				return next(c)
			}

			// --- Handle Preflight (OPTIONS) Requests ---
			// Preflight requests are sent by browsers to check CORS permissions before sending the actual request.
			if c.Method() == MethodOptions {
				logger.Debugf("CORS: Handling preflight (OPTIONS) request for Origin '%s', Path %s.", requestOrigin, c.Path())

				// Set ACAO header for preflight.
				c.SetHeader("Access-Control-Allow-Origin", allowedOriginValue)

				// The "Vary" header is crucial for caching proxies.
				// The response to an OPTIONS request can vary based on these request headers.
				c.SetHeader("Vary", "Origin") // Always vary by Origin.
				// If Access-Control-Request-Method is present in the request, vary by it too.
				if c.Header("Access-Control-Request-Method") != "" {
					c.Ctx.Response.Header.Add("Vary", "Access-Control-Request-Method") // Use Add for multi-value headers.
				}
				// If Access-Control-Request-Headers is present, vary by it.
				if c.Header("Access-Control-Request-Headers") != "" {
					c.Ctx.Response.Header.Add("Vary", "Access-Control-Request-Headers")
				}

				// Set "Access-Control-Allow-Methods" with the configured methods.
				c.SetHeader("Access-Control-Allow-Methods", allowMethodsStr)
				logger.Debugf("CORS: Preflight: Setting ACAM (Allow-Methods) to: '%s'", allowMethodsStr)

				// Set "Access-Control-Allow-Headers" with the configured headers.
				c.SetHeader("Access-Control-Allow-Headers", allowHeadersStr)
				logger.Debugf("CORS: Preflight: Setting ACAH (Allow-Headers) to: '%s'", allowHeadersStr)

				// Handle "Access-Control-Allow-Credentials".
				if config.AllowCredentials {
					c.SetHeader("Access-Control-Allow-Credentials", "true")
					logger.Debugf("CORS: Preflight: Setting ACAC (Allow-Credentials) to 'true'.")
				}

				// Handle "Access-Control-Max-Age" for preflight response caching.
				if config.MaxAge > 0 {
					c.SetHeader("Access-Control-Max-Age", maxAgeStr)
					logger.Debugf("CORS: Preflight: Setting ACMA (Max-Age) to '%s' seconds.", maxAgeStr)
				}

				// For preflight (OPTIONS) requests, respond with 204 No Content and terminate the chain.
				// This indicates the preflight check is successful.
				return c.NoContent(StatusNoContent) // Using Xylium's StatusNoContent.
			}

			// --- Handle Actual (Non-OPTIONS) CORS Requests ---
			logger.Debugf("CORS: Handling actual (%s) request for Origin '%s', Path %s.", c.Method(), requestOrigin, c.Path())

			// Set "Access-Control-Allow-Origin" for the actual request's response.
			c.SetHeader("Access-Control-Allow-Origin", allowedOriginValue)
			// Always set "Vary: Origin" for actual requests too, as the ACAO header might change.
			c.SetHeader("Vary", "Origin")

			// Set "Access-Control-Allow-Credentials" if configured.
			if config.AllowCredentials {
				c.SetHeader("Access-Control-Allow-Credentials", "true")
				logger.Debugf("CORS: Actual: Setting ACAC (Allow-Credentials) to 'true'.")
			}

			// Set "Access-Control-Expose-Headers" if configured, so client JS can access them.
			if len(config.ExposeHeaders) > 0 {
				c.SetHeader("Access-Control-Expose-Headers", exposeHeadersStr)
				logger.Debugf("CORS: Actual: Setting ACEH (Expose-Headers) to: '%s'", exposeHeadersStr)
			}

			// Proceed to the next handler in the chain for the actual resource.
			return next(c)
		}
	}
}
