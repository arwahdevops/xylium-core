// src/xylium/router_defaults.go
package xylium

import (
	"errors"  // For errors.As, used to check if an error is of type *HTTPError.
	"fmt"     // For string formatting in error messages and log outputs.
	"strings" // For string manipulation, like splitting the "Allow" header.
)

// defaultGlobalErrorHandler is the default central error handler for Xylium.
// It processes errors returned by route handlers or middleware.
// The error details provided to the client can vary based on Xylium's operating mode (DebugMode vs. ReleaseMode).
// It utilizes c.Logger() for contextualized logging, ensuring that logs include
// information like request_id (if available) and adhere to the router's logger configuration.
func defaultGlobalErrorHandler(c *Context) error {
	// Retrieve the original error that caused this handler to be invoked.
	// This error is typically set in c.store by Router.Handler using the key "handler_error_cause".
	errVal, _ := c.Get("handler_error_cause")
	originalErr, isErrorType := errVal.(error) // Type assert to error.

	// Get the request-scoped logger from the context. This logger is pre-configured
	// based on Xylium's mode and includes request_id if the RequestID middleware is active.
	currentLogger := c.Logger()
	currentMode := c.RouterMode() // Get the current Xylium operating mode from the context.

	// Initialize default HTTP status code and response message for the client.
	httpStatusCode := StatusInternalServerError                            // Default to 500 Internal Server Error.
	var responseMessage interface{} = M{"error": StatusText(StatusInternalServerError)} // Default user-facing JSON message.

	if !isErrorType || originalErr == nil {
		// This situation is unexpected: GlobalErrorHandler was called without a valid error.
		// Log a warning, as this might indicate an issue in how errors are propagated.
		currentLogger.Warnf(
			"defaultGlobalErrorHandler invoked without a valid 'handler_error_cause' in context for %s %s. Mode: %s. Responding with default 500.",
			c.Method(), c.Path(), currentMode,
		)
		// Provide a generic error message to the client.
		responseMessage = M{"error": "An unexpected error occurred internally; cause not specified."}
		// httpStatusCode remains StatusInternalServerError.
	} else {
		var httpErr *HTTPError
		if errors.As(originalErr, &httpErr) {
			// Case 1: The error is an *xylium.HTTPError.
			// This type of error contains a specific HTTP status code and message.
			httpStatusCode = httpErr.Code

			// Determine the user-facing response message from the HTTPError.
			if httpErr.Message != nil {
				responseMessage = httpErr.Message // This can be a string, xylium.M, or another struct.
			} else {
				// If HTTPError.Message is nil, use the standard text for the status code.
				responseMessage = M{"error": StatusText(httpStatusCode)}
			}

			// Prepare fields for structured logging.
			logFields := M{
				"status_code": httpStatusCode,
				"client_response_message": fmt.Sprintf("%v", responseMessage), // What the client will see.
			}

			// If the HTTPError has an internal, underlying error, log it.
			// In DebugMode, also include these internal details in the client's response.
			if httpErr.Internal != nil {
				logFields["internal_error_details"] = httpErr.Internal.Error()

				if currentMode == DebugMode {
					// Add internal error details to the client JSON response under a "_debug_info" key.
					debugInfo := M{"internal_error_details": httpErr.Internal.Error()}
					if respMap, ok := responseMessage.(M); ok {
						respMap["_debug_info"] = debugInfo // Add to existing M.
						responseMessage = respMap
					} else if respMapTyped, ok := responseMessage.(map[string]interface{}); ok {
						respMapTyped["_debug_info"] = debugInfo // Add to existing map[string]interface{}.
						responseMessage = respMapTyped
					} else {
						// If responseMessage is not a map (e.g., a simple string), create a new map
						// to include both the original message and the debug information.
						responseMessage = M{
							"message":     responseMessage, // Original client-facing message.
							"_debug_info": debugInfo,
						}
					}
				}
			}
			// Log the details of the HTTPError being handled.
			currentLogger.WithFields(logFields).Errorf(
				"HTTPError (status %d) handled for request %s %s. Mode: %s.",
				httpStatusCode, c.Method(), c.Path(), currentMode,
			)

		} else {
			// Case 2: The error is a generic Go error (not an *xylium.HTTPError).
			// For such errors, a 500 Internal Server Error is typically returned to the client.
			// httpStatusCode remains StatusInternalServerError.
			responseMessage = M{"error": StatusText(StatusInternalServerError)} // Generic message for the client.

			// In DebugMode, provide the original error message to the client for easier debugging.
			if currentMode == DebugMode {
				responseMessage = M{
					"error": StatusText(StatusInternalServerError),
					"_debug_info": M{"internal_error_details": originalErr.Error()},
				}
			}
			// Log the generic error.
			currentLogger.Errorf(
				"Generic error encountered for request %s %s: %v. Mode: %s. Responding with 500.",
				c.Method(), c.Path(), originalErr, currentMode,
			)
		}
	}

	// Send the JSON response to the client.
	// The c.JSON() method handles setting the Content-Type header and the status code.
	return c.JSON(httpStatusCode, responseMessage)
}

// defaultPanicHandler is Xylium's default handler for panics recovered during request processing.
// It logs detailed information about the panic (the stack trace is logged by Router.Handler)
// and then returns an *xylium.HTTPError. This error is subsequently processed by the
// defaultGlobalErrorHandler, which formulates the final client response.
// It uses c.Logger() for contextualized logging.
func defaultPanicHandler(c *Context) error {
	// Retrieve panic information from the context.
	// This is set by Router.Handler's defer block when a panic is recovered.
	panicInfo, _ := c.Get("panic_recovery_info")
	// Create an error object from the panic information for internal logging.
	recoveredErr := fmt.Errorf("panic recovery: %v", panicInfo)

	// Get the request-scoped logger and current operating mode.
	currentLogger := c.Logger()
	currentMode := c.RouterMode()

	// Log that a panic was recovered by this specific handler.
	// The full stack trace is typically logged earlier by the central panic recovery in Router.Handler.
	currentLogger.Errorf(
		"DefaultPanicHandler: Recovered from panic during request %s %s. Panic Info: %v. Mode: %s.",
		c.Method(), c.Path(), panicInfo, currentMode,
	)

	// Define a user-friendly message for the client.
	// Avoid exposing raw panic details to the client in production.
	clientMessage := "An unexpected server error occurred. Please try again later or contact support."

	// Construct and return an HTTPError.
	// This error will be passed to the GlobalErrorHandler, which decides the final client response content
	// based on the operating mode (e.g., including more details in DebugMode).
	// The 'recoveredErr' (containing the original panic value) is set as the internal cause.
	return NewHTTPError(StatusInternalServerError, clientMessage).WithInternal(recoveredErr)
}

// defaultNotFoundHandler is Xylium's default handler for requests where no route matches the path (HTTP 404).
// It returns an *xylium.HTTPError, which is then processed by the GlobalErrorHandler.
// Explicit logging within this handler is optional, as the GlobalErrorHandler will log the resulting HTTPError.
func defaultNotFoundHandler(c *Context) error {
	// Optional: Log the 404 event directly if specific logging for "Not Found" is desired
	// before it reaches the GlobalErrorHandler.
	// Example: c.Logger().Warnf("Resource not found by client: %s %s", c.Method(), c.Path())

	// Return a structured error message.
	return NewHTTPError(StatusNotFound,
		M{"error": fmt.Sprintf("The requested resource at '%s' could not be found on this server.", c.Path())},
	)
}

// defaultMethodNotAllowedHandler is Xylium's default handler for requests where a route path exists
// but not for the requested HTTP method (HTTP 405).
// It returns an *xylium.HTTPError, which is then processed by the GlobalErrorHandler.
// The "Allow" header (listing permitted methods) is expected to be set by Router.Handler before this handler is called.
// Explicit logging here is optional.
func defaultMethodNotAllowedHandler(c *Context) error {
	// Retrieve the "Allow" header, which should have been set by Router.Handler.
	allowHeader := c.Header("Allow")
	var allowedMethods []string // Slice to hold parsed allowed methods.

	if allowHeader != "" {
		// Split the comma-separated "Allow" header string into individual methods.
		methods := strings.Split(allowHeader, ",")
		for _, m := range methods {
			trimmedMethod := strings.TrimSpace(m) // Trim whitespace from each method.
			if trimmedMethod != "" {
				allowedMethods = append(allowedMethods, trimmedMethod)
			}
		}
	}

	// Optional: Log the 405 event directly.
	// Example: c.Logger().Warnf("Method not allowed: %s for %s. Allowed methods: [%s]", c.Method(), c.Path(), allowHeader)

	// Return a structured error message including the allowed methods.
	return NewHTTPError(StatusMethodNotAllowed,
		M{
			"error":           fmt.Sprintf("The method '%s' is not supported for the resource at '%s'.", c.Method(), c.Path()),
			"allowed_methods": allowedMethods, // Provide the list of allowed methods to the client.
		},
	)
}
