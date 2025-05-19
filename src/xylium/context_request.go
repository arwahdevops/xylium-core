package xylium

import (
	"fmt"            // For error formatting in ParamInt, QueryParamInt.
	"mime/multipart" // For FormFile, MultipartForm types.
	"strconv"        // For parsing string parameters to integers.
	"strings"        // For string manipulation in RealIP, Scheme.
)

// --- Request Information ---
// This section provides methods to access various properties of the incoming HTTP request.

// Method returns the HTTP request method string (e.g., "GET", "POST").
func (c *Context) Method() string { return string(c.Ctx.Method()) }

// Path returns the request path string (e.g., "/users/123").
// This is the path part of the URI, without query parameters.
func (c *Context) Path() string { return string(c.Ctx.Path()) }

// URI returns the full request URI string, including the path and query parameters
// (e.g., "/search?query=xylium&limit=10").
func (c *Context) URI() string { return string(c.Ctx.RequestURI()) }

// IP returns the remote IP address of the client making the request, as seen by the server.
// This might be the IP of a proxy if the server is behind one. For a more accurate
// client IP, consider using `RealIP()`.
func (c *Context) IP() string { return c.Ctx.RemoteIP().String() }

// RealIP attempts to determine the real client IP address by checking common proxy headers
// like "X-Forwarded-For" and "X-Real-IP". If these headers are not present or are malformed,
// it falls back to `c.IP()`.
// Note: Trust in these headers depends on the deployment environment. Ensure proxies correctly
// set/append to "X-Forwarded-For" and that "X-Real-IP" is set by a trusted proxy.
func (c *Context) RealIP() string {
	// Check "X-Forwarded-For" header. It can contain a comma-separated list of IPs;
	// the first one is typically the original client IP.
	if ip := c.Header("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0]) // Return the first IP in the list.
	}
	// Check "X-Real-IP" header, often set by reverse proxies like Nginx.
	if ip := c.Header("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	// Fallback to the direct remote IP if no proxy headers are found.
	return c.IP()
}

// Scheme returns the request scheme ("http" or "https").
// It checks if the connection is TLS and also considers the "X-Forwarded-Proto"
// header, which is often set by reverse proxies terminating TLS.
func (c *Context) Scheme() string {
	if c.Ctx.IsTLS() { // Check direct TLS connection.
		return "https"
	}
	// Check "X-Forwarded-Proto" header (e.g., set by load balancers/reverse proxies).
	if proto := c.Header("X-Forwarded-Proto"); proto != "" {
		return strings.ToLower(proto) // Normalize to lowercase (e.g., "HTTPS" -> "https").
	}
	// Default to "http" if no TLS or proxy header indicates otherwise.
	return "http"
}

// Host returns the host from the request's "Host" header.
func (c *Context) Host() string { return string(c.Ctx.Host()) }

// UserAgent returns the client's User-Agent header string.
func (c *Context) UserAgent() string { return string(c.Ctx.UserAgent()) }

// Referer returns the client's Referer (or Referrer) header string, indicating
// the URL of the page that linked to the current resource.
func (c *Context) Referer() string { return string(c.Ctx.Referer()) }

// ContentType returns the Content-Type header of the request body.
// Example: "application/json", "application/x-www-form-urlencoded".
func (c *Context) ContentType() string { return string(c.Ctx.Request.Header.ContentType()) }

// IsTLS returns true if the underlying connection to the server is TLS (HTTPS).
func (c *Context) IsTLS() bool { return c.Ctx.IsTLS() }

// IsAJAX (or IsXHR) returns true if the request appears to be an AJAX (XMLHttpRequest) request.
// It checks for the presence of the "X-Requested-With: XMLHttpRequest" header,
// which is a common convention, though not a formal standard.
func (c *Context) IsAJAX() bool { return c.Header("X-Requested-With") == "XMLHttpRequest" }

// Header returns the value of a specific request header by its key.
// Header keys are typically case-insensitive. `fasthttp` normalizes them.
func (c *Context) Header(key string) string { return string(c.Ctx.Request.Header.Peek(key)) }

// Headers returns all request headers as a map[string]string.
// Note: HTTP headers can have multiple values for the same key; this method
// typically returns the first or a comma-separated value depending on fasthttp's behavior.
// For full control over multi-value headers, access `c.Ctx.Request.Header` directly.
func (c *Context) Headers() map[string]string {
	h := make(map[string]string)
	c.Ctx.Request.Header.VisitAll(func(k, v []byte) { h[string(k)] = string(v) })
	return h
}

// --- Request Data: Route Parameters, Query Parameters, Form Data, Cookies ---

// Param returns the value of a route parameter extracted from the URL path.
// Route parameters are defined in route patterns (e.g., "/users/:id", where "id" is the parameter name).
// Returns an empty string if the parameter `name` is not found.
func (c *Context) Param(name string) string {
	// c.Params is populated by the router during route matching.
	return c.Params[name]
}

// ParamInt attempts to parse a route parameter as an integer.
// Returns the integer value and an error if the parameter is not found or
// if its value cannot be parsed into an integer.
func (c *Context) ParamInt(name string) (int, error) {
	s, ok := c.Params[name]
	if !ok {
		return 0, fmt.Errorf("route parameter '%s' not found", name)
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("route parameter '%s' (value: '%s') is not a valid integer: %w", name, s, err)
	}
	return i, nil
}

// ParamIntDefault attempts to parse a route parameter as an integer.
// If the parameter is not found or parsing fails, it returns the provided `def` (default) value.
// This is a convenience method to avoid manual error checking when a default is acceptable.
func (c *Context) ParamIntDefault(name string, def int) int {
	v, err := c.ParamInt(name)
	if err != nil {
		return def // Return default value on error.
	}
	return v
}

// QueryParam returns the value of a URL query parameter by its key.
// For a URL like "/search?query=xylium&limit=10", `c.QueryParam("query")` returns "xylium".
// Returns an empty string if the key is not found.
// Query arguments are parsed and cached on first access.
func (c *Context) QueryParam(key string) string {
	if c.queryArgs == nil {
		c.queryArgs = c.Ctx.QueryArgs() // Parse and cache query arguments.
	}
	return string(c.queryArgs.Peek(key))
}

// QueryParams returns all URL query parameters as a map[string]string.
// Note: If a query parameter key appears multiple times, `fasthttp`'s `Peek` (used by `QueryParam`)
// typically returns the first value. This method will also reflect that behavior for each key.
// For full access to multi-value query parameters, use `c.Ctx.QueryArgs().PeekMulti(key)`.
// Query arguments are parsed and cached on first access.
func (c *Context) QueryParams() map[string]string {
	if c.queryArgs == nil {
		c.queryArgs = c.Ctx.QueryArgs() // Parse and cache.
	}
	p := make(map[string]string)
	c.queryArgs.VisitAll(func(k, v []byte) { p[string(k)] = string(v) })
	return p
}

// QueryParamInt attempts to parse a URL query parameter as an integer.
// Returns the integer value and an error if the key is not found, the value is empty,
// or the value cannot be parsed into an integer.
func (c *Context) QueryParamInt(key string) (int, error) {
	s := c.QueryParam(key) // Uses cached queryArgs if already parsed.
	if s == "" {
		// Distinguish between key not found and key found with empty value.
		// c.Ctx.QueryArgs().Has(key) could be used, but simple empty check is often sufficient.
		return 0, fmt.Errorf("query parameter '%s' not found or is empty", key)
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("query parameter '%s' (value: '%s') is not a valid integer: %w", key, s, err)
	}
	return i, nil
}

// QueryParamIntDefault attempts to parse a URL query parameter as an integer.
// If the key is not found, parsing fails, or the value is empty, it returns the
// provided `def` (default) value.
func (c *Context) QueryParamIntDefault(key string, def int) int {
	v, err := c.QueryParamInt(key)
	if err != nil {
		return def // Return default value on error.
	}
	return v
}

// FormValue returns the value of a form field from a POST or PUT request body.
// It supports "application/x-www-form-urlencoded" and "multipart/form-data" content types.
// Returns an empty string if the key is not found.
// Form arguments are parsed from the request body by `fasthttp` and cached.
func (c *Context) FormValue(key string) string {
	// c.Ctx.FormValue() handles parsing PostArgs if necessary.
	return string(c.Ctx.FormValue(key))
}

// PostFormValue returns the value of a form field specifically from POST arguments
// (from the request body, not URL query parameters).
// Similar to `FormValue` but more specific to `c.Ctx.PostArgs()`.
// Form arguments are parsed and cached on first access.
func (c *Context) PostFormValue(key string) string {
	if c.formArgs == nil {
		_ = c.Ctx.PostArgs() // Ensure PostArgs are parsed and cached.
		c.formArgs = c.Ctx.PostArgs()
	}
	return string(c.formArgs.Peek(key))
}

// PostFormParams returns all POST form parameters (from the request body) as a map[string]string.
// Form arguments are parsed and cached on first access.
func (c *Context) PostFormParams() map[string]string {
	if c.formArgs == nil {
		_ = c.Ctx.PostArgs() // Ensure PostArgs are parsed and cached.
		c.formArgs = c.Ctx.PostArgs()
	}
	p := make(map[string]string)
	c.formArgs.VisitAll(func(k, v []byte) { p[string(k)] = string(v) })
	return p
}

// FormFile returns the first file uploaded for the provided form key in a "multipart/form-data" request.
// It returns a `*multipart.FileHeader` (containing file metadata and an interface to read the file)
// and an error if the key is not found or if there's an issue retrieving the file.
func (c *Context) FormFile(key string) (*multipart.FileHeader, error) {
	// c.Ctx.FormFile() handles parsing the multipart form if necessary.
	return c.Ctx.FormFile(key)
}

// MultipartForm parses a "multipart/form-data" request body.
// It returns a `*multipart.Form` containing both form field values and uploaded files.
// Returns an error if the request body is not multipart or if parsing fails.
// The form is parsed by `fasthttp` and cached.
func (c *Context) MultipartForm() (*multipart.Form, error) {
	// c.Ctx.MultipartForm() handles parsing and caching.
	return c.Ctx.MultipartForm()
}

// Body returns the raw request body as a byte slice.
// For "multipart/form-data" requests, this might return the raw, unparsed body.
// If you need parsed form data or files, use `FormValue`, `FormFile`, or `MultipartForm`.
func (c *Context) Body() []byte {
	return c.Ctx.PostBody() // `fasthttp` caches the PostBody.
}

// Cookie returns the value of a request cookie by its name.
// Returns an empty string if the cookie is not found.
func (c *Context) Cookie(name string) string {
	return string(c.Ctx.Request.Header.Cookie(name))
}

// Cookies returns all request cookies as a map[string]string.
func (c *Context) Cookies() map[string]string {
	ck := make(map[string]string)
	c.Ctx.Request.Header.VisitAllCookie(func(k, v []byte) { ck[string(k)] = string(v) })
	return ck
}
