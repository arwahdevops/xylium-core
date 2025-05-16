package xylium

import (
	"errors" // Untuk errors.As, errors.Is
	"fmt"
)

// HTTPError represents an error with an associated HTTP status code and an
// optional internal error for logging or further inspection.
type HTTPError struct {
	Code     int         `json:"-"` // HTTP status code, tidak di-marshal ke JSON secara default
	Message  interface{} `json:"error"`   // User-facing error message (string, map, struct for JSON)
	Internal error       `json:"-"` // Internal error, tidak di-marshal
}

// NewHTTPError creates a new HTTPError instance.
// The message can be a string, or a struct/map that will be marshalled to JSON.
// If no message is provided, a default status text for the code will be used.
func NewHTTPError(code int, message ...interface{}) *HTTPError {
	he := &HTTPError{Code: code}

	if len(message) > 0 && message[0] != nil {
		if err, ok := message[0].(error); ok {
			var herr *HTTPError
			if errors.As(err, &herr) {
				// If the passed error is already an HTTPError,
				// copy its properties to the new instance.
				// This maintains consistency by always returning a new *HTTPError
				// from this constructor, but initialized with relevant data.
				he.Message = herr.Message
				// Preserve the original internal error chain from herr
				if herr.Internal != nil {
					he.Internal = herr.Internal
				} else {
					he.Internal = herr // herr itself if it had no further internal error
				}
				// If code was different, the new code from NewHTTPError(code,...) takes precedence.
				// If message was nil in herr, it will be set below.
			} else {
				he.Message = err.Error() // Get message from generic error
				he.Internal = err        // Store original error as internal
			}
		} else {
			he.Message = message[0]
		}
	}

	if he.Message == nil || he.Message == "" {
		he.Message = StatusText(code)
		if he.Message == "" { // If StatusText is not available for this code
			he.Message = fmt.Sprintf("Error code %d", code)
		}
	}
	return he
}

// Error makes HTTPError satisfy the error interface.
// It provides a developer-friendly string representation of the error.
func (he *HTTPError) Error() string {
	if he.Internal != nil {
		// Check if internal error is the message itself to avoid redundancy.
		errMsg := fmt.Sprintf("%v", he.Message)
		if he.Internal.Error() == errMsg {
			return fmt.Sprintf("xylium.HTTPError: code=%d, message=%s", he.Code, errMsg)
		}
		return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v, internal_error=%q", he.Code, he.Message, he.Internal.Error())
	}
	return fmt.Sprintf("xylium.HTTPError: code=%d, message=%v", he.Code, he.Message)
}

// Unwrap provides compatibility for errors.Is and errors.As,
// allowing inspection of the internal error.
func (he *HTTPError) Unwrap() error {
	return he.Internal
}

// WithInternal sets or replaces the internal error and returns the HTTPError instance
// for convenient chaining.
func (he *HTTPError) WithInternal(err error) *HTTPError {
	he.Internal = err
	return he
}

// WithMessage sets or replaces the user-facing message and returns the HTTPError instance.
func (he *HTTPError) WithMessage(message interface{}) *HTTPError {
	he.Message = message
	if (he.Message == nil || he.Message == "") && he.Code != 0 {
		he.Message = StatusText(he.Code)
		if he.Message == "" {
			he.Message = fmt.Sprintf("Error code %d", he.Code)
		}
	}
	return he
}

// IsHTTPError checks if an error is an instance of HTTPError and optionally
// if it matches a specific status code.
func IsHTTPError(err error, code ...int) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		if len(code) > 0 {
			return he.Code == code[0]
		}
		return true
	}
	return false
}
