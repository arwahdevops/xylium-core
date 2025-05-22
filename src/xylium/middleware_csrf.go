// src/xylium/middleware_csrf.go
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
	CookieMaxAge    time.Duration           // Durasi, 0 untuk session, <0 untuk delete
	CookieSecure    *bool                   // Pointer agar bisa bedakan antara tidak diset vs false eksplisit
	CookieHTTPOnly  *bool                   // Pointer agar bisa bedakan antara tidak diset vs false eksplisit
	CookieSameSite  fasthttp.CookieSameSite // fasthttp.CookieSameSite enum (0 adalah DefaultMode)
	HeaderName      string
	FormFieldName   string
	SafeMethods     []string
	ErrorHandler    func(c *Context, err error) error
	TokenLookup     string
	Extractor       func(c *Context) (string, error)
	ContextTokenKey string
}

var ErrorCSRFTokenInvalid = errors.New("xylium: invalid, missing, or mismatched CSRF token")

const ConfiguredCSRFErrorHandlerErrorKey = "csrf_validation_cause"

// DefaultCSRFConfig provides sensible default configurations for CSRF protection.
// Boolean fields CookieSecure dan CookieHTTPOnly diset ke pointer agar kita bisa tahu
// apakah pengguna secara eksplisit menyetelnya atau tidak di config yang di-pass.
var DefaultCSRFConfig = func() CSRFConfig {
	secureTrue := true
	httpOnlyTrue := true
	return CSRFConfig{
		TokenLength:     32,
		CookieName:      "_csrf_token",
		CookiePath:      "/",
		CookieMaxAge:    12 * time.Hour,
		CookieSecure:    &secureTrue,   // Default true
		CookieHTTPOnly:  &httpOnlyTrue, // Default true
		CookieSameSite:  fasthttp.CookieSameSiteLaxMode,
		HeaderName:      "X-CSRF-Token",
		FormFieldName:   "_csrf",
		SafeMethods:     []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
		ContextTokenKey: ContextKeyCSRFToken,
	}
}()

// CSRF returns a CSRF protection middleware with default configuration.
func CSRF() Middleware {
	cfgCopy := DefaultCSRFConfig // Salin default agar tidak termodifikasi global
	return CSRFWithConfig(cfgCopy)
}

// GenerateRandomStringBase64 (tetap sebagai variabel fungsi global untuk mocking di tes)
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

func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalize Configuration ---
	// Gunakan nilai dari DefaultCSRFConfig jika field di 'config' tidak disetel (nilai zero atau nil untuk pointer)

	if config.TokenLength <= 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}
	// Untuk CookieMaxAge: jika pengguna set 0, itu valid (session cookie).
	// Jika pengguna tidak set (masih zero value dari time.Duration), baru pakai default.
	// Ini sulit dibedakan. Asumsikan jika 0, itu intensi pengguna untuk session.
	// DefaultCSRFConfig.CookieMaxAge adalah 12 jam. Jika pengguna ingin session, mereka harus set 0.
	// Jika pengguna pass config kosong, CookieMaxAge akan 0.
	// Perilaku saat ini: Jika config.CookieMaxAge == 0, maka akan jadi session cookie.
	// Jika ingin default 12 jam jika config.CookieMaxAge == 0, perlu logika tambahan.
	// Mari kita asumsikan 0 di config = session cookie, ini lebih intuitif.
	// DefaultCSRFConfig memiliki MaxAge, jadi jika config adalah DefaultCSRFConfig, itu akan dipakai.

	// Untuk boolean (CookieSecure, CookieHTTPOnly), gunakan pointer di CSRFConfig
	// agar bisa membedakan antara "tidak disetel" (nil) dan "disetel ke false".
	if config.CookieSecure == nil {
		config.CookieSecure = DefaultCSRFConfig.CookieSecure // Ambil dari default
	}
	if config.CookieHTTPOnly == nil {
		config.CookieHTTPOnly = DefaultCSRFConfig.CookieHTTPOnly // Ambil dari default
	}

	// Untuk CookieSameSite, 0 adalah fasthttp.CookieSameSiteDefaultMode,
	// yang mungkin tidak sama dengan LaxMode. Jadi kita set default eksplisit.
	if config.CookieSameSite == fasthttp.CookieSameSiteDefaultMode { // 0
		config.CookieSameSite = DefaultCSRFConfig.CookieSameSite // Default ke Lax
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
		// Jika TokenLookup kosong, buat default lookup menggunakan HeaderName dan FormFieldName yang sudah dinormalisasi.
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
		errorHandler = func(c *Context, errCause error) error {
			errMsg := "CSRF token validation failed. Access denied."
			return NewHTTPError(StatusForbidden, errMsg).WithInternal(errCause)
		}
	}

	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "CSRF"})
			var currentTokenForSession string

			newToken, errGen := GenerateRandomStringBase64(config.TokenLength)
			if errGen != nil {
				logger.Errorf("Failed to generate new CSRF security token: %v", errGen)
				return NewHTTPError(StatusInternalServerError, "Could not generate security token.").WithInternal(errGen)
			}
			currentTokenForSession = newToken

			cookie := fasthttp.AcquireCookie()
			defer fasthttp.ReleaseCookie(cookie)

			cookie.SetKey(config.CookieName)
			cookie.SetValue(currentTokenForSession)
			cookie.SetPath(config.CookiePath)
			cookie.SetDomain(config.CookieDomain)
			if config.CookieMaxAge > 0 {
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
			} else if config.CookieMaxAge < 0 {
				cookie.SetMaxAge(-1)
			} else { // config.CookieMaxAge == 0
				cookie.SetMaxAge(0) // Session cookie
			}
			// Gunakan nilai boolean setelah dereference pointer (yang sudah dinormalisasi)
			cookie.SetSecure(*config.CookieSecure)
			cookie.SetHTTPOnly(*config.CookieHTTPOnly)
			cookie.SetSameSite(config.CookieSameSite)
			c.SetCookie(cookie)

			c.Set(config.ContextTokenKey, currentTokenForSession)

			// Log disederhanakan
			logTokenSuffix := ""
			if len(currentTokenForSession) > 4 {
				logTokenSuffix = currentTokenForSession[len(currentTokenForSession)-4:]
			}
			logger.Debugf("CSRF token set/refreshed in cookie '%s' and context key '%s'. Token ends with: ****%s (path: %s %s)",
				config.CookieName, config.ContextTokenKey, logTokenSuffix, c.Method(), c.Path())

			_, methodIsSafe := safeMethodsMap[c.Method()]
			if methodIsSafe {
				return next(c)
			}

			expectedToken := currentTokenForSession

			var tokenFromRequest string
			var extractionProcessError error

			if config.Extractor != nil {
				token, errExt := config.Extractor(c)
				if errExt != nil {
					logger.Errorf("Custom CSRF token extractor failed: %v (path: %s %s)", errExt, c.Method(), c.Path())
					extractionProcessError = errExt
				} else {
					tokenFromRequest = token
				}
			} else {
				for _, extractorFunc := range tokenExtractors {
					token, errLoopExt := extractorFunc(c)
					if errLoopExt != nil {
						logger.Warnf("CSRF token lookup source failed: %v (path: %s %s)", errLoopExt, c.Method(), c.Path())
						if extractionProcessError == nil {
							extractionProcessError = errLoopExt
						}
					}
					if token != "" {
						tokenFromRequest = token
						extractionProcessError = nil
						break
					}
				}
			}

			if extractionProcessError != nil {
				logger.Warnf("CSRF token extraction process encountered an error: %v (path: %s %s)", extractionProcessError, c.Method(), c.Path())
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, extractionProcessError)
				return errorHandler(c, extractionProcessError)
			}

			if tokenFromRequest == "" {
				logger.Warnf("No CSRF token found in request via configured methods for unsafe method %s %s.", c.Method(), c.Path())
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			expectedTokenBytes := []byte(expectedToken)
			requestTokenBytes := []byte(tokenFromRequest)
			tokensMatch := false

			if len(requestTokenBytes) > 0 && len(requestTokenBytes) == len(expectedTokenBytes) {
				if subtle.ConstantTimeCompare(expectedTokenBytes, requestTokenBytes) == 1 {
					tokensMatch = true
				}
			}

			if !tokensMatch {
				logMessage := fmt.Sprintf("CSRF token mismatch for unsafe method %s %s.", c.Method(), c.Path())
				if len(requestTokenBytes) == 0 {
					logMessage += " Client did not submit a CSRF token."
				} else if len(expectedTokenBytes) != len(requestTokenBytes) {
					logMessage += fmt.Sprintf(" Submitted token length (%d) does not match expected token length (%d).", len(requestTokenBytes), len(expectedTokenBytes))
				} else {
					logMessage += " Submitted token does not match the expected token."
				}
				logger.Warnf(logMessage)
				c.Set(ConfiguredCSRFErrorHandlerErrorKey, ErrorCSRFTokenInvalid)
				return errorHandler(c, ErrorCSRFTokenInvalid)
			}

			logger.Debugf("CSRF token validated successfully (constant time) for unsafe method %s %s.", c.Method(), c.Path())
			return next(c)
		}
	}
}
