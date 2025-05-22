// File: src/xylium/middleware_compress.go
package xylium

import (
	"strconv" // For converting Content-Length int to string.
	"strings" // For string manipulation (content type parsing).

	"github.com/valyala/fasthttp" // For fasthttp constants and Gzip compression.
)

// GzipConfig defines the configuration for the Gzip compression middleware.
// This middleware compresses response bodies to reduce transfer size, which can
// improve performance for clients with limited bandwidth.
type GzipConfig struct {
	// Level is the Gzip compression level to use.
	// Higher levels offer better compression ratios but consume more CPU.
	// Options are defined in `github.com/valyala/fasthttp`:
	//  - `fasthttp.CompressNoCompression` (0)
	//  - `fasthttp.CompressBestSpeed` (1)
	//  - `fasthttp.CompressBestCompression` (9)
	//  - `fasthttp.CompressDefaultCompression` (-1, typically equivalent to level 6)
	//  - `fasthttp.CompressHuffmanOnly` (-2)
	// Default: `fasthttp.CompressDefaultCompression` if not specified or set to 0 (as 0 means NoCompression).
	Level int

	// MinLength is the minimum response body length (in bytes) required to trigger compression.
	// Responses smaller than this will not be compressed, as the overhead of compression
	// might outweigh the benefits for very small payloads.
	// Default: 0 (compress all eligible responses regardless of size, if not specified).
	// A common practical value might be 1024 bytes (1KB).
	MinLength int

	// ContentTypes is a list of MIME types that should be considered for compression.
	// If this list is empty, a default list of common compressible types will be used
	// (see `defaultCompressContentTypes`).
	// Comparison is case-insensitive and considers the base MIME type (e.g., "text/html"
	// from "text/html; charset=utf-8").
	// Example: `[]string{"application/json", "text/html; charset=utf-8"}`
	ContentTypes []string
}

// defaultCompressContentTypes is a list of common MIME types that are typically
// good candidates for Gzip compression. Text-based formats like HTML, CSS, JS, JSON,
// and XML benefit significantly from compression.
var defaultCompressContentTypes = []string{
	"text/plain", "text/html", "text/css", "text/xml", "text/javascript",
	"application/json", "application/xml", "application/javascript", "application/x-javascript",
	"application/rss+xml", "application/atom+xml", "image/svg+xml",
}

// Gzip returns a Gzip compression middleware with default configuration.
// It uses GzipConfig{} which will then be populated with defaults by GzipWithConfig.
func Gzip() Middleware {
	return GzipWithConfig(GzipConfig{}) // Pass empty struct to use defaults in GzipWithConfig
}

// GzipWithConfig returns a Gzip compression middleware with the provided custom configuration.
// It handles checking client capabilities, response eligibility, performing compression,
// and setting appropriate response headers.
func GzipWithConfig(config GzipConfig) Middleware {
	// Apply default compression level if not specified or if set to NoCompression (0)
	// by the user but they intended to use defaults.
	if config.Level == fasthttp.CompressNoCompression {
		config.Level = fasthttp.CompressDefaultCompression
	}
	// Note: MinLength defaults to 0 (compress all sizes) if not set, which is acceptable.

	compressibleTypes := make(map[string]struct{})
	typesToUse := config.ContentTypes
	if len(typesToUse) == 0 {
		typesToUse = defaultCompressContentTypes
	}
	for _, t := range typesToUse {
		normalizedType := strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
		if normalizedType != "" {
			compressibleTypes[normalizedType] = struct{}{}
		}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "Gzip"})

			acceptEncoding := c.Header("Accept-Encoding")
			if !strings.Contains(acceptEncoding, "gzip") {
				logger.Debugf("Client does not accept gzip encoding ('%s'). Skipping compression for %s %s.",
					acceptEncoding, c.Method(), c.Path())
				return next(c)
			}

			err := next(c) // Call next handler first to get the response prepared
			if err != nil {
				logger.Debugf("Error occurred in handler chain. Skipping compression for %s %s.", c.Method(), c.Path())
				return err
			}

			// Kondisi untuk skip kompresi setelah handler dijalankan:
			// (Cek ResponseCommitted() DIHAPUS dari sini karena Gzip ingin memodifikasi body yang sudah diset handler)

			if c.Ctx.Response.StatusCode() >= StatusBadRequest {
				logger.Debugf("Response status code %d is >= 400. Skipping compression for %s %s.",
					c.Ctx.Response.StatusCode(), c.Method(), c.Path())
				return nil // Jangan return error, biarkan response error asli yang dikirim
			}
			if len(c.Ctx.Response.Header.Peek("Content-Encoding")) > 0 {
				logger.Debugf("'Content-Encoding' header already set to '%s'. Skipping compression for %s %s.",
					string(c.Ctx.Response.Header.Peek("Content-Encoding")), c.Method(), c.Path())
				return nil
			}

			responseBody := c.Ctx.Response.Body()
			if len(responseBody) == 0 {
				logger.Debugf("Response body is empty. Skipping compression for %s %s.", c.Method(), c.Path())
				return nil
			}

			if config.MinLength > 0 && len(responseBody) < config.MinLength {
				logger.Debugf("Response body length %d bytes is less than MinLength %d bytes. Skipping compression for %s %s.",
					len(responseBody), config.MinLength, c.Method(), c.Path())
				return nil
			}

			contentType := string(c.Ctx.Response.Header.ContentType())
			normalizedContentType := strings.ToLower(strings.Split(contentType, ";")[0])
			if _, typeIsCompressible := compressibleTypes[normalizedContentType]; !typeIsCompressible {
				logger.Debugf("Content-Type '%s' (normalized: '%s') is not in the compressible types list. Skipping compression for %s %s.",
					contentType, normalizedContentType, c.Method(), c.Path())
				return nil
			}

			// Lakukan kompresi
			logger.Debugf("Compressing response for %s %s (Content-Type: %s, Original Size: %d bytes, Level: %d).",
				c.Method(), c.Path(), contentType, len(responseBody), config.Level)

			compressedBody := fasthttp.AppendGzipBytesLevel(nil, responseBody, config.Level)

			// Set body dan header baru
			c.Ctx.Response.SetBodyRaw(compressedBody)
			c.SetHeader("Content-Encoding", "gzip")
			// Content-Length harus diupdate ke ukuran body yang terkompresi
			c.SetHeader("Content-Length", strconv.Itoa(len(compressedBody)))
			// Tambahkan Vary header
			c.Ctx.Response.Header.Add("Vary", "Accept-Encoding")

			logger.Debugf("Compression successful for %s %s. New size: %d bytes.",
				c.Method(), c.Path(), len(compressedBody))

			return nil // Sukses, tidak ada error dari middleware Gzip itu sendiri
		}
	}
}
