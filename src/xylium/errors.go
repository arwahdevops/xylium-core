package xylium

import (
	"errors" // For errors.As, errors.Is for error unwrapping and checking.
	"fmt"    // For string formatting.
)

// HTTPError represents an error that is associated with a specific HTTP status code.
// It is Xylium's standard mechanism for communicating errors that should result in a
// particular HTTP response status and, optionally, a structured error message body
// sent to the client.
//
// Instances of `HTTPError` are typically returned from `xylium.HandlerFunc` or middleware.
// The `Router.GlobalErrorHandler` is then responsible for processing these `HTTPError`
// instances to generate and send the final HTTP response to the client.
type HTTPError struct {
	// Code is the HTTP status code that should be used in the HTTP response line
	// (e.g., 400 for Bad Request, 404 for Not Found, 500 for Internal Server Error).
	// This field is typically *not* marshalled into the JSON response body itself,
	// as the status code is an integral part of the HTTP response's status line.
	// The `json:"-"` tag explicitly prevents it from being included during JSON marshalling
	// if the `HTTPError` struct (or its `Message` field if it's an `HTTPError`) were
	// directly marshalled.
	Code int `json:"-"`

	// Message is the user-facing payload that will form the body of the error response.
	// It can be a simple string (e.g., "Resource not found"), or a more complex type
	// such as `xylium.M` (map[string]interface{}) or a custom struct, which will then
	// be marshalled to JSON (or another format depending on the response method used).
	// The `json:"error"` tag suggests that if this `HTTPError`'s `Message` is marshalled
	// as part of a larger JSON structure, this field would appear under the key "error".
	// When `c.JSON` is used with an `HTTPError`'s `Message` as data, the `Message`
	// content itself becomes the JSON body.
	Message interface{} `json:"error"`

	// Internal is an optional, underlying error that provides more detailed, internal-only
	// context about the error's cause. This is invaluable for server-side logging and
	// debugging. It is typically *not* exposed directly to the client in the HTTP response,
	// especially in production (`ReleaseMode`). However, in `DebugMode`, Xylium's
	// `defaultGlobalErrorHandler` might include details from `Internal.Error()` in
	// the JSON response body (e.g., under a `_debug_info` key) to aid development.
	// The `json:"-"` tag prevents its inclusion in default JSON marshalling of this struct.
	Internal error `json:"-"`
}

// NewHTTPError creates and returns a new `*HTTPError` instance.
// This is the primary constructor for generating HTTP-specific errors within Xylium.
//
// Parameters:
//   - `code` (int): The HTTP status code for this error (e.g., `xylium.StatusBadRequest`, `xylium.StatusNotFound`).
//   - `message...` (interface{}): An optional variadic argument for the user-facing message.
//     Only the first element `message[0]` is processed if provided.
//   - If `message[0]` is an `error` type:
//   - If it's an existing `*xylium.HTTPError` (let's call it `originalHTTPErr`):
//     The new `*HTTPError` will adopt `originalHTTPErr.Message`.
//     Its `Internal` error will be `originalHTTPErr.Internal` if `originalHTTPErr.Internal` was not nil.
//     If `originalHTTPErr.Internal` was nil, then `originalHTTPErr` itself becomes the `Internal` error
//     of the new `*HTTPError`. The `code` provided to *this* `NewHTTPError` call
//     will take precedence over `originalHTTPErr.Code`.
//   - If it's any other `error` type (a generic Go error):
//     The result of `err.Error()` (the string representation of the generic error)
//     will become the `Message` of the new `*HTTPError`.
//     The generic error `err` itself will be set as the `Internal` error.
//   - If `message[0]` is *not* an `error` type (e.g., a string, `xylium.M`, or a custom struct):
//     It is used directly as the `Message` of the new `*HTTPError`.
//   - If no `message` argument is provided, or if `message[0]` is `nil`, or if after processing
//     the `Message` field is still effectively empty (e.g., an empty string):
//     The `Message` defaults to the standard HTTP status text for the given `code`
//     (e.g., "Not Found" for code 404), obtained via `xylium.StatusText(code)`.
//     If `StatusText` returns an empty string for an unknown code, a fallback message
//     like "Error code XXX" is used.
//
// Returns:
//   - `*HTTPError`: A pointer to the newly created `HTTPError` instance.
func NewHTTPError(code int, message ...interface{}) *HTTPError {
	he := &HTTPError{Code: code} // Initialize with the provided HTTP status code.

	if len(message) > 0 && message[0] != nil {
		// Process the first element of the variadic `message` argument.
		msgArg := message[0]
		if err, ok := msgArg.(error); ok { // Case 1: The message argument is an error.
			var originalHTTPErr *HTTPError
			if errors.As(err, &originalHTTPErr) { // Case 1a: It's specifically an *xylium.HTTPError.
				// Adopt properties from the existing HTTPError.
				// The `code` from *this* NewHTTPError(code, ...) call takes precedence.
				he.Message = originalHTTPErr.Message
				// Preserve the original internal error chain from `originalHTTPErr`.
				if originalHTTPErr.Internal != nil {
					he.Internal = originalHTTPErr.Internal
				} else {
					// If `originalHTTPErr` had no further internal error, `originalHTTPErr` itself
					// is considered the cause for the purpose of the `Internal` field here.
					he.Internal = originalHTTPErr
				}
				// If originalHTTPErr.Message was nil, it will be set to default status text below.
			} else { // Case 1b: It's a generic Go error (not an *xylium.HTTPError).
				// Use its string representation as the user-facing message,
				// and store the original generic error as the internal cause.
				he.Message = err.Error()
				he.Internal = err
			}
		} else { // Case 2: The message argument is not an error type (e.g., string, xylium.M, struct).
			// Use it directly as the user-facing message.
			he.Message = msgArg
		}
	}

	// After processing the message argument, if `he.Message` is still nil or
	// represents an empty string, set it to the default HTTP status text for the `he.Code`.
	// This ensures there's always some meaningful message.
	if he.Message == nil {
		he.Message = StatusText(he.Code)
	} else if msgStr, isStr := he.Message.(string); isStr && msgStr == "" {
		he.Message = StatusText(he.Code)
	}
	// If StatusText(he.Code) itself was empty (e.g., for a highly custom or unknown status code),
	// provide a generic fallback message.
	if msgStr, isStr := he.Message.(string); isStr && msgStr == "" {
		he.Message = fmt.Sprintf("Error code %d", he.Code)
	}

	return he
}

// Error makes `HTTPError` satisfy the standard Go `error` interface.
// It returns a string representation of the `HTTPError`, primarily intended for
// server-side logging and debugging, not for direct presentation to the client.
// The format includes the HTTP status code, the user-facing message, and details
// of the internal error if present.
//
// Example output:
//
//	"xylium.HTTPError: code=404, message=Resource not found"
//	"xylium.HTTPError: code=500, message=Database query failed, internal_error="connection refused""
//	"xylium.HTTPError: code=400, message=bad input (internal error is same as message)"
func (he *HTTPError) Error() string {
	if he.Internal != nil {
		// For clarity in logs, if the he.Message is simply the string form of he.Internal.Error(),
		// note that they are the same to avoid redundancy in the log string.
		errMsgStr := fmt.Sprintf("%v", he.Message) // Use %v for generic message type printing.
		internalErrStr := he.Internal.Error()
		if internalErrStr == errMsgStr {
			return fmt.Sprintf("xylium.HTTPError: code=%d, message=%s (internal error is same as message)", he.Code, errMsgStr)
		}
		// Quote internal_error for better readability if it contains spaces or special chars.
		return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v, internal_error=%q", he.Code, he.Message, internalErrStr)
	}
	// If there's no internal error.
	return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v", he.Code, he.Message)
}

// Unwrap provides compatibility with Go's standard `errors.Is` and `errors.As`
// functions (introduced in Go 1.13+). It allows these functions to inspect the
// `Internal` error field of the `HTTPError`, effectively making `Internal` part
// of the error chain that can be examined.
//
// If `Internal` is nil, `Unwrap` returns nil.
//
// Example usage:
//
//	if errors.Is(xyliumErr, sql.ErrNoRows) { ... }
//	var pgErr *pq.Error; if errors.As(xyliumErr, &pgErr) { ... }
func (he *HTTPError) Unwrap() error {
	return he.Internal
}

// WithInternal sets or replaces the `Internal` error of the `HTTPError` instance.
// The `Internal` error is used for server-side logging and debugging to provide
// more detailed context about the failure, without exposing these details to the client.
//
// This method returns the modified `HTTPError` pointer, allowing for convenient
// chaining of calls.
//
// Example:
//
//	return xylium.NewHTTPError(500, "Database operation failed").WithInternal(originalDbError)
func (he *HTTPError) WithInternal(err error) *HTTPError {
	he.Internal = err
	return he
}

// WithMessage sets or replaces the user-facing `Message` of the `HTTPError` instance.
// The `Message` is the payload that will typically form the body of the HTTP error response
// sent to the client.
//
// If the new `message` argument is nil or an empty string, `WithMessage` will attempt
// to set a default message based on the `HTTPError`'s `Code` field (using `xylium.StatusText`).
//
// This method returns the modified `HTTPError` pointer, allowing for chaining.
//
// Example:
//
//	err := xylium.NewHTTPError(xylium.StatusForbidden)
//	// ... some logic ...
//	if user.IsSuspended {
//	    err = err.WithMessage("Access denied: Your account is suspended.")
//	}
//	return err
func (he *HTTPError) WithMessage(message interface{}) *HTTPError {
	he.Message = message
	// If the new message is nil or effectively empty (e.g., an empty string),
	// and a valid Code is set, re-apply the default status text for that code.
	// This ensures the message field remains meaningful.
	if he.Message == nil {
		he.Message = StatusText(he.Code)
	} else if msgStr, isStr := he.Message.(string); isStr && msgStr == "" {
		he.Message = StatusText(he.Code)
	}
	// Fallback if StatusText(he.Code) was also empty (e.g., for a custom/unknown code).
	if msgStr, isStr := he.Message.(string); isStr && msgStr == "" && he.Code != 0 {
		he.Message = fmt.Sprintf("Error code %d", he.Code)
	}
	return he
}

// IsHTTPError checks if a given error `err` is an instance of `*xylium.HTTPError`.
// It uses `errors.As` to traverse the error chain (via `Unwrap` methods), so it
// can identify an `*xylium.HTTPError` even if it's wrapped by other errors.
//
// Optionally, if one or more integer `code` arguments are provided, this function
// will also check if the `HTTPError`'s `Code` field matches the *first* `code`
// argument provided.
//
// Parameters:
//   - `err` (error): The error to check.
//   - `code...` (int): Optional HTTP status code(s) to match against `HTTPError.Code`.
//     Only the first `code` argument is used if multiple are provided.
//
// Returns:
//   - `bool`: True if `err` is an `*xylium.HTTPError` (and, if a `code` argument
//     was provided, its `Code` field matches). False otherwise.
//
// Example:
//
//	if xylium.IsHTTPError(err, xylium.StatusNotFound) {
//	    // Handle "Not Found" specifically
//	} else if xylium.IsHTTPError(err) {
//	    // It's some other Xylium HTTPError
//	}
func IsHTTPError(err error, code ...int) bool {
	var he *HTTPError
	// `errors.As` checks if `err` (or any error in its chain via Unwrap methods)
	// can be assigned to `he` (which is of type *HTTPError).
	if errors.As(err, &he) {
		if len(code) > 0 {
			// If a specific status code to check was provided, compare it.
			return he.Code == code[0]
		}
		// It's an HTTPError, and no specific code check was requested.
		return true
	}
	// The error `err` (or its chain) is not an *xylium.HTTPError.
	return false
}
