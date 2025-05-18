# Xylium Cookbook: Practical Recipes and Examples

Welcome to the Xylium Cookbook! This document provides practical recipes and examples to help you accomplish various tasks using the Xylium web framework. Whether you're just starting or looking for specific solutions, this guide aims to provide clear, actionable examples.

## Table of Contents

*   **1. Server Basics**
    *   [1.1. Creating and Running a Basic Server](#recipe-1-1-basic-server)
    *   [1.2. Configuring Server Port](#recipe-1-2-server-port)
    *   [1.3. Understanding and Setting Operating Modes](#recipe-1-3-operating-modes)
    *   [1.4. Enabling HTTPS (TLS)](#recipe-1-4-https)
    *   [1.5. Graceful Shutdown](#recipe-1-5-graceful-shutdown)
*   **2. Routing**
    *   [2.1. Basic GET, POST, etc. Routes](#recipe-2-1-basic-routes)
    *   [2.2. Routes with Path Parameters](#recipe-2-2-path-parameters)
    *   [2.3. Catch-All Routes](#recipe-2-3-catch-all-routes)
    *   [2.4. Grouping Routes](#recipe-2-4-route-groups)
    *   [2.5. Serving Static Files](#recipe-2-5-static-files)
    *   [2.6. Serving a Single Static File (e.g., favicon)](#recipe-2-6-single-static-file)
    *   [2.7. Custom Not Found (404) Handler](#recipe-2-7-custom-404)
    *   [2.8. Custom Method Not Allowed (405) Handler](#recipe-2-8-custom-405)
*   **3. Request Handling**
    *   [3.1. Reading Path Parameters](#recipe-3-1-reading-path-params)
    *   [3.2. Reading Query Parameters](#recipe-3-2-reading-query-params)
    *   [3.3. Reading Form Data (URL-encoded or Multipart)](#recipe-3-3-reading-form-data)
    *   [3.4. Reading JSON Request Body](#recipe-3-4-reading-json-body)
    *   [3.5. Reading XML Request Body](#recipe-3-5-reading-xml-body)
    *   [3.6. Binding Request Data to Structs (JSON, Form, Query)](#recipe-3-6-binding-to-structs)
    *   [3.7. Validating Bound Structs](#recipe-3-7-validating-structs)
    *   [3.8. Handling File Uploads (Single and Multiple)](#recipe-3-8-file-uploads)
    *   [3.9. Reading Request Headers](#recipe-3-9-reading-headers)
    *   [3.10. Working with Cookies (Reading and Setting)](#recipe-3-10-cookies)
    *   [3.11. Accessing Raw Request Body](#recipe-3-11-raw-body)
    *   [3.12. Getting Client IP Address](#recipe-3-12-client-ip)
*   **4. Response Handling**
    *   [4.1. Sending String Responses](#recipe-4-1-string-response)
    *   [4.2. Sending JSON Responses](#recipe-4-2-json-response)
    *   [4.3. Sending XML Responses](#recipe-4-3-xml-response)
    *   [4.4. Sending HTML Responses (Using a Renderer)](#recipe-4-4-html-response)
    *   [4.5. Serving Files as Responses](#recipe-4-5-file-response)
    *   [4.6. Forcing File Download (Attachment)](#recipe-4-6-file-download)
    *   [4.7. Redirecting Requests](#recipe-4-7-redirects)
    *   [4.8. Sending `204 No Content` Responses](#recipe-4-8-no-content)
    *   [4.9. Setting Response Status Code and Headers](#recipe-4-9-status-headers)
*   **5. Middleware**
    *   [5.1. Creating Custom Middleware](#recipe-5-1-custom-middleware)
    *   [5.2. Using Global Middleware](#recipe-5-2-global-middleware)
    *   [5.3. Using Route-Specific Middleware](#recipe-5-3-route-middleware)
    *   [5.4. Using Group-Specific Middleware](#recipe-5-4-group-middleware)
    *   [5.5. Built-in: RequestID Middleware](#recipe-5-5-requestid-middleware)
    *   [5.6. Built-in: Logger Middleware (Automatic)](#recipe-5-6-logger-middleware)
    *   [5.7. Built-in: Gzip Compression Middleware](#recipe-5-7-gzip-middleware)
    *   [5.8. Built-in: CORS Middleware](#recipe-5-8-cors-middleware)
    *   [5.9. Built-in: CSRF Protection Middleware](#recipe-5-9-csrf-middleware)
    *   [5.10. Built-in: BasicAuth Middleware](#recipe-5-10-basicauth-middleware)
    *   [5.11. Built-in: Rate Limiter Middleware](#recipe-5-11-ratelimiter-middleware)
    *   [5.12. Built-in: Timeout Middleware](#recipe-5-12-timeout-middleware)
    *   [5.13. Passing Data Between Middleware and Handlers](#recipe-5-13-middleware-data-pass)
*   **6. Error Handling**
    *   [6.1. Returning Errors from Handlers](#recipe-6-1-returning-errors)
    *   [6.2. Using `xylium.HTTPError` for Custom Error Responses](#recipe-6-2-httperror)
    *   [6.3. Custom Global Error Handler](#recipe-6-3-custom-global-errorhandler)
    *   [6.4. Custom Panic Handler](#recipe-6-4-custom-panic-handler)
    *   [6.5. Handling Validation Errors from `BindAndValidate`](#recipe-6-5-validation-error-details)
*   **7. Logging**
    *   [7.1. Application-Level Logging (`app.Logger()`)](#recipe-7-1-app-logger)
    *   [7.2. Request-Scoped Logging (`c.Logger()`)](#recipe-7-2-request-logger)
    *   [7.3. Structured Logging with Fields (`WithFields`)](#recipe-7-3-structured-logging)
    *   [7.4. Configuring the Default Logger (Level, Format, Color, Caller)](#recipe-7-4-config-default-logger)
    *   [7.5. Using a Custom Logger Implementation](#recipe-7-5-custom-logger-impl)
*   **8. Go Context Integration**
    *   [8.1. Accessing the Go Context (`c.GoContext()`)](#recipe-8-1-accessing-go-context)
    *   [8.2. Propagating Go Context to Downstream Services](#recipe-8-2-propagating-go-context)
    *   [8.3. Using `context.WithTimeout` or `context.WithCancel` in Handlers](#recipe-8-3-go-context-timeout-cancel)
    *   [8.4. Passing Request-Scoped Values via Go Context (Advanced)](#recipe-8-4-go-context-values-advanced)
*   **9. Advanced Configuration**
    *   [9.1. Custom Validator Instance](#recipe-9-1-custom-validator)
    *   [9.2. Advanced Fasthttp Server Settings (`xylium.ServerConfig`)](#recipe-9-2-advanced-serverconfig)

---

## 1. Server Basics

### <a name="recipe-1-1-basic-server"></a>1.1. Creating and Running a Basic Server

This is the most fundamental Xylium application: a server that listens on a port and responds to a GET request.

```go
package main

import (
	"net/http" // For http.StatusOK

	// Adjust the import path according to your project structure
	"github.com/arwahdevops/xylium-core/src/xylium" 
)

func main() {
	// Create a new Xylium application instance.
	// By default, it runs in DebugMode with an auto-configured logger.
	app := xylium.New()

	// Define a GET route for the root path "/".
	app.GET("/", func(c *xylium.Context) error {
		// Use the request-scoped logger for debugging or info.
		c.Logger().Debugf("Handling request for path: %s", c.Path())
		// Send a simple string response with a 200 OK status.
		return c.String(http.StatusOK, "Hello, Xylium World!")
	})

	// Define the address and port to listen on.
	listenAddr := ":8080"

	// Use the application-level logger for startup messages.
	// app.CurrentMode() returns the effective operating mode (e.g., "debug").
	app.Logger().Infof("Xylium server starting on http://localhost%s (Mode: %s)", listenAddr, app.CurrentMode())

	// Start the server. app.Start() provides graceful shutdown.
	if err := app.Start(listenAddr); err != nil {
		// Log fatal errors during server startup.
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
**Explanation:**
*   `xylium.New()`: Initializes your Xylium application.
*   `app.GET("/", ...)`: Defines a handler for HTTP GET requests to the root path.
*   `c.Logger()`: Provides a logger instance with request-specific context (like `request_id` if middleware is used).
*   `c.String(...)`: Sends a plain text response.
*   `app.Logger()`: Provides an application-level logger.
*   `app.Start(":8080")`: Starts the HTTP server on port 8080 with graceful shutdown enabled.

### <a name="recipe-1-2-server-port"></a>1.2. Configuring Server Port

You can easily change the port your Xylium server listens on.

```go
package main

import (
	"net/http"
	"os" // To potentially get port from environment variable

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	app.GET("/", func(c *xylium.Context) error {
		return c.String(http.StatusOK, "Listening on a custom port!")
	})

	// Get port from environment variable or use a default.
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // Default port if not set
	}
	listenAddr := ":" + port

	app.Logger().Infof("Server starting on http://localhost%s", listenAddr)
	if err := app.Start(listenAddr); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
**Explanation:**
*   The `listenAddr` variable is constructed using the desired port.
*   This example also shows how to fetch the port from an environment variable (`PORT`), a common practice for deployments.

### <a name="recipe-1-3-operating-modes"></a>1.3. Understanding and Setting Operating Modes

Xylium supports `debug`, `test`, and `release` modes, which affect logging verbosity and error details. `debug` is the default.

**Setting Mode via Environment Variable:**
(This is overridden by programmatic setting)

```bash
# For release mode
XYLIUM_MODE=release go run main.go

# For test mode
XYLIUM_MODE=test go run main.go
```

**Setting Mode Programmatically (Highest Priority):**
Call `xylium.SetMode()` *before* `xylium.New()`.

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Set the mode before initializing the app.
	xylium.SetMode(xylium.ReleaseMode) // Or xylium.TestMode, xylium.DebugMode

	app := xylium.New()
	// The app's logger and default behaviors will now reflect ReleaseMode.

	app.GET("/", func(c *xylium.Context) error {
		// c.RouterMode() gives the mode of the router instance.
		// c.Logger() behavior is influenced by this mode.
		c.Logger().Infof("Handler invoked. Current router mode: %s", c.RouterMode())
		return c.String(http.StatusOK, "Application running in %s mode.", app.CurrentMode())
	})

	app.Logger().Infof("Server starting (Effective Mode: %s)", app.CurrentMode())
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Error starting server: %v", err)
	}
}
```
**Key Points:**
*   **Debug Mode:** Verbose logging (debug level, caller info, color if TTY), detailed error messages to client.
*   **Release Mode:** Less verbose logging (info level), generic error messages to client.
*   **Test Mode:** Similar to debug but often without color, suitable for automated tests.
*   The logger (`app.Logger()` and `c.Logger()`) is automatically configured based on the effective mode.

---
