# Xylium Response Handling

Xylium provides a flexible and convenient API through `xylium.Context` for sending various types of HTTP responses to the client. This includes strings, JSON, XML, HTML, files, and redirects, along with methods to manage status codes and headers.

## Table of Contents

*   [1. Setting Status Code](#1-setting-status-code)
*   [2. Setting Response Headers](#2-setting-response-headers)
    *   [2.1. Setting a Single Header](#21-setting-a-single-header)
    *   [2.2. Setting Content-Type](#22-setting-content-type)
*   [3. Sending String Responses](#3-sending-string-responses)
*   [4. Sending JSON Responses](#4-sending-json-responses)
*   [5. Sending XML Responses](#5-sending-xml-responses)
*   [6. Sending HTML Responses (Using a Renderer)](#6-sending-html-responses-using-a-renderer)
*   [7. Serving Files as Responses](#7-serving-files-as-responses)
    *   [7.1. Serving a Local File (`c.File()`)](#71-serving-a-local-file-cfile)
    *   [7.2. Forcing File Download (`c.Attachment()`)](#72-forcing-file-download-cattachment)
*   [8. Redirecting Requests](#8-redirecting-requests)
*   [9. Sending `204 No Content` Responses](#9-sending-204-no-content-responses)
*   [10. Low-Level Writes](#10-low-level-writes)
    *   [10.1. `c.Write([]byte)`](#101-cwritebyte)
    *   [10.2. `c.WriteString(string)`](#102-cwritestringstring)
*   [11. Response Commitment](#11-response-commitment)

---

## 1. Setting Status Code

You can set the HTTP response status code using `c.Status(code int) *Context`. This method returns the `Context` pointer, allowing for chaining.

```go
func GetResourceHandler(c *xylium.Context) error {
	exists := false // Simulating resource check
	if !exists {
		// Chain Status() with another response method
		return c.Status(http.StatusNotFound).JSON(xylium.M{"error": "Resource not found"})
	}
	return c.Status(http.StatusOK).String("Resource data...")
}
```
If you use specific response methods like `c.JSON(code, data)`, `c.String(code, format, ...)`, etc., they internally set the status code, so a separate `c.Status()` call is often not needed for them.

## 2. Setting Response Headers

### 2.1. Setting a Single Header

Use `c.SetHeader(key, value string) *Context` to set or replace a response header.

```go
func CustomHeaderHandler(c *xylium.Context) error {
	c.SetHeader("X-Custom-Header", "MyValue")
	c.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
	return c.String(http.StatusOK, "Response with custom headers.")
}
```

### 2.2. Setting Content-Type

Use `c.SetContentType(contentType string) *Context` to specifically set the `Content-Type` header.

```go
func PlainTextHandler(c *xylium.Context) error {
	c.SetContentType("text/plain; charset=iso-8859-1") // Override default UTF-8
	return c.WriteString("Hello in Latin-1!")
}
```
Most high-level response methods (`c.JSON`, `c.XML`, `c.HTML`, `c.String`) automatically set the appropriate `Content-Type`. `c.SetContentType` is useful when you need a specific or non-standard content type, or when using lower-level write methods like `c.Write()`.

Xylium's `c.Write()` and `c.WriteString()` methods will automatically call `c.SetDefaultContentType()` (setting to "text/plain; charset=utf-8") if no `Content-Type` has been set by that point. This ensures a `Content-Type` is always present.

## 3. Sending String Responses

Use `c.String(code int, format string, values ...interface{}) error` to send a plain text response.
*   Sets `Content-Type: text/plain; charset=utf-8`.
*   Uses `fmt.Sprintf` if `values` are provided.

```go
func GreetHandler(c *xylium.Context) error {
	name := c.QueryParamDefault("name", "Guest")
	return c.String(http.StatusOK, "Hello, %s!", name)
}

func PlainMessageHandler(c *xylium.Context) error {
	return c.String(http.StatusOK, "This is a plain text message.")
}
```

## 4. Sending JSON Responses

Use `c.JSON(code int, data interface{}) error` to send a JSON response.
*   Sets `Content-Type: application/json; charset=utf-8`.
*   If `data` is `[]byte`, it's written directly. Otherwise, `data` is marshalled to JSON.
*   Returns `*xylium.HTTPError` if marshalling fails.

```go
type UserInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func GetUserJSONHandler(c *xylium.Context) error {
	user := UserInfo{ID: 1, Name: "Alice"}
	return c.JSON(http.StatusOK, user)
}

func SimpleMapJSONHandler(c *xylium.Context) error {
	return c.JSON(http.StatusOK, xylium.M{
		"status":  "success",
		"data_id": 12345,
	})
}
```
The Global Error Handler typically processes the `*xylium.HTTPError` from marshalling failures.

## 5. Sending XML Responses

Use `c.XML(code int, data interface{}) error` to send an XML response.
*   Sets `Content-Type: application/xml; charset=utf-8`.
*   If `data` is `[]byte`, it's written directly. Otherwise, `data` is marshalled to XML.
*   Returns `*xylium.HTTPError` if marshalling fails.

```go
import "encoding/xml"

type ProductXML struct {
	XMLName xml.Name `xml:"product"`
	SKU     string   `xml:"sku,attr"`
	Title   string   `xml:"title"`
}

func GetProductXMLHandler(c *xylium.Context) error {
	product := ProductXML{SKU: "XYZ-001", Title: "Awesome Widget"}
	return c.XML(http.StatusOK, product)
}
```

## 6. Sending HTML Responses (Using a Renderer)

Use `c.HTML(code int, name string, data interface{}) error` to render an HTML template and send it as a response.
*   Sets `Content-Type: text/html; charset=utf-8`.
*   Requires an `HTMLRenderer` to be configured on the `xylium.Router` instance.
*   `name` is the template name, `data` is the context for the template.

```go
// Assume HTMLRenderer is configured on the app instance (e.g., in main.go)
// app.HTMLRenderer = myTemplateEngine // myTemplateEngine implements xylium.HTMLRenderer

func ShowHomePage(c *xylium.Context) error {
	pageData := xylium.M{
		"PageTitle": "Welcome Home!",
		"Username":  "CurrentUser",
	}
	// "home.html" is the name of the template to render
	return c.HTML(http.StatusOK, "home.html", pageData)
}
```
If no `HTMLRenderer` is configured, `c.HTML()` will return an `*xylium.HTTPError`. See `README.md` or other documentation on how to set up an `HTMLRenderer`.

## 7. Serving Files as Responses

Xylium provides methods to send local files as the HTTP response.

### 7.1. Serving a Local File (`c.File()`)

`c.File(filepathToServe string) error` serves a local file.
*   Uses `fasthttp.ServeFile` for efficient serving.
*   Automatically sets `Content-Type` based on file extension.
*   Handles `If-Modified-Since` requests (304 Not Modified).
*   Returns an `*xylium.HTTPError` (e.g., 404 Not Found, 403 Forbidden for directories) if the file cannot be served.

```go
func DownloadReportHandler(c *xylium.Context) error {
	reportPath := "./reports/monthly_report.pdf" // Path on server filesystem
	// Security: Ensure reportPath is validated and not user-supplied directly without sanitization.
	return c.File(reportPath)
}
```

### 7.2. Forcing File Download (`c.Attachment()`)

`c.Attachment(filepathToServe string, downloadFilename string) error` serves a local file and sets the `Content-Disposition: attachment` header, prompting the browser to download it with the given `downloadFilename`.

```go
func DownloadSoftwareHandler(c *xylium.Context) error {
	filePath := "./dist/my_software_v1.zip"
	// User will be prompted to download "MySoftware-v1.0.zip"
	return c.Attachment(filePath, "MySoftware-v1.0.zip")
}
```
This method internally calls `c.File()` to serve the content.

## 8. Redirecting Requests

Use `c.Redirect(location string, code int) error` to send an HTTP redirect.
*   `location` is the URL to redirect to.
*   `code` is the HTTP redirect status code (e.g., `http.StatusFound` (302), `http.StatusMovedPermanently` (301)).
*   Defaults to 302 if an invalid or non-redirect code is given.

```go
func OldPathHandler(c *xylium.Context) error {
	return c.Redirect("/new-path", http.StatusMovedPermanently)
}

func TemporaryMoveHandler(c *xylium.Context) error {
	targetURL := c.QueryParam("target")
	if targetURL == "" {
		targetURL = "/default-page"
	}
	return c.Redirect(targetURL, http.StatusFound)
}
```

## 9. Sending `204 No Content` Responses

Use `c.NoContent(code int) error` to send a response with a status code and no body. This is commonly used for `204 No Content`.

```go
func DeleteResourceHandler(c *xylium.Context) error {
	// ... logic to delete a resource ...
	// After successful deletion, typically no content needs to be returned.
	return c.NoContent(http.StatusNoContent)
}

func UpdateAcknowledgedHandler(c *xylium.Context) error {
    // ... logic to acknowledge an update without returning data ...
    return c.NoContent(http.StatusOK) // Can also be used with 200 OK if no body is intended
}
```

## 10. Low-Level Writes

For fine-grained control or streaming, you can use `c.Write()` and `c.WriteString()`.

### 10.1. `c.Write([]byte)`

`c.Write(p []byte) error` writes a byte slice to the response body.

```go
func RawBytesHandler(c *xylium.Context) error {
	c.SetContentType("application/octet-stream") // Set specific content type
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	return c.Write(data)
}
```

### 10.2. `c.WriteString(string)`

`c.WriteString(s string) error` writes a string to the response body.

```go
func CustomFormatHandler(c *xylium.Context) error {
	c.SetContentType("application/custom-format")
	customData := "field1=value1;field2=value2"
	return c.WriteString(customData)
}
```
Both `c.Write` and `c.WriteString` will automatically set a default `Content-Type` of `text/plain; charset=utf-8` if no `Content-Type` has been set by that point using `c.responseOnce`.

## 11. Response Commitment

Once response headers or body have been written to the client, the response is considered "committed." After this point, you generally cannot change the status code or headers.

*   `c.ResponseCommitted() bool`: Checks if the response headers have been sent.

This is useful in middleware or complex handlers to determine if a response has already been dispatched.

```go
func MyMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		err := next(c) // Call downstream handlers

		// If an error occurred and response hasn't been sent yet by downstream,
		// this middleware can try to send a custom error response.
		if err != nil && !c.ResponseCommitted() {
			c.Logger().Warnf("Middleware detected error and response not committed. Sending fallback. Error: %v", err)
			return c.Status(http.StatusInternalServerError).String("Middleware error handling")
		}
		return err // Propagate error if response already sent or no error
	}
}
```
Xylium's `GlobalErrorHandler` also checks `c.ResponseCommitted()` before attempting to send an error response.
