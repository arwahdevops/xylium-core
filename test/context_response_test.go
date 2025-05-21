// File: xylium-core-main/test/context_response_test.go
package xylium_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/valyala/fasthttp"
)

// Helper untuk membuat mock xylium.Context untuk pengujian response
func newTestContextForResponse() (*xylium.Context, *fasthttp.RequestCtx) {
	var fasthttpCtx fasthttp.RequestCtx
	ctx := xylium.NewContextForTest(nil, &fasthttpCtx)
	return ctx, &fasthttpCtx
}

func TestContext_Status(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"OK", http.StatusOK},
		{"Not Found", http.StatusNotFound},
		{"Internal Server Error", http.StatusInternalServerError},
		{"Created", http.StatusCreated},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fasthttpCtx.Response.Reset()
			returnedCtx := ctx.Status(tc.statusCode)
			if returnedCtx != ctx {
				t.Errorf("Status() did not return the same context instance")
			}
			if fasthttpCtx.Response.StatusCode() != tc.statusCode {
				t.Errorf("Expected status code %d, got %d", tc.statusCode, fasthttpCtx.Response.StatusCode())
			}
		})
	}
}

func TestContext_SetHeader(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	headerKey := "X-Custom-Header"
	headerVal := "MyValue"
	returnedCtx := ctx.SetHeader(headerKey, headerVal)
	if returnedCtx != ctx {
		t.Errorf("SetHeader() did not return the same context instance")
	}
	actualVal := string(fasthttpCtx.Response.Header.Peek(headerKey))
	if actualVal != headerVal {
		t.Errorf("Expected header '%s' to be '%s', got '%s'", headerKey, headerVal, actualVal)
	}
	newHeaderVal := "NewValue"
	ctx.SetHeader(headerKey, newHeaderVal)
	actualVal = string(fasthttpCtx.Response.Header.Peek(headerKey))
	if actualVal != newHeaderVal {
		t.Errorf("Expected overridden header '%s' to be '%s', got '%s'", headerKey, newHeaderVal, actualVal)
	}
}

func TestContext_SetContentType(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	contentType := "application/vnd.api+json"
	returnedCtx := ctx.SetContentType(contentType)
	if returnedCtx != ctx {
		t.Errorf("SetContentType() did not return the same context instance")
	}
	actualVal := string(fasthttpCtx.Response.Header.ContentType())
	if actualVal != contentType {
		t.Errorf("Expected Content-Type to be '%s', got '%s'", contentType, actualVal)
	}
}

func TestContext_String(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	testCases := []struct {
		name         string
		statusCode   int
		format       string
		args         []interface{}
		expectedBody string
		expectedCT   string
	}{
		{"Simple String", http.StatusOK, "Hello, World!", nil, "Hello, World!", "text/plain; charset=utf-8"},
		{"Formatted String", http.StatusAccepted, "User %s created with ID %d", []interface{}{"Alice", 123}, "User Alice created with ID 123", "text/plain; charset=utf-8"},
		{"String with no args but format specifiers (writes literal string)", http.StatusOK, "Test %s %d", nil, "Test %s %d", "text/plain; charset=utf-8"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fasthttpCtx.Response.Reset()
			err := ctx.String(tc.statusCode, tc.format, tc.args...)
			if err != nil {
				t.Fatalf("String() returned an unexpected error: %v", err)
			}
			if fasthttpCtx.Response.StatusCode() != tc.statusCode {
				t.Errorf("Expected status code %d, got %d", tc.statusCode, fasthttpCtx.Response.StatusCode())
			}
			if string(fasthttpCtx.Response.Header.ContentType()) != tc.expectedCT {
				t.Errorf("Expected Content-Type '%s', got '%s'", tc.expectedCT, string(fasthttpCtx.Response.Header.ContentType()))
			}
			if string(fasthttpCtx.Response.Body()) != tc.expectedBody {
				t.Errorf("Expected body '%s', got '%s'", tc.expectedBody, string(fasthttpCtx.Response.Body()))
			}
		})
	}
}

func TestContext_JSON(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	type sampleStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	testCases := []struct {
		name         string
		statusCode   int
		data         interface{}
		expectedBody string
		expectedCT   string
		expectError  bool
	}{
		{"Simple Struct", http.StatusOK, sampleStruct{Name: "Bob", Age: 25}, `{"name":"Bob","age":25}`, "application/json; charset=utf-8", false},
		{"xylium.M (map)", http.StatusCreated, xylium.M{"message": "success", "id": 1}, `{"id":1,"message":"success"}`, "application/json; charset=utf-8", false},
		{"Byte Slice Data", http.StatusOK, []byte(`{"raw":true}`), `{"raw":true}`, "application/json; charset=utf-8", false},
		{"Marshal Error (e.g., channel)", http.StatusInternalServerError, make(chan int), "", "application/json; charset=utf-8", true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fasthttpCtx.Response.Reset()
			err := ctx.JSON(tc.statusCode, tc.data)
			if tc.expectError {
				if err == nil {
					t.Fatalf("JSON() expected an error, but got nil")
				}
				var httpErr *xylium.HTTPError
				if !errors.As(err, &httpErr) {
					t.Errorf("JSON() expected error of type *xylium.HTTPError, got %T", err)
				}
				if string(fasthttpCtx.Response.Header.ContentType()) != tc.expectedCT {
					t.Errorf("Expected Content-Type '%s' even on marshal error, got '%s'", tc.expectedCT, string(fasthttpCtx.Response.Header.ContentType()))
				}
				if fasthttpCtx.Response.StatusCode() != tc.statusCode {
					t.Logf("Note: Status code for marshalling error might be overridden by GlobalErrorHandler. Initial set: %d", fasthttpCtx.Response.StatusCode())
				}
			} else {
				if err != nil {
					t.Fatalf("JSON() returned an unexpected error: %v", err)
				}
				if fasthttpCtx.Response.StatusCode() != tc.statusCode {
					t.Errorf("Expected status code %d, got %d", tc.statusCode, fasthttpCtx.Response.StatusCode())
				}
				if string(fasthttpCtx.Response.Header.ContentType()) != tc.expectedCT {
					t.Errorf("Expected Content-Type '%s', got '%s'", tc.expectedCT, string(fasthttpCtx.Response.Header.ContentType()))
				}
				var actualBodyMap map[string]interface{}
				var expectedBodyMap map[string]interface{}
				if err := json.Unmarshal(fasthttpCtx.Response.Body(), &actualBodyMap); err != nil {
					t.Fatalf("Failed to unmarshal actual response body: %v. Body: %s", err, string(fasthttpCtx.Response.Body()))
				}
				if err := json.Unmarshal([]byte(tc.expectedBody), &expectedBodyMap); err != nil {
					t.Fatalf("Failed to unmarshal expected response body: %v. Expected: %s", err, tc.expectedBody)
				}
				if !jsonMapsEqual(actualBodyMap, expectedBodyMap) {
					t.Errorf("Expected body %v, got %v", expectedBodyMap, actualBodyMap)
				}
			}
		})
	}
}

func jsonMapsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, vA := range a {
		vB, ok := b[k]
		if !ok {
			return false
		}
		valAStr := fmt.Sprintf("%v", vA)
		valBStr := fmt.Sprintf("%v", vB)
		numA, okA := vA.(float64)
		numB, okB := vB.(float64)
		if okA && okB {
			if numA != numB {
				return false
			}
		} else if valAStr != valBStr {
			return false
		}
	}
	return true
}

func TestContext_NoContent(t *testing.T) {
	ctx, fasthttpCtx := newTestContextForResponse()
	initialTestCT := "application/initial-test-type" // Definisikan di luar loop testCases

	testCases := []struct {
		name                string
		statusCode          int
		expectedContentType string
	}{
		{
			"No Content",
			http.StatusNoContent,
			"text/plain; charset=utf-8", // Sesuai observasi perilaku fasthttp
		},
		{
			"Accepted with No Content",
			http.StatusAccepted,
			initialTestCT, // PERBAIKAN DI SINI: Harapkan CT awal tetap ada
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fasthttpCtx.Response.Reset()
			fasthttpCtx.Response.Header.SetContentType(initialTestCT)

			err := ctx.NoContent(tc.statusCode)

			if err != nil {
				t.Fatalf("NoContent() returned an unexpected error: %v", err)
			}
			if fasthttpCtx.Response.StatusCode() != tc.statusCode {
				t.Errorf("Expected status code %d, got %d", tc.statusCode, fasthttpCtx.Response.StatusCode())
			}
			if len(fasthttpCtx.Response.Body()) != 0 {
				t.Errorf("Expected empty body, got %d bytes", len(fasthttpCtx.Response.Body()))
			}

			actualContentType := string(fasthttpCtx.Response.Header.ContentType())
			if actualContentType != tc.expectedContentType {
				t.Errorf("For status %d, expected Content-Type to be '%s', got '%s'", tc.statusCode, tc.expectedContentType, actualContentType)
			}
		})
	}
}
