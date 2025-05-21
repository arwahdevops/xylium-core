package xylium_test

import (
	// "fmt" // Dihapus karena tidak ada penggunaan langsung fmt.xxx
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
	// Ganti path ini sesuai dengan module path Anda
	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/stretchr/testify/assert" // Opsional
)

// Helper menggunakan fungsi yang diekspor dari xylium
func newTestContextWithParams(params map[string]string) *xylium.Context {
	return xylium.NewContextForTest(params, nil)
}

// Untuk pengujian QueryParam, kita bisa mengisi fasthttpCtx
func newTestContextWithQuery(queryValues map[string]string) *xylium.Context {
	var fasthttpCtx fasthttp.RequestCtx
	if queryValues != nil {
		for k, v := range queryValues {
			fasthttpCtx.QueryArgs().Set(k, v)
		}
	}
	return xylium.NewContextForTest(nil, &fasthttpCtx)
}

func TestContext_Param(t *testing.T) {
	testCases := []struct {
		name        string
		params      map[string]string
		paramToGet  string
		expectedVal string
	}{
		{
			name:        "Parameter Exists",
			params:      map[string]string{"id": "123", "name": "xylium"},
			paramToGet:  "id",
			expectedVal: "123",
		},
		{
			name:        "Another Parameter Exists",
			params:      map[string]string{"id": "123", "name": "xylium"},
			paramToGet:  "name",
			expectedVal: "xylium",
		},
		{
			name:        "Parameter Not Exists",
			params:      map[string]string{"id": "123"},
			paramToGet:  "status",
			expectedVal: "",
		},
		{
			name:        "Empty Params Map",
			params:      map[string]string{},
			paramToGet:  "id",
			expectedVal: "",
		},
		{
			name:        "Nil Params Map (di-handle oleh helper)",
			params:      nil,
			paramToGet:  "id",
			expectedVal: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContextWithParams(tc.params)
			val := ctx.Param(tc.paramToGet)

			if val != tc.expectedVal {
				t.Errorf("Param(%s): expected '%s', got '%s'", tc.paramToGet, tc.expectedVal, val)
			}
		})
	}
}

func TestContext_ParamInt(t *testing.T) {
	testCases := []struct {
		name                      string
		params                    map[string]string
		paramToGet                string
		expectedVal               int
		expectError               bool
		expectedErrorMsgSubstring string
	}{
		{
			name:        "Valid Integer Parameter",
			params:      map[string]string{"age": "30"},
			paramToGet:  "age",
			expectedVal: 30,
			expectError: false,
		},
		{
			name:        "Valid Negative Integer Parameter",
			params:      map[string]string{"offset": "-5"},
			paramToGet:  "offset",
			expectedVal: -5,
			expectError: false,
		},
		{
			name:                      "Parameter Not Exists",
			params:                    map[string]string{"id": "123"},
			paramToGet:                "count",
			expectedVal:               0,
			expectError:               true,
			expectedErrorMsgSubstring: "route parameter 'count' not found",
		},
		{
			name:                      "Parameter Not An Integer",
			params:                    map[string]string{"id": "abc"},
			paramToGet:                "id",
			expectedVal:               0,
			expectError:               true,
			expectedErrorMsgSubstring: "is not a valid integer",
		},
		{
			name:                      "Parameter Is Empty String",
			params:                    map[string]string{"id": ""},
			paramToGet:                "id",
			expectedVal:               0,
			expectError:               true,
			expectedErrorMsgSubstring: "is not a valid integer",
		},
		{
			name:                      "Parameter Is Float String",
			params:                    map[string]string{"id": "12.3"},
			paramToGet:                "id",
			expectedVal:               0,
			expectError:               true,
			expectedErrorMsgSubstring: "is not a valid integer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContextWithParams(tc.params)
			val, err := ctx.ParamInt(tc.paramToGet)

			if tc.expectError {
				if err == nil {
					t.Errorf("ParamInt(%s): expected an error, but got nil", tc.paramToGet)
				} else if tc.expectedErrorMsgSubstring != "" && !strings.Contains(err.Error(), tc.expectedErrorMsgSubstring) {
					t.Errorf("ParamInt(%s): error message '%s' does not contain substring '%s'", tc.paramToGet, err.Error(), tc.expectedErrorMsgSubstring)
				}
			} else {
				if err != nil {
					t.Errorf("ParamInt(%s): expected no error, but got: %v", tc.paramToGet, err)
				}
				if val != tc.expectedVal {
					t.Errorf("ParamInt(%s): expected value %d, got %d", tc.paramToGet, tc.expectedVal, val)
				}
			}
		})
	}
}

func TestContext_ParamIntDefault(t *testing.T) {
	defaultValue := 999
	testCases := []struct {
		name        string
		params      map[string]string
		paramToGet  string
		expectedVal int
	}{
		{
			name:        "Valid Integer Parameter",
			params:      map[string]string{"limit": "10"},
			paramToGet:  "limit",
			expectedVal: 10,
		},
		{
			name:        "Parameter Not Exists",
			params:      map[string]string{"id": "123"},
			paramToGet:  "limit",
			expectedVal: defaultValue,
		},
		{
			name:        "Parameter Not An Integer",
			params:      map[string]string{"limit": "abc"},
			paramToGet:  "limit",
			expectedVal: defaultValue,
		},
		{
			name:        "Parameter Is Empty String",
			params:      map[string]string{"limit": ""},
			paramToGet:  "limit",
			expectedVal: defaultValue,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContextWithParams(tc.params)
			val := ctx.ParamIntDefault(tc.paramToGet, defaultValue)

			if val != tc.expectedVal {
				t.Errorf("ParamIntDefault(%s): expected %d, got %d", tc.paramToGet, tc.expectedVal, val)
			}
		})
	}
}

func TestContext_QueryParam(t *testing.T) {
	testCases := []struct {
		name        string
		query       map[string]string
		paramToGet  string
		expectedVal string
	}{
		{
			name:        "Query Parameter Exists",
			query:       map[string]string{"search": "xylium", "page": "2"},
			paramToGet:  "search",
			expectedVal: "xylium",
		},
		{
			name:        "Query Parameter Not Exists",
			query:       map[string]string{"search": "xylium"},
			paramToGet:  "limit",
			expectedVal: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContextWithQuery(tc.query)
			val := ctx.QueryParam(tc.paramToGet)
			if val != tc.expectedVal {
				t.Errorf("QueryParam(%s): expected '%s', got '%s'", tc.paramToGet, tc.expectedVal, val)
			}
		})
	}
}
