package xylium

import (
	"context" // For context.WithTimeout and context.Done().
	"fmt"     // For string formatting.
	"time"    // For time.Duration.
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
	// It receives the `xylium.Context` and the timeout error (typically `context.DeadlineExceeded`).
	// If nil, a default handler sends an HTTP 503 Service Unavailable response.
	// The ErrorHandler is responsible for formulating and sending the complete client response.
	// It should return an error if it fails to handle the timeout, though typically it returns nil
	// after sending the response.
	ErrorHandler func(c *Context, err error) error
}

// ContextKeyOriginalUserValueForTimeout is a specific key Xylium uses to temporarily
// store and restore fasthttp's original `UserValue` if it was a `context.Context`.
// This is to avoid interference if `UserValue("parent_context")` is used by other
// fasthttp components or middleware for context propagation, ensuring Xylium's timeout
// context doesn't overwrite a context set by another part of the system unintentionally.
// Note: This constant is currently not used in the refactored code below, as the approach
// is to set "parent_context" and restore it. If more complex UserValue management is needed,
// this key could be employed. The current approach is simpler.
// const ContextKeyOriginalUserValueForTimeout = "xylium_timeout_original_parent_context"

// Timeout returns a middleware that cancels the request context if processing
// by subsequent handlers exceeds the specified `timeout` duration.
// Uses default message and error handling if a timeout occurs.
func Timeout(timeout time.Duration) Middleware {
	return TimeoutWithConfig(TimeoutConfig{
		Timeout: timeout,
		// Message and ErrorHandler will use defaults defined within TimeoutWithConfig.
	})
}

// TimeoutWithConfig returns a Timeout middleware with the provided custom configuration.
// It creates a cancellable context with the specified timeout and runs the subsequent
// handler chain within that context.
func TimeoutWithConfig(config TimeoutConfig) Middleware {
	// Validate mandatory configuration: Timeout duration must be positive.
	if config.Timeout <= 0 {
		panic("xylium: Timeout middleware 'Timeout' duration must be greater than 0")
	}

	// Define the default error handler if no custom ErrorHandler is provided.
	// This handler is invoked when a request times out.
	defaultErrorHandler := func(c *Context, err error) error {
		// `err` here is typically `context.DeadlineExceeded`.
		logger := c.Logger().WithFields(M{"middleware": "Timeout"}) // Get request-scoped, contextualized logger.
		timeoutDuration := config.Timeout                          // For logging and message.

		// Construct the client-facing error message.
		var clientErrorMessage string
		switch msg := config.Message.(type) {
		case string:
			if msg == "" { // If user provided empty string, use default.
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			} else {
				clientErrorMessage = msg
			}
		case func(c *Context) string: // If Message is a function, call it.
			if msg != nil {
				clientErrorMessage = msg(c)
			} else { // Fallback if function is nil (should ideally not happen).
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			}
		default: // Fallback for other types or nil Message.
			clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
		}

		logger.Warnf("Request %s %s timed out after %v. Responding with 503 Service Unavailable. Original context error: %v",
			c.Method(), c.Path(), timeoutDuration, err) // Log with WARN level.

		// Return an HTTPError. The GlobalErrorHandler will process this, sending a 503 status
		// and a JSON body (by default) with `clientErrorMessage`.
		// The original context error (e.g., `context.DeadlineExceeded`) is included as the internal cause.
		return NewHTTPError(StatusServiceUnavailable, clientErrorMessage).WithInternal(err)
	}

	// Use the user-provided ErrorHandler if available; otherwise, use the default one.
	handlerToUse := config.ErrorHandler
	if handlerToUse == nil {
		handlerToUse = defaultErrorHandler
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			logger := c.Logger().WithFields(M{"middleware": "Timeout"}) // Contextual logger.

			// --- Context Propagation and Timeout Setup ---
			// Create a new context with a timeout, derived from an appropriate parent context.
			// fasthttp.RequestCtx.UserValue is a common way to pass Go's `context.Context`
			// through fasthttp's handler chain if other parts of the system use it.
			var parentCtx context.Context
			// Check if a "parent_context" (a common key) already exists in UserValue.
			if uv := c.Ctx.UserValue("parent_context"); uv != nil {
				if pCtx, ok := uv.(context.Context); ok {
					parentCtx = pCtx // Use existing parent context.
				}
			}

			if parentCtx == nil {
				// If no parent context was found in UserValue or it wasn't a context.Context,
				// fallback to context.Background(). This ensures we always have a valid parent.
				parentCtx = context.Background()
				logger.Debugf("No 'parent_context' found in UserValue for %s %s, or it was not a Go context. Using context.Background() as parent for timeout context.",
					c.Method(), c.Path())
			}

			// Create the cancellable context with the specified timeout.
			ctxWithTimeout, cancel := context.WithTimeout(parentCtx, config.Timeout)
			defer cancel() // IMPORTANT: Always call cancel to release resources associated with ctxWithTimeout.

			// Make this ctxWithTimeout available to downstream handlers if they are context-aware.
			// This is done by setting it on fasthttp's UserValue.
			// We save the original UserValue for "parent_context" to restore it after this middleware.
			originalUserValue := c.Ctx.UserValue("parent_context")
			c.Ctx.SetUserValue("parent_context", ctxWithTimeout) // Expose the timed context.
			// Restore the original UserValue after this middleware's scope.
			defer c.Ctx.SetUserValue("parent_context", originalUserValue)


			// --- Asynchronous Handler Execution with Timeout Monitoring ---
			// Channels to signal completion or panic from the `next` handler.
			done := make(chan error, 1)       // Buffered channel for the error (or nil) from `next(c)`.
			panicChan := make(chan interface{}, 1) // Buffered channel for panics recovered from `next(c)`.

			// Execute the next handler (the rest of the middleware chain and the route handler)
			// in a separate goroutine. This allows the main middleware goroutine to monitor for timeouts.
			go func() {
				defer func() {
					// Recover from panics within the `next` handler's goroutine.
					if p := recover(); p != nil {
						panicChan <- p // Send panic information to the panicChan.
					}
				}()
				// Execute `next(c)` and send its returned error (or nil) to the `done` channel.
				done <- next(c)
			}()

			// Wait for one of three outcomes using `select`:
			// 1. The `next` handler completes successfully or returns an error (`<-done`).
			// 2. The `next` handler panics (`<-panicChan`).
			// 3. The timeout context (`ctxWithTimeout`) is done, indicating a timeout (`<-ctxWithTimeout.Done()`).
			select {
			case err := <-done:
				// Handler completed normally. `err` might be nil or an error from downstream.
				return err // Propagate the result.

			case p := <-panicChan:
				// Handler panicked. Re-panic in the main middleware goroutine
				// so Xylium's central panic handler (in Router.Handler) can catch and process it.
				logger.Errorf("Panic recovered from downstream handler for %s %s. Re-panicking. Panic: %v",
					c.Method(), c.Path(), p)
				panic(p) // This panic will be caught by Router.Handler's defer.

			case <-ctxWithTimeout.Done():
				// Timeout occurred. ctxWithTimeout.Err() will be context.DeadlineExceeded.
				timeoutError := ctxWithTimeout.Err()

				// Critical Check: Has the response already been sent by the (now timed-out) handler?
				// If so, we cannot send a new timeout error response. We can only log the event.
				if c.ResponseCommitted() {
					logger.Warnf(
						"Request %s %s timed out after %v, but response was already committed. Cannot send timeout response. Original context error: %v",
						c.Method(), c.Path(), config.Timeout, timeoutError,
					)
					// Return the context error. Router.Handler's defer might log this if it's unexpected
					// for a committed response to still have an error returned.
					return timeoutError
				}

				// Response has not been committed, so invoke the configured timeout error handler.
				// This handler (default or custom) is responsible for sending the client response (e.g., 503).
				return handlerToUse(c, timeoutError)
			}
		}
	}
}
