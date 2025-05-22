// File: xylium-core-main/test/middleware_gzip_test.go
package xylium_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/valyala/fasthttp"
)

// Helper untuk menjalankan middleware Gzip
type gzipTestResult struct {
	statusCode        int
	responseBody      []byte // Akan menyimpan body respons
	contentEncoding   string
	contentType       string
	contentLength     string
	varyHeader        string
	handlerCalled     bool
	nextError         error
	isActuallyGzipped bool
}

func runGzipMiddleware(
	t *testing.T,
	config *xylium.GzipConfig,
	acceptEncodingHeader string,
	handlerResponseStatus int,
	handlerResponseBody string,
	handlerResponseContentType string,
	handlerPreSetContentEncoding string,
) gzipTestResult {

	var fasthttpCtx fasthttp.RequestCtx
	if acceptEncodingHeader != "" {
		fasthttpCtx.Request.Header.Set("Accept-Encoding", acceptEncodingHeader)
	}
	fasthttpCtx.Request.Header.SetMethod("GET")
	fasthttpCtx.Request.SetRequestURI("/test-gzip")

	router := xylium.NewRouterForTesting()
	ctx := xylium.NewContextForTest(nil, &fasthttpCtx)
	ctx.SetRouterForTesting(router)

	result := gzipTestResult{}

	dummyHandler := func(c *xylium.Context) error {
		result.handlerCalled = true
		c.Status(handlerResponseStatus)
		if handlerResponseContentType != "" {
			c.SetContentType(handlerResponseContentType)
		}
		if handlerPreSetContentEncoding != "" {
			c.SetHeader("Content-Encoding", handlerPreSetContentEncoding)
		}
		if handlerResponseBody != "" {
			if strings.HasPrefix(handlerResponseContentType, "application/json") {
				return c.JSON(handlerResponseStatus, xylium.M{"data": handlerResponseBody})
			}
			return c.WriteString(handlerResponseBody)
		}
		return nil
	}

	var mw xylium.Middleware
	if config != nil {
		mw = xylium.GzipWithConfig(*config)
	} else {
		mw = xylium.Gzip()
	}

	handlerWithMiddleware := mw(dummyHandler)
	result.nextError = handlerWithMiddleware(ctx)

	result.statusCode = fasthttpCtx.Response.StatusCode()

	// <<< PERBAIKAN DI SINI >>>
	// Ambil salinan body untuk memastikan kita punya snapshot yang tidak berubah
	// jika ada operasi lain pada fasthttpCtx.Response.Body() setelah ini (meskipun jarang).
	// Untuk pembacaan saja, fasthttpCtx.Response.Body() langsung sudah cukup.
	// Namun, membuat salinan adalah praktik yang lebih aman di tes.
	bodyBytes := fasthttpCtx.Response.Body()
	result.responseBody = make([]byte, len(bodyBytes))
	copy(result.responseBody, bodyBytes)
	// <<< AKHIR PERBAIKAN >>>

	result.contentEncoding = string(fasthttpCtx.Response.Header.Peek("Content-Encoding"))
	result.contentType = string(fasthttpCtx.Response.Header.ContentType())
	result.contentLength = string(fasthttpCtx.Response.Header.Peek("Content-Length"))
	result.varyHeader = string(fasthttpCtx.Response.Header.Peek("Vary"))

	if result.contentEncoding == "gzip" && len(result.responseBody) > 0 {
		gzReader, err := gzip.NewReader(bytes.NewReader(result.responseBody))
		if err == nil {
			// Penting untuk menutup gzReader setelah selesai
			// Jika tidak, resource mungkin tidak dilepaskan dengan benar,
			// terutama dalam loop atau tes yang berjalan berkali-kali.
			defer func() {
				if errClose := gzReader.Close(); errClose != nil {
					t.Logf("Gzip check: Error closing gzip.Reader: %v", errClose)
				}
			}()
			_, errRead := io.ReadAll(gzReader)
			if errRead == nil {
				result.isActuallyGzipped = true
			} else {
				t.Logf("Gzip check: ReadAll from gzip.Reader failed: %v", errRead)
			}
		} else {
			t.Logf("Gzip check: gzip.NewReader failed: %v", err)
		}
	}

	return result
}

func TestGzipMiddleware_Defaults(t *testing.T) {
	longBody := strings.Repeat("Xylium Framework is Fast! ", 100)

	t.Run("ClientAcceptsGzip_EligibleContentType_Compresses", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "gzip, deflate, br", http.StatusOK, longBody, "text/html", "")

		if !result.handlerCalled {
			t.Error("Handler not called")
		}
		if result.nextError != nil {
			t.Errorf("Unexpected error: %v", result.nextError)
		}
		if result.statusCode != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, result.statusCode)
		}

		if result.contentEncoding != "gzip" {
			t.Errorf("Expected Content-Encoding 'gzip', got '%s'", result.contentEncoding)
		}
		if !result.isActuallyGzipped {
			t.Error("Body was not actually gzipped")
		}
		if !strings.Contains(result.varyHeader, "Accept-Encoding") {
			t.Errorf("Expected Vary header to contain 'Accept-Encoding', got '%s'", result.varyHeader)
		}
		compressedLength, _ := strconv.Atoi(result.contentLength)
		if compressedLength == 0 || compressedLength >= len(longBody) {
			t.Errorf("Expected compressed Content-Length (%d) to be less than original (%d)", compressedLength, len(longBody))
		}
	})

	t.Run("ClientAcceptsGzip_NonEligibleContentType_NoCompress", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "gzip", http.StatusOK, longBody, "image/jpeg", "")

		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no gzip compression for image/jpeg, but Content-Encoding is '%s'", result.contentEncoding)
		}
		if result.isActuallyGzipped {
			t.Error("Body was gzipped for image/jpeg")
		}
	})

	t.Run("ClientDoesNotAcceptGzip_NoCompress", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "deflate, br", http.StatusOK, longBody, "text/html", "")

		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no gzip compression, but Content-Encoding is '%s'", result.contentEncoding)
		}
		if result.isActuallyGzipped {
			t.Error("Body was gzipped when client does not accept gzip")
		}
	})

	t.Run("EmptyBody_NoCompress", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "gzip", http.StatusOK, "", "text/html", "")
		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no gzip compression for empty body, got '%s'", result.contentEncoding)
		}
	})

	t.Run("ErrorStatus_NoCompress", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "gzip", http.StatusNotFound, "Not Found", "text/plain", "")
		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no gzip compression for 404 status, got '%s'", result.contentEncoding)
		}
	})

	t.Run("ContentEncodingAlreadySet_NoCompress", func(t *testing.T) {
		result := runGzipMiddleware(t, nil, "gzip", http.StatusOK, longBody, "text/html", "br")
		if result.contentEncoding != "br" {
			t.Errorf("Expected Content-Encoding 'br', got '%s'", result.contentEncoding)
		}
		if result.isActuallyGzipped {
			t.Error("Body was gzipped when Content-Encoding was already set to 'br'")
		}
	})
}

func TestGzipMiddleware_WithCustomConfig(t *testing.T) {
	mediumBody := strings.Repeat("Medium text for Gzip. ", 10)
	longBody := strings.Repeat("Long text, definitely compressible. ", 50)

	t.Run("CustomMinLength_BodyTooShort", func(t *testing.T) {
		config := xylium.GzipConfig{MinLength: 250}
		if len(mediumBody) >= config.MinLength {
			t.Fatalf("Test setup error: mediumBody length (%d) is not less than MinLength (%d)", len(mediumBody), config.MinLength)
		}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, mediumBody, "text/plain", "")

		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no compression for body shorter than MinLength, got encoding '%s'", result.contentEncoding)
		}
	})

	t.Run("CustomMinLength_BodyLongEnough", func(t *testing.T) {
		config := xylium.GzipConfig{MinLength: 50}
		if len(mediumBody) < config.MinLength {
			t.Fatalf("Test setup error: mediumBody length (%d) is not >= MinLength (%d)", len(mediumBody), config.MinLength)
		}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, mediumBody, "text/plain", "")

		if result.contentEncoding != "gzip" {
			t.Errorf("Expected compression for body longer than MinLength, got encoding '%s'", result.contentEncoding)
		}
		if !result.isActuallyGzipped {
			t.Error("Body was not actually gzipped when it met MinLength")
		}
	})

	t.Run("CustomContentTypes_Eligible", func(t *testing.T) {
		config := xylium.GzipConfig{ContentTypes: []string{"application/custom-type", "text/vnd.xylium"}}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, longBody, "application/custom-type", "")

		if result.contentEncoding != "gzip" {
			t.Errorf("Expected compression for custom eligible Content-Type, got '%s'", result.contentEncoding)
		}
		if !result.isActuallyGzipped {
			t.Error("Body not gzipped for custom eligible Content-Type")
		}
	})

	t.Run("CustomContentTypes_NotEligible", func(t *testing.T) {
		config := xylium.GzipConfig{ContentTypes: []string{"application/custom-type"}}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, longBody, "text/plain", "")

		if result.contentEncoding == "gzip" {
			t.Errorf("Expected no compression for non-custom-eligible Content-Type, got '%s'", result.contentEncoding)
		}
	})

	t.Run("CustomCompressionLevel_BestSpeed", func(t *testing.T) {
		config := xylium.GzipConfig{Level: fasthttp.CompressBestSpeed}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, longBody, "text/plain", "")

		if result.contentEncoding != "gzip" {
			t.Errorf("Expected compression with CompressBestSpeed, got '%s'", result.contentEncoding)
		}
		if !result.isActuallyGzipped {
			t.Error("Body not gzipped with CompressBestSpeed")
		}
	})

	t.Run("CustomCompressionLevel_NoCompressionConstant", func(t *testing.T) {
		config := xylium.GzipConfig{Level: fasthttp.CompressNoCompression}
		result := runGzipMiddleware(t, &config, "gzip", http.StatusOK, longBody, "text/plain", "")

		if result.contentEncoding != "gzip" {
			t.Errorf("Expected compression (default level) even if config level was NoCompression, got '%s'", result.contentEncoding)
		}
		if !result.isActuallyGzipped {
			t.Error("Body not gzipped when config level was NoCompression (expected default compression)")
		}
	})
}
