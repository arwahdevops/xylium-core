package xylium

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10" // For struct validation.
)

// HandlerWithParamsFunc defines the signature for handlers that accept type-safe parameters.
// P is the struct type containing fields tagged for path or query parameter binding.
// This handler function is called after Xylium has successfully bound and (optionally)
// validated the parameters into an instance of P.
type HandlerWithParamsFunc[P any] func(c *Context, params P) error

// RegisterParams is a generic helper function for registering routes with type-safe
// parameter binding for URL path and query parameters.
// It wraps the provided `handler` (of type `HandlerWithParamsFunc[P]`) with logic
// that automatically:
//  1. Creates an instance of the parameter struct `P`.
//  2. Populates its fields from `c.Param()` (for `path:"..."` tags) and
//     `c.Ctx.QueryArgs()` (for `query:"..."` tags), respecting `default:"..."` tags for query parameters.
//  3. Converts the extracted string values to the Go types of the struct fields.
//  4. If `enableValidation` is true, validates the populated struct using Xylium's
//     configured validator and `validate` tags on the struct fields.
//  5. Calls the original `handler` with the populated and validated `params` struct.
//
// If any step (binding, conversion, validation) fails, an appropriate `*xylium.HTTPError`
// (typically 400 Bad Request) is returned, which is then handled by `Router.GlobalErrorHandler`.
//
// Parameters:
//   - r: The `*xylium.Router` instance to register the route on.
//   - method: The HTTP method string (e.g., `http.MethodGet`, `http.MethodPost`).
//   - path: The URL path pattern for the route (e.g., "/users/:userID").
//   - handler: The `HandlerWithParamsFunc[P]` to execute upon a successful request.
//   - enableValidation: A boolean indicating whether to validate the `params` struct after binding.
//   - routeMiddleware: Optional `xylium.Middleware` specific to this route.
//
// Type Parameter:
//   - P: The struct type (not a pointer) that defines the expected path and query parameters.
//     Fields in `P` should be tagged with `path:"paramName"`, `query:"paramName"`,
//     `default:"defaultValue"` (for query), and `validate:"rules"`.
//
// Usage:
//
//	type GetUserDetailsParams struct {
//	    UserID   int    `path:"id" validate:"required,min=1"`
//	    Format   string `query:"format" default:"json" validate:"oneof=json xml"`
//	    Extended *bool  `query:"extended" default:"false"`
//	}
//
//	func getUserDetailsHandler(c *xylium.Context, params GetUserDetailsParams) error {
//	    // params.UserID, params.Format, params.Extended are populated and validated.
//	    return c.JSON(http.StatusOK, params)
//	}
//
//	app := xylium.New()
//	xylium.RegisterParams[GetUserDetailsParams](app, http.MethodGet, "/users/:id", getUserDetailsHandler, true)
//
// Note: This mechanism is distinct from the general-purpose `c.Bind()` and `c.BindAndValidate()`
// which primarily bind request bodies (JSON, XML, Form) or query parameters for GET requests
// without the compile-time type safety for the handler signature offered here.
// The `default` tag for query parameters is exclusively supported by this type-safe binding mechanism.
// For more details on supported field types and tags, refer to `TypeSafeParams.md`.
func RegisterParams[P any](r *Router, method string, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	// wrappedHandler is the actual handler registered with the router.
	// It performs the binding and validation before calling the user's type-safe handler.
	wrappedHandler := func(c *Context) error {
		var params P // Create an instance of the parameter struct P.
		paramsValue := reflect.ValueOf(&params).Elem()
		paramsType := paramsValue.Type()

		// Ensure P is indeed a struct.
		if paramsType.Kind() != reflect.Struct {
			err := fmt.Errorf("type P in RegisterParams must be a struct, got %s", paramsType.Kind())
			c.Logger().Errorf("Internal configuration error for generic handler: %v", err)
			return NewHTTPError(http.StatusInternalServerError, "Generic handler misconfiguration").WithInternal(err)
		}

		// Iterate over the fields of the parameter struct.
		for i := 0; i < paramsValue.NumField(); i++ {
			fieldStruct := paramsType.Field(i) // reflect.StructField
			fieldValue := paramsValue.Field(i) // reflect.Value

			if !fieldValue.CanSet() { // Skip unexported or unaddressable fields.
				continue
			}

			// --- Bind from Path Parameters ---
			pathTag := fieldStruct.Tag.Get("path")
			if pathTag != "" && pathTag != "-" { // If path tag is present and not "-"
				paramName := strings.Split(pathTag, ",")[0] // Get name part of tag.
				rawParamValue := c.Param(paramName)         // Extract from c.Params (set by router).

				// Set the struct field value from the raw path parameter string.
				if err := setStructFieldFromStrings(fieldValue, fieldStruct.Type, []string{rawParamValue}, "path parameter", paramName, ""); err != nil {
					return NewHTTPError(http.StatusBadRequest,
						fmt.Sprintf("Invalid path parameter '%s' for field '%s': %v", paramName, fieldStruct.Name, err)).WithInternal(err)
				}
				continue // Move to next field if path parameter was bound.
			}

			// --- Bind from Query Parameters ---
			queryTag := fieldStruct.Tag.Get("query")
			if queryTag != "" && queryTag != "-" { // If query tag is present and not "-"
				paramName := strings.Split(queryTag, ",")[0]
				defaultValue := fieldStruct.Tag.Get("default") // Get default value if specified.

				var rawParamValues []string
				queryArgs := c.Ctx.QueryArgs()               // Access fasthttp query arguments.
				byteValues := queryArgs.PeekMulti(paramName) // Get all values for this query key.

				if len(byteValues) > 0 { // If parameter was present in query.
					rawParamValues = make([]string, len(byteValues))
					for k, bv := range byteValues {
						rawParamValues[k] = string(bv)
					}
				} else if defaultValue != "" { // Parameter not in query, but has a default value.
					rawParamValues = []string{defaultValue}
				}
				// If not in query and no default, rawParamValues remains empty/nil;
				// setStructFieldFromStrings will handle this (e.g., for pointers or zero values).

				// Set the struct field value from the raw query parameter string(s).
				if err := setStructFieldFromStrings(fieldValue, fieldStruct.Type, rawParamValues, "query parameter", paramName, defaultValue); err != nil {
					return NewHTTPError(http.StatusBadRequest,
						fmt.Sprintf("Invalid query parameter '%s' for field '%s': %v", paramName, fieldStruct.Name, err)).WithInternal(err)
				}
			}
		}

		// --- Validate the populated parameter struct (if enabled) ---
		if enableValidation {
			currentValidator := GetValidator() // Get Xylium's configured validator.
			if err := currentValidator.Struct(params); err != nil {
				// Convert validation errors to a structured HTTPError.
				return formatValidationErrors(err, "Parameter")
			}
		}

		// Call the user's original type-safe handler.
		return handler(c, params)
	}

	// Add the wrapped handler to the router.
	r.addRoute(method, path, wrappedHandler, routeMiddleware...)
}

// RegisterGETParams is a convenience helper for `RegisterParams` specifically for GET requests.
// See `RegisterParams` for detailed documentation.
func RegisterGETParams[P any](r *Router, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	RegisterParams(r, http.MethodGet, path, handler, enableValidation, routeMiddleware...)
}

// RegisterPOSTParams is a convenience helper for `RegisterParams` specifically for POST requests.
// Note: This is primarily for binding URL path or query parameters in POST requests.
// For binding the request body of a POST request (e.g., JSON, form), use `c.BindAndValidate()`
// or `c.Bind()` within the handler.
// See `RegisterParams` for detailed documentation.
func RegisterPOSTParams[P any](r *Router, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	RegisterParams(r, http.MethodPost, path, handler, enableValidation, routeMiddleware...)
}

// setStructFieldFromStrings populates a single struct field (`fieldValue` of `fieldType`)
// with string values (`strValues`) from the request.
// It handles slices and pointers to scalar types.
// `sourceDesc` and `paramName` are used for richer error messages.
// `defaultValue` is context for query params, mainly for logging/debugging, as its application
// is handled before this function for query params.
func setStructFieldFromStrings(fieldValue reflect.Value, fieldType reflect.Type, strValues []string, sourceDesc string, paramName string, defaultValue string) error {
	isPtrField := fieldType.Kind() == reflect.Ptr
	underlyingFieldType := fieldType
	if isPtrField {
		underlyingFieldType = fieldType.Elem()
	}

	// If no string values are provided (e.g., query param not present and no default).
	if len(strValues) == 0 {
		if isPtrField && fieldValue.IsNil() {
			return nil // Keep nil pointer if no value provided.
		}
		// For non-pointer, non-slice string fields, set to empty string.
		if underlyingFieldType.Kind() == reflect.String {
			targetVal := fieldValue
			if isPtrField { // This branch might be less common if Ptr with empty value is already handled
				if targetVal.IsNil() {
					targetVal.Set(reflect.New(underlyingFieldType))
				}
				targetVal = targetVal.Elem()
			}
			targetVal.SetString("")
			return nil
		}
		// For non-pointer slice fields, set to empty slice.
		if underlyingFieldType.Kind() == reflect.Slice {
			targetVal := fieldValue
			if isPtrField { // Slice field itself could be a pointer to a slice: *[]string
				if targetVal.IsNil() {
					targetVal.Set(reflect.New(underlyingFieldType))
				}
				targetVal = targetVal.Elem()
			}
			targetVal.Set(reflect.MakeSlice(underlyingFieldType, 0, 0))
			return nil
		}
		// For other non-pointer, non-slice types, leave as zero value if no input.
		return nil
	}

	// If field is a pointer, allocate if nil, unless it's an empty string for non-string type.
	if isPtrField && fieldValue.IsNil() {
		// Special handling: if the input is an empty string for a pointer to a non-string, non-slice type,
		// treat it as "not provided" and keep the pointer nil.
		if len(strValues) == 1 && strValues[0] == "" &&
			underlyingFieldType.Kind() != reflect.String &&
			underlyingFieldType.Kind() != reflect.Slice {
			return nil // Keep pointer nil to differentiate from zero value.
		}
		fieldValue.Set(reflect.New(underlyingFieldType)) // Allocate new element.
	}

	// Get the target reflect.Value to set (dereference if it was a pointer).
	targetValueToSet := fieldValue
	if isPtrField {
		targetValueToSet = fieldValue.Elem()
	}

	// Handle slice fields (e.g., []string, []int, []*bool).
	if targetValueToSet.Kind() == reflect.Slice {
		sliceElemType := targetValueToSet.Type().Elem()
		if len(strValues) == 0 { // If, after defaults, still no values for slice.
			targetValueToSet.Set(reflect.MakeSlice(targetValueToSet.Type(), 0, 0))
			return nil
		}
		newSlice := reflect.MakeSlice(targetValueToSet.Type(), len(strValues), len(strValues))
		for i, strVal := range strValues {
			elemVal := newSlice.Index(i) // Get the Value for the i-th element of the new slice.
			// setScalar handles setting the element, including if the element itself is a pointer (e.g., []*int).
			if err := setScalar(elemVal, sliceElemType, strVal, sourceDesc, paramName); err != nil {
				return fmt.Errorf("error setting slice element %d for %s '%s' from value '%s': %w", i, sourceDesc, paramName, strVal, err)
			}
		}
		targetValueToSet.Set(newSlice) // Set the struct field to the newly populated slice.
		return nil
	}

	// Handle scalar fields (or dereferenced pointer to scalar).
	// Use the first string value from `strValues` as scalars expect a single value.
	if len(strValues) > 0 {
		return setScalar(targetValueToSet, targetValueToSet.Type(), strValues[0], sourceDesc, paramName)
	}
	// If strValues is empty (e.g. for a non-slice, non-pointer field where query param was absent and no default),
	// the field remains its zero value, which is usually fine.
	return nil
}

// setScalar sets a scalar (non-slice) field.
// `scalarValue` is the reflect.Value of the field/element to set.
// `scalarType` is its reflect.Type.
// `strValue` is the string data from the request.
// Handles direct scalar types and also pointer-to-scalar types (if `scalarType` is Ptr).
// This is used for struct fields directly or for elements within a slice (e.g. []*int).
func setScalar(scalarValue reflect.Value, scalarType reflect.Type, strValue string, sourceDesc string, paramName string) error {
	isPtrElement := scalarType.Kind() == reflect.Ptr // Check if the type itself is a pointer (e.g. *int, *string in a slice []*int)
	actualScalarType := scalarType
	targetScalarValue := scalarValue

	if isPtrElement {
		actualScalarType = scalarType.Elem() // Get the underlying type (e.g., int from *int)

		// For pointer elements (like in []*int): if the string value is empty and the
		// underlying type is not string, leave this specific pointer element as nil.
		if strValue == "" && actualScalarType.Kind() != reflect.String {
			// `scalarValue` is the reflect.Value of the pointer itself (e.g., the *int in []*int).
			// If it's settable and currently nil, we can leave it nil.
			if targetScalarValue.CanSet() && targetScalarValue.IsNil() {
				return nil // Leave this pointer element as nil.
			}
		}
		// If we need to set a value (strValue not empty, or it's for *string), ensure pointer is allocated.
		if targetScalarValue.IsNil() {
			targetScalarValue.Set(reflect.New(actualScalarType)) // Create new instance of underlying type.
		}
		targetScalarValue = targetScalarValue.Elem() // Dereference to set the actual value.
	}

	// Special handling for time.Time
	if actualScalarType == reflect.TypeOf(time.Time{}) {
		// If the original field was a pointer (*time.Time) and strValue is empty,
		// isPtrElement path above should have returned, leaving pointer nil.
		// If it's a direct time.Time field, empty string is an error.
		if strValue == "" {
			if isPtrElement { // This case should have been handled by nil return for Ptr above.
				return nil // Safety: if somehow strValue is empty for a *time.Time that's being set.
			}
			return errors.New("cannot parse empty string as time.Time")
		}

		// Attempt to parse with common date/datetime formats.
		formats := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} // RFC3339, ISO-like, Date only
		var parsedTime time.Time
		var err error
		parsed := false
		for _, format := range formats {
			parsedTime, err = time.Parse(format, strValue)
			if err == nil {
				parsed = true
				break
			}
		}
		if !parsed {
			return fmt.Errorf("cannot parse '%s' as time.Time for %s '%s' (tried RFC3339, YYYY-MM-DDTHH:MM:SS, YYYY-MM-DD)", strValue, sourceDesc, paramName)
		}
		targetScalarValue.Set(reflect.ValueOf(parsedTime))
		return nil
	}

	// Handle empty string for non-string, non-pointer scalar types:
	// If the field is NOT a pointer itself (isPtrElement is false),
	// and its type is not string, and the input string is empty,
	// it typically means the parameter was provided as empty. For numeric/bool,
	// this should lead to parsing error or be treated as "not set if optional".
	// Here, we let it fall through to strconv, which will err for empty on numerics/bool.
	// If it was an optional field, validation (`omitempty`) or pointer type should handle "not provided".
	// If the field was a pointer and strValue is empty (and not *string), it's handled above by returning nil early.
	if strValue == "" && actualScalarType.Kind() != reflect.String && !isPtrElement {
		// For direct scalar fields (not pointers to them), an empty string usually means
		// "not provided" or "invalid for type". If it needs to be zero, pointer is better.
		// Or, validation (e.g. `omitempty` + `min=0` for int) handles if it's optional.
		// Here, we can simply return nil, effectively leaving it as zero value for non-string types.
		// This behavior aligns with fasthttp's form/query parsing which might yield empty string
		// for absent optional numerics if not handled by default tag.
		// For consistency with ContextBinding.setScalarField, let strconv handle empty for non-strings.
		// No, correction: if it's a direct scalar and input is empty, it should result in parse error.
		// The `if isPtrElement` block above handles *Type cases correctly for empty strings.
		// This block is for direct Type fields.
		// Let strconv calls below handle the empty string for their respective types.
	}

	// Handle other scalar types.
	switch actualScalarType.Kind() {
	case reflect.String:
		targetScalarValue.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strValue == "" { // strconv.ParseInt errors on empty string
			return fmt.Errorf("cannot parse empty string as integer (type %s) for %s '%s'", actualScalarType.Kind(), sourceDesc, paramName)
		}
		i, err := strconv.ParseInt(strValue, 10, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as integer (type %s) for %s '%s': %w", strValue, actualScalarType.Kind(), sourceDesc, paramName, err)
		}
		targetScalarValue.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" { // strconv.ParseUint errors on empty string
			return fmt.Errorf("cannot parse empty string as unsigned integer (type %s) for %s '%s'", actualScalarType.Kind(), sourceDesc, paramName)
		}
		u, err := strconv.ParseUint(strValue, 10, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as unsigned integer (type %s) for %s '%s': %w", strValue, actualScalarType.Kind(), sourceDesc, paramName, err)
		}
		targetScalarValue.SetUint(u)
	case reflect.Bool:
		if strValue == "" { // strconv.ParseBool errors on empty string
			return fmt.Errorf("cannot parse empty string as boolean for %s '%s'", sourceDesc, paramName)
		}
		b, err := strconv.ParseBool(strValue) // Handles "true", "false", "1", "0", etc.
		if err != nil {
			// Add custom parsing for common checkbox/form values.
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b, err = true, nil
			} else if lowerVal == "off" || lowerVal == "no" {
				b, err = false, nil
			}
			if err != nil { // If still an error after custom checks.
				return fmt.Errorf("failed to parse '%s' as boolean for %s '%s': %w", strValue, sourceDesc, paramName, err)
			}
		}
		targetScalarValue.SetBool(b)
	case reflect.Float32, reflect.Float64:
		if strValue == "" { // strconv.ParseFloat errors on empty string
			return fmt.Errorf("cannot parse empty string as float (type %s) for %s '%s'", actualScalarType.Kind(), sourceDesc, paramName)
		}
		f, err := strconv.ParseFloat(strValue, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as float (type %s) for %s '%s': %w", strValue, actualScalarType.Kind(), sourceDesc, paramName, err)
		}
		targetScalarValue.SetFloat(f)
	default:
		return fmt.Errorf("unsupported field type '%s' for %s '%s' binding", actualScalarType.Kind(), sourceDesc, paramName)
	}
	return nil
}

// formatValidationErrors converts validator.ValidationErrors into a structured *xylium.HTTPError.
// `contextName` (e.g., "Parameter", "Body") is used in the error message title.
func formatValidationErrors(err error, contextName string) *HTTPError {
	if vErrs, ok := err.(validator.ValidationErrors); ok {
		errFields := make(map[string]string)
		for _, fe := range vErrs {
			// Use fe.Namespace() for full path in nested structs, fe.Field() for simple field name.
			// For parameters, fe.Field() is usually sufficient.
			fieldName := fe.Field()
			// Construct a user-friendly error message for each validation failure.
			errMsg := fmt.Sprintf("field '%s' failed validation on tag '%s'", fieldName, fe.Tag())
			if fe.Param() != "" { // Include validation parameter if present (e.g., min=3, max=10).
				errMsg += fmt.Sprintf(" with value '%s'", fe.Param())
			}
			// Optionally include the actual problematic value, being mindful of sensitive data.
			// fe.Value() returns interface{}, so format appropriately.
			if fe.Value() != nil && fe.Value() != "" { // Avoid printing for empty optional fields.
				errMsg += fmt.Sprintf(" (actual value: '%v')", fe.Value())
			}
			errFields[fieldName] = errMsg
		}
		title := fmt.Sprintf("%s validation failed.", contextName)
		// Return a 400 Bad Request with structured details.
		return NewHTTPError(http.StatusBadRequest, M{"message": title, "details": errFields}).WithInternal(err)
	}
	// If the error is not validator.ValidationErrors, it's an unexpected validation processing error.
	return NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("%s validation processing error.", contextName)).WithInternal(err)
}
