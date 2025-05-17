package xylium

import (
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"
)

// GzipConfig mendefinisikan konfigurasi untuk middleware Gzip.
type GzipConfig struct {
	Level        int
	MinLength    int
	ContentTypes []string
}

var defaultCompressContentTypes = []string{
	"text/plain", "text/html", "text/css", "text/xml", "text/javascript",
	"application/json", "application/xml", "application/javascript",
	"application/rss+xml", "application/atom+xml", "image/svg+xml",
}

// Gzip mengembalikan middleware Gzip dengan konfigurasi default.
func Gzip() Middleware {
	return GzipWithConfig(GzipConfig{})
}

// GzipWithConfig mengembalikan middleware Gzip dengan konfigurasi yang diberikan.
func GzipWithConfig(config GzipConfig) Middleware {
	if config.Level == 0 {
		config.Level = fasthttp.CompressDefaultCompression
	}

	var typesToCompress map[string]struct{}
	if len(config.ContentTypes) > 0 {
		typesToCompress = make(map[string]struct{}, len(config.ContentTypes))
		for _, t := range config.ContentTypes {
			typesToCompress[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
		}
	} else {
		typesToCompress = make(map[string]struct{}, len(defaultCompressContentTypes))
		for _, t := range defaultCompressContentTypes {
			typesToCompress[t] = struct{}{}
		}
	}

	// PERBAIKAN: Hapus deklarasi compressWrapper yang tidak digunakan
	// compressWrapper := fasthttp.CompressHandlerLevel(func(ctx *fasthttp.RequestCtx) {
	// }, config.Level)

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			acceptEncoding := string(c.Ctx.Request.Header.Peek("Accept-Encoding"))
			if !strings.Contains(acceptEncoding, "gzip") {
				return next(c)
			}

			err := next(c)
			if err != nil {
				return err
			}

			if c.ResponseCommitted() || c.Ctx.Response.StatusCode() >= 400 {
				return nil
			}

			responseBody := c.Ctx.Response.Body()
			if len(responseBody) == 0 || (config.MinLength > 0 && len(responseBody) < config.MinLength) {
				return nil
			}

			contentType := string(c.Ctx.Response.Header.ContentType())
			normalizedContentType := strings.ToLower(strings.Split(contentType, ";")[0])

			_, typeIsAllowed := typesToCompress[normalizedContentType]

			if !typeIsAllowed { // Jika tipe tidak diizinkan (baik karena tidak di default atau tidak di custom list)
				return nil
			}

			compressedBody := fasthttp.AppendGzipBytesLevel(nil, responseBody, config.Level)

			c.Ctx.Response.SetBodyRaw(compressedBody)
			c.SetHeader("Content-Encoding", "gzip")
			c.SetHeader("Content-Length", strconv.Itoa(len(compressedBody))) // strconv sekarang sudah diimpor
			c.SetHeader("Vary", "Accept-Encoding")

			return nil
		}
	}
}
