# Xylium Data Binding: From Request to Structs

Xylium provides powerful and flexible mechanisms for binding incoming request data (JSON, XML, form data, query parameters) to Go structs. This process often includes validation to ensure data integrity. Xylium offers two primary ways to achieve this: general-purpose binding using `c.Bind()` and `c.BindAndValidate()`, and individual, type-aware access to URL path and query parameters (e.g., `c.ParamInt()`, `c.QueryParamInt()`, detailed in `RequestHandling.md`).

## Table of Contents

*   [1. Basic Binding and Validation: `c.BindAndValidate()`](#1-basic-binding-and-validation-cbindandvalidate)
*   [2. Binding Only: `c.Bind()`](#2-binding-only-cbind)
*   [3. How Xylium Determines Binding Source (for `c.Bind()` and `c.BindAndValidate()`)](#3-how-xylium-determines-binding-source-for-cbind-and-cbindandvalidate)
    *   [3.1. Custom Binding with `XBind` Interface (High Performance/Control)](#31-custom-binding-with-xbind-interface-high-performancecontrol)
    *   [3.2. Reflection-Based Binding (Default Behavior for `c.Bind()`)](#32-reflection-based-binding-default-behavior-for-cbind)
        *   [Request Body (JSON, XML, Form)](#request-body-json-xml-form)
        *   [URL Query Parameters (for GET, DELETE, HEAD)](#url-query-parameters-for-get-delete-head)
        *   [Order of Precedence](#order-of-precedence)
*   [4. Supported Data Types for Reflection-Based Binding (via `c.Bind()`)](#4-supported-data-types-for-reflection-based-binding-via-cbind)
*   [5. Struct Tags for Reflection-Based Binding (via `c.Bind()`)](#5-struct-tags-for-reflection-based-binding-via-cbind)
    *   [`json:"fieldName"`](#jsonfieldname)
    *   [`xml:"fieldName"`](#xmlfieldname)
    *   [`form:"fieldName"`](#formfieldname)
    *   [`query:"fieldName"`](#queryfieldname)
    *   [Note on `default` tag](#note-on-default-tag)
*   [6. Validation](#6-validation)
    *   [Validation Tags](#validation-tags)
    *   [Handling Validation Errors](#handling-validation-errors)
*   [7. Custom Validator](#7-custom-validator)
*   [8. Examples Using `c.BindAndValidate()`](#8-examples-using-cbindandvalidate)
    *   [8.1. Binding JSON with Validation](#81-binding-json-with-validation)
    *   [8.2. Binding Query Parameters with Validation (for GET requests)](#82-binding-query-parameters-with-validation-for-get-requests)
    *   [8.3. Implementing `XBind` for Custom Binding Logic](#83-implementing-xbind-for-custom-binding-logic)

---

## 1. Basic Binding and Validation: `c.BindAndValidate()`

The most common way to handle request data is using `c.BindAndValidate(out interface{}) error`. This method performs two main operations:

1.  **Binding**: It attempts to populate the fields of the `out` struct (which must be a pointer) with data from the HTTP request. It uses the logic described in [Section 3](#3-how-xylium-determines-binding-source-for-cbind-and-cbindandvalidate).
2.  **Validation**: If binding is successful, it validates the populated struct using the `validate` tags on its fields (see [Section 6](#6-validation)).

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

type CreateUserInput struct {
	Username string `json:"username" validate:"required,min=3,max=50"`
	Email    string `json:"email"    validate:"required,email"`
	Age      int    `json:"age"      validate:"omitempty,gte=18"`
}

func CreateUserHandler(c *xylium.Context) error {
	var input CreateUserInput

	// BindAndValidate will try to bind based on Content-Type or method (e.g., JSON for POST, Query for GET)
	// and then validate the 'input' struct.
	if err := c.BindAndValidate(&input); err != nil {
		// err will be *xylium.HTTPError, typically with status xylium.StatusBadRequest.
		// The GlobalErrorHandler will handle logging and sending the client response.
		c.Logger().Warnf("Binding/validation failed for user creation: %v", err)
		return err
	}

	// If we reach here, input is bound and validated.
	// Process input...
	c.Logger().Infof("User successfully bound and validated: %+v", input)
	return c.JSON(xylium.StatusCreated, xylium.M{"message": "User created", "user": input})
}
```

If binding or validation fails, `c.BindAndValidate()` returns a `*xylium.HTTPError`. This error typically has an HTTP status code of `xylium.StatusBadRequest` and contains details about the failure.

## 2. Binding Only: `c.Bind()`

If you only need to bind data without immediate validation (perhaps validation is conditional or done later), you can use `c.Bind(out interface{}) error`.

```go
// Assume MyProfileUpdate struct is defined elsewhere
// type MyProfileUpdate struct {
//	NewPassword string `json:"new_password"`
//	// ... other fields without 'required' tags for partial updates
// }

func UpdateProfilePartialHandler(c *xylium.Context) error {
	var partialData MyProfileUpdate // Assume MyProfileUpdate has no 'required' tags

	if err := c.Bind(&partialData); err != nil { // Binding only
		// err will be *xylium.HTTPError if binding itself fails (e.g., malformed JSON).
		return err 
	}

	// Perform custom logic or conditional validation here...
	if partialData.NewPassword != "" && len(partialData.NewPassword) < 8 {
		return xylium.NewHTTPError(xylium.StatusBadRequest, "New password too short.")
	}

	// Update profile...
	return c.String(xylium.StatusOK, "Profile update processed (partially).")
}
```

## 3. How Xylium Determines Binding Source (for `c.Bind()` and `c.BindAndValidate()`)

Xylium's `c.Bind()` method (and by extension `c.BindAndValidate()`) uses a prioritized approach to determine how to populate the `out` struct:

### 3.1. Custom Binding with `XBind` Interface (High Performance/Control)

For maximum performance or highly specific binding requirements (e.g., parsing a custom binary format, using a faster JSON library), your struct can implement the `xylium.XBind` interface:

```go
package xylium // In xylium's types (src/xylium/context_binding.go)

// XBind is an interface that can be implemented by types
// to provide custom data binding logic.
type XBind interface {
	Bind(c *Context) error
}
```

If the struct passed to `c.Bind()` (or `c.BindAndValidate()`) implements this interface, Xylium will call its `Bind(c *xylium.Context) error` method. This gives you complete control over how the struct is populated from the request context.

**Example:**

```go
// In your models package
// package models

import (
	"encoding/json" // Or a faster JSON library like json-iterator
	"strings"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust path
)

type MyCustomPayload struct {
	FieldA string
	FieldB int
}

func (mcp *MyCustomPayload) Bind(c *xylium.Context) error {
	c.Logger().Debug("MyCustomPayload: Using custom binding logic via XBind.")
	// Example: Bind only from JSON, perhaps with specific error handling or library
	if strings.HasPrefix(c.ContentType(), "application/json") {
		// For instance, using jsoniter for speed:
		// return jsoniter.Unmarshal(c.Body(), mcp)
		return json.Unmarshal(c.Body(), mcp) // Standard library for this example
	}
	// Could return an error if content type is not supported by this custom binder
	return xylium.NewHTTPError(xylium.StatusUnsupportedMediaType, "MyCustomPayload only supports JSON via XBind.")
}
```
When `c.Bind(&MyCustomPayload{})` is called, the `MyCustomPayload.Bind()` method will be executed. Refer to [Section 8.3](#83-implementing-xbind-for-custom-binding-logic) for more.

### 3.2. Reflection-Based Binding (Default Behavior for `c.Bind()`)

If the struct does *not* implement `XBind`, Xylium falls back to its default reflection-based binding mechanism. The source of data depends on the HTTP method and `Content-Type` header:

#### Request Body (JSON, XML, Form)

For HTTP methods like `POST`, `PUT`, `PATCH` (which typically have a request body):

*   **JSON (`application/json`)**: If `Content-Type` is `application/json`, Xylium attempts to unmarshal the request body as JSON into the struct. Uses struct tags like `json:"fieldName"`.
*   **XML (`application/xml`, `text/xml`)**: If `Content-Type` is XML, it unmarshals the XML body. Uses struct tags like `xml:"fieldName"`.
*   **Form Data (`application/x-www-form-urlencoded`, `multipart/form-data`)**: If `Content-Type` indicates form data, Xylium populates the struct from form fields (from the request body). Uses struct tags like `form:"fieldName"`. File uploads are handled separately using `c.FormFile()` or `c.MultipartForm()` (see `RequestHandling.md`).

#### URL Query Parameters (for GET, DELETE, HEAD)

For HTTP methods like `GET`, `DELETE`, `HEAD` (which typically don't have a request body, or where the body isn't the primary data source for binding to a simple struct):

*   Xylium attempts to populate the struct fields from URL query parameters.
*   Uses struct tags like `query:"fieldName"`.
    *Example*: `GET /search?name=xylium&page=1`

#### Order of Precedence

1.  **`XBind` implementation**: If present, this takes full control.
2.  **Reflection-based on method and `Content-Type`**:
    *   For `GET`, `DELETE`, `HEAD`: Primarily **URL Query Parameters**.
    *   For `POST`, `PUT`, `PATCH`: Based on **`Content-Type`** (JSON > XML > Form Data from body).

If a `POST` request has `Content-Length: 0` (no body) and `c.Bind()` is called on a struct, the binding operation itself will succeed (resulting in a zero-value struct). Subsequent validation (e.g., `required` tags via `c.BindAndValidate()`) will then determine if this is acceptable.

**Note on Form Data vs. Query Parameters:**
While `c.FormValue()` accesses both query and POST form values, Xylium's `c.Bind()` method distinguishes:
*   For `GET`/`DELETE`/`HEAD`, it binds from query parameters using `query` tags.
*   For `POST`/`PUT`/`PATCH` with form content types, it binds from the request body's form fields using `form` tags.

### 4. Supported Data Types for Reflection-Based Binding (via `c.Bind()`)

The reflection-based binding from query or form data supports:

*   Basic types: `string`, `int` (and its variants `int8`, `int16`, `int32`, `int64`), `uint` (and its variants), `bool`, `float32`, `float64`.
*   `time.Time`: Parses RFC3339 format (e.g., "2006-01-02T15:04:05Z07:00") and "YYYY-MM-DD" (e.g., "2023-10-26").
*   Pointers to these types (e.g., `*string`, `*int`).
    *   **Important:** If the request data for a pointer field is an empty string and the underlying type is **not `string`** (e.g., `*int`, `*bool`), the pointer will remain `nil`. This helps differentiate "not provided/empty" from "provided as zero/false". For `*string`, an empty string value results in a pointer to an empty string.
*   Slices of these types (e.g., `[]string`, `[]int`). For query/form, this is typically used when a parameter is repeated (e.g., `?ids=1&ids=2`).
*   Slices of pointers to these types (e.g. `[]*int`).

For JSON and XML binding, any type supported by `encoding/json` and `encoding/xml` respectively can be used.

## 5. Struct Tags for Reflection-Based Binding (via `c.Bind()`)

Struct tags control how fields are mapped from different data sources during reflection-based binding with `c.Bind()`.

### `json:"fieldName"`
Used when binding from a JSON request body (`application/json`).
```go
type MyData struct {
	ProductName  string `json:"product_name"`
	ProductPrice int    `json:"price,omitempty"` // omitempty is for JSON marshalling, not binding
}
```

### `xml:"fieldName"`
Used when binding from an XML request body (`application/xml`, `text/xml`).
```go
// import "encoding/xml" // For xml.Name

type Item struct {
	XMLName xml.Name `xml:"item"`    // Root element for XML
	ItemID  string   `xml:"id,attr"` // Attribute
	Name    string   `xml:"name_element"` // Element
}
```

### `form:"fieldName"`
Used when binding from `application/x-www-form-urlencoded` or `multipart/form-data` request bodies (for `POST`, `PUT`, `PATCH`).
```go
type ContactForm struct {
	Subject string `form:"subject_line" validate:"required"`
	Message string `form:"message_body" validate:"required"`
}
```

### `query:"fieldName"`
Used when binding from URL query parameters (typically for `GET`, `DELETE`, `HEAD` requests).
```go
type SearchFilters struct {
	SearchQuery string   `query:"q"`
	PageNumber  int      `query:"page" validate:"omitempty,min=1"`
	SortBy      []string `query:"sort_by"` // e.g., ?sort_by=name&sort_by=date
}
```

**Behavior without Specific Tags:**
If a specific tag (like `query` or `form`) is missing for a field, Xylium's reflection binder will use the **field's name** (case-sensitive) as the default key to look for in the request data for that source. If a tag is `"-"`, the field is skipped during binding from that source.

### Note on `default` tag
The `default:"value"` tag for specifying default values is **not supported** by Xylium's general-purpose `c.Bind()` or `c.BindAndValidate()` methods. Default values should be handled by initializing the struct before binding or by application logic after binding.

## 6. Validation

Xylium integrates with `go-playground/validator/v10` for struct validation, typically invoked via `c.BindAndValidate()`.

### Validation Tags
You use `validate` struct tags to define validation rules.
```go
type User struct {
	Username string   `json:"username" form:"username" query:"username" validate:"required,alphanum,min=3,max=30"`
	Email    string   `json:"email"    form:"email"    query:"email"    validate:"required,email"`
	Age      int      `json:"age"      form:"age"      query:"age"      validate:"omitempty,gte=18,lte=130"` // Optional, but if present, must be 18-130
	Homepage string   `json:"homepage" form:"homepage" query:"homepage" validate:"omitempty,url"`
	Tags     []string `json:"tags"     form:"tags"     query:"tags"     validate:"omitempty,dive,required,min=2"` // If tags slice exists, each element must be min 2 chars
	Address  struct {
		Street string `json:"street" validate:"required"`
		City   string `json:"city" validate:"required"`
	} `json:"address" validate:"required"` // Nested struct validation
}
```
Refer to the [go-playground/validator documentation](https://pkg.go.dev/github.com/go-playground/validator/v10) for a complete list of available tags and custom validation options.

### Handling Validation Errors

When `c.BindAndValidate()` encounters validation errors, it returns a `*xylium.HTTPError` with:
*   Status Code: `xylium.StatusBadRequest` (400)
*   Message: A `xylium.M` (map) containing:
    *   `"message": "Validation failed."`
    *   `"details": map[string]string` where keys are field names (or namespaces for nested structs, e.g., `Address.Street`). Xylium's binder attempts to remove the top-level struct name prefix from the namespace provided by `validator.FieldError.Namespace()` to give a more client-friendly field path.
        *   For a field `Username` in `struct User`, the key will be `Username`.
        *   For a field `Street` in a nested `struct Address` (field name `Address` in `User`), the key will be `Address.Street`.

**Example JSON Error Response for Validation Failure:**
```json
// Status: 400 Bad Request
{
    "error": {
        "message": "Validation failed.",
        "details": {
            "Username": "validation failed on tag 'required'",
            "Email": "validation failed on tag 'email'",
            "Address.Street": "validation failed on tag 'required'" 
        }
    }
}
```
This structured error is then typically handled by Xylium's `GlobalErrorHandler`, which logs it and sends the JSON response to the client.

## 7. Custom Validator

You can replace Xylium's default `go-playground/validator/v10` instance with your own custom validator instance (which must still be of type `*validator.Validate`). This is useful if you need to register custom validation functions, custom type validators, or use a differently configured validator (e.g., with custom translations).

Call `xylium.SetCustomValidator(v *validator.Validate)` **before** initializing your Xylium app (`xylium.New()` or `xylium.NewWithConfig()`).

```go
import (
	"github.com/go-playground/validator/v10"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust path
)

// myCustomValidationFunc is an example of a custom validation function.
func myCustomValidationFunc(fl validator.FieldLevel) bool {
	// Example: field must be the string "xylium_rocks"
	return fl.Field().String() == "xylium_rocks"
}

func main() {
	customValidator := validator.New()
	// Register custom validation functions if needed
	err := customValidator.RegisterValidation("must_be_xylium_rocks", myCustomValidationFunc)
	if err != nil {
		// Handle registration error appropriately
		panic("Failed to register custom validation: " + err.Error())
	}

	xylium.SetCustomValidator(customValidator) // Must be called before app initialization

	app := xylium.New()
	// ... your app setup ...
	// Now c.BindAndValidate() will use your customValidator.
}
```
For more details, refer to `AdvancedConfiguration.md`.

## 8. Examples Using `c.BindAndValidate()`

### 8.1. Binding JSON with Validation

```go
// Handler
func HandleCreatePost(c *xylium.Context) error {
	type CreatePostInput struct {
		Title   string   `json:"title" validate:"required,min=5"`
		Content string   `json:"content" validate:"required"`
		Tags    []string `json:"tags" validate:"omitempty,max=5,dive,alphanum"` // dive validates each element in slice
	}
	var input CreatePostInput
	if err := c.BindAndValidate(&input); err != nil {
		return err // Let GlobalErrorHandler handle response
	}
	// ... process input ...
	return c.JSON(xylium.StatusCreated, input)
}

/*
Example Request: POST /posts
Content-Type: application/json

{
    "title": "My New Post",
    "content": "This is the content.",
    "tags": ["go", "xylium"]
}
*/
```

### 8.2. Binding Query Parameters with Validation (for GET requests)
```go
// Handler
func HandleListArticles(c *xylium.Context) error {
	type ListArticlesQuery struct {
		Page     int    `query:"page" validate:"omitempty,min=1"` // 'query' tag is crucial here
		Limit    int    `query:"limit" validate:"omitempty,min=1,max=100"`
		AuthorID string `query:"author_id" validate:"omitempty,uuid4"`
	}
	var queryParams ListArticlesQuery
	// For GET requests, BindAndValidate will bind from query parameters.
	if err := c.BindAndValidate(&queryParams); err != nil {
		return err
	}
	// ... process queryParams ...
	return c.JSON(xylium.StatusOK, xylium.M{"filters_applied": queryParams, "data": "placeholder_articles"})
}

/*
Example Request: GET /articles?page=2&limit=20&author_id=a1b2c3d4-e5f6-7890-1234-567890abcdef
*/
```

### 8.3. Implementing `XBind` for Custom Binding Logic

Refer to [Section 3.1](#31-custom-binding-with-xbind-interface-high-performancecontrol) for a detailed example of implementing the `XBind` interface for a struct named `MyCustomPayload`. This approach is recommended when you need to:
*   Bypass reflection for performance-critical paths.
*   Implement binding logic from non-standard request formats (e.g., Protocol Buffers, custom binary).
*   Have fine-grained control over how data is mapped to your struct fields or perform complex pre-processing during binding.

---

By understanding these binding mechanisms, you can efficiently and safely process incoming request data in your Xylium applications. Choose the method (`c.BindAndValidate`, `XBind`, or individual parameter access) that best suits your needs for clarity, performance, and control.
