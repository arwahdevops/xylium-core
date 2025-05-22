// File: src/xylium/middleware_csrf.go
package xylium

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

// CSRFConfig defines the configuration for the CSRF (Cross-Site Request Forgery) protection middleware.
type CSRFConfig struct {
	TokenLength     int
	CookieName      string
	CookiePath      string
	CookieDomain    string
	CookieMaxAge    time.Duration
	CookieSecure    *bool // Pointer to distinguish between not set vs. explicitly false
	CookieHTTPOnly  *bool // Pointer to distinguish
	CookieSameSite  fasthttp.CookieSameSite
	HeaderName      string
	FormFieldName   string
	SafeMethods     []string
	ErrorHandler    func(c *Context, err error) error
	TokenLookup     string                           // Comma-separated "source:name" (e.g., "header:X-CSRF-Token,form:_csrf")
	Extractor       func(c *Context) (string, error) // Custom token extractor
	ContextTokenKey string                           // Key to store the token for the *next* request in c.store
}

// ErrorCSRFTokenInvalid is returned when CSRF token validation fails.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// ConfiguredCSRFErrorHandlerErrorKey is the context key used to pass the specific CSRF cause error
// to a custom ErrorHandler, if one is configured.
const ConfiguredCSRFErrorHandlerErrorKey = "csrf_validation_cause"

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
var DefaultCSRFConfig = func() CSRFConfig {
	secureTrue := true
	httpOnlyTrue := true
	return CSRFConfig{
		TokenLength:     32, // Recommended length for good entropy
		CookieName:      "_csrf_token",
		CookiePath:      "/",
		CookieDomain:    "", // Defaults to current host
		CookieMaxAge:    12 * time.Hour,
		CookieSecure:    &secureTrue,   // Default to true (send only over HTTPS)
		CookieHTTPOnly:  &httpOnlyTrue, // Default to true (cookie not accessible via client-side JS)
		CookieSameSite:  fasthttp.CookieSameSiteLaxMode,
		HeaderName:      "X-CSRF-Token", // Common header name
		FormFieldName:   "_csrf",        // Common form field name
		SafeMethods:     []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
		ContextTokenKey: ContextKeyCSRFToken, // Default context key from types.go
		// TokenLookup default will be constructed based on HeaderName and FormFieldName if empty
	}
}()

// CSRF returns a CSRF protection middleware with default configuration.
func CSRF() Middleware {
	cfgCopy := DefaultCSRFConfig // Copy defaults to avoid modifying the global variable
	return CSRFWithConfig(cfgCopy)
}

// GenerateRandomStringBase64 generates a cryptographically secure random string encoded in Base64.
// Exported as a variable to allow mocking in tests.
var GenerateRandomStringBase64 = func(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 {
		lengthInBytes = 32 // Default to 32 bytes for good security
	}
	randomBytes := make([]byte, lengthInBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	// RawURLEncoding is suitable for tokens in headers/cookies (no padding, URL-safe).
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

// CSRFWithConfig returns a CSRF protection middleware with the provided configuration.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize Configuration ---
	if config.TokenLength <= 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}
	// CookieDomain defaults to "" (current host)
	// CookieMaxAge: 0 means session cookie. <0 means delete. >0 means duration.
	// If config.CookieMaxAge is 0 by user, it's a session cookie.
	// If CSRFConfig struct was zero-initialized, it would be 0. DefaultCSRFConfig sets 12h.

	if config.CookieSecure == nil {
		config.CookieSecure = DefaultCSRFConfig.CookieSecure
	}
	if config.CookieHTTPOnly == nil {
		config.CookieHTTPOnly = DefaultCSRFConfig.CookieHTTPOnly
	}
	if config.CookieSameSite == fasthttp.CookieSameSiteDefaultMode { // 0
		config.CookieSameSite = DefaultCSRFConfig.CookieSameSite // Default to Lax
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

	var tokenExtractors []func(c *Context) (string, error)
	if config.Extractor != nil {
		tokenExtractors = append(tokenExtractors, config.Extractor)
	} else {
		lookupString := config.TokenLookup
		if lookupString == "" {
			// Default lookup: header then form. Query param lookup for CSRF is less common/secure.
			lookupString = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
		}
		parts := strings.Split(lookupString, ",")
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
			case "query": // Kept for flexibility, but use with caution for CSRF tokens.
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported: header, form, query.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		// This should ideally be caught by the default lookupString construction if both HeaderName and FormFieldName are empty,
		// but an explicit panic is safer.
		panic("xylium: CSRF TokenLookup or Extractor must result in at least one token extraction method.")
	}

	errorHandler := config.ErrorHandler
	if errorHandler == nil {
		// Default error handler for CSRF failures.
		errorHandler = func(c *Context, errCause error) error {
			// Default to 403 Forbidden.
			return NewHTTPError(StatusForbidden, "CSRF token validation failed. Access denied.").WithInternal(errCause)
		}
	}

	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "CSRF"})

			// --- Token Generation and Setting for the *Response* (Rolling Token) ---
			// A new token is always generated and set in the response cookie for the next request.
			// This new token is also made available in the current request's context store.
			tokenForResponseCookie, errGen := GenerateRandomStringBase64(config.TokenLength)
			if errGen != nil {
				logger.Errorf("Failed to generate new CSRF security token for response: %v", errGen)
				// Pass the generation error to the configured error handler.
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, errGen)
				return errorHandler(c, NewHTTPError(StatusInternalServerError, "Could not generate security token.").WithInternal(errGen))
			}

			// Set the new token in the outgoing response cookie.
			responseCookie := fasthttp.AcquireCookie() // Use fasthttp's pool for cookies.
			responseCookie.SetKey(config.CookieName)
			responseCookie.SetValue(tokenForResponseCookie)
			responseCookie.SetPath(config.CookiePath)
			responseCookie.SetDomain(config.CookieDomain)

			if config.CookieMaxAge > 0 {
				responseCookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
			} else if config.CookieMaxAge < 0 { // Explicitly delete cookie
				responseCookie.SetMaxAge(-1) // Set a past MaxAge or use Expire. Fasthttp SetMaxAge handles this.
			} else { // config.CookieMaxAge == 0, session cookie
				responseCookie.SetMaxAge(0)
			}

			// Dereference pointer booleans for CookieSecure and CookieHTTPOnly.
			responseCookie.SetSecure(*config.CookieSecure)
			responseCookie.SetHTTPOnly(*config.CookieHTTPOnly)
			responseCookie.SetSameSite(config.CookieSameSite)

			c.SetCookie(responseCookie)            // Adds Set-Cookie header to the response.
			fasthttp.ReleaseCookie(responseCookie) // Release cookie back to pool.

			// Store the token that will be in the response cookie into the current request's context.
			// This allows handlers for the *current* request to access this token if they need to
			// embed it in HTML forms that will be submitted with the *next* request.
			c.Set(config.ContextTokenKey, tokenForResponseCookie)

			// Minimal logging for the new token being set. Avoid logging the token itself in production.
			if c.RouterMode() == DebugMode {
				tokenSuffix := ""
				if len(tokenForResponseCookie) > 4 {
					tokenSuffix = tokenForResponseCookie[len(tokenForResponseCookie)-4:]
				}
				logger.Debugf("CSRF: New token for next request (ends with ****%s) set in context ('%s') and response cookie ('%s'). Path: %s %s",
					tokenSuffix, config.ContextTokenKey, config.CookieName, c.Method(), c.Path())
			}

			// --- Check if method is safe (GET, HEAD, OPTIONS, TRACE by default) ---
			// If the method is safe, no CSRF validation is required for the current request.
			_, methodIsSafe := safeMethodsMap[c.Method()]
			if methodIsSafe {
				return next(c) // Proceed to the next handler.
			}

			// --- Method is UNSAFE (e.g., POST, PUT, DELETE), perform CSRF validation ---

			// 1. Retrieve the token from the *incoming request's* CSRF cookie.
			// This is the token that was set by a previous response from our server.
			tokenFromRequestCookie := string(c.Ctx.Request.Header.Cookie(config.CookieName))

			if tokenFromRequestCookie == "" {
				logger.Warnf("CSRF: Validation failed for unsafe method %s %s. Reason: CSRF cookie ('%s') missing from request.",
					c.Method(), c.Path(), config.CookieName)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			// 2. Extract the token submitted by the client in the request data (header, form, or query).
			var tokenFromRequestData string
			var extractionError error // To store any error from custom extractor.

			if config.Extractor != nil {
				// Use custom extractor function if provided.
				tokenFromRequestData, extractionError = config.Extractor(c)
				if extractionError != nil {
					logger.Warnf("CSRF: Custom token extractor failed for %s %s: %v", c.Method(), c.Path(), extractionError)
					c.Set(ConfiguredCSRFErrorHandlerErrorKey, extractionError) // Pass specific extractor error
					return errorHandler(c, extractionError)
				}
			} else {
				// Use standard token lookup (header, form, query based on config.TokenLookup).
				for _, extractorFunc := range tokenExtractors {
					token, errLoopExt := extractorFunc(c) // errLoopExt is currently always nil for built-in extractors.
					if errLoopExt != nil {                // Defensive check for future.
						logger.Warnf("CSRF: Token lookup source failed for %s %s: %v", c.Method(), c.Path(), errLoopExt)
						// If one extractor errors, we might not want to immediately fail if others can succeed.
						// For now, we'll let it try other extractors.
						// If all fail to find a token, tokenFromRequestData will remain empty.
					}
					if token != "" {
						tokenFromRequestData = token
						break // Token found, stop searching.
					}
				}
			}

			if tokenFromRequestData == "" {
				logger.Warnf("CSRF: Validation failed for unsafe method %s %s. Reason: CSRF token missing from request data (expected in header/form/query via configured lookup).",
					c.Method(), c.Path())
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			// 3. Compare the token from the request cookie with the token from the request data.
			// Must use constant-time comparison to prevent timing attacks.
			cookieTokenBytes := []byte(tokenFromRequestCookie)
			requestDataTokenBytes := []byte(tokenFromRequestData)
			tokensMatch := false

			// subtle.ConstantTimeCompare requires slices of the same length.
			// If lengths differ, it's an automatic mismatch.
			if len(cookieTokenBytes) > 0 && len(cookieTokenBytes) == len(requestDataTokenBytes) {
				if subtle.ConstantTimeCompare(cookieTokenBytes, requestDataTokenBytes) == 1 {
					tokensMatch = true
				}
			}

			if !tokensMatch {
				logMessage := fmt.Sprintf("CSRF: Validation failed for unsafe method %s %s. Reason: Token mismatch.", c.Method(), c.Path())
				if len(cookieTokenBytes) != len(requestDataTokenBytes) {
					logMessage += fmt.Sprintf(" Submitted token length (%d) does not match cookie token length (%d).", len(requestDataTokenBytes), len(cookieTokenBytes))
				}
				// Avoid logging actual token values unless in extreme debug scenarios with secure logs.
				logger.Warnf(logMessage)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			// CSRF token validation successful.
			logger.Debugf("CSRF: Token validated successfully (constant-time comparison) for unsafe method %s %s.", c.Method(), c.Path())
			return next(c) // Proceed to the next handler.
		}
	}
}
