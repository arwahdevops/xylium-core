# Xylium Routing

Xylium uses an efficient radix tree-based router to map incoming HTTP requests to their corresponding handlers. It supports static paths, named path parameters, and catch-all parameters, along with robust route grouping and middleware attachment capabilities.

## Table of Contents

*   [1. Basic HTTP Method Routes](#1-basic-http-method-routes)
*   [2. Routes with Path Parameters](#2-routes-with-path-parameters)
    *   [2.1. Named Parameters](#21-named-parameters)
    *   [2.2. Reading Path Parameters](#22-reading-path-parameters)
*   [3. Catch-All Routes](#3-catch-all-routes)
*   [4. Route Grouping](#4-route-grouping)
    *   [4.1. Basic Grouping](#41-basic-grouping)
    *   [4.2. Nested Groups](#42-nested-groups)
    *   [4.3. Group Middleware](#43-group-middleware)
*   [5. Serving Static Files](#5-serving-static-files)
    *   [5.1. Serving a Directory](#51-serving-a-directory)
    *   [5.2. Serving a Single Static File](#52-serving-a-single-static-file)
*   [6. Custom Not Found (404) Handler](#6-custom-not-found-404-handler)
*   [7. Custom Method Not Allowed (405) Handler](#7-custom-method-not-allowed-405-handler)
*   [8. Route Matching Order](#8-route-matching-order)
*   [9. Printing Registered Routes](#9-printing-registered-routes)

---

## 1. Basic HTTP Method Routes

Xylium provides methods on the `Router` (or `RouteGroup`) instance to register handlers for common HTTP methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, and `OPTIONS`.

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	// GET request
	app.GET("/ping", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, xylium.M{"message": "pong"})
	})

	// POST request
	app.POST("/users", func(c *xylium.Context) error {
		// ... logic to create a user ...
		return c.JSON(http.StatusCreated, xylium.M{"message": "User created"})
	})

	// PUT request
	app.PUT("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to update user with userID ...
		return c.JSON(http.StatusOK, xylium.M{"message": "User " + userID + " updated"})
	})

	// DELETE request
	app.DELETE("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to delete user with userID ...
		return c.String(http.StatusOK, "User "+userID+" deleted")
	})

	// PATCH request
	app.PATCH("/users/:id/profile", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to partially update user's profile ...
		return c.JSON(http.StatusOK, xylium.M{"message": "Profile for user " + userID + " patched"})
	})

	// HEAD request
	app.HEAD("/status", func(c *xylium.Context) error {
		// Typically, set headers and no body for HEAD requests.
		// Xylium handles not sending a body if Content-Length is 0 or not set.
		c.SetHeader("X-App-Status", "healthy")
		return c.NoContent(http.StatusOK) // Or c.Status(http.StatusOK).String("")
	})

	// OPTIONS request
	app.OPTIONS("/resource", func(c *xylium.Context) error {
		c.SetHeader("Allow", "GET, POST, OPTIONS")
		return c.NoContent(http.StatusNoContent)
	})

	app.Start(":8080")
}
```
Each method registration takes a path string, a `xylium.HandlerFunc`, and optional route-specific middleware.

## 2. Routes with Path Parameters

Path parameters allow you to capture dynamic segments from the URL path.

### 2.1. Named Parameters

Named parameters are defined by prefixing a path segment with a colon (`:`).
Example: `/users/:id`, `/books/:category/:bookId`

```go
// Matches /users/123, /users/abc, etc.
// 'id' will be "123" or "abc"
app.GET("/users/:id", GetUserHandler)

// Matches /orders/2023/inv-001
// 'year' will be "2023", 'invoiceId' will be "inv-001"
app.GET("/orders/:year/:invoiceId", GetOrderHandler)
```

### 2.2. Reading Path Parameters

Inside your handler, you can access the values of named parameters using `c.Param(name string) string`.

```go
func GetUserHandler(c *xylium.Context) error {
	userID := c.Param("id") // "id" is the name from ":id"
	// ... fetch user by userID ...
	return c.String(http.StatusOK, "Fetching user with ID: %s", userID)
}

func GetOrderHandler(c *xylium.Context) error {
	year := c.Param("year")
	invoiceID := c.Param("invoiceId")
	return c.JSON(http.StatusOK, xylium.M{
		"message":    "Fetching order details",
		"year":       year,
		"invoice_id": invoiceID,
	})
}
```
Xylium also provides helpers like `c.ParamInt(name string) (int, error)` and `c.ParamIntDefault(name string, def int) int` for convenient type conversion. See `ContextBinding.md` or `RequestHandling.md` for more details.

## 3. Catch-All Routes

Catch-all parameters capture all path segments from their position to the end of the URL. They are defined by prefixing a path segment with an asterisk (`*`). A catch-all parameter must be the last segment in a route pattern.

Example: `/static/*filepath`

```go
// Matches /static/css/style.css -> filepath = "css/style.css"
// Matches /static/images/logo.png -> filepath = "images/logo.png"
// Matches /static/some/deep/path/file.js -> filepath = "some/deep/path/file.js"
app.GET("/serve/*resourcepath", func(c *xylium.Context) error {
	resourcePath := c.Param("resourcepath")
	// ... logic to serve the file at resourcePath ...
	return c.String(http.StatusOK, "Serving resource: %s", resourcePath)
})
```
This is commonly used for serving static files or proxying requests.

## 4. Route Grouping

Route grouping allows you to organize routes under a common path prefix and apply shared middleware to all routes within that group.

### 4.1. Basic Grouping

Use `app.Group(prefix string, groupMiddleware ...xylium.Middleware)` to create a new group.

```go
apiV1 := app.Group("/api/v1")
{ // Braces are optional but improve readability
	apiV1.GET("/users", ListUsersHandler)    // Path: /api/v1/users
	apiV1.POST("/users", CreateUserHandler)  // Path: /api/v1/users
	apiV1.GET("/products", ListProducts) // Path: /api/v1/products
}
```

### 4.2. Nested Groups

Groups can be nested to create more complex path structures.

```go
adminGroup := app.Group("/admin")
{
	adminGroup.GET("/dashboard", AdminDashboardHandler) // /admin/dashboard

	// Nested group for user management within admin
	userManagementGroup := adminGroup.Group("/users")
	{
		userManagementGroup.GET("/", AdminListUsersHandler)         // /admin/users
		userManagementGroup.GET("/:id", AdminGetUserHandler)       // /admin/users/:id
		userManagementGroup.POST("/:id/ban", AdminBanUserHandler)  // /admin/users/:id/ban
	}
}
```

### 4.3. Group Middleware

Middleware can be applied at the group level. This middleware will execute for all routes defined within that group and its subgroups.

```go
// ExampleAuthMiddleware is a placeholder for actual authentication middleware
func ExampleAuthMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		c.Logger().Info("Auth Middleware called for group")
		// ... authentication logic ...
		// if !authenticated { return c.Status(http.StatusUnauthorized).String("Unauthorized") }
		return next(c)
	}
}

// Apply AuthMiddleware to the /admin group and all its routes
adminSecureGroup := app.Group("/admin", ExampleAuthMiddleware)
{
	adminSecureGroup.GET("/settings", AdminSettingsHandler) // AuthMiddleware runs first
	// More admin routes...
}
```
Route-specific middleware can also be added to routes within a group, and they will run after the group's middleware.

## 5. Serving Static Files

### 5.1. Serving a Directory

Xylium provides `app.ServeFiles(urlPathPrefix string, fileSystemRoot string)` to serve static files from a directory.

```go
// Serve files from the "./public_assets" directory under the "/static" URL prefix.
// e.g., request to "/static/css/main.css" will serve "./public_assets/css/main.css"
app.ServeFiles("/static", "./public_assets")

// To serve from the root URL path:
// app.ServeFiles("/", "./public_html") // Caution: Ensure no route conflicts
```
`ServeFiles` uses `fasthttp.FS` internally, which handles:
*   Serving `index.html` by default if a directory is requested.
*   Setting appropriate `Content-Type` headers based on file extension.
*   Byte range requests.
*   Compression (if `fasthttp.FS.Compress` is enabled, which it is by default in Xylium's `ServeFiles` usage).
*   Path not found handling (returns a JSON 404 error by default).

See `router.go` (`Router.ServeFiles`) for implementation details of the path not found handler.

### 5.2. Serving a Single Static File

To serve a single specific file, like a `favicon.ico` or `robots.txt`, you can define a regular route and use `c.File(filepathToServe string)`.

```go
// Serve favicon.ico from the root
app.GET("/favicon.ico", func(c *xylium.Context) error {
	return c.File("./static/favicon.ico") // Path to your favicon file
})

app.GET("/robots.txt", func(c *xylium.Context) error {
	return c.File("./static/robots.txt")
})
```
`c.File()` also uses `fasthttp.ServeFile` for efficient serving.

## 6. Custom Not Found (404) Handler

When no route matches the requested path, Xylium invokes the `Router.NotFoundHandler`. You can replace the default 404 handler.

```go
app.NotFoundHandler = func(c *xylium.Context) error {
	c.Logger().Warnf("Custom 404: Path '%s' not found.", c.Path())
	// You can render an HTML 404 page or return a custom JSON response
	return c.Status(http.StatusNotFound).JSON(xylium.M{
		"error_code": "RESOURCE_NOT_FOUND",
		"message":    "The page you are looking for does not exist.",
		"path":       c.Path(),
	})
}
```
This handler should be set on the `app` instance *before* starting the server. See `ErrorHandling.md` for more on the default handler.

## 7. Custom Method Not Allowed (405) Handler

If a route path exists but not for the HTTP method used in the request, Xylium invokes `Router.MethodNotAllowedHandler`. The `Allow` header, listing permitted methods, is automatically set by the router before calling this handler.

```go
app.MethodNotAllowedHandler = func(c *xylium.Context) error {
	allowedMethods := c.Header("Allow") // Router sets this based on tree.Find results
	c.Logger().Warnf("Custom 405: Method '%s' not allowed for path '%s'. Allowed: [%s]",
		c.Method(), c.Path(), allowedMethods)

	return c.Status(http.StatusMethodNotAllowed).JSON(xylium.M{
		"error_code":     "METHOD_NOT_SUPPORTED",
		"message":        "The request method is not supported for this resource.",
		"requested_method": c.Method(),
		"allowed_methods":  strings.Split(allowedMethods, ", "), // Convert to slice for JSON
	})
}
```
See `ErrorHandling.md` for more on the default handler.

## 8. Route Matching Order

Xylium's radix tree router matches routes with the following priority:
1.  **Static Routes**: Exact path matches (e.g., `/users/profile`) have the highest priority.
2.  **Named Parameter Routes**: Routes with path parameters (e.g., `/users/:id`) are matched next if no static route fits.
3.  **Catch-All Routes**: Routes with catch-all parameters (e.g., `/files/*filepath`) have the lowest priority and match if no static or named parameter route fits.

Within the same priority level (e.g., multiple static routes at the same tree depth), the router's behavior is deterministic, but relying on specific ordering of equally specific routes is generally discouraged. Design your routes to be unambiguous.

If a path is matched but the HTTP method is not defined for that path, the `MethodNotAllowedHandler` is invoked. If no path matches at all, the `NotFoundHandler` is invoked.

## 9. Printing Registered Routes

For debugging purposes, especially in `DebugMode`, Xylium can print all registered routes to the logger when the server starts. This functionality is built into the `Tree.PrintRoutes(logger Logger)` method, which is called by the router's server startup methods (`ListenAndServeGracefully`, etc.) when in `DebugMode`.

Example log output in `DebugMode`:
```
[XYLIUM-BOOTSTRAP] Mode set to 'debug' from internal default.
...
[XYLIUM-ROUTER] Xylium Router initialized (Adopting Mode: debug, Determined By: internal_default)
...
[XYLIUM-ROUTER] Printing registered routes for ListenAndServeGracefully on :8080:
[XYLIUM-TREE] Xylium Registered Routes (Radix Tree Structure):
[XYLIUM-TREE]   GET     /ping
[XYLIUM-TREE]   POST    /users
[XYLIUM-TREE]   PUT     /users/:id
[XYLIUM-TREE]   DELETE  /users/:id
[XYLIUM-TREE]   GET     /serve/*resourcepath
[XYLIUM-TREE]   GET     /api/v1/users
[XYLIUM-TREE]   POST    /api/v1/users
...
[XYLIUM-ROUTER] Xylium server listening gracefully on :8080 (Mode: debug)
```
This provides a clear overview of your application's routing table.
