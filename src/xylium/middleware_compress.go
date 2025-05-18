// src/xylium/middleware_compress.go
package xylium

import (
	"strconv" // For converting Content-Length int to string.
	"strings" // For string manipulation (content type parsing).

	"github.com/valyala/fasthttp" // For fasthttp constants and Gzip compression.
)

// GzipConfig defines the configuration for the Gzip compression middleware.
type GzipConfig struct {
	// Level is the Gzip compression level.
	// Options include:
	//  - fasthttp.CompressNoCompression
	//  - fasthttp.CompressBestSpeed
	//  - fasthttp.CompressBestCompression
	//  - fasthttp.CompressDefaultCompression
	// Default: fasthttp.CompressDefaultCompression.
	Level int

	// MinLength is the minimum response body length (in bytes) to trigger compression.
	// Responses smaller than this will not be compressed.
	// Default: 0 (compress all eligible responses regardless of size, if not specified).
	// A common value might be 1024 (1KB).
	MinLength int

	// ContentTypes is a list of MIME types that should be considered for compression.
	// If empty, a default list of common compressible types will be used.
	// Comparison is case-insensitive.
	// Example: []string{"application/json", "text/html; charset=utf-8"}
	ContentTypes []string
}

// defaultCompressContentTypes is a list of common MIME types that are typically good candidates for Gzip compression.
var defaultCompressContentTypes = []string{
	"text/plain", "text/html", "text/css", "text/xml", "text/javascript",
	"application/json", "application/xml", "application/javascript", "application/x-javascript",
	"application/rss+xml", "application/atom+xml", "image/svg+xml",
}

// Gzip returns a Gzip compression middleware with default configuration.
func Gzip() Middleware {
	// Uses defaults defined within GzipWithConfig.
	return GzipWithConfig(GzipConfig{})
}

// GzipWithConfig returns a Gzip compression middleware with the provided custom configuration.
func GzipWithConfig(config GzipConfig) Middleware {
	// Apply default compression level if not specified.
	if config.Level == 0 { // fasthttp.CompressNoCompression is 0, so check for explicit non-zero or use default.
		config.Level = fasthttp.CompressDefaultCompression
	}

	// Prepare a set of compressible content types for efficient lookup.
	// Content types are normalized to lowercase.
	compressibleTypes := make(map[string]struct{})
	typesToUse := config.ContentTypes
	if len(typesToUse) == 0 {
		typesToUse = defaultCompressContentTypes // Use default list if user provides none.
	}
	for _, t := range typesToUse {
		// Normalize by taking the part before any ';' (e.g., "text/html; charset=utf-8" -> "text/html")
		// and converting to lowercase.
		normalizedType := strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
		if normalizedType != "" {
			compressibleTypes[normalizedType] = struct{}{}
		}
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Get request-scoped logger.

			// 1. Check if the client accepts Gzip encoding.
			acceptEncoding := c.Header("Accept-Encoding") // fasthttp normalizes header keys.
			if !strings.Contains(acceptEncoding, "gzip") {
				logger.Debugf("Gzip: Client does not accept gzip encoding ('%s'). Skipping compression for %s %s.",
					acceptEncoding, c.Method(), c.Path())
				return next(c) // Proceed without compression.
			}

			// 2. Call the next handler in the chain to generate the response.
			err := next(c)
			if err != nil {
				// If an error occurs in subsequent handlers, do not attempt compression.
				logger.Debugf("Gzip: Error occurred in handler chain. Skipping compression for %s %s.", c.Method(), c.Path())
				return err
			}

			// 3. Post-handler checks before applying compression:
			//    - Don't compress if the response has already been committed (e.g., hijacked connection).
			//    - Don't compress error responses (e.g., 4xx, 5xx status codes).
			//    - Don't compress if "Content-Encoding" is already set (e.g., to "br" or already "gzip").
			if c.ResponseCommitted() {
				logger.Debugf("Gzip: Response already committed. Skipping compression for %s %s.", c.Method(), c.Path())
				return nil
			}
			if c.Ctx.Response.StatusCode() >= StatusBadRequest { // StatusBadRequest is 400
				logger.Debugf("Gzip: Response status code %d is >= 400. Skipping compression for %s %s.",
					c.Ctx.Response.StatusCode(), c.Method(), c.Path())
				return nil
			}
			if c.Ctx.Response.Header.Peek("Content-Encoding") != nil {
				logger.Debugf("Gzip: 'Content-Encoding' header already set to '%s'. Skipping compression for %s %s.",
					string(c.Ctx.Response.Header.Peek("Content-Encoding")), c.Method(), c.Path())
				return nil
			}


			responseBody := c.Ctx.Response.Body()
			// 4. Don't compress if the response body is empty.
			if len(responseBody) == 0 {
				logger.Debugf("Gzip: Response body is empty. Skipping compression for %s %s.", c.Method(), c.Path())
				return nil
			}

			// 5. Don't compress if body length is below the configured MinLength.
			if config.MinLength > 0 && len(responseBody) < config.MinLength {
				logger.Debugf("Gzip: Response body length %d is less than MinLength %d. Skipping compression for %s %s.",
					len(responseBody), config.MinLength, c.Method(), c.Path())
				return nil
			}

			// 6. Check if the response Content-Type is eligible for compression.
			contentType := string(c.Ctx.Response.Header.ContentType())
			normalizedContentType := strings.ToLower(strings.Split(contentType, ";")[0])
			if _, typeIsCompressible := compressibleTypes[normalizedContentType]; !typeIsCompressible {
				logger.Debugf("Gzip: Content-Type '%s' (normalized: '%s') is not in compressible types list. Skipping compression for %s %s.",
					contentType, normalizedContentType, c.Method(), c.Path())
				return nil
			}

			// 7. Perform Gzip compression.
			logger.Debugf("Gzip: Compressing response for %s %s (Content-Type: %s, Original Size: %d bytes).",
				c.Method(), c.Path(), contentType, len(responseBody))

			// fasthttp.AppendGzipBytesLevel appends compressed data to a destination slice (nil creates new).
			compressedBody := fasthttp.AppendGzipBytesLevel(nil, responseBody, config.Level)

			// 8. Update response body and headers.
			c.Ctx.Response.SetBodyRaw(compressedBody) // SetBodyRaw avoids copying.
			c.SetHeader("Content-Encoding", "gzip")
			// Content-Length must be updated to the size of the compressed body.
			c.SetHeader("Content-Length", strconv.Itoa(len(compressedBody)))
			// Add Vary: Accept-Encoding header to inform caches that the response
			// can vary based on the client's Accept-Encoding header.
			c.SetHeader("Vary", "Accept-Encoding") // fasthttp appends to Vary if it exists.

			logger.Debugf("Gzip: Compression successful for %s %s. New size: %d bytes.",
				c.Method(), c.Path(), len(compressedBody))

			return nil // No error from the compression process itself.
		}
	}
}
