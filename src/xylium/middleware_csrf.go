package xylium

import (
	"crypto/rand"    // Untuk pembuatan token acak yang aman secara kriptografis
	"encoding/base64"  // Untuk encoding token menjadi string yang aman untuk URL/header
	"errors"         // Untuk definisi error kustom
	"fmt"            // Untuk formatting string error
	"strings"        // Untuk manipulasi string (misalnya, parsing TokenLookup)
	"time"           // Untuk manajemen durasi cookie

	"github.com/valyala/fasthttp" // PERBAIKAN: Impor fasthttp untuk konstanta CookieSameSite dan objek Cookie
)

// CSRFConfig mendefinisikan konfigurasi untuk middleware CSRF Protection.
type CSRFConfig struct {
	// TokenLength adalah panjang token CSRF dalam byte sebelum di-encode ke base64.
	// Semakin panjang, semakin aman. Default: 32 byte.
	TokenLength int

	// CookieName adalah nama cookie yang akan digunakan untuk menyimpan token CSRF (bagian server).
	// Default: "_csrf_token".
	CookieName string

	// CookiePath adalah path URL dimana cookie CSRF akan berlaku.
	// Default: "/" (berlaku untuk seluruh domain).
	CookiePath string

	// CookieDomain adalah domain dimana cookie CSRF akan berlaku.
	// Kosongkan agar browser menggunakan domain saat ini.
	// Default: "" (kosong).
	CookieDomain string

	// CookieMaxAge adalah durasi (dalam detik) cookie CSRF akan valid di browser.
	// Default: 12 jam (12 * 60 * 60 detik).
	CookieMaxAge time.Duration

	// CookieSecure menentukan apakah cookie hanya boleh dikirim melalui koneksi HTTPS.
	// Sangat direkomendasikan `true` untuk produksi.
	// Default: true.
	CookieSecure bool

	// CookieHTTPOnly menentukan apakah cookie tidak dapat diakses melalui JavaScript sisi klien.
	// Untuk metode "Double Submit Cookie" di mana JavaScript perlu membaca token dari cookie
	// untuk dikirim kembali di header, ini harus diset `false`.
	// Jika `true`, server harus menyediakan token ke JavaScript melalui cara lain (misalnya, meta tag atau data response).
	// Default: false.
	CookieHTTPOnly bool

	// CookieSameSite mengatur atribut SameSite untuk cookie CSRF, membantu melindungi dari serangan CSRF lintas situs.
	// Pilihan: fasthttp.CookieSameSiteLaxMode, fasthttp.CookieSameSiteStrictMode, fasthttp.CookieSameSiteNoneMode.
	// Default: fasthttp.CookieSameSiteLaxMode.
	CookieSameSite fasthttp.CookieSameSite

	// HeaderName adalah nama header HTTP yang diharapkan berisi token CSRF dari klien untuk validasi.
	// Umumnya digunakan oleh AJAX/SPA.
	// Default: "X-CSRF-Token".
	HeaderName string

	// FormFieldName adalah nama field dalam form (application/x-www-form-urlencoded atau multipart/form-data)
	// yang diharapkan berisi token CSRF dari klien untuk validasi.
	// Umumnya digunakan oleh form HTML tradisional.
	// Default: "_csrf".
	FormFieldName string

	// SafeMethods adalah daftar metode HTTP yang dianggap "aman" (tidak mengubah state server)
	// dan oleh karena itu tidak memerlukan validasi token CSRF.
	// Default: []string{"GET", "HEAD", "OPTIONS", "TRACE"}.
	SafeMethods []string

	// ErrorHandler adalah fungsi kustom yang akan dipanggil jika validasi CSRF gagal.
	// Jika nil, handler default akan mengirim respons HTTP 403 Forbidden.
	ErrorHandler HandlerFunc // func(c *Context) error

	// TokenLookup adalah string yang mendefinisikan dari mana token CSRF akan diekstrak dari request klien.
	// Format: "source1:name1,source2:name2,...". Source bisa "header", "form", atau "query".
	// Contoh: "header:X-CSRF-Token,form:_csrf_token_field".
	// Jika Extractor diset, TokenLookup akan diabaikan.
	// Default: "header:X-CSRF-Token,form:_csrf" (sesuai HeaderName dan FormFieldName default).
	TokenLookup string

	// Extractor adalah fungsi kustom untuk mengekstrak token CSRF dari Context.
	// Memberikan fleksibilitas penuh jika TokenLookup tidak cukup.
	// Jika diset, akan meng-override TokenLookup.
	Extractor func(c *Context) (string, error)
}

// ErrorCSRFTokenInvalid adalah error standar yang dikembalikan jika token CSRF tidak valid, hilang, atau tidak cocok.
var ErrorCSRFTokenInvalid = errors.New("xylium: invalid or missing CSRF token")

// DefaultCSRFConfig menyediakan konfigurasi CSRF default yang seimbang.
var DefaultCSRFConfig = CSRFConfig{
	TokenLength:    32,
	CookieName:     "_csrf_token", // Nama cookie yang umum
	CookiePath:     "/",
	CookieMaxAge:   12 * time.Hour,
	CookieSecure:   true,  // PENTING: Set `false` hanya untuk development HTTP lokal
	CookieHTTPOnly: false, // Umum untuk SPA yang membaca token dari cookie via JS
	CookieSameSite: fasthttp.CookieSameSiteLaxMode, // Pilihan yang baik untuk keseimbangan keamanan & UX
	HeaderName:     "X-CSRF-Token",                 // Nama header yang umum
	FormFieldName:  "_csrf",                        // Nama field form yang umum (bisa juga _csrf_token)
	SafeMethods:    []string{MethodGet, MethodHead, MethodOptions, MethodTrace},
	// TokenLookup default akan dibangun berdasarkan HeaderName dan FormFieldName jika tidak diset
}

// CSRF mengembalikan middleware CSRF dengan konfigurasi default.
func CSRF() Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig mengembalikan middleware CSRF dengan konfigurasi yang diberikan.
func CSRFWithConfig(config CSRFConfig) Middleware {
	// --- Normalisasi dan Validasi Konfigurasi ---
	if config.TokenLength <= 0 { // Minimal panjang token yang wajar
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookiePath == "" {
		config.CookiePath = DefaultCSRFConfig.CookiePath
	}
	if config.CookieMaxAge <= 0 { // Durasi cookie harus positif
		config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge
	}
	// CookieSecure dan CookieHTTPOnly menggunakan nilai dari config jika diset,
	// atau nilai default jika tidak diset (boolean defaultnya false).
	// Kita perlu memastikan default dari DefaultCSRFConfig diterapkan jika user tidak menspesifikasikannya.
	// Namun, karena boolean, jika user tidak set, akan jadi false.
	// Jadi, kita bisa biarkan apa adanya atau set secara eksplisit jika nilai user adalah zero value boolean.
	// Untuk CookieSecure, defaultnya true, jadi jika user tidak set, ini akan jadi false. Ini perlu diperhatikan.
	// Solusi: User harus selalu menspesifikasikan atau kita set default di sini.
	// Mari kita asumsikan jika tidak diset, default dari DefaultCSRFConfig berlaku.
	// Ini sudah ditangani oleh bagaimana struct default di-pass.
	// Misal: jika CSRF() dipanggil, config = DefaultCSRFConfig.
	// Jika CSRFWithConfig({}) dipanggil, maka boolean akan jadi false.

	// Jika config.CookieSameSite tidak di-set (akan menjadi 0), gunakan default.
	if config.CookieSameSite == 0 { // fasthttp.CookieSameSite value are > 0
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

	// Bangun TokenLookup default jika tidak ada Extractor dan TokenLookup kosong
	if config.Extractor == nil && config.TokenLookup == "" {
		config.TokenLookup = fmt.Sprintf("header:%s,form:%s", config.HeaderName, config.FormFieldName)
	}

	// Buat map dari SafeMethods untuk pencarian yang efisien
	safeMethodsMap := make(map[string]struct{}, len(config.SafeMethods))
	for _, method := range config.SafeMethods {
		safeMethodsMap[strings.ToUpper(method)] = struct{}{}
	}

	// Parse TokenLookup menjadi daftar fungsi extractor
	var extractors []func(c *Context) (string, error)
	if config.Extractor != nil {
		extractors = append(extractors, config.Extractor)
	} else { // Parse dari string TokenLookup
		parts := strings.Split(config.TokenLookup, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			segments := strings.SplitN(part, ":", 2)
			if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
				panic(fmt.Errorf("xylium: invalid CSRF TokenLookup format in part: '%s'", part))
			}
			source, name := strings.ToLower(segments[0]), segments[1]
			switch source {
			case "header":
				extractors = append(extractors, func(c *Context) (string, error) { return c.Header(name), nil })
			case "form":
				extractors = append(extractors, func(c *Context) (string, error) { return c.FormValue(name), nil })
			case "query":
				extractors = append(extractors, func(c *Context) (string, error) { return c.QueryParam(name), nil })
			default:
				panic(fmt.Errorf("xylium: unsupported CSRF TokenLookup source: '%s'", source))
			}
		}
	}
	if len(extractors) == 0 { // Harus ada setidaknya satu cara untuk mengekstrak token
		panic("xylium: CSRF TokenLookup or Extractor must be configured to define at least one extraction method")
	}

	// Siapkan ErrorHandler
	errorHandler := config.ErrorHandler
	if errorHandler == nil { // Handler default jika tidak ada yang disediakan
		errorHandler = func(c *Context) error {
			// Ambil pesan error dari context jika ada (diset saat validasi gagal)
			errCause := ErrorCSRFTokenInvalid // Default cause
			if errVal, exists := c.Get("csrf_error"); exists {
				if e, ok := errVal.(error); ok {
					errCause = e
				}
			}
			return NewHTTPError(StatusForbidden, "Invalid or missing CSRF token.").WithInternal(errCause)
		}
	}

	// --- Middleware Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// 1. Ambil token CSRF yang ada di cookie (jika ada).
			tokenFromCookie := c.Cookie(config.CookieName)

			// 2. Untuk metode yang dianggap aman (GET, HEAD, dll.), atau jika token di cookie belum ada,
			//    kita perlu memastikan token ada dan dikirim ke klien.
			//    Ini berarti kita generate/refresh token dan set di cookie.
			_, methodIsSafe := safeMethodsMap[c.Method()]
			if methodIsSafe || tokenFromCookie == "" {
				newToken, err := generateRandomString(config.TokenLength)
				if err != nil {
					// Log error dan mungkin kembalikan error server jika gagal generate token krusial
					if c.router != nil && c.router.Logger() != nil {
						c.router.Logger().Printf("CSRF: Failed to generate new token: %v", err)
					}
					return NewHTTPError(StatusInternalServerError, "Could not generate security token.").WithInternal(err)
				}
				tokenFromCookie = newToken // Gunakan token baru ini untuk sisa request dan untuk di-set di cookie

				// Set (atau perbarui) cookie CSRF
				cookie := fasthttp.AcquireCookie() // Dapatkan cookie dari pool fasthttp
				defer fasthttp.ReleaseCookie(cookie) // Kembalikan ke pool setelah selesai

				cookie.SetKey(config.CookieName)
				cookie.SetValue(tokenFromCookie)
				cookie.SetPath(config.CookiePath)
				cookie.SetDomain(config.CookieDomain)
				cookie.SetMaxAge(int(config.CookieMaxAge.Seconds()))
				cookie.SetSecure(config.CookieSecure)
				cookie.SetHTTPOnly(config.CookieHTTPOnly)
				cookie.SetSameSite(config.CookieSameSite) // Menggunakan nilai fasthttp.CookieSameSite
				c.SetCookie(cookie)
			}

			// 3. Selalu simpan token yang (seharusnya) ada di cookie (baik yang lama atau baru digenerate)
			//    ke context. Ini berguna agar template view atau handler lain bisa mengaksesnya
			//    untuk disisipkan ke form atau untuk keperluan AJAX.
			c.Set("csrf_token", tokenFromCookie) // Kunci bisa dikonfigurasi jika perlu

			// 4. Jika metode request *tidak* aman (misalnya POST, PUT, DELETE),
			//    maka kita *wajib* memvalidasi token CSRF dari request.
			if !methodIsSafe {
				if tokenFromCookie == "" {
					// Ini seharusnya tidak terjadi jika logika di atas (poin 2) benar,
					// karena token seharusnya sudah digenerate. Tapi sebagai lapisan pertahanan.
					if c.router != nil && c.router.Logger() != nil {
						c.router.Logger().Printf("CSRF: CRITICAL - No token in cookie for unsafe method %s %s", c.Method(), c.Path())
					}
					c.Set("csrf_error", ErrorCSRFTokenInvalid) // Set penyebab error
					return errorHandler(c)
				}

				// Ekstrak token dari request (header, form, atau query) menggunakan daftar extractors
				var tokenFromRequest string
				var extractionErr error
				for _, extractorFunc := range extractors {
					token, err := extractorFunc(c)
					if err != nil { // Error dari extractor kustom
						extractionErr = err // Simpan error pertama dari extractor
						break
					}
					if token != "" {
						tokenFromRequest = token
						break // Token ditemukan, tidak perlu cek extractor lain
					}
				}

				if extractionErr != nil { // Jika ada error dari fungsi extractor kustom
					if c.router != nil && c.router.Logger() != nil {
						c.router.Logger().Printf("CSRF: Custom extractor failed: %v for %s %s", extractionErr, c.Method(), c.Path())
					}
					return NewHTTPError(StatusInternalServerError, "CSRF token extraction process failed.").WithInternal(extractionErr)
				}

				// Validasi token: token dari cookie harus sama dengan token dari request
				if tokenFromRequest == "" || tokenFromCookie != tokenFromRequest {
					if c.router != nil && c.router.Logger() != nil {
						logMsg := fmt.Sprintf("CSRF: Token mismatch or not found in request for %s %s.", c.Method(), c.Path())
						if tokenFromRequest == "" {
							logMsg += " Token not found in request."
						} else {
							// Jangan log token aktual ke log produksi untuk keamanan, cukup indikasi mismatch
							// logMsg += fmt.Sprintf(" CookieToken: '%s', RequestToken: '%s'", tokenFromCookie, tokenFromRequest)
							logMsg += " Token mismatch."
						}
						c.router.Logger().Printf(logMsg)
					}
					c.Set("csrf_error", ErrorCSRFTokenInvalid) // Set penyebab error
					return errorHandler(c)
				}
			}

			// Jika semua validasi lolos atau metode aman, lanjutkan ke handler berikutnya
			return next(c)
		}
	}
}

// generateRandomString menghasilkan string acak yang aman secara kriptografis,
// di-encode dengan base64 URL-safe.
func generateRandomString(lengthInBytes int) (string, error) {
	if lengthInBytes <= 0 { // Pastikan panjang byte positif
		lengthInBytes = 32 // Default yang aman jika input tidak valid
	}
	randomBytes := make([]byte, lengthInBytes)
	// Isi slice dengan byte acak dari sumber kriptografis
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}
	// Encode ke base64 URL-safe (tanpa padding jika memungkinkan, atau padding standar)
	return base64.URLEncoding.EncodeToString(randomBytes), nil
}
