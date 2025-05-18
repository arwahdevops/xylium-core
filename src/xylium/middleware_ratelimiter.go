package xylium

import (
	"fmt"
	"log" // Standard logger for InMemoryStore if no Xylium logger is provided to it.
	"net/http" // For http.TimeFormat when RetryAfterMode is HTTPDate.
	"strconv"
	"sync"
	"time"
)

// DefaultCleanupInterval is the default interval for cleaning up stale entries in the InMemoryStore.
const DefaultCleanupInterval = 10 * time.Minute

// visitor stores request count and window information for a given key (e.g., IP address).
type visitor struct {
	count      int       // Number of requests seen in the current window.
	lastSeen   time.Time // Timestamp of the last request seen.
	windowEnds time.Time // Timestamp when the current rate limit window for this visitor expires.
}

// LimiterStore defines the interface for storing and managing rate limiter state.
// This allows for different backend stores (e.g., in-memory, Redis).
type LimiterStore interface {
	// Allow checks if a request identified by 'key' is permitted based on the 'limit' and 'window'.
	// It returns:
	//  - allowed (bool): True if the request is allowed, false otherwise.
	//  - currentCount (int): The current request count for the key within the window after this request.
	//  - configuredLimit (int): The 'limit' parameter passed to this function.
	//  - windowEnds (time.Time): The time when the current window for this key resets.
	Allow(key string, limit int, window time.Duration) (allowed bool, currentCount int, configuredLimit int, windowEnds time.Time)

	// Close (optional) is used to release any resources held by the store,
	// such as stopping cleanup goroutines for in-memory stores.
	// Returns an error if closing fails.
	Close() error
}

// InMemoryStore is a LimiterStore implementation using an in-memory map.
// It includes a periodic cleanup mechanism for stale visitor entries.
type InMemoryStore struct {
	visitors        map[string]*visitor
	mu              sync.RWMutex // Mutex to protect concurrent access to the 'visitors' map.
	cleanupInterval time.Duration
	stopCleanup     chan struct{} // Channel to signal the cleanup goroutine to stop.
	once            sync.Once     // Ensures the cleanup goroutine is started only once.
	logger          Logger        // Optional Xylium logger for internal store messages.
}

// InMemoryStoreOption defines a function signature for options to configure an InMemoryStore.
type InMemoryStoreOption func(*InMemoryStore)

// WithCleanupInterval sets the cleanup interval for removing stale entries from the InMemoryStore.
// If interval is <= 0, cleanup is disabled (unless already started with a positive interval).
func WithCleanupInterval(interval time.Duration) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.cleanupInterval = interval
	}
}

// WithLogger sets a xylium.Logger for the InMemoryStore to use for its internal logging.
// If not provided, the store will use the standard 'log' package for critical messages or remain silent.
func WithLogger(logger Logger) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.logger = logger
	}
}

// NewInMemoryStore creates a new InMemoryStore instance.
// It accepts InMemoryStoreOption functions for customization (e.g., cleanup interval, logger).
// The cleanup goroutine is started automatically if cleanupInterval > 0.
func NewInMemoryStore(options ...InMemoryStoreOption) *InMemoryStore {
	s := &InMemoryStore{
		visitors:        make(map[string]*visitor),
		cleanupInterval: DefaultCleanupInterval, // Default interval.
		stopCleanup:     make(chan struct{}),
		// logger defaults to nil; will use standard log if needed and nil.
	}

	for _, option := range options {
		option(s)
	}

	// Start the cleanup routine if a positive interval is set.
	// The 'once' ensures it's started only if not already running and interval is valid.
	if s.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}
	return s
}

// logf is an internal helper for InMemoryStore to log messages.
// It uses the configured xylium.Logger if available, otherwise Go's standard log.
func (s *InMemoryStore) logf(level LogLevel, format string, args ...interface{}) {
	if s.logger != nil {
		switch level {
		case LevelDebug:
			s.logger.Debugf(format, args...)
		case LevelInfo:
			s.logger.Infof(format, args...)
		case LevelWarn:
			s.logger.Warnf(format, args...)
		case LevelError: // Add Error case
			s.logger.Errorf(format, args...)
		default: // Fallback for other levels if logger doesn't support them all directly
			s.logger.Printf(format, args...)
		}
	} else {
		// Fallback to standard log package if no Xylium logger is configured.
		// Only log INFO and above to avoid being too noisy with standard log.
		if level >= LevelInfo {
			log.Printf("[InMemoryStore] "+format, args...)
		}
	}
}

// startCleanupRoutine starts a goroutine that periodically cleans up expired visitor entries.
// It uses sync.Once to ensure it's only called once per InMemoryStore instance.
func (s *InMemoryStore) startCleanupRoutine() {
	s.once.Do(func() {
		if s.cleanupInterval <= 0 { // Double-check, should be caught by caller too.
			s.logf(LevelWarn, "Cleanup interval is not positive (%v), cleanup routine not started.", s.cleanupInterval)
			return
		}
		s.logf(LevelDebug, "Starting cleanup routine with interval %v.", s.cleanupInterval)
		go func() {
			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.cleanup()
				case <-s.stopCleanup:
					s.logf(LevelDebug, "Cleanup routine stopped.")
					return
				}
			}
		}()
	})
}

// Allow implements the LimiterStore interface for InMemoryStore.
// It checks if a request from 'key' is allowed within the 'limit' and 'window'.
func (s *InMemoryStore) Allow(key string, limit int, window time.Duration) (bool, int, int, time.Time) {
	s.mu.Lock() // Exclusive lock for modifying the visitors map.
	defer s.mu.Unlock()

	now := time.Now()
	v, exists := s.visitors[key]

	// If visitor doesn't exist or their window has expired, create/reset the visitor.
	if !exists || now.After(v.windowEnds) {
		newWindowEnds := now.Add(window)
		s.visitors[key] = &visitor{
			count:      1,
			lastSeen:   now,
			windowEnds: newWindowEnds,
		}
		// s.logf(LevelDebug, "Key '%s': New window created. Count: 1/%d. Window ends: %s", key, limit, newWindowEnds)
		return true, 1, limit, newWindowEnds
	}

	// Increment request count for existing visitor within their window.
	v.count++
	v.lastSeen = now // Update last seen time.

	allowed := v.count <= limit
	// if !allowed {
	// 	s.logf(LevelDebug, "Key '%s': Limit exceeded. Count: %d/%d. Window ends: %s", key, v.count, limit, v.windowEnds)
	// } else {
	// 	s.logf(LevelDebug, "Key '%s': Request allowed. Count: %d/%d. Window ends: %s", key, v.count, limit, v.windowEnds)
	// }
	return allowed, v.count, limit, v.windowEnds
}

// cleanup is called periodically to remove expired visitor entries from the map.
func (s *InMemoryStore) cleanup() {
	s.mu.Lock() // Exclusive lock for modifying the visitors map.
	defer s.mu.Unlock()

	now := time.Now()
	cleanedCount := 0
	for key, v := range s.visitors {
		if now.After(v.windowEnds) {
			delete(s.visitors, key)
			cleanedCount++
		}
	}
	if cleanedCount > 0 {
		s.logf(LevelDebug, "Cleaned up %d expired visitor entries.", cleanedCount)
	}
}

// Close signals the cleanup goroutine (if running) to stop.
// Implements the LimiterStore interface.
func (s *InMemoryStore) Close() error {
	// Check if stopCleanup channel is already closed or if cleanup was never started.
	// This prevents panic from closing an already closed channel.
	// This select is a non-blocking way to check.
	select {
	case <-s.stopCleanup:
		// Already closed or signal sent.
		s.logf(LevelDebug, "Close called, cleanup routine was already signaled to stop or never started.")
	default:
		// Not yet closed, so close it to signal the goroutine.
		if s.cleanupInterval > 0 { // Only try to close if cleanup was potentially started
			s.logf(LevelDebug, "Signaling cleanup routine to stop...")
			close(s.stopCleanup)
		}
	}
	return nil
}

// RateLimiterConfig stores configuration for the rate limiter middleware.
type RateLimiterConfig struct {
	// MaxRequests is the maximum number of requests allowed from a single key within WindowDuration.
	MaxRequests int
	// WindowDuration is the time window during which requests are counted.
	WindowDuration time.Duration
	// Message is the message sent to the client when the rate limit is exceeded.
	// Can be a string or a func(c *Context, limit int, window time.Duration, resetTime time.Time) string.
	Message interface{}
	// KeyGenerator is a function to generate a unique key for each request to be rate-limited.
	// Defaults to using the client's real IP address (c.RealIP()).
	KeyGenerator func(c *Context) string
	// Store is the LimiterStore implementation to use for tracking request counts.
	// If nil, a new InMemoryStore with default settings will be created.
	// The user is responsible for calling Close() on the store if it's managed externally.
	Store LimiterStore
	// Skip is a function that, if it returns true, skips rate limiting for the current request.
	Skip func(c *Context) bool
	// SendRateLimitHeaders defines when to send X-RateLimit-* headers.
	// Options: SendHeadersAlways, SendHeadersOnLimit, SendHeadersNever.
	// Default: SendHeadersAlways.
	SendRateLimitHeaders string
	// RetryAfterMode defines the format of the "Retry-After" header.
	// Options: RetryAfterSeconds, RetryAfterHTTPDate.
	// Default: RetryAfterSeconds.
	RetryAfterMode string
	// LoggerForStore (optional): if you want the internally created InMemoryStore to use a specific Xylium logger.
	// If Store is provided by the user, this field is ignored for that user-provided store.
	LoggerForStore Logger
}

// Constants for SendRateLimitHeaders configuration.
const (
	SendHeadersAlways  = "always"  // Always send X-RateLimit-* headers.
	SendHeadersOnLimit = "on_limit" // Send X-RateLimit-* headers only when the limit is hit.
	SendHeadersNever   = "never"   // Never send X-RateLimit-* headers.
)

// Constants for RetryAfterMode configuration.
const (
	RetryAfterSeconds  = "seconds_to_reset" // "Retry-After" header value is in seconds.
	RetryAfterHTTPDate = "http_date"        // "Retry-After" header value is an HTTP-date.
)

// RateLimiter returns a middleware that applies rate limiting to requests.
func RateLimiter(config RateLimiterConfig) Middleware {
	// Validate mandatory configuration.
	if config.MaxRequests <= 0 {
		panic("xylium: RateLimiter MaxRequests must be greater than 0")
	}
	if config.WindowDuration <= 0 {
		panic("xylium: RateLimiter WindowDuration must be greater than 0")
	}

	// Apply defaults for optional configuration fields.
	if config.KeyGenerator == nil {
		config.KeyGenerator = func(c *Context) string {
			return c.RealIP() // Default key is client's IP.
		}
	}
	if config.Store == nil {
		// If no store is provided, create a default InMemoryStore.
		// Pass the LoggerForStore if provided in the config.
		var storeOpts []InMemoryStoreOption
		if config.LoggerForStore != nil {
			storeOpts = append(storeOpts, WithLogger(config.LoggerForStore))
		}
		config.Store = NewInMemoryStore(storeOpts...)
		// Note: If this default store is created, the application currently has no direct
		// way to call Close() on it during shutdown unless Xylium manages it internally.
		// This is a known consideration. For robust resource management, users should
		// create and manage their own store instances.
	}
	if config.SendRateLimitHeaders == "" {
		config.SendRateLimitHeaders = SendHeadersAlways
	}
	if config.RetryAfterMode == "" {
		config.RetryAfterMode = RetryAfterSeconds
	}

	// --- The Middleware Handler Function ---
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Get request-scoped logger.

			// Check if rate limiting should be skipped for this request.
			if config.Skip != nil && config.Skip(c) {
				logger.Debugf("RateLimiter: Skipping rate limit for key (if generated) on path %s %s due to Skip function.", c.Method(), c.Path())
				return next(c)
			}

			// Generate the key for rate limiting.
			key := config.KeyGenerator(c)
			logger.Debugf("RateLimiter: Generated key '%s' for %s %s.", key, c.Method(), c.Path())


			// Check with the store if the request is allowed.
			allowed, currentCount, configuredLimit, windowEnds := config.Store.Allow(key, config.MaxRequests, config.WindowDuration)

			now := time.Now()
			remainingRequests := configuredLimit - currentCount
			if !allowed { // If limit is hit or exceeded.
				remainingRequests = 0
			} else if remainingRequests < 0 { // Should not happen if currentCount <= configuredLimit.
				remainingRequests = 0 // Defensive.
			}

			// Prepare RateLimit headers.
			headersToSend := make(map[string]string)
			headersToSend["X-RateLimit-Limit"] = strconv.Itoa(configuredLimit)
			headersToSend["X-RateLimit-Remaining"] = strconv.Itoa(remainingRequests)

			var resetValue string
			secondsToReset := int(windowEnds.Sub(now).Seconds())
			// Ensure secondsToReset is not negative if window has already passed slightly due to timing.
			if secondsToReset < 0 {
				secondsToReset = 0
			}

			if config.RetryAfterMode == RetryAfterHTTPDate {
				resetValue = windowEnds.Format(http.TimeFormat) // RFC1123 format.
			} else { // Default to RetryAfterSeconds.
				resetValue = strconv.Itoa(secondsToReset)
			}
			headersToSend["X-RateLimit-Reset"] = resetValue

			// If the request is NOT allowed (rate limit exceeded).
			if !allowed {
				logger.Warnf(
					"RateLimiter: Limit exceeded for key '%s' on %s %s. Count: %d/%d. Window ends: %s (in %d sec).",
					key, c.Method(), c.Path(), currentCount, configuredLimit, windowEnds.Format(DefaultTimestampFormat), secondsToReset,
				)

				// Set Retry-After header.
				c.SetHeader("Retry-After", resetValue) // Value already formatted based on RetryAfterMode.

				// Set X-RateLimit-* headers if configured to do so on limit.
				if config.SendRateLimitHeaders == SendHeadersAlways || config.SendRateLimitHeaders == SendHeadersOnLimit {
					for hKey, hVal := range headersToSend {
						c.SetHeader(hKey, hVal)
					}
				}

				// Construct and return the error message response.
				var errorResponseMessage string
				switch msg := config.Message.(type) {
				case string:
					if msg == "" { // Default message if string is empty.
						errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					} else {
						errorResponseMessage = msg
					}
				case func(c *Context, limit int, window time.Duration, resetTime time.Time) string:
					if msg != nil {
						errorResponseMessage = msg(c, configuredLimit, config.WindowDuration, windowEnds)
					} else { // Fallback if func is nil (should not happen if interface is used properly).
						errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					}
				default: // Fallback for unknown message type.
					errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
				}
				// Return an HTTPError, which GlobalErrorHandler will process.
				return NewHTTPError(StatusTooManyRequests, errorResponseMessage)
			}

			// If request IS allowed.
			logger.Debugf(
				"RateLimiter: Request allowed for key '%s' on %s %s. Count: %d/%d. Window ends: %s.",
				key, c.Method(), c.Path(), currentCount, configuredLimit, windowEnds.Format(DefaultTimestampFormat),
			)

			// Set X-RateLimit-* headers if configured to always send them.
			if config.SendRateLimitHeaders == SendHeadersAlways {
				for hKey, hVal := range headersToSend {
					c.SetHeader(hKey, hVal)
				}
			}

			// Proceed to the next handler.
			return next(c)
		}
	}
}
