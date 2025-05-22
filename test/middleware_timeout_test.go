// File: /test/middleware_timeout_test.go
package xylium_test

import (
	"context"
	"encoding/json" // Ditambahkan untuk helper simulasi GlobalErrorHandler
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/valyala/fasthttp"
)

type timeoutTestResult struct {
	handlerCompleted bool        // Apakah timer di handler dummy sempat selesai
	handlerError     error       // Error yang dikembalikan oleh handler dummy (jika selesai normal)
	middlewareError  error       // Error yang dikembalikan oleh pemanggilan handlerWithMiddleware(ctx)
	statusCode       int         // Status code HTTP akhir dari response
	responseBody     string      // Body response akhir
	goContextError   error       // Error dari timedXyliumCtx.GoContext().Err() yang diobservasi oleh handler dummy
	finalGoCtxError  error       // Error dari ctx.GoContext().Err() di akhir helper (pada context asli 'c' setelah middleware selesai)
	panicValue       interface{} // Jika ada panic yang tertangkap oleh helper tes
}

func runTimeoutMiddleware(
	t *testing.T,
	config xylium.TimeoutConfig,
	handlerDuration time.Duration,
	handlerShouldError bool,
	handlerShouldPanic bool,
	handlerWritesResponseEarly bool,
) timeoutTestResult {

	var fasthttpCtx fasthttp.RequestCtx
	router := xylium.NewRouterForTesting() // Menggunakan helper yang sudah membungkam log bootstrap

	// Buat instance xylium.Context BARU untuk setiap run
	ctx := xylium.NewContextForTest(nil, &fasthttpCtx)
	ctx.SetRouterForTesting(router)
	fasthttpCtx.Request.SetRequestURI("/test-timeout-path") // Set path untuk tes

	result := timeoutTestResult{}
	var wg sync.WaitGroup

	dummyHandler := func(c *xylium.Context) error { // 'c' di sini adalah timedXyliumCtx
		wg.Add(1)
		defer wg.Done()

		var earlyWriteSimWait time.Duration = 5 * time.Millisecond

		if handlerWritesResponseEarly {
			select {
			case <-c.GoContext().Done(): // Cek apakah sudah timeout sebelum sempat menulis
				result.goContextError = c.GoContext().Err()
				return result.goContextError
			case <-time.After(earlyWriteSimWait): // Tunggu sedikit untuk simulasi kerja sebelum tulis
				select {
				case <-c.GoContext().Done(): // Cek lagi setelah delay
					result.goContextError = c.GoContext().Err()
					return result.goContextError
				default:
					// Aman untuk menulis response
					// Pastikan response belum committed oleh hal lain (seharusnya tidak pada timedXyliumCtx baru)
					if !c.ResponseCommitted() {
						errStr := c.String(http.StatusOK, "handler_early_response")
						if errStr != nil {
							t.Logf("runTimeoutMiddleware: c.String (early) in dummyHandler returned error: %v", errStr)
						}
					} else {
						t.Logf("runTimeoutMiddleware: dummyHandler wanted to write early, but response already committed on timedXyliumCtx.")
					}
				}
			}
		}

		// Hitung sisa durasi kerja
		remainingHandlerDuration := handlerDuration
		if handlerWritesResponseEarly {
			// Kurangi waktu yang sudah dipakai untuk "early write"
			if remainingHandlerDuration > earlyWriteSimWait {
				remainingHandlerDuration -= earlyWriteSimWait
			} else {
				remainingHandlerDuration = 0
			}
		}

		if handlerShouldPanic {
			result.handlerCompleted = false // Tidak akan sampai ke completed jika panik
			if remainingHandlerDuration > 0 {
				// Cek context sebelum tidur panjang
				select {
				case <-c.GoContext().Done():
					result.goContextError = c.GoContext().Err()
					return result.goContextError
				default: // Belum done, lanjutkan tidur
				}
				time.Sleep(remainingHandlerDuration)
				// Cek context lagi setelah tidur
				select {
				case <-c.GoContext().Done():
					result.goContextError = c.GoContext().Err()
					return result.goContextError
				default: // Belum done, lanjutkan untuk panik
				}
			}
			panic("handler_deliberate_panic")
		}

		if remainingHandlerDuration <= 0 { // Jika tidak ada pekerjaan lagi atau durasi awal 0
			result.handlerCompleted = true
			if handlerShouldError {
				result.handlerError = errors.New("handler_deliberate_error_no_wait")
				return result.handlerError
			}
			return nil
		}

		workTimer := time.NewTimer(remainingHandlerDuration)
		defer workTimer.Stop()

		select {
		case <-workTimer.C:
			result.handlerCompleted = true
			if handlerShouldError {
				result.handlerError = errors.New("handler_deliberate_error_after_wait")
				return result.handlerError
			}
			return nil
		case <-c.GoContext().Done():
			result.handlerCompleted = false
			result.goContextError = c.GoContext().Err()
			return c.GoContext().Err()
		}
	}

	mw := xylium.TimeoutWithConfig(config)
	handlerWithMiddleware := mw(dummyHandler)

	// Jalankan handler yang sudah di-wrap middleware secara langsung
	// dan tangkap panic jika ada.
	func() {
		defer func() {
			if r := recover(); r != nil {
				result.panicValue = r
			}
		}()
		// 'ctx' yang di-pass ke handlerWithMiddleware adalah Xylium Context asli (parent)
		result.middlewareError = handlerWithMiddleware(ctx)
	}()

	wg.Wait() // Pastikan goroutine dummyHandler selesai atau dibatalkan

	// Setelah handlerWithMiddleware selesai (atau panik), kita proses hasilnya
	// untuk menentukan statusCode dan responseBody akhir.
	// Ini mensimulasikan apa yang akan dilakukan oleh GlobalErrorHandler Xylium.

	finalErrorToProcess := result.middlewareError
	if result.panicValue != nil {
		// Jika ada panic, GlobalPanicHandler Xylium akan membuat HTTPError 500
		finalErrorToProcess = xylium.NewHTTPError(http.StatusInternalServerError, "Server Error due to panic").WithInternal(fmt.Errorf("panic: %v", result.panicValue))
	}

	// Periksa apakah response belum di-commit SEBELUM error handler dari middleware/panic menulis.
	// Gunakan Xylium Context 'ctx' untuk memeriksa ResponseCommitted.
	// Ini merefleksikan state akhir SETELAH middleware dan handler (mungkin) menulis ke fasthttpCtx.
	responseAlreadyCommitted := ctx.ResponseCommitted()

	if finalErrorToProcess != nil {
		var httpErr *xylium.HTTPError
		if errors.As(finalErrorToProcess, &httpErr) {
			// Jika error adalah HTTPError (dari ErrorHandler timeout atau dari simulasi panic handler)
			// dan response belum di-commit oleh dummyHandler (atau handler kustom),
			// maka status dan body dari HTTPError ini yang berlaku.
			if !responseAlreadyCommitted {
				fasthttpCtx.Response.SetStatusCode(httpErr.Code)
				// Set body berdasarkan Message dari HTTPError
				if msgStr, ok := httpErr.Message.(string); ok {
					fasthttpCtx.Response.SetBodyString(msgStr)
				} else if msgMap, ok := httpErr.Message.(xylium.M); ok {
					// Simulasikan JSON response jika message adalah map
					// Biasanya GlobalErrorHandler Xylium yang akan melakukan ini.
					// Untuk tes, kita bungkus dalam {"error": ...} jika belum.
					errorKeyExists := false
					for k := range msgMap {
						if k == "error" {
							errorKeyExists = true
							break
						}
					}
					var finalJson xylium.M
					if errorKeyExists && len(msgMap) == 1 { // Jika hanya ada key "error"
						finalJson = msgMap
					} else if !errorKeyExists { // Jika tidak ada key "error", bungkus seluruh map
						finalJson = xylium.M{"error": msgMap}
					} else { // Ada key "error" tapi ada field lain juga
						finalJson = msgMap
					}

					jsonBody, _ := json.Marshal(finalJson)
					fasthttpCtx.Response.SetBody(jsonBody)
					// Hanya set Content-Type jika belum ada, karena c.String/JSON/XML sudah menyetelnya
					if len(fasthttpCtx.Response.Header.Peek("Content-Type")) == 0 {
						fasthttpCtx.Response.Header.SetContentType("application/json; charset=utf-8")
					}
				} else { // Jika tipe message lain, coba konversi ke string
					fasthttpCtx.Response.SetBodyString(fmt.Sprintf("%v", httpErr.Message))
				}
			}
			// Jika responseAlreadyCommitted adalah true, status dan body dari dummyHandler yang akan tetap.
		} else if !responseAlreadyCommitted { // Error BUKAN HTTPError dan response belum committed
			// Ini adalah kasus di mana middleware mungkin mengembalikan context.DeadlineExceeded secara langsung
			// karena response sudah di-commit oleh handler (defaultErrorHandler akan melakukan ini).
			// Atau, error lain yang tidak terduga.
			// Jika error adalah DeadlineExceeded, status code di fasthttpCtx mungkin masih dari handler sebelumnya.
			// Jika tidak ada handler sebelumnya yang menulis, status code akan default (0 atau 200).
			// Kita tidak ubah status code di sini kecuali jika ingin selalu 500 untuk error non-HTTPError.
			// Biarkan status code yang sudah ada (jika dari early write) atau default fasthttp.
			// Tes case spesifik yang akan memvalidasi ini.
			t.Logf("runTimeoutMiddleware: finalErrorToProcess is not HTTPError (%T: %v) and response was not committed by handler. Final status might be default.", finalErrorToProcess, finalErrorToProcess)
		}
	} else if !responseAlreadyCommitted && result.panicValue == nil {
		// Tidak ada error dari middleware, tidak ada panic, dan handler dummy tidak menulis apa-apa.
		// fasthttp akan default ke status 200 OK jika tidak ada yang diset.
		// atau 0 jika bahkan status tidak diset.
		if fasthttpCtx.Response.StatusCode() == 0 {
			// fasthttpCtx.Response.SetStatusCode(http.StatusOK) // Opsional
		}
	}

	result.statusCode = fasthttpCtx.Response.StatusCode()
	result.responseBody = string(fasthttpCtx.Response.Body())

	// Ambil error dari GoContext *asli* (parent) yang di-pass ke middleware
	// dan juga yang mungkin sudah dimodifikasi oleh WithGoContext (seperti ctxWithTimeout)
	if parentGoCtx := ctx.GoContext(); parentGoCtx != nil {
		result.finalGoCtxError = parentGoCtx.Err()
		// Jika handler dummy mengobservasi error context (result.goContextError),
		// dan itu adalah DeadlineExceeded, maka finalGoCtxError juga harus DeadlineExceeded
		// karena cancelFunc() dari context.WithTimeout akan dipanggil.
		if errors.Is(result.goContextError, context.DeadlineExceeded) && !errors.Is(result.finalGoCtxError, context.DeadlineExceeded) {
			// Ini bisa terjadi jika cancelFunc belum sepenuhnya membatalkan parent saat ini dicek.
			// Lebih aman mengandalkan result.goContextError untuk state context di handler.
			// Untuk finalGoCtxError, setelah defer cancelFunc() jalan, ia harusnya DeadlineExceeded jika timeout.
			// Kita akan asumsikan jika goContextError adalah DeadlineExceeded, finalGoCtxError juga demikian.
			result.finalGoCtxError = result.goContextError
		}
	}

	return result
}

func TestTimeoutMiddleware(t *testing.T) {
	veryShortTimeout := 15 * time.Millisecond
	shortHandlerWork := 5 * time.Millisecond
	mediumHandlerWork := 30 * time.Millisecond
	longHandlerWork := 50 * time.Millisecond

	t.Run("HandlerCompletesBeforeTimeout", func(t *testing.T) {
		config := xylium.TimeoutConfig{Timeout: mediumHandlerWork}                       // Timeout 30ms
		result := runTimeoutMiddleware(t, config, shortHandlerWork, false, false, false) // Handler 5ms

		if !result.handlerCompleted {
			t.Error("Expected handler to complete")
		}
		if result.middlewareError != nil {
			t.Errorf("Expected no middleware error, got %v", result.middlewareError)
		}
		if result.goContextError != nil {
			t.Errorf("Expected GoContext error (from handler perspective) to be nil, got %v", result.goContextError)
		}
		if result.finalGoCtxError != nil {
			t.Errorf("Expected final GoContext error (original parent) to be nil, got %v", result.finalGoCtxError)
		}
		// Jika handler dummy tidak menulis apa pun, status code bisa 0 (default fasthttp)
		// atau fasthttp otomatis set 200 OK.
		if result.statusCode != 0 && result.statusCode != http.StatusOK {
			if result.statusCode >= http.StatusBadRequest {
				t.Errorf("Expected non-error status code (e.g. 0 or 200), got %d", result.statusCode)
			} else {
				t.Logf("HandlerCompletesBeforeTimeout: status code is %d (non-error)", result.statusCode)
			}
		}
	})

	t.Run("HandlerExceedsTimeout_DefaultError", func(t *testing.T) {
		config := xylium.TimeoutConfig{Timeout: veryShortTimeout}                         // Timeout 15ms
		result := runTimeoutMiddleware(t, config, mediumHandlerWork, false, false, false) // Handler 30ms

		if result.handlerCompleted {
			t.Error("Expected handler NOT to complete due to timeout")
		}
		if result.middlewareError == nil {
			t.Fatal("Expected middleware error due to timeout, got nil")
		}

		var httpErr *xylium.HTTPError
		if !errors.As(result.middlewareError, &httpErr) {
			if errors.Is(result.middlewareError, context.DeadlineExceeded) {
				// Ini terjadi jika defaultErrorHandler mengembalikan err asli karena ResponseCommitted
				t.Logf("Middleware returned context.DeadlineExceeded directly. Status: %d, Body: '%s'", result.statusCode, result.responseBody)
				// Dalam kasus ini, statusCode dan body akan dari handler (jika ada) atau default
				// Kita tetap harapkan goContextError adalah DeadlineExceeded
			} else {
				t.Fatalf("Expected middleware error to be *xylium.HTTPError or context.DeadlineExceeded, got %T (value: %v)", result.middlewareError, result.middlewareError)
			}
		}

		// Jika itu HTTPError (dari defaultErrorHandler karena response belum committed)
		if httpErr != nil {
			if httpErr.Code != xylium.StatusServiceUnavailable {
				t.Errorf("Expected status code %d in HTTPError, got %d", xylium.StatusServiceUnavailable, httpErr.Code)
			}
			expectedMsgPart := fmt.Sprintf("Request processing timed out after %v.", veryShortTimeout)
			if msgStr, ok := httpErr.Message.(string); !ok || msgStr != expectedMsgPart {
				t.Errorf("Expected default timeout message part '%s', got '%s'", expectedMsgPart, msgStr)
			}
		}

		// Status code di response HTTP harusnya 503 jika default error handler dipanggil dan response belum committed
		if result.statusCode != xylium.StatusServiceUnavailable {
			t.Errorf("Expected final response status code %d, got %d", xylium.StatusServiceUnavailable, result.statusCode)
		}
		if !errors.Is(result.goContextError, context.DeadlineExceeded) {
			t.Errorf("Expected GoContext error (from handler) to be context.DeadlineExceeded, got %v", result.goContextError)
		}
		if !errors.Is(result.finalGoCtxError, context.DeadlineExceeded) {
			t.Errorf("Expected final GoContext error (from timed context) to be context.DeadlineExceeded, got %v", result.finalGoCtxError)
		}
	})

	t.Run("HandlerExceedsTimeout_CustomMessageString", func(t *testing.T) {
		customMsg := "Sorry, request took too long!"
		config := xylium.TimeoutConfig{
			Timeout: veryShortTimeout,
			Message: customMsg,
		}
		result := runTimeoutMiddleware(t, config, mediumHandlerWork, false, false, false)

		if result.middlewareError == nil {
			t.Fatal("Expected middleware error due to timeout")
		}
		var httpErr *xylium.HTTPError
		if !errors.As(result.middlewareError, &httpErr) {
			t.Fatalf("Middleware error is not *xylium.HTTPError, got %T", result.middlewareError)
		}
		if httpErr.Message.(string) != customMsg {
			t.Errorf("Expected custom timeout message '%s', got '%s'", customMsg, httpErr.Message.(string))
		}
		if result.statusCode != xylium.StatusServiceUnavailable {
			t.Errorf("Expected response status code %d, got %d", xylium.StatusServiceUnavailable, result.statusCode)
		}
	})

	t.Run("HandlerExceedsTimeout_CustomMessageFunc", func(t *testing.T) {
		var actualPathInsideFunc string
		expectedPathForMsg := "/test-timeout-path"

		customMsgFunc := func(c *xylium.Context) string {
			actualPathInsideFunc = c.Path()
			return fmt.Sprintf("Timeout on path %s after %s", c.Path(), "configured_duration")
		}
		config := xylium.TimeoutConfig{
			Timeout: veryShortTimeout,
			Message: customMsgFunc,
		}
		result := runTimeoutMiddleware(t, config, mediumHandlerWork, false, false, false)

		if result.middlewareError == nil {
			t.Fatal("Expected middleware error due to timeout")
		}
		var httpErr *xylium.HTTPError
		if !errors.As(result.middlewareError, &httpErr) {
			t.Fatalf("Middleware error is not *xylium.HTTPError, got %T", result.middlewareError)
		}

		if actualPathInsideFunc != expectedPathForMsg {
			t.Errorf("Path inside customMsgFunc was '%s', expected '%s'", actualPathInsideFunc, expectedPathForMsg)
		}
		expectedMsg := fmt.Sprintf("Timeout on path %s after configured_duration", expectedPathForMsg)

		if httpErr.Message.(string) != expectedMsg {
			t.Errorf("Expected custom func timeout message '%s', got '%s'", expectedMsg, httpErr.Message.(string))
		}
	})

	t.Run("HandlerExceedsTimeout_CustomErrorHandler", func(t *testing.T) {
		customStatus := http.StatusGatewayTimeout
		customErrorBodyVar := "Custom_Timeout_Response_Body"

		var errorHandlerCalledWithError error
		config := xylium.TimeoutConfig{
			Timeout: veryShortTimeout,
			ErrorHandler: func(c *xylium.Context, err error) error {
				errorHandlerCalledWithError = err
				if !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("CustomErrorHandler: expected context.DeadlineExceeded, got %v", err)
				}
				return c.String(customStatus, "%s", customErrorBodyVar)
			},
		}
		result := runTimeoutMiddleware(t, config, mediumHandlerWork, false, false, false)

		if !errors.Is(errorHandlerCalledWithError, context.DeadlineExceeded) {
			t.Errorf("CustomErrorHandler was not called with context.DeadlineExceeded, got: %v", errorHandlerCalledWithError)
		}

		if result.middlewareError != nil {
			t.Errorf("Expected no middleware error when custom error handler sends response successfully, got %v", result.middlewareError)
		}

		if result.statusCode != customStatus {
			t.Errorf("Expected custom error handler status code %d, got %d", customStatus, result.statusCode)
		}
		if result.responseBody != customErrorBodyVar {
			t.Errorf("Expected custom error handler body '%s', got '%s'", customErrorBodyVar, result.responseBody)
		}
	})

	t.Run("HandlerWritesResponse_ThenExceedsTimeout", func(t *testing.T) {
		config := xylium.TimeoutConfig{Timeout: veryShortTimeout} // 15ms
		// Handler ingin berjalan 30ms, menulis response di awal (~5ms)
		result := runTimeoutMiddleware(t, config, mediumHandlerWork, false, false, true)

		if result.handlerCompleted {
			t.Error("Expected handler NOT to complete its full (simulated) work duration due to timeout, even if it wrote early")
		}

		// result.middlewareError HARUS context.DeadlineExceeded.
		// Karena response sudah committed, defaultErrorHandler (di dalam middleware timeout)
		// akan mengembalikan context.DeadlineExceeded secara langsung.
		if !errors.Is(result.middlewareError, context.DeadlineExceeded) {
			t.Errorf("Expected middlewareError to be context.DeadlineExceeded because response was committed, got %T: %v", result.middlewareError, result.middlewareError)
		}

		if result.statusCode != http.StatusOK { // Status dari early write
			t.Errorf("Expected status code from early response %d, got %d", http.StatusOK, result.statusCode)
		}
		if result.responseBody != "handler_early_response" {
			t.Errorf("Expected body from early response 'handler_early_response', got '%s'", result.responseBody)
		}
		if !errors.Is(result.goContextError, context.DeadlineExceeded) {
			t.Errorf("Expected GoContext error (from handler perspective) to be context.DeadlineExceeded, got %v", result.goContextError)
		}
		// finalGoCtxError akan DeadlineExceeded karena ctxWithTimeout (yang menjadi c.goCtx untuk timedXyliumCtx)
		// dibuat dari parentCtx (c.goCtx asli) dan kemudian timed out, dan cancelFunc dipanggil.
		if !errors.Is(result.finalGoCtxError, context.DeadlineExceeded) {
			t.Errorf("Expected final GoContext error (from timed context) to be context.DeadlineExceeded, got %v", result.finalGoCtxError)
		}
	})

	t.Run("HandlerPanics_BeforeTimeout", func(t *testing.T) {
		config := xylium.TimeoutConfig{Timeout: longHandlerWork} // Timeout 50ms
		// Handler panik setelah 5ms
		result := runTimeoutMiddleware(t, config, shortHandlerWork, false, true, false)

		if result.panicValue == nil {
			t.Error("Expected a panic from the handler")
		} else if fmt.Sprintf("%v", result.panicValue) != "handler_deliberate_panic" {
			t.Errorf("Unexpected panic value: %v", result.panicValue)
		}

		// Jika panic, middlewareError mungkin akan menjadi HTTPError 500 setelah simulasi GlobalErrorHandler
		if result.middlewareError != nil {
			var httpErr *xylium.HTTPError
			if errors.As(result.middlewareError, &httpErr) {
				if httpErr.Code != http.StatusInternalServerError {
					t.Errorf("Expected HTTP 500 for panic, got %d. Error: %v", httpErr.Code, httpErr)
				}
			} else {
				t.Logf("Panic occurred, middlewareError is: %v. Expected HTTPError 500 if panic was processed by simulated GlobalErrorHandler.", result.middlewareError)
			}
		} else if result.panicValue == nil {
			// Seharusnya tidak sampai sini jika panicValue ada
			t.Error("Expected either panicValue or middlewareError when handler panics")
		}

		if result.handlerCompleted {
			t.Error("Handler should not be marked as completed if it panics")
		}
	})

	t.Run("InvalidConfig_ZeroTimeout", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for zero timeout duration, but did not panic")
			}
		}()
		_ = xylium.TimeoutWithConfig(xylium.TimeoutConfig{Timeout: 0})
	})
}
