// src/xylium/router_server.go
package xylium

import (
	"log"      // For default logger and fallback in loggerAdapter
	"net"      // For net.Conn in ConnState
	"os"       // For os.Signal and os.Stderr
	"os/signal" // For graceful shutdown
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
	Logger Logger
	// ConnState specifies an optional callback function that is called when a
	// connection's state changes.
	ConnState func(conn net.Conn, state fasthttp.ConnState)
	// ShutdownTimeout is the maximum duration to wait for active connections to finish
	// during a graceful shutdown.
	ShutdownTimeout time.Duration
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() ServerConfig {
	// Create a default logger instance using Go's standard log package.
	defaultAppLogger := log.New(os.Stderr, "[xyliumSrvDefault] ", log.LstdFlags)
	return ServerConfig{
		Name:                 "Xylium Server", // Default server name.
		ReadTimeout:          60 * time.Second,
		WriteTimeout:         60 * time.Second,
		IdleTimeout:          120 * time.Second,
		MaxRequestBodySize:   4 * 1024 * 1024, // 4MB default.
		Concurrency:          fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:    false,
		Logger:               defaultAppLogger, // Use the default xylium.Logger.
		CloseOnShutdown:      true,
		ShutdownTimeout:      15 * time.Second, // Default graceful shutdown timeout.
	}
}

// loggerAdapter adapts a xylium.Logger to fasthttp.Logger interface.
// fasthttp.Logger has the same Printf method signature as xylium.Logger.
type loggerAdapter struct {
	internalLogger Logger // The xylium.Logger to adapt.
}

// Printf implements the fasthttp.Logger interface.
func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	if la.internalLogger != nil {
		la.internalLogger.Printf(format, args...)
	} else {
		// Fallback if internalLogger is somehow nil.
		log.Printf(format, args...)
	}
}

// buildFasthttpServer constructs a new fasthttp.Server instance based on the
// Router's ServerConfig.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	var fasthttpCompatibleLogger fasthttp.Logger // fasthttp.Server expects a fasthttp.Logger.

	if r.serverConfig.Logger != nil {
		// If the provided xylium.Logger already implements fasthttp.Logger, use it directly.
		// This is possible if the user provides a fasthttp.Logger instance.
		if fhl, ok := r.serverConfig.Logger.(fasthttp.Logger); ok {
			fasthttpCompatibleLogger = fhl
		} else {
			// Otherwise, wrap the xylium.Logger with the adapter.
			fasthttpCompatibleLogger = &loggerAdapter{internalLogger: r.serverConfig.Logger}
		}
	} else {
		// Fallback if no logger is configured at all (should be handled by DefaultServerConfig).
		fasthttpCompatibleLogger = &loggerAdapter{internalLogger: log.New(os.Stderr, "[xyliumFasthttpFallback] ", log.LstdFlags)}
	}

	return &fasthttp.Server{
		Handler:                       r.Handler, // The main Xylium request handler.
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
		Logger:                        fasthttpCompatibleLogger, // Use the adapted or direct fasthttp.Logger.
		ConnState:                     r.serverConfig.ConnState,
	}
}

// ListenAndServe starts an HTTP server on the given network address.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServe(addr string) error {
	if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger()) // Log routes if in DebugMode.
	}
	server := r.buildFasthttpServer()
	r.Logger().Printf("Xylium server listening on %s (Mode: %s)", addr, r.CurrentMode())
	return server.ListenAndServe(addr)
}

// ListenAndServeTLS starts an HTTPS server on the given network address using
// the provided certificate and key files.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger())
	}
	server := r.buildFasthttpServer()
	r.Logger().Printf("Xylium HTTPS server listening on %s (Mode: %s)", addr, r.CurrentMode())
	return server.ListenAndServeTLS(addr, certFile, keyFile)
}

// ListenAndServeTLSEmbed starts an HTTPS server on the given network address using
// embedded certificate and key data.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger())
	}
	server := r.buildFasthttpServer()
	r.Logger().Printf("Xylium HTTPS server (embedded cert) listening on %s (Mode: %s)", addr, r.CurrentMode())
	return server.ListenAndServeTLSEmbed(addr, certData, keyData)
}

// ListenAndServeGracefully starts an HTTP server on the given network address
// and handles OS signals (SIGINT, SIGTERM) for a graceful shutdown.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServeGracefully(addr string) error {
	if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger())
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1) // Channel to capture errors from ListenAndServe.

	// Start the server in a goroutine so it doesn't block.
	go func() {
		r.Logger().Printf("Xylium server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		serverErrors <- server.ListenAndServe(addr)
	}()

	// Channel to listen for OS shutdown signals.
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Block until either a server error occurs or a shutdown signal is received.
	select {
	case err := <-serverErrors:
		// If ListenAndServe fails (e.g., address already in use), return the error.
		return err
	case sig := <-shutdownChan:
		r.Logger().Printf("Shutdown signal received: %s. Starting graceful shutdown...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			// Use a default shutdown timeout if not configured or invalid.
			shutdownTimeout = 15 * time.Second
			r.Logger().Printf("Using default shutdown timeout: %s", shutdownTimeout)
		}

		// Perform graceful shutdown in a new goroutine.
		// server.Shutdown() will block until all connections are closed or the timeout is reached.
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := server.Shutdown(); err != nil {
				r.Logger().Printf("Error during server shutdown: %v", err)
			}
		}()

		// Wait for shutdown to complete or timeout.
		select {
		case <-done:
			r.Logger().Printf("Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			r.Logger().Printf("Graceful shutdown timed out after %s.", shutdownTimeout)
			// fasthttp's server.Shutdown() itself handles the timeout.
			// The program will exit after this point if the main goroutine is this one.
		}
		return nil // Indicates the shutdown sequence was initiated.
	}
}

// ListenAndServeTLSGracefully starts an HTTPS server with graceful shutdown.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
	if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger())
	}
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		r.Logger().Printf("Xylium HTTPS server listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		serverErrors <- server.ListenAndServeTLS(addr, certFile, keyFile)
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// The graceful shutdown logic is identical to ListenAndServeGracefully.
	select {
	case err := <-serverErrors:
		return err
	case sig := <-shutdownChan:
		r.Logger().Printf("Shutdown signal received: %s. Starting graceful shutdown...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			r.Logger().Printf("Using default shutdown timeout: %s", shutdownTimeout)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := server.Shutdown(); err != nil {
				r.Logger().Printf("Error during server shutdown: %v", err)
			}
		}()

		select {
		case <-done:
			r.Logger().Printf("Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			r.Logger().Printf("Graceful shutdown timed out after %s.", shutdownTimeout)
		}
		return nil
	}
}


// ListenAndServeTLSEmbedGracefully starts an HTTPS server using embedded certificate/key data
// and handles graceful shutdown.
// It logs registered routes if the router is in DebugMode.
func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
    if r.CurrentMode() == DebugMode && r.tree != nil && r.Logger() != nil {
		r.tree.PrintRoutes(r.Logger())
	}
    server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		r.Logger().Printf("Xylium HTTPS server (embedded cert) listening gracefully on %s (Mode: %s)", addr, r.CurrentMode())
		serverErrors <- server.ListenAndServeTLSEmbed(addr, certData, keyData)
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// The graceful shutdown logic is identical to ListenAndServeGracefully.
	select {
	case err := <-serverErrors:
		return err
	case sig := <-shutdownChan:
		r.Logger().Printf("Shutdown signal received: %s. Starting graceful shutdown...", sig)
		shutdownTimeout := r.serverConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
			r.Logger().Printf("Using default shutdown timeout: %s", shutdownTimeout)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := server.Shutdown(); err != nil {
				r.Logger().Printf("Error during server shutdown: %v", err)
			}
		}()

		select {
		case <-done:
			r.Logger().Printf("Server gracefully stopped.")
		case <-time.After(shutdownTimeout):
			r.Logger().Printf("Graceful shutdown timed out after %s.", shutdownTimeout)
		}
		return nil
	}
}
