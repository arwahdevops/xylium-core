package xylium

import (
	"crypto/rand"     // For cryptographically secure random number generation for tokens.
	"crypto/subtle"   // For constant-time string comparison to prevent timing attacks.
	"encoding/base64" // For encoding random bytes into a string token.
	"errors"          // For defining standard error types like ErrorCSRFTokenInvalid.
	"fmt"             // For formatting error messages and panic messages.
	"reflect"         // Added for reflect.DeepEqual (or other reflection needs if any)
	"strings"         // For string manipulation (splitting TokenLookup, trimming).
	"time"            // For cookie expiration (MaxAge).

	"github.com/valyala/fasthttp" // For fasthttp.Cookie and cookie SameSite constants.
)

// CSRFConfig defines the configuration options for the CSRF (Cross-Site Request Forgery)
// protection middleware. This middleware helps protect against CSRF attacks by requiring
// a secret, unique, and unpredictable token to be submitted with state-changing requests
// (typically non-safe HTTP methods like POST, PUT, DELETE).
//
// Xylium's CSRF middleware typically employs the "Double Submit Cookie" pattern, where:
//  1. A CSRF token is generated and set in an HTTP cookie sent to the client.
//  2. This token is also made available to the server-side application (e.g., via `c.Get(ContextTokenKey)`).
//  3. For subsequent unsafe requests, the client must include this token in the request
//     (e.g., in a custom HTTP header like "X-CSRF-Token" or a form field).
//  4. The middleware then validates that the token from the request data matches the
//     token found in the request's CSRF cookie.
//
// A new token is typically generated and set in the response cookie for each request
// (rolling token) to enhance security.
type CSRFConfig struct {
	// TokenLength specifies the length, in bytes, of the random data used to generate
	// the CSRF token before Base64 encoding. A longer length increases entropy.
	// Default: 32 bytes (from `DefaultCSRFConfig`), resulting in a Base64 string of ~43 chars.
	TokenLength int

	// CookieName is the name of the HTTP cookie used to store the CSRF token.
	// Default: "_csrf_token" (from `DefaultCSRFConfig`).
	CookieName string
	// CookiePath is the path attribute for the CSRF cookie.
	// Default: "/" (cookie is valid for all paths on the domain).
	CookiePath string
	// CookieDomain is the domain attribute for the CSRF cookie.
	// If empty, the browser defaults to the host of the current document URL.
	// Default: "" (empty string).
	CookieDomain string
	// CookieMaxAge specifies the maximum age of the CSRF cookie in seconds.
	// - If > 0, it sets Max-Age.
	// - If == 0, it's a session cookie (deleted when the browser closes). `DefaultCSRFConfig` sets 12 hours.
	// - If < 0, the cookie is deleted immediately (Max-Age=0 and Expires to a past date).
	// Default: 12 * time.Hour (from `DefaultCSRFConfig`).
	CookieMaxAge time.Duration
	// CookieSecure, if true, sets the Secure attribute on the CSRF cookie, meaning
	// it will only be sent by the browser over HTTPS connections.
	// It's a pointer to distinguish between not set (use default) vs. explicitly false.
	// Default: true (from `DefaultCSRFConfig`). Should be false for local HTTP development.
	CookieSecure *bool
	// CookieHTTPOnly, if true, sets the HttpOnly attribute on the CSRF cookie,
	// preventing client-side JavaScript from accessing it.
	// **IMPORTANT**: If your frontend (e.g., a Single Page Application) needs to read the
	// CSRF token from this cookie to send it back in a request header (e.g., X-CSRF-Token),
	// then `CookieHTTPOnly` *must* be set to `false`.
	// It's a pointer to distinguish between not set vs. explicitly false.
	// Default: true (from `DefaultCSRFConfig`).
	CookieHTTPOnly *bool
	// CookieSameSite defines the SameSite attribute for the CSRF cookie, controlling
	// whether it's sent with cross-site requests. Common values include
	// `fasthttp.CookieSameSiteLaxMode` (default), `fasthttp.CookieSameSiteStrictMode`,
	// or `fasthttp.CookieSameSiteNoneMode` (requires Secure attribute).
	// Default: `fasthttp.CookieSameSiteLaxMode` (from `DefaultCSRFConfig`).
	CookieSameSite fasthttp.CookieSameSite

	// HeaderName is the name of the HTTP request header from which the middleware
	// will attempt to extract the submitted CSRF token for validation.
	// This is one of the common ways SPAs send the token.
	// Default: "X-CSRF-Token" (from `DefaultCSRFConfig`).
	HeaderName string
	// FormFieldName is the name of the HTML form field (for `application/x-www-form-urlencoded`
	// or `multipart/form-data` requests) from which the middleware will attempt
	// to extract the submitted CSRF token.
	// Default: "_csrf" (from `DefaultCSRFConfig`).
	FormFieldName string

	// SafeMethods is a list of HTTP methods that are considered "safe" and therefore
	// do not require CSRF token validation. Requests using these methods will typically
	// still receive a new CSRF token in a response cookie (rolling token).
	// Default: []string{"GET", "HEAD", "OPTIONS", "TRACE"} (from `DefaultCSRFConfig`).
	SafeMethods []string

	// ErrorHandler is a custom function to be invoked when CSRF validation fails
	// (e.g., token mismatch, token missing from request data or cookie).
	// It receives the `xylium.Context` and the specific error cause (e.g., `ErrorCSRFTokenInvalid`).
	// The handler is responsible for sending an appropriate HTTP error response to the client.
	// If nil, a default error handler is used, which typically sends an HTTP 403 Forbidden response.
	ErrorHandler func(c *Context, err error) error

	// TokenLookup specifies a comma-separated string defining where and in what order
	// to look for the submitted CSRF token in the incoming request.
	// Each part is "source:name", e.g., "header:X-CSRF-Token,form:_csrf,query:csrf_value".
	// Supported sources: "header", "form", "query".
	// If `Extractor` is set, `TokenLookup` is ignored.
	// If both `Extractor` and `TokenLookup` are empty, it defaults to looking in
	// the header specified by `HeaderName`, then the form field by `FormFieldName`.
	// Query parameter lookup for CSRF tokens is generally less common and potentially less secure.
	TokenLookup string
	// Extractor is a custom function that can be provided to extract the submitted
	// CSRF token from the `xylium.Context`. If set, this function takes precedence
	// over `TokenLookup` and the default extraction logic (HeaderName, FormFieldName).
	// It should return the extracted token string and an error if extraction fails.
	// A nil error with an empty token string means the token was not found by this extractor.
	Extractor func(c *Context) (string, error)

	// ContextTokenKey is the key used to store the CSRF token (that will be set in the
	// response cookie for the *next* request) in the current request's `xylium.Context`
	// store (`c.store`). Handlers for the *current* request can retrieve this token
	// using `c.Get(config.ContextTokenKey)` if they need to embed it in HTML forms
	// or provide it to client-side JavaScript.
	// Default: `xylium.ContextKeyCSRFToken` (value: "csrf_token") (from `DefaultCSRFConfig`).
	ContextTokenKey string
}

// ErrorCSRFTokenInvalid is a standard error returned or used as a cause when
// CSRF token validation fails due to a missing, invalid, or mismatched token.
// This can be checked using `errors.Is(err, ErrorCSRFTokenInvalid)` in custom error handlers.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

// ConfiguredCSRFErrorHandlerErrorKey is the key used in `xylium.Context.store` to pass
// the specific underlying error (e.g., `ErrorCSRFTokenInvalid`, or an error from
// token generation or a custom extractor) to a user-configured `CSRFConfig.ErrorHandler`.
// The custom error handler can retrieve this using `c.Get(ConfiguredCSRFErrorHandlerErrorKey)`.
const ConfiguredCSRFErrorHandlerErrorKey = "xylium_csrf_validation_cause_error"

// DefaultCSRFConfig provides a `CSRFConfig` instance initialized with sensible default values.
// These defaults aim for a good balance of security and usability.
//
// Key Defaults:
//   - TokenLength: 32 bytes (for strong entropy).
//   - CookieName: "_csrf_token".
//   - CookiePath: "/".
//   - CookieMaxAge: 12 hours (token refreshed on each request anyway).
//   - CookieSecure: true (HTTPS only). **Set to false for local HTTP development.**
//   - CookieHTTPOnly: true (JS cannot access). **Set to false if SPA needs to read from cookie.**
//   - CookieSameSite: `fasthttp.CookieSameSiteLaxMode`.
//   - HeaderName: "X-CSRF-Token".
//   - FormFieldName: "_csrf".
//   - SafeMethods: {"GET", "HEAD", "OPTIONS", "TRACE"}.
//   - ContextTokenKey: `xylium.ContextKeyCSRFToken` ("csrf_token").
//   - ErrorHandler: Default sends HTTP 403 Forbidden.
//
// **Important Security Notes on Defaults:**
//   - `CookieSecure` defaults to `true`. For local development over HTTP, you
//     will need to explicitly set `myCSRFConfig.CookieSecure = &falseVar` where `falseVar` is `false`.
//   - `CookieHTTPOnly` defaults to `true`. If your client-side JavaScript (e.g., in an SPA)
//     needs to read the CSRF token from the cookie to send it in a header, you *must*
//     set `myCSRFConfig.CookieHTTPOnly = &falseVar`.
var DefaultCSRFConfig = func() CSRFConfig {
	defaultSecure := true
	defaultHttpOnly := true
	return CSRFConfig{
		TokenLength:     32,
		CookieName:      "_csrf_token",
		CookiePath:      "/",
		CookieDomain:    "",
		CookieMaxAge:    12 * time.Hour,
		CookieSecure:    &defaultSecure,
		CookieHTTPOnly:  &defaultHttpOnly,
		CookieSameSite:  fasthttp.CookieSameSiteLaxMode,
		HeaderName:      "X-CSRF-Token",
		FormFieldName:   "_csrf",
		SafeMethods:     []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
		ContextTokenKey: ContextKeyCSRFToken,
	}
}()

// CSRF returns a new CSRF protection `Middleware` configured with default settings
// from `DefaultCSRFConfig`.
// For custom behavior, use `CSRFWithConfig`.
//
// **Remember to review `DefaultCSRFConfig` security notes, especially regarding
// `CookieSecure` (for HTTP development) and `CookieHTTPOnly` (for SPAs).**
func CSRF() Middleware {
	cfgCopy := DefaultCSRFConfig
	if cfgCopy.CookieSecure == nil {
		s := true
		cfgCopy.CookieSecure = &s
	}
	if cfgCopy.CookieHTTPOnly == nil {
		h := true
		cfgCopy.CookieHTTPOnly = &h
	}
	return CSRFWithConfig(cfgCopy)
}

// GenerateRandomStringBase64 is a variable holding a function that generates a
// cryptographically secure random string of `lengthInBytes` (before Base64 encoding),
// encoded using Base64 Raw URL Encoding (no padding, URL-safe).
// This is used by the CSRF middleware to create unpredictable tokens.
//
// It's exported as a variable primarily to allow mocking during unit testing,
// enabling deterministic token generation for predictable test outcomes.
// In production, it uses `crypto/rand`.
// Defaults to 32 bytes if `lengthInBytes` is non-positive.
var GenerateRandomStringBase64 = func(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 {
		lengthInBytes = 32
	}
	randomBytes := make([]byte, lengthInBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes for CSRF token generation: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

// CSRFWithConfig returns a new CSRF protection `Middleware` configured with the
// options provided in the `config` argument.
// This function normalizes the `config` by applying defaults from `DefaultCSRFConfig`
// for any fields that are not explicitly set.
//
// Panics:
//   - If `TokenLookup` is malformed (e.g., "header:", "form:name1,badsyntax").
//   - If `TokenLookup` sources are unsupported (valid: "header", "form", "query").
//   - If, after resolving `Extractor` and `TokenLookup`, no token extraction methods are defined.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize Configuration: Apply defaults if fields are not set ---
	if config.TokenLength <= 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}

	// Check if config is the zero value of CSRFConfig for CookieMaxAge handling.
	// This means the user provided an empty CSRFConfig{} struct.
	isZeroValueConfigForMaxAge := false
	if config.CookieMaxAge == 0 {
		// A more robust check if it's truly a zero-value struct passed by user for this field.
		// This can be tricky if other fields are also zero by default.
		// For now, we assume if CookieMaxAge is 0 in the passed config, and
		// DefaultCSRFConfig.CookieMaxAge is not 0, then the user *might* intend session cookie
		// OR they passed a zero struct.
		// A simple way is if the passed 'config' is entirely its zero value.
		if reflect.DeepEqual(config, CSRFConfig{}) { // Check if the whole struct is zero-value
			isZeroValueConfigForMaxAge = true
		}
	}

	if config.CookieMaxAge == 0 && (isZeroValueConfigForMaxAge && DefaultCSRFConfig.CookieMaxAge != 0) {
		// If user passed a completely empty CSRFConfig{}, and default MaxAge is non-zero, use default.
		config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge
	}
	// If user explicitly set config.CookieMaxAge = 0 (meaning session cookie), it will be respected.
	// If user set config.CookieMaxAge to a non-zero value, that will be used.

	if config.CookieSecure == nil {
		config.CookieSecure = DefaultCSRFConfig.CookieSecure
	}
	if config.CookieHTTPOnly == nil {
		config.CookieHTTPOnly = DefaultCSRFConfig.CookieHTTPOnly
	}

	if config.CookieSameSite == fasthttp.CookieSameSiteDefaultMode {
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

	var tokenExtractors []func(c *Context) (string, error)
	if config.Extractor != nil {
		tokenExtractors = append(tokenExtractors, config.Extractor)
	} else {
		lookupString := config.TokenLookup
		if lookupString == "" {
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
				panic(fmt.Errorf("xylium: invalid CSRF TokenLookup format in part: '%s'. Expected 'source:name' (e.g., 'header:X-My-Token').", trimmedPart))
			}
			source := strings.ToLower(strings.TrimSpace(segments[0]))
			name := strings.TrimSpace(segments[1])

			switch source {
			case "header":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.Header(name), nil })
			case "form":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.FormValue(name), nil })
			case "query":
				tokenExtractors = append(tokenExtractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'. Supported sources are 'header', 'form', 'query'.", source))
			}
		}
	}
	if len(tokenExtractors) == 0 {
		panic("xylium: CSRF configuration must result in at least one token extraction method (via Extractor or TokenLookup/HeaderName/FormFieldName).")
	}

	errorHandler := config.ErrorHandler
	if errorHandler == nil {
		errorHandler = func(c *Context, errCause error) error {
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

			tokenForResponseCookie, errGen := GenerateRandomStringBase64(config.TokenLength)
			if errGen != nil {
				logger.Errorf("Failed to generate new CSRF security token for response: %v", errGen)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, errGen)
				return errorHandler(c, NewHTTPError(StatusInternalServerError, "Could not generate security token for CSRF protection.").WithInternal(errGen))
			}

			responseCookie := fasthttp.AcquireCookie()
			responseCookie.SetKey(config.CookieName)
			responseCookie.SetValue(tokenForResponseCookie)
			responseCookie.SetPath(config.CookiePath)
			responseCookie.SetDomain(config.CookieDomain)

			if config.CookieMaxAge > 0 {
				responseCookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
			} else if config.CookieMaxAge < 0 {
				responseCookie.SetMaxAge(-1)
			} else {
				responseCookie.SetMaxAge(0)
			}

			responseCookie.SetSecure(*config.CookieSecure)
			responseCookie.SetHTTPOnly(*config.CookieHTTPOnly)
			responseCookie.SetSameSite(config.CookieSameSite)

			c.SetCookie(responseCookie)
			fasthttp.ReleaseCookie(responseCookie)

			c.Set(config.ContextTokenKey, tokenForResponseCookie)

			if c.RouterMode() == DebugMode {
				tokenSuffix := ""
				if len(tokenForResponseCookie) > 4 {
					tokenSuffix = tokenForResponseCookie[len(tokenForResponseCookie)-4:]
				}
				logger.Debugf("CSRF: New token for next request (ends with ...%s) set in context key '%s' and response cookie '%s'. Request: %s %s",
					tokenSuffix, config.ContextTokenKey, config.CookieName, c.Method(), c.Path())
			}

			_, methodIsSafe := safeMethodsMap[c.Method()]
			if methodIsSafe {
				logger.Debugf("CSRF: Method %s is safe for path %s. Skipping CSRF validation for this request.", c.Method(), c.Path())
				return next(c)
			}

			logger.Debugf("CSRF: Method %s is unsafe for path %s. Performing CSRF token validation.", c.Method(), c.Path())

			tokenFromRequestCookie := string(c.Ctx.Request.Header.Cookie(config.CookieName))

			if tokenFromRequestCookie == "" {
				logger.Warnf("CSRF: Validation failed for %s %s. Reason: CSRF cookie ('%s') missing from incoming request.",
					c.Method(), c.Path(), config.CookieName)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			var tokenFromRequestData string
			var extractionError error

			for _, extractorFunc := range tokenExtractors {
				token, errLoopExt := extractorFunc(c)
				if errLoopExt != nil {
					logger.Warnf("CSRF: Token extractor failed for %s %s: %v", c.Method(), c.Path(), errLoopExt)
					extractionError = errLoopExt
					break
				}
				if token != "" {
					tokenFromRequestData = token
					break
				}
			}

			if extractionError != nil {
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, extractionError)
				return errorHandler(c, extractionError)
			}
			if tokenFromRequestData == "" {
				logger.Warnf("CSRF: Validation failed for %s %s. Reason: CSRF token missing from request data (expected in sources: %s).",
					c.Method(), c.Path(), config.TokenLookup)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			cookieTokenBytes := []byte(tokenFromRequestCookie)
			requestDataTokenBytes := []byte(tokenFromRequestData)
			tokensMatch := false

			if len(cookieTokenBytes) > 0 && len(cookieTokenBytes) == len(requestDataTokenBytes) {
				if subtle.ConstantTimeCompare(cookieTokenBytes, requestDataTokenBytes) == 1 {
					tokensMatch = true
				}
			}

			if !tokensMatch {
				logMessage := fmt.Sprintf("CSRF: Validation failed for %s %s. Reason: Token mismatch.", c.Method(), c.Path())
				if len(cookieTokenBytes) != len(requestDataTokenBytes) {
					logMessage += fmt.Sprintf(" Submitted token length (%d) does not match cookie token length (%d).", len(requestDataTokenBytes), len(cookieTokenBytes))
				}
				logger.Warnf(logMessage)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			logger.Debugf("CSRF: Token validated successfully (constant-time comparison) for unsafe method %s %s.", c.Method(), c.Path())
			return next(c)
		}
	}
}
