package xylium

import (
	"crypto/rand"    // For cryptographically secure random token generation.
	"crypto/subtle"  // For constant-time string comparison.
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
	TokenLength int
	// CookieName is the name of the cookie used to store the server-side CSRF secret.
	CookieName string
	// CookiePath is the path attribute for the CSRF cookie.
	CookiePath string
	// CookieDomain is the domain attribute for the CSRF cookie.
	CookieDomain string
	// CookieMaxAge specifies the duration for which the CSRF cookie is valid.
	CookieMaxAge time.Duration
	// CookieSecure specifies if the CSRF cookie should only be transmitted over HTTPS.
	// CRITICAL: Should be true in production. Set to false ONLY for local HTTP development.
	CookieSecure bool
	// CookieHTTPOnly specifies if the CSRF cookie should be inaccessible to client-side JavaScript.
	// True for traditional server-rendered forms; false for SPAs reading token from this cookie.
	CookieHTTPOnly bool
	// CookieSameSite sets the SameSite attribute for the CSRF cookie.
	CookieSameSite fasthttp.CookieSameSite
	// HeaderName is the name of the HTTP header expected to contain the client-submitted CSRF token.
	HeaderName string
	// FormFieldName is the name of the form field expected to contain the client-submitted CSRF token.
	FormFieldName string
	// SafeMethods is a list of HTTP methods considered "safe" and thus do not require CSRF token validation.
	SafeMethods []string
	// ErrorHandler is a custom function invoked if CSRF validation fails.
	ErrorHandler HandlerFunc
	// TokenLookup is a comma-separated string defining where and in what order to extract
	// the client-submitted CSRF token. Format: "source1:name1,source2:name2,...".
	// Valid sources: "header", "form", "query". Ignored if Extractor is set.
	TokenLookup string
	// Extractor is a custom function to extract the CSRF token from the xylium.Context.
	// Overrides TokenLookup if set.
	Extractor func(c *Context) (string, error)
	// ContextTokenKey is the key used to store the generated/current server-side CSRF token
	// in the xylium.Context store.
	ContextTokenKey string
}

// ErrorCSRFTokenInvalid is a standard error indicating an invalid, missing, or mismatched CSRF token.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
var DefaultCSRFConfig = CSRFConfig{
	TokenLength:    32, // 256 bits, strong default.
	CookieName:     "_csrf_token",
	CookiePath:     "/", // Applies to the entire domain.
	CookieMaxAge:   12 * time.Hour,
	CookieSecure:   true,  // Secure by default; override for local HTTP dev.
	CookieHTTPOnly: false, // Allows JS to read for SPAs (Double Submit Cookie pattern).
	CookieSameSite: fasthttp.CookieSameSiteLaxMode,
	HeaderName:     "X-CSRF-Token",
	FormFieldName:  "_csrf",
	SafeMethods:    []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
	ContextTokenKey: "csrf_token", // Key for accessing the token in c.store.
}

// CSRF returns a CSRF protection middleware with default configuration (DefaultCSRFConfig).
func CSRF() Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig returns a CSRF protection middleware with the provided configuration.
// It validates the configuration, sets up token generation, cookie management,
// and the core token validation logic using constant-time comparison.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize and Validate Configuration ---
	if config.TokenLength <= 0 { config.TokenLength = DefaultCSRFConfig.TokenLength }
	if config.CookieName == "" { config.CookieName = DefaultCSRFConfig.CookieName }
	if config.CookiePath == "" { config.CookiePath = DefaultCSRFConfig.CookiePath }
	if config.CookieMaxAge <= 0 { config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge }
	// For booleans like CookieSecure, CookieHTTPOnly, the zero value is 'false'.
	// If not explicitly set by user, they rely on DefaultCSRFConfig values if this func is called via CSRF().
	// If user calls CSRFWithConfig(CSRFConfig{...}), their explicit false is honored.
	if config.CookieSameSite == 0 { config.CookieSameSite = DefaultCSRFConfig.CookieSameSite } // 0 is fasthttp.CookieSameSiteDefaultMode
	if config.HeaderName == "" { config.HeaderName = DefaultCSRFConfig.HeaderName }
	if config.FormFieldName == "" { config.FormFieldName = DefaultCSRFConfig.FormFieldName }
	if len(config.SafeMethods) == 0 { config.SafeMethods = DefaultCSRFConfig.SafeMethods }
	if config.ContextTokenKey == "" { config.ContextTokenKey = DefaultCSRFConfig.ContextTokenKey }

	// Build TokenLookup string if it's empty and no custom Extractor is provided.
	if config.Extractor == nil && config.TokenLookup == "" {
		config.TokenLookup = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
	}

	// Create a map of safe HTTP methods (uppercase) for efficient lookup.
	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	// Parse TokenLookup string into a list of extractor functions if no custom Extractor is given.
	var tokenExtractors []func(c *Context) (string, error)
	if config.Extractor != nil {
		tokenExtractors = append(tokenExtractors, config.Extractor)
	} else {
		parts := strings.Split(config.TokenLookup, ",")
		for _, part := range parts {
			trimmedPart := strings.TrimSpace(part)
			if trimmedPart == "" { continue }
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
			case "query": // Query params for CSRF tokens are generally discouraged.
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported: header, form, query.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		panic("xylium: CSRF TokenLookup or Extractor must be configured with at least one token extraction method.")
	}

	// Define the error handler for CSRF validation failures.
	errorHandler := config.ErrorHandler
	if errorHandler == nil { // Use default error handler if none provided.
		errorHandler = func(c *Context) error {
			var internalCause error = ErrorCSRFTokenInvalid // Default internal error.
			if errVal, exists := c.Get("csrf_validation_error"); exists {
				if e, ok := errVal.(error); ok { internalCause = e } // Use more specific error if available.
			}
			return NewHTTPError(StatusForbidden, "CSRF token validation failed. Access denied.").WithInternal(internalCause)
		}
	}

	// --- The Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Request-scoped logger.

			// 1. Retrieve existing CSRF token (secret) from the cookie.
			tokenFromCookie := c.Cookie(config.CookieName)
			isNewSessionOrTokenExpired := tokenFromCookie == ""

			// 2. Determine if the current request method is "safe".
			_, methodIsSafe := safeMethodsMap[c.Method()]

			// 3. Generate/Refresh Token & Set Cookie if method is safe or no token exists.
			if methodIsSafe || isNewSessionOrTokenExpired {
				newToken, err := generateRandomStringBase64(config.TokenLength)
				if err != nil {
					logger.Errorf("CSRF: Failed to generate new security token: %v", err)
					return NewHTTPError(StatusInternalServerError, "Could not generate security token for CSRF protection.").WithInternal(err)
				}
				tokenFromCookie = newToken // Update for subsequent logic and cookie setting.

				cookie := fasthttp.AcquireCookie()
				defer fasthttp.ReleaseCookie(cookie)
				cookie.SetKey(config.CookieName)
				cookie.SetValue(tokenFromCookie)
				cookie.SetPath(config.CookiePath)
				cookie.SetDomain(config.CookieDomain)
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
				cookie.SetSecure(config.CookieSecure)
				cookie.SetHTTPOnly(config.CookieHTTPOnly)
				cookie.SetSameSite(config.CookieSameSite)
				c.SetCookie(cookie)

				logMsg := "CSRF: New token generated and set in cookie '%s' for path %s %s."
				if !isNewSessionOrTokenExpired && methodIsSafe {
					logMsg = "CSRF: Token refreshed in cookie '%s' for safe method %s %s."
				}
				logger.Debugf(logMsg, config.CookieName, c.Method(), c.Path())
			}

			// 4. Store Current Token in Context for access by handlers/templates.
			if tokenFromCookie != "" {
				c.Set(config.ContextTokenKey, tokenFromCookie)
			}

			// 5. Validate Token for Unsafe Methods.
			if !methodIsSafe {
				if tokenFromCookie == "" {
					// This should ideally not be reached due to logic in step 3. Critical safeguard.
					logger.Warnf("CSRF: CRITICAL - No token in cookie for unsafe method %s %s. Validation will fail.", c.Method(), c.Path())
					c.Set("csrf_validation_error", errors.New("critical: missing CSRF token in cookie for unsafe method"))
					return errorHandler(c)
				}

				// Extract client-submitted token.
				var tokenFromRequest string
				var extractionErr error
				for _, extractorFunc := range tokenExtractors {
					token, err := extractorFunc(c)
					if err != nil { extractionErr = err; break }
					if token != "" { tokenFromRequest = token; break }
				}

				if extractionErr != nil {
					logger.Errorf("CSRF: Custom token extractor failed for %s %s: %v", c.Method(), c.Path(), extractionErr)
					return NewHTTPError(StatusInternalServerError, "CSRF token extraction process failed internally.").WithInternal(extractionErr)
				}

				// Perform constant-time comparison for CSRF tokens.
				cookieTokenBytes := []byte(tokenFromCookie)
				requestTokenBytes := []byte(tokenFromRequest)
				tokensMatch := false

				// subtle.ConstantTimeCompare returns 1 if inputs are equal, 0 otherwise.
				// It requires inputs to be of the same length.
				if len(requestTokenBytes) > 0 && len(requestTokenBytes) == len(cookieTokenBytes) {
					// Only proceed if tokenFromRequest is not empty and lengths match.
					if subtle.ConstantTimeCompare(cookieTokenBytes, requestTokenBytes) == 1 {
						tokensMatch = true
					}
				}

				if !tokensMatch {
					logMessage := fmt.Sprintf("CSRF: Token mismatch or invalid token in request for unsafe method %s %s.", c.Method(), c.Path())
					if tokenFromRequest == "" {
						logMessage += " Client did not submit a CSRF token."
					} else if len(cookieTokenBytes) != len(requestTokenBytes) {
						logMessage += " Submitted token length does not match expected token length."
					} else {
						logMessage += " Submitted token does not match the expected token from the cookie."
					}
					logger.Warnf(logMessage)
					c.Set("csrf_validation_error", ErrorCSRFTokenInvalid)
					return errorHandler(c)
				}
				logger.Debugf("CSRF: Token validated successfully (constant time) for unsafe method %s %s.", c.Method(), c.Path())
			}

			// If validation passes (or method is safe), proceed to the next handler.
			return next(c)
		}
	}
}

// generateRandomStringBase64 generates a cryptographically secure random string,
// encoded using URL-safe base64 (without padding).
// `lengthInBytes` is the number of random bytes to generate before encoding.
func generateRandomStringBase64(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 { // Ensure a sensible default length.
		lengthInBytes = 32 // Default to 32 bytes (256 bits of entropy).
	}
	randomBytes := make([]byte, lengthInBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	// Encode to URL-safe base64 string. URLEncoding is preferred for tokens.
	return base64.URLEncoding.EncodeToString(randomBytes), nil
}
