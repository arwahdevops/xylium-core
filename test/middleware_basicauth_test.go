// File: /test/middleware_basicauth_test.go
package xylium_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/valyala/fasthttp"
)

// Helper untuk menjalankan middleware BasicAuth
type basicAuthTestResult struct {
	handlerCalled  bool
	contextUser    interface{}
	contextUserKey string
	statusCode     int
	wwwAuthHeader  string
	responseBody   string
	nextError      error
}

func runBasicAuthMiddleware(
	t *testing.T,
	config xylium.BasicAuthConfig,
	authHeaderValue string,
	useDefaultErrorHandler bool,
) basicAuthTestResult {

	var fasthttpCtx fasthttp.RequestCtx
	if authHeaderValue != "" {
		fasthttpCtx.Request.Header.Set("Authorization", authHeaderValue)
	}

	router := xylium.NewRouterForTesting()
	ctx := xylium.NewContextForTest(nil, &fasthttpCtx)
	ctx.SetRouterForTesting(router)

	result := basicAuthTestResult{}
	result.contextUserKey = config.ContextUserKey
	if result.contextUserKey == "" {
		result.contextUserKey = "user"
	}

	dummyHandler := func(c *xylium.Context) error {
		result.handlerCalled = true
		userVal, exists := c.Get(result.contextUserKey)
		if exists {
			result.contextUser = userVal
		}
		return nil
	}

	var mw xylium.Middleware
	if config.Validator == nil {
		panic("Validator function must be provided for BasicAuth testing via config")
	}
	mw = xylium.BasicAuthWithConfig(config)

	handlerWithMiddleware := mw(dummyHandler)
	err := handlerWithMiddleware(ctx)
	result.nextError = err

	result.statusCode = fasthttpCtx.Response.StatusCode()
	result.wwwAuthHeader = string(fasthttpCtx.Response.Header.Peek("WWW-Authenticate"))
	result.responseBody = string(fasthttpCtx.Response.Body())

	if useDefaultErrorHandler && err != nil {
		var httpErr *xylium.HTTPError
		if errors.As(err, &httpErr) {
			result.statusCode = httpErr.Code
		}
	}
	return result
}

// --- Test Cases ---

func TestBasicAuth_Success(t *testing.T) {
	validUser := "testuser"
	validPass := "testpass"
	expectedUserInfo := map[string]string{"username": validUser, "role": "tester"}

	config := xylium.BasicAuthConfig{
		Validator: func(username, password string, c *xylium.Context) (interface{}, bool, error) {
			if username == validUser && password == validPass {
				return expectedUserInfo, true, nil
			}
			return nil, false, nil
		},
	}

	authValue := "Basic " + base64.StdEncoding.EncodeToString([]byte(validUser+":"+validPass))
	result := runBasicAuthMiddleware(t, config, authValue, true)

	if !result.handlerCalled {
		t.Error("Expected dummy handler to be called on successful auth")
	}
	if result.statusCode != 0 && result.statusCode != http.StatusOK {
		if result.statusCode >= 400 {
			t.Errorf("Expected status code indicating success (e.g. 0 or 200), got %d", result.statusCode)
		}
	}
	if result.contextUser == nil {
		t.Fatal("Expected user info in context, got nil")
	}
	userInfo, ok := result.contextUser.(map[string]string)
	if !ok {
		t.Fatalf("User info in context is not of expected type map[string]string, got %T", result.contextUser)
	}
	if userInfo["username"] != validUser || userInfo["role"] != "tester" {
		t.Errorf("User info in context is incorrect. Expected %v, got %v", expectedUserInfo, userInfo)
	}
	if result.nextError != nil {
		t.Errorf("Expected no error from middleware chain on success, got %v", result.nextError)
	}
}

func TestBasicAuth_Failures(t *testing.T) {
	alwaysValidValidator := func(username, password string, c *xylium.Context) (interface{}, bool, error) {
		return "valid_user_data", true, nil
	}

	alwaysFailValidator := func(username, password string, c *xylium.Context) (interface{}, bool, error) {
		return nil, false, nil
	}

	testCases := []struct {
		name                string
		authHeader          string
		validator           func(username, password string, c *xylium.Context) (interface{}, bool, error)
		expectedStatusCode  int
		expectHandlerCalled bool
		expectWWWAuth       bool
		realm               string
	}{
		{
			name:                "NoAuthorizationHeader",
			authHeader:          "",
			validator:           alwaysValidValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "Restricted",
		},
		{
			name:                "NotBasicScheme",
			authHeader:          "Bearer sometoken",
			validator:           alwaysValidValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "Restricted",
		},
		{
			name:                "InvalidBase64",
			authHeader:          "Basic invalid-base64-$$",
			validator:           alwaysValidValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "Restricted",
		},
		{
			name:                "MalformedCredentials_NoColon",
			authHeader:          "Basic " + base64.StdEncoding.EncodeToString([]byte("useronly")),
			validator:           alwaysValidValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "Restricted",
		},
		{
			name:                "InvalidCredentials_ValidatorFails",
			authHeader:          "Basic " + base64.StdEncoding.EncodeToString([]byte("wronguser:wrongpass")),
			validator:           alwaysFailValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "Restricted",
		},
		{
			name:                "CustomRealmInWWWAuth",
			authHeader:          "",
			validator:           alwaysValidValidator,
			expectedStatusCode:  http.StatusUnauthorized,
			expectHandlerCalled: false,
			expectWWWAuth:       true,
			realm:               "MySuperSecureApp",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := xylium.BasicAuthConfig{
				Validator: tc.validator,
				Realm:     tc.realm,
			}

			result := runBasicAuthMiddleware(t, config, tc.authHeader, true)

			if result.handlerCalled != tc.expectHandlerCalled {
				t.Errorf("Expected handlerCalled to be %t, got %t", tc.expectHandlerCalled, result.handlerCalled)
			}
			if result.statusCode != tc.expectedStatusCode {
				errMsg := ""
				if result.nextError != nil {
					errMsg = fmt.Sprintf(" (middleware error: %v)", result.nextError)
				}
				t.Errorf("Expected status code %d, got %d%s", tc.expectedStatusCode, result.statusCode, errMsg)
			}
			if tc.expectWWWAuth {
				expectedRealm := config.Realm
				if expectedRealm == "" {
					expectedRealm = "Restricted"
				}
				expectedWWWAuthValue := fmt.Sprintf(`Basic realm="%s"`, expectedRealm)
				if result.wwwAuthHeader != expectedWWWAuthValue {
					t.Errorf("Expected WWW-Authenticate header '%s', got '%s'", expectedWWWAuthValue, result.wwwAuthHeader)
				}
			} else if result.wwwAuthHeader != "" {
				t.Errorf("Expected no WWW-Authenticate header, got '%s'", result.wwwAuthHeader)
			}
		})
	}
}

func TestBasicAuth_ValidatorError(t *testing.T) {
	dbError := errors.New("simulated database connection error")
	config := xylium.BasicAuthConfig{
		Validator: func(username, password string, c *xylium.Context) (interface{}, bool, error) {
			return nil, false, dbError
		},
	}

	authValue := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	result := runBasicAuthMiddleware(t, config, authValue, false)

	if result.handlerCalled {
		t.Error("Expected dummy handler NOT to be called when validator returns an error")
	}

	if result.nextError == nil {
		t.Fatal("Expected an error from middleware chain when validator returns an error, got nil")
	}

	var httpErr *xylium.HTTPError
	if !errors.As(result.nextError, &httpErr) {
		t.Fatalf("Expected error from chain to be *xylium.HTTPError, got %T", result.nextError)
	}

	if httpErr.Code != http.StatusInternalServerError {
		t.Errorf("Expected HTTPError code %d, got %d", http.StatusInternalServerError, httpErr.Code)
	}
	if !errors.Is(httpErr.Internal, dbError) {
		t.Errorf("Expected HTTPError internal error to be '%v', got '%v'", dbError, httpErr.Internal)
	}
}

func TestBasicAuth_CustomContextUserKey(t *testing.T) {
	customKey := "auth_principal"
	expectedUser := "test_user_id_123"
	config := xylium.BasicAuthConfig{
		Validator: func(username, password string, c *xylium.Context) (interface{}, bool, error) {
			return expectedUser, true, nil
		},
		ContextUserKey: customKey,
	}

	authValue := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	result := runBasicAuthMiddleware(t, config, authValue, true)

	if !result.handlerCalled {
		t.Error("Handler was not called")
	}
	if result.contextUserKey != customKey {
		t.Errorf("Test setup error: contextUserKey in result should be '%s', got '%s'", customKey, result.contextUserKey)
	}
	if result.contextUser == nil {
		t.Fatal("Expected user info in context, got nil")
	}
	if result.contextUser.(string) != expectedUser {
		t.Errorf("Expected user info '%s' in context with key '%s', got '%s'", expectedUser, customKey, result.contextUser.(string))
	}
}

func TestBasicAuth_CustomErrorHandler(t *testing.T) {
	customErrorMessageVar := "Custom Auth Error: Access Denied!" // Simpan di variabel
	customStatusCode := http.StatusProxyAuthRequired

	config := xylium.BasicAuthConfig{
		Validator: func(username, password string, c *xylium.Context) (interface{}, bool, error) {
			return nil, false, nil
		},
		ErrorHandler: func(c *xylium.Context) error {
			// Gunakan string literal untuk format jika ada argumen,
			// atau langsung jika tidak ada argumen format.
			// Karena customErrorMessageVar tidak punya %s, %d, dll., kita bisa:
			return c.String(customStatusCode, "%s", customErrorMessageVar)
			// atau jika c.String bisa menerima non-format string:
			// return c.String(customStatusCode, customErrorMessageVar)
			// Mari kita asumsikan yang pertama lebih aman untuk linter.
		},
	}

	authValue := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	result := runBasicAuthMiddleware(t, config, authValue, false)

	if result.handlerCalled {
		t.Error("Expected handler NOT to be called when custom error handler is used and auth fails")
	}
	if result.nextError != nil {
		t.Errorf("Expected no error from middleware chain when custom error handler sends response, got: %v", result.nextError)
	}
	if result.statusCode != customStatusCode {
		t.Errorf("Expected custom error handler to set status code %d, got %d", customStatusCode, result.statusCode)
	}
	if result.responseBody != customErrorMessageVar { // Bandingkan dengan variabel
		t.Errorf("Expected custom error handler to set body '%s', got '%s'", customErrorMessageVar, result.responseBody)
	}
	if result.wwwAuthHeader != "" {
		t.Errorf("Expected no WWW-Authenticate header from custom error handler, got '%s'", result.wwwAuthHeader)
	}
}
