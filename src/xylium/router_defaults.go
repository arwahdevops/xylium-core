// src/xylium/router_defaults.go
package xylium

import (
	"errors" // For errors.As
	"fmt"    // For error formatting
	"log"    // For fallback logger implementation
	"os"     // For fallback logger implementation
)

// defaultGlobalErrorHandler is the default handler for errors returned by route handlers or middleware.
// Its behavior, especially the details in the client response, can vary based on Xylium's operating mode.
func defaultGlobalErrorHandler(c *Context) error {
	errVal, _ := c.Get("handler_error_cause") // Retrieve the original error from the context.
	originalErr, isError := errVal.(error)

	// Get the logger.
	// c.router should not be nil at this point if the context was properly initialized.
	var currentLogger Logger = log.New(os.Stderr, "[DefaultGlobalErrFallback] ", log.LstdFlags) // Fallback logger.
	if c.router != nil && c.router.Logger() != nil {
		currentLogger = c.router.Logger()
	}

	// Get the current operating mode from the router associated with the context.
	// Default to ReleaseMode if c.router is somehow nil (should not happen).
	currentMode := ReleaseMode
	if c.router != nil {
		currentMode = c.router.CurrentMode()
	}

	// Initialize variables for the response.
	httpStatusCode := StatusInternalServerError                            // Default to 500.
	var responseMessage interface{} = M{"error": StatusText(StatusInternalServerError)} // Default error message.

	if !isError || originalErr == nil {
		// This case should ideally not happen if error handling is consistent.
		currentLogger.Printf("WARNING: defaultGlobalErrorHandler called without a valid 'handler_error_cause' in context for %s %s. Mode: %s.",
			c.Method(), c.Path(), currentMode)
		responseMessage = M{"error": "An unexpected error occurred."}
		// httpStatusCode remains StatusInternalServerError.
	} else {
		var httpErr *HTTPError
		if errors.As(originalErr, &httpErr) { // If the error is an HTTPError.
			httpStatusCode = httpErr.Code

			// Determine the message for the client.
			if httpErr.Message != nil {
				responseMessage = httpErr.Message // This can be a string, map, or struct.
			} else {
				// If HTTPError.Message is nil, use the standard text for the status code.
				responseMessage = M{"error": StatusText(httpStatusCode)}
			}

			// Add internal error details to the response if in DebugMode and an internal error exists.
			if currentMode == DebugMode && httpErr.Internal != nil {
				debugDetails := M{"internal_error_details": httpErr.Internal.Error()}
				// Attempt to merge debugDetails with responseMessage if responseMessage is a map.
				if respMap, ok := responseMessage.(M); ok {
					respMap["debug_info"] = debugDetails // Use a distinct key for debug info
					responseMessage = respMap
				} else if respMap, ok := responseMessage.(map[string]interface{}); ok {
					respMap["debug_info"] = debugDetails
					responseMessage = respMap
				} else {
					// If responseMessage is not a map (e.g., a simple string), create a new map.
					responseMessage = M{
						"message":    responseMessage, // Original client-facing message
						"debug_info": debugDetails,
					}
				}
			}

			// Server-side logging.
			logEntry := fmt.Sprintf("HTTPError: Status=%d, Message=%v", httpStatusCode, httpErr.Message)
			if httpErr.Internal != nil {
				logEntry += fmt.Sprintf(", Internal=%v", httpErr.Internal)
			}
			currentLogger.Printf("%s for %s %s. Mode: %s.", logEntry, c.Method(), c.Path(), currentMode)

		} else { // If the error is a generic Go error.
			// httpStatusCode remains StatusInternalServerError.
			responseMessage = M{"error": StatusText(StatusInternalServerError)}

			if currentMode == DebugMode {
				// Add original error details to the response if in DebugMode.
				responseMessage = M{
					"error":      StatusText(StatusInternalServerError),
					"debug_info": M{"internal_error_details": originalErr.Error()},
				}
			}
			currentLogger.Printf("Generic Error: %v for %s %s. Mode: %s. Responding with 500.",
				originalErr, c.Method(), c.Path(), currentMode)
		}
	}

	// Send the JSON response.
	// c.JSON will set the Content-Type header.
	return c.JSON(httpStatusCode, responseMessage)
}

// defaultPanicHandler is the default handler for recovered panics.
// It logs the panic and then returns an HTTPError, which will be further processed by the GlobalErrorHandler.
func defaultPanicHandler(c *Context) error {
	panicInfo, _ := c.Get("panic_recovery_info")         // Retrieve panic information from context.
	recoveredErr := fmt.Errorf("panic recovery: %v", panicInfo) // The original error from panic.

	// Get logger and current mode.
	var currentLogger Logger = log.New(os.Stderr, "[DefaultPanicHdlrFallback] ", log.LstdFlags)
	if c.router != nil && c.router.Logger() != nil {
		currentLogger = c.router.Logger()
	}
	currentMode := ReleaseMode
	if c.router != nil {
		currentMode = c.router.CurrentMode()
	}

	// The stack trace is already logged by Router.Handler.
	// Here, we log that a panic was recovered by this specific handler.
	currentLogger.Printf("DefaultPanicHandler: Recovered from panic for %s %s. Info: %v. Mode: %s.",
		c.Method(), c.Path(), panicInfo, currentMode)

	// Client-facing message for panic situations.
	clientMessage := "An unexpected server error occurred."

	// Construct an HTTPError. The GlobalErrorHandler will then decide
	// how much detail to show to the client based on the operating mode,
	// using the `recoveredErr` as the internal cause.
	return NewHTTPError(StatusInternalServerError, clientMessage).WithInternal(recoveredErr)
}

// defaultNotFoundHandler is the default handler for 404 Not Found errors.
// Its behavior typically does not depend on the operating mode.
func defaultNotFoundHandler(c *Context) error {
	return NewHTTPError(StatusNotFound, fmt.Sprintf("Resource %s not found", c.Path()))
}

// defaultMethodNotAllowedHandler is the default handler for 405 Method Not Allowed errors.
// Its behavior typically does not depend on the operating mode.
func defaultMethodNotAllowedHandler(c *Context) error {
	return NewHTTPError(StatusMethodNotAllowed, fmt.Sprintf("Method %s not allowed for %s", c.Method(), c.Path()))
}
