package xylium_test

import (
	"errors"
	"fmt"
	"net/http" // Untuk konstanta status HTTP
	"strings"
	"testing"

	// Ganti path ini sesuai dengan module path Anda
	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/stretchr/testify/assert" // Opsional
)

func TestNewHTTPError(t *testing.T) {
	testCases := []struct {
		name              string
		code              int
		messages          []interface{} // Variadic messages
		expectedCode      int
		expectedMsgString string   // Untuk perbandingan pesan string sederhana
		expectedMsgMap    xylium.M // Untuk perbandingan pesan map
		expectedInternal  error
		checkInternalText string // Substring untuk dicari di error.Error() dari internal
	}{
		{
			name:              "With String Message",
			code:              http.StatusBadRequest,
			messages:          []interface{}{"Invalid input provided"},
			expectedCode:      http.StatusBadRequest,
			expectedMsgString: "Invalid input provided",
		},
		{
			name:           "With Xylium.M Message",
			code:           http.StatusUnprocessableEntity,
			messages:       []interface{}{xylium.M{"field": "email", "error": "already exists"}},
			expectedCode:   http.StatusUnprocessableEntity,
			expectedMsgMap: xylium.M{"field": "email", "error": "already exists"},
		},
		{
			name:              "No Message (Defaults to Status Text)",
			code:              http.StatusNotFound,
			messages:          nil,
			expectedCode:      http.StatusNotFound,
			expectedMsgString: xylium.StatusText(http.StatusNotFound), // Gunakan StatusText dari xylium
		},
		{
			name:              "Nil Message in Slice (Defaults to Status Text)",
			code:              http.StatusForbidden,
			messages:          []interface{}{nil},
			expectedCode:      http.StatusForbidden,
			expectedMsgString: xylium.StatusText(http.StatusForbidden),
		},
		{
			name:              "Empty String Message (Defaults to Status Text)",
			code:              http.StatusServiceUnavailable,
			messages:          []interface{}{""},
			expectedCode:      http.StatusServiceUnavailable,
			expectedMsgString: xylium.StatusText(http.StatusServiceUnavailable),
		},
		{
			name:              "Wrapping Generic Error",
			code:              http.StatusInternalServerError,
			messages:          []interface{}{errors.New("database connection failed")},
			expectedCode:      http.StatusInternalServerError,
			expectedMsgString: "database connection failed", // Pesan dari error.Error()
			expectedInternal:  errors.New("database connection failed"),
			checkInternalText: "database connection failed",
		},
		{
			name: "Wrapping Another HTTPError",
			code: http.StatusBadGateway, // Kode baru akan menimpa
			messages: []interface{}{
				xylium.NewHTTPError(http.StatusServiceUnavailable, "Downstream service unavailable").WithInternal(errors.New("downstream timeout")),
			},
			expectedCode:      http.StatusBadGateway,
			expectedMsgString: "Downstream service unavailable",
			expectedInternal:  xylium.NewHTTPError(http.StatusServiceUnavailable, "Downstream service unavailable").WithInternal(errors.New("downstream timeout")),
			checkInternalText: "downstream timeout", // Dari WithInternal
		},
		{
			name: "Wrapping HTTPError without explicit internal (HTTPError itself becomes internal)",
			code: http.StatusConflict,
			messages: []interface{}{
				xylium.NewHTTPError(http.StatusBadRequest, "Specific bad request reason"),
			},
			expectedCode:      http.StatusConflict,
			expectedMsgString: "Specific bad request reason",
			expectedInternal:  xylium.NewHTTPError(http.StatusBadRequest, "Specific bad request reason"),
			checkInternalText: "Specific bad request reason", // Teks dari HTTPError yang di-wrap
		},
		{
			name:              "Unknown Status Code (Defaults to Error Code X message)",
			code:              599, // Kode tidak standar
			messages:          nil,
			expectedCode:      599,
			expectedMsgString: "Error code 599", // Perilaku fallback
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var httpErr *xylium.HTTPError
			if tc.messages == nil { // NewHTTPError bisa dipanggil tanpa argumen message
				httpErr = xylium.NewHTTPError(tc.code)
			} else {
				httpErr = xylium.NewHTTPError(tc.code, tc.messages...)
			}

			if httpErr.Code != tc.expectedCode {
				t.Errorf("Expected code %d, got %d", tc.expectedCode, httpErr.Code)
			}

			if tc.expectedMsgString != "" {
				if msgStr, ok := httpErr.Message.(string); !ok || msgStr != tc.expectedMsgString {
					t.Errorf("Expected message string '%s', got '%v' (type %T)", tc.expectedMsgString, httpErr.Message, httpErr.Message)
				}
			} else if tc.expectedMsgMap != nil {
				if msgMap, ok := httpErr.Message.(xylium.M); !ok {
					t.Errorf("Expected message to be xylium.M, got %T", httpErr.Message)
				} else {
					// Perbandingan map sederhana
					if len(msgMap) != len(tc.expectedMsgMap) {
						t.Errorf("Expected message map length %d, got %d. Expected: %v, Got: %v", len(tc.expectedMsgMap), len(msgMap), tc.expectedMsgMap, msgMap)
					}
					for k, expectedV := range tc.expectedMsgMap {
						actualV, exists := msgMap[k]
						if !exists || fmt.Sprintf("%v", actualV) != fmt.Sprintf("%v", expectedV) {
							t.Errorf("Expected message map key '%s' to have value '%v', got '%v' (exists: %t). ExpectedMap: %v, ActualMap: %v", k, expectedV, actualV, exists, tc.expectedMsgMap, msgMap)
							break
						}
					}
				}
			}

			if tc.expectedInternal != nil {
				if httpErr.Internal == nil {
					t.Errorf("Expected internal error, but got nil")
				} else if tc.checkInternalText != "" && !strings.Contains(httpErr.Internal.Error(), tc.checkInternalText) {
					// Membandingkan pesan error internal
					t.Errorf("Internal error message '%s' does not contain '%s'", httpErr.Internal.Error(), tc.checkInternalText)
				} else if tc.checkInternalText == "" && httpErr.Internal.Error() != tc.expectedInternal.Error() {
					// Fallback ke perbandingan string error jika checkInternalText tidak diset
					// Ini mungkin rapuh jika error memiliki detail dinamis.
					// Untuk perbandingan error yang lebih baik, gunakan errors.Is jika memungkinkan
					// atau bandingkan tipe errornya.
					// Di sini kita fokus pada pesan error internal yang diharapkan.
					// Jika expectedInternal adalah *xylium.HTTPError, Error() akan mencakup kodenya.
					if _, ok := tc.expectedInternal.(*xylium.HTTPError); ok {
						if httpErr.Internal.Error() != tc.expectedInternal.Error() {
							t.Errorf("Expected internal error '%v', got '%v'", tc.expectedInternal, httpErr.Internal)
						}
					} else { // Jika error generik
						if httpErr.Internal.Error() != tc.expectedInternal.Error() && !errors.Is(httpErr.Internal, tc.expectedInternal) {
							t.Errorf("Expected internal error '%v', got '%v'", tc.expectedInternal, httpErr.Internal)
						}
					}
				}
			} else if httpErr.Internal != nil {
				t.Errorf("Expected no internal error, but got: %v", httpErr.Internal)
			}
		})
	}
}

func TestHTTPError_ErrorMethod(t *testing.T) {
	testCases := []struct {
		name           string
		httpErr        *xylium.HTTPError
		expectedString string
	}{
		{
			name:           "Error with no internal",
			httpErr:        xylium.NewHTTPError(http.StatusNotFound, "Resource not found"),
			expectedString: "xylium.HTTPError: code=404, message=Resource not found",
		},
		{
			name: "Error with internal error",
			httpErr: xylium.NewHTTPError(http.StatusInternalServerError, "Database query failed").
				WithInternal(errors.New("connection refused")),
			expectedString: `xylium.HTTPError: code=500, message=Database query failed, internal_error="connection refused"`,
		},
		{
			name:           "Error with internal error same as message",
			httpErr:        xylium.NewHTTPError(http.StatusBadRequest, errors.New("bad input")),
			expectedString: `xylium.HTTPError: code=400, message=bad input (internal error is same as message)`,
		},
		{
			name: "Error with xylium.M message and internal error",
			httpErr: xylium.NewHTTPError(http.StatusUnprocessableEntity, xylium.M{"field": "email"}).
				WithInternal(errors.New("validation rule failed")),
			expectedString: `xylium.HTTPError: code=422, message=map[field:email], internal_error="validation rule failed"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.httpErr.Error() != tc.expectedString {
				t.Errorf("Error() expected '%s', got '%s'", tc.expectedString, tc.httpErr.Error())
			}
		})
	}
}

func TestHTTPError_Unwrap(t *testing.T) {
	originalErr := errors.New("original cause")
	httpErrWithInternal := xylium.NewHTTPError(500).WithInternal(originalErr)
	httpErrNoInternal := xylium.NewHTTPError(404)

	// Test Unwrap with internal error
	unwrapped := errors.Unwrap(httpErrWithInternal)
	if unwrapped != originalErr {
		t.Errorf("Unwrap() expected to return original internal error, got %v", unwrapped)
	}

	// Test errors.Is with internal error
	if !errors.Is(httpErrWithInternal, originalErr) {
		t.Errorf("errors.Is expected to be true for original internal error")
	}

	// Test Unwrap with no internal error
	unwrappedNil := errors.Unwrap(httpErrNoInternal)
	if unwrappedNil != nil {
		t.Errorf("Unwrap() expected to return nil when no internal error, got %v", unwrappedNil)
	}
}

func TestHTTPError_WithInternal(t *testing.T) {
	httpErr := xylium.NewHTTPError(http.StatusConflict, "Conflict occurred")
	internalErr := errors.New("specific conflict reason")

	returnedErr := httpErr.WithInternal(internalErr)
	if returnedErr != httpErr {
		t.Errorf("WithInternal() should return the same *HTTPError instance")
	}
	if httpErr.Internal != internalErr {
		t.Errorf("WithInternal() did not set the internal error correctly")
	}

	// Test chaining
	anotherInternal := errors.New("another reason")
	httpErr.WithInternal(errors.New("first reason")).WithInternal(anotherInternal)
	if httpErr.Internal != anotherInternal {
		t.Errorf("Chained WithInternal() did not set the final internal error correctly")
	}
}

func TestHTTPError_WithMessage(t *testing.T) {
	httpErr := xylium.NewHTTPError(http.StatusPaymentRequired) // Message akan default ke "Payment Required"
	originalMessage := httpErr.Message.(string)

	// Test with new string message
	newMessageStr := "Subscription expired"
	returnedErr := httpErr.WithMessage(newMessageStr)
	if returnedErr != httpErr {
		t.Errorf("WithMessage() should return the same *HTTPError instance")
	}
	if httpErr.Message.(string) != newMessageStr {
		t.Errorf("WithMessage() did not set string message correctly. Expected '%s', got '%s'", newMessageStr, httpErr.Message.(string))
	}

	// Test with new map message
	newMapMessage := xylium.M{"error_code": "SUB_001", "details": "Please renew"}
	httpErr.WithMessage(newMapMessage)
	if _, ok := httpErr.Message.(xylium.M); !ok {
		t.Errorf("WithMessage() did not set map message correctly. Got type %T", httpErr.Message)
	}
	// Anda bisa menambahkan perbandingan map yang lebih detail di sini jika perlu

	// Test with nil message (should revert to default for code)
	httpErr.WithMessage(nil)
	if httpErr.Message.(string) != originalMessage { // Harus kembali ke "Payment Required"
		t.Errorf("WithMessage(nil) did not revert to default message. Expected '%s', got '%s'", originalMessage, httpErr.Message.(string))
	}

	// Test with empty string message (should revert to default for code)
	httpErr.WithMessage("")
	if httpErr.Message.(string) != originalMessage {
		t.Errorf("WithMessage(\"\") did not revert to default message. Expected '%s', got '%s'", originalMessage, httpErr.Message.(string))
	}
}

func TestIsHTTPError(t *testing.T) {
	httpErr400 := xylium.NewHTTPError(http.StatusBadRequest, "Bad request")
	httpErr404 := xylium.NewHTTPError(http.StatusNotFound, "Not found")
	genericErr := errors.New("generic error")

	// Test: Is HTTPError, no code check
	if !xylium.IsHTTPError(httpErr400) {
		t.Errorf("IsHTTPError(httpErr400) expected true, got false")
	}
	if xylium.IsHTTPError(genericErr) {
		t.Errorf("IsHTTPError(genericErr) expected false, got true")
	}
	if xylium.IsHTTPError(nil) {
		t.Errorf("IsHTTPError(nil) expected false, got true")
	}

	// Test: Is HTTPError, with matching code check
	if !xylium.IsHTTPError(httpErr400, http.StatusBadRequest) {
		t.Errorf("IsHTTPError(httpErr400, 400) expected true, got false")
	}

	// Test: Is HTTPError, with non-matching code check
	if xylium.IsHTTPError(httpErr400, http.StatusNotFound) {
		t.Errorf("IsHTTPError(httpErr400, 404) expected false, got true")
	}

	// Test: Error is not HTTPError, with code check (should be false)
	if xylium.IsHTTPError(genericErr, http.StatusInternalServerError) {
		t.Errorf("IsHTTPError(genericErr, 500) expected false, got true")
	}

	// Test: Wrapped HTTPError
	wrappedHTTPErr := fmt.Errorf("wrapped: %w", httpErr404)
	if !xylium.IsHTTPError(wrappedHTTPErr) {
		t.Errorf("IsHTTPError(wrappedHTTPErr) expected true (due to unwrap), got false")
	}
	if !xylium.IsHTTPError(wrappedHTTPErr, http.StatusNotFound) {
		t.Errorf("IsHTTPError(wrappedHTTPErr, 404) expected true, got false")
	}
	if xylium.IsHTTPError(wrappedHTTPErr, http.StatusBadRequest) {
		t.Errorf("IsHTTPError(wrappedHTTPErr, 400) expected false, got true")
	}
}
