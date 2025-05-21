// File: xylium-core-main/test/context_binding_test.go
package xylium_test

import (
	// "bytes" // Tidak digunakan secara langsung saat ini
	// "encoding/json" // Tidak digunakan secara langsung saat ini, xylium.Bind menangani
	// "encoding/xml"  // Akan dibutuhkan untuk tes XML binding
	"errors"
	// "mime/multipart" // Akan dibutuhkan untuk tes multipart form
	"net/http"
	"net/url" // Digunakan untuk query/form values
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	// Ganti path ini sesuai dengan module path Anda
	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/go-playground/validator/v10" // Akan dibutuhkan untuk tes custom validator
)

// --- Helper Structs untuk Binding ---
type BasicBindingStruct struct {
	Name string `json:"name" xml:"Name" form:"name" query:"name"`
	Age  int    `json:"age" xml:"Age" form:"age" query:"age"`
}

type ValidationStruct struct {
	RequiredField string `json:"required_field" validate:"required"`
	EmailField    string `json:"email_field" validate:"omitempty,email"`
	NumberField   int    `json:"number_field" validate:"gte=0,lte=100"`
	Nested        struct {
		InnerField string `json:"inner_field" validate:"required,min=3"`
	} `json:"nested"`
	SliceField []string `json:"slice_field" validate:"omitempty,dive,min=2"`
}

type CustomBindStruct struct {
	Message   string
	Processed bool
}

func (cbs *CustomBindStruct) Bind(c *xylium.Context) error {
	if c.Method() == "POST" && strings.HasPrefix(c.ContentType(), "application/custom") {
		bodyStr := string(c.Body())
		cbs.Message = "Custom bound: " + bodyStr
		cbs.Processed = true
		return nil
	}
	return xylium.NewHTTPError(http.StatusUnsupportedMediaType, "CustomBindStruct only supports POST with application/custom")
}

type AllTypesQueryFormStruct struct {
	StringField    string     `query:"s" form:"s"`
	IntField       int        `query:"i" form:"i"`
	BoolField      bool       `query:"b" form:"b"`
	FloatField     float64    `query:"f" form:"f"`
	TimeField      time.Time  `query:"t" form:"t"`
	StringSlice    []string   `query:"ss" form:"ss"`
	IntSlice       []int      `query:"is" form:"is"`
	PtrString      *string    `query:"ps" form:"ps"`
	PtrInt         *int       `query:"pi" form:"pi"`
	PtrBool        *bool      `query:"pb" form:"pb"`
	PtrTime        *time.Time `query:"pt" form:"pt"`
	PtrStringSlice []*string  `query:"pss" form:"pss"`
}

// --- Helper Functions untuk Test Binding ---

func newTestContextWithBody(method, path, contentType string, body []byte) *xylium.Context {
	var fasthttpCtx fasthttp.RequestCtx
	fasthttpCtx.Request.Header.SetMethod(method)
	fasthttpCtx.Request.SetRequestURI(path)
	if contentType != "" {
		fasthttpCtx.Request.Header.SetContentType(contentType)
	}
	if body != nil {
		fasthttpCtx.Request.SetBody(body)
		fasthttpCtx.Request.Header.SetContentLength(len(body))
	}
	return xylium.NewContextForTest(nil, &fasthttpCtx)
}

func newTestContextWithQueryForm(method, path string, queryValues url.Values, formValues url.Values) *xylium.Context {
	var fasthttpCtx fasthttp.RequestCtx
	fasthttpCtx.Request.Header.SetMethod(method)
	uri := path
	if queryValues != nil && len(queryValues) > 0 {
		uri = path + "?" + queryValues.Encode()
	}
	fasthttpCtx.Request.SetRequestURI(uri)
	if formValues != nil && len(formValues) > 0 {
		if method == "POST" || method == "PUT" || method == "PATCH" {
			fasthttpCtx.Request.Header.SetContentType("application/x-www-form-urlencoded")
			fasthttpCtx.Request.SetBodyString(formValues.Encode())
			fasthttpCtx.Request.Header.SetContentLength(len(fasthttpCtx.Request.Body()))
		}
	}
	return xylium.NewContextForTest(nil, &fasthttpCtx)
}

// --- Test Cases ---

func TestContext_Bind_JSON(t *testing.T) {
	t.Run("ValidJSON", func(t *testing.T) {
		jsonData := `{"name":"Xylium","age":1}`
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(jsonData))
		var data BasicBindingStruct
		err := ctx.Bind(&data)
		if err != nil {
			t.Fatalf("Bind() returned an unexpected error: %v", err)
		}
		if data.Name != "Xylium" || data.Age != 1 {
			t.Errorf("Expected data {Xylium, 1}, got %+v", data)
		}
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		jsonData := `{"name":"Xylium","age":1`
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(jsonData))
		var data BasicBindingStruct
		err := ctx.Bind(&data)
		if err == nil {
			t.Fatal("Bind() expected an error for malformed JSON, but got nil")
		}
		var httpErr *xylium.HTTPError
		if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
			t.Errorf("Expected HTTPError with status %d, got %v (type %T)", http.StatusBadRequest, err, err)
		}
	})

	t.Run("EmptyJSONBody", func(t *testing.T) {
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(""))
		var data BasicBindingStruct
		err := ctx.Bind(&data)
		if err != nil {
			t.Fatalf("Bind() with empty JSON body returned an unexpected error: %v", err)
		}
		if data.Name != "" || data.Age != 0 {
			t.Errorf("Expected empty struct, got %+v", data)
		}
	})

	t.Run("NilTarget", func(t *testing.T) {
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(`{}`))
		var data *BasicBindingStruct
		err := ctx.Bind(data)
		if err == nil {
			t.Fatal("Bind() expected an error for nil target, but got nil")
		}
	})

	t.Run("NonPointerTarget", func(t *testing.T) {
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(`{}`))
		var data BasicBindingStruct
		err := ctx.Bind(data)
		if err == nil {
			t.Fatal("Bind() expected an error for non-pointer target, but got nil")
		}
	})
}

func TestContext_BindAndValidate_JSON(t *testing.T) {
	t.Run("ValidData", func(t *testing.T) {
		jsonData := `{"required_field":"hello","email_field":"test@example.com","number_field":50,"nested":{"inner_field":"world"},"slice_field":["ab","cd"]}`
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(jsonData))
		var data ValidationStruct
		err := ctx.BindAndValidate(&data)
		if err != nil {
			t.Fatalf("BindAndValidate() returned an unexpected error: %v", err)
		}
		if data.RequiredField != "hello" || data.EmailField != "test@example.com" || data.NumberField != 50 || data.Nested.InnerField != "world" {
			t.Errorf("Data not bound correctly: %+v", data)
		}
		if len(data.SliceField) != 2 || data.SliceField[0] != "ab" || data.SliceField[1] != "cd" {
			t.Errorf("SliceField not bound correctly: %+v", data.SliceField)
		}
	})

	t.Run("ValidationError", func(t *testing.T) {
		jsonData := `{"email_field":"not-an-email","number_field":200,"nested":{"inner_field":"a"}}`
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(jsonData))
		var data ValidationStruct
		err := ctx.BindAndValidate(&data)
		if err == nil {
			t.Fatal("BindAndValidate() expected a validation error, but got nil")
		}
		var httpErr *xylium.HTTPError
		if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
			t.Fatalf("Expected HTTPError with status %d, got %v (type %T)", http.StatusBadRequest, err, err)
		}
		if details, ok := httpErr.Message.(xylium.M)["details"].(map[string]string); ok {
			if _, ok := details["RequiredField"]; !ok {
				t.Errorf("Expected validation error for RequiredField. Details: %v", details)
			}
			if _, ok := details["EmailField"]; !ok {
				t.Errorf("Expected validation error for EmailField. Details: %v", details)
			}
			if _, ok := details["NumberField"]; !ok {
				t.Errorf("Expected validation error for NumberField. Details: %v", details)
			}
			// Kunci DI SINI disesuaikan dengan fe.Namespace() yang dipotong di context_binding.go
			if _, ok := details["Nested.InnerField"]; !ok {
				t.Errorf("Expected validation error for Nested.InnerField. Details: %v", details)
			}
		} else {
			t.Errorf("Validation error details not found or not in expected format: %v", httpErr.Message)
		}
	})

	t.Run("EmptyJSONBodyWithRequiredFields", func(t *testing.T) {
		ctx := newTestContextWithBody("POST", "/test", "application/json", []byte(""))
		var data ValidationStruct
		err := ctx.BindAndValidate(&data)
		if err == nil {
			t.Fatal("BindAndValidate() with empty JSON body for struct with required fields expected an error, but got nil")
		}
		var httpErr *xylium.HTTPError
		if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
			t.Errorf("Expected HTTPError with status %d, got %v", http.StatusBadRequest, err)
		}
	})
}

func TestContext_Bind_XBind(t *testing.T) {
	ctx := newTestContextWithBody("POST", "/custom", "application/custom", []byte("payload"))
	var data CustomBindStruct
	err := ctx.Bind(&data)
	if err != nil {
		t.Fatalf("Bind() with XBind implementation returned an error: %v", err)
	}
	if !data.Processed || data.Message != "Custom bound: payload" {
		t.Errorf("XBind implementation did not process correctly: %+v", data)
	}

	ctxWrongCT := newTestContextWithBody("POST", "/custom", "application/json", []byte("payload"))
	var dataWrongCT CustomBindStruct
	errWrongCT := ctxWrongCT.Bind(&dataWrongCT)
	if errWrongCT == nil {
		t.Fatal("Bind() with XBind implementation expected an error for wrong Content-Type, got nil")
	}
	var httpErr *xylium.HTTPError
	if !errors.As(errWrongCT, &httpErr) || httpErr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Expected HTTPError with status %d for XBind wrong CT, got %v", http.StatusUnsupportedMediaType, errWrongCT)
	}
}

func TestContext_Bind_Query(t *testing.T) {
	t.Run("ValidQueryData", func(t *testing.T) {
		q := url.Values{}
		q.Set("name", "QueryUser")
		q.Set("age", "25")
		ctx := newTestContextWithQueryForm("GET", "/test", q, nil)
		var data BasicBindingStruct
		err := ctx.Bind(&data)
		if err != nil {
			t.Fatalf("Bind() from query returned an unexpected error: %v", err)
		}
		if data.Name != "QueryUser" || data.Age != 25 {
			t.Errorf("Expected data {QueryUser, 25} from query, got %+v", data)
		}
	})

	t.Run("ValidQueryDataAllTypes", func(t *testing.T) {
		q := url.Values{}
		q.Set("s", "hello")
		q.Set("i", "123")
		q.Set("b", "true")
		q.Set("f", "12.34")
		q.Set("t", "2023-10-27T15:04:05Z")
		q.Add("ss", "apple")
		q.Add("ss", "banana")
		q.Add("is", "1")
		q.Add("is", "2")
		q.Set("ps", "ptr_str")
		q.Set("pi", "789")
		q.Set("pb", "false")
		q.Set("pt", "2024-01-01")
		q.Add("pss", "ptr_val1")
		q.Add("pss", "ptr_val2")

		ctx := newTestContextWithQueryForm("GET", "/test", q, nil)
		var data AllTypesQueryFormStruct
		err := ctx.Bind(&data)
		if err != nil {
			t.Fatalf("Bind() from query (AllTypes) returned an unexpected error: %v", err)
		}
		if data.StringField != "hello" {
			t.Errorf("StringField: expected 'hello', got '%s'", data.StringField)
		}
		if data.IntField != 123 {
			t.Errorf("IntField: expected 123, got %d", data.IntField)
		}
		if data.BoolField != true {
			t.Errorf("BoolField: expected true, got %t", data.BoolField)
		}
		if data.FloatField != 12.34 {
			t.Errorf("FloatField: expected 12.34, got %f", data.FloatField)
		}
		expectedTime, _ := time.Parse(time.RFC3339, "2023-10-27T15:04:05Z")
		if !data.TimeField.Equal(expectedTime) {
			t.Errorf("TimeField: expected %v, got %v", expectedTime, data.TimeField)
		}
		if len(data.StringSlice) != 2 || data.StringSlice[0] != "apple" || data.StringSlice[1] != "banana" {
			t.Errorf("StringSlice: expected [apple banana], got %v", data.StringSlice)
		}
		if len(data.IntSlice) != 2 || data.IntSlice[0] != 1 || data.IntSlice[1] != 2 {
			t.Errorf("IntSlice: expected [1 2], got %v", data.IntSlice)
		}
		if data.PtrString == nil || *data.PtrString != "ptr_str" {
			t.Errorf("PtrString: expected 'ptr_str', got %v", data.PtrString)
		}
		if data.PtrInt == nil || *data.PtrInt != 789 {
			t.Errorf("PtrInt: expected 789, got %v", data.PtrInt)
		}
		if data.PtrBool == nil || *data.PtrBool != false {
			t.Errorf("PtrBool: expected false, got %v", data.PtrBool)
		}
		expectedPtrTime, _ := time.Parse("2006-01-02", "2024-01-01")
		if data.PtrTime == nil || !data.PtrTime.Equal(expectedPtrTime) {
			t.Errorf("PtrTime: expected %v, got %v", expectedPtrTime, data.PtrTime)
		}
		if len(data.PtrStringSlice) != 2 || data.PtrStringSlice[0] == nil || *data.PtrStringSlice[0] != "ptr_val1" || data.PtrStringSlice[1] == nil || *data.PtrStringSlice[1] != "ptr_val2" {
			// Perbaikan: derefString hanya untuk logging, perbandingan tetap pada pointer atau nilai dereferenced
			val1 := "<nil>"
			if data.PtrStringSlice[0] != nil {
				val1 = *data.PtrStringSlice[0]
			}
			val2 := "<nil>"
			if len(data.PtrStringSlice) > 1 && data.PtrStringSlice[1] != nil { // Check length before accessing index 1
				val2 = *data.PtrStringSlice[1]
			}
			t.Errorf("PtrStringSlice: expected [ptr_val1 ptr_val2], got actual values [%s %s] from pointers %v", val1, val2, data.PtrStringSlice)
		}

		qEmpty := url.Values{}
		qEmpty.Set("ps", "")
		qEmpty.Set("pi", "")
		qEmpty.Set("pb", "")
		qEmpty.Set("pt", "")
		ctxEmpty := newTestContextWithQueryForm("GET", "/test", qEmpty, nil)
		var dataEmpty AllTypesQueryFormStruct
		errEmpty := ctxEmpty.Bind(&dataEmpty)
		if errEmpty != nil {
			t.Fatalf("Bind() with empty query for pointers failed: %v", errEmpty)
		}
		if dataEmpty.PtrString == nil || *dataEmpty.PtrString != "" {
			t.Errorf("PtrString with empty input: expected pointer to '', got %v (value: %s)", dataEmpty.PtrString, derefString(dataEmpty.PtrString))
		}
		if dataEmpty.PtrInt != nil {
			t.Errorf("PtrInt with empty input: expected nil, got value %d", *dataEmpty.PtrInt)
		}
		if dataEmpty.PtrBool != nil {
			t.Errorf("PtrBool with empty input: expected nil, got value %t", *dataEmpty.PtrBool)
		}
		if dataEmpty.PtrTime != nil {
			t.Errorf("PtrTime with empty input: expected nil, got value %v", *dataEmpty.PtrTime)
		}
	})
}

func TestContext_Bind_Form(t *testing.T) {
	t.Run("ValidFormData", func(t *testing.T) {
		f := url.Values{}
		f.Set("name", "FormUser")
		f.Set("age", "35")
		ctx := newTestContextWithQueryForm("POST", "/submit", nil, f)
		var data BasicBindingStruct
		err := ctx.Bind(&data)
		if err != nil {
			t.Fatalf("Bind() from form returned an unexpected error: %v", err)
		}
		if data.Name != "FormUser" || data.Age != 35 {
			t.Errorf("Expected data {FormUser, 35} from form, got %+v", data)
		}
	})
}

// Helper untuk dereference string pointer dengan aman untuk logging
func derefString(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
