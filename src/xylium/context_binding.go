package xylium

import (
	"encoding/json" // For unmarshalling JSON request bodies.
	"encoding/xml"  // For unmarshalling XML request bodies.
	"fmt"           // For string formatting in error messages.
	"reflect"       // For reflection-based data binding.
	"strconv"       // For parsing strings to numeric types and booleans.
	"strings"       // For string manipulation (e.g., splitting tags).
	"time"          // For parsing string values into time.Time.

	"github.com/go-playground/validator/v10" // For struct field validation.
	"github.com/valyala/fasthttp"            // For fasthttp.Args (query/form parameters).
)

// XBind is an interface that can be implemented by custom Go types to provide
// their own specialized data binding logic. If a struct (or other type) passed to
// `c.Bind()` or `c.BindAndValidate()` implements this interface, Xylium will invoke
// its `Bind(c *Context) error` method directly, bypassing the default reflection-based
// binding mechanisms.
//
// Implementing `XBind` is useful for:
//   - Performance-critical paths where reflection overhead needs to be avoided.
//   - Handling custom request body formats (e.g., Protocol Buffers, MessagePack, custom binary).
//   - Implementing complex binding logic that involves pre-processing, conditional mapping,
//     or interaction with multiple parts of the request (`xylium.Context`).
//   - Using alternative unmarshalling libraries (e.g., a faster JSON library).
//
// The `Bind` method receives the `*xylium.Context` and is responsible for populating
// the fields of the receiver struct (or type) from the request data. It should return
// an error if binding fails (preferably an `*xylium.HTTPError` for client responses,
// or a standard Go error that will be wrapped by Xylium).
type XBind interface {
	Bind(c *Context) error
}

// BindAndValidate performs two primary operations:
//  1. **Binding**: It attempts to populate the fields of the `out` struct (which must
//     be a non-nil pointer to a struct) with data from the HTTP request.
//     - If the type of `out` implements the `XBind` interface, its `Bind(c)` method is called.
//     - Otherwise, `c.Bind(out)` is invoked, which uses Xylium's default reflection-based
//     binding logic (see `c.Bind()` and `c.bindWithReflection()` for details on how
//     it determines the data source based on Content-Type and HTTP method).
//  2. **Validation**: If the binding operation is successful (returns no error),
//     `BindAndValidate` then validates the populated `out` struct using Xylium's
//     currently configured `go-playground/validator/v10` instance (retrieved via
//     `xylium.GetValidator()`). Validation rules are typically defined using `validate`
//     struct tags on the fields of `out`.
//
// Parameters:
//   - `out` (interface{}): A non-nil pointer to a struct that will be populated with
//     request data and then validated.
//
// Returns:
//   - `*xylium.HTTPError`: If binding or validation fails.
//   - For binding failures (e.g., malformed JSON, unsupported Content-Type), the error
//     will typically have a status code like `xylium.StatusBadRequest` or
//     `xylium.StatusUnsupportedMediaType`.
//   - For validation failures, the error will be an `*xylium.HTTPError` with
//     status `xylium.StatusBadRequest`. Its `Message` field will be a `xylium.M`
//     (map[string]interface{}) containing:
//   - `"message": "Validation failed."`
//   - `"details": map[string]string` where keys are field names (or field paths
//     for nested structs, e.g., "Address.Street") and values are specific
//     validation error messages (e.g., "validation failed on tag 'required'").
//     Xylium attempts to make these field paths client-friendly by removing the
//     top-level struct name prefix.
//   - `nil`: If both binding and validation are successful.
func (c *Context) BindAndValidate(out interface{}) error {
	// First, attempt to bind the data.
	if err := c.Bind(out); err != nil {
		// If c.Bind() itself returns an error (e.g., *HTTPError for malformed JSON),
		// propagate it directly.
		return err
	}

	// If binding was successful, proceed to validation.
	currentValidator := GetValidator() // Get the globally configured validator.
	if err := currentValidator.Struct(out); err != nil {
		// Validation failed. `err` here is from `go-playground/validator`.
		if vErrs, ok := err.(validator.ValidationErrors); ok {
			// It's a `validator.ValidationErrors` type, meaning we have detailed field errors.
			errFields := make(map[string]string)
			outType := reflect.TypeOf(out) // Get type of the `out` interface (should be *Struct).
			var baseTypeName string
			if outType.Kind() == reflect.Ptr {
				// Get the name of the struct itself (e.g., "CreateUserInput").
				baseTypeName = outType.Elem().Name()
			}

			for _, fe := range vErrs {
				// `fe.Namespace()` gives the full path to the field, e.g., "ValidationStruct.Nested.InnerField".
				fieldName := fe.Namespace()

				// Attempt to make the field name more client-friendly by removing the top-level struct name prefix.
				// Example: "ValidationStruct.Nested.InnerField" becomes "Nested.InnerField".
				// This is done if `baseTypeName` was successfully determined.
				if baseTypeName != "" {
					prefixToRemove := baseTypeName + "."
					if strings.HasPrefix(fieldName, prefixToRemove) {
						fieldName = fieldName[len(prefixToRemove):]
					}
					// If it's not a nested field, `fieldName` might just be "ValidationStruct.FieldName",
					// in which case, after stripping, it becomes "FieldName".
					// If `fieldName` was just "FieldName" (no prefix from validator, e.g. if `RegisterTagNameFunc` was used),
					// this stripping logic won't apply, which is fine.
				}

				// Construct a user-friendly error message for this specific field validation failure.
				errMsg := fmt.Sprintf("validation failed on tag '%s'", fe.Tag())
				if fe.Param() != "" { // Include validation parameter if present (e.g., for 'min', 'max', 'oneof').
					errMsg += fmt.Sprintf(" (param: %s)", fe.Param())
				}
				errFields[fieldName] = errMsg
			}
			// Return a new HTTPError with status 400 and the structured validation details.
			// The original `validator.ValidationErrors` is included as the internal error.
			return NewHTTPError(StatusBadRequest, M{"message": "Validation failed.", "details": errFields}).WithInternal(err)
		}
		// If `err` is not `validator.ValidationErrors` but still an error from `validator.Struct()`,
		// it's an unexpected validation processing error.
		return NewHTTPError(StatusBadRequest, "Validation processing error occurred.").WithInternal(err)
	}
	// Both binding and validation were successful.
	return nil
}

// Bind attempts to bind incoming request data to the `out` interface.
// The `out` argument must be a non-nil pointer to the target data structure
// (typically a struct, but can also be `*map[string]string` for reflection-based
// binding from form/query data).
//
// Binding Logic:
//  1. **Custom Binding (`XBind` Interface)**: If the type of `out` implements
//     the `xylium.XBind` interface, its `Bind(c *Context) error` method is
//     called. This gives the type full control over how it's populated from
//     the request.
//  2. **Reflection-Based Binding**: If `out` does not implement `XBind`, Xylium
//     falls back to its default reflection-based binding mechanism (`c.bindWithReflection`).
//     This mechanism intelligently determines the data source based on the request's
//     HTTP method and `Content-Type` header:
//     - For `GET`, `DELETE`, `HEAD` requests: Binds from URL query parameters (using `query` struct tags).
//     - For `POST`, `PUT`, `PATCH` requests:
//     - `application/json`: Binds from JSON request body (using `json` struct tags).
//     - `application/xml` or `text/xml`: Binds from XML request body (using `xml` struct tags).
//     - `application/x-www-form-urlencoded` or `multipart/form-data`: Binds from
//     form data in the request body (using `form` struct tags).
//     - If a `POST`/`PUT`/`PATCH` request has no body (`Content-Length: 0`), binding
//     succeeds with `out` remaining in its zero-value state (or as initialized).
//     Subsequent validation (if using `BindAndValidate`) will determine if this is acceptable.
//
// Returns:
//   - `*xylium.HTTPError`: If binding fails (e.g., malformed JSON/XML, unsupported Content-Type,
//     target `out` is not a valid non-nil pointer, or reflection-based binding encounters
//     an issue like type mismatch during parsing).
//   - `nil`: If binding is successful.
func (c *Context) Bind(out interface{}) error {
	// Validate that 'out' is a non-nil pointer.
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		// Create an internal error for logging/debugging context.
		internalErr := fmt.Errorf("binding target 'out' must be a non-nil pointer, but got type %T (value: %v)", out, out)
		// Return an HTTPError for the client.
		return NewHTTPError(StatusInternalServerError, "Internal server error: Invalid binding target provided.").WithInternal(internalErr)
	}

	// Check if 'out' implements the XBind interface for custom binding.
	if binder, ok := out.(XBind); ok {
		return binder.Bind(c) // Delegate binding to the type's custom Bind method.
	}

	// Fallback to reflection-based binding.
	return c.bindWithReflection(out)
}

// bindWithReflection is an internal method that handles the default, reflection-based
// data binding logic when a custom `XBind` interface is not implemented by the target.
// It determines the data source (JSON, XML, Form, Query) based on the request's
// `Content-Type` header and HTTP method, then attempts to populate the `out` struct.
//
// Precondition: `out` is guaranteed to be a non-nil pointer by `c.Bind()`.
func (c *Context) bindWithReflection(out interface{}) error {
	// If the request method typically has a body (POST, PUT, PATCH, etc.) but
	// Content-Length is 0, there's no body data to bind. Succeed silently.
	// Subsequent validation (e.g., for required fields) will handle this if needed.
	// This check also implicitly allows GET/DELETE/HEAD (which use query params) to proceed.
	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		return nil // No body to bind from for POST/PUT/PATCH with empty body.
	}

	contentType := c.ContentType() // Get the request's Content-Type header.

	// Determine binding strategy based on HTTP method.
	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		// For GET, DELETE, HEAD, always attempt to bind from URL query parameters.
		if c.queryArgs == nil {
			// Lazily parse and cache query arguments from fasthttp.RequestCtx.
			c.queryArgs = c.Ctx.QueryArgs()
		}
		return c.bindDataFromArgs(out, c.queryArgs, "URL query parameters", "query")
	}

	// For other methods (POST, PUT, PATCH, etc.), determine binding by Content-Type.
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		body := c.Body() // Get the raw request body.
		if len(body) == 0 {
			// Empty JSON body is considered valid for binding (results in zero-value struct).
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data provided in request body.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		body := c.Body()
		if len(body) == 0 {
			return nil // Empty XML body is valid for binding.
		}
		if err := xml.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data provided in request body.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		// For form data (URL-encoded or multipart), bind from POST arguments.
		// Note: multipart/form-data file uploads are handled separately by c.FormFile() / c.MultipartForm().
		// This binding focuses on non-file form fields.
		if c.formArgs == nil {
			// Lazily parse and cache POST form arguments from fasthttp.RequestCtx.
			// Calling PostArgs() parses the body if it hasn't been already.
			_ = c.Ctx.PostArgs() // Ensure parsing.
			c.formArgs = c.Ctx.PostArgs()
		}
		return c.bindDataFromArgs(out, c.formArgs, "form data from request body", "form")
	default:
		// If Content-Type is not recognized for binding and there is a request body,
		// return an "Unsupported Media Type" error.
		// If there's no body, binding can be considered successful (empty struct).
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for request body binding: "+contentType)
		}
		// No body and unrecognized Content-Type: effectively no data to bind, so succeed.
		return nil
	}
	return nil // Should be covered by switch cases.
}

// bindDataFromArgs is an internal helper function to bind data from `fasthttp.Args`
// (which can represent URL query parameters or form data) into the `out` interface.
// The `out` interface is expected to be either `*map[string]string` (to capture all
// arguments into a map) or a pointer to a struct (where fields are populated based
// on struct tags like `query:"fieldName"` or `form:"fieldName"`).
//
// Parameters:
//   - `out` (interface{}): The target to bind data into.
//   - `args` (*fasthttp.Args): The source of key-value data (query or form arguments).
//   - `source` (string): A descriptive string for the data source (e.g., "URL query parameters"), used in error messages.
//   - `tagKey` (string): The struct tag key to look for (e.g., "query", "form").
func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	// If there are no arguments to bind from, nothing to do.
	if args == nil || args.Len() == 0 {
		return nil
	}

	// Case 1: Target `out` is *map[string]string. Populate the map directly.
	if m, ok := out.(*map[string]string); ok {
		if *m == nil { // Ensure the map is initialized if it's a nil pointer.
			*m = make(map[string]string)
		}
		// Iterate over all arguments and add them to the map.
		// fasthttp.Args.VisitAll calls the function for each key-value pair.
		// If a key has multiple values, VisitAll typically processes each occurrence,
		// so the map will end up with the last value for that key.
		args.VisitAll(func(key, value []byte) {
			(*m)[string(key)] = string(value)
		})
		return nil
	}

	// Case 2: Target `out` is a pointer to a struct. Use reflection to populate fields.
	val := reflect.ValueOf(out) // `out` is already validated as a non-nil pointer.
	elem := val.Elem()          // Get the struct value itself.

	// Ensure `elem` is indeed a struct.
	if elem.Kind() != reflect.Struct {
		unsupportedTypeErr := fmt.Errorf("binding from %s to type %T is not implemented; target must be *map[string]string or a pointer to a struct", source, out)
		return NewHTTPError(StatusInternalServerError, "Internal server error: Invalid target type for argument binding.").WithInternal(unsupportedTypeErr)
	}

	typ := elem.Type() // Get the type information of the struct.
	numFields := elem.NumField()

	// Iterate over each field of the struct.
	for i := 0; i < numFields; i++ {
		fieldStructType := typ.Field(i)  // reflect.StructField (metadata about the field).
		fieldReflectVal := elem.Field(i) // reflect.Value (the actual field value we can set).

		// Skip unexported fields or fields that cannot be set.
		if !fieldReflectVal.CanSet() {
			continue
		}

		// Determine the name to look for in `args` based on the struct tag or field name.
		tagValue := fieldStructType.Tag.Get(tagKey) // Get the tag (e.g., `query:"name,omitempty"`).
		lookupName := ""
		if tagValue != "" && tagValue != "-" { // If tag exists and is not "-", use it.
			// Tag might have options like ",omitempty". Take only the name part.
			lookupName = strings.Split(tagValue, ",")[0]
		}
		if lookupName == "" { // If tag is missing or was just ",", use the field's actual name.
			lookupName = fieldStructType.Name
		}
		if lookupName == "-" { // If tag is "-", explicitly skip this field for binding.
			continue
		}

		// Get argument values from `args` based on `lookupName`.
		var argStrValues []string
		if fieldReflectVal.Kind() == reflect.Slice {
			// If the struct field is a slice, attempt to get multiple values for the key.
			// `fasthttp.Args.PeekMulti` returns a slice of byte slices.
			byteValues := args.PeekMulti(lookupName)
			if len(byteValues) == 0 {
				continue // No values found for this key.
			}
			argStrValues = make([]string, len(byteValues))
			for i, bv := range byteValues {
				argStrValues[i] = string(bv)
			}
		} else {
			// If the struct field is not a slice, get a single value.
			// `fasthttp.Args.Peek` returns the first value for the key.
			argValueBytes := args.Peek(lookupName)
			if argValueBytes == nil {
				continue // No value found for this key.
			}
			argStrValues = []string{string(argValueBytes)}
		}

		// Set the struct field's value using the retrieved string(s).
		if err := c.setStructField(fieldReflectVal, fieldStructType.Type, argStrValues); err != nil {
			// If setting the field fails (e.g., parsing error), return an HTTPError.
			bindingErr := fmt.Errorf("error binding %s parameter '%s' to field '%s' (type %s): %w",
				source, lookupName, fieldStructType.Name, fieldStructType.Type.String(), err)
			return NewHTTPError(StatusBadRequest, bindingErr.Error()).WithInternal(err)
		}
	}
	return nil
}

// setStructField is an internal helper that populates a single struct field (`fieldVal`
// of type `fieldType`) with one or more string values (`strValues`) obtained from
// the request data (query/form). It handles both scalar and slice fields, as well as pointers.
func (c *Context) setStructField(fieldVal reflect.Value, fieldType reflect.Type, strValues []string) error {
	if len(strValues) == 0 {
		return nil // No values to set.
	}

	// Handle pointer fields: if the field is a pointer, dereference it or create a new instance.
	if fieldType.Kind() == reflect.Ptr {
		// Special case for non-string pointers: if the input string is empty,
		// leave the pointer as nil. This helps distinguish "not provided" from "provided as zero/false".
		// For *string, an empty input string should result in a pointer to an empty string.
		if len(strValues) == 1 && strValues[0] == "" && fieldType.Elem().Kind() != reflect.String {
			// Field is a pointer, input is a single empty string, and underlying type is not string.
			// Keep the pointer nil (or its current value if already set).
			return nil
		}
		// If the pointer field is nil, create a new instance of the element type.
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem()))
		}
		// Dereference the pointer to set the underlying value.
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem() // Update fieldType to the element type.
	}

	// Handle slice fields.
	if fieldType.Kind() == reflect.Slice {
		sliceElemType := fieldType.Elem() // Get the type of elements in the slice.
		// Create a new slice of the correct type and length.
		newSlice := reflect.MakeSlice(fieldType, len(strValues), len(strValues))
		for i, strVal := range strValues {
			// Set each element of the new slice by parsing its string value.
			if err := c.setScalarField(newSlice.Index(i), sliceElemType, strVal); err != nil {
				return fmt.Errorf("error setting slice element %d from value '%s': %w", i, strVal, err)
			}
		}
		fieldVal.Set(newSlice) // Set the struct field to the newly populated slice.
		return nil
	}

	// Handle scalar (non-slice, non-pointer at this stage) fields.
	// Only one string value is expected for scalar fields.
	return c.setScalarField(fieldVal, fieldType, strValues[0])
}

// setScalarField is an internal helper that sets a scalar (non-slice) field (`fieldVal`
// of `fieldType`) from a single string value (`strValue`). It handles parsing for
// basic types like string, int, uint, bool, float, and `time.Time`.
// It also correctly handles setting pointer-to-scalar types if `fieldVal` and `fieldType`
// have already been dereferenced by `setStructField`.
func (c *Context) setScalarField(fieldVal reflect.Value, fieldType reflect.Type, strValue string) error {
	// If fieldType is still a pointer (e.g., for slice of pointers like []*int),
	// dereference it for setting the underlying scalar value.
	if fieldType.Kind() == reflect.Ptr {
		// Similar to setStructField: for non-string pointers, empty input means nil pointer.
		if strValue == "" && fieldType.Elem().Kind() != reflect.String {
			if fieldVal.CanSet() && fieldVal.IsNil() { // Only if it's settable and currently nil.
				return nil
			}
			// If it's already non-nil, or not settable (shouldn't happen here), error or skip.
			// For simplicity, if it's a pointer and we get here with empty string for non-string type,
			// it might be an error unless it was intended to be skipped earlier.
			// However, setStructField's logic should prevent this path mostly.
		}
		// If the pointer field (element of a slice of pointers) is nil, create new instance.
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem()))
		}
		fieldVal = fieldVal.Elem()   // Dereference.
		fieldType = fieldType.Elem() // Update to element type.
	}

	// Special handling for time.Time.
	if fieldType == reflect.TypeOf(time.Time{}) {
		if !fieldVal.CanSet() { // Should not happen if CanSet was checked by caller.
			return fmt.Errorf("internal error: field of type time.Time cannot be set")
		}
		if strValue == "" { // Cannot parse an empty string into a time.
			return fmt.Errorf("cannot parse empty string as time.Time for field")
		}
		// Try parsing in RFC3339 format first.
		parsedTimeRFC3339, errRFC3339 := time.Parse(time.RFC3339, strValue)
		if errRFC3339 == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeRFC3339))
			return nil
		}
		// If RFC3339 fails, try parsing in "YYYY-MM-DD" date format.
		parsedTimeDate, errDate := time.Parse("2006-01-02", strValue)
		if errDate == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeDate))
			return nil
		}
		// If both parsing attempts fail.
		return fmt.Errorf("cannot parse '%s' as time.Time (tried RFC3339: %v; and YYYY-MM-DD: %v)", strValue, errRFC3339, errDate)
	}

	// Handle other scalar types.
	switch fieldType.Kind() {
	case reflect.String:
		fieldVal.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strValue == "" { // Cannot parse empty string to integer.
			return fmt.Errorf("cannot parse empty string as integer (type %s)", fieldType.Kind())
		}
		i, err := strconv.ParseInt(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" { // Cannot parse empty string to unsigned integer.
			return fmt.Errorf("cannot parse empty string as unsigned integer (type %s)", fieldType.Kind())
		}
		u, err := strconv.ParseUint(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as unsigned integer (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetUint(u)
	case reflect.Bool:
		if strValue == "" { // Cannot parse empty string to boolean.
			return fmt.Errorf("cannot parse empty string as boolean")
		}
		b, err := strconv.ParseBool(strValue)
		if err != nil {
			// Allow common alternatives for boolean like "on"/"off", "yes"/"no".
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b, err = true, nil
			} else if lowerVal == "off" || lowerVal == "no" {
				b, err = false, nil
			}
			if err != nil { // If still not parsable after checking alternatives.
				return fmt.Errorf("cannot parse '%s' as boolean: %w", strValue, err)
			}
		}
		fieldVal.SetBool(b)
	case reflect.Float32, reflect.Float64:
		if strValue == "" { // Cannot parse empty string to float.
			return fmt.Errorf("cannot parse empty string as float (type %s)", fieldType.Kind())
		}
		f, err := strconv.ParseFloat(strValue, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as float (type %s): %w", strValue, fieldType.Kind(), err)
		}
		fieldVal.SetFloat(f)
	default:
		// This type is not supported for reflection-based binding from query/form strings.
		return fmt.Errorf("unsupported scalar field type '%s' for form/query string binding", fieldType.Kind())
	}
	return nil
}
