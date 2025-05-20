// src/xylium/router_server.go
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

// ServerConfig holds configuration options for the fasthttp server and Xylium's
// application-level settings related to server operation.
type ServerConfig struct {
	// Name is the server name, sent in the "Server" header if not disabled.
	// Default: "Xylium Framework Server".
	Name string

	// ReadTimeout is the maximum duration for reading the entire request, including body.
	// Default: 60 seconds.
	ReadTimeout time.Duration

	// WriteTimeout is the maximum duration for writing the entire response.
	// Default: 60 seconds.
	WriteTimeout time.Duration

	// IdleTimeout is the maximum duration to keep an idle keep-alive connection open.
	// Default: 120 seconds.
	IdleTimeout time.Duration

	// MaxRequestBodySize is the maximum request body size in bytes.
	// Default: 4MB (4 * 1024 * 1024).
	MaxRequestBodySize int

	// ReduceMemoryUsage, if true, enables fasthttp's memory reduction mode
	// at the cost of potentially higher CPU usage.
	// Default: false.
	ReduceMemoryUsage bool

	// Concurrency is the maximum number of concurrent connections the server will accept.
	// Default: fasthttp.DefaultConcurrency.
	Concurrency int

	// DisableKeepalive, if true, disables HTTP keep-alive connections.
	// Default: false.
	DisableKeepalive bool

	// TCPKeepalive, if true, enables TCP keep-alive periods for incoming connections.
	// Default: false.
	TCPKeepalive bool

	// TCPKeepalivePeriod is the duration for TCP keep-alive probes if TCPKeepalive is true.
	// Default: 0 (fasthttp default, typically system default).
	TCPKeepalivePeriod time.Duration

	// MaxConnsPerIP is the maximum number of concurrent connections allowed from a single IP address.
	// Default: 0 (unlimited).
	MaxConnsPerIP int

	// MaxRequestsPerConn is the maximum number of requests per keep-alive connection.
	// Default: 0 (unlimited).
	MaxRequestsPerConn int

	// GetOnly, if true, only accepts GET requests, rejecting others with 405 Method Not Allowed.
	// Default: false.
	GetOnly bool

	// DisableHeaderNamesNormalizing, if true, prevents fasthttp from normalizing
	// HTTP header names (e.g., to canonical MIME header format).
	// Default: false.
	DisableHeaderNamesNormalizing bool

	// NoDefaultServerHeader, if true, suppresses the automatic "Server" header.
	// Default: false.
	NoDefaultServerHeader bool

	// NoDefaultDate, if true, suppresses the automatic "Date" header.
	// Default: false.
	NoDefaultDate bool

	// NoDefaultContentType, if true, suppresses the automatic "Content-Type" header
	// (e.g., "text/plain; charset=utf-8") for responses written by c.Write or c.WriteString
	// if no Content-Type was explicitly set.
	// Default: false.
	NoDefaultContentType bool

	// KeepHijackedConns, if true, hijacked connections are not automatically closed
	// when the server shuts down.
	// Default: false.
	KeepHijackedConns bool

	// CloseOnShutdown is fasthttp's option. If true (default in Xylium), fasthttp actively
	// closes client connections when `server.Shutdown()` is called. If false, it waits
	// for them to complete naturally or hit their idle timeout.
	// Default: true.
	CloseOnShutdown bool

	// StreamRequestBody enables streaming request bodies, which can be useful for
	// handling large uploads without buffering the entire body in memory.
	// Default: false.
	StreamRequestBody bool

	// Logger is the Xylium logger instance to be used by the server and router.
	// If nil when creating a Router (via NewWithConfig), a DefaultLogger will be
	// initialized and configured based on Xylium's operating mode and LoggerConfig.
	Logger Logger

	// LoggerConfig provides detailed configuration for the DefaultLogger if `Logger` is nil
	// and a DefaultLogger is being created internally by Xylium.
	// If `Logger` is provided, this field is ignored.
	LoggerConfig *LoggerConfig

	// ConnState is an optional callback function that fasthttp calls when a
	// client connection's state changes (e.g., new, active, idle, hijacked).
	// Useful for metrics or advanced connection management.
	ConnState func(conn net.Conn, state fasthttp.ConnState)

	// ShutdownTimeout is Xylium's application-level graceful shutdown timeout.
	// This is the total time allowed for the fasthttp server to shut down AND
	// for Xylium to close its registered application resources (e.g., those set via AppSet).
	// Default: 15 seconds.
	ShutdownTimeout time.Duration
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
// These defaults provide a good starting point for most Xylium applications.
func DefaultServerConfig() ServerConfig {
	defaultLogCfg := DefaultLoggerConfig() // Get default logger configuration.
	return ServerConfig{
		Name:               "Xylium Framework Server",
		ReadTimeout:        60 * time.Second,
		WriteTimeout:       60 * time.Second,
		IdleTimeout:        120 * time.Second,
		MaxRequestBodySize: 4 * 1024 * 1024, // 4MB
		Concurrency:        fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:  false,
		Logger:             nil, // Will be initialized in router.NewWithConfig if nil.
		LoggerConfig:       &defaultLogCfg,
		CloseOnShutdown:    true, // Xylium default for fasthttp's behavior
		ShutdownTimeout:    15 * time.Second,
		// Other fields default to their zero values (false, 0, nil), which
		// generally align with fasthttp's defaults or mean "no special behavior".
	}
}

// loggerAdapter adapts a xylium.Logger to the fasthttp.Logger interface.
// fasthttp.Logger expects only a Printf method.
type loggerAdapter struct {
	internalLogger Logger // Holds the xylium.Logger instance.
}

// Printf implements the fasthttp.Logger interface by forwarding messages
// to the internal Xylium logger's Infof method. This ensures fasthttp's
// internal logs (e.g., errors during connection handling if not caught elsewhere)
// are processed by Xylium's configured logging system.
func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	if la.internalLogger != nil {
		// Log fasthttp internal messages at INFO level, as they are typically operational
		// or error-related but not necessarily Xylium application errors.
		la.internalLogger.Infof(format, args...)
	} else {
		// Fallback to standard log package if internalLogger is somehow nil.
		// This should not happen in a correctly initialized Xylium application.
		// Using a distinct prefix helps identify these rare fallback logs.
		log.Printf("[XYLIUM-FasthttpLoggerAdapter-FALLBACK] "+format, args...)
	}
}

// buildFasthttpServer constructs a new `fasthttp.Server` instance based on the
// Router's `ServerConfig`. This method is called internally by the server
// listening methods (e.g., `ListenAndServeGracefully`).
// The Router's `serverConfig.Logger` is guaranteed to be non-nil at this point,
// as it's initialized in `router.NewWithConfig`.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	fasthttpCompatibleLogger := &loggerAdapter{internalLogger: r.serverConfig.Logger}

	// Log server configuration details if in DebugMode for easier diagnostics.
	if r.CurrentMode() == DebugMode {
		cfgLog := r.Logger().WithFields(M{"component": "xylium-server-builder"})
		cfgLog.Debugf("Building fasthttp.Server with Name: '%s'", r.serverConfig.Name)
		cfgLog.Debugf("Timeouts (Read/Write/Idle): %v / %v / %v", r.serverConfig.ReadTimeout, r.serverConfig.WriteTimeout, r.serverConfig.IdleTimeout)
		cfgLog.Debugf("Limits (MaxBodySize/Concurrency): %d / %d", r.serverConfig.MaxRequestBodySize, r.serverConfig.Concurrency)
		cfgLog.Debugf("Fasthttp.CloseOnShutdown: %t, XyliumApp.ShutdownTimeout: %v", r.serverConfig.CloseOnShutdown, r.serverConfig.ShutdownTimeout)
		cfgLog.Debugf("Other settings (ReduceMemoryUsage: %t, DisableKeepalive: %t, StreamRequestBody: %t)",
			r.serverConfig.ReduceMemoryUsage, r.serverConfig.DisableKeepalive, r.serverConfig.StreamRequestBody)
	}

	return &fasthttp.Server{
		Handler:                       r.Handler, // Xylium's main request handler.
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
	}
}

// ListenAndServe starts an HTTP server on the given network address `addr`.
// This is a blocking call and does not implement graceful shutdown by default.
// For production environments, `ListenAndServeGracefully` (or its alias `Start`) is recommended.
// If the server fails to start (e.g., address in use), it attempts to close
// any application resources registered with the router.
func (r *Router) ListenAndServe(addr string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServe on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTP server listening on %s (Mode: %s, Graceful Shutdown: No)", addr, r.CurrentMode())
	err := server.ListenAndServe(addr)
	// Attempt to close resources even if ListenAndServe fails (e.g., on startup).
	r.closeApplicationResources()
	return err
}

// ListenAndServeTLS starts an HTTPS server on `addr` using the provided
// certificate file (`certFile`) and key file (`keyFile`).
// This is a blocking call without graceful shutdown. Consider `ListenAndServeTLSGracefully`.
// If the server fails, registered application resources are attempted to be closed.
func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLS on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server listening on %s (Mode: %s, Graceful Shutdown: No)", addr, r.CurrentMode())
	err := server.ListenAndServeTLS(addr, certFile, keyFile)
	r.closeApplicationResources()
	return err
}

// ListenAndServeTLSEmbed starts an HTTPS server on `addr` using embedded
// certificate (`certData`) and key (`keyData`) byte slices.
// This is a blocking call without graceful shutdown. Consider `ListenAndServeTLSEmbedGracefully`.
// If the server fails, registered application resources are attempted to be closed.
func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbed on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server (embedded certs) listening on %s (Mode: %s, Graceful Shutdown: No)", addr, r.CurrentMode())
	err := server.ListenAndServeTLSEmbed(addr, certData, keyData)
	r.closeApplicationResources()
	return err
}

// closeApplicationResources is a helper function to close all registered internal
// and application-level resources managed by the Xylium router. This includes
// internally created rate limiter stores (LimiterStore) and any io.Closer instances
// registered via `Router.AppSet` (if the value implements io.Closer) or `Router.RegisterCloser`.
// This method is called during the graceful shutdown process or if server startup methods fail.
// It ensures that resources are given a chance to clean up properly.
func (r *Router) closeApplicationResources() {
	currentLogger := r.Logger()
	currentLogger.Debug("Initiating closure of Xylium application resources...")

	// --- Close Xylium-internal rate limiter stores ---
	r.internalRateLimitStoresMux.Lock() // Ensure thread-safe access to the slice.
	if len(r.internalRateLimitStores) > 0 {
		currentLogger.Infof("Closing %d Xylium-internal rate limiter store(s)...", len(r.internalRateLimitStores))
		// Iterate in reverse order; defensive if Close() could modify the slice.
		for i := len(r.internalRateLimitStores) - 1; i >= 0; i-- {
			store := r.internalRateLimitStores[i]
			currentLogger.Debugf("Attempting to close internal rate limiter store (type %T)...", store)
			if err := store.Close(); err != nil {
				currentLogger.Errorf("Error closing internal rate limiter store (type %T): %v", store, err)
			}
		}
		r.internalRateLimitStores = make([]LimiterStore, 0) // Clear the slice after attempting to close all.
		currentLogger.Info("All Xylium-internal rate limiter stores attempted to close.")
	} else {
		currentLogger.Debug("No Xylium-internal rate limiter stores were registered to close.")
	}
	r.internalRateLimitStoresMux.Unlock()

	// --- Close user-registered io.Closer resources ---
	r.closersMux.Lock() // Ensure thread-safe access to the slice.
	if len(r.closers) > 0 {
		currentLogger.Infof("Closing %d registered application resource(s) (io.Closer)...", len(r.closers))
		// Iterate in reverse order for similar defensive reasons.
		for i := len(r.closers) - 1; i >= 0; i-- {
			closer := r.closers[i]
			currentLogger.Debugf("Attempting to close registered application resource (type %T)...", closer)
			if err := closer.Close(); err != nil {
				currentLogger.Errorf("Error closing registered application resource (type %T): %v", closer, err)
			}
		}
		r.closers = make([]io.Closer, 0) // Clear the slice after attempting to close all.
		currentLogger.Info("All registered application resources attempted to close.")
	} else {
		currentLogger.Debug("No application resources (io.Closer) were registered for graceful shutdown.")
	}
	r.closersMux.Unlock()

	currentLogger.Debug("Xylium application resource closure process finished.")
}

// commonGracefulShutdownLogic encapsulates the shared logic for graceful shutdown
// across different server types (HTTP, HTTPS with files, HTTPS with embedded certs).
// It handles OS signal listening, fasthttp server shutdown with timeout, and
// closing of Xylium application resources.
//
// Parameters:
//   - server: The `*fasthttp.Server` instance to manage.
//   - startServerFunc: A function that, when called, starts the fasthttp server
//     (e.g., `server.ListenAndServe(addr)`). It should be blocking and return an error
//     if server startup or operation fails.
//
// Returns:
//   - An error if the server failed to start or if a critical error occurred during shutdown.
//   - Nil if shutdown was initiated successfully (gracefully or timed out).
func (r *Router) commonGracefulShutdownLogic(server *fasthttp.Server, startServerFunc func() error) error {
	currentLogger := r.Logger()
	// Note: Route printing and "listening gracefully" messages are now handled by the calling ListenAndServe*Gracefully methods.

	serverErrors := make(chan error, 1) // Channel for server start/run errors.

	// Goroutine to run the fasthttp server via startServerFunc.
	go func() {
		if err := startServerFunc(); err != nil {
			// Send error only if it's not due to normal fasthttp.Shutdown() behavior,
			// which typically returns nil or ErrServerClosed (which fasthttp handles internally).
			// If ListenAndServe returns an actual error (e.g., address in use), send it.
			serverErrors <- err
		}
		// If startServerFunc returns nil (e.g., after successful Shutdown), this goroutine exits.
	}()

	// Channel to listen for OS shutdown signals (Ctrl+C, termination requests).
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Block and wait for either a server error or a shutdown signal.
	select {
	case err := <-serverErrors:
		// Server failed to start or encountered a runtime error.
		if err != nil { // Only log if there was an actual error from startServerFunc.
			currentLogger.Errorf("Server failed to start or encountered an error: %v", err)
		}
		r.closeApplicationResources() // Attempt to close Xylium resources even on server start error.
		return err                    // Propagate the error.

	case sig := <-shutdownChan:
		// Shutdown signal received.
		currentLogger.Infof("Shutdown signal '%s' received. Initiating graceful shutdown of Xylium application...", sig)

		// Determine the application-level shutdown timeout.
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 { // Ensure a positive timeout.
			shutdownTimeout = 15 * time.Second // Fallback to a sensible default.
			currentLogger.Warnf("ServerConfig.ShutdownTimeout is not configured or invalid (<=0). Using default: %s for application shutdown.", shutdownTimeout)
		}

		// Perform fasthttp server shutdown. This call is blocking.
		// Run in a goroutine to allow for an application-level timeout on the shutdown process itself.
		shutdownComplete := make(chan struct{})
		go func() {
			defer close(shutdownComplete) // Signal completion.
			currentLogger.Debugf("Attempting to gracefully shut down fasthttp server (timeout: %v)...", shutdownTimeout)
			if err := server.Shutdown(); err != nil {
				// fasthttp's internal logger (our adapter) should log details of its shutdown errors.
				// We add a Xylium-level log for visibility.
				currentLogger.Errorf("Error reported by fasthttp server.Shutdown(): %v", err)
			}
		}()

		// Wait for fasthttp shutdown to complete or for our app-level timeout.
		select {
		case <-shutdownComplete:
			currentLogger.Info("fasthttp server has gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("Graceful shutdown of fasthttp server timed out after %s. The server might not have fully released all resources.", shutdownTimeout)
			// If fasthttp's Shutdown times out, it means some connections might still be active
			// or its internal cleanup didn't finish within our app's overall timeout.
		}

		r.closeApplicationResources() // Close Xylium's internal and application-registered resources.
		currentLogger.Info("Xylium application shutdown process complete.")
		return nil // Indicates a shutdown (graceful or timed out) was initiated successfully.
	}
}

// ListenAndServeGracefully starts an HTTP server on the given network address `addr`
// with graceful shutdown capabilities. It handles OS signals (SIGINT, SIGTERM)
// to initiate a graceful shutdown, allowing active requests to complete and
// registered Xylium application resources to be closed.
// This is the recommended way to start an HTTP server in production.
func (r *Router) ListenAndServeGracefully(addr string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer() // Build the server instance once for this start attempt.

	// Define the function that will actually start the fasthttp server.
	startFn := func() error {
		currentLogger.Infof("Xylium HTTP server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		return server.ListenAndServe(addr)
	}
	// Delegate to the common graceful shutdown logic.
	return r.commonGracefulShutdownLogic(server, startFn)
}

// ListenAndServeTLSGracefully starts an HTTPS server using certificate files (`certFile`, `keyFile`)
// with graceful shutdown capabilities.
// It handles OS signals for graceful termination and resource cleanup.
func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()

	startFn := func() error {
		currentLogger.Infof("Xylium HTTPS server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		return server.ListenAndServeTLS(addr, certFile, keyFile)
	}
	return r.commonGracefulShutdownLogic(server, startFn)
}

// ListenAndServeTLSEmbedGracefully starts an HTTPS server with embedded certificate (`certData`)
// and key (`keyData`) byte slices, along with graceful shutdown capabilities.
// It handles OS signals for graceful termination and resource cleanup.
func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbedGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()

	startFn := func() error {
		currentLogger.Infof("Xylium HTTPS server (embedded certs) listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		return server.ListenAndServeTLSEmbed(addr, certData, keyData)
	}
	return r.commonGracefulShutdownLogic(server, startFn)
}

// Start is a convenience alias for `ListenAndServeGracefully`.
// It starts an HTTP server on the given network address `addr` and handles
// OS signals (SIGINT, SIGTERM) for a graceful shutdown, ensuring active requests
// can complete and registered resources are closed.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
