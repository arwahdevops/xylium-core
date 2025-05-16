package xylium

import (
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"

)

// --- Request Information ---

// Method returns the HTTP request method.
func (c *Context) Method() string { return string(c.Ctx.Method()) }

// Path returns the request path.
func (c *Context) Path() string { return string(c.Ctx.Path()) }

// URI returns the request URI.
func (c *Context) URI() string { return string(c.Ctx.RequestURI()) }

// IP returns the remote IP address of the client.
func (c *Context) IP() string { return c.Ctx.RemoteIP().String() }

// RealIP attempts to get the real client IP address.
func (c *Context) RealIP() string {
	if ip := c.Header("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip := c.Header("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	return c.IP()
}

// Scheme returns the request scheme (http or https).
func (c *Context) Scheme() string {
	if c.Ctx.IsTLS() {
		return "https"
	}
	if proto := c.Header("X-Forwarded-Proto"); proto != "" {
		return strings.ToLower(proto)
	}
	return "http"
}

// Host returns the host from the request.
func (c *Context) Host() string { return string(c.Ctx.Host()) }

// UserAgent returns the client's User-Agent header.
func (c *Context) UserAgent() string { return string(c.Ctx.UserAgent()) }

// Referer returns the client's Referer header.
func (c *Context) Referer() string { return string(c.Ctx.Referer()) }

// ContentType returns the Content-Type header of the request.
func (c *Context) ContentType() string { return string(c.Ctx.Request.Header.ContentType()) }

// IsTLS returns true if the connection is TLS.
func (c *Context) IsTLS() bool { return c.Ctx.IsTLS() }

// IsAJAX returns true if the request is an AJAX request.
func (c *Context) IsAJAX() bool { return c.Header("X-Requested-With") == "XMLHttpRequest" }

// Header returns the value of a request header.
func (c *Context) Header(key string) string { return string(c.Ctx.Request.Header.Peek(key)) }

// Headers returns all request headers.
func (c *Context) Headers() map[string]string {
	h := make(map[string]string)
	c.Ctx.Request.Header.VisitAll(func(k, v []byte) { h[string(k)] = string(v) })
	return h
}

// --- Request Data: Query, Form, Params, Cookies ---

// Param returns the value of a route parameter.
func (c *Context) Param(name string) string {
	return c.Params[name]
}

// ParamInt attempts to parse a route parameter as an integer.
func (c *Context) ParamInt(name string) (int, error) {
	s, ok := c.Params[name]
	if !ok {
		return 0, fmt.Errorf("param '%s' not found", name)
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("param '%s' ('%s') is not an integer: %w", name, s, err)
	}
	return i, nil
}

// ParamIntDefault attempts to parse a route parameter as an integer,
// returning a default value if not found or parsing fails.
func (c *Context) ParamIntDefault(name string, def int) int {
	v, err := c.ParamInt(name)
	if err != nil {
		return def
	}
	return v
}

// QueryParam returns the value of a URL query parameter.
func (c *Context) QueryParam(key string) string {
	if c.queryArgs == nil {
		c.queryArgs = c.Ctx.QueryArgs()
	}
	return string(c.queryArgs.Peek(key))
}

// QueryParams returns all URL query parameters as a map.
func (c *Context) QueryParams() map[string]string {
	if c.queryArgs == nil {
		c.queryArgs = c.Ctx.QueryArgs()
	}
	p := make(map[string]string)
	c.queryArgs.VisitAll(func(k, v []byte) { p[string(k)] = string(v) })
	return p
}

// QueryParamInt attempts to parse a URL query parameter as an integer.
func (c *Context) QueryParamInt(key string) (int, error) {
	s := c.QueryParam(key)
	if s == "" {
		return 0, fmt.Errorf("query parameter '%s' not found or empty", key)
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("query parameter '%s' ('%s') is not an integer: %w", key, s, err)
	}
	return i, nil
}

// QueryParamIntDefault attempts to parse a URL query parameter as an integer,
// returning a default value if not found or parsing fails.
func (c *Context) QueryParamIntDefault(key string, def int) int {
	v, err := c.QueryParamInt(key)
	if err != nil {
		return def
	}
	return v
}

// FormValue returns the value of a form field from POST or PUT body.
func (c *Context) FormValue(key string) string {
	return string(c.Ctx.FormValue(key))
}

// PostFormValue returns the value of a form field specifically from POST arguments.
func (c *Context) PostFormValue(key string) string {
	if c.formArgs == nil {
		c.formArgs = c.Ctx.PostArgs()
	}
	return string(c.formArgs.Peek(key))
}

// PostFormParams returns all POST form parameters as a map.
func (c *Context) PostFormParams() map[string]string {
	if c.formArgs == nil {
		c.formArgs = c.Ctx.PostArgs()
	}
	p := make(map[string]string)
	c.formArgs.VisitAll(func(k, v []byte) { p[string(k)] = string(v) })
	return p
}

// FormFile returns the first file for the provided form key.
func (c *Context) FormFile(key string) (*multipart.FileHeader, error) {
	return c.Ctx.FormFile(key)
}

// MultipartForm parses a multipart form request.
func (c *Context) MultipartForm() (*multipart.Form, error) {
	return c.Ctx.MultipartForm()
}

// Body returns the raw request body as a byte slice.
func (c *Context) Body() []byte {
	return c.Ctx.PostBody()
}

// Cookie returns the value of a request cookie.
func (c *Context) Cookie(name string) string {
	return string(c.Ctx.Request.Header.Cookie(name))
}

// Cookies returns all request cookies as a map.
func (c *Context) Cookies() map[string]string {
	ck := make(map[string]string)
	c.Ctx.Request.Header.VisitAllCookie(func(k, v []byte) { ck[string(k)] = string(v) })
	return ck
}
