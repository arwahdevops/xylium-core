# Xylium Request Handling

Xylium provides a rich `Context` object (`xylium.Context`) that offers numerous methods to access and process incoming request data. This includes reading path parameters, query strings, form data, JSON/XML bodies, file uploads, headers, and cookies. Xylium also integrates data binding and validation to streamline request processing.

## Table of Contents

*   [1. Reading Path Parameters](#1-reading-path-parameters)
*   [2. Reading Query Parameters](#2-reading-query-parameters)
    *   [2.1. Single Value Query Parameters](#21-single-value-query-parameters)
    *   [2.2. Multi-Value Query Parameters (Slices)](#22-multi-value-query-parameters-slices)
    *   [2.3. Typed Query Parameter Helpers](#23-typed-query-parameter-helpers)
*   [3. Reading Form Data (URL-encoded or Multipart)](#3-reading-form-data-url-encoded-or-multipart)
    *   [3.1. Accessing Form Values](#31-accessing-form-values)
    *   [3.2. Binding Form Data to Structs](#32-binding-form-data-to-structs)
*   [4. Reading JSON Request Body](#4-reading-json-request-body)
    *   [4.1. Binding JSON to Structs](#41-binding-json-to-structs)
    *   [4.2. Binding JSON to `map[string]interface{}`](#42-binding-json-to-mapstringinterface)
*   [5. Reading XML Request Body](#5-reading-xml-request-body)
    *   [5.1. Binding XML to Structs](#51-binding-xml-to-structs)
*   [6. Binding Request Data to Structs (General)](#6-binding-request-data-to-structs-general)
    *   [Refer to `ContextBinding.md`](#refer-to-contextbindingmd)
*   [7. Validating Bound Structs](#7-validating-bound-structs)
    *   [Refer to `ContextBinding.md`](#refer-to-contextbindingmd)
*   [8. Handling File Uploads (Single and Multiple)](#8-handling-file-uploads-single-and-multiple)
    *   [8.1. Single File Upload](#81-single-file-upload)
    *   [8.2. Multiple File Uploads (Same Field Name)](#82-multiple-file-uploads-same-field-name)
    *   [8.3. Accessing Multipart Form Values and Files](#83-accessing-multipart-form-values-and-files)
    *   [8.4. Saving Uploaded Files](#84-saving-uploaded-files)
*   [9. Reading Request Headers](#9-reading-request-headers)
    *   [9.1. Reading a Specific Header](#91-reading-a-specific-header)
    *   [9.2. Reading All Headers](#92-reading-all-headers)
*   [10. Working with Cookies (Reading and Setting)](#10-working-with-cookies-reading-and-setting)
    *   [10.1. Reading Request Cookies](#101-reading-request-cookies)
    *   [10.2. Setting Response Cookies](#102-setting-response-cookies)
*   [11. Accessing Raw Request Body](#11-accessing-raw-request-body)
*   [12. Getting Client IP Address](#12-getting-client-ip-address)
*   [13. Other Request Information](#13-other-request-information)

---

## 1. Reading Path Parameters

Path parameters are dynamic segments captured from the URL path, defined in route patterns (e.g., `/users/:id`).

*   `c.Param(name string) string`: Returns the value of the named path parameter.
*   `c.ParamInt(name string) (int, error)`: Parses the parameter as an integer.
*   `c.ParamIntDefault(name string, def int) int`: Parses as int, returns default on error.

```go
// Route: app.GET("/items/:category/:itemId", GetItemHandler)
// Request: GET /items/electronics/123

func GetItemHandler(c *xylium.Context) error {
	category := c.Param("category") // "electronics"
	itemIdStr := c.Param("itemId")   // "123"

	itemId, err := c.ParamInt("itemId")
	if err != nil {
		return xylium.NewHTTPError(http.StatusBadRequest, "Invalid item ID format").WithInternal(err)
	}
	// itemId is now an int: 123

	// Using default
	legacyId := c.ParamIntDefault("legacyId", 0) // If "legacyId" param doesn't exist

	return c.JSON(http.StatusOK, xylium.M{
		"category":    category,
		"item_id_str": itemIdStr,
		"item_id_int": itemId,
		"legacy_id":   legacyId,
	})
}
```
For more details on defining routes with parameters, see `Routing.md`.

## 2. Reading Query Parameters

Query parameters are key-value pairs appended to the URL after a `?` (e.g., `/search?q=xylium&page=2`).

### 2.1. Single Value Query Parameters

*   `c.QueryParam(key string) string`: Returns the value of the first query parameter with the given key.
*   `c.QueryParams() map[string]string`: Returns all query parameters as a map. (If a key has multiple values, `fasthttp`'s `Peek` behavior usually returns the first).

```go
// Request: GET /products?name=laptop&sort=price_asc

func ListProductsHandler(c *xylium.Context) error {
	productName := c.QueryParam("name")   // "laptop"
	sortBy := c.QueryParam("sort")       // "price_asc"
	category := c.QueryParam("category") // "" (empty if not present)

	allParams := c.QueryParams()
	// allParams will be map[string]string{"name": "laptop", "sort": "price_asc"}

	return c.JSON(http.StatusOK, xylium.M{
		"query_name": productName,
		"sort_by":    sortBy,
		"category":   category,
		"all_params": allParams,
	})
}
```

### 2.2. Multi-Value Query Parameters (Slices)

If a query parameter key appears multiple times (e.g., `?ids=1&ids=2&ids=3`), you can bind it to a slice when using struct binding (see `ContextBinding.md`). To access them directly:

*   `c.Ctx.QueryArgs().PeekMulti(key []byte) [][]byte`: Returns a slice of byte slices for all values of the key.

```go
// Request: GET /filter?status=active&status=pending&tags=go&tags=web

func FilterResultsHandler(c *xylium.Context) error {
	// Accessing fasthttp's underlying QueryArgs for multi-value
	queryArgs := c.Ctx.QueryArgs()

	statusBytes := queryArgs.PeekMulti("status") // [][]byte{[]byte("active"), []byte("pending")}
	var statuses []string
	for _, sb := range statusBytes {
		statuses = append(statuses, string(sb))
	}

	tagBytes := queryArgs.PeekMulti("tags")
	var tags []string
	for _, tb := range tagBytes {
		tags = append(tags, string(tb))
	}

	return c.JSON(http.StatusOK, xylium.M{
		"statuses_found": statuses, // ["active", "pending"]
		"tags_found":     tags,     // ["go", "web"]
	})
}
```
For easier handling of multi-value parameters, binding to a struct field of type `[]string`, `[]int`, etc., with `query:"fieldName"` tag is recommended. See `ContextBinding.md` (Section 4 & 5).

### 2.3. Typed Query Parameter Helpers

*   `c.QueryParamInt(key string) (int, error)`: Parses query param as an integer.
*   `c.QueryParamIntDefault(key string, def int) int`: Parses as int, returns default on error.

```go
// Request: GET /list?page=2&limit=20

func ListItemsHandler(c *xylium.Context) error {
	page, err := c.QueryParamInt("page")
	if err != nil {
		page = 1 // Default to page 1 if error or not present
	}

	limit := c.QueryParamIntDefault("limit", 10) // Default to 10 if error or not present

	return c.JSON(http.StatusOK, xylium.M{"page": page, "limit": limit})
}
```

## 3. Reading Form Data (URL-encoded or Multipart)

Form data is typically sent in the request body with `Content-Type: application/x-www-form-urlencoded` or `Content-Type: multipart/form-data` (often used for file uploads).

### 3.1. Accessing Form Values

*   `c.FormValue(key string) string`: Returns the value of a form field. It checks both URL query parameters and POST/PUT body parameters.
*   `c.PostFormValue(key string) string`: Returns the value of a form field specifically from POST arguments (request body).
*   `c.PostFormParams() map[string]string`: Returns all POST form parameters from the body as a map.

```go
// Assuming a POST request to /submit-form with body: name=John+Doe&email=john@example.com
// Content-Type: application/x-www-form-urlencoded

func SubmitFormHandler(c *xylium.Context) error {
	name := c.FormValue("name")         // "John Doe"
	email := c.PostFormValue("email")   // "john@example.com"
	subject := c.FormValue("subject") // "" (empty if not present in form or query)

	allPostParams := c.PostFormParams()
	// allPostParams: map[string]string{"name":"John Doe", "email":"john@example.com"}

	return c.JSON(http.StatusOK, xylium.M{
		"name_submitted": name,
		"email_submitted": email,
		"subject": subject,
		"all_post_data": allPostParams,
	})
}
```

### 3.2. Binding Form Data to Structs

For more complex forms, binding the data to a Go struct is highly recommended. Use `c.BindAndValidate(&yourStruct)` or `c.Bind(&yourStruct)`. Ensure your struct fields have `form:"fieldName"` tags.

Refer to **`ContextBinding.md` (Section 3.2 & 5.3)** for detailed examples.

## 4. Reading JSON Request Body

When a client sends data as JSON (`Content-Type: application/json`).

### 4.1. Binding JSON to Structs

This is the most common and recommended way. Use `c.BindAndValidate(&yourStruct)` or `c.Bind(&yourStruct)`. Struct fields should have `json:"fieldName"` tags.

```go
type CreateProductInput struct {
	Name        string  `json:"name" validate:"required"`
	Price       float64 `json:"price" validate:"gte=0"`
	Description string  `json:"description,omitempty"`
}

// POST /products
// Content-Type: application/json
// Body: {"name": "Awesome Gadget", "price": 99.99}
func CreateProductHandler(c *xylium.Context) error {
	var input CreateProductInput

	if err := c.BindAndValidate(&input); err != nil {
		// BindAndValidate returns *xylium.HTTPError for validation or binding failures.
		// GlobalErrorHandler will typically handle logging and sending the 400 response.
		return err
	}

	// Process the validated 'input'
	c.Logger().Infof("Product to create: %+v", input)
	return c.JSON(http.StatusCreated, xylium.M{"product_id": "new_id", "product_data": input})
}
```
Refer to **`ContextBinding.md` (Section 3.2 & 5.1)** for comprehensive details.

### 4.2. Binding JSON to `map[string]interface{}`

If you need to handle arbitrary JSON structures, you can bind to a `map[string]interface{}`.

```go
// POST /generic-json
// Content-Type: application/json
// Body: {"key1": "value1", "key2": {"nested_key": 123}}
func GenericJsonHandler(c *xylium.Context) error {
	var jsonData map[string]interface{}

	// Using c.Bind() without validation for a map
	if err := c.Bind(&jsonData); err != nil {
		// err will be *xylium.HTTPError if JSON is malformed
		return err
	}

	// Access jsonData map
	c.Logger().Infof("Received generic JSON: %+v", jsonData)
	if val, ok := jsonData["key1"].(string); ok {
		c.Logger().Infof("key1: %s", val)
	}

	return c.JSON(http.StatusOK, jsonData)
}
```
Alternatively, you can use `json.Unmarshal(c.Body(), &jsonData)` for direct unmarshalling.

## 5. Reading XML Request Body

When a client sends data as XML (`Content-Type: application/xml` or `text/xml`).

### 5.1. Binding XML to Structs

Similar to JSON, use `c.BindAndValidate(&yourStruct)` or `c.Bind(&yourStruct)`. Struct fields should have `xml:"fieldName"` tags.

```go
import "encoding/xml" // For xml.Name if needed in struct

type ItemXML struct {
	XMLName xml.Name `xml:"item"` // Optional: specifies the root XML element name
	ID      string   `xml:"id,attr"`
	Name    string   `xml:"name"`
	Price   float64  `xml:"priceValue"`
}

// POST /xml-item
// Content-Type: application/xml
// Body: <item id="x1"><name>My Item</name><priceValue>20.50</priceValue></item>
func CreateItemXMLHandler(c *xylium.Context) error {
	var item ItemXML
	if err := c.BindAndValidate(&item); err != nil {
		return err
	}
	c.Logger().Infof("XML Item received: %+v", item)
	return c.JSON(http.StatusCreated, item) // Responding with JSON for simplicity here
}
```
Refer to **`ContextBinding.md` (Section 3.2 & 5.2)** for more information.

## 6. Binding Request Data to Structs (General)

Xylium's `c.Bind(out interface{})` and `c.BindAndValidate(out interface{})` methods are versatile. They intelligently determine the binding source (JSON, XML, Form, Query) based on the request's `Content-Type` and HTTP method.

### Refer to `ContextBinding.md`
For a complete guide on data binding, including custom binding with `XBind`, reflection-based binding details, supported data types, struct tags (`json`, `xml`, `form`, `query`), and validation, please see the **[ContextBinding.md](./ContextBinding.md)** documentation.

## 7. Validating Bound Structs

Validation is typically performed after binding data to a struct using `c.BindAndValidate()`. Xylium uses `go-playground/validator/v10` and `validate` struct tags.

### Refer to `ContextBinding.md`
For comprehensive details on validation tags, handling validation errors, and using a custom validator instance, please see the **[ContextBinding.md](./ContextBinding.md#6-validation)** documentation.

## 8. Handling File Uploads (Single and Multiple)

File uploads are usually part of `multipart/form-data` requests.

### 8.1. Single File Upload

Use `c.FormFile(key string) (*multipart.FileHeader, error)` to get a single uploaded file.

```go
// POST /upload-avatar
// Content-Type: multipart/form-data
// Form field: name="avatar_file", type="file"
func UploadAvatarHandler(c *xylium.Context) error {
	fileHeader, err := c.FormFile("avatar_file")
	if err != nil {
		if err == http.ErrMissingFile { // fasthttp might use a different error, check its docs or Xylium's behavior
			return xylium.NewHTTPError(http.StatusBadRequest, "Avatar file is required.")
		}
		return xylium.NewHTTPError(http.StatusInternalServerError, "Failed to retrieve avatar file.").WithInternal(err)
	}

	c.Logger().Infof("Uploaded File: %s", fileHeader.Filename)
	c.Logger().Infof("File Size: %d bytes", fileHeader.Size)
	c.Logger().Infof("MIME Header: %+v", fileHeader.Header)

	// To save the file, see Section 8.4

	return c.String(http.StatusOK, "Avatar '%s' uploaded successfully.", fileHeader.Filename)
}
```

### 8.2. Multiple File Uploads (Same Field Name)

If multiple files are uploaded with the same form field name, you need to access the `multipart.Form`.

```go
// POST /upload-gallery
// Content-Type: multipart/form-data
// Form fields: name="images", type="file" (multiple times)
func UploadGalleryHandler(c *xylium.Context) error {
	form, err := c.MultipartForm()
	if err != nil {
		return xylium.NewHTTPError(http.StatusBadRequest, "Invalid multipart form data.").WithInternal(err)
	}

	// "images" is the key used in the form for file inputs
	fileHeaders := form.File["images"] // This is a slice of *multipart.FileHeader

	if len(fileHeaders) == 0 {
		return xylium.NewHTTPError(http.StatusBadRequest, "No images uploaded for gallery.")
	}

	var uploadedFilenames []string
	for _, fileHeader := range fileHeaders {
		c.Logger().Infof("Processing gallery image: %s (Size: %d)", fileHeader.Filename, fileHeader.Size)
		// ... save each file (see Section 8.4) ...
		uploadedFilenames = append(uploadedFilenames, fileHeader.Filename)
	}

	return c.JSON(http.StatusOK, xylium.M{
		"message":            "Gallery images uploaded.",
		"uploaded_filenames": uploadedFilenames,
		"count":              len(uploadedFilenames),
	})
}
```

### 8.3. Accessing Multipart Form Values and Files

`c.MultipartForm() (*multipart.Form, error)` gives you access to both regular form field values and uploaded files.

```go
// POST /upload-document
// Content-Type: multipart/form-data
// Form field (text): name="title", value="My Report"
// Form field (file): name="document_file", type="file"
func UploadDocumentHandler(c *xylium.Context) error {
	form, err := c.MultipartForm()
	if err != nil {
		return xylium.NewHTTPError(http.StatusBadRequest, "Error parsing multipart form.").WithInternal(err)
	}

	// Access regular form values
	titles := form.Value["title"] // Slice of strings
	var title string
	if len(titles) > 0 {
		title = titles[0]
	}

	// Access uploaded files
	fileHeaders := form.File["document_file"] // Slice of *multipart.FileHeader
	if len(fileHeaders) == 0 {
		return xylium.NewHTTPError(http.StatusBadRequest, "Document file is required.")
	}
	documentFileHeader := fileHeaders[0]

	c.Logger().Infof("Document Title: %s", title)
	c.Logger().Infof("Document File: %s", documentFileHeader.Filename)

	// ... save documentFileHeader (see Section 8.4) ...

	return c.String(http.StatusOK, "Document '%s' with title '%s' uploaded.", documentFileHeader.Filename, title)
}
```

### 8.4. Saving Uploaded Files

After getting a `*multipart.FileHeader`, you need to open it and save its content.

```go
import (
	"io"
	"os"
	"path/filepath"
	// ... other imports
)

func saveUploadedFile(fileHeader *multipart.FileHeader, destPath string) error {
	// 1. Open the uploaded file part
	srcFile, err := fileHeader.Open()
	if err != nil {
		return fmt.Errorf("failed to open uploaded file part: %w", err)
	}
	defer srcFile.Close()

	// 2. Create the destination file
	// Ensure the destination directory exists, or create it.
	// For security, sanitize destPath and ensure it's within a safe base directory.
	// This example assumes destPath is already safe.
	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", filepath.Dir(destPath), err)
	}

	dstFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destPath, err)
	}
	defer dstFile.Close()

	// 3. Copy the uploaded file content to the destination file
	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy uploaded file to %s: %w", destPath, err)
	}
	return nil
}

// Example usage in a handler:
// ... inside UploadAvatarHandler from 8.1 ...
// fileHeader, err := c.FormFile("avatar_file")
// ...
// // Construct a safe destination path
// // NEVER use fileHeader.Filename directly without sanitization for security.
// // Example: create a unique filename or use a sanitized version.
// safeFilename := filepath.Base(fileHeader.Filename) // Basic sanitization
// destination := filepath.Join("./uploads/avatars", safeFilename)
//
// if err := saveUploadedFile(fileHeader, destination); err != nil {
//     c.Logger().Errorf("Failed to save uploaded file '%s' to '%s': %v", fileHeader.Filename, destination, err)
//     return xylium.NewHTTPError(http.StatusInternalServerError, "Could not save uploaded file.")
// }
// c.Logger().Infof("File '%s' saved to '%s'", fileHeader.Filename, destination)
// ...
```
**Security Note:** Always sanitize filenames from `fileHeader.Filename` before using them to construct file paths on your server to prevent path traversal attacks. `filepath.Base()` is a good start. Consider generating unique filenames.

## 9. Reading Request Headers

### 9.1. Reading a Specific Header

*   `c.Header(key string) string`: Returns the value of the specified request header. Header keys are typically case-insensitive as `fasthttp` normalizes them.

```go
func CheckAuthHeaderHandler(c *xylium.Context) error {
	authToken := c.Header("Authorization") // e.g., "Bearer <token>"
	userAgent := c.Header("User-Agent")
	customHeader := c.Header("X-Custom-Data")

	if authToken == "" {
		return c.Status(http.StatusUnauthorized).String("Authorization header missing.")
	}

	// Process headers...
	return c.JSON(http.StatusOK, xylium.M{
		"auth_token_present": authToken != "",
		"user_agent":         userAgent,
		"custom_data":        customHeader,
	})
}
```

### 9.2. Reading All Headers

*   `c.Headers() map[string]string`: Returns all request headers as a map.
    Note: HTTP headers can have multiple values for the same key. This method usually returns the first or a comma-separated value depending on `fasthttp`'s behavior. For full control over multi-value headers, access `c.Ctx.Request.Header` directly (e.g., `c.Ctx.Request.Header.PeekMulti(key)`).

```go
func DumpHeadersHandler(c *xylium.Context) error {
	allHeaders := c.Headers()
	c.Logger().Infof("All request headers: %+v", allHeaders)
	return c.JSON(http.StatusOK, allHeaders)
}
```

## 10. Working with Cookies (Reading and Setting)

### 10.1. Reading Request Cookies

*   `c.Cookie(name string) string`: Returns the value of a request cookie by its name.
*   `c.Cookies() map[string]string`: Returns all request cookies as a map.

```go
func GetSessionCookieHandler(c *xylium.Context) error {
	sessionID := c.Cookie("session_id")
	themePreference := c.Cookie("theme") // "" if not present

	allCookies := c.Cookies()
	c.Logger().Debugf("All cookies: %+v", allCookies)

	if sessionID == "" {
		return c.String(http.StatusUnauthorized, "No session ID cookie found.")
	}

	return c.JSON(http.StatusOK, xylium.M{
		"session_id": sessionID,
		"theme":      themePreference,
	})
}
```

### 10.2. Setting Response Cookies

Xylium provides methods to set cookies on the HTTP response. These methods add a `Set-Cookie` header.

*   `c.SetCookie(cookie *fasthttp.Cookie)`: Sets a cookie using a `fasthttp.Cookie` object.
*   `c.ClearCookie(name string)`: Clears a cookie by setting its expiration to the past.
*   `xylium.NewxyliumCookie(name, value string) *xylium.xyliumCookie`: A helper to create a `fasthttp.Cookie` with some defaults (Path="/", HTTPOnly=true).
*   `c.SetCustomCookie(customCookie *xylium.xyliumCookie)`: Sets a cookie using the `xyliumCookie` helper.

```go
import (
	"time"
	"github.com/valyala/fasthttp"
	// ... other imports
)

func SetAndClearCookiesHandler(c *xylium.Context) error {
	// Set a simple cookie
	simpleCookie := xylium.NewxyliumCookie("user_id", "12345")
	// simpleCookie.Cookie is a fasthttp.Cookie, modify it further if needed
	simpleCookie.SetMaxAge(3600) // Expires in 1 hour (MaxAge in seconds)
	simpleCookie.SetSecure(true) // Only send over HTTPS (if applicable)
	// CookieHTTPOnly is true by default from NewxyliumCookie
	c.SetCustomCookie(simpleCookie)

	// Set another cookie directly using fasthttp.Cookie
	advCookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(advCookie) // Good practice
	advCookie.SetKey("pref_lang")
	advCookie.SetValue("en-US")
	advCookie.SetPath("/prefs")
	advCookie.SetExpire(time.Now().Add(24 * time.Hour * 30)) // Expires in 30 days
	advCookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	c.SetCookie(advCookie)

	// Clear a cookie (e.g., on logout)
	if c.QueryParam("action") == "logout" {
		c.ClearCookie("user_id") // Clears the user_id cookie set above
		c.ClearCookie("session_token") // Example of clearing another cookie
		return c.String(http.StatusOK, "Cookies cleared (simulated logout).")
	}

	return c.String(http.StatusOK, "Cookies have been set.")
}
```
Ensure the `Path` and `Domain` (if used) match when clearing a cookie.

## 11. Accessing Raw Request Body

*   `c.Body() []byte`: Returns the raw request body as a byte slice.
    `fasthttp` caches this body, so calling it multiple times is safe and efficient.

```go
// POST /raw-data
func RawBodyHandler(c *xylium.Context) error {
	rawBody := c.Body()

	if len(rawBody) == 0 {
		return c.String(http.StatusBadRequest, "Request body is empty.")
	}

	c.Logger().Infof("Received raw body (%d bytes): %s", len(rawBody), string(rawBody))
	// Process the rawBody, e.g., parse a custom format, proxy it, etc.

	return c.String(http.StatusOK, "Raw body received and logged.")
}
```
If you are binding to structs (JSON, XML, Form), you generally don't need to call `c.Body()` directly, as the binding mechanism handles reading the body. If you call `c.Body()` before binding, the binding might fail or read an empty body if `fasthttp`'s body stream was already consumed (though `fasthttp` often buffers it). It's usually best to let `c.Bind()` handle body reading.

## 12. Getting Client IP Address

*   `c.IP() string`: Returns the remote IP address of the client directly connected to the server. This might be a proxy's IP.
*   `c.RealIP() string`: Attempts to determine the actual client IP by checking common proxy headers like `X-Forwarded-For` and `X-Real-IP`. Falls back to `c.IP()` if headers are not present.

```go
func ShowIPHandler(c *xylium.Context) error {
	directIP := c.IP()
	realClientIP := c.RealIP() // More reliable if behind trusted proxies

	c.Logger().Infof("Direct IP: %s, Real Client IP: %s", directIP, realClientIP)
	return c.JSON(http.StatusOK, xylium.M{
		"connected_ip": directIP,
		"client_ip":    realClientIP,
	})
}
```
**Note:** Trust in `X-Forwarded-For` and `X-Real-IP` headers depends on your server's deployment environment and proxy configuration. Ensure these headers are set correctly by trusted upstream proxies.

## 13. Other Request Information

`xylium.Context` provides various other methods to inspect the request:

*   `c.Method() string`: HTTP request method (e.g., "GET", "POST").
*   `c.Path() string`: Request path (e.g., "/users/1").
*   `c.URI() string`: Full request URI including query string (e.g., "/search?q=term").
*   `c.Scheme() string`: Request scheme ("http" or "https"). Considers `X-Forwarded-Proto`.
*   `c.Host() string`: Host from the "Host" header.
*   `c.UserAgent() string`: Client's User-Agent header.
*   `c.Referer() string`: Client's Referer header.
*   `c.ContentType() string`: Request body's Content-Type header.
*   `c.IsTLS() bool`: True if the direct connection is TLS.
*   `c.IsAJAX() bool`: True if "X-Requested-With: XMLHttpRequest" header is present.

These methods allow for comprehensive inspection and handling of incoming HTTP requests in your Xylium application.
