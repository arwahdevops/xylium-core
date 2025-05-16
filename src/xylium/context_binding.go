package xylium

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
)

// --- Data Binding and Validation ---

// BindAndValidate first binds the request data (body, query, or form) to `out`,
// then validates the populated struct `out` using the default validator.
// Returns an *HTTPError if binding or validation fails.
func (c *Context) BindAndValidate(out interface{}) error {
	if err := c.Bind(out); err != nil {
		// Bind already returns *HTTPError or nil
		return err
	}

	currentValidator := GetValidator()
	if err := currentValidator.Struct(out); err != nil {
		if vErrs, ok := err.(validator.ValidationErrors); ok {
			errFields := make(map[string]string)
			for _, fe := range vErrs {
				// Provide a more user-friendly field name if possible (e.g., JSON tag)
				fieldName := fe.Field()
				// One could potentially inspect struct tags (e.g., `json`) for a better name.
				// For now, fe.Field() is used.
				errFields[fieldName] = fmt.Sprintf("validation failed on '%s' tag", fe.Tag())
				if fe.Param() != "" {
					errFields[fieldName] += fmt.Sprintf(" (param: %s)", fe.Param())
				}
			}
			return NewHTTPError(StatusBadRequest, map[string]interface{}{"message": "Validation failed", "details": errFields}).WithInternal(err)
		}
		// Non-validator.ValidationErrors error
		return NewHTTPError(StatusBadRequest, "Validation processing error").WithInternal(err)
	}
	return nil
}

// Bind attempts to bind request data to the `out` interface.
// - For GET/DELETE: tries to bind from query parameters.
// - For POST/PUT/PATCH etc.:
//   - application/json: unmarshals JSON body.
//   - application/xml, text/xml: unmarshals XML body.
//   - application/x-www-form-urlencoded, multipart/form-data: binds from form data.
// If Content-Length is 0 for methods other than GET/DELETE, it does nothing and returns nil.
// Returns an *HTTPError if binding fails or content type is unsupported.
func (c *Context) Bind(out interface{}) error {
	// If Content-Length is 0 and method is not GET/DELETE, nothing to bind from body.
	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		return nil // Nothing to bind
	}

	contentType := c.ContentType()

	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		// For GET/DELETE/HEAD, try to bind from query parameters.
		// Initialize queryArgs if not already done.
		if c.queryArgs == nil {
			c.queryArgs = c.Ctx.QueryArgs()
		}
		return c.bindDataFromArgs(out, c.queryArgs, "query", "query")
	}

	// For other methods, bind based on Content-Type from the body or form.
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		if len(c.Body()) == 0 { return nil } // No body to unmarshal
		if err := json.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		if len(c.Body()) == 0 { return nil } // No body to unmarshal
		if err := xml.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		// Initialize formArgs if not already done. For multipart, Ctx.PostArgs() handles it.
		if c.formArgs == nil {
			// This ensures form data is parsed if it hasn't been already.
			// For multipart, MultipartForm() might need to be called first by the user if they need files,
			// but PostArgs() will still provide non-file fields.
			_ = c.Ctx.PostArgs() // The actual args are stored in c.Ctx
			c.formArgs = c.Ctx.PostArgs()
		}
		return c.bindDataFromArgs(out, c.formArgs, "form", "form")
	default:
		// If there's a body but content type is not supported for binding
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for binding: "+contentType)
		}
	}
	return nil
}

// bindDataFromArgs attempts to bind data from fasthttp.Args (query or form) into `out`.
// `source` is "query" or "form" for error messages.
// `tagKey` is the struct tag to look for (e.g., "query" or "form").
func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	if args == nil {
		return nil // No arguments to bind
	}

	// Handle map[string]string directly
	if m, ok := out.(*map[string]string); ok {
		if *m == nil {
			*m = make(map[string]string)
		}
		args.VisitAll(func(key, value []byte) {
			(*m)[string(key)] = string(value)
		})
		return nil
	}
    
	// Handle binding to struct fields
	val := reflect.ValueOf(out)
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Struct {
		// Only try to bind to map[string]string or pointer to struct
		if val.Kind() == reflect.Struct {
			return NewHTTPError(StatusInternalServerError,
				fmt.Sprintf("Binding from %s to non-pointer struct %T is not supported. Pass a pointer.", source, out))
		}
		return NewHTTPError(StatusNotImplemented,
			fmt.Sprintf("Binding from %s to type %T is not implemented. Supported: *map[string]string, *struct.", source, out))
	}

	elem := val.Elem()
	typ := elem.Type()

	numFields := elem.NumField()
	boundFields := 0

	for i := 0; i < numFields; i++ {
		field := typ.Field(i)
		fieldVal := elem.Field(i)

		if !fieldVal.CanSet() {
			continue // Skip unexported or unsettable fields
		}

		tagValue := field.Tag.Get(tagKey)
		if tagValue == "" || tagValue == "-" { // No tag or explicitly ignored
			// Fallback to field name if no tag for query/form
			// For JSON/XML, exact match or tag is usually required by unmarshallers.
			// Here, for form/query, we can be a bit more lenient or strict as per design.
			// Let's use field name as a fallback if no tag.
			tagValue = field.Name
		}
		
		formFieldName := strings.Split(tagValue, ",")[0] // Get the name part of the tag (e.g., "name" from "name,omitempty")
		if formFieldName == "" {
			continue
		}

		argValueBytes := args.Peek(formFieldName)
		if argValueBytes == nil {
			// Field not present in args, try next field
			continue
		}
		argValueStr := string(argValueBytes)

		// Set field value
		if err := c.setStructField(fieldVal, argValueStr); err != nil {
			return NewHTTPError(StatusBadRequest,
				fmt.Sprintf("Error binding %s parameter '%s' to field '%s': %v", source, formFieldName, field.Name, err)).WithInternal(err)
		}
		boundFields++
	}

	// Optional: If no fields were bound and args were present, one might consider it an error or a specific case.
	// For now, if nothing matches, it's not an error.
	// if args.Len() > 0 && boundFields == 0 {
	//  return NewHTTPError(StatusBadRequest, fmt.Sprintf("No %s parameters found to bind to %T", source, out))
	// }

	return nil
}

// setStructField converts string value to the type of the field and sets it.
// Supports basic types: string, int, int64, bool, float64.
func (c *Context) setStructField(fieldVal reflect.Value, strValue string) error {
	switch fieldVal.Kind() {
	case reflect.String:
		fieldVal.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strValue == "" { // Handle empty string for numeric types as 0 or error
			// Depending on desired behavior, either set to zero or return error.
			// For now, let's try to parse, strconv.ParseInt will error on "" for base 10.
			// If you want "" to be 0, handle it: fieldVal.SetInt(0); return nil
		}
		i, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as integer: %w", strValue, err)
		}
		fieldVal.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" {
			// similar handling as Int
		}
		u, err := strconv.ParseUint(strValue, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as unsigned integer: %w", strValue, err)
		}
		fieldVal.SetUint(u)
	case reflect.Bool:
		b, err := strconv.ParseBool(strValue)
		if err != nil {
			// Handle common boolean string representations if strconv.ParseBool is too strict
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b = true
				err = nil
			} else if lowerVal == "off" || lowerVal == "no" {
				b = false
				err = nil
			} else {
				return fmt.Errorf("cannot parse '%s' as boolean: %w", strValue, err)
			}
		}
		fieldVal.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(strValue, fieldVal.Type().Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as float: %w", strValue, err)
		}
		fieldVal.SetFloat(f)
	// TODO: Add support for slices (e.g., ?ids=1&ids=2&ids=3)
	// TODO: Add support for time.Time
	default:
		return fmt.Errorf("unsupported field type %s for form/query binding", fieldVal.Kind())
	}
	return nil
}
