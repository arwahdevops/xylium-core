package xylium

import (
	"errors" // For errors.As, errors.Is for error unwrapping and checking.
	"fmt"    // For string formatting.
)

// HTTPError represents an error with an associated HTTP status code.
// It is Xylium's standard way of representing errors that should result in a specific
// HTTP response status and potentially a structured error message to the client.
// `GlobalErrorHandler` processes `HTTPError` instances to generate client responses.
type HTTPError struct {
	// Code is the HTTP status code that should be sent to the client (e.g., 400, 404, 500).
	// This field is typically not marshalled into the JSON response body by default
	// as the status code is part of the HTTP response line.
	Code int `json:"-"`

	// Message is the user-facing error message. It can be a simple string,
	// or a more complex type (like `xylium.M` or a custom struct) that will be
	// marshalled to JSON for the response body.
	Message interface{} `json:"error"` // The key "error" is common in JSON error responses.

	// Internal is an optional underlying error that provides more detailed, internal-only
	// context about the error. This is useful for logging and debugging but is typically
	// not exposed directly to the client (unless in DebugMode, where GlobalErrorHandler might include it).
	Internal error `json:"-"` // Not marshalled to JSON by default.
}

// NewHTTPError creates a new `HTTPError` instance.
// - `code`: The HTTP status code for this error.
// - `message...`: An optional variadic argument for the user-facing message.
//   - If provided, the first element `message[0]` is used.
//   - If `message[0]` is an `error` type:
//   - If it's already an `*HTTPError`, its `Message` and `Internal` error are adopted (unless `message[0]` had no `Internal`, then `message[0]` itself becomes `Internal`). The `code` from `NewHTTPError` call takes precedence.
//   - If it's a generic `error`, its `Error()` string becomes `he.Message`, and the error itself becomes `he.Internal`.
//   - If `message[0]` is not an `error` (e.g., string, `xylium.M`), it's used directly as `he.Message`.
//   - If no `message` is provided or it's nil, `he.Message` defaults to the standard HTTP status text for `code` (e.g., "Not Found" for 404).
func NewHTTPError(code int, message ...interface{}) *HTTPError {
	he := &HTTPError{Code: code} // Initialize with the provided status code.

	if len(message) > 0 && message[0] != nil {
		// Process the first element of the variadic `message` argument.
		msgArg := message[0]
		if err, ok := msgArg.(error); ok { // If the message argument is an error type.
			var herr *HTTPError
			if errors.As(err, &herr) { // If it's specifically an *HTTPError.
				// Adopt properties from the existing HTTPError.
				// The new `code` from `NewHTTPError(code, ...)` call takes precedence.
				he.Message = herr.Message
				// Preserve the original internal error chain from `herr`.
				if herr.Internal != nil {
					he.Internal = herr.Internal
				} else {
					// If `herr` had no further internal error, `herr` itself is the cause.
					he.Internal = herr
				}
				// If `herr.Message` was nil, it will be set to default status text below.
			} else {
				// If it's a generic Go error, use its string representation as the message
				// and store the original error as the internal cause.
				he.Message = err.Error()
				he.Internal = err
			}
		} else {
			// If the message argument is not an error type (e.g., string, xylium.M, struct),
			// use it directly as the user-facing message.
			he.Message = msgArg
		}
	}

	// If, after processing, `he.Message` is still nil or an empty string,
	// set it to the default HTTP status text for the given code.
	if he.Message == nil || (fmt.Sprintf("%v", he.Message) == "") { // Check for empty string representation too.
		he.Message = StatusText(code)
		if he.Message == "" { // Fallback if StatusText is not available for this code.
			he.Message = fmt.Sprintf("Error code %d", code)
		}
	}
	return he
}

// Error makes `HTTPError` satisfy the standard Go `error` interface.
// It provides a developer-friendly string representation of the error,
// including the code, message, and internal error details if present.
// This string is primarily for logging and debugging, not for client responses.
func (he *HTTPError) Error() string {
	if he.Internal != nil {
		// Avoid redundant message if internal error is the message itself (e.g. from NewHTTPError(500, errors.New("db fail"))).
		errMsgStr := fmt.Sprintf("%v", he.Message) // Use %v for generic message printing.
		internalErrStr := he.Internal.Error()
		if internalErrStr == errMsgStr {
			return fmt.Sprintf("xylium.HTTPError: code=%d, message=%s (internal error is same as message)", he.Code, errMsgStr)
		}
		return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v, internal_error=%q", he.Code, he.Message, internalErrStr)
	}
	return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v", he.Code, he.Message)
}

// Unwrap provides compatibility with Go's `errors.Is` and `errors.As` functions (Go 1.13+).
// It allows inspection of the `Internal` error, enabling checking of the underlying error chain.
func (he *HTTPError) Unwrap() error {
	return he.Internal
}

// WithInternal sets or replaces the `Internal` error of the `HTTPError` instance.
// It returns the modified `HTTPError` pointer, allowing for convenient chaining of calls.
// Example: `return xylium.NewHTTPError(500, "Database error").WithInternal(originalDbErr)`
func (he *HTTPError) WithInternal(err error) *HTTPError {
	he.Internal = err
	return he
}

// WithMessage sets or replaces the user-facing `Message` of the `HTTPError` instance.
// If the new message is nil or empty, it attempts to set a default message based on `he.Code`.
// It returns the modified `HTTPError` pointer for chaining.
func (he *HTTPError) WithMessage(message interface{}) *HTTPError {
	he.Message = message
	// If the new message is nil or effectively empty, and a code is set,
	// re-apply the default status text for that code.
	if (he.Message == nil || fmt.Sprintf("%v", he.Message) == "") && he.Code != 0 {
		he.Message = StatusText(he.Code)
		if he.Message == "" { // Fallback if StatusText is not available.
			he.Message = fmt.Sprintf("Error code %d", he.Code)
		}
	}
	return he
}

// IsHTTPError checks if a given `err` is an instance of `*xylium.HTTPError`.
// Optionally, if `code` (a status code integer) is provided as a second argument,
// it also checks if the `HTTPError`'s `Code` field matches that status code.
// Returns true if the conditions are met, false otherwise.
func IsHTTPError(err error, code ...int) bool {
	var he *HTTPError
	// `errors.As` checks if `err` (or any error in its chain via Unwrap) matches `*HTTPError`,
	// and if so, assigns it to `he`.
	if errors.As(err, &he) {
		if len(code) > 0 { // If a specific status code to check was provided.
			return he.Code == code[0]
		}
		return true // It's an HTTPError, no specific code check requested.
	}
	return false // Not an HTTPError.
}
