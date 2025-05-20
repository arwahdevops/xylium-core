# Xylium Type-Safe Request Parameters

Xylium enhances developer experience and code robustness by offering a way to automatically bind and validate URL path and query parameters directly into Go structs. This feature leverages Go generics to provide type safety and reduce boilerplate code typically associated with manual parameter extraction and conversion.

## Table of Contents

*   [1. Why Type-Safe Parameters?](#1-why-type-safe-parameters)
*   [2. Defining Parameter Structs](#2-defining-parameter-structs)
    *   [2.1. Path Parameters (`path` tag)](#21-path-parameters-path-tag)
    *   [2.2. Query Parameters (`query` tag)](#22-query-parameters-query-tag)
    *   [2.3. Default Values (`default` tag for query)](#23-default-values-default-tag-for-query)
    *   [2.4. Supported Field Types](#24-supported-field-types)
*   [3. Registering Handlers with Type-Safe Parameters](#3-registering-handlers-with-type-safe-parameters)
    *   [3.1. Using `xylium.RegisterParams`](#31-using-xyliumregisterparams)
    *   [3.2. Convenience Helpers (e.g., `xylium.RegisterGETParams`)](#32-convenience-helpers-eg-xyliumregistergetparams)
    *   [3.3. Enabling Validation](#33-enabling-validation)
*   [4. Writing Type-Safe Handlers](#4-writing-type-safe-handlers)
*   [5. How it Works Under the Hood](#5-how-it-works-under-the-hood)
*   [6. Error Handling](#6-error-handling)
*   [7. Examples](#7-examples)
    *   [7.1. Path Parameters Example](#71-path-parameters-example)
    *   [7.2. Query Parameters Example with Slices and Defaults](#72-query-parameters-example-with-slices-and-defaults)
    *   [7.3. Combined Path and Query Parameters](#73-combined-path-and-query-parameters)
*   [8. Benefits and Considerations](#8-benefits-and-considerations)

---

## 1. Why Type-Safe Parameters?

Traditionally, extracting path or query parameters involves:
1.  Getting the raw string value (e.g., `c.Param("id")`, `c.QueryParam("page")`).
2.  Manually converting it to the desired type (e.g., `strconv.Atoi`).
3.  Handling potential conversion errors.
4.  Validating the converted value.

This process can be verbose and error-prone. Xylium's type-safe parameter binding automates these steps:

*   **Reduced Boilerplate**: Less manual parsing and conversion code.
*   **Improved Readability**: Handler signatures clearly define expected parameters and their types.
*   **Type Safety**: Parameters are converted to their Go types before your handler logic runs.
*   **Automatic Validation**: Leverage Xylium's built-in validation engine (`go-playground/validator/v10`) on your parameter structs.
*   **Clearer Intent**: Struct tags explicitly declare the source (`path`, `query`) and name of parameters.

## 2. Defining Parameter Structs

You define a Go struct where each field represents a path or query parameter. Tags are used to map URL parameters to struct fields.

### 2.1. Path Parameters (`path` tag)

Fields tagged with `path:"paramName"` will be populated from the corresponding URL path parameter.

```go
type GetBookParams struct {
    BookID   uint   `path:"bookId" validate:"required,min=1"` // from /books/:bookId
    AuthorID string `path:"authorId" validate:"omitempty,uuid4"` // from /authors/:authorId/books/...
}
```
Path parameters are typically required for a route to match. The value extracted from the URL will be converted to the field's type.

### 2.2. Query Parameters (`query` tag)

Fields tagged with `query:"paramName"` will be populated from URL query parameters.

```go
type ListProductsQuery struct {
    SearchTerm string   `query:"q" validate:"omitempty,max=100"`
    Page       int      `query:"page" validate:"min=1"`
    Limit      int      `query:"limit" validate:"min=1,max=200"`
    Categories []string `query:"category" validate:"omitempty,dive,alphanum"` // For ?category=electronics&category=books
    PriceMin   *float64 `query:"price_min" validate:"omitempty,gte=0"`      // Pointer for optional numeric values
}
```
*   **Slices**: If a query parameter can appear multiple times (e.g., `?category=a&category=b`), define the corresponding struct field as a slice (e.g., `[]string`, `[]int`).
*   **Pointers**: Using a pointer type (e.g., `*int`, `*string`, `*bool`) for a query parameter field allows you to distinguish between a parameter that was not provided (field remains `nil`) and a parameter that was provided with a value that converts to the type's zero value (e.g., `?count=0`).

### 2.3. Default Values (`default` tag for query)

For query parameters, you can specify a default value using the `default:"value"` tag. If the query parameter is not present in the URL, this default string value will be used for binding.

```go
type SearchOptions struct {
    SortBy    string `query:"sort_by" default:"relevance" validate:"oneof=relevance date_asc date_desc"`
    Results   int    `query:"results" default:"20" validate:"min=5,max=50"`
    ShowFull  *bool  `query:"show_full" default:"false"` // Default for a pointer bool
}
```
The default value is always a string and will be converted to the field's type during binding.

### 2.4. Supported Field Types

The type-safe parameter binding supports common scalar types and their slices (for query parameters):
*   `string`
*   `int`, `int8`, `int16`, `int32`, `int64`
*   `uint`, `uint8`, `uint16`, `uint32`, `uint64`
*   `bool` (accepts "true", "false", "1", "0", "on", "off", "yes", "no")
*   `float32`, `float64`
*   `time.Time` (parses RFC3339, "YYYY-MM-DDTHH:MM:SS", and "YYYY-MM-DD")
*   Pointers to these scalar types (e.g., `*int`, `*string`)
*   Slices of these scalar types or pointers to them (primarily for query parameters, e.g., `[]string`, `[]*int`)

## 3. Registering Handlers with Type-Safe Parameters

Xylium provides generic helper functions to register routes with handlers that accept these typed parameter structs.

### 3.1. Using `xylium.RegisterParams`

The primary generic helper is `xylium.RegisterParams`.

```go
// P is your parameter struct type
// handler is func(c *xylium.Context, params P) error
xylium.RegisterParams[P any](
    router *xylium.Router,
    method string,
    path string,
    handler HandlerWithParamsFunc[P],
    enableValidation bool, // If true, validates the 'params' struct
    routeMiddleware ...xylium.Middleware,
)
```

**Example:**
```go
type UserProfileParams struct {
    UserID int `path:"userId" validate:"required"`
}

func GetUserProfile(c *xylium.Context, params UserProfileParams) error {
    // params.UserID is already an int and validated if enableValidation was true
    c.Logger().Infof("Fetching profile for user ID: %d", params.UserID)
    // ...
    return c.JSON(http.StatusOK, xylium.M{"user_id": params.UserID, "data": "profile_data"})
}

func main() {
    app := xylium.New()
    xylium.RegisterParams[UserProfileParams](app, http.MethodGet, "/users/:userId/profile", GetUserProfile, true)
    app.Start(":8080")
}
```

### 3.2. Convenience Helpers (e.g., `xylium.RegisterGETParams`)

For common HTTP methods, Xylium provides shorthand helpers:

*   `xylium.RegisterGETParams[P any](router, path, handler, enableValidation, routeMiddleware...)`
*   `xylium.RegisterPOSTParams[P any](router, path, handler, enableValidation, routeMiddleware...)`
    (Note: `POST` helpers are more for consistency if path/query params are primary; body binding uses `c.BindAndValidate`.)

**Example:**
```go
xylium.RegisterGETParams[UserProfileParams](app, "/users/:userId/profile", GetUserProfile, true)
```

### 3.3. Enabling Validation

The `enableValidation` boolean argument in `RegisterParams` (and its helpers) determines whether the populated parameter struct will be validated using Xylium's standard validator (`go-playground/validator/v10`). If set to `true`, validation rules defined via `validate` tags on the struct fields are enforced.

## 4. Writing Type-Safe Handlers

Your handler function signature changes to accept the Xylium context and your typed parameter struct:

```go
// P must be a struct type, not a pointer to a struct.
type HandlerWithParamsFunc[P any] func(c *xylium.Context, params P) error
```

Inside the handler, you can directly access the fields of the `params` struct, which are already of the correct Go type.

```go
type ArticleFilters struct {
    Category string `query:"category" validate:"required"`
    Page     int    `query:"page" default:"1" validate:"min=1"`
}

func HandleListArticles(c *xylium.Context, filters ArticleFilters) error {
    c.Logger().Infof("Fetching articles for category '%s', page %d", filters.Category, filters.Page)
    // ... logic using filters.Category (string) and filters.Page (int) ...
    return c.JSON(http.StatusOK, xylium.M{"category": filters.Category, "page": filters.Page, "articles": "..."})
}

// Registration:
// xylium.RegisterGETParams[ArticleFilters](app, "/articles", HandleListArticles, true)
```

## 5. How it Works Under the Hood

1.  The generic registration function (`RegisterParams`) wraps your provided handler.
2.  When a request matches the route, this wrapper is invoked.
3.  Inside the wrapper:
    *   An instance of your parameter struct (`P`) is created.
    *   Reflection is used to iterate over the fields of the struct.
    *   For each field:
        *   It looks for `path:"paramName"` or `query:"paramName"` tags.
        *   It extracts the raw string value(s) using `c.Param(paramName)` or from `c.Ctx.QueryArgs()`.
        *   If a `default:"value"` tag is present for a query parameter and the parameter is not in the URL, the default is used.
        *   The raw string value(s) are converted to the field's Go type (e.g., `int`, `string`, `[]bool`, `*time.Time`).
        *   The struct field is set with the converted value.
    *   If `enableValidation` is true, the populated struct instance is passed to `GetValidator().Struct()`.
    *   If any binding, conversion, or validation error occurs, an appropriate `*xylium.HTTPError` (usually 400 Bad Request) is returned, which is then handled by the `GlobalErrorHandler`.
    *   If successful, your original handler is called with the `*xylium.Context` and the populated, validated `params` struct.

## 6. Error Handling

If an error occurs during the parameter binding, conversion, or validation process:
*   An `*xylium.HTTPError` is returned by the wrapper.
*   This error typically has an HTTP status of `400 Bad Request`.
*   The error message will contain details about which parameter or field failed and why (e.g., "Invalid path parameter 'userId' for field 'UserID': failed to parse 'abc' as integer" or "Parameter validation failed. details: {Category: 'field 'Category' failed validation on tag 'required'}'").
*   Xylium's `GlobalErrorHandler` will process this `HTTPError`, log it, and send the JSON error response to the client.

You do not need to handle these binding/validation errors explicitly in your type-safe handler; the framework takes care of it.

## 7. Examples

### 7.1. Path Parameters Example

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

type ProductPathParams struct {
	ProductID uint   `path:"pid" validate:"required,gt=0"`
	Version   string `path:"version" validate:"omitempty,alphanum"`
}

func GetProductVersion(c *xylium.Context, params ProductPathParams) error {
	c.Logger().Infof("Fetching product ID %d, version '%s'", params.ProductID, params.Version)
	return c.JSON(http.StatusOK, xylium.M{
		"product_id": params.ProductID,
		"version":    params.Version,
		"details":    "Product version details...",
	})
}

func main() {
	app := xylium.New()
	xylium.RegisterGETParams[ProductPathParams](app, "/products/:pid/versions/:version", GetProductVersion, true)
	// Example request: GET /products/123/versions/v2
	
    // Example for path param only
    type SimpleProductParams struct {
        ProductID uint `path:"id"`
    }
    xylium.RegisterGETParams[SimpleProductParams](app, "/product/:id", 
        func(c *xylium.Context, params SimpleProductParams) error {
		    return c.String(http.StatusOK, "Product ID: %d", params.ProductID)
	    }, true)
    // Example request: GET /product/456

	app.Start(":8080")
}
```

### 7.2. Query Parameters Example with Slices and Defaults

```go
package main

import (
	"net/http"
	"time"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

type EventSearchQuery struct {
	Keywords  []string   `query:"keyword" validate:"omitempty,dive,min=2"`
	Limit     int        `query:"limit" default:"10" validate:"min=1,max=50"`
	StartDate *time.Time `query:"start_date" validate:"omitempty"` // Optional start date
	IsLive    *bool      `query:"live" default:"true"`             // Optional, defaults to true
}

func SearchEvents(c *xylium.Context, query EventSearchQuery) error {
	liveStatus := "any"
	if query.IsLive != nil {
		if *query.IsLive {
			liveStatus = "live only"
		} else {
			liveStatus = "not live"
		}
	}
	
	c.Logger().Infof("Searching events with keywords: %v, limit: %d, live: %s", 
        query.Keywords, query.Limit, liveStatus)
	
    if query.StartDate != nil {
        c.Logger().Infof("Filtering by start date: %s", query.StartDate.Format(time.DateOnly))
    }

	return c.JSON(http.StatusOK, xylium.M{
		"filters_applied": query,
		"results_count":   0, // Placeholder
	})
}

func main() {
	app := xylium.New()
	xylium.RegisterGETParams[EventSearchQuery](app, "/events/search", SearchEvents, true)
	// Example: /events/search?keyword=conference&keyword=tech&live=false&start_date=2024-08-01
	// Example: /events/search (uses defaults: limit=10, live=true)
	app.Start(":8080")
}
```

### 7.3. Combined Path and Query Parameters

You can define a single struct that includes fields for both path and query parameters.

```go
package main

import (
	"net/http"
	"github.com/arwahdevops/xylium-core/src/xylium"
)

type ItemDetailsParams struct {
	StoreID  int    `path:"storeId" validate:"required,gt=0"`
	ItemID   string `path:"itemId" validate:"required,uuid4"`
	Format   string `query:"format" default:"json" validate:"oneof=json xml"`
	Extended *bool  `query:"extended_info" default:"false"`
}

func GetItemDetails(c *xylium.Context, params ItemDetailsParams) error {
	c.Logger().Infof("Store: %d, Item: %s, Format: %s, Extended: %v",
		params.StoreID, params.ItemID, params.Format, *params.Extended)
	return c.JSON(http.StatusOK, params)
}

func main() {
	app := xylium.New()
	// Use the general RegisterParams for clarity or if method is not GET
	xylium.RegisterParams[ItemDetailsParams](app, http.MethodGet, "/stores/:storeId/items/:itemId", GetItemDetails, true)
	// Example: /stores/101/items/f47ac10b-58cc-4372-a567-0e02b2c3d479?format=xml&extended_info=true
	app.Start(":8080")
}
```

## 8. Benefits and Considerations

**Benefits:**
*   **Enhanced Type Safety**: Reduces runtime errors related to incorrect parameter types.
*   **Improved Code Clarity**: Handlers and parameter structs are self-documenting.
*   **Reduced Boilerplate**: Eliminates repetitive parsing and conversion logic.
*   **Integrated Validation**: Seamlessly use struct tags for validating parameters.
*   **Better Maintainability**: Easier to refactor and understand parameter handling.

**Considerations:**
*   **Reflection Overhead**: This feature uses reflection, which has a performance cost compared to manual parsing. For extremely high-performance, critical paths, manual parsing or `XBind` might still be preferred. However, for most applications, the DX benefits often outweigh the minor overhead.
*   **Generics Requirement**: Relies on Go generics (Go 1.18+).
*   **Struct Definition**: Requires defining a struct for parameters, which might feel like extra setup for very simple cases with one or two parameters.

By using Xylium's type-safe parameter binding, you can write cleaner, more robust, and more maintainable Go web applications.
