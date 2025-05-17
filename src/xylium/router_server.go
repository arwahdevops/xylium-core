package xylium

import (
	// "context" // PERBAIKAN: Impor ini tidak digunakan
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
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
	Logger                        Logger
	ConnState                     func(conn net.Conn, state fasthttp.ConnState)
	ShutdownTimeout               time.Duration
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() ServerConfig {
	defaultAppLogger := log.New(os.Stderr, "[xyliumSrvDefault] ", log.LstdFlags)
	return ServerConfig{
		Name:                 "xylium Server",
		ReadTimeout:          60 * time.Second,
		WriteTimeout:         60 * time.Second,
		IdleTimeout:          120 * time.Second,
		MaxRequestBodySize:   4 * 1024 * 1024, // 4MB
		Concurrency:          fasthttp.DefaultConcurrency,
		ReduceMemoryUsage:    false,
		Logger:               defaultAppLogger,
		CloseOnShutdown:      true,
		ShutdownTimeout:      15 * time.Second,
	}
}

type loggerAdapter struct {
	internalLogger Logger
}

func (la *loggerAdapter) Printf(format string, args ...interface{}) {
	la.internalLogger.Printf(format, args...)
}

func (r *Router) buildFasthttpServer() *fasthttp.Server {
	var fasthttpCompatibleLogger fasthttp.Logger
	if r.serverConfig.Logger != nil {
		if fhl, ok := r.serverConfig.Logger.(fasthttp.Logger); ok {
			fasthttpCompatibleLogger = fhl
		} else {
			fasthttpCompatibleLogger = &loggerAdapter{internalLogger: r.serverConfig.Logger}
		}
	}

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
		Logger:                        fasthttpCompatibleLogger,
		ConnState:                     r.serverConfig.ConnState,
	}
}

func (r *Router) ListenAndServe(addr string) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium server listening on %s", addr)
	return server.ListenAndServe(addr)
}

func (r *Router) ListenAndServeTLS(addr, certFile, keyFile string) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium HTTPS server listening on %s", addr)
	return server.ListenAndServeTLS(addr, certFile, keyFile)
}

func (r *Router) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	server := r.buildFasthttpServer()
	r.Logger().Printf("xylium HTTPS server (embedded cert) listening on %s", addr)
	return server.ListenAndServeTLSEmbed(addr, certData, keyData)
}

func (r *Router) ListenAndServeGracefully(addr string) error {
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		r.Logger().Printf("xylium server listening gracefully on %s", addr)
		serverErrors <- server.ListenAndServe(addr)
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

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
			// Tidak ada server.ForceShutdown() di fasthttp.
			// server.Shutdown() yang dipanggil di goroutine akan terus berjalan atau selesai.
			// Program akan exit setelah ini.
		}
		return nil
	}
}

func (r *Router) ListenAndServeTLSGracefully(addr, certFile, keyFile string) error {
	server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		r.Logger().Printf("xylium HTTPS server listening gracefully on %s", addr)
		serverErrors <- server.ListenAndServeTLS(addr, certFile, keyFile)
	}()
	
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

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

func (r *Router) ListenAndServeTLSEmbedGracefully(addr string, certData, keyData []byte) error {
    server := r.buildFasthttpServer()
	serverErrors := make(chan error, 1)

	go func() {
		r.Logger().Printf("xylium HTTPS server (embedded cert) listening gracefully on %s", addr)
		serverErrors <- server.ListenAndServeTLSEmbed(addr, certData, keyData)
	}()
	
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

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
