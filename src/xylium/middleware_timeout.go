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
// handler chain within that context, leveraging the integrated Go context in xylium.Context.
func TimeoutWithConfig(config TimeoutConfig) Middleware {
	// Validate mandatory configuration: Timeout duration must be positive.
	if config.Timeout <= 0 {
		panic("xylium: Timeout middleware 'Timeout' duration must be greater than 0")
	}

	// Define the default error handler if no custom ErrorHandler is provided.
	// This handler is invoked when a request times out.
	defaultErrorHandler := func(c *Context, err error) error {
		// `err` here is typically `context.DeadlineExceeded`.
		logger := c.Logger().WithFields(M{"middleware": "Timeout"})
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
				clientErrorMessage = msg(c)
			} else {
				clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
			}
		default:
			clientErrorMessage = fmt.Sprintf("Request processing timed out after %v.", timeoutDuration)
		}

		logger.Warnf("Request %s %s timed out after %v. Responding with 503 Service Unavailable. Original context error: %v",
			c.Method(), c.Path(), timeoutDuration, err)

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
			logger := c.Logger().WithFields(M{"middleware": "Timeout"})

			// --- Context Propagation and Timeout Setup ---
			// Get the current Go context.Context from the Xylium context.
			// This parentCtx might have been set by previous middleware or initialized in acquireCtx.
			parentCtx := c.GoContext() // Use the integrated Go context.

			// Create the cancellable Go context with the specified timeout, derived from parentCtx.
			ctxWithTimeout, cancelFunc := context.WithTimeout(parentCtx, config.Timeout)
			defer cancelFunc() // IMPORTANT: Always call cancelFunc to release resources associated with ctxWithTimeout.

			// Create a new Xylium.Context instance (shallow copy) that holds this new ctxWithTimeout.
			// This ensures that if `next` or any subsequent handler calls `c.GoContext()`,
			// they will receive the context that includes this timeout.
			timedXyliumCtx := c.WithGoContext(ctxWithTimeout)

			// --- Asynchronous Handler Execution with Timeout Monitoring ---
			done := make(chan error, 1)            // Buffered channel for the error (or nil) from `next(c)`.
			panicChan := make(chan interface{}, 1) // Buffered channel for panics recovered from `next(c)`.

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
				}()
				// Execute `next(timedXyliumCtx)`: pass the Xylium context that has the timed Go context.
				done <- next(timedXyliumCtx)
			}()

			// Wait for one of three outcomes:
			select {
			case errFromHandler := <-done:
				// Handler completed normally (or with an error from downstream).
				return errFromHandler

			case panicVal := <-panicChan:
				// Handler panicked. Re-panic so Xylium's central panic handler can catch it.
				// Log using the method/path from timedXyliumCtx for context.
				logger.Errorf("Panic recovered from downstream handler for %s %s. Re-panicking. Panic: %v",
					timedXyliumCtx.Method(), timedXyliumCtx.Path(), panicVal)
				panic(panicVal)

			case <-ctxWithTimeout.Done(): // This is the Go context's Done channel.
				// Timeout occurred. ctxWithTimeout.Err() will be context.DeadlineExceeded.
				timeoutError := ctxWithTimeout.Err()

				// Check if the response has already been sent by the (now timed-out) handler.
				// Use timedXyliumCtx for this check, as it reflects the state during the handler's execution.
				if timedXyliumCtx.ResponseCommitted() {
					logger.Warnf(
						"Request %s %s timed out after %v, but response was already committed. Cannot send timeout response. Original context error: %v",
						timedXyliumCtx.Method(), timedXyliumCtx.Path(), config.Timeout, timeoutError,
					)
					return timeoutError // Return the context error; response already sent.
				}

				// Response not committed. Invoke the configured timeout error handler.
				// Pass the original Xylium context `c` to the error handler.
				// The error handler typically doesn't need the timed Go context; it just needs
				// to know a timeout occurred (via `timeoutError`) and respond using `c`.
				return handlerToUse(c, timeoutError)
			}
		}
	}
}
