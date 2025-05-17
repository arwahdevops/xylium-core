package xylium

import (
	"context"
	"fmt"
	// "net/http" // PERBAIKAN: Impor ini tidak digunakan
	"time"
)

// TimeoutConfig mendefinisikan konfigurasi untuk middleware Timeout.
type TimeoutConfig struct {
	Timeout      time.Duration
	Message      interface{}
	ErrorHandler func(c *Context, err error) error
}

const ContextKeyOriginalContext = "xylium_original_context_for_timeout"

// Timeout mengembalikan middleware yang akan membatalkan request jika melebihi durasi yang ditentukan.
func Timeout(timeout time.Duration) Middleware {
	return TimeoutWithConfig(TimeoutConfig{
		Timeout: timeout,
	})
}

// TimeoutWithConfig mengembalikan middleware Timeout dengan konfigurasi yang diberikan.
func TimeoutWithConfig(config TimeoutConfig) Middleware {
	if config.Timeout <= 0 {
		panic("xylium: Timeout middleware duration must be greater than 0")
	}

	defaultErrorHandler := func(c *Context, err error) error {
		var errMsg string
		// originalCtxValue, _ := c.Get(ContextKeyOriginalContext) // Tidak digunakan di default handler ini

		switch msg := config.Message.(type) {
		case string:
			if msg == "" {
				errMsg = fmt.Sprintf("Request timed out after %v", config.Timeout)
			} else {
				errMsg = msg
			}
		case func(c *Context) string:
			if msg != nil {
				errMsg = msg(c) // Panggil dengan context saat ini
			} else {
				errMsg = fmt.Sprintf("Request timed out after %v", config.Timeout)
			}
		default:
			errMsg = fmt.Sprintf("Request timed out after %v", config.Timeout)
		}
		return NewHTTPError(StatusServiceUnavailable, errMsg).WithInternal(err) // Menggunakan konstanta status Xylium
	}

	handlerToUse := defaultErrorHandler
	if config.ErrorHandler != nil {
		handlerToUse = config.ErrorHandler
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Ambil parent context dari UserValue jika ada, atau buat background jika tidak.
			// Ini penting agar pembatalan dari request HTTP upstream (jika ada) bisa terpropagasi.
			// Jika Xylium digunakan sebagai server HTTP root, parentCtx bisa context.Background().
			// Jika Xylium di-embed, parent context bisa datang dari server luar.
			// Untuk saat ini, asumsikan c.Ctx.UserValue("parent_context") sudah diset dengan benar
			// oleh fasthttp atau middleware sebelumnya (jika ada).
			// Jika tidak, kita default ke context.Background().
			var parentCtx context.Context
			userValParentCtx := c.Ctx.UserValue("parent_context")
			if pCtx, ok := userValParentCtx.(context.Context); ok && pCtx != nil {
				parentCtx = pCtx
			} else {
				parentCtx = context.Background() // Fallback jika tidak ada parent context yang valid
			}


			ctx, cancel := context.WithTimeout(parentCtx, config.Timeout)
			defer cancel()

			originalUserCtx := c.Ctx.UserValue("parent_context")
			c.Ctx.SetUserValue("parent_context", ctx)
			defer c.Ctx.SetUserValue("parent_context", originalUserCtx)

			done := make(chan error, 1)
			panicChan := make(chan interface{}, 1)

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
				}()
				done <- next(c)
			}()

			select {
			case err := <-done:
				return err
			case p := <-panicChan:
				panic(p)
			case <-ctx.Done():
				if c.ResponseCommitted() {
					if c.router != nil && c.router.Logger() != nil { // Cek nil untuk router dan logger
						c.router.Logger().Printf("Timeout occurred but response already committed for %s %s", c.Method(), c.Path())
					}
					return ctx.Err()
				}
				return handlerToUse(c, ctx.Err())
			}
		}
	}
}
