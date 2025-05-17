package xylium

import (
	"encoding/base64"
	"errors"
	"strings"
)

// BasicAuthConfig mendefinisikan konfigurasi untuk middleware BasicAuth.
type BasicAuthConfig struct {
	// Validator adalah fungsi yang memvalidasi username dan password.
	// Harus mengembalikan true jika valid, dan user (opsional) atau error.
	// User yang dikembalikan bisa disimpan di context.
	Validator func(username, password string, c *Context) (user interface{}, valid bool, err error)

	// Realm adalah realm yang ditampilkan di dialog otentikasi browser.
	// Default: "Restricted".
	Realm string

	// ErrorHandler adalah fungsi kustom untuk menangani error otentikasi.
	// Jika nil, default handler akan mengirim StatusUnauthorized dengan header WWW-Authenticate.
	ErrorHandler HandlerFunc
}

// ErrorBasicAuthInvalid adalah error yang dikembalikan jika kredensial Basic Auth tidak valid.
var ErrorBasicAuthInvalid = errors.New("xylium: invalid basic auth credentials")

// BasicAuth mengembalikan middleware BasicAuth.
// Validator harus disediakan.
func BasicAuth(validator func(username, password string, c *Context) (interface{}, bool, error)) Middleware {
	config := BasicAuthConfig{
		Validator: validator,
		Realm:     "Restricted", // Realm default
	}
	return BasicAuthWithConfig(config)
}

// BasicAuthWithConfig mengembalikan middleware BasicAuth dengan konfigurasi yang diberikan.
func BasicAuthWithConfig(config BasicAuthConfig) Middleware {
	if config.Validator == nil {
		panic("xylium: BasicAuth middleware requires a validator function")
	}
	if config.Realm == "" {
		config.Realm = "Restricted"
	}

	errorHandler := config.ErrorHandler
	if errorHandler == nil {
		errorHandler = func(c *Context) error {
			// Kirim header WWW-Authenticate agar browser menampilkan dialog login
			c.SetHeader("WWW-Authenticate", `Basic realm="`+config.Realm+`"`)
			return NewHTTPError(StatusUnauthorized, "Unauthorized.").WithInternal(ErrorBasicAuthInvalid)
		}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			authHeader := c.Header("Authorization")
			if authHeader == "" {
				return errorHandler(c) // Tidak ada header Authorization
			}

			const prefix = "Basic "
			if !strings.HasPrefix(authHeader, prefix) {
				// Format header salah
				c.router.Logger().Printf("BasicAuth: Invalid Authorization header format for %s %s", c.Method(), c.Path())
				return errorHandler(c)
			}

			encodedCredentials := authHeader[len(prefix):]
			credentials, err := base64.StdEncoding.DecodeString(encodedCredentials)
			if err != nil {
				// Gagal decode base64
				c.router.Logger().Printf("BasicAuth: Failed to decode credentials for %s %s: %v", c.Method(), c.Path(), err)
				return errorHandler(c)
			}

			parts := strings.SplitN(string(credentials), ":", 2)
			if len(parts) != 2 {
				// Format username:password salah
				c.router.Logger().Printf("BasicAuth: Invalid credentials format for %s %s", c.Method(), c.Path())
				return errorHandler(c)
			}
			username, password := parts[0], parts[1]

			// Panggil validator
			user, valid, valErr := config.Validator(username, password, c)
			if valErr != nil { // Error dari validator
				c.router.Logger().Printf("BasicAuth: Validator error for user '%s': %v", username, valErr)
				// Mungkin kirim error server internal atau biarkan error handler default
				return NewHTTPError(StatusInternalServerError, "Authentication check failed.").WithInternal(valErr)
			}

			if !valid { // Kredensial tidak valid
				c.router.Logger().Printf("BasicAuth: Invalid credentials for user '%s' for %s %s", username, c.Method(), c.Path())
				return errorHandler(c)
			}

			// Jika valid, simpan user (jika ada) ke context
			if user != nil {
				c.Set("user", user) // Kunci "user" bisa dikonfigurasi jika perlu
			}

			return next(c)
		}
	}
}
