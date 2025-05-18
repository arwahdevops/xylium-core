package xylium

import (
	"errors"  // For errors.As, used to check if an error is of type *HTTPError.
	"fmt"     // For string formatting in error messages and log outputs.
	"strings" // For string manipulation, like splitting the "Allow" header.
)

// defaultGlobalErrorHandler is the default central error handler for Xylium.
// It is invoked by the router's main Handler when an error is returned by the
// request handler chain (including middleware) or by the PanicHandler.
//
// Key Responsibilities:
// - Retrieves the original error cause from the context (`c.Get("handler_error_cause")`).
// - Uses `c.Logger()` for contextualized logging (includes request_id, respects router's logger config).
// - Differentiates between `xylium.HTTPError` and generic Go errors.
// - For `xylium.HTTPError`:
//   - Uses the error's specified HTTP status code and message for the client response.
//   - Logs the error details, including any internal error (`httpErr.Internal`).
//   - In `DebugMode`, includes `httpErr.Internal.Error()` in the client JSON response under `_debug_info`.
// - For generic Go errors:
//   - Responds with HTTP 500 Internal Server Error.
//   - In `DebugMode`, includes the `originalErr.Error()` in the client JSON response under `_debug_info`.
//   - In `ReleaseMode`, provides a generic "Internal Server Error" message to the client.
// - Sends a JSON response to the client.
func defaultGlobalErrorHandler(c *Context) error {
	// Retrieve the original error that caused this handler to be invoked.
	// This error is set in c.store by Router.Handler using the key "handler_error_cause".
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
		// This might indicate an issue in how errors are propagated within the router or middleware.
		currentLogger.Warnf(
			"defaultGlobalErrorHandler invoked without a valid 'handler_error_cause' in context for %s %s. Mode: %s. Responding with default 500.",
			c.Method(), c.Path(), currentMode,
		)
		// Provide a generic error message to the client.
		responseMessage = M{"error": "An unexpected error occurred internally; cause not specified."}
		// httpStatusCode remains StatusInternalServerError.
	} else {
		// Attempt to cast the originalError to *xylium.HTTPError.
		var httpErr *HTTPError
		if errors.As(originalErr, &httpErr) {
			// Case 1: The error is an *xylium.HTTPError.
			// This type of error carries specific HTTP status code and a user-facing message.
			httpStatusCode = httpErr.Code

			// Determine the user-facing response message from the HTTPError.
			if httpErr.Message != nil {
				// httpErr.Message can be a string, xylium.M, or another struct suitable for JSON.
				responseMessage = httpErr.Message
			} else {
				// If HTTPError.Message is nil, use the standard text for the status code.
				responseMessage = M{"error": StatusText(httpStatusCode)}
			}

			// Prepare fields for structured logging.
			logFields := M{
				"status_code":             httpStatusCode,
				"client_response_message": fmt.Sprintf("%#v", responseMessage), // Log the structure of the client message.
			}

			// If the HTTPError has an internal, underlying error, log it for debugging.
			// In DebugMode, also include these internal details in the client's response.
			if httpErr.Internal != nil {
				logFields["internal_error_details"] = httpErr.Internal.Error()

				if currentMode == DebugMode {
					// Add internal error details to the client JSON response under a "_debug_info" key.
					debugInfo := M{"internal_error_details": httpErr.Internal.Error()}
					// Safely add to responseMessage, whether it's xylium.M, map[string]interface{}, or other.
					if respMap, ok := responseMessage.(M); ok {
						respMap["_debug_info"] = debugInfo
						responseMessage = respMap
					} else if respMapTyped, ok := responseMessage.(map[string]interface{}); ok {
						respMapTyped["_debug_info"] = debugInfo
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
					"error": StatusText(StatusInternalServerError), // Keep generic error message for client.
					"_debug_info": M{"internal_error_details": originalErr.Error()}, // Add debug info.
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
	// c.JSON() handles setting the Content-Type header and the status code.
	// It also returns an error if JSON marshalling fails, which would be critical here.
	// If c.JSON itself fails, the error is returned, and Router.Handler's defer
	// will log it (though it can't send another response if this one failed mid-way).
	return c.JSON(httpStatusCode, responseMessage)
}

// defaultPanicHandler is Xylium's default handler for panics recovered during request processing.
// It is invoked by the router's main Handler when `recover()` captures a panic.
//
// Key Responsibilities:
// - Retrieves panic information from the context (`c.Get("panic_recovery_info")`).
// - Logs the panic event using `c.Logger()`. The full stack trace is logged by Router.Handler's defer.
// - Constructs and returns an `xylium.HTTPError` with status 500.
// - This `HTTPError` is then processed by the `defaultGlobalErrorHandler` (or a custom one),
//   which formulates the final client response (generic in ReleaseMode, more details in DebugMode).
func defaultPanicHandler(c *Context) error {
	// Retrieve panic information from the context.
	// This is set by Router.Handler's defer block when a panic is recovered.
	panicInfo, _ := c.Get("panic_recovery_info")
	// Create an error object from the panic information for internal logging and error chaining.
	recoveredErr := fmt.Errorf("panic recovery: %v", panicInfo)

	// Get the request-scoped logger and current operating mode.
	currentLogger := c.Logger()
	currentMode := c.RouterMode()

	// Log that a panic was recovered by this specific handler.
	// The full stack trace is typically logged earlier by the central panic recovery in Router.Handler.
	// This log entry confirms that the panic is being handled by the designated PanicHandler.
	currentLogger.Errorf(
		"DefaultPanicHandler: Recovered from panic during request %s %s. Panic Info: %v. Mode: %s.",
		c.Method(), c.Path(), panicInfo, currentMode,
	)

	// Define a user-friendly message for the client.
	// Avoid exposing raw panic details (like stack traces or sensitive variable states)
	// to the client, especially in production (ReleaseMode).
	clientMessage := "An unexpected server error occurred. Please try again later or contact support."

	// Construct and return an HTTPError.
	// This error will be passed to the GlobalErrorHandler, which decides the final client response content
	// based on the operating mode (e.g., including more details like the panic value in DebugMode).
	// The 'recoveredErr' (containing the original panic value) is set as the internal cause.
	return NewHTTPError(StatusInternalServerError, clientMessage).WithInternal(recoveredErr)
}

// defaultNotFoundHandler is Xylium's default handler for requests where no route
// matches the requested path (HTTP 404 Not Found).
// It is invoked by the router's main Handler.
// It returns an `xylium.HTTPError`, which is then processed by the `GlobalErrorHandler`.
func defaultNotFoundHandler(c *Context) error {
	// The `GlobalErrorHandler` will log the resulting `HTTPError`.
	// If specific "Not Found" logging distinct from other errors is desired before
	// it reaches GlobalErrorHandler, it can be added here using c.Logger().
	// Example: c.Logger().Warnf("Resource not found by client: %s %s", c.Method(), c.Path())

	// Return a structured error message for the client.
	return NewHTTPError(StatusNotFound,
		M{"error": fmt.Sprintf("The requested resource at '%s' could not be found on this server.", c.Path())},
	)
}

// defaultMethodNotAllowedHandler is Xylium's default handler for requests where a route
// path exists, but not for the requested HTTP method (HTTP 405 Method Not Allowed).
// It is invoked by the router's main Handler.
// The "Allow" header (listing permitted methods for the path) is expected to be set by
// Router.Handler before this handler is called.
// It returns an `xylium.HTTPError`, which is then processed by the `GlobalErrorHandler`.
func defaultMethodNotAllowedHandler(c *Context) error {
	// Retrieve the "Allow" header, which should have been set by Router.Handler.
	allowHeader := c.Header("Allow") // fasthttp specific: c.Ctx.Response.Header.Peek("Allow") if set on response
	                                 // but Router.Handler sets it on c.SetHeader which goes to response.

	var allowedMethods []string // Slice to hold parsed allowed methods for the JSON response.

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

	// The `GlobalErrorHandler` will log the resulting `HTTPError`.
	// If specific "Method Not Allowed" logging is desired before GlobalErrorHandler, add it here.
	// Example:
	// c.Logger().Warnf("Method %s not allowed for path %s. Allowed methods: [%s]",
	//    c.Method(), c.Path(), allowHeader)

	// Return a structured error message including the allowed methods.
	return NewHTTPError(StatusMethodNotAllowed,
		M{
			"error":           fmt.Sprintf("The method '%s' is not supported for the resource at '%s'.", c.Method(), c.Path()),
			"allowed_methods": allowedMethods, // Provide the list of allowed methods to the client.
		},
	)
}
