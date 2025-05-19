---

# Error Handling in Xylium

Xylium provides a flexible and centralized error handling mechanism, allowing you to manage application errors consistently and provide appropriate responses to clients. This guide covers how to return errors, use `xylium.HTTPError` for precise HTTP responses, and customize global error and panic handlers.

## Table of Contents

*   [1. Basics of Error Handling](#1-basics-of-error-handling)
    *   [Returning Errors from Handlers](#returning-errors-from-handlers)
*   [2. `xylium.HTTPError`: Precise HTTP Error Responses](#2-xyliumhttperror-precise-http-error-responses)
    *   [2.1. Definition](#21-definition)
    *   [2.2. Creating an `HTTPError`](#22-creating-an-httperror)
        *   [Simple String Message](#simple-string-message)
        *   [Default Message from Status Code](#default-message-from-status-code)
        *   [Structured Message (using `xylium.M`)](#structured-message-using-xyliumm)
        *   [Including an Internal Error (`WithInternal`)](#including-an-internal-error-withinternal)
        *   [Adopting Other Errors](#adopting-other-errors)
    *   [2.3. Modifying an `HTTPError`'s Message (`WithMessage`)](#23-modifying-an-httperrors-message-withmessage)
    *   [2.4. Checking for `HTTPError` (`IsHTTPError`)](#24-checking-for-httperror-ishttperror)
*   [3. Global Error Handler (`Router.GlobalErrorHandler`)](#3-global-error-handler-routerglobalerrorhandler)
    *   [3.1. Default Behavior](#31-default-behavior)
    *   [3.2. Customizing the Global Error Handler](#32-customizing-the-global-error-handler)
*   [4. Panic Handling (`Router.PanicHandler`)](#4-panic-handling-routerpanichandler)
    *   [4.1. Default Behavior](#41-default-behavior)
    *   [4.2. Customizing the Panic Handler](#42-customizing-the-panic-handler)
*   [5. Errors from `c.BindAndValidate()`](#5-errors-from-cbindandvalidate)
*   [6. Error Handling Flow Summary](#6-error-handling-flow-summary)

---

## 1. Basics of Error Handling

### Returning Errors from Handlers

In Xylium, route handlers and middleware have the signature `func(c *xylium.Context) error`.

*   If a handler returns `nil`, Xylium assumes the request was handled successfully (and a response may have already been sent).
*   If a handler returns a non-nil `error`, this error is passed to the framework's **Global Error Handler** (`Router.GlobalErrorHandler`).

```go
import (
	"errors"
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path
)

func GetItemHandler(c *xylium.Context) error {
	itemID := c.Param("id")
	item, err := findItemByID(itemID) // Assume this service function returns (Item, error)
	
	if err != nil {
		if errors.Is(err, ErrItemNotFound) { // Assuming ErrItemNotFound is a defined error
			// Return a specific HTTPError for "Not Found"
			return xylium.NewHTTPError(http.StatusNotFound, "Item with ID "+itemID+" not found.")
		}
		// For other unexpected errors from the service
		c.Logger().Errorf("Error fetching item %s: %v", itemID, err)
		// Return the original error; GlobalErrorHandler will likely treat it as a 500.
		// Or, wrap it in an HTTPError with more context:
		// return xylium.NewHTTPError(http.StatusInternalServerError, "Failed to retrieve item.").WithInternal(err)
		return err 
	}

	return c.JSON(http.StatusOK, item)
}
```

## 2. `xylium.HTTPError`: Precise HTTP Error Responses

For full control over HTTP error responses, Xylium provides the `xylium.HTTPError` struct. This is the standard way to communicate errors that should result in a specific HTTP status code and message to the client.

### 2.1. Definition

(Simplified from `xylium/errors.go`)
```go
type HTTPError struct {
    Code     int         `json:"-"`    // HTTP status code (e.g., 400, 404, 500)
    Message  interface{} `json:"error"`  // Client-facing message (string, xylium.M, or other struct)
    Internal error       `json:"-"`    // Internal error for logging, not exposed to client (except in DebugMode)
}
```
*   `Code`: The HTTP status code.
*   `Message`: The payload for the error response body. Can be a string or a struct (like `xylium.M`) for JSON responses.
*   `Internal`: An optional underlying error. This is useful for logging and debugging but is not exposed to the client by default (the `GlobalErrorHandler` might expose it in `DebugMode`).

### 2.2. Creating an `HTTPError`

Use the `xylium.NewHTTPError(code int, message ...interface{}) *HTTPError` function.

#### Simple String Message
```go
if productNotFound {
    // Returns a 404 with a JSON body like: {"error": "Product not found."}
    return xylium.NewHTTPError(http.StatusNotFound, "Product not found.")
}
```

#### Default Message from Status Code
If you omit the message, Xylium uses the standard HTTP status text.
```go
if resourceIsGone {
    // Returns a 410 with a JSON body like: {"error": "Gone"}
    return xylium.NewHTTPError(http.StatusGone) 
}
```

#### Structured Message (using `xylium.M`)
Provide more detailed error information to the client.
```go
if validationFailed {
    details := xylium.M{
        "field": "email",
        "issue": "Email address is already registered.",
    }
    // Returns a 409 with a JSON body like:
    // { "error": { "message": "User registration failed", "details": { "field": "email", ... } } }
    return xylium.NewHTTPError(http.StatusConflict, xylium.M{
        "message": "User registration failed",
        "details": details,
    })
}
// Alternatively, if the entire message IS the structured part:
// return xylium.NewHTTPError(http.StatusBadRequest, xylium.M{"code": "VALIDATION_ERROR", "field_errors": details})
```

#### Including an Internal Error (`WithInternal`)
Crucial for logging internal error details without exposing them to the client (unless in `DebugMode`).
```go
dbResult, dbErr := queryDatabaseForUser(userID)
if dbErr != nil {
    // Generic message for the client
    clientMessage := "An error occurred while accessing user data."
    // The original dbErr is wrapped as an internal error for logging
    return xylium.NewHTTPError(http.StatusInternalServerError, clientMessage).WithInternal(dbErr)
}
```
*   **In `ReleaseMode`**, the client sees:
    ```json
    // Status: 500 Internal Server Error
    { "error": "An error occurred while accessing user data." }
    ```
    The server logs will contain details from `dbErr`.
*   **In `DebugMode`**, Xylium's default `GlobalErrorHandler` adds `_debug_info`:
    ```json
    // Status: 500 Internal Server Error
    {
        "error": "An error occurred while accessing user data.",
        "_debug_info": {
            "internal_error_details": "pq: connection refused" // Example from dbErr.Error()
        }
    }
    ```

#### Adopting Other Errors
`NewHTTPError` can intelligently wrap existing errors:
```go
// Scenario 1: Wrapping a generic Go error
err := someServiceCall()
if err != nil {
    // Make this a 400 Bad Request. err.Error() becomes the Message, err becomes Internal.
    return xylium.NewHTTPError(http.StatusBadRequest, err) 
}

// Scenario 2: Wrapping an existing HTTPError to change its code
originalHttpErr := xylium.NewHTTPError(http.StatusForbidden, "Access denied.")
if someCondition {
    // Change to 503, keeping original message and internal error (if any)
    return xylium.NewHTTPError(http.StatusServiceUnavailable, originalHttpErr)
}
```

### 2.3. Modifying an `HTTPError`'s Message (`WithMessage`)
You can change the `Message` of an existing `HTTPError`:
```go
err := xylium.NewHTTPError(http.StatusPaymentRequired, "Premium feature access required.")
// ... some logic ...
if user.IsTrialExpired() {
    err = err.WithMessage("Your trial has expired. Please upgrade to access this feature.")
}
return err
```

### 2.4. Checking for `HTTPError` (`IsHTTPError`)
Use `xylium.IsHTTPError(err error, code ...int) bool` to check if an error is an `*xylium.HTTPError` and optionally if it matches a specific status code.
```go
err := someFunctionThatMightReturnHttpError()
if xylium.IsHTTPError(err, http.StatusNotFound) {
    // Handle "Not Found" specifically
} else if he, ok := err.(*xylium.HTTPError); ok {
    // It's some other HTTPError
    log.Printf("HTTP Error with code %d occurred.", he.Code)
}
```

## 3. Global Error Handler (`Router.GlobalErrorHandler`)

All non-nil errors returned from handlers or middleware (and errors returned by the `PanicHandler`) are ultimately processed by `Router.GlobalErrorHandler`. Its signature is `func(c *xylium.Context) error`.

### 3.1. Default Behavior
Xylium provides a `defaultGlobalErrorHandler` that:
1.  Retrieves the original error from `c.Get(xylium.ContextKeyErrorCause)`. (Assuming `ContextKeyErrorCause` is a defined constant like `ContextKeyRequestID`).
2.  Uses `c.Logger()` for contextual logging.
3.  Differentiates between `*xylium.HTTPError` and generic Go errors.
4.  Sends a JSON response to the client:
    *   For `*xylium.HTTPError`: Uses its `Code` and `Message`. In `DebugMode`, `Internal.Error()` is added to `_debug_info` if `Internal` is not nil.
    *   For generic Go errors: Sends HTTP 500. In `DebugMode`, `originalErr.Error()` is added to `_debug_info`.

### 3.2. Customizing the Global Error Handler
You can replace the default handler:
```go
// In main.go or your app setup
app := xylium.New() // or NewWithConfig

app.GlobalErrorHandler = func(c *xylium.Context) error {
    errVal, _ := c.Get(xylium.ContextKeyErrorCause) // Use defined constant
    originalErr, _ := errVal.(error)
    logger := c.Logger() // Contextual logger

    var httpCode int = http.StatusInternalServerError
    var clientResponse interface{}

    if he, ok := originalErr.(*xylium.HTTPError); ok {
        httpCode = he.Code
        clientResponse = he.Message // This could be string or xylium.M
        
        logFields := xylium.M{"status": httpCode}
        if he.Internal != nil {
            logFields["internal_error"] = he.Internal.Error()
        }
        logger.WithFields(logFields).Errorf("CustomErrorHandler (HTTPError): %v", he.Message)
    } else if originalErr != nil { // Generic error
        clientResponse = xylium.M{"error_code": "UNEXPECTED_ERROR", "message": "An unexpected error occurred."}
        if c.RouterMode() == xylium.DebugMode {
             clientResponse.(xylium.M)["debug_details"] = originalErr.Error()
        }
        logger.WithFields(xylium.M{"status": httpCode}).Errorf("CustomErrorHandler (Generic Error): %v", originalErr)
    } else {
        // Should ideally not happen if errors are propagated correctly
        clientResponse = xylium.M{"error": "Unknown error"}
        logger.Warn("CustomErrorHandler: Called without a valid error cause.")
    }

    // Ensure response is not already sent (e.g., by a middleware that short-circuited)
    if !c.ResponseCommitted() {
        return c.JSON(httpCode, clientResponse)
    }
    logger.Debug("CustomErrorHandler: Response already committed, cannot send error response.")
    return nil // Response already sent
}
```
**Important**: Your custom error handler must always send a response or return an error if it fails to send one, to avoid hanging requests.

## 4. Panic Handling (`Router.PanicHandler`)

Xylium automatically recovers from panics that occur in handlers or middleware. After recovery, `Router.PanicHandler` is called. Its signature is `func(c *xylium.Context) error`.

### 4.1. Default Behavior
Xylium's `defaultPanicHandler`:
1.  Logs the panic. The stack trace is logged by the core Router logic before `PanicHandler` is invoked.
2.  Sets `panic_recovery_info` (use a defined constant like `xylium.ContextKeyPanicInfo`) in the context.
3.  Returns an `xylium.NewHTTPError` with status 500 and a generic message. The panic value is set as the `Internal` error.
4.  This `HTTPError` is then processed by the `GlobalErrorHandler`.

### 4.2. Customizing the Panic Handler
```go
app.PanicHandler = func(c *xylium.Context) error {
    panicInfo, _ := c.Get(xylium.ContextKeyPanicInfo) // Use defined constant
    
    // Example: Send notification to an external monitoring system
    // alertService.NotifyPanic(fmt.Sprintf("Panic: %v", panicInfo), c.Path(), c.RealIP())

    c.Logger().WithFields(xylium.M{"panic_value": panicInfo}).Criticalf("PANIC RECOVERED (Custom Handler)!")
    
    // Return an HTTPError that will be processed by GlobalErrorHandler
    errorMessage := "We're experiencing technical difficulties. Our team has been notified."
    if c.RouterMode() == xylium.DebugMode {
        errorMessage = fmt.Sprintf("Panic: %v. See server logs for stack trace.", panicInfo)
    }
    return xylium.NewHTTPError(http.StatusInternalServerError, errorMessage).WithInternal(fmt.Errorf("panic: %v", panicInfo))
}
```

## 5. Errors from `c.BindAndValidate()`

The `c.BindAndValidate(out interface{}) error` method returns a `*xylium.HTTPError` if binding or validation fails:
*   **Binding Failure**: Typically results in a `400 Bad Request` (e.g., "Invalid JSON data provided.").
*   **Validation Failure**: Results in a `400 Bad Request`. The `Message` field of the `HTTPError` will be a `xylium.M` containing `message: "Validation failed."` and `details: map[string]string` mapping field names to specific validation error messages.

**Example Response from `BindAndValidate` Validation Failure:**
```json
// Status: 400 Bad Request
{
    "error": {
        "message": "Validation failed.",
        "details": {
            "Username": "validation failed on tag 'required'",
            "Email": "validation failed on tag 'email'"
        }
    }
}
```
You can directly return the error from `BindAndValidate` to be processed by the `GlobalErrorHandler`:
```go
func CreateItemHandler(c *xylium.Context) error {
    var input CreateItemInput
    if err := c.BindAndValidate(&input); err != nil {
        // No need to log here if GlobalErrorHandler does it.
        // err is already *xylium.HTTPError.
        return err 
    }
    // ... process input ...
    return c.String(http.StatusCreated, "Item created")
}
```

## 6. Error Handling Flow Summary

1.  **Handler/Middleware returns an `error`**:
    *   If `nil`: Processing continues or request ends.
    *   If non-nil: Error is passed to `Router.Handler`'s error handling logic.
2.  **Panic occurs in Handler/Middleware**:
    *   `Router.Handler` recovers the panic.
    *   `Router.PanicHandler` is called.
    *   `PanicHandler` returns an `error` (typically an `HTTPError`). This error then flows as if it were returned by a normal handler.
3.  **`Router.Handler`'s Error Processing**:
    *   The error (from step 1 or 2) is set into `c.store` (e.g., `c.Set(xylium.ContextKeyErrorCause, err)`).
    *   `Router.GlobalErrorHandler` is called.
4.  **`Router.GlobalErrorHandler`**:
    *   Retrieves the error cause from context.
    *   Logs the error.
    *   Formats and sends an HTTP response to the client (usually JSON).

By understanding `HTTPError` and the error/panic handling flow, you can build robust Xylium applications that provide meaningful feedback to both developers (via logs) and users (via API responses). Remember to use defined constants (e.g., `xylium.ContextKeyErrorCause`, `xylium.ContextKeyPanicInfo`) for context keys for better maintainability.

---
