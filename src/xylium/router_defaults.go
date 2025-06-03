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
//   - Retrieves the original error cause from the context (using ContextKeyErrorCause).
//   - Uses `c.Logger()` for contextualized logging.
//   - Differentiates between `xylium.HTTPError` and generic Go errors.
//   - For `xylium.HTTPError`:
//   - Uses the error's specified HTTP status code and message for the client response.
//   - Logs the error details, including any internal error (`httpErr.Internal`).
//   - In `DebugMode`, includes `httpErr.Internal.Error()` in the client JSON response under `_debug_info`.
//   - For generic Go errors:
//   - Responds with HTTP 500 Internal Server Error.
//   - In `DebugMode`, includes the `originalErr.Error()` in the client JSON response under `_debug_info`.
//   - In `ReleaseMode`, provides a generic "Internal Server Error" message to the client.
//   - Sends a JSON response to the client.
func defaultGlobalErrorHandler(c *Context) error {
	errVal, _ := c.Get(ContextKeyErrorCause) // Use defined constant
	originalErr, isErrorType := errVal.(error)

	currentLogger := c.Logger()
	currentMode := c.RouterMode()

	httpStatusCode := StatusInternalServerError
	var responseMessage interface{} = M{"error": StatusText(StatusInternalServerError)}

	if !isErrorType || originalErr == nil {
		currentLogger.Warnf(
			"defaultGlobalErrorHandler invoked without a valid '%s' in context for %s %s. Mode: %s. Responding with default 500.",
			ContextKeyErrorCause, c.Method(), c.Path(), currentMode,
		)
		responseMessage = M{"error": "An unexpected error occurred internally; cause not specified."}
	} else {
		var httpErr *HTTPError
		if errors.As(originalErr, &httpErr) {
			httpStatusCode = httpErr.Code
			if httpErr.Message != nil {
				responseMessage = httpErr.Message
			} else {
				responseMessage = M{"error": StatusText(httpStatusCode)}
			}

			logFields := M{
				"status_code":             httpStatusCode,
				"client_response_message": fmt.Sprintf("%#v", responseMessage),
			}

			if httpErr.Internal != nil {
				internalErrorStr := httpErr.Internal.Error()
				logFields["internal_error_details"] = internalErrorStr

				if currentMode == DebugMode {
					var debugInfoContent interface{}
					if clientMsgStr, ok := httpErr.Message.(string); ok && clientMsgStr == internalErrorStr {
						debugInfoContent = M{"internal_error_details": "Same as primary error message."}
					} else {
						debugInfoContent = M{"internal_error_details": internalErrorStr}
					}

					if respMap, ok := responseMessage.(M); ok {
						respMap["_debug_info"] = debugInfoContent
						responseMessage = respMap
					} else if respMapTyped, ok := responseMessage.(map[string]interface{}); ok {
						respMapTyped["_debug_info"] = debugInfoContent
						responseMessage = respMapTyped
					} else {
						responseMessage = M{
							"message":     responseMessage,
							"_debug_info": debugInfoContent,
						}
					}
				}
			}
			currentLogger.WithFields(logFields).Errorf(
				"HTTPError (status %d) handled for request %s %s. Mode: %s.",
				httpStatusCode, c.Method(), c.Path(), currentMode,
			)
		} else { // Generic Go error
			// httpStatusCode remains StatusInternalServerError.
			responseMessage = M{"error": StatusText(StatusInternalServerError)}
			if currentMode == DebugMode {
				responseMessage = M{
					"error":       StatusText(StatusInternalServerError),
					"_debug_info": M{"internal_error_details": originalErr.Error()},
				}
			}
			currentLogger.Errorf(
				"Generic error encountered for request %s %s: %v. Mode: %s. Responding with 500.",
				c.Method(), c.Path(), originalErr, currentMode,
			)
		}
	}
	return c.JSON(httpStatusCode, responseMessage)
}

// defaultPanicHandler is Xylium's default handler for panics recovered during request processing.
// It is invoked by the router's main Handler when `recover()` captures a panic.
//
// Key Responsibilities:
//   - Retrieves panic information from the context (using ContextKeyPanicInfo).
//   - Logs the panic event using `c.Logger()`. (Stack trace logged by Router.Handler).
//   - Constructs and returns an `xylium.HTTPError` with status 500.
//   - This `HTTPError` is then processed by the `GlobalErrorHandler`.
func defaultPanicHandler(c *Context) error {
	panicInfo, _ := c.Get(ContextKeyPanicInfo) // Use defined constant
	recoveredErr := fmt.Errorf("panic recovery: %v", panicInfo)

	currentLogger := c.Logger()
	currentMode := c.RouterMode()

	currentLogger.Errorf(
		"DefaultPanicHandler: Recovered from panic during request %s %s. Panic Info: %v. Mode: %s.",
		c.Method(), c.Path(), panicInfo, currentMode,
	)

	clientMessage := "An unexpected server error occurred. Please try again later or contact support."
	return NewHTTPError(StatusInternalServerError, clientMessage).WithInternal(recoveredErr)
}

// defaultNotFoundHandler is Xylium's default handler for requests where no route
// matches the requested path (HTTP 404 Not Found).
// It returns an `xylium.HTTPError`, which is then processed by the `GlobalErrorHandler`.
func defaultNotFoundHandler(c *Context) error {
	return NewHTTPError(StatusNotFound,
		M{"error": fmt.Sprintf("The requested resource at '%s' could not be found on this server.", c.Path())},
	)
}

// defaultMethodNotAllowedHandler is Xylium's default handler for requests where a route
// path exists, but not for the requested HTTP method (HTTP 405 Method Not Allowed).
// The "Allow" header is expected to be set by Router.Handler before this is called.
// It returns an `xylium.HTTPError`, processed by the `GlobalErrorHandler`.
func defaultMethodNotAllowedHandler(c *Context) error {
	allowHeader := string(c.Ctx.Response.Header.Peek("Allow"))
	var allowedMethods []string
	if allowHeader != "" {
		methods := strings.Split(allowHeader, ",")
		for _, m := range methods {
			trimmedMethod := strings.TrimSpace(m)
			if trimmedMethod != "" {
				allowedMethods = append(allowedMethods, trimmedMethod)
			}
		}
	}
	return NewHTTPError(StatusMethodNotAllowed,
		M{
			"error":           fmt.Sprintf("The method '%s' is not supported for the resource at '%s'.", c.Method(), c.Path()),
			"allowed_methods": allowedMethods,
		},
	)
}
