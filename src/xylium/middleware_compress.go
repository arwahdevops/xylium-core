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
	// Consider adding other text-based types like "application/manifest+json", "text/calendar", etc.
}

// Gzip returns a Gzip compression middleware with default configuration.
// See `GzipConfig` and `DefaultCORSConfig` for default values.
func Gzip() Middleware {
	// Uses defaults defined within GzipWithConfig by passing an empty GzipConfig struct.
	return GzipWithConfig(GzipConfig{})
}

// GzipWithConfig returns a Gzip compression middleware with the provided custom configuration.
// It handles checking client capabilities, response eligibility, performing compression,
// and setting appropriate response headers.
func GzipWithConfig(config GzipConfig) Middleware {
	// Apply default compression level if not specified or if set to NoCompression (0)
	// by the user but they intended to use defaults.
	// `fasthttp.CompressDefaultCompression` is typically a good balance.
	if config.Level == fasthttp.CompressNoCompression { // Check against actual NoCompression constant
		config.Level = fasthttp.CompressDefaultCompression
	}
	// Note: MinLength defaults to 0 (compress all sizes) if not set, which is acceptable.

	// Prepare a set of compressible content types for efficient lookup.
	// Content types are normalized to lowercase and only the base type is used
	// (e.g., "text/html" from "text/html; charset=utf-8").
	compressibleTypes := make(map[string]struct{})
	typesToUse := config.ContentTypes
	if len(typesToUse) == 0 {
		typesToUse = defaultCompressContentTypes // Use default list if user provides none.
	}
	for _, t := range typesToUse {
		// Normalize by taking the part before any ';' and converting to lowercase.
		normalizedType := strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
		if normalizedType != "" {
			compressibleTypes[normalizedType] = struct{}{}
		}
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "Gzip"}) // Get request-scoped, contextualized logger.

			// 1. Check if the client accepts Gzip encoding via the "Accept-Encoding" header.
			acceptEncoding := c.Header("Accept-Encoding") // fasthttp normalizes header keys.
			if !strings.Contains(acceptEncoding, "gzip") {
				logger.Debugf("Client does not accept gzip encoding ('%s'). Skipping compression for %s %s.",
					acceptEncoding, c.Method(), c.Path())
				return next(c) // Proceed without compression.
			}

			// 2. Call the next handler in the chain to generate the response body and headers.
			err := next(c)
			if err != nil {
				// If an error occurs in subsequent handlers, do not attempt compression,
				// as the response might be an error page or partially formed.
				logger.Debugf("Error occurred in handler chain. Skipping compression for %s %s.", c.Method(), c.Path())
				return err // Propagate the error.
			}

			// 3. Post-handler checks before applying compression:
			//    - Don't compress if the response has already been committed (e.g., hijacked connection).
			//    - Don't compress error responses (e.g., 4xx, 5xx status codes are usually not compressed).
			//    - Don't compress if "Content-Encoding" is already set (e.g., to "br" or already "gzip" by another middleware/handler).
			if c.ResponseCommitted() {
				logger.Debugf("Response already committed. Skipping compression for %s %s.", c.Method(), c.Path())
				return nil
			}
			if c.Ctx.Response.StatusCode() >= StatusBadRequest { // StatusBadRequest is 400.
				logger.Debugf("Response status code %d is >= 400 (client/server error). Skipping compression for %s %s.",
					c.Ctx.Response.StatusCode(), c.Method(), c.Path())
				return nil
			}
			if len(c.Ctx.Response.Header.Peek("Content-Encoding")) > 0 { // Check if Content-Encoding is already set.
				logger.Debugf("'Content-Encoding' header already set to '%s'. Skipping compression for %s %s.",
					string(c.Ctx.Response.Header.Peek("Content-Encoding")), c.Method(), c.Path())
				return nil
			}

			responseBody := c.Ctx.Response.Body()
			// 4. Don't compress if the response body is empty.
			if len(responseBody) == 0 {
				logger.Debugf("Response body is empty. Skipping compression for %s %s.", c.Method(), c.Path())
				return nil
			}

			// 5. Don't compress if body length is below the configured MinLength.
			if config.MinLength > 0 && len(responseBody) < config.MinLength {
				logger.Debugf("Response body length %d bytes is less than MinLength %d bytes. Skipping compression for %s %s.",
					len(responseBody), config.MinLength, c.Method(), c.Path())
				return nil
			}

			// 6. Check if the response Content-Type is in the list of eligible types for compression.
			contentType := string(c.Ctx.Response.Header.ContentType())
			normalizedContentType := strings.ToLower(strings.Split(contentType, ";")[0])
			if _, typeIsCompressible := compressibleTypes[normalizedContentType]; !typeIsCompressible {
				logger.Debugf("Content-Type '%s' (normalized: '%s') is not in the compressible types list. Skipping compression for %s %s.",
					contentType, normalizedContentType, c.Method(), c.Path())
				return nil
			}

			// 7. Perform Gzip compression.
			logger.Debugf("Compressing response for %s %s (Content-Type: %s, Original Size: %d bytes, Level: %d).",
				c.Method(), c.Path(), contentType, len(responseBody), config.Level)

			// `fasthttp.AppendGzipBytesLevel` appends compressed data to a destination slice.
			// Passing `nil` as the first argument creates a new slice for the compressed body.
			compressedBody := fasthttp.AppendGzipBytesLevel(nil, responseBody, config.Level)

			// 8. Update response body and headers with compressed data and metadata.
			c.Ctx.Response.SetBodyRaw(compressedBody) // SetBodyRaw avoids an unnecessary copy of the compressed data.
			c.SetHeader("Content-Encoding", "gzip")
			// The Content-Length header MUST be updated to the size of the compressed body.
			c.SetHeader("Content-Length", strconv.Itoa(len(compressedBody)))
			// Add "Vary: Accept-Encoding" header. This informs caches that the response
			// can vary based on the client's "Accept-Encoding" header, ensuring that
			// clients not accepting gzip don't receive a cached gzipped response.
			// fasthttp's `c.SetHeader` (via `c.Ctx.Response.Header.Set`) should correctly handle
			// appending to "Vary" if it already exists with other values.
			// If more precise control is needed (e.g. ensuring it's only added once or merged correctly):
			// currentVary := string(c.Ctx.Response.Header.Peek("Vary"))
			// if currentVary == "" { c.SetHeader("Vary", "Accept-Encoding") }
			// else if !strings.Contains(currentVary, "Accept-Encoding") { c.SetHeader("Vary", currentVary + ", Accept-Encoding") }
			// However, standard library and fasthttp usually handle this fine with Set.
			// For fasthttp, using `c.Ctx.Response.Header.Add("Vary", "Accept-Encoding")` is safer for multi-value.
			c.Ctx.Response.Header.Add("Vary", "Accept-Encoding")

			logger.Debugf("Compression successful for %s %s. New size: %d bytes.",
				c.Method(), c.Path(), len(compressedBody))

			return nil // No error from the compression process itself to propagate.
		}
	}
}
