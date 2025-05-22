// src/xylium/middleware_timeout.go
package xylium

import (
	"context"
	"fmt"
	"time" // Diperlukan untuk time.Duration dan time.After
)

// TimeoutConfig defines the configuration for the request Timeout middleware.
// This middleware sets a maximum duration for processing a request by subsequent
// handlers in the chain. If this duration is exceeded, the request context is canceled,
// and an error response (typically HTTP 503 Service Unavailable) is sent.
type TimeoutConfig struct {
	// Timeout is the maximum duration allowed for processing a request.
	// This duration starts when the timeout middleware begins processing.
	// If processing by `next(c)` and subsequent handlers exceeds this, a timeout occurs.
	// Must be a positive duration (e.g., `5 * time.Second`).
	Timeout time.Duration

	// Message is the message sent to the client when a timeout occurs.
	// - If a `string`: this string is used as the error message.
	// - If a `func(c *Context) string`: this function is called to generate the message.
	// - If nil or an empty string (for string type): a default timeout message is used,
	//   indicating the timeout duration.
	Message interface{}

	// ErrorHandler is a custom function to handle the timeout event.
	// It receives the `xylium.Context` (konteks asli, bukan yang di-timeout)
	// and the timeout error (typically `context.DeadlineExceeded`).
	// If nil, a default handler sends an HTTP 503 Service Unavailable response.
	// The ErrorHandler is responsible for formulating and sending the complete client response.
	// It should return an error if it fails to handle the timeout, though typically it returns nil
	// after sending the response.
	ErrorHandler func(c *Context, err error) error
}

// Timeout returns a middleware that cancels the request context if processing
// by subsequent handlers exceeds the specified `timeout` duration.
// Uses default message and error handling if a timeout occurs.
func Timeout(timeout time.Duration) Middleware {
	return TimeoutWithConfig(TimeoutConfig{
		Timeout: timeout,
	})
}

// TimeoutWithConfig returns a Timeout middleware with the provided custom configuration.
func TimeoutWithConfig(config TimeoutConfig) Middleware {
	if config.Timeout <= 0 {
		panic("xylium: Timeout middleware 'Timeout' duration must be greater than 0")
	}

	defaultErrorHandler := func(c *Context, err error) error {
		logger := c.Logger().WithFields(M{"middleware": "Timeout", "handler": "defaultErrorHandler"})
		timeoutDuration := config.Timeout
		var clientErrorMessage string
		switch msg := config.Message.(type) {
		case string:
			if msg == "" {
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			} else {
				clientErrorMessage = msg
			}
		case func(c *Context) string:
			if msg != nil {
				clientErrorMessage = msg(c) // Gunakan context 'c' yang asli
			} else {
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			}
		default:
			clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
		}

		// PENTING: Periksa apakah response sudah dikirim SEBELUM mencoba mengirim response error baru.
		// Gunakan context 'c' yang asli untuk pemeriksaan ResponseCommitted, karena ini merefleksikan
		// state akhir dari response yang mungkin sudah dikirim oleh handler yang di-timeout.
		if c.ResponseCommitted() {
			logger.Warnf(
				"Request %s %s timed out after %v, but response was already committed by a downstream handler. Cannot send new error response. Original context error: %v",
				c.Method(), c.Path(), timeoutDuration, err,
			)
			// Kembalikan error asli (timeoutError) agar bisa dicatat oleh error handler global Xylium,
			// tapi response ke klien tidak akan berubah.
			return err
		}

		logger.Warnf("Request %s %s timed out after %v. Responding with %d. Original context error: %v",
			c.Method(), c.Path(), timeoutDuration, StatusServiceUnavailable, err)
		return NewHTTPError(StatusServiceUnavailable, clientErrorMessage).WithInternal(err)
	}

	errorHandlerToUse := config.ErrorHandler
	if errorHandlerToUse == nil {
		errorHandlerToUse = defaultErrorHandler
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error { // 'c' di sini adalah context asli dari router
			logger := c.Logger().WithFields(M{"middleware": "Timeout"})

			parentCtx := c.GoContext() // Go context dari 'c'
			ctxWithTimeout, cancelFunc := context.WithTimeout(parentCtx, config.Timeout)
			defer cancelFunc() // Pastikan cancel selalu dipanggil

			// timedXyliumCtx adalah context Xylium yang membawa Go context yang di-timeout
			timedXyliumCtx := c.WithGoContext(ctxWithTimeout)

			resultChan := make(chan error, 1)
			panicValChan := make(chan interface{}, 1) // Menggunakan nama yang berbeda untuk kejelasan

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicValChan <- p // Kirim panic jika ada
						// Tidak perlu close panicValChan di sini jika panic, biarkan select utama yang menghabiskan
						return // Keluar setelah mengirim panic
					}
					// Jika tidak ada panic, tutup panicValChan untuk menandakan tidak ada panic
					close(panicValChan)
				}()
				// Jalankan handler next dengan timedXyliumCtx
				resultChan <- next(timedXyliumCtx)
				// Tutup resultChan setelah mengirimkan hasil (error atau nil)
				close(resultChan)
			}()

			select {
			case errFromHandler, resultChanOk := <-resultChan:
				// Handler selesai atau resultChan ditutup.
				// `resultChanOk` adalah false jika channel ditutup.

				// Periksa dulu apakah ada panic yang mungkin terjadi.
				// Ini dibaca dari panicValChan.
				pVal, panicChanOk := <-panicValChan
				if panicChanOk && pVal != nil { // Ada panic yang tertangkap dan channel belum ditutup
					logger.Errorf("Panic (handler finished/errored then panic detected): %v for %s %s. Re-panicking.",
						pVal, timedXyliumCtx.Method(), timedXyliumCtx.Path())
					panic(pVal)
				}
				// Jika panicChanOk false, berarti channel sudah ditutup (tidak ada panic).

				// Jika kita sampai di sini, tidak ada panic yang diprioritaskan.
				// Sekarang, periksa apakah timeout dari middleware juga terjadi
				// (penting jika handler selesai TEPAT saat timeout).
				select {
				case <-ctxWithTimeout.Done(): // Timeout menang!
					timeoutError := ctxWithTimeout.Err()
					// PENTING: Saat memanggil ErrorHandler, kita gunakan 'c' (context asli)
					// agar ErrorHandler bisa menggunakan c.ResponseCommitted() yang merefleksikan
					// state response dari handler 'next' yang berjalan dengan 'timedXyliumCtx'.
					// Namun, untuk kejelasan, errorHandlerToUse sudah memeriksa c.ResponseCommitted().
					return errorHandlerToUse(c, timeoutError)
				default:
					// Timeout belum terjadi (atau belum terdeteksi di sini).
					// Kembalikan hasil dari handler.
					if !resultChanOk && errFromHandler == nil { // Channel resultChan ditutup dan tidak ada error.
						return nil
					}
					return errFromHandler // Bisa error dari handler, atau nil.
				}

			case pVal, panicChanOk := <-panicValChan:
				// Panic terjadi SEBELUM resultChan memberi sinyal, atau panicChan ditutup.
				if panicChanOk && pVal != nil { // Ada panic yang tertangkap
					logger.Errorf("Panic (detected directly via panicChan): %v for %s %s. Re-panicking.",
						pVal, timedXyliumCtx.Method(), timedXyliumCtx.Path())
					panic(pVal)
				}
				// Jika panicChan ditutup tanpa nilai (ok false), berarti tidak ada panic.
				// Kita harus menunggu hasil dari resultChan.
				errFromHandlerAfterEmptyPanic, resultChanStillOk := <-resultChan
				if !resultChanStillOk && errFromHandlerAfterEmptyPanic == nil { // resultChan juga ditutup & nil.
					return nil
				}
				// Jika sampai sini, berarti panicChan ditutup (tidak ada panic), dan resultChan memberi hasil.
				// Cek timeout lagi untuk kasus handler selesai bersamaan dengan timeout.
				select {
				case <-ctxWithTimeout.Done():
					timeoutError := ctxWithTimeout.Err()
					return errorHandlerToUse(c, timeoutError)
				default:
					return errFromHandlerAfterEmptyPanic
				}

			case <-ctxWithTimeout.Done(): // Timeout terpicu SEBELUM handler selesai atau panik.
				timeoutError := ctxWithTimeout.Err()

				// Beri kesempatan terakhir untuk panicValChan jika ada, karena bisa saja panic
				// terjadi sangat dekat dengan timeout dan panicValChan belum terbaca.
				select {
				case pVal, pOk := <-panicValChan:
					if pOk && pVal != nil {
						logger.Errorf("Handler panicked around the time of context timeout (timeout won primary select, panic checked after): %v for %s %s. Prioritizing panic.",
							pVal, timedXyliumCtx.Method(), timedXyliumCtx.Path())
						panic(pVal)
					}
				default:
					// Tidak ada panic atau panicValChan belum siap/ditutup.
				}

				// Panggil ErrorHandler. ErrorHandler (default atau custom)
				// akan memeriksa c.ResponseCommitted() dari context asli 'c'.
				return errorHandlerToUse(c, timeoutError)
			}
		}
	}
}
