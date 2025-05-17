package xylium

import (
	// "net/http" // TIDAK DIGUNAKAN LAGI
	"strconv"
	"strings"
	// "time" // TIDAK DIGUNAKAN LAGI
)

// CORSConfig mendefinisikan konfigurasi untuk middleware CORS.
type CORSConfig struct {
	// AllowOrigins menentukan origin yang diizinkan.
	// Default adalah []string{"*"} (mengizinkan semua origin).
	// Hati-hati menggunakan "*" di produksi tanpa memahami implikasi keamanannya.
	AllowOrigins []string

	// AllowMethods menentukan metode HTTP yang diizinkan.
	// Default adalah []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "HEAD", "PATCH"}.
	AllowMethods []string

	// AllowHeaders menentukan header request yang diizinkan.
	// Default adalah []string{"Origin", "Content-Type", "Accept", "Authorization"}.
	AllowHeaders []string

	// ExposeHeaders menentukan header response yang aman untuk diekspos ke browser.
	// Default kosong.
	ExposeHeaders []string

	// AllowCredentials menentukan apakah request dapat menyertakan kredensial (misalnya, cookie, header Authorization).
	// Default false. Jika true, AllowOrigins tidak boleh "*".
	AllowCredentials bool

	// MaxAge menentukan berapa lama (dalam detik) hasil dari preflight request (OPTIONS) dapat di-cache.
	// Default 0 (tidak ada cache).
	MaxAge int // dalam detik
}

// DefaultCORSConfig adalah konfigurasi CORS default.
var DefaultCORSConfig = CORSConfig{
	AllowOrigins:     []string{"*"},
	AllowMethods:     []string{MethodGet, MethodPost, MethodPut, MethodDelete, MethodOptions, MethodHead, MethodPatch},
	AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
	ExposeHeaders:    []string{},
	AllowCredentials: false,
	MaxAge:           0,
}

// CORS mengembalikan middleware CORS dengan konfigurasi default.
func CORS() Middleware {
	return CORSWithConfig(DefaultCORSConfig)
}

// CORSWithConfig mengembalikan middleware CORS dengan konfigurasi yang diberikan.
func CORSWithConfig(config CORSConfig) Middleware {
	// Validasi dan normalisasi config
	if len(config.AllowOrigins) == 0 {
		config.AllowOrigins = DefaultCORSConfig.AllowOrigins
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = DefaultCORSConfig.AllowMethods
	}
	if len(config.AllowHeaders) == 0 {
		config.AllowHeaders = DefaultCORSConfig.AllowHeaders
	}

	// PERBAIKAN: Hapus deklarasi allowOriginPatterns karena compileOrigins belum diimplementasikan sepenuhnya
	// allowOriginPatterns := compileOrigins(config.AllowOrigins) 

	allowMethods := strings.Join(config.AllowMethods, ",")
	allowHeaders := strings.Join(config.AllowHeaders, ",")
	exposeHeaders := strings.Join(config.ExposeHeaders, ",")
	maxAge := strconv.Itoa(config.MaxAge)

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			origin := c.Header("Origin")
			// Selalu variasikan berdasarkan Origin untuk caching yang benar
			c.SetHeader("Vary", "Origin")

			// Jika tidak ada Origin, bukan request CORS, lanjutkan.
			if origin == "" {
				return next(c)
			}

			// Cek apakah origin diizinkan
			allowedOrigin := ""
			// Jika AllowOrigins hanya berisi "*" dan kredensial tidak diizinkan
			if len(config.AllowOrigins) == 1 && config.AllowOrigins[0] == "*" && !config.AllowCredentials {
				allowedOrigin = "*"
			} else {
				// Iterasi melalui daftar origin yang diizinkan
				for _, o := range config.AllowOrigins {
					// Jika "*" diizinkan dan kredensial tidak diperlukan, maka set sebagai "*"
					// Ini mengasumsikan jika "*" ada dalam daftar, itu adalah fallback jika tidak ada match spesifik
					// dan kredensial tidak diperlukan.
					if o == "*" && !config.AllowCredentials {
						// Jika kita sudah menemukan match spesifik, jangan override dengan "*"
						if allowedOrigin == "" {
							allowedOrigin = "*" 
						}
						// Jika sudah ada match spesifik, break saja, jangan set ke "*"
						// break 
					} else if o == origin { // Exact match
						allowedOrigin = origin
						break // Ditemukan match spesifik, tidak perlu cek lagi
					}
				}
				// Jika setelah iterasi, allowedOrigin masih kosong dan ada "*" dalam daftar tanpa kredensial,
				// maka gunakan "*". Ini menangani kasus di mana "*" mungkin bukan elemen pertama.
				if allowedOrigin == "" {
					for _, o := range config.AllowOrigins {
						if o == "*" && !config.AllowCredentials {
							allowedOrigin = "*"
							break
						}
					}
				}
			}

			if allowedOrigin == "" { // Origin tidak diizinkan
				// Kita biarkan request berlanjut, browser yang akan memblokir respons jika perlu
				// Ini adalah perilaku yang umum untuk menghindari pembocoran informasi
				// tentang origin mana yang diizinkan/tidak.
				return next(c)
			}


			// --- Tangani Preflight Request (OPTIONS) ---
			if c.Method() == MethodOptions {
				// Tambahkan Vary header untuk metode dan header request
				// karena respons preflight bergantung pada ini.
				if c.Header("Access-Control-Request-Method") != "" {
					c.SetHeader("Vary", "Access-Control-Request-Method")
				}
				if c.Header("Access-Control-Request-Headers") != "" {
					c.SetHeader("Vary", "Access-Control-Request-Headers")
				}

				c.SetHeader("Access-Control-Allow-Origin", allowedOrigin)
				c.SetHeader("Access-Control-Allow-Methods", allowMethods)
				c.SetHeader("Access-Control-Allow-Headers", allowHeaders)

				if config.AllowCredentials {
					c.SetHeader("Access-Control-Allow-Credentials", "true")
				}
				if config.MaxAge > 0 {
					c.SetHeader("Access-Control-Max-Age", maxAge)
				}
				// Untuk preflight, kita tidak memanggil next(c), cukup kembalikan status OK.
				// StatusNoContent (204) lebih umum dan direkomendasikan untuk preflight.
				return c.NoContent(StatusNoContent)
			}

			// --- Tangani Actual Request (GET, POST, dll.) ---
			c.SetHeader("Access-Control-Allow-Origin", allowedOrigin)
			if config.AllowCredentials {
				c.SetHeader("Access-Control-Allow-Credentials", "true")
			}
			if len(config.ExposeHeaders) > 0 {
				c.SetHeader("Access-Control-Expose-Headers", exposeHeaders)
			}

			return next(c)
		}
	}
}

// compileOrigins (placeholder) - bisa dikembangkan untuk mendukung wildcard/pola
// func compileOrigins(origins []string) []string {
// // Untuk saat ini, hanya mengembalikan apa adanya.
// // Implementasi sebenarnya akan mem-parse pola.
// return origins
// }
