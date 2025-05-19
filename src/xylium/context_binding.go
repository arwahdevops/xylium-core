package xylium

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10" // For struct validation.
	"github.com/valyala/fasthttp"            // For fasthttp.Args.
)

// XBind is an interface that can be implemented by types
// to provide custom data binding logic. If a type implements this
// interface, its Bind method will be called by c.Bind(),
// bypassing the default reflection-based binding. This allows for
// optimized binding for performance-critical types.
type XBind interface {
	Bind(c *Context) error
}

// BindAndValidate first attempts to bind request data to the `out` struct.
// If `out` implements XBind, its Bind method is called.
// Otherwise, c.Bind(out) (which then calls reflection-based binding) is used.
// If binding is successful, it then validates the populated `out` struct using Xylium's
// configured validator.
// - `out` must be a pointer to a struct.
// Returns an `*xylium.HTTPError` if binding or validation fails.
// The error message may include details about validation failures.
func (c *Context) BindAndValidate(out interface{}) error {
	// The Bind method below will handle calling either the custom XBind.Bind
	// or the internal reflection-based binding.
	if err := c.Bind(out); err != nil {
		// If Bind (custom or reflection) returns an error, it's expected to be an *HTTPError or nil.
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
				fieldName := fe.Field() // Or fe.Namespace() for full path in nested structs.
				errMsg := fmt.Sprintf("validation failed on tag '%s'", fe.Tag())
				if fe.Param() != "" { // Include validation parameter if present (e.g., min=3, max=10).
					errMsg += fmt.Sprintf(" (param: %s)", fe.Param())
				}
				errFields[fieldName] = errMsg
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

// Bind attempts to bind request data to the `out` interface.
// If `out` implements the `XBind` interface, its `Bind` method is called.
// Otherwise, it falls back to reflection-based binding (`bindWithReflection`)
// which considers Content-Type and HTTP method.
// - `out` must be a pointer to a struct or `*map[string]string` (for reflection-based form/query binding).
// Returns an `*xylium.HTTPError` if binding fails, or nil on success.
func (c *Context) Bind(out interface{}) error {
	// Check if 'out' is a valid pointer type for binding.
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return NewHTTPError(StatusInternalServerError,
			fmt.Sprintf("Binding target 'out' must be a non-nil pointer, got %T", out)).WithInternal(errors.New("invalid binding target type"))
	}

	// Attempt to use custom binder first.
	if binder, ok := out.(XBind); ok {
		return binder.Bind(c) // Call the custom Bind method.
	}

	// Fallback to reflection-based binding.
	return c.bindWithReflection(out)
}

// bindWithReflection handles the reflection-based binding logic if a custom XBind is not implemented.
// It binds based on Content-Type (JSON, XML, Form) or query parameters for GET/DELETE/HEAD.
func (c *Context) bindWithReflection(out interface{}) error {
	// This check is somewhat redundant if Bind already did it, but good for direct calls or future refactoring.
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() { // Should not happen if called from Bind.
		return NewHTTPError(StatusInternalServerError, "Internal error: bindWithReflection called with invalid target.").WithInternal(errors.New("invalid target for reflection bind"))
	}

	// For methods like POST/PUT with no body, if 'out' is a struct, binding is successful (empty struct).
	// Validation on the struct (e.g., 'required' tags) should catch if body was mandatory.
	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		return nil
	}

	contentType := c.ContentType()

	// For GET, DELETE, HEAD methods, always attempt to bind from URL query parameters.
	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		if c.queryArgs == nil {
			c.queryArgs = c.Ctx.QueryArgs() // Parse and cache query args if not already done.
		}
		return c.bindDataFromArgs(out, c.queryArgs, "query parameters", "query")
	}

	// For other methods (POST, PUT, PATCH, etc.), bind based on Content-Type.
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		body := c.Body()
		if len(body) == 0 { // Allow empty JSON body if not required by struct validation.
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data provided.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		body := c.Body()
		if len(body) == 0 { // Allow empty XML body.
			return nil
		}
		if err := xml.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data provided.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		if c.formArgs == nil {
			_ = c.Ctx.PostArgs() // Parse and cache form args if not already done.
			c.formArgs = c.Ctx.PostArgs()
		}
		return c.bindDataFromArgs(out, c.formArgs, "form data", "form")
	default:
		// If there's a request body but the Content-Type is unsupported for binding.
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for binding: "+contentType)
		}
		// If no body and Content-Type is not one of the above, binding is vacuously successful.
	}
	return nil
}

// bindDataFromArgs is an internal helper to bind data from `fasthttp.Args` (query or form)
// into the `out` interface (either `*map[string]string` or a pointer to a struct).
// - `source`: A descriptive string for the data source (e.g., "query parameters") for error messages.
// - `tagKey`: The struct tag key to use for mapping (e.g., "query", "form").
func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	if args == nil || args.Len() == 0 { // No arguments to bind from.
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
	val := reflect.ValueOf(out) // `val` is Ptr.
	elem := val.Elem()          // The struct value itself.
	if elem.Kind() != reflect.Struct {
		return NewHTTPError(StatusNotImplemented,
			fmt.Sprintf("Binding from %s to type %T is not implemented. Supported: *map[string]string or a pointer to a struct.", source, out))
	}

	typ := elem.Type() // The struct type.
	numFields := elem.NumField()

	// Iterate over the fields of the struct.
	for i := 0; i < numFields; i++ {
		field := typ.Field(i)     // reflect.StructField
		fieldVal := elem.Field(i) // reflect.Value for the field

		if !fieldVal.CanSet() { // Skip unexported or unaddressable fields.
			continue
		}

		tagValue := field.Tag.Get(tagKey)
		formFieldName := ""
		if tagValue != "" && tagValue != "-" {
			formFieldName = strings.Split(tagValue, ",")[0] // Get name part of tag.
		}
		if formFieldName == "" { // If no tag or tag is "-", use field name as default.
			formFieldName = field.Name
		}
		if formFieldName == "-" { // Explicitly skip this field.
			continue
		}

		var argValues []string // Holds string values from form/query for this field.
		if fieldVal.Kind() == reflect.Slice {
			// For slice fields, get all values for the parameter name.
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

	// If the field is a pointer type (e.g., *string, *int, *bool, *time.Time).
	if fieldType.Kind() == reflect.Ptr {
		// For pointer to non-string types, if input string is empty, keep pointer nil.
		// This distinguishes "not provided" or "provided as empty" from a zero value.
		if len(strValues) == 1 && strValues[0] == "" && fieldType.Elem().Kind() != reflect.String {
			return nil // Keep pointer as nil.
		}

		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem())) // Allocate new element of pointed-to type.
		}
		// Dereference: subsequent operations apply to the value pointed to.
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem() // Update fieldType to the element's type.
	}

	// If the field is a slice (e.g., []string, []int).
	if fieldType.Kind() == reflect.Slice {
		sliceElemType := fieldType.Elem() // Get the type of elements in the slice.
		newSlice := reflect.MakeSlice(fieldType, len(strValues), len(strValues))
		for i, strVal := range strValues {
			// Set each element of the new slice by converting the string value.
			// For slices of pointers (e.g. []*int), setScalarField handles the pointer element.
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
// and also handles pointer-to-scalar types if fieldType is a Ptr kind (e.g. for slice of pointers []*Type).
func (c *Context) setScalarField(fieldVal reflect.Value, fieldType reflect.Type, strValue string) error {
	// If the field itself (or slice element type) is a pointer (e.g. for slice of pointers []*int).
	if fieldType.Kind() == reflect.Ptr {
		// If strValue is empty for a non-string pointer element, leave this pointer element nil.
		if strValue == "" && fieldType.Elem().Kind() != reflect.String {
			// fieldVal is the reflect.Value of the pointer itself (e.g., the *int in []*int).
			if fieldVal.CanSet() && fieldVal.IsNil() {
				return nil // Leave this pointer element as nil.
			}
		}
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem())) // Create a new instance of the element type.
		}
		// Dereference the pointer to set the actual value.
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem() // Update fieldType to the underlying element's type.
	}

	// Handle time.Time separately due to multiple supported parsing formats.
	if fieldType == reflect.TypeOf(time.Time{}) {
		if !fieldVal.CanSet() {
			return fmt.Errorf("field of type time.Time cannot be set")
		}
		if strValue == "" { // Empty string is a parsing error for direct time.Time field.
			return fmt.Errorf("cannot parse empty string as time.Time")
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
		return fmt.Errorf("cannot parse '%s' as time.Time (tried RFC3339: %v; tried YYYY-MM-DD: %v)", strValue, errRFC3339, errDate)
	}

	// Handle other scalar types.
	switch fieldType.Kind() {
	case reflect.String:
		fieldVal.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strValue == "" {
			return fmt.Errorf("cannot parse empty string as integer (type %s)", fieldType.Kind())
		}
		i, err := strconv.ParseInt(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" {
			return fmt.Errorf("cannot parse empty string as unsigned integer (type %s)", fieldType.Kind())
		}
		u, err := strconv.ParseUint(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as unsigned integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetUint(u)
	case reflect.Bool:
		if strValue == "" {
			return fmt.Errorf("cannot parse empty string as boolean")
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
			if err != nil {
				return fmt.Errorf("cannot parse '%s' as boolean: %w", strValue, err)
			}
		}
		fieldVal.SetBool(b)
	case reflect.Float32, reflect.Float64:
		if strValue == "" {
			return fmt.Errorf("cannot parse empty string as float (type %s)", fieldType.Kind())
		}
		f, err := strconv.ParseFloat(strValue, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as float (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetFloat(f)
	default:
		return fmt.Errorf("unsupported scalar field type '%s' for form/query binding", fieldType.Kind())
	}
	return nil
}
