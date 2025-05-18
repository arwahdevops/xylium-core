// src/xylium/middleware_csrf.go
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
type CSRFConfig struct {
	// TokenLength is the length of the raw CSRF token in bytes before base64 encoding.
	// Default: 32 bytes (256 bits), providing strong cryptographic randomness.
	TokenLength int

	// CookieName is the name of the cookie used to store the server-side CSRF token.
	// Default: "_csrf_token".
	CookieName string

	// CookiePath is the path attribute for the CSRF cookie.
	// Default: "/" (applies to the entire domain).
	CookiePath string

	// CookieDomain is the domain attribute for the CSRF cookie.
	// Leave empty for the browser to use the current host.
	// Default: "" (empty).
	CookieDomain string

	// CookieMaxAge specifies the duration for which the CSRF cookie is valid in the browser.
	// Default: 12 hours (12 * time.Hour).
	CookieMaxAge time.Duration

	// CookieSecure specifies if the CSRF cookie should only be sent over HTTPS.
	// Highly recommended to be 'true' in production.
	// Default: true. Set to 'false' only for local HTTP development.
	CookieSecure bool

	// CookieHTTPOnly specifies if the CSRF cookie should be inaccessible to client-side JavaScript.
	// For the "Double Submit Cookie" pattern where JS reads the token from this cookie
	// to send it in a header, this MUST be 'false'. If 'true', the server needs
	// to provide the token to JS via other means (e.g., meta tag, API response).
	// Default: false (common for SPAs).
	CookieHTTPOnly bool

	// CookieSameSite sets the SameSite attribute for the CSRF cookie, mitigating CSRF attacks.
	// Options: fasthttp.CookieSameSiteLaxMode, fasthttp.CookieSameSiteStrictMode, fasthttp.CookieSameSiteNoneMode.
	// 'Lax' is a good balance. 'Strict' can break some cross-site navigations.
	// 'None' requires CookieSecure=true.
	// Default: fasthttp.CookieSameSiteLaxMode.
	CookieSameSite fasthttp.CookieSameSite

	// HeaderName is the name of the HTTP header expected to contain the client-submitted CSRF token.
	// Commonly used by AJAX/SPA requests.
	// Default: "X-CSRF-Token".
	HeaderName string

	// FormFieldName is the name of the form field (in application/x-www-form-urlencoded or multipart/form-data)
	// expected to contain the client-submitted CSRF token.
	// Commonly used by traditional HTML forms.
	// Default: "_csrf".
	FormFieldName string

	// SafeMethods is a list of HTTP methods considered "safe" (idempotent, not state-changing)
	// and thus do not require CSRF token validation.
	// Default: []string{"GET", "HEAD", "OPTIONS", "TRACE"}.
	SafeMethods []string

	// ErrorHandler is a custom function invoked if CSRF validation fails.
	// If nil, a default handler sends an HTTP 403 Forbidden response.
	// The ErrorHandler is responsible for sending the client response.
	ErrorHandler HandlerFunc

	// TokenLookup is a comma-separated string defining where to extract the CSRF token from client requests.
	// Format: "source1:name1,source2:name2,...". Source can be "header", "form", or "query".
	// Example: "header:X-CSRF-Token,form:_csrf_field".
	// If Extractor is set, TokenLookup is ignored.
	// Default: "header:X-CSRF-Token,form:_csrf" (based on default HeaderName and FormFieldName).
	TokenLookup string

	// Extractor is a custom function to extract the CSRF token from the xylium.Context.
	// Provides maximum flexibility if TokenLookup is insufficient.
	// If set, this overrides TokenLookup.
	Extractor func(c *Context) (string, error)

	// ContextTokenKey is the key used to store the generated/current CSRF token in the Xylium Context store.
	// This allows handlers or templates to access the token.
	// Default: "csrf_token".
	ContextTokenKey string
}

// ErrorCSRFTokenInvalid is a standard error indicating an invalid, missing, or mismatched CSRF token.
// It can be used as the internal error in NewHTTPError for CSRF failures.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
var DefaultCSRFConfig = CSRFConfig{
	TokenLength:    32,
	CookieName:     "_csrf_token",
	CookiePath:     "/",
	CookieMaxAge:   12 * time.Hour,
	CookieSecure:   true,  // IMPORTANT: Set 'false' only for local HTTP development.
	CookieHTTPOnly: false, // Common for SPAs reading token from cookie via JS.
	CookieSameSite: fasthttp.CookieSameSiteLaxMode,
	HeaderName:     "X-CSRF-Token",
	FormFieldName:  "_csrf",
	SafeMethods:    []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
	// TokenLookup will be auto-generated if not set.
	ContextTokenKey: "csrf_token", // Default key for context store.
}

// CSRF returns a CSRF protection middleware with default configuration.
func CSRF() Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig returns a CSRF protection middleware with the provided configuration.
// It validates the configuration and sets up token generation, cookie management, and validation logic.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize and Validate Configuration ---
	if config.TokenLength <= 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}
	if config.CookieMaxAge <= 0 {
		config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge
	}
	// For boolean flags like CookieSecure and CookieHTTPOnly, if the user provides
	// an empty CSRFConfig{}, they will default to false. We need to ensure
	// DefaultCSRFConfig values are used if not explicitly overridden.
	// This is implicitly handled if DefaultCSRFConfig is passed to this function.
	// If CSRFWithConfig is called with a partially filled config,
	// we need to merge with defaults for unprovided fields.
	// A common pattern is to create a default, then override.
	// Example: cfg := DefaultCSRFConfig; cfg.CookieName = "my_csrf"; CSRFWithConfig(cfg)
	// For this function, we assume config is either DefaultCSRFConfig or a user-modified version of it.

	if config.CookieSameSite == 0 { // fasthttp.CookieSameSite values are typically > 0. 0 is "DefaultMode".
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
	if config.Extractor == nil && config.TokenLookup == "" {
		config.TokenLookup = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
	}

	// Create a map of safe HTTP methods for efficient lookup.
	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	// Parse TokenLookup into a list of extractor functions if no custom Extractor is given.
	var tokenExtractors []func(c *Context) (string, error)
	if config.Extractor != nil {
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
				panic(fmt.Errorf("xylium: invalid CSRF TokenLookup format in part: '%s'. Expected 'source:name'.", trimmedPart))
			}
			source, name := strings.ToLower(strings.TrimSpace(segments[0])), strings.TrimSpace(segments[1])
			switch source {
			case "header":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.Header(name), nil })
			case "form":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.FormValue(name), nil })
			case "query": // Query parameters are generally not recommended for CSRF tokens due to URL logging.
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported: header, form, query.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		panic("xylium: CSRF TokenLookup or Extractor must be configured to define at least one token extraction method.")
	}

	// Define the error handler.
	errorHandler := config.ErrorHandler
	if errorHandler == nil { // Default error handler if none provided.
		errorHandler = func(c *Context) error {
			// Retrieve the specific cause if set by the middleware.
			var internalCause error = ErrorCSRFTokenInvalid // Default internal error.
			if errVal, exists := c.Get("csrf_validation_error"); exists {
				if e, ok := errVal.(error); ok {
					internalCause = e
				}
			}
			// GlobalErrorHandler will handle logging of this HTTPError.
			return NewHTTPError(StatusForbidden, "CSRF token validation failed.").WithInternal(internalCause)
		}
	}

	// --- The Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Get the request-scoped logger.
			logger := c.Logger()

			// 1. Retrieve the existing CSRF token from the request cookie, if any.
			tokenFromCookie := c.Cookie(config.CookieName)
			isNewSessionOrTokenExpired := tokenFromCookie == ""

			// 2. Determine if the current request method is considered "safe".
			_, methodIsSafe := safeMethodsMap[c.Method()]

			// 3. Generate or refresh the CSRF token and set/update the cookie if:
			//    - The method is safe (e.g., GET request, always provide/refresh token for forms/SPAs).
			//    - Or, if it's an unsafe method but no token currently exists in the cookie (new session).
			if methodIsSafe || isNewSessionOrTokenExpired {
				newToken, err := generateRandomStringBase64(config.TokenLength)
				if err != nil {
					logger.Errorf("CSRF: Failed to generate new security token: %v", err)
					// This is a server-side issue; return an error that GlobalErrorHandler will catch.
					return NewHTTPError(StatusInternalServerError, "Could not generate security token.").WithInternal(err)
				}
				// Update tokenFromCookie to the newly generated one for subsequent logic and cookie setting.
				tokenFromCookie = newToken

				// Set (or update) the CSRF token cookie.
				cookie := fasthttp.AcquireCookie()
				defer fasthttp.ReleaseCookie(cookie) // Return to pool.

				cookie.SetKey(config.CookieName)
				cookie.SetValue(tokenFromCookie)
				cookie.SetPath(config.CookiePath)
				cookie.SetDomain(config.CookieDomain)
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
				cookie.SetSecure(config.CookieSecure)
				cookie.SetHTTPOnly(config.CookieHTTPOnly)
				cookie.SetSameSite(config.CookieSameSite)
				c.SetCookie(cookie)

				if isNewSessionOrTokenExpired {
					logger.Debugf("CSRF: New token generated and set in cookie '%s' for path %s %s.", config.CookieName, c.Method(), c.Path())
				} else { // Token was refreshed for a safe method.
					logger.Debugf("CSRF: Token refreshed in cookie '%s' for safe method %s %s.", config.CookieName, c.Method(), c.Path())
				}
			}

			// 4. Always store the current (potentially new) token from the cookie into the context.
			//    This makes it accessible to templates or handlers that need to embed it in forms or headers.
			c.Set(config.ContextTokenKey, tokenFromCookie)

			// 5. If the request method is *not* safe (e.g., POST, PUT, DELETE),
			//    CSRF token validation is required.
			if !methodIsSafe {
				if tokenFromCookie == "" {
					// This should ideally not be reached if logic in step 3 is correct,
					// as a token should have been generated for unsafe methods if one wasn't present.
					// This acts as a safeguard.
					logger.Warnf("CSRF: CRITICAL - No token in cookie for unsafe method %s %s. Validation will fail.", c.Method(), c.Path())
					c.Set("csrf_validation_error", errors.New("missing CSRF token in cookie for unsafe method"))
					return errorHandler(c)
				}

				// Extract the token submitted by the client from the request (header, form, etc.).
				var tokenFromRequest string
				var extractionErr error
				for _, extractorFunc := range tokenExtractors {
					token, err := extractorFunc(c)
					if err != nil { // An error from a custom extractor.
						extractionErr = err // Store the first error encountered.
						break
					}
					if token != "" {
						tokenFromRequest = token
						break // Token found, no need to check other extractors.
					}
				}

				if extractionErr != nil {
					logger.Errorf("CSRF: Custom token extractor failed for %s %s: %v", c.Method(), c.Path(), extractionErr)
					// Treat extractor failure as a server-side issue.
					return NewHTTPError(StatusInternalServerError, "CSRF token extraction process failed internally.").WithInternal(extractionErr)
				}

				// Perform the validation: token from cookie must match token from request.
				// Note: Use a constant-time comparison function in a real high-security scenario
				// to prevent timing attacks, though for CSRF tokens, direct string comparison is common.
				// For simplicity, direct comparison is used here.
				if tokenFromRequest == "" || tokenFromCookie != tokenFromRequest {
					logMessage := fmt.Sprintf("CSRF: Token mismatch or not found in request for %s %s.", c.Method(), c.Path())
					if tokenFromRequest == "" {
						logMessage += " Client did not submit a token."
					} else {
						// Avoid logging actual token values to prevent accidental exposure.
						logMessage += " Submitted token does not match cookie token."
					}
					logger.Warnf(logMessage)
					c.Set("csrf_validation_error", ErrorCSRFTokenInvalid) // Set cause for error handler.
					return errorHandler(c)
				}
				logger.Debugf("CSRF: Token validated successfully for unsafe method %s %s.", c.Method(), c.Path())
			}

			// If validation passes (or method is safe), proceed to the next handler.
			return next(c)
		}
	}
}

// generateRandomStringBase64 generates a cryptographically secure random string,
// encoded using URL-safe base64.
// `lengthInBytes` is the number of random bytes to generate before encoding.
func generateRandomStringBase64(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 {
		// Ensure a sensible default length if input is invalid.
		lengthInBytes = 32 // Default to 32 bytes for strong randomness.
	}
	randomBytes := make([]byte, lengthInBytes)
	// Fill the byte slice with random data from a cryptographically secure source.
	if _, err := rand.Read(randomBytes); err != nil {
		// This would be a critical failure in the crypto/rand source.
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	// Encode the random bytes to a URL-safe base64 string (without padding for cleaner tokens).
	return base64.URLEncoding.EncodeToString(randomBytes), nil
}
