package xylium

import (
	"encoding/json" // For c.JSON() marshalling.
	"encoding/xml"  // For c.XML() marshalling.
	"fmt"           // For c.String() formatting and error messages.
	"net/url"       // For c.Attachment() filename escaping.
	"os"            // For c.File() to stat files.
	"path/filepath" // For c.File() path cleaning.

	"github.com/valyala/fasthttp" // For fasthttp.ServeFile and status codes.
)

// --- Response Writing ---
// This section provides methods for constructing and sending HTTP responses.

// SetDefaultContentType sets the Content-Type header to "text/plain; charset=utf-8"
// if it has not already been set by another response method (e.g., c.JSON, c.SetContentType).
// This is called automatically by low-level write methods like `c.Write` and `c.WriteString`
// to ensure a default Content-Type is present if none was specified.
// It uses `c.responseOnce` to ensure this initialization happens at most once per request.
func (c *Context) SetDefaultContentType() {
	c.responseOnce.Do(func() {
		if len(c.Ctx.Response.Header.Peek("Content-Type")) == 0 {
			c.Ctx.Response.Header.SetContentTypeBytes([]byte("text/plain; charset=utf-8"))
		}
	})
}

// Status sets the HTTP response status code.
// Returns the Context pointer for method chaining.
// Example: `c.Status(http.StatusNotFound).JSON(...)`
func (c *Context) Status(code int) *Context {
	c.Ctx.SetStatusCode(code)
	return c
}

// SetHeader sets a response header with the given key and value.
// If the header key already exists, its value is replaced.
// Returns the Context pointer for method chaining.
func (c *Context) SetHeader(key, value string) *Context {
	c.Ctx.Response.Header.Set(key, value)
	return c
}

// SetContentType sets the "Content-Type" response header.
// Returns the Context pointer for method chaining.
// Example: `c.SetContentType("application/octet-stream")`
func (c *Context) SetContentType(contentType string) *Context {
	c.Ctx.Response.Header.SetContentType(contentType)
	return c
}

// Write writes a byte slice `p` to the response body.
// It automatically calls `SetDefaultContentType` if no Content-Type has been set yet.
// Returns an error if the write operation fails.
func (c *Context) Write(p []byte) error {
	c.SetDefaultContentType() // Ensure a default Content-Type if none is set.
	_, err := c.Ctx.Write(p)
	return err
}

// WriteString writes a string `s` to the response body.
// It automatically calls `SetDefaultContentType` if no Content-Type has been set yet.
// Returns an error if the write operation fails.
func (c *Context) WriteString(s string) error {
	c.SetDefaultContentType() // Ensure a default Content-Type if none is set.
	_, err := c.Ctx.WriteString(s)
	return err
}

// JSON sends a JSON response with the given status code and data.
// - Sets the Content-Type to "application/json; charset=utf-8".
// - If `data` is `[]byte`, it's written directly to the response body.
// - Otherwise, `data` is marshalled to JSON using `json.Marshal`.
// Returns an `*HTTPError` if marshalling fails, otherwise nil on success or write error.
func (c *Context) JSON(code int, data interface{}) error {
	c.Status(code).SetContentType("application/json; charset=utf-8")
	if b, ok := data.([]byte); ok { // If data is already []byte, write directly.
		return c.Write(b)
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		// Return an HTTPError that the GlobalErrorHandler can process.
		// This ensures consistent error logging and response formatting.
		return NewHTTPError(StatusInternalServerError, "JSON marshal error").WithInternal(err)
	}
	return c.Write(jsonData)
}

// XML sends an XML response with the given status code and data.
// - Sets the Content-Type to "application/xml; charset=utf-8".
// - If `data` is `[]byte`, it's written directly to the response body.
// - Otherwise, `data` is marshalled to XML using `xml.Marshal`.
// Returns an `*HTTPError` if marshalling fails, otherwise nil on success or write error.
func (c *Context) XML(code int, data interface{}) error {
	c.Status(code).SetContentType("application/xml; charset=utf-8")
	if b, ok := data.([]byte); ok { // If data is already []byte, write directly.
		return c.Write(b)
	}
	xmlData, err := xml.Marshal(data)
	if err != nil {
		return NewHTTPError(StatusInternalServerError, "XML marshal error").WithInternal(err)
	}
	return c.Write(xmlData)
}

// String sends a plain text response with the given status code and formatted string.
// - Sets the Content-Type to "text/plain; charset=utf-8".
// - If `values` are provided, `s` is used as a format string for `fmt.Sprintf`.
// Returns nil on success or an error if writing fails.
func (c *Context) String(code int, s string, values ...interface{}) error {
	c.Status(code).SetContentType("text/plain; charset=utf-8")
	if len(values) > 0 {
		return c.WriteString(fmt.Sprintf(s, values...))
	}
	return c.WriteString(s)
}

// HTML renders an HTML template using the configured `HTMLRenderer` on the Router
// and sends it as a response with the given status code.
// - Sets the Content-Type to "text/html; charset=utf-8".
// - `name` is the name of the template to render.
// - `data` is the data to pass to the template.
// Returns an `*HTTPError` if no `HTMLRenderer` is configured or if rendering fails.
func (c *Context) HTML(code int, name string, data interface{}) error {
	if c.router == nil || c.router.HTMLRenderer == nil {
		return NewHTTPError(StatusInternalServerError, "HTML renderer not configured on router")
	}
	c.Status(code).SetContentType("text/html; charset=utf-8")
	// The HTMLRenderer's Render method writes directly to the response body writer.
	return c.router.HTMLRenderer.Render(c.Ctx.Response.BodyWriter(), name, data, c)
}

// File sends a local file as the response body.
// - `filepathToServe` is the path to the file on the server's filesystem.
// - It performs security checks: ensures the path is valid, the file exists, and is not a directory.
// - It uses `fasthttp.ServeFile` for efficient file serving, which also sets appropriate
//   Content-Type based on file extension and handles `If-Modified-Since` requests.
// Returns an `*HTTPError` if the file is not found, is a directory, or if there's an access error.
// Otherwise, returns nil as `fasthttp.ServeFile` handles the response.
func (c *Context) File(filepathToServe string) error {
	// Resolve to an absolute path for security and consistency.
	absPath, err := filepath.Abs(filepathToServe)
	if err != nil {
		// This typically means the path string itself is malformed system-wise.
		return NewHTTPError(StatusInternalServerError, "Invalid file path string provided.").WithInternal(err)
	}

	// Check if file exists and is not a directory before calling fasthttp.ServeFile.
	// os.Stat is important as ServeFile might have less explicit error reporting for some cases.
	info, statErr := os.Stat(absPath)
	if os.IsNotExist(statErr) {
		// File does not exist at the specified path.
		return NewHTTPError(StatusNotFound, fmt.Sprintf("File '%s' not found.", filepath.Clean(filepathToServe))).WithInternal(statErr)
	}
	if statErr != nil {
		// Other errors accessing the file (e.g., permission issues).
		return NewHTTPError(StatusInternalServerError, "Error accessing file system to serve file.").WithInternal(statErr)
	}
	if info.IsDir() {
		// Serving directories directly using c.File() is disallowed for security.
		// To serve files from a directory (e.g., static assets), use `router.ServeFiles()`.
		return NewHTTPError(StatusForbidden, "Serving directories directly is not allowed via c.File(). Path is a directory.").WithInternal(fmt.Errorf("attempted to serve directory: %s", absPath))
	}

	// Delegate to fasthttp.ServeFile for efficient serving.
	// ServeFile handles setting Content-Type, ETag, Last-Modified, and byte range requests.
	fasthttp.ServeFile(c.Ctx, absPath)

	// fasthttp.ServeFile commits the response (sends headers and body).
	// If it encounters an issue (e.g., cannot read file after stat), it will set an appropriate
	// error status code on c.Ctx.Response directly.
	// Thus, we return nil here, as the response is handled.
	return nil
}

// Attachment sends a local file as an attachment, prompting the user to download it
// with the specified `downloadFilename`.
// - It sets the "Content-Disposition" header to "attachment".
// - It then calls `c.File(filepathToServe)` to serve the file content.
// Returns an error if `c.File` returns an error.
func (c *Context) Attachment(filepathToServe string, downloadFilename string) error {
	// Set Content-Disposition header to suggest download.
	// url.PathEscape ensures the filename is safe for use in the header.
	c.SetHeader("Content-Disposition", `attachment; filename="`+url.PathEscape(downloadFilename)+`"`)
	// Content-Type will be set by `c.File` (via `fasthttp.ServeFile`) based on file extension.
	return c.File(filepathToServe)
}

// Redirect sends an HTTP redirect response (3xx) to a new `location` with the given `code`.
// - `location`: The URL to redirect to.
// - `code`: The HTTP redirect status code (e.g., `StatusFound` (302), `StatusMovedPermanently` (301)).
//   If an invalid or non-redirect code is given, it defaults to `StatusFound` (302).
// Returns nil as `fasthttp.RequestCtx.Redirect` handles sending the response.
func (c *Context) Redirect(location string, code int) error {
	// Validate redirect code to ensure it's a 3xx status.
	// Common redirect codes range from 300 (StatusMultipleChoices) to 308 (StatusPermanentRedirect).
	// StatusNotModified (304) is not a redirect in the typical sense of changing location.
	if code < StatusMultipleChoices || code > StatusPermanentRedirect || code == StatusNotModified {
		code = StatusFound // Default to 302 Found if an unsuitable code is provided.
	}
	c.Ctx.Redirect(location, code) // fasthttp handles setting Location header and status.
	return nil // Redirect itself doesn't produce an error for the handler chain.
}

// Error sends an error response using `fasthttp.RequestCtx.Error`.
// This is a low-level way to send an error message with a status code.
// It might bypass Xylium's `GlobalErrorHandler` and custom error formatting/logging.
// Prefer returning an `*xylium.HTTPError` from handlers for consistent error management.
// `message` is the error message string. `code` is the HTTP status code.
// Returns nil as `fasthttp.RequestCtx.Error` handles sending the response.
func (c *Context) Error(message string, code int) error {
	c.Ctx.Error(message, code) // fasthttp sets status, Content-Type (text/plain), and body.
	return nil                 // Error commits the response.
}

// NoContent sends a response with the given status code and no body.
// Commonly used for HTTP 204 No Content responses.
// `code` should typically be `StatusNoContent` (204) or similar (e.g., 200, 201 after a successful
// operation where no body needs to be returned, though 204 is more idiomatic for "no content").
// Returns nil as the response is fully handled.
func (c *Context) NoContent(code int) error {
	c.Status(code)               // Set the desired status code.
	c.Ctx.Response.ResetBody()   // Ensure no body is sent.
	// Content-Type is not strictly necessary for 204 but fasthttp might set one by default.
	// To be explicit about no content type for 204:
	if code == StatusNoContent {
	    c.Ctx.Response.Header.Del("Content-Type")
	}
	return nil
}
