package xylium

import (
	"crypto/rand"     // For cryptographically secure random token generation.
	"crypto/subtle"   // For constant-time string comparison.
	"encoding/base64" // For encoding the token into a URL/header-safe string.
	"errors"          // For defining custom error types.
	"fmt"             // For string formatting in errors and logs.
	"strings"         // For string manipulation (e.g., parsing TokenLookup, method checking).
	"time"            // For cookie expiration management.

	"github.com/valyala/fasthttp" // For fasthttp.Cookie and related constants.
)

// CSRFConfig defines the configuration for the CSRF (Cross-Site Request Forgery) protection middleware.
type CSRFConfig struct {
	TokenLength  int
	CookieName   string
	CookiePath   string
	CookieDomain string
	CookieMaxAge time.Duration
	CookieSecure bool
	// CookieHTTPOnly specifies if the CSRF cookie should be inaccessible to client-side JavaScript.
	// Default (from DefaultCSRFConfig): true (more secure for traditional apps).
	// SPAs using Double Submit Cookie pattern (reading token from this cookie via JS)
	// MUST explicitly set this to `false`.
	CookieHTTPOnly  bool
	CookieSameSite  fasthttp.CookieSameSite
	HeaderName      string
	FormFieldName   string
	SafeMethods     []string
	ErrorHandler    HandlerFunc
	TokenLookup     string
	Extractor       func(c *Context) (string, error)
	ContextTokenKey string
}

// ErrorCSRFTokenInvalid is a standard error indicating an invalid, missing, or mismatched CSRF token.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
var DefaultCSRFConfig = CSRFConfig{
	TokenLength:  32,
	CookieName:   "_csrf_token",
	CookiePath:   "/",
	CookieMaxAge: 12 * time.Hour,
	CookieSecure: true, // Secure by default; override for local HTTP dev.
	// CookieHTTPOnly is now true by default for better security.
	// SPAs needing to read the CSRF token from a cookie via JavaScript (Double Submit Cookie pattern)
	// must explicitly set config.CookieHTTPOnly = false.
	CookieHTTPOnly:  true,
	CookieSameSite:  fasthttp.CookieSameSiteLaxMode, // Lax is a good balance.
	HeaderName:      "X-CSRF-Token",
	FormFieldName:   "_csrf",
	SafeMethods:     []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
	ContextTokenKey: "csrf_token",
}

// CSRF returns a CSRF protection middleware with default configuration (DefaultCSRFConfig).
func CSRF() Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig returns a CSRF protection middleware with the provided configuration.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// Normalize and Validate Configuration
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
	// CookieSecure and CookieHTTPOnly will use user's explicit value or default from DefaultCSRFConfig.
	if config.CookieSameSite == 0 { // 0 is fasthttp.CookieSameSiteDefaultMode
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

	if config.Extractor == nil && config.TokenLookup == "" {
		config.TokenLookup = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
	}

	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

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
			case "query":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported: header, form, query.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		panic("xylium: CSRF TokenLookup or Extractor must be configured with at least one token extraction method.")
	}

	errorHandler := config.ErrorHandler
	if errorHandler == nil {
		errorHandler = func(c *Context) error {
			var internalCause error = ErrorCSRFTokenInvalid
			if errVal, exists := c.Get("csrf_validation_error"); exists {
				if e, ok := errVal.(error); ok {
					internalCause = e
				}
			}
			return NewHTTPError(StatusForbidden, "CSRF token validation failed. Access denied.").WithInternal(internalCause)
		}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "CSRF"})

			tokenFromCookie := c.Cookie(config.CookieName)
			isNewSessionOrTokenExpired := tokenFromCookie == ""
			_, methodIsSafe := safeMethodsMap[c.Method()]

			if methodIsSafe || isNewSessionOrTokenExpired {
				newToken, err := generateRandomStringBase64(config.TokenLength)
				if err != nil {
					logger.Errorf("Failed to generate new security token: %v", err)
					return NewHTTPError(StatusInternalServerError, "Could not generate security token for CSRF protection.").WithInternal(err)
				}
				tokenFromCookie = newToken

				cookie := fasthttp.AcquireCookie()
				defer fasthttp.ReleaseCookie(cookie)

				cookie.SetKey(config.CookieName)
				cookie.SetValue(tokenFromCookie)
				cookie.SetPath(config.CookiePath)
				cookie.SetDomain(config.CookieDomain)
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
				cookie.SetSecure(config.CookieSecure)
				cookie.SetHTTPOnly(config.CookieHTTPOnly) // Uses the configured value.
				cookie.SetSameSite(config.CookieSameSite)
				c.SetCookie(cookie)

				logMsg := "New token generated and set in cookie '%s' for path %s %s."
				if !isNewSessionOrTokenExpired && methodIsSafe {
					logMsg = "Token refreshed/ensured in cookie '%s' for safe method %s %s."
				}
				logger.Debugf(logMsg, config.CookieName, c.Method(), c.Path())
			}

			if tokenFromCookie != "" {
				c.Set(config.ContextTokenKey, tokenFromCookie)
			}

			if !methodIsSafe {
				if tokenFromCookie == "" {
					logger.Warnf("CRITICAL - No token in cookie for unsafe method %s %s. Validation will fail.", c.Method(), c.Path())
					c.Set("csrf_validation_error", errors.New("critical: missing CSRF token in cookie for unsafe method"))
					return errorHandler(c)
				}

				var tokenFromRequest string
				var extractionErr error
				for _, extractorFunc := range tokenExtractors {
					token, err := extractorFunc(c)
					if err != nil {
						extractionErr = err
						break
					}
					if token != "" {
						tokenFromRequest = token
						break
					}
				}

				if extractionErr != nil {
					logger.Errorf("Custom token extractor failed for %s %s: %v", c.Method(), c.Path(), extractionErr)
					c.Set("csrf_validation_error", extractionErr)
					return NewHTTPError(StatusInternalServerError, "CSRF token extraction process failed internally.").WithInternal(extractionErr)
				}

				cookieTokenBytes := []byte(tokenFromCookie)
				requestTokenBytes := []byte(tokenFromRequest)
				tokensMatch := false

				if len(requestTokenBytes) > 0 && len(requestTokenBytes) == len(cookieTokenBytes) {
					if subtle.ConstantTimeCompare(cookieTokenBytes, requestTokenBytes) == 1 {
						tokensMatch = true
					}
				}

				if !tokensMatch {
					logMessage := fmt.Sprintf("Token mismatch or invalid token in request for unsafe method %s %s.", c.Method(), c.Path())
					if tokenFromRequest == "" {
						logMessage += " Client did not submit a CSRF token via configured methods."
					} else if len(cookieTokenBytes) != len(requestTokenBytes) && len(requestTokenBytes) > 0 {
						logMessage += " Submitted token length does not match expected token length."
					} else {
						logMessage += " Submitted token does not match the expected token from the cookie or was missing."
					}
					logger.Warnf(logMessage)
					c.Set("csrf_validation_error", ErrorCSRFTokenInvalid)
					return errorHandler(c)
				}
				logger.Debugf("Token validated successfully (constant time) for unsafe method %s %s.", c.Method(), c.Path())
			}
			return next(c)
		}
	}
}

// generateRandomStringBase64 generates a cryptographically secure random string,
// encoded using URL-safe base64.
func generateRandomStringBase64(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 {
		lengthInBytes = 32
	}
	randomBytes := make([]byte, lengthInBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
