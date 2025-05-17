package xylium

import (
	"github.com/google/uuid" // Untuk menghasilkan UUID
)

// DefaultRequestIDHeader adalah nama header default untuk Request ID.
const DefaultRequestIDHeader = "X-Request-ID"

// ContextKeyRequestID adalah kunci yang digunakan untuk menyimpan Request ID di context store.
const ContextKeyRequestID = "xylium_request_id"

// RequestIDConfig mendefinisikan konfigurasi untuk middleware RequestID.
type RequestIDConfig struct {
	// Generator adalah fungsi untuk menghasilkan ID.
	// Defaultnya menggunakan UUID v4.
	Generator func() string
	// HeaderName adalah nama header HTTP untuk Request ID.
	// Default: "X-Request-ID".
	HeaderName string
}

// RequestID mengembalikan middleware RequestID dengan konfigurasi default.
func RequestID() Middleware {
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig mengembalikan middleware RequestID dengan konfigurasi yang diberikan.
func RequestIDWithConfig(config RequestIDConfig) Middleware {
	if config.Generator == nil {
		config.Generator = func() string {
			return uuid.NewString()
		}
	}
	if config.HeaderName == "" {
		config.HeaderName = DefaultRequestIDHeader
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Dapatkan Request ID dari header request jika ada (misalnya, dari proxy atau klien)
			rid := c.Header(config.HeaderName)
			if rid == "" {
				// Jika tidak ada, generate yang baru
				rid = config.Generator()
			}

			// Set Request ID di context store agar bisa diakses oleh handler lain atau logger
			c.Set(ContextKeyRequestID, rid)

			// Set Request ID di header response agar klien bisa melihatnya
			c.SetHeader(config.HeaderName, rid)

			return next(c)
		}
	}
}
