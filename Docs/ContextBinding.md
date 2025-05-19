---
# Xylium Data Binding: From Request to Structs

Xylium provides powerful and flexible mechanisms for binding incoming request data (JSON, XML, form data, query parameters) to Go structs. This process often includes validation to ensure data integrity.

## Table of Contents

*   [1. Basic Binding and Validation: `c.BindAndValidate()`](#1-basic-binding-and-validation-cbindandvalidate)
*   [2. Binding Only: `c.Bind()`](#2-binding-only-cbind)
*   [3. How Xylium Determines Binding Source](#3-how-xylium-determines-binding-source)
    *   [3.1. Custom Binding with `XBind` Interface (High Performance/Control)](#31-custom-binding-with-xbind-interface-high-performancecontrol)
    *   [3.2. Reflection-Based Binding (Default)](#32-reflection-based-binding-default)
        *   [Request Body (JSON, XML, Form)](#request-body-json-xml-form)
        *   [URL Query Parameters](#url-query-parameters)
        *   [Form Data](#form-data)
*   [4. Supported Data Types for Reflection-Based Binding](#4-supported-data-types-for-reflection-based-binding)
*   [5. Struct Tags for Reflection-Based Binding](#5-struct-tags-for-reflection-based-binding)
    *   [`json:"fieldName"`](#jsonfieldname)
    *   [`xml:"fieldName"`](#xmlfieldname)
    *   [`form:"fieldName"`](#formfieldname)
    *   [`query:"fieldName"`](#queryfieldname)
*   [6. Validation](#6-validation)
    *   [Validation Tags](#validation-tags)
    *   [Handling Validation Errors](#handling-validation-errors)
*   [7. Custom Validator](#7-custom-validator)
*   [8. Examples](#8-examples)
    *   [8.1. Binding JSON with Validation](#81-binding-json-with-validation)
    *   [8.2. Binding Query Parameters with Validation](#82-binding-query-parameters-with-validation)
    *   [8.3. Implementing `XBind` for Custom Binding Logic](#83-implementing-xbind-for-custom-binding-logic)

---

## 1. Basic Binding and Validation: `c.BindAndValidate()`

The most common way to handle request data is using `c.BindAndValidate(out interface{}) error`. This method performs two main operations:

1.  **Binding**: It attempts to populate the fields of the `out` struct (which must be a pointer) with data from the HTTP request.
2.  **Validation**: If binding is successful, it validates the populated struct using the `validate` tags on its fields.

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path
)

type CreateUserInput struct {
	Username string `json:"username" validate:"required,min=3,max=50"`
	Email    string `json:"email"    validate:"required,email"`
	Age      int    `json:"age"      validate:"omitempty,gte=18"`
}

func CreateUserHandler(c *xylium.Context) error {
	var input CreateUserInput

	// BindAndValidate will try to bind JSON (based on Content-Type or method)
	// and then validate the 'input' struct.
	if err := c.BindAndValidate(&input); err != nil {
		// err will be *xylium.HTTPError, typically with status 400.
		// The GlobalErrorHandler will handle logging and sending the client response.
		c.Logger().Warnf("Binding/validation failed for user creation: %v", err)
		return err 
	}

	// If we reach here, input is bound and validated.
	// Process input...
	c.Logger().Infof("User successfully bound and validated: %+v", input)
	return c.JSON(http.StatusCreated, xylium.M{"message": "User created", "user": input})
}
```

If binding or validation fails, `c.BindAndValidate()` returns a `*xylium.HTTPError`. This error typically has an HTTP status code of `400 Bad Request` and contains details about the failure (e.g., which validation rule failed for which field).

## 2. Binding Only: `c.Bind()`

If you only need to bind data without immediate validation (perhaps validation is conditional or done later), you can use `c.Bind(out interface{}) error`.

```go
func UpdateProfilePartialHandler(c *xylium.Context) error {
	var partialData MyProfileUpdate // Assume MyProfileUpdate has no 'required' tags

	if err := c.Bind(&partialData); err != nil {
		return err // Handle binding error
	}

	// Perform custom logic or conditional validation here...
	if partialData.NewPassword != "" && len(partialData.NewPassword) < 8 {
		return xylium.NewHTTPError(http.StatusBadRequest, "New password too short.")
	}

	// Update profile...
	return c.String(http.StatusOK, "Profile update processed (partially).")
}
```

## 3. How Xylium Determines Binding Source

Xylium's `c.Bind()` method is intelligent and uses a prioritized approach to bind data:

### 3.1. Custom Binding with `XBind` Interface (High Performance/Control)

For maximum performance or highly specific binding requirements, your struct can implement the `xylium.XBind` interface:

```go
package xylium // In xylium's types

type XBind interface {
	Bind(c *Context) error
}
```

If the struct passed to `c.Bind()` (or `c.BindAndValidate()`) implements this interface, Xylium will call its `Bind(c *xylium.Context) error` method. This allows you to define precisely how the struct should be populated from the request context.

**Example:**

```go
// In your models package
package models

import (
	"encoding/json"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust path
)

type MyCustomPayload struct {
	FieldA string
	FieldB int
}

func (mcp *MyCustomPayload) Bind(c *xylium.Context) error {
	// Example: Only bind from JSON, perhaps using a faster library
	if strings.HasPrefix(c.ContentType(), "application/json") {
		c.Logger().Debug("MyCustomPayload: Using custom JSON binding logic.")
		// For instance, using jsoniter for speed:
		// return jsoniter.Unmarshal(c.Body(), mcp) 
		return json.Unmarshal(c.Body(), mcp) // Standard library for example
	}
	// Could return an error if content type is not supported by this custom binder
	return xylium.NewHTTPError(xylium.StatusUnsupportedMediaType, "MyCustomPayload only supports JSON.")
}
```
When `c.Bind(&MyCustomPayload{})` is called, the `MyCustomPayload.Bind()` method will be executed.

### 3.2. Reflection-Based Binding (Default)

If the struct does *not* implement `XBind`, Xylium falls back to its default reflection-based binding mechanism. The source of data depends on the HTTP method and `Content-Type` header:

#### Request Body (JSON, XML, Form)

For HTTP methods like `POST`, `PUT`, `PATCH` (which typically have a request body):

*   **JSON (`application/json`)**: If `Content-Type` is `application/json`, Xylium attempts to unmarshal the request body as JSON into the struct. Uses struct tags like `json:"fieldName"`.
*   **XML (`application/xml`, `text/xml`)**: If `Content-Type` is XML, it unmarshals the XML body. Uses struct tags like `xml:"fieldName"`.
*   **Form Data (`application/x-www-form-urlencoded`, `multipart/form-data`)**: If `Content-Type` indicates form data, Xylium populates the struct from form fields. Uses struct tags like `form:"fieldName"`. File uploads are handled separately using `c.FormFile()` or `c.MultipartForm()`.

#### URL Query Parameters

For HTTP methods like `GET`, `DELETE`, `HEAD` (which typically don't have a request body, or where the body isn't the primary data source for binding to a simple struct):

*   Xylium attempts to populate the struct fields from URL query parameters.
*   Uses struct tags like `query:"fieldName"`.
    *Example*: `GET /search?name=xylium&page=1`

#### Form Data

Although primarily for body-based methods, `c.bindDataFromArgs` (used internally by `bindWithReflection`) can also be made to bind from `c.Ctx.FormArgs()` if called directly, which includes both query and POST form values. However, the standard `c.Bind()` flow separates these based on method and Content-Type.

**Order of Precedence (if multiple sources could theoretically apply):**

1.  **`XBind` implementation**: If present, this takes full control.
2.  **Reflection-based on method and Content-Type**:
    *   For `GET`, `DELETE`, `HEAD`: Primarily **Query Parameters**.
    *   For `POST`, `PUT`, `PATCH`: Based on **`Content-Type`** (JSON > XML > Form).

If a request has no body (e.g., `Content-Length: 0` for a `POST` request) and the struct is being bound, the binding operation itself will succeed (resulting in a zero-value struct or an empty map). Subsequent validation (e.g., `required` tags) will then determine if this is acceptable.

## 4. Supported Data Types for Reflection-Based Binding

The reflection-based binding (from query or form data) supports:

*   Basic types: `string`, `int` (and its variants `int8`, `int16`, `int32`, `int64`), `uint` (and its variants), `bool`, `float32`, `float64`.
*   `time.Time`: Parses RFC3339 format (e.g., "2006-01-02T15:04:05Z07:00") and "YYYY-MM-DD" (e.g., "2023-10-26").
*   Pointers to these types (e.g., `*string`, `*int`). If the request data for a pointer field is an empty string and the underlying type is not `string` (e.g. `*int`), the pointer will remain `nil`. This helps differentiate "not provided" from "provided as zero/empty".
*   Slices of these types (e.g., `[]string`, `[]int`). For query/form, this is typically used when a parameter is repeated (e.g., `?ids=1&ids=2`).
*   Slices of pointers to these types (e.g. `[]*int`).

For JSON and XML binding, any type supported by `encoding/json` and `encoding/xml` respectively can be used.

## 5. Struct Tags for Reflection-Based Binding

Struct tags control how fields are mapped from different data sources during reflection-based binding.

### `json:"fieldName"`
Used when binding from a JSON request body.
```go
type MyData struct {
	ProductName  string `json:"product_name"`
	ProductPrice int    `json:"price,omitempty"` // omitempty for JSON marshalling
}
```

### `xml:"fieldName"`
Used when binding from an XML request body.
```go
type Item struct {
	XMLName xml.Name `xml:"item"`
	ItemID  string   `xml:"id,attr"`    // Attribute
	Name    string   `xml:"name_field"` // Element
}
```

### `form:"fieldName"`
Used when binding from `application/x-www-form-urlencoded` or `multipart/form-data` request bodies.
```go
type ContactForm struct {
	Subject string `form:"subject_line" validate:"required"`
	Message string `form:"message_body" validate:"required"`
}
```

### `query:"fieldName"`
Used when binding from URL query parameters (typically for `GET` requests).
```go
type SearchFilters struct {
	SearchQuery string   `query:"q"`
	PageNumber  int      `query:"page" validate:"omitempty,min=1"`
	SortBy      []string `query:"sort_by"` // e.g., ?sort_by=name&sort_by=date
}
```

If a specific tag (like `query` or `form`) is missing for a field, Xylium's reflection binder will use the **field's name** as the default key to look for in the request data. If a tag is `"-"`, the field is skipped during binding from that source.

## 6. Validation

Xylium integrates with `go-playground/validator/v10` for struct validation.

### Validation Tags
You use `validate` struct tags to define validation rules.
```go
type User struct {
	Username string `json:"username" validate:"required,alphanum,min=3,max=30"`
	Email    string `json:"email"    validate:"required,email"`
	Age      int    `json:"age"      validate:"omitempty,gte=18,lte=130"` // Optional, but if present, must be 18-130
	Homepage string `json:"homepage" validate:"omitempty,url"`
	Tags     []string `json:"tags"    validate:"omitempty,dive,required,min=2"` // If tags slice exists, each element must be min 2 chars
}
```
Refer to the [go-playground/validator documentation](https://pkg.go.dev/github.com/go-playground/validator/v10) for a complete list of available tags and custom validation options.

### Handling Validation Errors

When `c.BindAndValidate()` encounters validation errors, it returns a `*xylium.HTTPError` with:
*   Status Code: `400 Bad Request`
*   Message: A `xylium.M` (map) containing:
    *   `"message": "Validation failed."`
    *   `"details": map[string]string` where keys are field names and values are specific validation error messages (e.g., `"Username": "validation failed on tag 'required'"`).

**Example JSON Error Response for Validation Failure:**
```json
// Status: 400 Bad Request
{
    "error": {
        "message": "Validation failed.",
        "details": {
            "Username": "validation failed on tag 'required'",
            "Email": "validation failed on tag 'email'"
        }
    }
}
```
This structured error is then typically handled by Xylium's `GlobalErrorHandler`, which logs it and sends the JSON response to the client.

## 7. Custom Validator

You can replace Xylium's default `go-playground/validator/v10` instance with your own custom validator instance (which must still be of type `*validator.Validate`). This is useful if you need to register custom validation functions or use a differently configured validator.

Call `xylium.SetCustomValidator(v *validator.Validate)` *before* initializing your Xylium app (`xylium.New()`).

```go
import (
	"github.com/go-playground/validator/v10"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust path
)

func main() {
	customValidator := validator.New()
	// Register custom validation functions if needed
	// customValidator.RegisterValidation("my_custom_tag", myCustomValidationFunc)
	
	xylium.SetCustomValidator(customValidator)

	app := xylium.New()
	// ... your app setup ...
}
```

## 8. Examples

### 8.1. Binding JSON with Validation

```go
// Handler
func HandleCreatePost(c *xylium.Context) error {
	type CreatePostInput struct {
		Title   string   `json:"title" validate:"required,min=5"`
		Content string   `json:"content" validate:"required"`
		Tags    []string `json:"tags" validate:"omitempty,max=5,dive,alphanum"`
	}
	var input CreatePostInput
	if err := c.BindAndValidate(&input); err != nil {
		return err // Let GlobalErrorHandler handle response
	}
	// ... process input ...
	return c.JSON(http.StatusCreated, input)
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

### 8.2. Binding Query Parameters with Validation
```go
// Handler
func HandleListArticles(c *xylium.Context) error {
	type ListArticlesQuery struct {
		Page     int    `query:"page" validate:"omitempty,min=1"`
		Limit    int    `query:"limit" validate:"omitempty,min=1,max=100"`
		AuthorID string `query:"author_id" validate:"omitempty,uuid4"`
	}
	var query ListArticlesQuery
	if err := c.BindAndValidate(&query); err != nil {
		return err
	}
	// ... process query ...
	return c.JSON(http.StatusOK, xylium.M{"filters": query, "data": "placeholder_articles"})
}

/*
Example Request: GET /articles?page=2&limit=20&author_id=a1b2c3d4-e5f6-7890-1234-567890abcdef
*/
```

### 8.3. Implementing `XBind` for Custom Binding Logic

Refer to [Section 3.1](#31-custom-binding-with-xbind-interface-high-performancecontrol) for a detailed example of implementing the `XBind` interface for a struct named `MyCustomPayload`. This approach is recommended when you need to:
*   Bypass reflection for performance-critical paths.
*   Implement binding logic from non-standard request formats.
*   Have fine-grained control over how data is mapped to your struct fields.

By understanding these binding mechanisms, you can efficiently and safely process incoming request data in your Xylium applications.

---
