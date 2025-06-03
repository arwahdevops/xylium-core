package xylium

import (
	"io"        // For io.Closer, used in closeApplicationResources.
	"log"       // Used by fasthttp as a fallback if its logger is nil, and for emergency logs.
	"net"       // For net.Conn, fasthttp.ConnState.
	"os"        // For os.Signal.
	"os/signal" // For graceful shutdown signal handling.
	"syscall"   // For syscall.SIGINT, syscall.SIGTERM.
	"time"      // For timeouts.

	"github.com/valyala/fasthttp" // The underlying HTTP server.
)

// ServerConfig holds a comprehensive set of configuration options for the underlying
// `fasthttp.Server` instance used by Xylium, as well as Xylium-specific
// application-level settings related to server operation and behavior.
//
// Instances of `ServerConfig` are typically passed to `xylium.NewWithConfig()` to
// customize the server. If `xylium.New()` is used, `DefaultServerConfig()` provides
// the initial settings.
type ServerConfig struct {
	// Name is the server name that will be sent in the "Server" HTTP response header.
	// If `NoDefaultServerHeader` is true, this header is suppressed.
	// Default: "Xylium Framework Server" (from `DefaultServerConfig()`).
	Name string

	// ReadTimeout specifies the maximum duration for reading the entire HTTP request,
	// including its body. If the request is not fully read within this duration,
	// the connection is closed.
	// Default: 60 seconds (from `DefaultServerConfig()`).
	ReadTimeout time.Duration

	// WriteTimeout specifies the maximum duration for writing the entire HTTP response.
	// If the response is not fully written within this duration, the connection is closed.
	// Default: 60 seconds (from `DefaultServerConfig()`).
	WriteTimeout time.Duration

	// IdleTimeout specifies the maximum duration a keep-alive connection will be kept
	// open while idle (waiting for the next request). If no new request arrives
	// within this duration, the connection is closed.
	// Default: 120 seconds (from `DefaultServerConfig()`).
	IdleTimeout time.Duration

	// MaxRequestBodySize defines the maximum allowed size for an incoming request body,
	// in bytes. If a request body exceeds this size, `fasthttp` will typically
	// return an HTTP 413 "Request Entity Too Large" error.
	// Default: 4MB (4 * 1024 * 1024) (from `DefaultServerConfig()`).
	MaxRequestBodySize int

	// ReduceMemoryUsage, if true, enables `fasthttp`'s memory reduction mode.
	// This can decrease memory allocations at the cost of potentially higher CPU usage.
	// Test with your specific workload to determine the impact.
	// Default: false (from `DefaultServerConfig()`).
	ReduceMemoryUsage bool

	// Concurrency defines the maximum number of concurrent client connections
	// the server will accept and process simultaneously. Requests beyond this limit
	// may be queued or rejected depending on system and `fasthttp` behavior.
	// Default: `fasthttp.DefaultConcurrency` (typically 256 * 1024).
	Concurrency int

	// DisableKeepalive, if true, disables HTTP keep-alive (persistent) connections.
	// Each request will then require a new TCP connection, which can impact performance.
	// Default: false (keep-alive is enabled by default).
	DisableKeepalive bool

	// TCPKeepalive, if true, enables TCP keep-alive probes for incoming client connections.
	// This helps detect and close stale connections at the TCP level.
	// Default: false.
	TCPKeepalive bool

	// TCPKeepalivePeriod specifies the duration between TCP keep-alive probes if
	// `TCPKeepalive` is true. If set to 0, the system's default TCP keep-alive period is used.
	// Default: 0 (system default).
	TCPKeepalivePeriod time.Duration

	// MaxConnsPerIP defines the maximum number of concurrent connections allowed
	// from a single client IP address. A value of 0 means no limit.
	// This can help mitigate simple denial-of-service attacks.
	// Default: 0 (unlimited).
	MaxConnsPerIP int

	// MaxRequestsPerConn defines the maximum number of requests that can be served
	// over a single keep-alive connection. After this many requests, the connection
	// will be closed. A value of 0 means no limit.
	// Default: 0 (unlimited).
	MaxRequestsPerConn int

	// GetOnly, if true, configures the server to accept only GET requests.
	// All other HTTP methods will be rejected, typically with an HTTP 405 "Method Not Allowed" error.
	// Default: false.
	GetOnly bool

	// DisableHeaderNamesNormalizing, if true, prevents `fasthttp` from normalizing
	// HTTP header names (e.g., converting "content-type" to "Content-Type").
	// Enabling this might be needed for compatibility with non-standard clients
	// but is generally not recommended.
	// Default: false (header names are normalized).
	DisableHeaderNamesNormalizing bool

	// NoDefaultServerHeader, if true, suppresses the automatic inclusion of the "Server"
	// HTTP response header (which would otherwise use the `ServerConfig.Name` value).
	// Default: false ("Server" header is included).
	NoDefaultServerHeader bool

	// NoDefaultDate, if true, suppresses the automatic inclusion of the "Date"
	// HTTP response header.
	// Default: false ("Date" header is included).
	NoDefaultDate bool

	// NoDefaultContentType, if true, suppresses the automatic setting of the
	// "Content-Type: text/plain; charset=utf-8" header for responses written
	// using low-level methods like `c.Write()` or `c.WriteString()` when no
	// `Content-Type` has been explicitly set by the application.
	// Default: false (a default `Content-Type` is set if none is provided).
	NoDefaultContentType bool

	// KeepHijackedConns, if true, instructs `fasthttp` not to automatically close
	// connections that have been hijacked (e.g., for WebSocket upgrades) when
	// the server is shutting down. The application becomes responsible for managing
	// the lifecycle of these hijacked connections.
	// Default: false (hijacked connections are typically closed on shutdown).
	KeepHijackedConns bool

	// CloseOnShutdown is a `fasthttp.Server` option.
	// If true (Xylium's default), `fasthttp` will actively close all open client connections
	// when its `server.Shutdown()` method is called as part of Xylium's graceful shutdown.
	// If false, `fasthttp` will stop accepting new connections but will wait for existing
	// connections to complete their current requests naturally or until they hit their
	// `IdleTimeout`. Xylium's `ShutdownTimeout` still acts as an overarching limit
	// for the entire application shutdown process.
	// Default: true (from `DefaultServerConfig()`).
	CloseOnShutdown bool

	// StreamRequestBody, if true, enables streaming of request bodies. This can be
	// beneficial for handling very large uploads, as it avoids buffering the entire
	// request body in memory before processing. When enabled, `c.Body()` might behave
	// differently, and the body should be consumed as a stream.
	// Default: false (request bodies are typically buffered by `fasthttp`).
	StreamRequestBody bool

	// Logger is the `xylium.Logger` instance to be used by the Xylium server and router
	// for all logging purposes.
	// If this field is `nil` when `xylium.NewWithConfig()` is called, a `DefaultLogger`
	// will be automatically initialized and configured. This internal configuration
	// considers Xylium's current operating mode (Debug, Test, Release) and any
	// settings provided in `ServerConfig.LoggerConfig`.
	// If a non-nil `Logger` is provided here, it will be used directly, and
	// `ServerConfig.LoggerConfig` will be ignored. The custom logger is then
	// responsible for its own configuration (level, output, format, etc.).
	Logger Logger

	// LoggerConfig provides detailed configuration options specifically for Xylium's
	// `DefaultLogger`. This field is only used if `ServerConfig.Logger` is `nil`
	// (i.e., if Xylium is creating a `DefaultLogger` internally).
	// Settings in `LoggerConfig` (e.g., Level, Formatter, ShowCaller, UseColor, Output)
	// can override the defaults that would otherwise be applied based on Xylium's
	// operating mode. Refer to `xylium.LoggerConfig` and `xylium.DefaultLoggerConfig()`
	// for details on available options.
	// If `ServerConfig.Logger` is provided with a custom logger instance, `LoggerConfig` is ignored.
	LoggerConfig *LoggerConfig

	// ConnState is an optional callback function that `fasthttp` invokes whenever a
	// client connection's state changes. The `net.Conn` represents the client connection,
	// and `fasthttp.ConnState` indicates the new state (e.g., `StateNew`, `StateActive`,
	// `StateIdle`, `StateHijacked`, `StateClosed`).
	// This callback can be useful for implementing custom connection metrics,
	// advanced connection management, or debugging connection-related issues.
	// Default: nil (no callback).
	ConnState func(conn net.Conn, state fasthttp.ConnState)

	// ShutdownTimeout is Xylium's application-level timeout for the entire graceful
	// shutdown process. This duration begins when a shutdown signal (SIGINT, SIGTERM)
	// is received. It encompasses the time taken for the underlying `fasthttp.Server`
	// to shut down (which respects `fasthttp.Server.IdleTimeout` and `CloseOnShutdown`)
	// AND the time taken for Xylium to close all registered application resources
	// (via `router.closeApplicationResources()`, which includes `io.Closer` instances
	// from `AppSet` or `RegisterCloser`).
	// If the entire shutdown process exceeds this `ShutdownTimeout`, the Xylium
	// application will forcefully exit.
	// Default: 15 seconds (from `DefaultServerConfig()`).
	ShutdownTimeout time.Duration
}

// DefaultServerConfig returns a `ServerConfig` struct populated with sensible default values.
// These defaults are intended to provide a good starting point for most Xylium applications,
// balancing performance, security, and resource management.
//
// Key defaults include:
//   - Server Name: "Xylium Framework Server"
//   - Timeouts: Read (60s), Write (60s), Idle (120s)
//   - MaxRequestBodySize: 4MB
//   - Concurrency: `fasthttp.DefaultConcurrency`
//   - Logger: A `DefaultLogger` will be configured based on mode and `LoggerConfig`.
//   - `LoggerConfig`: Uses `xylium.DefaultLoggerConfig()` (LevelInfo, TextFormatter, etc.).
//   - `CloseOnShutdown`: true (for `fasthttp` behavior)
//   - `ShutdownTimeout`: 15 seconds (for Xylium application-level graceful shutdown)
//
// Refer to the `ServerConfig` struct definition for details on all fields and their
// individual default behaviors if not explicitly set by `DefaultServerConfig()`.
func DefaultServerConfig() ServerConfig {
	defaultLogCfg := DefaultLoggerConfig() // Get Xylium's base default logger configuration.
	return ServerConfig{
		Name:               "Xylium Framework Server",
		ReadTimeout:        60 * time.Second,
		WriteTimeout:       60 * time.Second,
		IdleTimeout:        120 * time.Second,
		MaxRequestBodySize: 4 * 1024 * 1024, // 4MB
		Concurrency:        fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:  false, // fasthttp default is false
		Logger:             nil,   // Signals NewWithConfig to create and configure a DefaultLogger.
		LoggerConfig:       &defaultLogCfg,
		CloseOnShutdown:    true, // Xylium's preferred default for fasthttp's Shutdown behavior.
		ShutdownTimeout:    15 * time.Second,
		// Other fields like DisableKeepalive, TCPKeepalive, MaxConnsPerIP, GetOnly, etc.,
		// will default to their zero values (false, 0, nil), which generally align with
		// `fasthttp`'s own defaults or imply standard behavior.
	}
}

// loggerAdapter implements the `fasthttp.Logger` interface, allowing Xylium's
// configured `xylium.Logger` to be used for logging internal messages from the
// `fasthttp` server (e.g., errors during connection handling if not caught elsewhere).
type loggerAdapter struct {
	internalLogger Logger // Holds the `xylium.Logger` instance to which messages will be forwarded.
}

// Printf implements the `fasthttp.Logger` interface's `Printf` method.
// It forwards the formatted log message from `fasthttp` to the `internalLogger`'s
// `Infof` method. This ensures that `fasthttp`'s operational logs are integrated
// into Xylium's logging system, respecting its configured level, format, and output.
//
// If `internalLogger` is somehow nil (which should not happen in a correctly
// initialized Xylium application), it falls back to using the standard Go `log` package.
func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	if la.internalLogger != nil {
		// Log messages from fasthttp at INFO level. These are often operational
		// (e.g., server start/stop if fasthttp logs them) or low-level errors.
		la.internalLogger.Infof(format, args...)
	} else {
		// Fallback to standard log package if internalLogger is nil.
		// This is a defensive measure; a Xylium application should always have its logger configured.
		// Using a distinct prefix helps identify these rare fallback logs.
		log.Printf("[XYLIUM-FasthttpLoggerAdapter-FALLBACK] "+format, args...)
	}
}

// buildFasthttpServer constructs and configures a new `*fasthttp.Server` instance
// based on the settings defined in the `Router`'s `r.serverConfig`.
// This method is called internally by Xylium's server listening methods
// (e.g., `ListenAndServeGracefully`, `Start`).
//
// The `r.serverConfig.Logger` is guaranteed to be non-nil at this point, as it is
// initialized by `router.NewWithConfig` if it was originally nil.
// This logger is adapted for `fasthttp` using `loggerAdapter`.
//
// If the router is in `DebugMode`, it logs key server configuration details
// for easier diagnostics during development.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	// Adapt Xylium's logger for use by fasthttp. r.serverConfig.Logger is non-nil.
	fasthttpCompatibleLogger := &loggerAdapter{internalLogger: r.serverConfig.Logger}

	// Log detailed server configuration if in DebugMode.
	if r.CurrentMode() == DebugMode {
		cfgLog := r.Logger().WithFields(M{"component": "xylium-server-builder", "operation": "buildFasthttpServer"})
		cfgLog.Debugf("Building fasthttp.Server with Name: '%s'", r.serverConfig.Name)
		cfgLog.Debugf("Timeouts (Read/Write/Idle): %v / %v / %v", r.serverConfig.ReadTimeout, r.serverConfig.WriteTimeout, r.serverConfig.IdleTimeout)
		cfgLog.Debugf("Limits (MaxBodySize Bytes: %d, Concurrency: %d, MaxConnsPerIP: %d, MaxReqsPerConn: %d)",
			r.serverConfig.MaxRequestBodySize, r.serverConfig.Concurrency, r.serverConfig.MaxConnsPerIP, r.serverConfig.MaxRequestsPerConn)
		cfgLog.Debugf("KeepAlive (DisableKeepalive: %t, TCPKeepalive: %t, TCPKeepalivePeriod: %v)",
			r.serverConfig.DisableKeepalive, r.serverConfig.TCPKeepalive, r.serverConfig.TCPKeepalivePeriod)
		cfgLog.Debugf("Fasthttp.CloseOnShutdown: %t, XyliumApp.ShutdownTimeout: %v", r.serverConfig.CloseOnShutdown, r.serverConfig.ShutdownTimeout)
		cfgLog.Debugf("Header Handling (DisableNormalization: %t, NoDefaultServer: %t, NoDefaultDate: %t, NoDefaultContentType: %t)",
			r.serverConfig.DisableHeaderNamesNormalizing, r.serverConfig.NoDefaultServerHeader, r.serverConfig.NoDefaultDate, r.serverConfig.NoDefaultContentType)
		cfgLog.Debugf("Other Settings (ReduceMemoryUsage: %t, GetOnly: %t, StreamRequestBody: %t, KeepHijackedConns: %t)",
			r.serverConfig.ReduceMemoryUsage, r.serverConfig.GetOnly, r.serverConfig.StreamRequestBody, r.serverConfig.KeepHijackedConns)
		if r.serverConfig.ConnState != nil {
			cfgLog.Debugf("ConnState callback is configured.")
		}
	}

	// Construct and return the fasthttp.Server instance.
	return &fasthttp.Server{
		Handler:                       r.Handler, // Xylium's main request router/handler.
		Name:                          r.serverConfig.Name,
		ReadTimeout:                   r.serverConfig.ReadTimeout,
		WriteTimeout:                  r.serverConfig.WriteTimeout,
		IdleTimeout:                   r.serverConfig.IdleTimeout,
		MaxRequestBodySize:            r.serverConfig.MaxRequestBodySize,
		ReduceMemoryUsage:             r.serverConfig.ReduceMemoryUsage,
		Concurrency:                   r.serverConfig.Concurrency,
		DisableKeepalive:              r.serverConfig.DisableKeepalive,
		TCPKeepalive:                  r.serverConfig.TCPKeepalive,
		TCPKeepalivePeriod:            r.serverConfig.TCPKeepalivePeriod,
		MaxConnsPerIP:                 r.serverConfig.MaxConnsPerIP,
		MaxRequestsPerConn:            r.serverConfig.MaxRequestsPerConn,
		GetOnly:                       r.serverConfig.GetOnly,
		DisableHeaderNamesNormalizing: r.serverConfig.DisableHeaderNamesNormalizing,
		NoDefaultServerHeader:         r.serverConfig.NoDefaultServerHeader,
		NoDefaultDate:                 r.serverConfig.NoDefaultDate,
		NoDefaultContentType:          r.serverConfig.NoDefaultContentType,
		KeepHijackedConns:             r.serverConfig.KeepHijackedConns,
		CloseOnShutdown:               r.serverConfig.CloseOnShutdown,
		StreamRequestBody:             r.serverConfig.StreamRequestBody,
		Logger:                        fasthttpCompatibleLogger, // Use the adapted Xylium logger.
		ConnState:                     r.serverConfig.ConnState,
		// Other fasthttp.Server fields like TLSConfig, MaxHeaderBytes, etc.,
		// are not directly exposed via Xylium's ServerConfig but could be added if needed.
		// For TLS, specific ListenAndServeTLS* methods handle TLS configuration.
	}
}

// ListenAndServe starts an HTTP server on the given network address `addr`.
// This method is a blocking call. It does *not* implement Xylium's graceful shutdown
// mechanism (handling OS signals for termination). For production environments,
// `ListenAndServeGracefully` or its alias `Start` are strongly recommended.
//
// If the server fails to start (e.g., if the address is already in use), this method
// will return an error. In such cases, it also attempts to close any application
// resources that were registered with the Xylium router (via `AppSet` or `RegisterCloser`).
//
// In `DebugMode`, registered routes are printed to the logger before starting.
func (r *Router) ListenAndServe(addr string) error {
	currentLogger := r.Logger()
	// Print routes if in DebugMode and the route tree exists.
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServe on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}

	server := r.buildFasthttpServer() // Construct the fasthttp server.
	currentLogger.Infof("Xylium HTTP server listening on %s (Mode: %s, Graceful Shutdown: No)", addr, r.CurrentMode())

	// Start the fasthttp server. This is a blocking call.
	err := server.ListenAndServe(addr)

	// After ListenAndServe returns (either due to error or server stop),
	// attempt to close application resources. This is important even if startup failed,
	// in case some resources were partially initialized.
	r.closeApplicationResources()
	return err
}

// ListenAndServeTLS starts an HTTPS server on the given network address `addr`,
// using the certificate from `certFile` and the private key from `keyFile`.
// This method is a blocking call and does *not* implement Xylium's graceful shutdown.
// For production HTTPS servers, `ListenAndServeTLSGracefully` is recommended.
//
// If the server fails to start, it returns an error and attempts to close registered
// application resources.
// In `DebugMode`, registered routes are printed before starting.
func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLS on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server listening on %s (Mode: %s, Graceful Shutdown: No, CertFile: %s, KeyFile: %s)", addr, r.CurrentMode(), certFile, keyFile)
	err := server.ListenAndServeTLS(addr, certFile, keyFile)
	r.closeApplicationResources()
	return err
}

// ListenAndServeTLSEmbed starts an HTTPS server on the given network address `addr`,
// using an in-memory TLS certificate (`certData`) and private key (`keyData`),
// provided as byte slices. This is useful for embedding TLS credentials directly
// into the application binary.
//
// This method is a blocking call and does *not* implement Xylium's graceful shutdown.
// For production HTTPS servers with embedded credentials, `ListenAndServeTLSEmbedGracefully`
// is recommended.
//
// If the server fails to start, it returns an error and attempts to close registered
// application resources.
// In `DebugMode`, registered routes are printed before starting.
func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbed on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server (with embedded certs) listening on %s (Mode: %s, Graceful Shutdown: No)", addr, r.CurrentMode())
	err := server.ListenAndServeTLSEmbed(addr, certData, keyData)
	r.closeApplicationResources()
	return err
}

// closeApplicationResources is an internal helper method responsible for closing all
// Xylium-internal and user-registered resources that implement `io.Closer`.
// This is a crucial part of the graceful shutdown process.
//
// It iterates through:
//  1. Internally created `LimiterStore` instances (e.g., from `RateLimiter` middleware
//     using the default `InMemoryStore`).
//  2. `io.Closer` instances that were stored in the application store via `r.AppSet()`
//     or explicitly registered using `r.RegisterCloser()`.
//
// For each resource, it calls its `Close()` method and logs any errors encountered
// during closure. This method is designed to be called once during application
// termination (either graceful shutdown or after a server startup failure).
// Access to internal lists of closers is thread-safe.
func (r *Router) closeApplicationResources() {
	currentLogger := r.Logger()
	currentLogger.Debug("Initiating closure of all registered Xylium application resources...")

	// --- Close Xylium-internal rate limiter stores ---
	r.internalRateLimitStoresMux.Lock() // Lock access to the internal stores slice.
	if len(r.internalRateLimitStores) > 0 {
		currentLogger.Infof("Closing %d Xylium-internal rate limiter store(s)...", len(r.internalRateLimitStores))
		// Iterate in reverse order as a defensive measure, in case Close() could modify the slice
		// (though standard LimiterStore.Close() shouldn't).
		for i := len(r.internalRateLimitStores) - 1; i >= 0; i-- {
			store := r.internalRateLimitStores[i]
			currentLogger.Debugf("Attempting to close internal rate limiter store (type %T, instance %p)...", store, store)
			if err := store.Close(); err != nil {
				currentLogger.Errorf("Error closing internal rate limiter store (type %T, instance %p): %v", store, store, err)
			}
		}
		// Clear the slice after attempting to close all.
		r.internalRateLimitStores = make([]LimiterStore, 0)
		currentLogger.Info("All Xylium-internal rate limiter stores have been processed for closure.")
	} else {
		currentLogger.Debug("No Xylium-internal rate limiter stores were registered to close.")
	}
	r.internalRateLimitStoresMux.Unlock()

	// --- Close user-registered io.Closer resources ---
	r.closersMux.Lock() // Lock access to the user-registered closers slice.
	if len(r.closers) > 0 {
		currentLogger.Infof("Closing %d registered application resource(s) (implementing io.Closer)...", len(r.closers))
		for i := len(r.closers) - 1; i >= 0; i-- {
			closer := r.closers[i]
			currentLogger.Debugf("Attempting to close registered application resource (type %T, instance %p)...", closer, closer)
			if err := closer.Close(); err != nil {
				currentLogger.Errorf("Error closing registered application resource (type %T, instance %p): %v", closer, closer, err)
			}
		}
		r.closers = make([]io.Closer, 0) // Clear the slice.
		currentLogger.Info("All user-registered application resources (io.Closer) have been processed for closure.")
	} else {
		currentLogger.Debug("No user-registered application resources (io.Closer) were found for graceful shutdown.")
	}
	r.closersMux.Unlock()

	currentLogger.Info("Xylium application resource closure process has finished.")
}

// commonGracefulShutdownLogic encapsulates the shared operational logic for initiating
// and managing a graceful shutdown of the `fasthttp.Server` and Xylium application resources.
// It listens for OS interrupt signals (SIGINT, SIGTERM), triggers the server shutdown,
// waits for it to complete (or times out according to `r.serverConfig.ShutdownTimeout`),
// and then closes all registered Xylium application resources.
//
// This function is used by all `ListenAndServe*Gracefully` methods.
//
// Parameters:
//   - `server` (*fasthttp.Server): The configured `fasthttp.Server` instance to manage.
//   - `startServerFunc` (func() error): A function that, when called, starts the
//     `fasthttp.Server`'s listening loop (e.g., `server.ListenAndServe(addr)`).
//     This function should be blocking and return an error if server startup or
//     operation fails (other than errors related to normal shutdown).
//
// Returns:
//   - `error`: An error if the server failed to start initially or if a critical
//     error occurred during the shutdown process itself.
//   - `nil`: If the shutdown sequence was initiated successfully (either completed
//     gracefully or timed out as per configuration). The server will no longer be listening.
func (r *Router) commonGracefulShutdownLogic(server *fasthttp.Server, startServerFunc func() error) error {
	currentLogger := r.Logger()
	// Note: Route printing and "listening gracefully on ADDR (Mode: X)" messages
	// are handled by the specific ListenAndServe*Gracefully methods before calling this.

	// Channel to capture errors from the server's listening loop (startServerFunc).
	serverErrors := make(chan error, 1)

	// Goroutine to run the fasthttp server.
	// This allows the main goroutine to listen for shutdown signals concurrently.
	go func() {
		// Call the provided function to start the server (e.g., server.ListenAndServe).
		if err := startServerFunc(); err != nil {
			// If startServerFunc returns an error (e.g., address in use, permission denied),
			// send it to the serverErrors channel.
			// A `nil` error from ListenAndServe usually means it was shut down normally.
			serverErrors <- err
		} else {
			// If startServerFunc returns nil, it usually means the server was shut down gracefully.
			close(serverErrors) // Close channel to signal completion without error.
		}
	}()

	// Channel to listen for OS shutdown signals (SIGINT for Ctrl+C, SIGTERM for termination).
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Main select loop: waits for either a server error or a shutdown signal.
	select {
	case err, ok := <-serverErrors:
		// The server's listening loop (startServerFunc) exited.
		if ok && err != nil {
			// An actual error occurred during server startup or operation.
			currentLogger.Errorf("Xylium server failed to start or encountered a runtime error: %v", err)
			// Attempt to clean up Xylium resources even if server startup failed,
			// as some might have been partially initialized or registered.
			r.closeApplicationResources()
			return err // Propagate the server error.
		}
		// If !ok or err is nil, it means server goroutine exited cleanly (likely due to shutdown).
		// This path is usually taken if Shutdown() was called from elsewhere or if SIGINT/SIGTERM
		// was handled very quickly causing the server to stop before this select hit the signal.
		currentLogger.Info("Xylium server's listening goroutine exited (likely due to shutdown signal or pre-emptive stop).")
		// Ensure resources are closed.
		r.closeApplicationResources()
		return nil // No error from Xylium's perspective if server stopped cleanly.

	case sig := <-shutdownChan:
		// An OS shutdown signal was received.
		currentLogger.Infof("Shutdown signal '%s' received. Initiating graceful shutdown of Xylium application...", sig.String())

		// Determine the application-level shutdown timeout from ServerConfig.
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			// Ensure a positive timeout. Fallback to a sensible default if misconfigured.
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("ServerConfig.ShutdownTimeout is not configured or is invalid (<=0). Using default: %s for overall application shutdown.", shutdownTimeout.String())
		}
		currentLogger.Debugf("Application graceful shutdown timeout is %s.", shutdownTimeout.String())

		// Perform fasthttp server shutdown. This call is blocking, so run it in a goroutine
		// to allow Xylium's application-level `shutdownTimeout` to manage the overall process.
		shutdownComplete := make(chan struct{})
		go func() {
			defer close(shutdownComplete) // Signal that fasthttp.Shutdown attempt has finished.
			currentLogger.Debugf("Attempting to gracefully shut down the underlying fasthttp server...")
			if err := server.Shutdown(); err != nil {
				// `fasthttp.Server.Shutdown()` can return errors (e.g., if called multiple times,
				// or if context used for shutdown is canceled, though Xylium doesn't pass a context here).
				// `fasthttp.ErrServerClosed` is not an error in this context for `ListenAndServe` which returns nil on successful shutdown.
				// Xylium's logger (via loggerAdapter) inside fasthttp should log more details if fasthttp logs anything.
				currentLogger.Errorf("Error reported by fasthttp server.Shutdown() call: %v. This may or may not be critical depending on the error.", err)
			}
		}()

		// Wait for fasthttp.Server.Shutdown() to complete or for Xylium's app-level timeout.
		select {
		case <-shutdownComplete:
			currentLogger.Info("Underlying fasthttp server has been instructed to stop and has completed its shutdown routine.")
		case <-time.After(shutdownTimeout):
			// This timeout is for the entire shutdown process, including fasthttp's part.
			// If fasthttp.Shutdown() itself takes longer than this, this case will be hit.
			currentLogger.Warnf("Graceful shutdown of fasthttp server timed out after %s (application-level timeout). The server might not have fully released all its internal resources or connections.", shutdownTimeout.String())
		}

		// After fasthttp server shutdown (or timeout), close Xylium's application resources.
		r.closeApplicationResources()
		currentLogger.Info("Xylium application graceful shutdown process is complete.")
		return nil // Indicates a shutdown (graceful or timed out) was successfully initiated and processed.
	}
}

// ListenAndServeGracefully starts an HTTP server on the given network address `addr`
// with integrated graceful shutdown capabilities. It monitors OS signals (SIGINT, SIGTERM)
// to initiate a controlled shutdown, allowing active requests to complete and ensuring
// registered Xylium application resources (see `AppSet`, `RegisterCloser`) are properly closed.
//
// This is the recommended method for starting a Xylium HTTP server in production environments
// to ensure data integrity and prevent abrupt disconnections.
//
// In `DebugMode`, registered routes are printed to the logger before the server starts.
// The overall shutdown process is governed by `ServerConfig.ShutdownTimeout`.
func (r *Router) ListenAndServeGracefully(addr string) error {
	currentLogger := r.Logger()
	// Print registered routes if in DebugMode.
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer() // Construct the fasthttp.Server instance.

	// Define the function that will actually start the fasthttp server's listening loop.
	startFn := func() error {
		currentLogger.Infof("Xylium HTTP server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		return server.ListenAndServe(addr)
	}
	// Delegate to the common graceful shutdown logic.
	return r.commonGracefulShutdownLogic(server, startFn)
}

// ListenAndServeTLSGracefully starts an HTTPS server on `addr` using the provided
// certificate file (`certFile`) and private key file (`keyFile`), with integrated
// graceful shutdown capabilities. It handles OS signals for termination and resource cleanup.
//
// This is the recommended method for starting a Xylium HTTPS server with file-based
// certificates in production.
// The overall shutdown process is governed by `ServerConfig.ShutdownTimeout`.
func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()

	startFn := func() error {
		currentLogger.Infof("Xylium HTTPS server listening gracefully on %s (Mode: %s, CertFile: %s, KeyFile: %s)", addr, r.CurrentMode(), certFile, keyFile)
		return server.ListenAndServeTLS(addr, certFile, keyFile)
	}
	return r.commonGracefulShutdownLogic(server, startFn)
}

// ListenAndServeTLSEmbedGracefully starts an HTTPS server on `addr` using embedded
// TLS certificate (`certData`) and private key (`keyData`) byte slices, with integrated
// graceful shutdown capabilities. This method is suitable for deployments where TLS
// credentials are embedded directly in the application binary.
//
// It handles OS signals for termination and resource cleanup.
// The overall shutdown process is governed by `ServerConfig.ShutdownTimeout`.
func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbedGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()

	startFn := func() error {
		currentLogger.Infof("Xylium HTTPS server (with embedded certs) listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		return server.ListenAndServeTLSEmbed(addr, certData, keyData)
	}
	return r.commonGracefulShutdownLogic(server, startFn)
}

// Start is a convenience alias for `ListenAndServeGracefully(addr)`.
// It starts an HTTP server on the given network address `addr` and includes
// Xylium's full graceful shutdown mechanism, handling OS signals (SIGINT, SIGTERM)
// to allow active requests to complete and to close registered application resources.
//
// This is the most commonly recommended method for starting a Xylium server.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
