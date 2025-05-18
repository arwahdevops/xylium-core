package xylium

import (
	"log"       // Used by fasthttp as a fallback if its logger is nil.
	"net"       // For net.Conn, fasthttp.ConnState.
	"os"        // For os.Signal.
	"os/signal" // For graceful shutdown signal handling.
	"syscall"   // For syscall.SIGINT, syscall.SIGTERM.
	"time"      // For timeouts.

	"github.com/valyala/fasthttp" // The underlying HTTP server.
)

// ServerConfig holds configuration options for the fasthttp server.
type ServerConfig struct {
	Name                          string
	ReadTimeout                   time.Duration
	WriteTimeout                  time.Duration
	IdleTimeout                   time.Duration
	MaxRequestBodySize            int
	ReduceMemoryUsage             bool
	Concurrency                   int
	DisableKeepalive              bool
	TCPKeepalive                  bool
	TCPKeepalivePeriod            time.Duration
	MaxConnsPerIP                 int
	MaxRequestsPerConn            int
	GetOnly                       bool
	DisableHeaderNamesNormalizing bool
	NoDefaultServerHeader         bool
	NoDefaultDate                 bool
	NoDefaultContentType          bool
	KeepHijackedConns             bool
	CloseOnShutdown               bool // fasthttp's option to close connections on shutdown.
	StreamRequestBody             bool
	Logger                        Logger        // Xylium logger.
	LoggerConfig                  *LoggerConfig // Detailed config for DefaultLogger if used.
	ConnState                     func(conn net.Conn, state fasthttp.ConnState)
	ShutdownTimeout               time.Duration // Xylium's application-level graceful shutdown timeout.
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() ServerConfig {
	defaultLogCfg := DefaultLoggerConfig() // Get default logger configuration.
	return ServerConfig{
		Name:               "Xylium Server",
		ReadTimeout:        60 * time.Second,
		WriteTimeout:       60 * time.Second,
		IdleTimeout:        120 * time.Second,
		MaxRequestBodySize: 4 * 1024 * 1024, // 4MB
		Concurrency:        fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:  false,
		Logger:             nil,              // Will be initialized in router.NewWithConfig if nil.
		LoggerConfig:       &defaultLogCfg,   // Provide default logger config.
		CloseOnShutdown:    true,             // Default fasthttp behavior.
		ShutdownTimeout:    15 * time.Second, // Xylium's graceful shutdown timeout.
	}
}

// loggerAdapter adapts a xylium.Logger to the fasthttp.Logger interface.
type loggerAdapter struct {
	internalLogger Logger // Holds the xylium.Logger instance.
}

// Printf implements the fasthttp.Logger interface by forwarding messages
// to the internal Xylium logger's Infof method.
func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	if la.internalLogger != nil {
		la.internalLogger.Infof(format, args...) // Log fasthttp internal messages as INFO.
	} else {
		// Fallback, should ideally not be reached in a correctly initialized Xylium app.
		log.Printf("[XYLIUM-LOGGER-ADAPTER-FALLBACK] "+format, args...)
	}
}

// buildFasthttpServer constructs a new fasthttp.Server instance based on the
// Router's ServerConfig.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	// r.serverConfig.Logger is guaranteed to be non-nil by router.NewWithConfig().
	fasthttpCompatibleLogger := &loggerAdapter{internalLogger: r.serverConfig.Logger}

	if r.CurrentMode() == DebugMode {
		cfgLog := r.Logger().WithFields(M{"component": "fasthttp-server-builder"})
		cfgLog.Debugf("Building fasthttp.Server with Name: '%s'", r.serverConfig.Name)
		cfgLog.Debugf("ReadTimeout: %v, WriteTimeout: %v, IdleTimeout: %v", r.serverConfig.ReadTimeout, r.serverConfig.WriteTimeout, r.serverConfig.IdleTimeout)
		cfgLog.Debugf("MaxRequestBodySize: %d, Concurrency: %d", r.serverConfig.MaxRequestBodySize, r.serverConfig.Concurrency)
		cfgLog.Debugf("CloseOnShutdown (fasthttp): %t, ShutdownTimeout (Xylium app-level): %v", r.serverConfig.CloseOnShutdown, r.serverConfig.ShutdownTimeout)
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
		CloseOnShutdown:               r.serverConfig.CloseOnShutdown, // fasthttp's own option.
		StreamRequestBody:             r.serverConfig.StreamRequestBody,
		Logger:                        fasthttpCompatibleLogger, // Pass the adapted Xylium logger.
		ConnState:                     r.serverConfig.ConnState,
	}
}

// ListenAndServe starts an HTTP server on the given network address.
func (r *Router) ListenAndServe(addr string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServe on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium server listening on %s (Mode: %s)", addr, r.CurrentMode())
	err := server.ListenAndServe(addr)
	// Attempt to close resources on ListenAndServe error (e.g. address in use)
	// This might be too early if server never started, but harmless for InMemoryStore.
	r.closeInternalResources()
	return err
}

// ListenAndServeTLS starts an HTTPS server.
func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLS on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server listening on %s (Mode: %s)", addr, r.CurrentMode())
	err := server.ListenAndServeTLS(addr, certFile, keyFile)
	r.closeInternalResources()
	return err
}

// ListenAndServeTLSEmbed starts an HTTPS server with embedded certificates.
func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbed on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium HTTPS server (embedded cert) listening on %s (Mode: %s)", addr, r.CurrentMode())
	err := server.ListenAndServeTLSEmbed(addr, certData, keyData)
	r.closeInternalResources()
	return err
}

// closeInternalResources is a helper function to close all registered internal resources
// managed by the Xylium router, such as internally created rate limiter stores.
func (r *Router) closeInternalResources() {
	currentLogger := r.Logger()

	// Close internal rate limiter stores.
	if len(r.internalRateLimitStores) > 0 {
		currentLogger.Infof("Closing %d internal rate limiter store(s)...", len(r.internalRateLimitStores))
		// Iterate in reverse order in case Close() removes from slice (though it doesn't currently).
		for i := len(r.internalRateLimitStores) - 1; i >= 0; i-- {
			store := r.internalRateLimitStores[i]
			if err := store.Close(); err != nil {
				currentLogger.Errorf("Error closing internal rate limiter store #%d (type %T): %v", i+1, store, err)
			}
		}
		currentLogger.Info("All internal rate limiter store(s) attempted to close.")
		// Clear the slice after attempting to close all.
		r.internalRateLimitStores = make([]LimiterStore, 0)
	} else {
		currentLogger.Debug("No internal rate limiter stores registered to close.")
	}

	// Future: Add logic here to close other types of internal resources if any are added.
}

// ListenAndServeGracefully starts an HTTP server with graceful shutdown capabilities.
// It handles OS signals (SIGINT, SIGTERM) to initiate a graceful shutdown.
func (r *Router) ListenAndServeGracefully(addr string) error {
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1) // Channel for server start/run errors.

	// Goroutine to run the fasthttp server.
	go func() {
		currentLogger.Infof("Xylium server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		if err := server.ListenAndServe(addr); err != nil {
			// Send error only if it's not due to normal shutdown.
			// fasthttp's ListenAndServe usually returns nil on successful Shutdown().
			// If an error occurs (e.g., address already in use), send it.
			serverErrors <- err
		}
		// If ListenAndServe returns nil (e.g., after successful Shutdown), this goroutine exits.
	}()

	// Channel to listen for OS shutdown signals.
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for either a server error or a shutdown signal.
	select {
	case err := <-serverErrors:
		if err != nil { // Only log if there was an actual error from ListenAndServe.
			currentLogger.Errorf("Server failed to start or encountered an error: %v", err)
		}
		r.closeInternalResources() // Attempt to close resources even on server start error.
		return err                 // Propagate the error.

	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' received. Starting graceful shutdown...", sig)

		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 { // Ensure a positive timeout.
			shutdownTimeout = 15 * time.Second // Sensible default.
			currentLogger.Warnf("ServerConfig.ShutdownTimeout is not configured or invalid (<=0). Using default: %s", shutdownTimeout)
		}

		// Perform server shutdown. fasthttp.Shutdown() is blocking.
		// Run in a goroutine to allow for an application-level timeout on the shutdown process itself.
		shutdownComplete := make(chan struct{})
		go func() {
			defer close(shutdownComplete)
			if err := server.Shutdown(); err != nil {
				currentLogger.Errorf("Error during fasthttp server graceful shutdown: %v", err)
			}
		}()

		// Wait for fasthttp shutdown to complete or for our app-level timeout.
		select {
		case <-shutdownComplete:
			currentLogger.Info("fasthttp server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("Graceful shutdown of fasthttp server timed out after %s.", shutdownTimeout)
			// fasthttp's Shutdown() should have its own internal mechanisms.
			// If we reach here, it implies a potential issue or very long-lived connections
			// that fasthttp couldn't terminate within its own limits or our app timeout.
		}

		r.closeInternalResources() // Close Xylium's internal resources after server shutdown attempt.
		currentLogger.Info("Xylium application shutdown process complete.")
		return nil // Indicates a shutdown (graceful or timed out) was initiated.
	}
}

// ListenAndServeTLSGracefully starts an HTTPS server with graceful shutdown.
func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
	// Similar logic to ListenAndServeGracefully, just with ListenAndServeTLS.
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		currentLogger.Infof("Xylium HTTPS server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		if err := server.ListenAndServeTLS(addr, certFile, keyFile); err != nil {
			serverErrors <- err
		}
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if err != nil {
			currentLogger.Errorf("HTTPS Server failed: %v", err)
		}
		r.closeInternalResources()
		return err
	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' received for HTTPS. Shutting down...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("Defaulting HTTPS ShutdownTimeout: %s", shutdownTimeout)
		}

		shutdownComplete := make(chan struct{})
		go func() { defer close(shutdownComplete); server.Shutdown() }() // Simplified: let Shutdown handle its own error logging via fasthttp logger.

		select {
		case <-shutdownComplete:
			currentLogger.Info("HTTPS Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("HTTPS Server shutdown timed out after %s.", shutdownTimeout)
		}
		r.closeInternalResources()
		currentLogger.Info("Xylium HTTPS application shutdown complete.")
		return nil
	}
}

// ListenAndServeTLSEmbedGracefully starts an HTTPS server with embedded certs and graceful shutdown.
func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
	// Similar logic to ListenAndServeGracefully, just with ListenAndServeTLSEmbed.
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeTLSEmbedGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		currentLogger.Infof("Xylium HTTPS server (embedded cert) listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		if err := server.ListenAndServeTLSEmbed(addr, certData, keyData); err != nil {
			serverErrors <- err
		}
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if err != nil {
			currentLogger.Errorf("Embedded HTTPS Server failed: %v", err)
		}
		r.closeInternalResources()
		return err
	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' for embedded HTTPS. Shutting down...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("Defaulting embedded HTTPS ShutdownTimeout: %s", shutdownTimeout)
		}

		shutdownComplete := make(chan struct{})
		go func() { defer close(shutdownComplete); server.Shutdown() }()

		select {
		case <-shutdownComplete:
			currentLogger.Info("Embedded HTTPS Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("Embedded HTTPS Server shutdown timed out after %s.", shutdownTimeout)
		}
		r.closeInternalResources()
		currentLogger.Info("Xylium embedded HTTPS application shutdown complete.")
		return nil
	}
}

// Start is a convenience alias for ListenAndServeGracefully.
// It starts an HTTP server on the given network address and handles
// OS signals (SIGINT, SIGTERM) for a graceful shutdown.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
