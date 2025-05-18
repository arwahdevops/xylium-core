// src/xylium/middleware_timeout.go
package xylium

import (
	"context" // For context.WithTimeout and context.Done().
	"fmt"
	// "net/http" // Not used.
	"time" // For time.Duration.
)

// TimeoutConfig defines the configuration for the request Timeout middleware.
type TimeoutConfig struct {
	// Timeout is the maximum duration allowed for processing a request by subsequent handlers.
	// If this duration is exceeded, the context is canceled, and an error response is sent.
	// Must be greater than 0.
	Timeout time.Duration

	// Message is the message sent to the client when a timeout occurs.
	// Can be a string or a function: func(c *Context) string.
	// If nil or empty string, a default timeout message is used.
	Message interface{}

	// ErrorHandler is a custom function to handle the timeout event.
	// It receives the context and the timeout error (typically context.DeadlineExceeded).
	// If nil, a default handler sends a StatusServiceUnavailable (503) response.
	// The ErrorHandler is responsible for sending the client response.
	ErrorHandler func(c *Context, err error) error
}

// ContextKeyOriginalUserValue is used to save and restore fasthttp's original UserValue
// if it was a context.Context, to avoid interference with other fasthttp components
// or middleware that might also use UserValue for context propagation.
// Using a specific key for Xylium.
const ContextKeyOriginalUserValueForTimeout = "xylium_timeout_original_parent_context"

// Timeout returns a middleware that cancels the request context if processing exceeds the specified duration.
func Timeout(timeout time.Duration) Middleware {
	return TimeoutWithConfig(TimeoutConfig{
		Timeout: timeout,
		// Message and ErrorHandler will use defaults set in TimeoutWithConfig.
	})
}

// TimeoutWithConfig returns a Timeout middleware with the provided custom configuration.
func TimeoutWithConfig(config TimeoutConfig) Middleware {
	// Validate mandatory configuration.
	if config.Timeout <= 0 {
		panic("xylium: Timeout middleware 'Timeout' duration must be greater than 0")
	}

	// Define the default error handler if none is provided.
	// This handler is invoked when a request times out.
	defaultErrorHandler := func(c *Context, err error) error {
		logger := c.Logger() // Get request-scoped logger.
		timeoutDuration := config.Timeout // For the log message.

		// Construct the client-facing error message.
		var clientErrorMessage string
		switch msg := config.Message.(type) {
		case string:
			if msg == "" {
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			} else {
				clientErrorMessage = msg
			}
		case func(c *Context) string: // If Message is a function, call it.
			if msg != nil {
				clientErrorMessage = msg(c)
			} else { // Fallback if function is nil.
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			}
		default: // Fallback for other types or nil Message.
			clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
		}

		logger.Warnf("Timeout: Request %s %s timed out after %v. Responding with 503. Original error: %v",
			c.Method(), c.Path(), timeoutDuration, err) // Log with WARN level.

		// Return an HTTPError. GlobalErrorHandler will handle sending the response.
		// The original context error (e.g., context.DeadlineExceeded) is included as internal.
		return NewHTTPError(StatusServiceUnavailable, clientErrorMessage).WithInternal(err)
	}

	// Use the user-provided ErrorHandler if available, otherwise use the default.
	handlerToUse := config.ErrorHandler
	if handlerToUse == nil {
		handlerToUse = defaultErrorHandler
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger() // Get request-scoped logger.

			// Create a new context with a timeout, derived from the request's original context.
			// fasthttp.RequestCtx.UserValue can be used for context propagation.
			// We need to be careful if other middleware also use UserValue("parent_context").
			var parentCtx context.Context
			if uv := c.Ctx.UserValue("parent_context"); uv != nil { // A common key for parent context
				if pCtx, ok := uv.(context.Context); ok {
					parentCtx = pCtx
				}
			}
			if parentCtx == nil { // If no parent context found or not of expected type
				parentCtx = context.Background() // Fallback to background context.
				logger.Debugf("Timeout: No 'parent_context' found in UserValue for %s %s. Using context.Background().",
					c.Method(), c.Path())
			}

			// Create the cancellable context with the specified timeout.
			ctxWithTimeout, cancel := context.WithTimeout(parentCtx, config.Timeout)
			defer cancel() // IMPORTANT: Always call cancel to release resources.

			// Make this ctxWithTimeout available to downstream handlers if they are context-aware.
			// Store the original UserValue to restore it later.
			originalUserValue := c.Ctx.UserValue("parent_context")
			c.Ctx.SetUserValue("parent_context", ctxWithTimeout) // Expose the timed context.
			// Restore the original UserValue after this middleware's scope.
			defer c.Ctx.SetUserValue("parent_context", originalUserValue)


			// Channels to signal completion or panic from the 'next' handler.
			done := make(chan error, 1)       // Buffered channel for the error from next(c).
			panicChan := make(chan interface{}, 1) // Buffered channel for panics from next(c).

			// Execute the next handler in a separate goroutine.
			go func() {
				defer func() {
					// Recover from panics within the 'next' handler's goroutine.
					if p := recover(); p != nil {
						panicChan <- p // Send panic to the panicChan.
					}
				}()
				// Execute the next handler and send its returned error (or nil) to done.
				done <- next(c)
			}()

			// Wait for one of three outcomes:
			// 1. The 'next' handler completes (sends to 'done').
			// 2. The 'next' handler panics (sends to 'panicChan').
			// 3. The timeout context (ctxWithTimeout) is done (timeout occurs).
			select {
			case err := <-done:
				// Handler completed normally (err might be nil).
				return err

			case p := <-panicChan:
				// Handler panicked. Re-panic in the main middleware goroutine
				// so Xylium's central panic handler can catch it.
				logger.Errorf("Timeout: Panic recovered from downstream handler for %s %s. Re-panicking. Panic: %v",
					c.Method(), c.Path(), p)
				panic(p)

			case <-ctxWithTimeout.Done():
				// Timeout occurred.
				// ctxWithTimeout.Err() will be context.DeadlineExceeded.

				// Check if the response has already been committed by the 'next' handler.
				// If so, we can't send a new timeout error response.
				if c.ResponseCommitted() {
					logger.Warnf(
						"Timeout: Request %s %s timed out after %v, but response was already committed. No timeout response sent. Original error: %v",
						c.Method(), c.Path(), config.Timeout, ctxWithTimeout.Err(),
					)
					// Return the context error; Xylium's Router.Handler defer will log it if response already committed.
					return ctxWithTimeout.Err()
				}

				// Response not committed, so invoke the timeout error handler.
				// The handlerToUse (either default or custom) is responsible for sending the client response.
				return handlerToUse(c, ctxWithTimeout.Err())
			}
		}
	}
}
