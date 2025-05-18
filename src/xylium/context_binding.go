package xylium

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10" // For struct validation.
	"github.com/valyala/fasthttp"          // For fasthttp.Args.
)

// --- Data Binding and Validation ---
// This section provides methods for binding incoming request data (JSON, XML, form, query)
// to Go structs and validating them.

// BindAndValidate first attempts to bind request data to the `out` struct using `c.Bind(out)`.
// If binding is successful, it then validates the populated `out` struct using Xylium's
// configured validator (see `xylium.GetValidator()`).
// - `out` must be a pointer to a struct.
// Returns an `*xylium.HTTPError` (typically with status 400 Bad Request) if binding
// or validation fails. The error message may include details about validation failures.
// Returns nil if both binding and validation are successful.
func (c *Context) BindAndValidate(out interface{}) error {
	if err := c.Bind(out); err != nil {
		// c.Bind() already returns an *HTTPError or nil.
		return err
	}

	// Get the currently configured validator instance.
	currentValidator := GetValidator()
	if err := currentValidator.Struct(out); err != nil {
		// Validation failed. Convert validator.ValidationErrors into a structured HTTPError.
		if vErrs, ok := err.(validator.ValidationErrors); ok {
			errFields := make(map[string]string)
			for _, fe := range vErrs {
				// Provide a user-friendly message for each validation failure.
				// fe.Field() gives the struct field name. fe.Tag() gives the failed validation tag.
				fieldName := fe.Field() // Or fe.Namespace() for full path in nested structs.
				errFields[fieldName] = fmt.Sprintf("validation failed on '%s' tag", fe.Tag())
				if fe.Param() != "" { // Include validation parameter if present (e.g., min=3, max=10).
					errFields[fieldName] += fmt.Sprintf(" (param: %s)", fe.Param())
				}
			}
			// Return a 400 Bad Request with structured details of validation failures.
			return NewHTTPError(StatusBadRequest, M{"message": "Validation failed.", "details": errFields}).WithInternal(err)
		}
		// If the error is not validator.ValidationErrors, it's an unexpected validation processing error.
		return NewHTTPError(StatusBadRequest, "Validation processing error occurred.").WithInternal(err)
	}
	// Binding and validation successful.
	return nil
}

// Bind attempts to bind request data to the `out` interface based on the request's
// Content-Type and HTTP method.
// - `out` must be a pointer to a struct or `*map[string]string` (for form/query binding to a map).
// Supported sources and Content-Types:
// - JSON body: "application/json"
// - XML body: "application/xml", "text/xml"
// - Form data (URL-encoded or multipart): "application/x-www-form-urlencoded", "multipart/form-data"
// - Query parameters: For GET, DELETE, HEAD methods.
// Returns an `*xylium.HTTPError` if binding fails (e.g., invalid JSON, unsupported Content-Type
// with a request body). Returns nil if binding is successful or if there's no data to bind
// (e.g., empty body for relevant methods, or GET request with no query params for struct fields).
func (c *Context) Bind(out interface{}) error {
	// If the request method typically doesn't have a body or if ContentLength is 0,
	// and it's not a GET/DELETE/HEAD (which bind from query), there might be nothing to bind from body.
	// However, GET/DELETE/HEAD will attempt to bind from query parameters.
	// Form binding also handles empty bodies gracefully.
	// JSON/XML binding will fail if body is empty and a struct is expected.
	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		// For methods like POST/PUT with no body, binding might be considered successful (nothing to bind).
		// This allows optional bodies. If a body is required, validation on the struct should catch it.
		return nil
	}

	contentType := c.ContentType()

	// For GET, DELETE, HEAD methods, always attempt to bind from URL query parameters.
	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		if c.queryArgs == nil {
			c.queryArgs = c.Ctx.QueryArgs() // Parse and cache query args if not already done.
		}
		// Bind data from query parameters using struct tags like `query:"fieldName"`.
		return c.bindDataFromArgs(out, c.queryArgs, "query parameters", "query")
	}

	// For other methods (POST, PUT, PATCH, etc.), bind based on Content-Type.
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		if len(c.Body()) == 0 { return nil } // Allow empty JSON body if not required by struct validation.
		if err := json.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data provided in request body.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		if len(c.Body()) == 0 { return nil } // Allow empty XML body.
		if err := xml.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data provided in request body.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		if c.formArgs == nil {
			_ = c.Ctx.PostArgs() // Parse and cache form args if not already done.
			c.formArgs = c.Ctx.PostArgs()
		}
		// Bind data from form fields using struct tags like `form:"fieldName"`.
		return c.bindDataFromArgs(out, c.formArgs, "form data", "form")
	default:
		// If there's a request body but the Content-Type is unsupported for binding.
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for request body binding: "+contentType)
		}
		// If no body and Content-Type is not one of the above, binding is vacuously successful.
	}
	return nil
}

// bindDataFromArgs is an internal helper to bind data from `fasthttp.Args` (query or form)
// into the `out` interface (either `*map[string]string` or a pointer to a struct).
// - `source`: A descriptive string for the data source (e.g., "query parameters", "form data") for error messages.
// - `tagKey`: The struct tag key to use for mapping (e.g., "query" for query params, "form" for form fields).
func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	if args == nil { // No arguments to bind from.
		return nil
	}

	// Handle binding to *map[string]string directly.
	if m, ok := out.(*map[string]string); ok {
		if *m == nil { // Initialize map if it's nil.
			*m = make(map[string]string)
		}
		args.VisitAll(func(key, value []byte) { // Iterate over all arguments.
			(*m)[string(key)] = string(value)
		})
		return nil
	}

	// Handle binding to a struct pointer.
	val := reflect.ValueOf(out)
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Struct {
		if val.Kind() == reflect.Struct { // User passed a struct value instead of a pointer.
			return NewHTTPError(StatusInternalServerError,
				fmt.Sprintf("Binding from %s to non-pointer struct %T is not supported. Pass a pointer to the struct.", source, out))
		}
		// Binding to other types (e.g., *int, *string) from args is not directly supported by this generic binder.
		return NewHTTPError(StatusNotImplemented,
			fmt.Sprintf("Binding from %s to type %T is not implemented. Supported types: *map[string]string, or a pointer to a struct.", source, out))
	}

	elem := val.Elem() // The struct value itself.
	typ := elem.Type()  // The struct type.
	numFields := elem.NumField()

	// Iterate over the fields of the struct.
	for i := 0; i < numFields; i++ {
		field := typ.Field(i)    // reflect.StructField
		fieldVal := elem.Field(i) // reflect.Value for the field

		if !fieldVal.CanSet() { // Skip unexported or unaddressable fields.
			continue
		}

		// Determine the name of the form/query parameter from the struct tag.
		// e.g., `form:"username"` or `query:"search_term"`
		tagValue := field.Tag.Get(tagKey)
		if tagValue == "" || tagValue == "-" { // If no tag or tag is "-", use field name as default.
			tagValue = field.Name
		}

		// The actual parameter name (e.g., "username" from `form:"username,omitempty"`)
		formFieldName := strings.Split(tagValue, ",")[0]
		if formFieldName == "" { // Skip if tag specifies an empty name after options.
			continue
		}

		var argValues []string // Holds string values from form/query for this field.
		if fieldVal.Kind() == reflect.Slice {
			// For slice fields, get all values for the parameter name (e.g., ?id=1&id=2&id=3).
			byteValues := args.PeekMulti(formFieldName)
			if len(byteValues) == 0 { // No values found for this parameter.
				continue
			}
			argValues = make([]string, len(byteValues))
			for i, bv := range byteValues {
				argValues[i] = string(bv)
			}
		} else {
			// For non-slice fields, get the first value for the parameter name.
			argValueBytes := args.Peek(formFieldName)
			if argValueBytes == nil { // Parameter not found.
				continue
			}
			argValues = []string{string(argValueBytes)}
		}

		// Set the struct field's value using the extracted string(s).
		if err := c.setStructField(fieldVal, field.Type, argValues); err != nil {
			return NewHTTPError(StatusBadRequest,
				fmt.Sprintf("Error binding %s parameter '%s' to field '%s' (type %s): %v",
					source, formFieldName, field.Name, field.Type.String(), err)).WithInternal(err)
		}
	}
	return nil
}

// setStructField populates a single struct field (`fieldVal` of `fieldType`)
// with string values (`strValues`) from the request.
// It handles slices and pointers to scalar types.
func (c *Context) setStructField(fieldVal reflect.Value, fieldType reflect.Type, strValues []string) error {
	if len(strValues) == 0 { // Nothing to set if no values were provided.
		return nil
	}

	// If the field is a pointer type (e.g., *string, *int, *time.Time),
	// allocate a new instance if it's nil, then operate on the pointed-to element.
	if fieldType.Kind() == reflect.Ptr {
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem())) // Allocate new element of pointed-to type.
		}
		// Dereference: subsequent operations apply to the value pointed to.
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem()
	}

	// If the field is a slice (e.g., []string, []int).
	if fieldType.Kind() == reflect.Slice {
		sliceElemType := fieldType.Elem() // Get the type of elements in the slice.
		// Create a new slice of the correct type and length.
		newSlice := reflect.MakeSlice(fieldType, len(strValues), len(strValues))
		for i, strVal := range strValues {
			// Set each element of the new slice by converting the string value.
			if err := c.setScalarField(newSlice.Index(i), sliceElemType, strVal); err != nil {
				return fmt.Errorf("error setting slice element %d from value '%s': %w", i, strVal, err)
			}
		}
		fieldVal.Set(newSlice) // Set the struct field to the newly populated slice.
		return nil
	}

	// If the field is a scalar (non-slice, non-pointer or dereferenced pointer).
	// Use the first string value from `strValues` as scalars expect a single value.
	return c.setScalarField(fieldVal, fieldType, strValues[0])
}

// setScalarField sets a scalar (non-slice) field (`fieldVal` of `fieldType`)
// from a single string value (`strValue`). It handles common scalar types
// like string, int, uint, bool, float, and time.Time.
func (c *Context) setScalarField(fieldVal reflect.Value, fieldType reflect.Type, strValue string) error {
	// Handle time.Time separately due to multiple supported parsing formats.
	if fieldType == reflect.TypeOf(time.Time{}) {
		if !fieldVal.CanSet() { // Should have been caught earlier, but defensive.
			return fmt.Errorf("field of type time.Time cannot be set")
		}
		// Try parsing as RFC3339 format (e.g., "2006-01-02T15:04:05Z07:00").
		parsedTimeRFC3339, errRFC3339 := time.Parse(time.RFC3339, strValue)
		if errRFC3339 == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeRFC3339))
			return nil
		}
		// Try parsing as YYYY-MM-DD date format.
		parsedTimeDate, errDate := time.Parse("2006-01-02", strValue)
		if errDate == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeDate))
			return nil
		}
		// If both parsing attempts fail, return a comprehensive error.
		return fmt.Errorf("cannot parse '%s' as time.Time (tried RFC3339: %v; tried YYYY-MM-DD: %v)", strValue, errRFC3339, errDate)
	}

	// Handle other scalar types.
	switch fieldType.Kind() {
	case reflect.String:
		fieldVal.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// For numeric types, if strValue is empty, set to 0 (Go's default for numbers).
		if strValue == "" {
			fieldVal.SetInt(0)
			return nil
		}
		i, err := strconv.ParseInt(strValue, 10, fieldType.Bits()) // Base 10, bit size from field type.
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" {
			fieldVal.SetUint(0)
			return nil
		}
		u, err := strconv.ParseUint(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as unsigned integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetUint(u)
	case reflect.Bool:
		// For booleans, if strValue is empty, set to false (Go's default for bool).
		if strValue == "" {
			fieldVal.SetBool(false)
			return nil
		}
		// strconv.ParseBool handles "true", "false", "1", "0", "T", "F", etc.
		b, err := strconv.ParseBool(strValue)
		if err != nil {
			// Add custom parsing for common checkbox values like "on", "yes", "off", "no".
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b = true
			} else if lowerVal == "off" || lowerVal == "no" {
				b = false
			} else {
				// If still not parsable, return the original error from strconv.ParseBool.
				return fmt.Errorf("cannot parse '%s' as boolean: %w", strValue, err)
			}
		}
		fieldVal.SetBool(b)
	case reflect.Float32, reflect.Float64:
		if strValue == "" {
			fieldVal.SetFloat(0)
			return nil
		}
		f, err := strconv.ParseFloat(strValue, fieldType.Bits()) // Bit size from field type.
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as float (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetFloat(f)
	default:
		return fmt.Errorf("unsupported scalar field type '%s' for form/query binding", fieldType.Kind())
	}
	return nil
}
