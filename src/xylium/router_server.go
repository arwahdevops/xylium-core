package xylium

import (
	"log"      // Used by fasthttp as a fallback if its logger is nil; not directly by Xylium's core logging.
	"net"      // For net.Conn, fasthttp.ConnState
	"os"       // For os.Signal, os.Stderr (though os.Stderr not directly used by Xylium logger by default)
	"os/signal" // For graceful shutdown signal handling
	"syscall"  // For syscall.SIGINT, syscall.SIGTERM
	"time"     // For timeouts

	"github.com/valyala/fasthttp" // The underlying HTTP server
)

// ServerConfig holds configuration options for the fasthttp server
// that Xylium uses.
type ServerConfig struct {
	// Name is the server name, sent in the "Server" header if NoDefaultServerHeader is false.
	Name string
	// ReadTimeout is the maximum duration for reading the entire request, including the body.
	ReadTimeout time.Duration
	// WriteTimeout is the maximum duration before timing out writes of the response.
	WriteTimeout time.Duration
	// IdleTimeout is the maximum amount of time to wait for the next request when keep-alives are enabled.
	IdleTimeout time.Duration
	// MaxRequestBodySize is the maximum request body size.
	MaxRequestBodySize int
	// ReduceMemoryUsage decreases memory usage at the cost of higher CPU usage.
	ReduceMemoryUsage bool
	// Concurrency is the maximum number of concurrent connections the server may serve.
	Concurrency int
	// DisableKeepalive, if true, disables HTTP keep-alive connections.
	DisableKeepalive bool
	// TCPKeepalive enables TCP keep-alive messages on accepted connections.
	TCPKeepalive bool
	// TCPKeepalivePeriod is the period between TCP keep-alive messages.
	TCPKeepalivePeriod time.Duration
	// MaxConnsPerIP is the maximum number of concurrent connections allowed per IP address.
	MaxConnsPerIP int
	// MaxRequestsPerConn is the maximum number of requests served per connection.
	MaxRequestsPerConn int
	// GetOnly, if true, causes the server to handle only GET requests.
	GetOnly bool
	// DisableHeaderNamesNormalizing, if true, disables normalization of response header names.
	DisableHeaderNamesNormalizing bool
	// NoDefaultServerHeader, if true, will not set the "Server" header.
	NoDefaultServerHeader bool
	// NoDefaultDate, if true, will not set the "Date" header.
	NoDefaultDate bool
	// NoDefaultContentType, if true, will not set the "Content-Type" header.
	NoDefaultContentType bool
	// KeepHijackedConns, if true, will keep TCP connections alive after hijacking.
	KeepHijackedConns bool
	// CloseOnShutdown, if true, will close all open connections during a graceful shutdown.
	CloseOnShutdown bool
	// StreamRequestBody enables request body streaming.
	StreamRequestBody bool
	// Logger is the logger for server errors and informational messages.
	// It must implement the xylium.Logger interface.
	Logger Logger // Uses the xylium.Logger interface.
	// ConnState specifies an optional callback function that is called when a
	// connection's state changes.
	ConnState func(conn net.Conn, state fasthttp.ConnState)
	// ShutdownTimeout is the maximum duration to wait for active connections to finish
	// during a graceful shutdown.
	ShutdownTimeout time.Duration
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
// The Logger is initialized with a new Xylium DefaultLogger.
// Mode-specific logger configurations (level, color, etc.) are applied
// later in router.NewWithConfig().
func DefaultServerConfig() ServerConfig {
	// Initialize with Xylium's DefaultLogger.
	// It will have its own defaults (e.g., LevelInfo, TextFormatter).
	xyliumLogger := NewDefaultLogger()

	return ServerConfig{
		Name:                 "Xylium Server", // Default server name.
		ReadTimeout:          60 * time.Second,
		WriteTimeout:         60 * time.Second,
		IdleTimeout:          120 * time.Second,
		MaxRequestBodySize:   4 * 1024 * 1024, // 4MB default.
		Concurrency:          fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:    false,
		Logger:               xyliumLogger, // Assign the new Xylium DefaultLogger.
		CloseOnShutdown:      true,         // Close connections on shutdown by default.
		ShutdownTimeout:      15 * time.Second, // Default time to wait for graceful shutdown.
		// Other fields default to their zero values, which fasthttp handles appropriately.
	}
}

// loggerAdapter adapts a xylium.Logger to the fasthttp.Logger interface,
// which only requires a Printf method.
type loggerAdapter struct {
	internalLogger Logger // Holds the xylium.Logger instance. Assumed non-nil.
}

// Printf implements the fasthttp.Logger interface by forwarding messages
// to the internal Xylium logger's Infof method.
// This treats internal fasthttp server messages as informational.
func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	// internalLogger is expected to be non-nil due to router.NewWithConfig() guarantees.
	// The 'if la.internalLogger != nil' check is highly defensive.
	if la.internalLogger != nil {
		la.internalLogger.Infof(format, args...) // Log fasthttp internal messages as INFO.
	} else {
		// This fallback should ideally never be reached in a correctly initialized Xylium app.
		log.Printf("[XYLIUM-LOGGER-ADAPTER-FALLBACK] "+format, args...)
	}
}

// buildFasthttpServer constructs a new fasthttp.Server instance based on the
// Router's ServerConfig. It ensures a compatible logger is passed to fasthttp.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	// r.serverConfig.Logger is guaranteed to be non-nil by router.NewWithConfig().
	// We always use the adapter to bridge xylium.Logger to fasthttp.Logger.
	fasthttpCompatibleLogger := &loggerAdapter{internalLogger: r.serverConfig.Logger}

	// In DebugMode, log key server configurations being applied.
	if r.CurrentMode() == DebugMode {
		cfgLog := r.Logger().WithFields(M{"component": "fasthttp-server-builder"})
		cfgLog.Debugf("Building fasthttp.Server with Name: '%s'", r.serverConfig.Name)
		cfgLog.Debugf("ReadTimeout: %v, WriteTimeout: %v, IdleTimeout: %v", r.serverConfig.ReadTimeout, r.serverConfig.WriteTimeout, r.serverConfig.IdleTimeout)
		cfgLog.Debugf("MaxRequestBodySize: %d, Concurrency: %d", r.serverConfig.MaxRequestBodySize, r.serverConfig.Concurrency)
		cfgLog.Debugf("CloseOnShutdown: %t, ShutdownTimeout: %v", r.serverConfig.CloseOnShutdown, r.serverConfig.ShutdownTimeout)
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
		Logger:                        fasthttpCompatibleLogger, // Pass the adapted logger to fasthttp.
		ConnState:                     r.serverConfig.ConnState,
	}
}

// ListenAndServe starts an HTTP server on the given network address.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServe(addr string) error {
	// r.Logger() will return the application-configured (and potentially mode-adjusted) logger.
	currentLogger := r.Logger()
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServe on %s:", addr)
		r.tree.PrintRoutes(currentLogger) // PrintRoutes expects a xylium.Logger.
	}
	server := r.buildFasthttpServer()
	currentLogger.Infof("Xylium server listening on %s (Mode: %s)", addr, r.CurrentMode())
	return server.ListenAndServe(addr)
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
	return server.ListenAndServeTLS(addr, certFile, keyFile)
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
	return server.ListenAndServeTLSEmbed(addr, certData, keyData)
}

// ListenAndServeGracefully starts an HTTP server with graceful shutdown capabilities.
func (r *Router) ListenAndServeGracefully(addr string) error {
	currentLogger := r.Logger() // Get the configured logger for this router.
	if r.CurrentMode() == DebugMode && r.tree != nil {
		currentLogger.Debugf("Printing registered routes for ListenAndServeGracefully on %s:", addr)
		r.tree.PrintRoutes(currentLogger)
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1) // Channel to receive errors from server.ListenAndServe.

	go func() {
		currentLogger.Infof("Xylium server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		if err := server.ListenAndServe(addr); err != nil {
			// Send error to channel only if it's not due to expected shutdown (though fasthttp usually returns nil on graceful Shutdown)
			// For robust error reporting from ListenAndServe, send any non-nil error.
			serverErrors <- err
		}
		// If ListenAndServe returns nil (e.g. after successful Shutdown), no error to send.
		// Consider closing serverErrors if ListenAndServe always exits on shutdown.
		// However, `select` will handle one error or signal.
	}()

	// Channel to listen for OS shutdown signals.
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for either a server error or a shutdown signal.
	select {
	case err := <-serverErrors:
		if err != nil { // Only log if there was an actual error.
			currentLogger.Errorf("Server failed to start or encountered an error: %v", err)
		}
		return err // Propagate the error.
	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' received. Starting graceful shutdown...", sig)

		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			// If not configured or invalid, use a sensible default.
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("ServerConfig.ShutdownTimeout is not configured or invalid (<=0). Using default: %s", shutdownTimeout)
		}

		// Perform server shutdown in a separate goroutine to allow timeout.
		done := make(chan struct{})
		go func() {
			defer close(done) // Signal completion.
			if err := server.Shutdown(); err != nil {
				currentLogger.Errorf("Error during server graceful shutdown: %v", err)
			}
		}()

		// Wait for shutdown to complete or timeout.
		select {
		case <-done:
			currentLogger.Info("Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("Graceful shutdown timed out after %s. Forcing server to stop.", shutdownTimeout)
			// fasthttp's Shutdown() blocks until completion or internal timeout.
			// If we reach here, it means Shutdown() itself might have hung longer than our app-level timeout.
			// The server might still be shutting down or might need a more forceful stop if Shutdown() doesn't respect its own internal limits.
			// For now, we just log the timeout.
		}
		return nil // Indicates a shutdown (graceful or timed out) was initiated.
	}
}

// ListenAndServeTLSGracefully starts an HTTPS server with graceful shutdown.
func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
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
			currentLogger.Errorf("HTTPS Server failed to start or encountered an error: %v", err)
		}
		return err
	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' received for HTTPS server. Starting graceful shutdown...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("ServerConfig.ShutdownTimeout not configured. Using default for HTTPS: %s", shutdownTimeout)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := server.Shutdown(); err != nil {
				currentLogger.Errorf("Error during HTTPS server graceful shutdown: %v", err)
			}
		}()
		select {
		case <-done:
			currentLogger.Info("HTTPS Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("HTTPS Server graceful shutdown timed out after %s.", shutdownTimeout)
		}
		return nil
	}
}

// ListenAndServeTLSEmbedGracefully starts an HTTPS server with embedded certs and graceful shutdown.
func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
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
			currentLogger.Errorf("HTTPS Server (embedded cert) failed to start or encountered an error: %v", err)
		}
		return err
	case sig := <-shutdownChan:
		currentLogger.Infof("Shutdown signal '%s' received for HTTPS server (embedded cert). Starting graceful shutdown...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			currentLogger.Warnf("ServerConfig.ShutdownTimeout not configured. Using default for embedded HTTPS: %s", shutdownTimeout)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := server.Shutdown(); err != nil {
				currentLogger.Errorf("Error during embedded HTTPS server graceful shutdown: %v", err)
			}
		}()
		select {
		case <-done:
			currentLogger.Info("HTTPS Server (embedded cert) gracefully stopped.")
		case <-time.After(shutdownTimeout):
			currentLogger.Warnf("Embedded HTTPS server graceful shutdown timed out after %s.", shutdownTimeout)
		}
		return nil
	}
}


// Start is a convenience alias for ListenAndServeGracefully.
// It starts an HTTP server on the given network address and handles
// OS signals (SIGINT, SIGTERM) for a graceful shutdown.
// Logging and route printing are handled by ListenAndServeGracefully.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
