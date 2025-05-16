package xylium

import (
	"github.com/valyala/fasthttp"
)

// xyliumCookie is a helper struct for setting cookies with more options or defaults.
// For simplicity, this example directly embeds fasthttp.Cookie but you could add
// more fields or methods for common configurations (e.g. default path, domain, secure flags).
type xyliumCookie struct {
	fasthttp.Cookie
	// You could add more fields here for framework-specific defaults
	// For example:
	// FrameworkSecureDefault bool
}

// NewxyliumCookie creates a new xyliumCookie with some sensible defaults.
func NewxyliumCookie(name, value string) *xyliumCookie {
	fc := &xyliumCookie{}
	fc.SetKey(name)
	fc.SetValue(value)
	fc.SetPath("/")
	fc.SetHTTPOnly(true)
	// Default expiration (e.g., session cookie or a fixed duration)
	// fc.SetExpire(time.Now().Add(24 * time.Hour)) // Jika ingin digunakan, impor "time" lagi
	return fc
}

// SetCookie adds a Set-Cookie header to the response.
// It takes a standard *fasthttp.Cookie.
func (c *Context) SetCookie(cookie *fasthttp.Cookie) *Context {
	c.Ctx.Response.Header.SetCookie(cookie)
	return c
}

// ClearCookie adds a Set-Cookie header to expire a cookie.
// It sets the cookie's expiration to a past time.
func (c *Context) ClearCookie(name string) *Context {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)

	cookie.SetKey(name)
	cookie.SetValue("") // Value can be empty
	cookie.SetPath("/")  // Path should match the original cookie's path
	// Consider other attributes like Domain, HTTPOnly, Secure if they were set on original.
	cookie.SetHTTPOnly(true)
	cookie.SetExpire(fasthttp.CookieExpireDelete) // Tell browser to delete immediately

	c.Ctx.Response.Header.SetCookie(cookie)
	return c
}

// SetCustomCookie adds a Set-Cookie header using the xyliumCookie helper struct.
func (c *Context) SetCustomCookie(customCookie *xyliumCookie) *Context {
	// Directly use the embedded fasthttp.Cookie
	c.Ctx.Response.Header.SetCookie(&customCookie.Cookie)
	return c
}

// Note: Methods for reading request cookies (c.Cookie, c.Cookies)
// are located in context_request.go as they pertain to incoming request data.
