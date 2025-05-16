package xylium

import (
	"log" // For default logger implementation
	"net"
	"os"
	"time"

	"github.com/valyala/fasthttp"
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
	CloseOnShutdown               bool
	StreamRequestBody             bool
	Logger                        Logger // Uses xylium.Logger interface
	ConnState                     func(conn net.Conn, state fasthttp.ConnState)
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() ServerConfig {
	// stdlib log.Logger satisfies the xylium.Logger interface.
	defaultAppLogger := log.New(os.Stderr, "[xyliumSrvDefault] ", log.LstdFlags)
	return ServerConfig{
		Name:                 "xylium Server",
		ReadTimeout:          60 * time.Second,
		WriteTimeout:         60 * time.Second,
		IdleTimeout:          120 * time.Second,
		MaxRequestBodySize:   4 * 1024 * 1024, // 4MB
		Concurrency:          fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:    false,
		Logger:               defaultAppLogger, // Provide default xylium.Logger
		CloseOnShutdown:      true,
	}
}

// loggerAdapter adapts a xylium.Logger (which only requires Printf)
// to fasthttp.Logger interface (which is identical for Printf).
// This is mostly for type compatibility if a user provides a custom Logger
// that isn't already a fasthttp.Logger.
type loggerAdapter struct {
	internalLogger Logger
}

func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	la.internalLogger.Printf(format, args...)
}

// buildFasthttpServer creates a fasthttp.Server instance from the Router's configuration.
func (r *Router) buildFasthttpServer() *fasthttp.Server {
	var fasthttpCompatibleLogger fasthttp.Logger
	if r.serverConfig.Logger != nil {
		if fhl, ok := r.serverConfig.Logger.(fasthttp.Logger); ok {
			// If the provided logger is already a fasthttp.Logger, use it directly.
			fasthttpCompatibleLogger = fhl
		} else {
			// Otherwise, wrap it with our adapter.
			fasthttpCompatibleLogger = &loggerAdapter{internalLogger: r.serverConfig.Logger}
		}
	}

	// All fasthttp.Server settings are populated from r.serverConfig
	return &fasthttp.Server{
		Handler:                       r.Handler,
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
		Logger:                        fasthttpCompatibleLogger, // Use the (potentially adapted) logger
		ConnState:                     r.serverConfig.ConnState,
	}
}

// ListenAndServe starts the HTTP server on the given address.
func (r *Router) ListenAndServe(addr string) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium server listening on %s", addr) // r.Logger() is xylium.Logger
	return server.ListenAndServe(addr)
}

// ListenAndServeTLS starts the HTTPS server with certificate and key files.
func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium HTTPS server listening on %s", addr)
	return server.ListenAndServeTLS(addr, certFile, keyFile)
}

// ListenAndServeTLSEmbed starts the HTTPS server with embedded certificate and key data.
func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium HTTPS server (embedded cert) listening on %s", addr)
	return server.ListenAndServeTLSEmbed(addr, certData, keyData)
}
