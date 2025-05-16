package xylium

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/valyala/fasthttp"
)

// --- Response Writing ---

// SetDefaultContentType sets the Content-Type header to "text/plain; charset=utf-8"
// if it has not been set yet. This is called automatically by Write and WriteString.
func (c *Context) SetDefaultContentType() {
	c.responseOnce.Do(func() {
		if len(c.Ctx.Response.Header.Peek("Content-Type")) == 0 {
			c.Ctx.Response.Header.SetContentTypeBytes([]byte("text/plain; charset=utf-8"))
		}
	})
}

// Status sets the HTTP response status code.
func (c *Context) Status(code int) *Context {
	c.Ctx.SetStatusCode(code)
	return c
}

// SetHeader sets a response header.
func (c *Context) SetHeader(key, value string) *Context {
	c.Ctx.Response.Header.Set(key, value)
	return c
}

// SetContentType sets the Content-Type response header.
func (c *Context) SetContentType(contentType string) *Context {
	c.Ctx.Response.Header.SetContentType(contentType)
	return c
}

// Write writes a byte slice to the response body.
// It sets a default Content-Type if not already set.
func (c *Context) Write(p []byte) error {
	c.SetDefaultContentType()
	_, err := c.Ctx.Write(p)
	return err
}

// WriteString writes a string to the response body.
// It sets a default Content-Type if not already set.
func (c *Context) WriteString(s string) error {
	c.SetDefaultContentType()
	_, err := c.Ctx.WriteString(s)
	return err
}

// JSON sends a JSON response with the given status code and data.
// If data is []byte, it's written directly. Otherwise, it's marshalled to JSON.
func (c *Context) JSON(code int, data interface{}) error {
	c.Status(code).SetContentType("application/json; charset=utf-8")
	if b, ok := data.([]byte); ok {
		return c.Write(b)
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return NewHTTPError(StatusInternalServerError, "JSON marshal error").WithInternal(err)
	}
	return c.Write(jsonData)
}

// XML sends an XML response with the given status code and data.
// If data is []byte, it's written directly. Otherwise, it's marshalled to XML.
func (c *Context) XML(code int, data interface{}) error {
	c.Status(code).SetContentType("application/xml; charset=utf-8")
	if b, ok := data.([]byte); ok {
		return c.Write(b)
	}
	xmlData, err := xml.Marshal(data)
	if err != nil {
		return NewHTTPError(StatusInternalServerError, "XML marshal error").WithInternal(err)
	}
	return c.Write(xmlData)
}

// String sends a plain text response with the given status code and formatted string.
func (c *Context) String(code int, s string, values ...interface{}) error {
	c.Status(code).SetContentType("text/plain; charset=utf-8")
	if len(values) > 0 {
		return c.WriteString(fmt.Sprintf(s, values...))
	}
	return c.WriteString(s)
}

// HTML renders an HTML template and sends it as a response.
// Requires an HTMLRenderer to be configured on the Router.
func (c *Context) HTML(code int, name string, data interface{}) error {
	if c.router == nil || c.router.HTMLRenderer == nil {
		return NewHTTPError(StatusInternalServerError, "HTML renderer not configured on router")
	}
	c.Status(code).SetContentType("text/html; charset=utf-8")
	// Render directly to the response body writer
	return c.router.HTMLRenderer.Render(c.Ctx.Response.BodyWriter(), name, data, c)
}

// File sends a file as a response.
// It checks for file existence and handles errors.
func (c *Context) File(filepathToServe string) error {
	absPath, err := filepath.Abs(filepathToServe)
	if err != nil {
		return NewHTTPError(StatusInternalServerError, "Invalid file path").WithInternal(err)
	}
	// Check if file exists and is not a directory before calling ServeFile
	// os.Stat is important here as ServeFile might not return clear errors for all cases.
	info, statErr := os.Stat(absPath)
	if os.IsNotExist(statErr) {
		return NewHTTPError(StatusNotFound, StatusText(StatusNotFound)).WithInternal(statErr)
	}
	if statErr != nil {
		return NewHTTPError(StatusInternalServerError, "Error accessing file").WithInternal(statErr)
	}
	if info.IsDir() {
		return NewHTTPError(StatusForbidden, "Cannot serve directory").WithInternal(fmt.Errorf("path is a directory: %s", absPath))
	}

	fasthttp.ServeFile(c.Ctx, absPath)
	// fasthttp.ServeFile commits the response.
	// If status code is OK, it implies success.
	// No specific action needed here if ResponseCommitted is true and status is OK,
	// as ServeFile handles the response.
	// If ServeFile itself encounters an issue, it will typically set an error status code.
	return nil
}

// Attachment sends a file as an attachment, prompting the user to download it.
func (c *Context) Attachment(filepathToServe string, downloadFilename string) error {
	c.SetHeader("Content-Disposition", `attachment; filename="`+url.PathEscape(downloadFilename)+`"`)
	// Content-Type will be set by fasthttp.ServeFile based on file extension
	return c.File(filepathToServe)
}

// Redirect sends a redirect response to a new location with the given status code.
// Code should be a 3xx redirect status code.
func (c *Context) Redirect(location string, code int) error {
	// Validate redirect code (common practice)
	if code < StatusMultipleChoices || code > StatusPermanentRedirect || code == StatusNotModified {
		code = StatusFound // Default to 302 Found
	}
	c.Ctx.Redirect(location, code)
	return nil // Redirect itself doesn't produce an error for the handler chain
}

// Error sends an error response using fasthttp's built-in error handler.
// This is a low-level way to send an error and might bypass custom error handling.
// Prefer returning an *HTTPError from handlers.
func (c *Context) Error(message string, code int) error {
	c.Ctx.Error(message, code)
	return nil // Error commits the response
}

// NoContent sends a response with the given status code and no body.
// Useful for 204 No Content responses.
func (c *Context) NoContent(code int) error {
	c.Status(code)
	c.Ctx.Response.ResetBody() // Ensure no body is sent
	// Content-Type is not strictly necessary for 204 but fasthttp might set one.
	// If you want to be explicit about no content type:
	// c.Ctx.Response.Header.Del("Content-Type")
	return nil
}
