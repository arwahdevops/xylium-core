package xylium

import (
	"fmt"      // For formatting error messages and log messages.
	"log"      // Standard Go logger, used by InMemoryStore as a fallback if no Xylium logger is provided.
	"net/http" // For http.TimeFormat, used when RetryAfterMode is RetryAfterHTTPDate.
	"strconv"  // For converting integers (counts, limits) to strings for headers.
	"sync"     // For sync.RWMutex, sync.Once for thread-safety in InMemoryStore.
	"time"     // For managing rate limit windows and cleanup intervals.
)

// DefaultCleanupInterval is the default interval at which the `InMemoryStore`
// will attempt to clean up stale (expired) visitor entries from its internal map.
// This helps prevent unbounded memory growth in long-running applications.
const DefaultCleanupInterval = 10 * time.Minute

// visitor is an internal struct used by `InMemoryStore` to track the request count
// and window information for a specific key (typically a client identifier like an IP address).
type visitor struct {
	count      int       // Number of requests received from this visitor in the current window.
	lastSeen   time.Time // Timestamp of the last request received from this visitor.
	windowEnds time.Time // Timestamp when the current rate limit window for this visitor expires.
}

// LimiterStore defines the interface for storage mechanisms used by the rate limiter middleware.
// Implementations of this interface are responsible for tracking request counts for different
// keys (e.g., client IPs) within defined time windows and determining if a request
// should be allowed or denied based on configured limits.
//
// Xylium provides a default `InMemoryStore`. For distributed systems or more persistent
// rate limiting, custom implementations (e.g., using Redis, Memcached) can be created.
type LimiterStore interface {
	// Allow checks if a request associated with the given `key` should be permitted
	// based on the `limit` (maximum number of requests) and `window` (duration).
	//
	// Parameters:
	//   - `key` (string): A unique identifier for the entity being rate-limited (e.g., client IP).
	//   - `limit` (int): The maximum number of requests allowed for this key within the `window`.
	//   - `window` (time.Duration): The time duration for the rate limit window.
	//
	// Returns:
	//   - `allowed` (bool): True if the request is within the limit, false otherwise.
	//   - `currentCount` (int): The current request count for the key within its window after this request.
	//   - `configuredLimit` (int): The `limit` that was applied for this check.
	//   - `windowEnds` (time.Time): The time when the current window for this key will reset/expire.
	Allow(key string, limit int, window time.Duration) (allowed bool, currentCount int, configuredLimit int, windowEnds time.Time)

	// Close is called to release any resources held by the LimiterStore, such as
	// background cleanup goroutines or connections to external storage.
	// It should be safe to call Close multiple times.
	// This method is crucial for graceful shutdown, especially for stores like
	// `InMemoryStore` that might run background tasks. Xylium's router will
	// attempt to call `Close()` on stores registered via `AppSet` or internal stores
	// during application shutdown.
	Close() error
}

// InMemoryStore is a `LimiterStore` implementation that uses an in-memory map
// to store visitor request counts. It is suitable for single-instance deployments.
// For distributed environments, a shared store (e.g., Redis-based) is recommended.
//
// `InMemoryStore` includes a background goroutine that periodically cleans up
// expired visitor entries to prevent unbounded memory growth. This cleanup
// interval can be configured.
type InMemoryStore struct {
	visitors        map[string]*visitor // Map storing visitor data, keyed by a unique identifier.
	mu              sync.RWMutex        // Read-write mutex protecting concurrent access to `visitors` map and `isClosed`.
	cleanupInterval time.Duration       // Interval at which expired entries are removed.
	stopCleanup     chan struct{}       // Channel used to signal the cleanup goroutine to stop.
	startOnce       sync.Once           // Ensures the cleanup goroutine is started only once per store instance.
	closeOnce       sync.Once           // Ensures the core `Close` logic (like closing `stopCleanup`) runs only once.
	logger          Logger              // Optional Xylium logger for internal store messages. Falls back to standard `log` if nil.
	isClosed        bool                // Flag, guarded by `mu`, indicating if `Close()` has been called.
}

// InMemoryStoreOption defines a function signature for options that can be used
// to configure an `InMemoryStore` instance upon its creation with `NewInMemoryStore`.
type InMemoryStoreOption func(*InMemoryStore)

// WithCleanupInterval is an `InMemoryStoreOption` that sets a custom cleanup interval
// for removing stale entries from the `InMemoryStore`.
// If `interval` is zero or negative, the cleanup goroutine will not be started.
func WithCleanupInterval(interval time.Duration) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.cleanupInterval = interval
	}
}

// WithLogger is an `InMemoryStoreOption` that provides a `xylium.Logger` instance
// to the `InMemoryStore` for its internal logging (e.g., cleanup activity, errors).
// If no logger is provided, `InMemoryStore` falls back to using the standard Go `log` package
// for essential messages.
func WithLogger(logger Logger) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.logger = logger
	}
}

// NewInMemoryStore creates and returns a new `InMemoryStore` instance.
// It can be configured with `InMemoryStoreOption` functions, such as `WithCleanupInterval`
// and `WithLogger`.
//
// If a positive `cleanupInterval` is configured (either by default or via `WithCleanupInterval`),
// a background goroutine is started to periodically remove expired entries. This goroutine
// is managed by `startCleanupRoutine` and stopped when `Close()` is called.
func NewInMemoryStore(options ...InMemoryStoreOption) *InMemoryStore {
	s := &InMemoryStore{
		visitors:        make(map[string]*visitor),
		cleanupInterval: DefaultCleanupInterval, // Default interval, can be overridden by options.
		stopCleanup:     make(chan struct{}),    // Initialize channel for stopping cleanup goroutine.
		// mu, startOnce, closeOnce, logger, isClosed will be initialized to their zero values.
	}
	// Apply any provided configuration options.
	for _, option := range options {
		option(s)
	}

	// Lazily start the cleanup goroutine if a positive cleanupInterval is set.
	// This is done via startCleanupRoutine which uses s.startOnce.
	// This prevents a goroutine leak if the store is created but, for example,
	// never actually used in a rate limiter that gets registered with the router
	// for graceful shutdown of its store. The store is only "active" with a goroutine
	// if it's configured to clean up.
	if s.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}
	return s
}

// logf is an internal helper method for `InMemoryStore` to perform logging.
// It uses the configured `s.logger` (a `xylium.Logger`) if available, otherwise
// it falls back to the standard Go `log` package for messages at `LevelInfo` or higher.
func (s *InMemoryStore) logf(level LogLevel, format string, args ...interface{}) {
	if s.logger != nil {
		// Use the provided Xylium logger.
		switch level {
		case LevelDebug:
			s.logger.Debugf(format, args...)
		case LevelInfo:
			s.logger.Infof(format, args...)
		case LevelWarn:
			s.logger.Warnf(format, args...)
		case LevelError:
			s.logger.Errorf(format, args...)
		default: // For any other custom levels or if level is unknown.
			s.logger.Printf(format, args...) // Fallback to Printf for safety.
		}
	} else {
		// Fallback to standard Go logger if no Xylium logger was configured.
		// Only log messages at LevelInfo or higher to avoid excessive noise from default logger.
		if level >= LevelInfo {
			log.Printf("[Xylium-InMemoryStore] "+format, args...)
		}
	}
}

// startCleanupRoutine starts the background goroutine responsible for periodically
// cleaning up expired visitor entries from the `InMemoryStore`.
// It uses `s.startOnce` to ensure that this goroutine is started at most once
// per `InMemoryStore` instance.
// The goroutine will only start if `s.cleanupInterval` is positive.
func (s *InMemoryStore) startCleanupRoutine() {
	s.startOnce.Do(func() { // Ensure this block runs only once.
		if s.cleanupInterval <= 0 {
			s.logf(LevelDebug, "InMemoryStore: Cleanup interval is not positive (%v), cleanup routine not started.", s.cleanupInterval)
			return
		}
		s.logf(LevelInfo, "InMemoryStore: Starting background cleanup routine with interval %v.", s.cleanupInterval)

		go func() {
			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop() // Important to stop the ticker when the goroutine exits.

			for {
				select {
				case <-ticker.C: // Wait for the next tick.
					s.mu.RLock() // Acquire read lock to safely check s.isClosed.
					closed := s.isClosed
					s.mu.RUnlock()

					if closed {
						// If store is marked as closed, stop the cleanup routine.
						s.logf(LevelDebug, "InMemoryStore cleanup routine: store is marked as closed, exiting loop.")
						return
					}
					s.cleanup() // Perform the cleanup operation. cleanup() handles its own locking.

				case <-s.stopCleanup: // Listen for a signal on the stopCleanup channel.
					// This channel is closed by InMemoryStore.Close() to signal shutdown.
					s.logf(LevelInfo, "InMemoryStore cleanup routine: stop signal received via channel, exiting.")
					return // Exit the goroutine.
				}
			}
		}()
	})
}

// Allow implements the `LimiterStore` interface. It checks if a request associated
// with `key` should be permitted based on the `limit` (max requests) and `window` duration.
// It updates the request count and window information for the `key`.
// This method is thread-safe.
func (s *InMemoryStore) Allow(key string, limit int, window time.Duration) (bool, int, int, time.Time) {
	s.mu.Lock() // Acquire full lock as we might modify the `visitors` map.
	defer s.mu.Unlock()

	if s.isClosed {
		// If the store has been closed (e.g., during application shutdown),
		// deny all new requests to prevent issues.
		s.logf(LevelWarn, "InMemoryStore: Allow called on a closed store for key '%s'. Denying request.", key)
		// Return values indicating denial: currentCount > limit, and windowEnds can be arbitrary (now).
		return false, limit + 1, limit, time.Now()
	}

	now := time.Now()
	v, exists := s.visitors[key]

	// If the visitor `key` doesn't exist in the map, or if their previous window has expired.
	if !exists || now.After(v.windowEnds) {
		// This is the first request in a new window for this key.
		newWindowEnds := now.Add(window) // Calculate when the new window will end.
		s.visitors[key] = &visitor{
			count:      1,             // First request in this window.
			lastSeen:   now,           // Record the time of this request.
			windowEnds: newWindowEnds, // Store the new window end time.
		}
		// Request is allowed. Return true, current count (1), configured limit, and new window end time.
		return true, 1, limit, newWindowEnds
	}

	// Visitor `key` exists and is within their current rate limit window.
	v.count++        // Increment their request count.
	v.lastSeen = now // Update their last seen time.
	// Request is allowed if their new count is less than or equal to the limit.
	// Return allowance status, new count, configured limit, and existing window end time.
	return v.count <= limit, v.count, limit, v.windowEnds
}

// cleanup is an internal method called periodically by the background goroutine
// (if `cleanupInterval` is positive) to remove expired visitor entries from the `visitors` map.
// An entry is considered expired if its `windowEnds` time is in the past.
// This method acquires a full lock on `s.mu` to safely modify the map.
func (s *InMemoryStore) cleanup() {
	s.mu.Lock() // Acquire full lock as we are modifying the `visitors` map.
	defer s.mu.Unlock()

	// Double-check if the store was closed between the check in the goroutine loop
	// and acquiring the lock here.
	if s.isClosed {
		return
	}

	now := time.Now()
	cleanedCount := 0
	for key, v := range s.visitors {
		if now.After(v.windowEnds) { // If the visitor's window has expired.
			delete(s.visitors, key) // Remove the entry from the map.
			cleanedCount++
		}
	}
	if cleanedCount > 0 {
		s.logf(LevelDebug, "InMemoryStore: Cleaned up %d expired visitor entries.", cleanedCount)
	}
}

// Close implements the `LimiterStore` interface. It signals the background cleanup
// goroutine (if one was started) to stop, and clears the internal `visitors` map
// to release memory. This is essential for graceful shutdown of the application.
//
// `Close` uses `s.closeOnce` to ensure its core logic (marking as closed, signaling
// the goroutine, clearing the map) executes at most once, making it safe to call
// `Close` multiple times.
func (s *InMemoryStore) Close() error {
	s.closeOnce.Do(func() { // Ensure this block runs only once.
		s.logf(LevelInfo, "InMemoryStore: Close() called. Initiating shutdown sequence...")

		// First, mark the store as closed. This prevents new `Allow` operations from
		// succeeding and signals the cleanup goroutine (if it checks `s.isClosed`).
		s.mu.Lock()
		s.isClosed = true
		s.mu.Unlock()

		// Signal the cleanup goroutine to stop by closing the `stopCleanup` channel.
		// This is the primary mechanism to gracefully terminate the goroutine.
		// Check if the channel is already closed to prevent a panic from `close()` on a closed channel.
		select {
		case <-s.stopCleanup:
			// Channel was already closed (e.g., Close called multiple times, or goroutine exited for other reasons).
			s.logf(LevelDebug, "InMemoryStore: stopCleanup channel was already closed during Close().")
		default:
			// Channel is open (or was just created and not yet closed), so close it.
			// This is only strictly necessary if the cleanup routine might have been started.
			if s.cleanupInterval > 0 {
				s.logf(LevelDebug, "InMemoryStore: Signaling background cleanup routine to stop via channel.")
				close(s.stopCleanup)
			} else {
				s.logf(LevelDebug, "InMemoryStore: Cleanup routine was not configured to run (interval <= 0), no channel to close.")
			}
		}

		// Finally, clear the `visitors` map to release all stored visitor data and free memory.
		// This should be done after signaling the goroutine, as a final cleanup step.
		s.mu.Lock()
		s.visitors = make(map[string]*visitor) // Replace with a new, empty map.
		s.mu.Unlock()
		s.logf(LevelInfo, "InMemoryStore: Successfully closed and visitors map cleared.")
	})
	return nil // InMemoryStore.Close() currently does not return errors.
}

// RateLimiterConfig holds the configuration options for the rate limiter middleware.
// This middleware restricts the number of requests a client (identified by a key,
// typically IP address) can make within a specified time window.
type RateLimiterConfig struct {
	// MaxRequests is the maximum number of requests allowed from a single key
	// within the `WindowDuration`. Must be greater than 0.
	MaxRequests int
	// WindowDuration is the time duration of the rate-limiting window.
	// Must be greater than 0.
	WindowDuration time.Duration

	// Message is the content of the response body sent to the client when the
	// rate limit is exceeded (resulting in an HTTP 429 Too Many Requests).
	// - If `string`: This string is used as the response body. If empty, a default
	//   message like "Rate limit exceeded. Try again in X seconds." is used.
	// - If `func(c *Context, limit int, window time.Duration, resetTime time.Time) string`:
	//   This function is called to dynamically generate the response message.
	//   It receives the `xylium.Context`, the configured limit, window duration,
	//   and the calculated time when the rate limit for the client will reset.
	// - If `nil` or any other type: A default message is used.
	Message interface{}

	// KeyGenerator is a function that generates a unique string key for each incoming
	// request to identify the entity being rate-limited.
	// If nil, it defaults to using the client's real IP address (`c.RealIP()`).
	// Custom generators can be used for user ID-based, API key-based, or other
	// forms of rate limiting.
	KeyGenerator func(c *Context) string

	// Store is an instance of `LimiterStore` used to store and manage rate limit counts.
	// If nil, a new `xylium.InMemoryStore` with default settings will be created
	// and used by this middleware instance. If multiple `RateLimiter` middlewares
	// are used and `Store` is nil for each, they will each have their own independent
	// `InMemoryStore`. For shared state, create a single `LimiterStore` instance and
	// pass it to each `RateLimiterConfig`.
	// If an `InMemoryStore` is created internally, Xylium's router will attempt to
	// register it for graceful shutdown. If you provide a custom store, ensure you
	// register it with the router via `app.RegisterCloser()` if it needs cleanup.
	Store LimiterStore

	// Skip is an optional function that, if provided and returns true, will cause
	// the rate limiter middleware to bypass rate limiting for the current request.
	// This can be used to exclude certain paths (e.g., health checks, static assets)
	// or specific clients from rate limiting.
	Skip func(c *Context) bool

	// SendRateLimitHeaders controls when standard rate limit headers are sent:
	//   - `SendHeadersAlways` (default): Headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`,
	//     `X-RateLimit-Reset`) are sent on every request.
	//   - `SendHeadersOnLimit`: Headers are sent only when a request is denied due to rate limiting.
	//   - `SendHeadersNever`: Rate limit headers are never sent.
	// If empty, defaults to `SendHeadersAlways`.
	SendRateLimitHeaders string // Use constants SendHeadersAlways, SendHeadersOnLimit, SendHeadersNever.

	// RetryAfterMode controls the format of the "Retry-After" header sent when a limit is hit:
	//   - `RetryAfterSeconds` (default): Value is the number of seconds until the limit resets.
	//   - `RetryAfterHTTPDate`: Value is an HTTP-date string indicating when the limit resets.
	// If empty, defaults to `RetryAfterSeconds`.
	RetryAfterMode string // Use constants RetryAfterSeconds, RetryAfterHTTPDate.

	// LoggerForStore is an optional `xylium.Logger` instance that will be passed to
	// an internally created `InMemoryStore` (if `config.Store` is nil).
	// This allows the internal store to use the application's logging conventions.
	// If `config.Store` is provided (a custom store), this field is ignored for that store.
	LoggerForStore Logger
}

// Constants for `RateLimiterConfig.SendRateLimitHeaders`
const (
	SendHeadersAlways  = "always"   // Send X-RateLimit-* headers on every request.
	SendHeadersOnLimit = "on_limit" // Send X-RateLimit-* headers only when a request is rate-limited.
	SendHeadersNever   = "never"    // Never send X-RateLimit-* headers.
)

// Constants for `RateLimiterConfig.RetryAfterMode`
const (
	RetryAfterSeconds  = "seconds_to_reset" // "Retry-After" header value is in seconds.
	RetryAfterHTTPDate = "http_date"        // "Retry-After" header value is an HTTP-date string.
)

// RateLimiter returns a Xylium `Middleware` that applies rate limiting based on the
// provided `RateLimiterConfig`.
//
// Panics:
//   - If `config.MaxRequests` is not greater than 0.
//   - If `config.WindowDuration` is not greater than 0.
//
// If `config.Store` is nil, a new `xylium.InMemoryStore` is created for this middleware
// instance. This internal store will be automatically registered with the Xylium router
// for graceful shutdown if the middleware is used. If you use multiple `RateLimiter`
// middlewares and want them to share state, create a single `LimiterStore` instance
// and pass it via `config.Store` to each, then register that shared store with the
// router using `app.RegisterCloser(mySharedStore)`.
func RateLimiter(config RateLimiterConfig) Middleware {
	// --- Validate Mandatory Configuration ---
	if config.MaxRequests <= 0 {
		panic("xylium: RateLimiterConfig.MaxRequests must be greater than 0")
	}
	if config.WindowDuration <= 0 {
		panic("xylium: RateLimiterConfig.WindowDuration must be greater than 0")
	}

	// --- Apply Defaults for Optional Configuration Fields ---
	if config.KeyGenerator == nil {
		config.KeyGenerator = func(c *Context) string { return c.RealIP() } // Default to client's real IP.
	}

	var internallyCreatedStore LimiterStore // Track if store was created by this middleware instance.
	if config.Store == nil {
		// If no store is provided, create a default InMemoryStore.
		var storeOpts []InMemoryStoreOption
		if config.LoggerForStore != nil {
			// Pass the optional logger to the InMemoryStore.
			storeOpts = append(storeOpts, WithLogger(config.LoggerForStore))
		}
		newStore := NewInMemoryStore(storeOpts...)
		config.Store = newStore
		internallyCreatedStore = newStore // Mark that this store was created internally.
	}

	if config.SendRateLimitHeaders == "" {
		config.SendRateLimitHeaders = SendHeadersAlways // Default header sending policy.
	}
	if config.RetryAfterMode == "" {
		config.RetryAfterMode = RetryAfterSeconds // Default Retry-After format.
	}

	// --- Return the Middleware Handler Function ---
	return func(next HandlerFunc) HandlerFunc {
		// `registerStoreOnce` ensures that if this middleware instance created an internal store,
		// it's registered with the router for graceful shutdown only once, even if the
		// middleware instance is somehow reused across multiple route definitions (though
		// typically a new RateLimiter(config) call is made per route/group if different configs needed).
		var registerStoreOnce sync.Once

		return func(c *Context) error {
			// If this middleware instance created its own InMemoryStore, and if a router
			// is available on the context, register the store for graceful shutdown.
			if internallyCreatedStore != nil && c.router != nil {
				registerStoreOnce.Do(func() {
					// `c.router.addInternalStore` is an unexported method to register stores
					// created by Xylium's own components.
					c.router.addInternalStore(internallyCreatedStore)
				})
			}

			// Get a request-scoped logger.
			logger := c.Logger().WithFields(M{"middleware": "RateLimiter"})

			// If a Skip function is provided and returns true, bypass rate limiting.
			if config.Skip != nil && config.Skip(c) {
				logger.Debugf("RateLimiter: Skipping rate limit check for %s %s due to Skip function.", c.Method(), c.Path())
				return next(c)
			}

			// Generate the key for this request.
			key := config.KeyGenerator(c)

			// Check with the store if the request is allowed.
			allowed, currentCount, configuredLimit, windowEnds := config.Store.Allow(key, config.MaxRequests, config.WindowDuration)
			now := time.Now()

			// Calculate remaining requests. Ensure it's not negative.
			remainingRequests := configuredLimit - currentCount
			if !allowed { // If denied, remaining is 0.
				remainingRequests = 0
			} else if remainingRequests < 0 { // Defensive: should not happen if store.Allow is correct.
				remainingRequests = 0
			}

			// Prepare rate limit headers.
			headersMap := make(map[string]string)
			headersMap["X-RateLimit-Limit"] = strconv.Itoa(configuredLimit)
			headersMap["X-RateLimit-Remaining"] = strconv.Itoa(remainingRequests)

			var resetValueStr string
			secondsToReset := int(windowEnds.Sub(now).Seconds())
			if secondsToReset < 0 { // If window has already ended but somehow not reset.
				secondsToReset = 0
			}

			if config.RetryAfterMode == RetryAfterHTTPDate {
				resetValueStr = windowEnds.UTC().Format(http.TimeFormat) // Format as HTTP-date.
			} else { // Default to seconds.
				resetValueStr = strconv.Itoa(secondsToReset)
			}
			headersMap["X-RateLimit-Reset"] = resetValueStr

			// If the request is NOT allowed (rate limit exceeded).
			if !allowed {
				logger.Warnf(
					"RateLimiter: Limit exceeded for key '%s' on request %s %s. Count: %d/%d. Window ends: %s (in %d seconds).",
					key, c.Method(), c.Path(), currentCount, configuredLimit, windowEnds.Format(DefaultTimestampFormat), secondsToReset,
				)
				// Set the "Retry-After" header.
				c.SetHeader("Retry-After", resetValueStr)

				// Send other X-RateLimit-* headers based on configuration.
				if config.SendRateLimitHeaders == SendHeadersAlways || config.SendRateLimitHeaders == SendHeadersOnLimit {
					for hKey, hVal := range headersMap {
						c.SetHeader(hKey, hVal)
					}
				}

				// Determine the error response message.
				var errorResponseMessage string
				switch msgProvider := config.Message.(type) {
				case string:
					if msgProvider == "" { // If string message is empty, use default.
						errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					} else {
						errorResponseMessage = msgProvider
					}
				case func(c *Context, limit int, window time.Duration, resetTime time.Time) string:
					if msgProvider != nil { // If function message provider exists.
						errorResponseMessage = msgProvider(c, configuredLimit, config.WindowDuration, windowEnds)
					} else { // Fallback if function provider is nil.
						errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
					}
				default: // Fallback for any other type or if config.Message is nil.
					errorResponseMessage = fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", secondsToReset)
				}
				// Return an HTTP 429 Too Many Requests error.
				return NewHTTPError(StatusTooManyRequests, errorResponseMessage)
			}

			// Request is allowed. Send X-RateLimit-* headers if configured for "always".
			if config.SendRateLimitHeaders == SendHeadersAlways {
				for hKey, hVal := range headersMap {
					c.SetHeader(hKey, hVal)
				}
			}

			// Proceed to the next handler in the chain.
			return next(c)
		}
	}
}
