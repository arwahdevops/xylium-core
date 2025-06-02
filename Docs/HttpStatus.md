# Xylium HTTP Status Codes

Xylium provides its own set of constants for HTTP status codes, mirroring the standard codes defined in relevant RFCs and commonly used in web development. These constants are defined in the `xylium` package (specifically in `httpstatus.go`) and should be preferred over importing `net/http` solely for status code constants within Xylium applications.

This promotes consistency within the Xylium ecosystem and avoids unnecessary imports of the standard `net/http` package if `fasthttp` is the primary HTTP engine.

## Table of Contents

*   [1. Why Use `xylium.StatusXXX`?](#1-why-use-xyliumstatusxxx)
*   [2. Available Status Code Constants](#2-available-status-code-constants)
    *   [2.1. Informational 1xx](#21-informational-1xx)
    *   [2.2. Successful 2xx](#22-successful-2xx)
    *   [2.3. Redirection 3xx](#23-redirection-3xx)
    *   [2.4. Client Error 4xx](#24-client-error-4xx)
    *   [2.5. Server Error 5xx](#25-server-error-5xx)
*   [3. `xylium.StatusText(code int) string`](#3-xyliumstatustextcode-int-string)
*   [4. Example Usage](#4-example-usage)

---

## 1. Why Use `xylium.StatusXXX`?

*   **Consistency:** Ensures that all parts of your Xylium application and Xylium itself use the same source for HTTP status codes.
*   **Reduced Dependencies:** Helps in minimizing direct dependencies on the standard `net/http` package if your application is primarily built around Xylium and `fasthttp`.
*   **Clarity:** Makes it clear that these are the status codes as recognized and used within the Xylium framework.

All Xylium's built-in handlers, middleware, and error reporting mechanisms use these `xylium.StatusXXX` constants.

## 2. Available Status Code Constants

The constants are named `xylium.Status<Description>`, for example, `xylium.StatusOK`, `xylium.StatusNotFound`.

### 2.1. Informational 1xx
*   `xylium.StatusContinue` (100)
*   `xylium.StatusSwitchingProtocols` (101)
*   `xylium.StatusProcessing` (102) - WebDAV
*   `xylium.StatusEarlyHints` (103)

### 2.2. Successful 2xx
*   `xylium.StatusOK` (200)
*   `xylium.StatusCreated` (201)
*   `xylium.StatusAccepted` (202)
*   `xylium.StatusNonAuthoritativeInfo` (203)
*   `xylium.StatusNoContent` (204)
*   `xylium.StatusResetContent` (205)
*   `xylium.StatusPartialContent` (206)
*   `xylium.StatusMultiStatus` (207) - WebDAV
*   `xylium.StatusAlreadyReported` (208) - WebDAV
*   `xylium.StatusIMUsed` (226) - Delta encoding

### 2.3. Redirection 3xx
*   `xylium.StatusMultipleChoices` (300)
*   `xylium.StatusMovedPermanently` (301)
*   `xylium.StatusFound` (302) - Formerly "Moved Temporarily"
*   `xylium.StatusSeeOther` (303)
*   `xylium.StatusNotModified` (304)
*   `xylium.StatusUseProxy` (305) - Deprecated
*   `xylium.StatusTemporaryRedirect` (307)
*   `xylium.StatusPermanentRedirect` (308)

### 2.4. Client Error 4xx
*   `xylium.StatusBadRequest` (400)
*   `xylium.StatusUnauthorized` (401)
*   `xylium.StatusPaymentRequired` (402)
*   `xylium.StatusForbidden` (403)
*   `xylium.StatusNotFound` (404)
*   `xylium.StatusMethodNotAllowed` (405)
*   `xylium.StatusNotAcceptable` (406)
*   `xylium.StatusProxyAuthRequired` (407)
*   `xylium.StatusRequestTimeout` (408)
*   `xylium.StatusConflict` (409)
*   `xylium.StatusGone` (410)
*   `xylium.StatusLengthRequired` (411)
*   `xylium.StatusPreconditionFailed` (412)
*   `xylium.StatusRequestEntityTooLarge` (413) - Formerly "Payload Too Large"
*   `xylium.StatusRequestURITooLong` (414) - Formerly "URI Too Long"
*   `xylium.StatusUnsupportedMediaType` (415)
*   `xylium.StatusRequestedRangeNotSatisfiable` (416)
*   `xylium.StatusExpectationFailed` (417)
*   `xylium.StatusTeapot` (418) - I'm a teapot
*   `xylium.StatusMisdirectedRequest` (421) - HTTP/2
*   `xylium.StatusUnprocessableEntity` (422) - WebDAV
*   `xylium.StatusLocked` (423) - WebDAV
*   `xylium.StatusFailedDependency` (424) - WebDAV
*   `xylium.StatusTooEarly` (425)
*   `xylium.StatusUpgradeRequired` (426)
*   `xylium.StatusPreconditionRequired` (428)
*   `xylium.StatusTooManyRequests` (429)
*   `xylium.StatusRequestHeaderFieldsTooLarge` (431)
*   `xylium.StatusUnavailableForLegalReasons` (451)

### 2.5. Server Error 5xx
*   `xylium.StatusInternalServerError` (500)
*   `xylium.StatusNotImplemented` (501)
*   `xylium.StatusBadGateway` (502)
*   `xylium.StatusServiceUnavailable` (503)
*   `xylium.StatusGatewayTimeout` (504)
*   `xylium.StatusHTTPVersionNotSupported` (505)
*   `xylium.StatusVariantAlsoNegotiates` (506)
*   `xylium.StatusInsufficientStorage` (507) - WebDAV
*   `xylium.StatusLoopDetected` (508) - WebDAV
*   `xylium.StatusNotExtended` (510)
*   `xylium.StatusNetworkAuthenticationRequired` (511)

*For a complete list and their corresponding RFCs, refer to the IANA HTTP Status Code Registry.*

## 3. `xylium.StatusText(code int) string`

Xylium provides a helper function `xylium.StatusText(code int) string` that returns the standard English text description for a given HTTP status code (e.g., `xylium.StatusText(404)` returns `"Not Found"`).

If the code is unknown, it returns an empty string. This function is used internally by `xylium.NewHTTPError` to provide default messages when none are explicitly given.

```go
func main() {
	// Example:
	// messageFor403 := xylium.StatusText(xylium.StatusForbidden) // "Forbidden"
	// messageForUnknown := xylium.StatusText(600)              // "" (empty string)
}
```

## 4. Example Usage

When sending responses or creating `xylium.HTTPError` instances, use these constants:

```go
package main

import (
	"github.com/arwahdevops/xylium-core/src/xylium"
)

func GetItemHandler(c *xylium.Context) error {
	itemID := c.Param("id")
	// item, err := findItemByID(itemID) // Assume this service function

	// if err != nil { // Pseudo-code for item not found
	//	if errors.Is(err, ErrItemNotFound) {
	//		// Using xylium.StatusNotFound
	//		return xylium.NewHTTPError(xylium.StatusNotFound, "Item "+itemID+" not found.")
	//	}
	//	return xylium.NewHTTPError(xylium.StatusInternalServerError, "Failed to retrieve item.").WithInternal(err)
	// }

	// Using xylium.StatusOK
	// return c.JSON(xylium.StatusOK, item)
	return c.String(xylium.StatusOK, "Item %s found (placeholder)", itemID) // Example
}

func CreateUserHandler(c *xylium.Context) error {
	// ... user creation logic ...
	// if successful:
	// return c.JSON(xylium.StatusCreated, xylium.M{"message": "User created successfully"})

	// if validation fails (e.g., from BindAndValidate):
	// err := c.BindAndValidate(&userInput)
	// if err != nil {
	//   // BindAndValidate already returns an HTTPError, often with xylium.StatusBadRequest
	//   return err
	// }
	return c.JSON(xylium.StatusCreated, xylium.M{"message": "User created (placeholder)"}) // Example
}

func main() {
	app := xylium.New()
	app.GET("/items/:id", GetItemHandler)
	app.POST("/users", CreateUserHandler)
	// ... other routes ...
	app.Start(":8080")
}
```

By consistently using `xylium.StatusXXX` constants and `xylium.StatusText()`, you ensure your Xylium application remains aligned with the framework's conventions and reduces external dependencies.
