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
    *   [5.1. Serving a Directory (`app.ServeFiles()`)](#51-serving-a-directory-appservefiles)
    *   [5.2. Serving a Single Static File (`c.File()`)](#52-serving-a-single-static-file-cfile)
*   [6. Custom Not Found (404) Handler (`Router.NotFoundHandler`)](#6-custom-not-found-404-handler-routernotfoundhandler)
*   [7. Custom Method Not Allowed (405) Handler (`Router.MethodNotAllowedHandler`)](#7-custom-method-not-allowed-405-handler-routermethodnotallowedhandler)
*   [8. Route Matching Order](#8-route-matching-order)
*   [9. Printing Registered Routes](#9-printing-registered-routes)

---

## 1. Basic HTTP Method Routes

Xylium provides methods on the `Router` (or `RouteGroup`) instance to register handlers for common HTTP methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, and `OPTIONS`.

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	// GET request
	app.GET("/ping", func(c *xylium.Context) error {
		return c.JSON(xylium.StatusOK, xylium.M{"message": "pong"})
	})

	// POST request
	app.POST("/users", func(c *xylium.Context) error {
		// ... logic to create a user ...
		return c.JSON(xylium.StatusCreated, xylium.M{"message": "User created"})
	})

	// PUT request
	app.PUT("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to update user with userID ...
		return c.JSON(xylium.StatusOK, xylium.M{"message": "User " + userID + " updated"})
	})

	// DELETE request
	app.DELETE("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to delete user with userID ...
		return c.String(xylium.StatusOK, "User "+userID+" deleted")
	})

	// PATCH request
	app.PATCH("/users/:id/profile", func(c *xylium.Context) error {
		userID := c.Param("id")
		// ... logic to partially update user's profile ...
		return c.JSON(xylium.StatusOK, xylium.M{"message": "Profile for user " + userID + " patched"})
	})

	// HEAD request
	app.HEAD("/status", func(c *xylium.Context) error {
		// Typically, set headers and no body for HEAD requests.
		// Xylium handles not sending a body if Content-Length is 0 or not set,
		// or if a "no content" status like xylium.StatusNoContent is used.
		c.SetHeader("X-App-Status", "healthy")
		return c.NoContent(xylium.StatusOK) // Or c.Status(xylium.StatusOK).String("") if headers are enough
	})

	// OPTIONS request
	app.OPTIONS("/resource", func(c *xylium.Context) error {
		c.SetHeader("Allow", "GET, POST, OPTIONS") // Example: Indicate allowed methods
		return c.NoContent(xylium.StatusNoContent)
	})

	// app.Start(":8080") // Assuming this is defined for a runnable example
}
```
Each method registration takes a path string, a `xylium.HandlerFunc`, and optional route-specific middleware.

## 2. Routes with Path Parameters

Path parameters allow you to capture dynamic segments from the URL path.

### 2.1. Named Parameters

Named parameters are defined by prefixing a path segment with a colon (`:`).
Example: `/users/:id`, `/books/:category/:bookId`

```go
// func GetUserHandler(c *xylium.Context) error { /* ... */ return nil }
// func GetOrderHandler(c *xylium.Context) error { /* ... */ return nil }
// app := xylium.New() // Assuming app is initialized

// Matches /users/123, /users/abc, etc.
// 'id' will be "123" or "abc"
// app.GET("/users/:id", GetUserHandler)

// Matches /orders/2023/inv-001
// 'year' will be "2023", 'invoiceId' will be "inv-001"
// app.GET("/orders/:year/:invoiceId", GetOrderHandler)
```

### 2.2. Reading Path Parameters

Inside your handler, you can access the values of named parameters using `c.Param(name string) string`.

```go
func GetUserHandler(c *xylium.Context) error {
	userID := c.Param("id") // "id" is the name from ":id"
	// ... fetch user by userID ...
	return c.String(xylium.StatusOK, "Fetching user with ID: %s", userID)
}

func GetOrderHandler(c *xylium.Context) error {
	year := c.Param("year")
	invoiceID := c.Param("invoiceId")
	return c.JSON(xylium.StatusOK, xylium.M{
		"message":    "Fetching order details",
		"year":       year,
		"invoice_id": invoiceID,
	})
}
```
Xylium also provides helpers like `c.ParamInt(name string) (int, error)` and `c.ParamIntDefault(name string, def int) int` for convenient type conversion. See `RequestHandling.md` for more details.

## 3. Catch-All Routes

Catch-all parameters capture all path segments from their position to the end of the URL. They are defined by prefixing a path segment with an asterisk (`*`). A catch-all parameter must be the last segment in a route pattern.

Example: `/static/*filepath`

```go
// app := xylium.New() // Assuming app is initialized

// Matches /serve/css/style.css -> resourcepath = "css/style.css"
// Matches /serve/images/logo.png -> resourcepath = "images/logo.png"
// Matches /serve/some/deep/path/file.js -> resourcepath = "some/deep/path/file.js"
// app.GET("/serve/*resourcepath", func(c *xylium.Context) error {
// 	resourcePath := c.Param("resourcepath")
// 	// ... logic to serve the file at resourcePath ...
// 	return c.String(xylium.StatusOK, "Serving resource: %s", resourcePath)
// })
```
This is commonly used for serving static files or proxying requests.

## 4. Route Grouping

Route grouping allows you to organize routes under a common path prefix and apply shared middleware to all routes within that group.

### 4.1. Basic Grouping

Use `app.Group(prefix string, groupMiddleware ...xylium.Middleware)` to create a new group.

```go
// Placeholder handlers
// func ListUsersHandler(c *xylium.Context) error   { return c.String(xylium.StatusOK, "List Users") }
// func CreateUserHandler(c *xylium.Context) error  { return c.String(xylium.StatusCreated, "Create User") }
// func ListProductsHandler(c *xylium.Context) error { return c.String(xylium.StatusOK, "List Products") } // Renamed for clarity
// app := xylium.New() // Assuming app is initialized

// apiV1 := app.Group("/api/v1")
// { // Braces are optional but improve readability
// 	apiV1.GET("/users", ListUsersHandler)    // Path: /api/v1/users
// 	apiV1.POST("/users", CreateUserHandler)  // Path: /api/v1/users
// 	apiV1.GET("/products", ListProductsHandler) // Path: /api/v1/products
// }
```

### 4.2. Nested Groups

Groups can be nested to create more complex path structures.

```go
// Placeholder handlers
// func AdminDashboardHandler(c *xylium.Context) error { return c.String(xylium.StatusOK, "Admin Dashboard") }
// func AdminListUsersHandler(c *xylium.Context) error { return c.String(xylium.StatusOK, "Admin List Users") }
// func AdminGetUserHandler(c *xylium.Context) error   { userID := c.Param("id"); return c.String(xylium.StatusOK, "Admin Get User "+userID) }
// func AdminBanUserHandler(c *xylium.Context) error   { userID := c.Param("id"); return c.String(xylium.StatusOK, "Admin Ban User "+userID) }
// app := xylium.New() // Assuming app is initialized

// adminGroup := app.Group("/admin")
// {
// 	adminGroup.GET("/dashboard", AdminDashboardHandler) // /admin/dashboard

// 	// Nested group for user management within admin
// 	userManagementGroup := adminGroup.Group("/users")
// 	{
// 		userManagementGroup.GET("/", AdminListUsersHandler)         // /admin/users
// 		userManagementGroup.GET("/:id", AdminGetUserHandler)       // /admin/users/:id
// 		userManagementGroup.POST("/:id/ban", AdminBanUserHandler)  // /admin/users/:id/ban
// 	}
// }
```

### 4.3. Group Middleware

Middleware can be applied at the group level. This middleware will execute for all routes defined within that group and its subgroups.

```go
// ExampleAuthMiddleware is a placeholder for actual authentication middleware
func ExampleAuthMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		c.Logger().Info("Auth Middleware called for group")
		// ... authentication logic ...
		// if !authenticated { return c.Status(xylium.StatusUnauthorized).String("Unauthorized") }
		return next(c)
	}
}
// func AdminSettingsHandler(c *xylium.Context) error { return c.String(xylium.StatusOK, "Admin Settings") }
// app := xylium.New() // Assuming app is initialized

// Apply AuthMiddleware to the /admin group and all its routes
// adminSecureGroup := app.Group("/admin", ExampleAuthMiddleware)
// {
// 	adminSecureGroup.GET("/settings", AdminSettingsHandler) // AuthMiddleware runs first
// 	// More admin routes...
// }
```
Route-specific middleware can also be added to routes within a group, and they will run after the group's middleware.

## 5. Serving Static Files

### 5.1. Serving a Directory (`app.ServeFiles()`)

Xylium provides `app.ServeFiles(urlPathPrefix string, fileSystemRoot string)` to serve static files from a directory.

```go
// app := xylium.New() // Assuming app is initialized

// Serve files from the "./public_assets" directory under the "/static" URL prefix.
// e.g., request to "/static/css/main.css" will serve "./public_assets/css/main.css"
// app.ServeFiles("/static", "./public_assets")

// To serve from the root URL path:
// app.ServeFiles("/", "./public_html") // Caution: Ensure no route conflicts with other handlers.
```
`ServeFiles` uses `fasthttp.FS` internally, which handles:
*   Serving `index.html` by default if a directory is requested (configurable via `fasthttp.FS.IndexNames`).
*   Setting appropriate `Content-Type` headers based on file extension.
*   Byte range requests (`Accept-Ranges` header).
*   Compression (if `fasthttp.FS.Compress` is enabled, which it is by default in Xylium's `ServeFiles` usage).
*   A custom `PathNotFound` handler that returns a JSON `xylium.StatusNotFound` error if a file is not found within the `fileSystemRoot`. This ensures API-like error responses even for static assets.

Refer to `router.go` (`Router.ServeFiles` implementation) for details on the custom `PathNotFound` handler.

### 5.2. Serving a Single Static File (`c.File()`)

To serve a single specific file, like a `favicon.ico` or `robots.txt`, you can define a regular route and use `c.File(filepathToServe string)` from `ResponseHandling.md`.

```go
// app := xylium.New() // Assuming app is initialized

// Serve favicon.ico from the root
// app.GET("/favicon.ico", func(c *xylium.Context) error {
// 	return c.File("./static_files/favicon.ico") // Path to your favicon file
// })

// app.GET("/robots.txt", func(c *xylium.Context) error {
// 	return c.File("./static_files/robots.txt")
// })
```
`c.File()` also uses `fasthttp.ServeFile` for efficient serving and proper header management.

## 6. Custom Not Found (404) Handler (`Router.NotFoundHandler`)

When no route matches the requested path, Xylium invokes the `Router.NotFoundHandler`. You can replace the default 404 handler to provide custom responses. The default handler returns a `*xylium.HTTPError` with status `xylium.StatusNotFound`.

```go
// app := xylium.New() // Assuming app is initialized

// app.NotFoundHandler = func(c *xylium.Context) error {
// 	c.Logger().Warnf("Custom 404: Path '%s' not found by client '%s'.", c.Path(), c.RealIP())
// 	// You can render an HTML 404 page or return a custom JSON response
// 	return c.Status(xylium.StatusNotFound).JSON(xylium.M{ // Explicitly set status, though NewHTTPError would also work
// 		"error_code": "RESOURCE_NOT_FOUND",
// 		"message":    "The page or resource you are looking for does not exist.",
// 		"path":       c.Path(),
// 	})
// }
```
This handler should be set on the `app` instance *before* starting the server. Your custom handler should typically return an error (often a `*xylium.HTTPError` created with `xylium.NewHTTPError`) or send a complete response itself. If it returns an error, that error will be processed by the `GlobalErrorHandler`.

## 7. Custom Method Not Allowed (405) Handler (`Router.MethodNotAllowedHandler`)

If a route path exists but not for the HTTP method used in the request, Xylium invokes `Router.MethodNotAllowedHandler`. The `Allow` header, listing permitted methods for the path, is automatically set by the router before calling this handler. The default handler returns a `*xylium.HTTPError` with status `xylium.StatusMethodNotAllowed`.

```go
// import "strings" // For strings.Split if parsing Allow header
// app := xylium.New() // Assuming app is initialized

// app.MethodNotAllowedHandler = func(c *xylium.Context) error {
// 	allowedMethodsHeader := c.Header("Allow") // Router sets this based on tree.Find results
// 	c.Logger().Warnf("Custom 405: Method '%s' not allowed for path '%s'. Allowed: [%s]. Client: '%s'",
// 		c.Method(), c.Path(), allowedMethodsHeader, c.RealIP())

// 	// Parse allowedMethodsHeader for JSON response if needed
// 	var allowedMethodsList []string
// 	if allowedMethodsHeader != "" {
// 		allowedMethodsList = strings.Split(allowedMethodsHeader, ", ")
// 	}


// 	return c.Status(xylium.StatusMethodNotAllowed).JSON(xylium.M{
// 		"error_code":     "METHOD_NOT_SUPPORTED",
// 		"message":        "The request method is not supported for this resource.",
// 		"requested_method": c.Method(),
// 		"allowed_methods":  allowedMethodsList,
// 	})
// }
```
Like `NotFoundHandler`, this should be set on the `app` instance. If your custom handler returns an error, it will be processed by `GlobalErrorHandler`.

## 8. Route Matching Order

Xylium's radix tree router matches routes with the following priority:
1.  **Static Routes**: Exact path matches (e.g., `/users/profile`) have the highest priority.
2.  **Named Parameter Routes**: Routes with path parameters (e.g., `/users/:id`) are matched next if no static route fits.
3.  **Catch-All Routes**: Routes with catch-all parameters (e.g., `/files/*filepath`) have the lowest priority and match if no static or named parameter route fits.

Within the same priority level (e.g., multiple static routes at the same tree depth), the router's behavior is deterministic due to the sorted nature of child nodes in the tree, but relying on specific ordering of equally specific routes is generally discouraged. Design your routes to be unambiguous.

If a path is matched but the HTTP method is not defined for that path, the `MethodNotAllowedHandler` is invoked. If no path matches at all, the `NotFoundHandler` is invoked.

## 9. Printing Registered Routes

For debugging purposes, especially in `DebugMode`, Xylium can print all registered routes to the logger when the server starts. This functionality is built into the `Tree.PrintRoutes(logger Logger)` method, which is called by the router's server startup methods (e.g., `ListenAndServeGracefully`, `Start`) when in `DebugMode`.

Example log output in `DebugMode`:
```
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
