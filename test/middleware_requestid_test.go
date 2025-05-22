// File: /test/middleware_requestid_test.go
package xylium_test

import (
	"testing"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/valyala/fasthttp"
)

// Helper untuk menjalankan middleware RequestID dan handler dummy
func runRequestIDMiddleware(
	t *testing.T,
	mw xylium.Middleware,
	incomingHeaderName string,
	incomingHeaderValue string,
	expectedResponseHeaderName string,
) (requestIDInContext interface{}, requestIDInResponseHeader string, handlerCalled bool) {

	var fasthttpCtx fasthttp.RequestCtx
	if incomingHeaderValue != "" {
		fasthttpCtx.Request.Header.Set(incomingHeaderName, incomingHeaderValue)
	}

	// Buat router minimal jika logger di handler dibutuhkan, tapi untuk tes ini mungkin tidak
	// router := xylium.NewRouterForTesting()
	ctx := xylium.NewContextForTest(nil, &fasthttpCtx)
	// ctx.SetRouterForTesting(router) // Uncomment jika handler butuh logger dari context

	dummyHandlerCalled := false
	dummyHandler := func(c *xylium.Context) error {
		dummyHandlerCalled = true
		val, exists := c.Get(xylium.ContextKeyRequestID)
		if exists {
			requestIDInContext = val
		}
		return nil
	}

	// Terapkan middleware ke handler dummy
	handlerWithMiddleware := mw(dummyHandler)

	// Jalankan handler
	err := handlerWithMiddleware(ctx)
	if err != nil {
		t.Fatalf("Middleware execution returned an error: %v", err)
	}

	return requestIDInContext, string(fasthttpCtx.Response.Header.Peek(expectedResponseHeaderName)), dummyHandlerCalled
}

func TestRequestID_Default(t *testing.T) {
	mw := xylium.RequestID()

	t.Run("NoIncomingHeader_GeneratesID", func(t *testing.T) {
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mw, xylium.DefaultRequestIDHeader, "", xylium.DefaultRequestIDHeader)

		if !called {
			t.Error("Handler was not called")
		}
		if idInCtx == nil {
			t.Fatal("Request ID not found in context")
		}
		idCtxStr, okCtx := idInCtx.(string)
		if !okCtx || idCtxStr == "" {
			t.Errorf("Request ID in context is not a non-empty string: %v", idInCtx)
		}
		if idInResp == "" {
			t.Error("Request ID not found in response header")
		}
		if idCtxStr != idInResp {
			t.Errorf("Request ID in context ('%s') does not match ID in response header ('%s')", idCtxStr, idInResp)
		}
		// Sulit untuk memvalidasi format UUID secara tepat tanpa library eksternal di sini,
		// tapi kita bisa cek panjangnya atau pola dasar jika perlu.
		// Untuk sekarang, non-empty string sudah cukup baik.
		if len(idCtxStr) < 30 { // UUID v4 biasanya 36 karakter
			t.Errorf("Generated Request ID '%s' seems too short for a UUID", idCtxStr)
		}
	})

	t.Run("WithIncomingHeader_UsesExistingID", func(t *testing.T) {
		existingID := "my-custom-request-id-123"
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mw, xylium.DefaultRequestIDHeader, existingID, xylium.DefaultRequestIDHeader)

		if !called {
			t.Error("Handler was not called")
		}
		if idInCtx == nil {
			t.Fatal("Request ID not found in context")
		}
		idCtxStr, okCtx := idInCtx.(string)
		if !okCtx || idCtxStr != existingID {
			t.Errorf("Expected Request ID in context to be '%s', got '%s'", existingID, idCtxStr)
		}
		if idInResp != existingID {
			t.Errorf("Expected Request ID in response header to be '%s', got '%s'", existingID, idInResp)
		}
	})
}

func TestRequestID_WithCustomConfig(t *testing.T) {
	customHeader := "X-Correlation-ID"
	customGeneratedValue := "generated-by-custom-func"

	config := xylium.RequestIDConfig{
		HeaderName: customHeader,
		Generator: func() string {
			return customGeneratedValue
		},
	}
	mw := xylium.RequestIDWithConfig(config)

	t.Run("CustomHeader_NoIncoming_GeneratesCustom", func(t *testing.T) {
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mw, customHeader, "", customHeader)

		if !called {
			t.Error("Handler was not called")
		}
		if idInCtx == nil {
			t.Fatal("Request ID not found in context")
		}
		idCtxStr, okCtx := idInCtx.(string)
		if !okCtx || idCtxStr != customGeneratedValue {
			t.Errorf("Expected Request ID in context to be '%s', got '%s'", customGeneratedValue, idCtxStr)
		}
		if idInResp != customGeneratedValue {
			t.Errorf("Expected Request ID in response header '%s' to be '%s', got '%s'", customHeader, customGeneratedValue, idInResp)
		}
	})

	t.Run("CustomHeader_WithIncoming_UsesExisting", func(t *testing.T) {
		existingID := "existing-correlation-id-456"
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mw, customHeader, existingID, customHeader)

		if !called {
			t.Error("Handler was not called")
		}
		if idInCtx == nil {
			t.Fatal("Request ID not found in context")
		}
		idCtxStr, okCtx := idInCtx.(string)
		if !okCtx || idCtxStr != existingID {
			t.Errorf("Expected Request ID in context to be '%s', got '%s'", existingID, idCtxStr)
		}
		if idInResp != existingID {
			t.Errorf("Expected Request ID in response header '%s' to be '%s', got '%s'", customHeader, existingID, idInResp)
		}
	})

	t.Run("DefaultGenerator_CustomHeader", func(t *testing.T) {
		// Tes di mana hanya HeaderName yang dikustomisasi, Generator menggunakan default (UUID)
		configOnlyHeader := xylium.RequestIDConfig{
			HeaderName: customHeader,
			// Generator: nil, // Akan menggunakan default UUID generator
		}
		mwOnlyHeader := xylium.RequestIDWithConfig(configOnlyHeader)
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mwOnlyHeader, customHeader, "", customHeader)

		if !called {
			t.Error("Handler was not called")
		}
		if idInCtx == nil {
			t.Fatal("Request ID not found in context")
		}
		idCtxStr, okCtx := idInCtx.(string)
		if !okCtx || idCtxStr == "" {
			t.Errorf("Request ID in context is not a non-empty string: %v", idInCtx)
		}
		if idInResp == "" {
			t.Error("Request ID not found in response header")
		}
		if idCtxStr != idInResp {
			t.Errorf("Request ID in context ('%s') does not match ID in response header ('%s')", idCtxStr, idInResp)
		}
		if len(idCtxStr) < 30 {
			t.Errorf("Generated Request ID '%s' (default generator) seems too short for a UUID", idCtxStr)
		}
	})

	t.Run("DefaultHeader_CustomGenerator", func(t *testing.T) {
		// Tes di mana hanya Generator yang dikustomisasi, HeaderName menggunakan default
		configOnlyGen := xylium.RequestIDConfig{
			// HeaderName: "", // Akan menggunakan default X-Request-ID
			Generator: func() string { return "fixed-id-for-test" },
		}
		mwOnlyGen := xylium.RequestIDWithConfig(configOnlyGen)
		idInCtx, idInResp, called := runRequestIDMiddleware(t, mwOnlyGen, xylium.DefaultRequestIDHeader, "", xylium.DefaultRequestIDHeader)

		if !called {
			t.Error("Handler was not called")
		}
		idCtxStr, _ := idInCtx.(string)
		if idCtxStr != "fixed-id-for-test" {
			t.Errorf("Expected custom generated ID 'fixed-id-for-test', got '%s'", idCtxStr)
		}
		if idInResp != "fixed-id-for-test" {
			t.Errorf("Expected custom generated ID 'fixed-id-for-test' in response header, got '%s'", idInResp)
		}
	})
}
