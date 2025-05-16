package xylium

import (
	"errors" // For errors.As
	"fmt"    // For error formatting
	"log"    // For fallback logger implementation
	"os"     // For fallback logger implementation
)

// defaultGlobalErrorHandler is the default handler for errors returned by the route handlers or middleware.
func defaultGlobalErrorHandler(c *Context) error {
	errVal, _ := c.Get("handler_error_cause")
	originalErr, isError := errVal.(error)

	// Determine logger to use
	var currentLogger Logger = log.New(os.Stderr, "[DefaultGlobalErrFallback] ", log.LstdFlags) // Fallback
	if c.router != nil && c.router.Logger() != nil {
		currentLogger = c.router.Logger() // Use configured router logger
	}

	if !isError || originalErr == nil {
		currentLogger.Printf("WARNING: defaultGlobalErrorHandler called without a valid 'handler_error_cause' in context for %s %s.", c.Method(), c.Path())
		if !c.ResponseCommitted() {
			return c.JSON(StatusInternalServerError, map[string]string{"error": "An unexpected error occurred."})
		}
		return nil
	}

	var httpErr *HTTPError
	if errors.As(originalErr, &httpErr) { // Check if it's an HTTPError
		// Log with more detail if internal error is present and different
		if httpErr.Internal != nil && httpErr.Internal.Error() != fmt.Sprintf("%v", httpErr.Message) {
			currentLogger.Printf("Error: %v (Internal: %v) for %s %s", httpErr.Message, httpErr.Internal, c.Method(), c.Path())
		} else {
			currentLogger.Printf("Error: %v for %s %s", httpErr.Message, c.Method(), c.Path())
		}
		// Prepare response message
		var responseMessage interface{} = map[string]string{"error": StatusText(httpErr.Code)} // Default
		if strMsg, ok := httpErr.Message.(string); ok && strMsg != "" {
			responseMessage = map[string]string{"error": strMsg}
		} else if httpErr.Message != nil { // If Message is a struct/map
			responseMessage = httpErr.Message
		}
		return c.JSON(httpErr.Code, responseMessage)
	}

	// For generic errors, log and return a 500
	currentLogger.Printf("Internal Server Error: %v for %s %s", originalErr, c.Method(), c.Path())
	return c.JSON(StatusInternalServerError, map[string]string{"error": StatusText(StatusInternalServerError)})
}

// defaultPanicHandler is the default handler for recovered panics.
func defaultPanicHandler(c *Context) error {
	panicInfo, _ := c.Get("panic_recovery_info")
	recoveredErr := fmt.Errorf("panic recovery: %v", panicInfo)

	var currentLogger Logger = log.New(os.Stderr, "[DefaultPanicHdlrFallback] ", log.LstdFlags) // Fallback
	if c.router != nil && c.router.Logger() != nil {
		currentLogger = c.router.Logger()
	}
	currentLogger.Printf("DefaultPanicHandler: %v for %s %s", recoveredErr, c.Method(), c.Path())

	// Return an HTTPError which will then be handled by GlobalErrorHandler
	return NewHTTPError(StatusInternalServerError, "An unexpected server error occurred.").WithInternal(recoveredErr)
}

// defaultNotFoundHandler is the default handler for 404 Not Found errors.
func defaultNotFoundHandler(c *Context) error {
	return NewHTTPError(StatusNotFound, fmt.Sprintf("Resource %s not found", c.Path()))
}

// defaultMethodNotAllowedHandler is the default handler for 405 Method Not Allowed errors.
func defaultMethodNotAllowedHandler(c *Context) error {
	return NewHTTPError(StatusMethodNotAllowed, fmt.Sprintf("Method %s not allowed for %s", c.Method(), c.Path()))
}
