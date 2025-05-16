package xylium

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// DefaultCleanupInterval adalah interval default untuk membersihkan store in-memory.
const DefaultCleanupInterval = 10 * time.Minute

// visitor menyimpan informasi tentang request dari satu kunci (misalnya, IP).
type visitor struct {
	count      int
	lastSeen   time.Time
	windowEnds time.Time
}

// LimiterStore adalah interface untuk penyimpanan state rate limiter.
type LimiterStore interface {
	// Allow checks if a request from the given key is allowed.
	// Returns:
	// - allowed (bool): true jika request diizinkan.
	// - currentCount (int): jumlah request saat ini dalam window.
	// - limit (int): batas request yang dikonfigurasi.
	// - windowEnds (time.Time): waktu saat window saat ini akan berakhir/reset.
	Allow(key string, limit int, window time.Duration) (allowed bool, currentCount int, configuredLimit int, windowEnds time.Time)

	// Close (opsional) digunakan untuk membersihkan resource yang digunakan oleh store,
	// seperti menghentikan goroutine cleanup.
	Close() error
}

// InMemoryStore adalah implementasi LimiterStore default yang menggunakan map in-memory.
type InMemoryStore struct {
	visitors        map[string]*visitor
	mu              sync.RWMutex
	cleanupInterval time.Duration
	stopCleanup     chan struct{} // Channel untuk memberi sinyal goroutine cleanup untuk berhenti
	once            sync.Once     // Untuk memastikan goroutine cleanup hanya dimulai sekali
}

// InMemoryStoreOption adalah fungsi untuk mengkonfigurasi InMemoryStore.
type InMemoryStoreOption func(*InMemoryStore)

// WithCleanupInterval mengatur interval cleanup untuk InMemoryStore.
func WithCleanupInterval(interval time.Duration) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		if interval > 0 {
			s.cleanupInterval = interval
		}
	}
}

// NewInMemoryStore membuat instance baru dari InMemoryStore.
// Ia juga akan memulai goroutine cleanup secara otomatis jika interval > 0.
func NewInMemoryStore(options ...InMemoryStoreOption) *InMemoryStore {
	s := &InMemoryStore{
		visitors:        make(map[string]*visitor),
		cleanupInterval: DefaultCleanupInterval, // Default interval
		stopCleanup:     make(chan struct{}),
	}

	for _, option := range options {
		option(s)
	}

	// Mulai goroutine cleanup jika interval valid dan belum dimulai
	if s.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}

	return s
}

// startCleanupRoutine memulai goroutine periodik untuk membersihkan entri yang kedaluwarsa.
// Menggunakan sync.Once untuk memastikan hanya dimulai sekali per instance store.
func (s *InMemoryStore) startCleanupRoutine() {
	s.once.Do(func() {
		if s.cleanupInterval <= 0 { // Jangan mulai jika interval tidak valid
			return
		}
		go func() {
			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.cleanup()
				case <-s.stopCleanup:
					// fmt.Println("InMemoryStore cleanup routine stopped.") // Untuk debugging
					return
				}
			}
		}()
		// fmt.Println("InMemoryStore cleanup routine started.") // Untuk debugging
	})
}

// Allow implementasi untuk InMemoryStore.
func (s *InMemoryStore) Allow(key string, limit int, window time.Duration) (bool, int, int, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	v, exists := s.visitors[key]

	if !exists || now.After(v.windowEnds) {
		s.visitors[key] = &visitor{
			count:      1,
			lastSeen:   now,
			windowEnds: now.Add(window),
		}
		return true, 1, limit, now.Add(window)
	}

	v.count++
	v.lastSeen = now

	return v.count <= limit, v.count, limit, v.windowEnds
}

// cleanup (private) menghapus entri pengunjung yang sudah kedaluwarsa.
func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cleanedCount := 0
	for key, v := range s.visitors {
		if now.After(v.windowEnds) {
			delete(s.visitors, key)
			cleanedCount++
		}
	}
	// if cleanedCount > 0 { // Untuk debugging
	// 	fmt.Printf("InMemoryStore cleaned %d expired entries.\n", cleanedCount)
	// }
}

// Close menghentikan goroutine cleanup (jika ada) untuk InMemoryStore.
// Implementasi LimiterStore interface.
func (s *InMemoryStore) Close() error {
	// Hanya perlu menghentikan jika goroutine cleanup pernah dimulai.
	// sync.Once memastikan startCleanupRoutine hanya dieksekusi sekali.
	// Jika cleanupInterval <=0 saat NewInMemoryStore, stopCleanup tidak akan pernah dibaca.
	// Mengirim ke channel yang tidak dibaca akan memblokir, jadi kita tutup channel-nya.
	// Atau, kita bisa menambahkan flag apakah goroutine berjalan.
	// Cara paling aman adalah menutup channel. Goroutine akan exit saat membaca dari channel yang ditutup.

	// Kirim sinyal stop jika belum dikirim, atau tutup channel.
	// Cukup tutup channel stopCleanup, ini akan memberi sinyal ke goroutine untuk berhenti.
	// Pastikan ini aman untuk dipanggil berkali-kali.
	// Kita bisa menggunakan sync.Once lagi untuk Close jika perlu, atau flag.
	// Untuk saat ini, menutup channel sudah cukup.
	// Goroutine select akan mendeteksi channel yang ditutup dan keluar.

	// Cek apakah stopCleanup sudah ditutup untuk menghindari panic jika Close dipanggil berkali-kali.
	// Ini cara sederhana untuk memeriksa.
	select {
	case <-s.stopCleanup:
		// Channel sudah ditutup atau sinyal sudah dikirim.
	default:
		close(s.stopCleanup)
	}
	return nil
}


// RateLimiterConfig menyimpan konfigurasi untuk middleware rate limiter.
type RateLimiterConfig struct {
	MaxRequests    int
	WindowDuration time.Duration
	Message        interface{}
	KeyGenerator   func(c *Context) string
	Store          LimiterStore // Pengguna sekarang bertanggung jawab untuk mengelola lifecycle store jika custom
	Skip           func(c *Context) bool
	SendRateLimitHeaders string
	RetryAfterMode string
	// CleanupInterval tidak lagi di sini, karena itu properti dari InMemoryStore
}

const (
	SendHeadersAlways  = "always"
	SendHeadersOnLimit = "on_limit"
	SendHeadersNever   = "never"

	RetryAfterSeconds  = "seconds_to_reset"
	RetryAfterHTTPDate = "http_date"
)

// RateLimiter adalah middleware yang menerapkan rate limiting.
func RateLimiter(config RateLimiterConfig) Middleware {
	if config.MaxRequests <= 0 {
		panic("xylium: RateLimiter MaxRequests harus lebih besar dari 0")
	}
	if config.WindowDuration <= 0 {
		panic("xylium: RateLimiter WindowDuration harus lebih besar dari 0")
	}

	if config.KeyGenerator == nil {
		config.KeyGenerator = func(c *Context) string {
			return c.RealIP()
		}
	}

	// Jika pengguna tidak menyediakan Store, buat InMemoryStore default.
	// Pengguna bertanggung jawab untuk memanggil Close() pada store ini jika diperlukan saat shutdown.
	if config.Store == nil {
		// Kita bisa membuat instance default di sini.
		// Pertanyaannya adalah siapa yang akan memanggil Close() pada instance default ini?
		// Mungkin lebih baik jika pengguna *selalu* menyediakan store, bahkan jika itu NewInMemoryStore().
		// Untuk saat ini, kita buat default, tapi ini area yang bisa dipertimbangkan.
		config.Store = NewInMemoryStore()
		// CATATAN: Jika store default dibuat di sini, aplikasi utama harus memiliki cara untuk
		// mendapatkan referensi ke store ini untuk memanggil Close() saat shutdown.
		// Ini menjadi rumit. Alternatif: RateLimiterConfig mewajibkan Store.
		// Atau, Xylium Router bisa melacak store yang dibuat secara internal dan menutupnya saat shutdown.
		// Untuk sekarang, biarkan seperti ini dan asumsikan pengguna mengelola store custom
		// atau menerima bahwa store default mungkin tidak di-Close dengan benar.
	}

	if config.SendRateLimitHeaders == "" {
		config.SendRateLimitHeaders = SendHeadersAlways
	}
	if config.RetryAfterMode == "" {
		config.RetryAfterMode = RetryAfterSeconds
	}

	// Goroutine cleanup tidak lagi dimulai dari sini, tapi dari NewInMemoryStore.

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			if config.Skip != nil && config.Skip(c) {
				return next(c)
			}

			key := config.KeyGenerator(c)
			allowed, currentCount, configuredLimit, windowEnds := config.Store.Allow(key, config.MaxRequests, config.WindowDuration)

			now := time.Now()
			remainingRequests := configuredLimit - currentCount
			if !allowed {
				remainingRequests = 0
			} else if remainingRequests < 0 {
				remainingRequests = 0
			}

			headersToSend := make(map[string]string)
			headersToSend["X-RateLimit-Limit"] = strconv.Itoa(configuredLimit)
			headersToSend["X-RateLimit-Remaining"] = strconv.Itoa(remainingRequests)

			var resetValue string
			secondsToReset := int(windowEnds.Sub(now).Seconds())
			if secondsToReset < 0 { secondsToReset = 0 }

			if config.RetryAfterMode == RetryAfterHTTPDate {
				resetValue = windowEnds.Format(http.TimeFormat)
			} else {
				resetValue = strconv.Itoa(secondsToReset)
			}
			headersToSend["X-RateLimit-Reset"] = resetValue

			if !allowed {
				if config.RetryAfterMode == RetryAfterHTTPDate {
					c.SetHeader("Retry-After", windowEnds.Format(http.TimeFormat))
				} else {
					c.SetHeader("Retry-After", strconv.Itoa(secondsToReset))
				}

				if config.SendRateLimitHeaders == SendHeadersAlways || config.SendRateLimitHeaders == SendHeadersOnLimit {
					for k, v := range headersToSend {
						c.SetHeader(k, v)
					}
				}

				var errMsg string
				switch msg := config.Message.(type) {
				case string:
					if msg == "" {
						errMsg = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					} else {
						errMsg = msg
					}
				case func(c *Context, limit int, window time.Duration, resetTime time.Time) string:
					if msg != nil {
						errMsg = msg(c, configuredLimit, config.WindowDuration, windowEnds)
					} else {
						errMsg = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					}
				default:
					errMsg = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
				}
				return NewHTTPError(StatusTooManyRequests, errMsg)
			}

			if config.SendRateLimitHeaders == SendHeadersAlways {
				for k, v := range headersToSend {
					c.SetHeader(k, v)
				}
			}

			return next(c)
		}
	}
}
