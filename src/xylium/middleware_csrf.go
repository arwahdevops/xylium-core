package xylium

import (
	"crypto/rand"    // For cryptographically secure random token generation.
	"encoding/base64"  // For encoding the token into a URL/header-safe string.
	"errors"         // For defining custom error types.
	"fmt"            // For string formatting in errors and logs.
	"strings"        // For string manipulation (e.g., parsing TokenLookup, method checking).
	"time"           // For cookie expiration management.

	"github.com/valyala/fasthttp" // For fasthttp.Cookie and related constants.
)

// CSRFConfig defines the configuration for the CSRF (Cross-Site Request Forgery) protection middleware.
// CSRF attacks trick a victim into submitting a malicious request. This middleware implements
// the "Double Submit Cookie" pattern or allows other token verification strategies to mitigate such attacks.
type CSRFConfig struct {
	// TokenLength is the length of the raw CSRF token in bytes before base64 encoding.
	// A longer token provides stronger cryptographic randomness.
	// Default: 32 bytes (256 bits), recommended by OWASP.
	TokenLength int

	// CookieName is the name of the cookie used to store the server-side CSRF secret (token).
	// This cookie is typically compared against a token submitted by the client in a header or form field.
	// Default: "_csrf_token".
	CookieName string

	// CookiePath is the path attribute for the CSRF cookie, determining its scope.
	// Default: "/" (applies to the entire domain).
	CookiePath string

	// CookieDomain is the domain attribute for the CSRF cookie.
	// Leave empty for the browser to use the current host, which is generally safer.
	// Setting a specific domain can be useful for subdomains if CSRF protection needs to span them.
	// Default: "" (empty).
	CookieDomain string

	// CookieMaxAge specifies the duration for which the CSRF cookie is valid in the browser.
	// After this duration, the cookie (and thus the CSRF token) expires.
	// Default: 12 hours (12 * time.Hour).
	CookieMaxAge time.Duration

	// CookieSecure specifies if the CSRF cookie should only be transmitted over HTTPS.
	// CRITICAL: This should ALWAYS be `true` in production environments.
	// Set to `false` ONLY for local HTTP development where HTTPS is not available.
	// Default: true.
	CookieSecure bool

	// CookieHTTPOnly specifies if the CSRF cookie should be inaccessible to client-side JavaScript.
	// - If `true` (recommended for traditional server-rendered forms where the server embeds the token):
	//   JavaScript cannot read this cookie. The server must provide the token to the client via
	//   other means (e.g., hidden form field, meta tag, API response body) for JS to use it in AJAX requests.
	// - If `false` (common for Single Page Applications - SPAs):
	//   Client-side JavaScript can read the token from this cookie and include it in a request header
	//   (e.g., X-CSRF-Token). This is a common "Double Submit Cookie" variation.
	// Default: false (to support SPAs easily).
	CookieHTTPOnly bool

	// CookieSameSite sets the SameSite attribute for the CSRF cookie, providing another layer
	// of CSRF mitigation by controlling when the cookie is sent with cross-site requests.
	// - `fasthttp.CookieSameSiteLaxMode`: Cookie is sent on top-level navigations and GET requests
	//   initiated by third-party websites. Good balance of security and usability.
	// - `fasthttp.CookieSameSiteStrictMode`: Cookie is only sent for same-site requests.
	//   Can break legitimate cross-site links that rely on session state.
	// - `fasthttp.CookieSameSiteNoneMode`: Cookie is sent on all requests (same-site and cross-site).
	//   Requires `CookieSecure=true`. Use with caution.
	// Default: `fasthttp.CookieSameSiteLaxMode`.
	CookieSameSite fasthttp.CookieSameSite

	// HeaderName is the name of the HTTP header expected to contain the client-submitted CSRF token.
	// This is commonly used by AJAX/SPA requests where JavaScript reads the token (from cookie or elsewhere)
	// and sends it in this header.
	// Default: "X-CSRF-Token".
	HeaderName string

	// FormFieldName is the name of the form field (in `application/x-www-form-urlencoded`
	// or `multipart/form-data` requests) expected to contain the client-submitted CSRF token.
	// This is commonly used by traditional HTML forms.
	// Default: "_csrf".
	FormFieldName string

	// SafeMethods is a list of HTTP methods considered "safe" (idempotent, not state-changing)
	// and thus do not require CSRF token validation. For these methods, a new token might be
	// generated or refreshed, but an incoming token is not validated.
	// Default: `[]string{"GET", "HEAD", "OPTIONS", "TRACE"}`.
	SafeMethods []string

	// ErrorHandler is a custom function invoked if CSRF validation fails (e.g., token mismatch, missing token).
	// If nil, a default handler sends an HTTP 403 Forbidden response.
	// The ErrorHandler is responsible for formulating and sending the client response.
	ErrorHandler HandlerFunc

	// TokenLookup is a comma-separated string defining where and in what order to extract
	// the client-submitted CSRF token from the request.
	// Format: "source1:name1,source2:name2,...".
	// Valid sources: "header", "form", "query".
	// Example: "header:X-CSRF-Token,form:_csrf_field,query:csrf_token".
	// If `Extractor` is set, `TokenLookup` is ignored.
	// If both `Extractor` and `TokenLookup` are empty, it defaults based on `HeaderName` and `FormFieldName`.
	// Default behavior: checks header defined by `HeaderName`, then form field by `FormFieldName`.
	TokenLookup string

	// Extractor is a custom function to extract the CSRF token from the `xylium.Context`.
	// This provides maximum flexibility if the standard `TokenLookup` mechanism is insufficient.
	// If set, this function overrides the `TokenLookup` behavior.
	// It should return the found token string and an optional error if extraction fails.
	Extractor func(c *Context) (string, error)

	// ContextTokenKey is the key used to store the generated/current server-side CSRF token
	// in the `xylium.Context` store (`c.store`). This allows handlers, middleware, or templates
	// to access the current valid token (e.g., to embed it in HTML forms or provide to JavaScript).
	// Default: "csrf_token".
	ContextTokenKey string
}

// ErrorCSRFTokenInvalid is a standard error indicating an invalid, missing, or mismatched CSRF token.
// This can be used as the `Internal` error in a `xylium.HTTPError` for CSRF failures,
// providing more context for logging or custom error handlers.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
// Users should review these defaults, especially `CookieSecure` and `CookieHTTPOnly`,
// to ensure they align with their application's security requirements and architecture.
var DefaultCSRFConfig = CSRFConfig{
	TokenLength:    32,
	CookieName:     "_csrf_token",
	CookiePath:     "/",
	CookieMaxAge:   12 * time.Hour,
	CookieSecure:   true,  // IMPORTANT: Set 'false' ONLY for local HTTP development.
	CookieHTTPOnly: false, // Allows JS to read cookie for SPAs (common Double Submit Cookie pattern).
	CookieSameSite: fasthttp.CookieSameSiteLaxMode,
	HeaderName:     "X-CSRF-Token",
	FormFieldName:  "_csrf",
	SafeMethods:    []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
	// TokenLookup will be auto-generated based on HeaderName and FormFieldName if left empty.
	ContextTokenKey: "csrf_token", // Key for accessing the token in c.store.
}

// CSRF returns a CSRF protection middleware with default configuration (DefaultCSRFConfig).
func CSRF() Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig returns a CSRF protection middleware with the provided configuration.
// It validates the configuration, sets up token generation, cookie management,
// and the core token validation logic.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize and Validate Configuration ---
	// Apply defaults from DefaultCSRFConfig for any zero-value fields in the provided config.
	if config.TokenLength <= 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}
	if config.CookieMaxAge <= 0 { // Duration
		config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge
	}
	// For boolean flags like CookieSecure and CookieHTTPOnly, the zero value is 'false'.
	// If this function is called with CSRFConfig{}, they get 'false'.
	// If called with DefaultCSRFConfig, they get DefaultCSRFConfig's values.
	// This logic assumes that if a user provides a config, they intend to override defaults,
	// even for booleans if they set them. If a more sophisticated merge is needed,
	// it would require checking if each field was explicitly set.

	if config.CookieSameSite == 0 { // fasthttp.CookieSameSite uses defined constants > 0. 0 is "DefaultMode".
		config.CookieSameSite = DefaultCSRFConfig.CookieSameSite
	}
	if config.HeaderName == "" {
		config.HeaderName = DefaultCSRFConfig.HeaderName
	}
	if config.FormFieldName == "" {
		config.FormFieldName = DefaultCSRFConfig.FormFieldName
	}
	if len(config.SafeMethods) == 0 {
		config.SafeMethods = DefaultCSRFConfig.SafeMethods
	}
	if config.ContextTokenKey == "" {
		config.ContextTokenKey = DefaultCSRFConfig.ContextTokenKey
	}

	// Build TokenLookup string if it's empty and no custom Extractor is provided.
	// This defines the default extraction order: Header then Form.
	if config.Extractor == nil && config.TokenLookup == "" {
		config.TokenLookup = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
	}

	// Create a map of safe HTTP methods (uppercase) for efficient lookup during request processing.
	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	// Parse TokenLookup string into a list of extractor functions if no custom Extractor is given.
	// These functions attempt to find the client-submitted token from various request parts.
	var tokenExtractors []func(c *Context) (string, error)
	if config.Extractor != nil {
		// If a custom Extractor is provided, use it exclusively.
		tokenExtractors = append(tokenExtractors, config.Extractor)
	} else {
		parts := strings.Split(config.TokenLookup, ",")
		for _, part := range parts {
			trimmedPart := strings.TrimSpace(part)
			if trimmedPart == "" {
				continue
			}
			segments := strings.SplitN(trimmedPart, ":", 2)
			if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
				// Invalid format in TokenLookup string. Panic early.
				panic(fmt.Errorf("xylium: invalid CSRF TokenLookup format in part: '%s'. Expected 'source:name'.", trimmedPart))
			}
			source, name := strings.ToLower(strings.TrimSpace(segments[0])), strings.TrimSpace(segments[1])
			switch source {
			case "header":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.Header(name), nil })
			case "form":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.FormValue(name), nil })
			case "query": // Query parameters are generally NOT recommended for CSRF tokens due to leakage risks (logs, referrers).
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported: header, form, query.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		// This should not happen if TokenLookup defaults correctly or if Extractor is provided.
		panic("xylium: CSRF TokenLookup or Extractor must be configured to define at least one token extraction method.")
	}

	// Define the error handler for CSRF validation failures.
	errorHandler := config.ErrorHandler
	if errorHandler == nil { // Use default error handler if none provided by user.
		errorHandler = func(c *Context) error {
			// Retrieve the specific cause of the CSRF error if set by the middleware.
			var internalCause error = ErrorCSRFTokenInvalid // Default internal error for logging.
			if errVal, exists := c.Get("csrf_validation_error"); exists {
				if e, ok := errVal.(error); ok {
					internalCause = e // Use the more specific error if available.
				}
			}
			// The GlobalErrorHandler will log details of this HTTPError, including the internalCause.
			return NewHTTPError(StatusForbidden, "CSRF token validation failed. Access denied.").WithInternal(internalCause)
		}
	}

	// --- The Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Get request-scoped logger.

			// 1. Retrieve the existing CSRF token (secret) from the request's cookie, if any.
			tokenFromCookie := c.Cookie(config.CookieName)
			isNewSessionOrTokenExpired := tokenFromCookie == ""

			// 2. Determine if the current request method is "safe" (e.g., GET, HEAD).
			//    Safe methods do not require CSRF token validation.
			_, methodIsSafe := safeMethodsMap[c.Method()]

			// 3. Generate/Refresh Token & Set Cookie:
			//    A new token is generated and set in the cookie if:
			//    a) The method is safe (e.g., a GET request to a page with a form; always provide/refresh token).
			//    b) Or, if it's an unsafe method but no token currently exists in the cookie (e.g., new session).
			//       This ensures unsafe methods always have a server-side token to compare against.
			if methodIsSafe || isNewSessionOrTokenExpired {
				newToken, err := generateRandomStringBase64(config.TokenLength)
				if err != nil {
					logger.Errorf("CSRF: Failed to generate new security token: %v", err)
					// This is a server-side issue; return an error that GlobalErrorHandler will catch.
					return NewHTTPError(StatusInternalServerError, "Could not generate security token for CSRF protection.").WithInternal(err)
				}
				// `tokenFromCookie` is updated to the newly generated one for subsequent logic and cookie setting.
				tokenFromCookie = newToken

				// Set (or update) the CSRF token cookie in the response.
				cookie := fasthttp.AcquireCookie() // Get a cookie object from fasthttp's pool.
				defer fasthttp.ReleaseCookie(cookie) // Return to pool when this function scope ends.

				cookie.SetKey(config.CookieName)
				cookie.SetValue(tokenFromCookie)
				cookie.SetPath(config.CookiePath)
				cookie.SetDomain(config.CookieDomain)
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds())) // MaxAge is in seconds.
				cookie.SetSecure(config.CookieSecure)
				cookie.SetHTTPOnly(config.CookieHTTPOnly)
				cookie.SetSameSite(config.CookieSameSite)
				c.SetCookie(cookie) // Add Set-Cookie header to the response.

				if isNewSessionOrTokenExpired {
					logger.Debugf("CSRF: New token generated and set in cookie '%s' for path %s %s.", config.CookieName, c.Method(), c.Path())
				} else { // Token was refreshed for a safe method.
					logger.Debugf("CSRF: Token refreshed in cookie '%s' for safe method %s %s.", config.CookieName, c.Method(), c.Path())
				}
			}

			// 4. Store Current Token in Context:
			//    Always store the current server-side token (from the cookie, potentially new) into the
			//    Xylium context store. This makes it accessible to view templates (to embed in forms)
			//    or to API handlers (to return to JS clients if needed).
			if tokenFromCookie != "" { // Only set if a token actually exists/was generated.
				c.Set(config.ContextTokenKey, tokenFromCookie)
			}

			// 5. Validate Token for Unsafe Methods:
			//    If the request method is *not* safe (e.g., POST, PUT, DELETE), CSRF token validation is required.
			if !methodIsSafe {
				if tokenFromCookie == "" {
					// This state should ideally not be reached for an unsafe method if the logic in step 3
					// (generating a token if `isNewSessionOrTokenExpired`) is correct.
					// This acts as a critical safeguard.
					logger.Warnf("CSRF: CRITICAL - No token in cookie for unsafe method %s %s. Validation will fail.", c.Method(), c.Path())
					c.Set("csrf_validation_error", errors.New("critical: missing CSRF token in cookie for unsafe method"))
					return errorHandler(c)
				}

				// Extract the token submitted by the client from the request (header, form, etc.)
				// using the configured extractors.
				var tokenFromRequest string
				var extractionErr error
				for _, extractorFunc := range tokenExtractors {
					token, err := extractorFunc(c) // Call the configured extractor function.
					if err != nil { // An error occurred within a custom extractor function.
						extractionErr = err // Store the first error encountered.
						break
					}
					if token != "" {
						tokenFromRequest = token // Token found.
						break                   // No need to check other extractors.
					}
				}

				if extractionErr != nil {
					logger.Errorf("CSRF: Custom token extractor failed for %s %s: %v", c.Method(), c.Path(), extractionErr)
					// Treat extractor failure as a server-side issue, not a client CSRF failure.
					return NewHTTPError(StatusInternalServerError, "CSRF token extraction process failed internally.").WithInternal(extractionErr)
				}

				// Perform the validation: token from cookie (server's secret) must match token from request (client's submission).
				// Note: For very high-security scenarios, a constant-time comparison function (e.g., `subtle.ConstantTimeCompare`)
				// should be used to prevent timing attacks. However, for CSRF tokens, direct string comparison is common
				// and generally considered acceptable given the nature of the token and attack vector.
				if tokenFromRequest == "" || tokenFromCookie != tokenFromRequest {
					logMessage := fmt.Sprintf("CSRF: Token mismatch or token not found in request for unsafe method %s %s.", c.Method(), c.Path())
					if tokenFromRequest == "" {
						logMessage += " Client did not submit a CSRF token."
					} else {
						// Avoid logging actual token values in production to prevent accidental exposure.
						// In DebugMode, one might log parts or hashes if necessary for deep debugging.
						logMessage += " Submitted token does not match the expected token from the cookie."
					}
					logger.Warnf(logMessage)
					c.Set("csrf_validation_error", ErrorCSRFTokenInvalid) // Set cause for custom error handler.
					return errorHandler(c)
				}
				logger.Debugf("CSRF: Token validated successfully for unsafe method %s %s.", c.Method(), c.Path())
			}

			// If validation passes (or method is safe), proceed to the next handler in the chain.
			return next(c)
		}
	}
}

// generateRandomStringBase64 generates a cryptographically secure random string,
// encoded using URL-safe base64 (without padding for cleaner tokens).
// `lengthInBytes` is the number of random bytes to generate before encoding.
// A good default (e.g., 32 bytes) provides strong entropy.
func generateRandomStringBase64(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 {
		// Ensure a sensible default length (e.g., 32 bytes for 256 bits of entropy)
		// if an invalid or non-positive length is provided. This makes the function more robust.
		lengthInBytes = 32
	}
	randomBytes := make([]byte, lengthInBytes)
	// Fill the byte slice with random data from a cryptographically secure source (crypto/rand).
	if _, err := rand.Read(randomBytes); err != nil {
		// This would be a critical failure in the crypto/rand source, indicating a system-level issue.
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	// Encode the random bytes to a URL-safe base64 string.
	// URLEncoding is preferred for tokens in headers/URLs over StdEncoding.
	return base64.URLEncoding.EncodeToString(randomBytes), nil
}
