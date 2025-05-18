package xylium

import (
	"github.com/valyala/fasthttp" // For fasthttp.Cookie and related constants.
)

// xyliumCookie is a helper struct for creating and managing cookies with Xylium.
// Currently, it directly embeds `fasthttp.Cookie`, providing a familiar structure.
// It can be extended in the future to include more Xylium-specific defaults or methods
// for common cookie configurations (e.g., framework-wide secure flag defaults based on mode).
type xyliumCookie struct {
	fasthttp.Cookie // Embeds fasthttp.Cookie for direct access to its fields and methods.
	// Example future extension:
	// FrameworkSecureDefault bool // Could be set based on Xylium's operating mode.
}

// NewxyliumCookie creates a new `xyliumCookie` instance with some sensible defaults,
// making it easier to set common cookies.
// - `name`: The name of the cookie.
// - `value`: The value of the cookie.
// Default settings applied:
// - Path: "/" (cookie is valid for the entire domain).
// - HTTPOnly: true (cookie is not accessible via client-side JavaScript, enhancing security).
// Users can modify these defaults by directly accessing the fields of the returned `xyliumCookie.Cookie`.
func NewxyliumCookie(name, value string) *xyliumCookie {
	xc := &xyliumCookie{}
	xc.SetKey(name)     // Set cookie name.
	xc.SetValue(value)  // Set cookie value.
	xc.SetPath("/")     // Default path.
	xc.SetHTTPOnly(true) // Default HTTPOnly flag.
	// Example: Set a default expiration (e.g., session cookie or a fixed duration).
	// This would require importing "time".
	// xc.SetExpire(time.Now().Add(24 * time.Hour)) // Example: expires in 24 hours.
	// If MaxAge is preferred over Expire:
	// xc.SetMaxAge(24 * 60 * 60) // MaxAge in seconds.
	return xc
}

// SetCookie adds a "Set-Cookie" header to the HTTP response using the provided
// `*fasthttp.Cookie` object. This method allows direct use of `fasthttp.Cookie`
// if preferred over `xyliumCookie`.
// Returns the Context pointer for method chaining.
func (c *Context) SetCookie(cookie *fasthttp.Cookie) *Context {
	if cookie == nil {
		return c // Do nothing if cookie is nil.
	}
	c.Ctx.Response.Header.SetCookie(cookie)
	return c
}

// ClearCookie adds a "Set-Cookie" header to the response that instructs the browser
// to delete the cookie with the specified `name`.
// It achieves this by setting the cookie's value to empty, its path to "/" (common default),
// HTTPOnly to true (common default), and its expiration time to a point in the past
// (using `fasthttp.CookieExpireDelete`).
// For effective deletion, ensure `Path` and `Domain` (if set originally) match the cookie
// being cleared. This method uses common defaults; for precise control, construct and
// set an expiring `fasthttp.Cookie` manually using `c.SetCookie`.
// Returns the Context pointer for method chaining.
func (c *Context) ClearCookie(name string) *Context {
	cookie := fasthttp.AcquireCookie() // Get a cookie object from fasthttp's pool.
	defer fasthttp.ReleaseCookie(cookie) // Return to pool when done.

	cookie.SetKey(name)
	cookie.SetValue("")    // Value can be empty for deletion.
	cookie.SetPath("/")    // Path should ideally match the original cookie's path.
	cookie.SetHTTPOnly(true) // Match common default.
	// `fasthttp.CookieExpireDelete` sets the expiration to a time in the past,
	// signaling the browser to delete the cookie immediately.
	cookie.SetExpire(fasthttp.CookieExpireDelete)
	// Note: If the original cookie had a specific Domain attribute, it should also be set here
	// for the browser to correctly identify and delete the cookie. e.g., cookie.SetDomain("example.com")

	c.Ctx.Response.Header.SetCookie(cookie)
	return c
}

// SetCustomCookie adds a "Set-Cookie" header using the `xyliumCookie` helper struct.
// This provides a Xylium-idiomatic way to set cookies if `xyliumCookie` is extended
// with more framework-specific features in the future. Currently, it's a thin wrapper
// around setting the embedded `fasthttp.Cookie`.
// Returns the Context pointer for method chaining.
func (c *Context) SetCustomCookie(customCookie *xyliumCookie) *Context {
	if customCookie == nil {
		return c // Do nothing if customCookie is nil.
	}
	// Directly use the embedded `fasthttp.Cookie` from `xyliumCookie`.
	c.Ctx.Response.Header.SetCookie(&customCookie.Cookie)
	return c
}

// Note on Reading Cookies:
// Methods for reading request cookies (e.g., `c.Cookie(name)`, `c.Cookies()`)
// are located in `context_request.go`, as they pertain to accessing data
// from the incoming HTTP request.
