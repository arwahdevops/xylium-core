package xylium

import (
	"strconv" // For converting MaxAge int to string.
	"strings" // For string joining and manipulation.
)

// CORSConfig defines the configuration for the CORS (Cross-Origin Resource Sharing) middleware.
type CORSConfig struct {
	// AllowOrigins specifies a list of origins that are allowed to make cross-site requests.
	// An origin is typically a scheme, host, and port (e.g., "https://example.com:8080").
	// - IMPORTANT: If this list is empty (the new default), no 'Access-Control-Allow-Origin'
	//   header will be sent, effectively denying all cross-origin requests unless a match is found
	//   against a non-empty AllowOrigins list explicitly configured by the user.
	// - To allow all origins (USE WITH EXTREME CAUTION, especially if AllowCredentials is true):
	//   set to `[]string{"*"}`. Browsers will not permit `Access-Control-Allow-Origin: *`
	//   with credentials; in such cases, the specific origin must be reflected.
	// - Specific origins: `[]string{"https://mydomain.com", "http://localhost:3000"}`.
	// Default (from DefaultCORSConfig): `[]string{}` (empty slice, more secure).
	AllowOrigins []string

	// AllowMethods specifies a list of HTTP methods (e.g., "GET", "POST") that are allowed
	// when accessing the resource from a different origin.
	AllowMethods []string

	// AllowHeaders specifies a list of HTTP headers that can be used when making the actual request
	// from a different origin.
	AllowHeaders []string

	// ExposeHeaders specifies a list of response headers (other than simple response headers)
	// that browsers are allowed to access from a cross-origin response.
	ExposeHeaders []string

	// AllowCredentials indicates whether the response to the request can be exposed to the
	// frontend JavaScript code when the request's credentials mode is 'include'.
	// If true, `AllowOrigins` cannot be `[]string{"*"}` for the ACAO header; the specific
	// requesting origin must be explicitly allowed and reflected in ACAO.
	// Default: false.
	AllowCredentials bool

	// MaxAge indicates how long (in seconds) the results of a preflight request (OPTIONS)
	// can be cached by the browser. A value of 0 means no caching.
	// Default: 0.
	MaxAge int
}

// DefaultCORSConfig provides a common default configuration for CORS.
// IT IS HIGHLY RECOMMENDED TO EXPLICITLY CONFIGURE `AllowOrigins` FOR PRODUCTION.
var DefaultCORSConfig = CORSConfig{
	// AllowOrigins default is now an empty slice, meaning no cross-origin requests
	// are allowed by default. Users MUST configure this for cross-origin functionality.
	AllowOrigins:     []string{},
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
func CORSWithConfig(config CORSConfig) Middleware {
	// Normalize and Prepare Configuration
	if len(config.AllowOrigins) == 0 { // Handles if user passes empty slice directly or if DefaultCORSConfig is used.
		// No specific action needed here for AllowOrigins, the logic below will handle it.
		// If it remains empty, no ACAO header will be set, effectively denying.
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = DefaultCORSConfig.AllowMethods
	}
	if len(config.AllowHeaders) == 0 {
		config.AllowHeaders = DefaultCORSConfig.AllowHeaders
	}
	// ExposeHeaders default handling is implicitly covered by DefaultCORSConfig base.

	allowMethodsStr := strings.Join(config.AllowMethods, ", ")
	allowHeadersStr := strings.Join(config.AllowHeaders, ", ")
	exposeHeadersStr := strings.Join(config.ExposeHeaders, ", ")
	maxAgeStr := strconv.Itoa(config.MaxAge)

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger()
			requestOrigin := c.Header("Origin")

			if requestOrigin == "" {
				logger.Debugf("CORS: No 'Origin' header found. Not a CORS request, skipping for %s %s.", c.Method(), c.Path())
				return next(c)
			}

			// Handle empty AllowOrigins: If no origins are configured, deny by not setting ACAO.
			if len(config.AllowOrigins) == 0 {
				logger.Warnf("CORS: No 'AllowOrigins' configured. Denying cross-origin request from '%s' for %s %s by not setting ACAO header. Please configure allowed origins.",
					requestOrigin, c.Method(), c.Path())
				c.SetHeader("Vary", "Origin") // Still good practice.
				return next(c)               // Proceed, but browser will block due to missing ACAO.
			}

			logger.Debugf("CORS: Processing request from Origin '%s' for %s %s.", requestOrigin, c.Method(), c.Path())

			var allowedOriginValue = ""
			isWildcardConfigured := false
			for _, o := range config.AllowOrigins {
				if o == "*" {
					isWildcardConfigured = true
					break
				}
			}

			if isWildcardConfigured {
				if !config.AllowCredentials {
					allowedOriginValue = "*"
					logger.Debugf("CORS: Wildcard origin '*' configured and credentials NOT required. Setting ACAO to '*'.")
				} else {
					logger.Debugf("CORS: Wildcard origin '*' configured, but credentials ARE required. ACAO '*' cannot be used. Checking for exact origin match for '%s'.", requestOrigin)
					for _, o := range config.AllowOrigins { // Check for explicit match even if "*" is present
						if o == requestOrigin {
							allowedOriginValue = requestOrigin
							logger.Debugf("CORS: Origin '%s' explicitly matches. Setting ACAO to '%s' (credentials required).", requestOrigin, allowedOriginValue)
							break
						}
					}
				}
			} else {
				for _, o := range config.AllowOrigins {
					if o == requestOrigin {
						allowedOriginValue = requestOrigin
						logger.Debugf("CORS: Origin '%s' matches configured allowed origin. Setting ACAO to '%s'.", requestOrigin, allowedOriginValue)
						break
					}
				}
			}

			if allowedOriginValue == "" {
				logger.Warnf("CORS: Origin '%s' is not in the allowed list (%v) or incompatible with AllowCredentials. Denying CORS request for %s %s by not setting ACAO header.",
					requestOrigin, config.AllowOrigins, c.Method(), c.Path())
				c.SetHeader("Vary", "Origin")
				return next(c)
			}

			// Handle Preflight (OPTIONS) Requests
			if c.Method() == MethodOptions {
				logger.Debugf("CORS: Handling preflight (OPTIONS) request for Origin '%s', Path %s.", requestOrigin, c.Path())
				c.SetHeader("Access-Control-Allow-Origin", allowedOriginValue)
				c.SetHeader("Vary", "Origin")
				if c.Header("Access-Control-Request-Method") != "" {
					c.Ctx.Response.Header.Add("Vary", "Access-Control-Request-Method")
				}
				if c.Header("Access-Control-Request-Headers") != "" {
					c.Ctx.Response.Header.Add("Vary", "Access-Control-Request-Headers")
				}
				c.SetHeader("Access-Control-Allow-Methods", allowMethodsStr)
				logger.Debugf("CORS: Preflight: Setting ACAM (Allow-Methods) to: '%s'", allowMethodsStr)
				c.SetHeader("Access-Control-Allow-Headers", allowHeadersStr)
				logger.Debugf("CORS: Preflight: Setting ACAH (Allow-Headers) to: '%s'", allowHeadersStr)

				if config.AllowCredentials {
					c.SetHeader("Access-Control-Allow-Credentials", "true")
					logger.Debugf("CORS: Preflight: Setting ACAC (Allow-Credentials) to 'true'.")
				}
				if config.MaxAge > 0 {
					c.SetHeader("Access-Control-Max-Age", maxAgeStr)
					logger.Debugf("CORS: Preflight: Setting ACMA (Max-Age) to '%s' seconds.", maxAgeStr)
				}
				return c.NoContent(StatusNoContent)
			}

			// Handle Actual (Non-OPTIONS) CORS Requests
			logger.Debugf("CORS: Handling actual (%s) request for Origin '%s', Path %s.", c.Method(), requestOrigin, c.Path())
			c.SetHeader("Access-Control-Allow-Origin", allowedOriginValue)
			c.SetHeader("Vary", "Origin")

			if config.AllowCredentials {
				c.SetHeader("Access-Control-Allow-Credentials", "true")
				logger.Debugf("CORS: Actual: Setting ACAC (Allow-Credentials) to 'true'.")
			}
			if len(config.ExposeHeaders) > 0 {
				c.SetHeader("Access-Control-Expose-Headers", exposeHeadersStr)
				logger.Debugf("CORS: Actual: Setting ACEH (Expose-Headers) to: '%s'", exposeHeadersStr)
			}
			return next(c)
		}
	}
}
