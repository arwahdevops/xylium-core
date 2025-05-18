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

// visitor stores request count and window information for a given key.
type visitor struct {
	count      int
	lastSeen   time.Time
	windowEnds time.Time
}

// LimiterStore defines the interface for storing and managing rate limiter state.
type LimiterStore interface {
	Allow(key string, limit int, window time.Duration) (allowed bool, currentCount int, configuredLimit int, windowEnds time.Time)
	Close() error
}

// InMemoryStore is a LimiterStore implementation using an in-memory map.
type InMemoryStore struct {
	visitors        map[string]*visitor
	mu              sync.RWMutex  // Protects visitors map and isClosed flag.
	cleanupInterval time.Duration
	stopCleanup     chan struct{} // Closed to signal the cleanup goroutine to stop.
	startOnce       sync.Once     // Ensures cleanup goroutine is started only once.
	closeOnce       sync.Once     // Ensures Close actions (like closing stopCleanup) happen only once.
	logger          Logger
	isClosed        bool          // Guarded by mu. True if Close() has been initiated.
}

// InMemoryStoreOption defines a function signature for options to configure an InMemoryStore.
type InMemoryStoreOption func(*InMemoryStore)

// WithCleanupInterval sets the cleanup interval for the InMemoryStore.
func WithCleanupInterval(interval time.Duration) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.cleanupInterval = interval
	}
}

// WithLogger sets a xylium.Logger for the InMemoryStore.
func WithLogger(logger Logger) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.logger = logger
	}
}

// NewInMemoryStore creates a new InMemoryStore.
func NewInMemoryStore(options ...InMemoryStoreOption) *InMemoryStore {
	s := &InMemoryStore{
		visitors:        make(map[string]*visitor),
		cleanupInterval: DefaultCleanupInterval,
		stopCleanup:     make(chan struct{}), // Initialize channel.
	}
	for _, option := range options {
		option(s)
	}
	// Cleanup goroutine is started lazily by startCleanupRoutine if interval is positive.
	// This prevents goroutine leak if store is created but not used with positive interval.
	if s.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}
	return s
}

// logf is an internal helper for InMemoryStore logging.
func (s *InMemoryStore) logf(level LogLevel, format string, args ...interface{}) {
	if s.logger != nil {
		switch level {
		case LevelDebug: s.logger.Debugf(format, args...)
		case LevelInfo:  s.logger.Infof(format, args...)
		case LevelWarn:  s.logger.Warnf(format, args...)
		case LevelError: s.logger.Errorf(format, args...)
		default:         s.logger.Printf(format, args...)
		}
	} else {
		if level >= LevelInfo { // Only log Info and above for standard logger
			log.Printf("[InMemoryStore] "+format, args...)
		}
	}
}

// startCleanupRoutine starts the periodic cleanup goroutine.
// It's called by NewInMemoryStore if a positive cleanupInterval is set.
func (s *InMemoryStore) startCleanupRoutine() {
	s.startOnce.Do(func() { // Ensure only one cleanup goroutine starts per store instance.
		if s.cleanupInterval <= 0 {
			s.logf(LevelWarn, "InMemoryStore: Cleanup interval is not positive (%v), cleanup routine not started.", s.cleanupInterval)
			return
		}
		s.logf(LevelDebug, "InMemoryStore: Starting cleanup routine with interval %v.", s.cleanupInterval)

		go func() {
			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop() // Ensure ticker is stopped when goroutine exits.
			for {
				select {
				case <-ticker.C:
					s.mu.RLock() // Read lock to check isClosed.
					closed := s.isClosed
					s.mu.RUnlock()

					if closed {
						s.logf(LevelDebug, "InMemoryStore cleanup routine: store is closed, exiting loop.")
						return // Exit goroutine if store is marked as closed.
					}
					s.cleanup() // cleanup() itself will handle its own locking.
				case <-s.stopCleanup: // Check if stopCleanup channel is closed.
					s.logf(LevelDebug, "InMemoryStore cleanup routine: stop signal received via channel, exiting.")
					return // Exit goroutine.
				}
			}
		}()
	})
}

// Allow checks if a request is permitted based on the configured limit and window.
func (s *InMemoryStore) Allow(key string, limit int, window time.Duration) (bool, int, int, time.Time) {
	s.mu.Lock() // Acquire full lock for reading isClosed and potentially modifying visitors.
	defer s.mu.Unlock()

	if s.isClosed { // If store is closed, deny new requests immediately.
		s.logf(LevelWarn, "InMemoryStore: Allow called on a closed store for key '%s'. Denying request.", key)
		// Return values indicating denial: count > limit, and windowEnds could be now or an arbitrary past time.
		return false, limit + 1, limit, time.Now()
	}

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
		return true, 1, limit, newWindowEnds // Allowed, first request in new window.
	}

	// Existing visitor within their current window. Increment request count.
	v.count++
	v.lastSeen = now // Update last seen time.
	return v.count <= limit, v.count, limit, v.windowEnds // Return based on new count.
}

// cleanup is called periodically to remove expired visitor entries from the map.
// This function expects the caller (startCleanupRoutine's goroutine) to handle
// checking if the store is closed before calling cleanup.
// However, it will also check isClosed internally as a safeguard.
func (s *InMemoryStore) cleanup() {
	s.mu.Lock() // Acquire full lock as we are modifying the visitors map.
	defer s.mu.Unlock()

	if s.isClosed { // Double-check if closed, in case of race or direct call.
		return
	}

	now := time.Now()
	cleanedCount := 0
	for key, v := range s.visitors {
		if now.After(v.windowEnds) {
			delete(s.visitors, key)
			cleanedCount++
		}
	}
	if cleanedCount > 0 {
		s.logf(LevelDebug, "InMemoryStore: Cleaned up %d expired visitor entries.", cleanedCount)
	}
}

// Close signals the cleanup goroutine (if running) to stop and clears internal resources.
// Implements the LimiterStore interface. It's safe to call Close multiple times.
func (s *InMemoryStore) Close() error {
	s.closeOnce.Do(func() { // Ensure the core closing logic runs only once.
		s.logf(LevelInfo, "InMemoryStore: Initiating close sequence...")

		// First, mark the store as closed. This prevents new operations
		// and signals the cleanup goroutine (if running and checking isClosed).
		s.mu.Lock()
		s.isClosed = true
		s.mu.Unlock()

		// Signal the cleanup goroutine to stop by closing the stopCleanup channel.
		// This is the primary mechanism to stop the goroutine.
		// Check if channel is already closed to prevent panic.
		select {
		case <-s.stopCleanup:
			// Channel was already closed (e.g., Close called multiple times, or goroutine exited for other reasons).
			s.logf(LevelDebug, "InMemoryStore: stopCleanup channel was already closed.")
		default:
			// Channel is open, so close it.
			if s.cleanupInterval > 0 { // Only relevant if cleanup routine might have been started.
				s.logf(LevelDebug, "InMemoryStore: Signaling cleanup routine to stop via channel.")
				close(s.stopCleanup)
			} else {
				s.logf(LevelDebug, "InMemoryStore: Cleanup routine was not configured to run (interval <= 0).")
			}
		}

		// Finally, clear the visitors map to release memory.
		// This should be done after signaling the goroutine, though the goroutine
		// should also stop accessing `visitors` once `isClosed` is true or `stopCleanup` is closed.
		s.mu.Lock()
		s.visitors = make(map[string]*visitor) // Replace with an empty map.
		s.mu.Unlock()
		s.logf(LevelInfo, "InMemoryStore: Closed and visitors map cleared.")
	})
	return nil
}

// RateLimiterConfig stores configuration for the rate limiter middleware.
type RateLimiterConfig struct {
	MaxRequests          int
	WindowDuration       time.Duration
	Message              interface{}
	KeyGenerator         func(c *Context) string
	Store                LimiterStore
	Skip                 func(c *Context) bool
	SendRateLimitHeaders string
	RetryAfterMode       string
	LoggerForStore       Logger // Optional logger for the internally created InMemoryStore.
}

// Constants for SendRateLimitHeaders.
const (
	SendHeadersAlways  = "always"
	SendHeadersOnLimit = "on_limit"
	SendHeadersNever   = "never"
)

// Constants for RetryAfterMode.
const (
	RetryAfterSeconds  = "seconds_to_reset"
	RetryAfterHTTPDate = "http_date"
)

// RateLimiter returns a middleware that applies rate limiting.
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
		config.KeyGenerator = func(c *Context) string { return c.RealIP() }
	}

	var internallyCreatedStore LimiterStore // Track if store was created by this instance.
	if config.Store == nil {
		var storeOpts []InMemoryStoreOption
		if config.LoggerForStore != nil {
			storeOpts = append(storeOpts, WithLogger(config.LoggerForStore))
		}
		newStore := NewInMemoryStore(storeOpts...)
		config.Store = newStore
		internallyCreatedStore = newStore // Mark for potential registration with router.
	}

	if config.SendRateLimitHeaders == "" { config.SendRateLimitHeaders = SendHeadersAlways }
	if config.RetryAfterMode == "" { config.RetryAfterMode = RetryAfterSeconds }

	// --- The Middleware Handler Function ---
	return func(next HandlerFunc) HandlerFunc {
		var registerStoreOnce sync.Once // Ensure registration with router happens once per middleware instance.

		return func(c *Context) error {
			if internallyCreatedStore != nil && c.router != nil {
				registerStoreOnce.Do(func() {
					c.router.addInternalStore(internallyCreatedStore)
				})
			}

			logger := c.Logger()

			if config.Skip != nil && config.Skip(c) {
				return next(c)
			}

			key := config.KeyGenerator(c)
			allowed, currentCount, configuredLimit, windowEnds := config.Store.Allow(key, config.MaxRequests, config.WindowDuration)
			now := time.Now()
			remainingRequests := configuredLimit - currentCount
			if !allowed { remainingRequests = 0 } else if remainingRequests < 0 { remainingRequests = 0 }

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
				logger.Warnf(
					"RateLimiter: Limit exceeded for key '%s' on %s %s. Count: %d/%d. Window ends: %s (in %d sec).",
					key, c.Method(), c.Path(), currentCount, configuredLimit, windowEnds.Format(DefaultTimestampFormat), secondsToReset,
				)
				c.SetHeader("Retry-After", resetValue)
				if config.SendRateLimitHeaders == SendHeadersAlways || config.SendRateLimitHeaders == SendHeadersOnLimit {
					for hKey, hVal := range headersToSend { c.SetHeader(hKey, hVal) }
				}
				var errorResponseMessage string
				switch msg := config.Message.(type) {
				case string:
					if msg == "" { errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					} else { errorResponseMessage = msg }
				case func(c *Context, limit int, window time.Duration, resetTime time.Time) string:
					if msg != nil { errorResponseMessage = msg(c, configuredLimit, config.WindowDuration, windowEnds)
					} else { errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset) }
				default:
					errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
				}
				return NewHTTPError(StatusTooManyRequests, errorResponseMessage)
			}

			if config.SendRateLimitHeaders == SendHeadersAlways {
				for hKey, hVal := range headersToSend { c.SetHeader(hKey, hVal) }
			}
			return next(c)
		}
	}
}
