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
	if err := c.Bind(out); err != nil {
		return err
	}

	currentValidator := GetValidator()
	if err := currentValidator.Struct(out); err != nil {
		if vErrs, ok := err.(validator.ValidationErrors); ok {
			errFields := make(map[string]string)
			for _, fe := range vErrs {
				// MENGGUNAKAN fe.Namespace() untuk mendapatkan path field yang lengkap
				// Contoh: Jika 'out' adalah *ValidationStruct, dan error ada di Nested.InnerField,
				// maka fe.Namespace() akan menghasilkan "ValidationStruct.Nested.InnerField".
				fieldName := fe.Namespace()

				// Opsional: Jika Anda ingin menghapus nama struct terluar dari namespace
				// agar kuncinya menjadi "Nested.InnerField" bukan "ValidationStruct.Nested.InnerField"
				// Ini bisa dilakukan jika 'out' adalah pointer ke struct dan kita tahu tipenya.
				outType := reflect.TypeOf(out)
				if outType.Kind() == reflect.Ptr {
					baseTypeName := outType.Elem().Name() // Nama struct tanpa package, misal "ValidationStruct"
					prefixToRemove := baseTypeName + "."
					if strings.HasPrefix(fieldName, prefixToRemove) {
						fieldName = fieldName[len(prefixToRemove):] // Menghasilkan "Nested.InnerField"
					}
				}
				// Jika Anda tidak melakukan pemotongan di atas, kunci akan tetap
				// "ValidationStruct.Nested.InnerField". Sesuaikan tes Anda dengan ini.
				// Untuk contoh ini, mari kita coba dengan pemotongan.

				errMsg := fmt.Sprintf("validation failed on tag '%s'", fe.Tag())
				if fe.Param() != "" {
					errMsg += fmt.Sprintf(" (param: %s)", fe.Param())
				}
				errFields[fieldName] = errMsg
			}
			return NewHTTPError(StatusBadRequest, M{"message": "Validation failed.", "details": errFields}).WithInternal(err)
		}
		return NewHTTPError(StatusBadRequest, "Validation processing error occurred.").WithInternal(err)
	}
	return nil
}

// Bind attempts to bind request data to the `out` interface.
// If `out` implements the `XBind` interface, its `Bind` method is called.
// Otherwise, it falls back to reflection-based binding (`bindWithReflection`)
// which considers Content-Type and HTTP method.
// - `out` must be a pointer to a struct or `*map[string]string` (for reflection-based form/query binding).
// Returns an `*xylium.HTTPError` if binding fails, or nil on success.
func (c *Context) Bind(out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return NewHTTPError(StatusInternalServerError,
			fmt.Sprintf("Binding target 'out' must be a non-nil pointer, got %T", out)).WithInternal(errors.New("invalid binding target type"))
	}

	if binder, ok := out.(XBind); ok {
		return binder.Bind(c)
	}
	return c.bindWithReflection(out)
}

// bindWithReflection handles the reflection-based binding logic if a custom XBind is not implemented.
// It binds based on Content-Type (JSON, XML, Form) or query parameters for GET/DELETE/HEAD.
func (c *Context) bindWithReflection(out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return NewHTTPError(StatusInternalServerError, "Internal error: bindWithReflection called with invalid target.").WithInternal(errors.New("invalid target for reflection bind"))
	}

	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		return nil
	}

	contentType := c.ContentType()

	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		if c.queryArgs == nil {
			c.queryArgs = c.Ctx.QueryArgs()
		}
		return c.bindDataFromArgs(out, c.queryArgs, "query parameters", "query")
	}

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		body := c.Body()
		if len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data provided.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		body := c.Body()
		if len(body) == 0 {
			return nil
		}
		if err := xml.Unmarshal(body, out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data provided.").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		if c.formArgs == nil {
			_ = c.Ctx.PostArgs()
			c.formArgs = c.Ctx.PostArgs()
		}
		return c.bindDataFromArgs(out, c.formArgs, "form data", "form")
	default:
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for binding: "+contentType)
		}
	}
	return nil
}

// bindDataFromArgs is an internal helper to bind data from `fasthttp.Args` (query or form)
// into the `out` interface (either `*map[string]string` or a pointer to a struct).
func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	if args == nil || args.Len() == 0 {
		return nil
	}

	if m, ok := out.(*map[string]string); ok {
		if *m == nil {
			*m = make(map[string]string)
		}
		args.VisitAll(func(key, value []byte) {
			(*m)[string(key)] = string(value)
		})
		return nil
	}

	val := reflect.ValueOf(out)
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return NewHTTPError(StatusNotImplemented,
			fmt.Sprintf("Binding from %s to type %T is not implemented. Supported: *map[string]string or a pointer to a struct.", source, out))
	}

	typ := elem.Type()
	numFields := elem.NumField()

	for i := 0; i < numFields; i++ {
		field := typ.Field(i)
		fieldVal := elem.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		tagValue := field.Tag.Get(tagKey)
		formFieldName := ""
		if tagValue != "" && tagValue != "-" {
			formFieldName = strings.Split(tagValue, ",")[0]
		}
		if formFieldName == "" {
			formFieldName = field.Name
		}
		if formFieldName == "-" {
			continue
		}

		var argValues []string
		if fieldVal.Kind() == reflect.Slice {
			byteValues := args.PeekMulti(formFieldName)
			if len(byteValues) == 0 {
				continue
			}
			argValues = make([]string, len(byteValues))
			for i, bv := range byteValues {
				argValues[i] = string(bv)
			}
		} else {
			argValueBytes := args.Peek(formFieldName)
			if argValueBytes == nil {
				continue
			}
			argValues = []string{string(argValueBytes)}
		}

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
func (c *Context) setStructField(fieldVal reflect.Value, fieldType reflect.Type, strValues []string) error {
	if len(strValues) == 0 {
		return nil
	}

	if fieldType.Kind() == reflect.Ptr {
		if len(strValues) == 1 && strValues[0] == "" && fieldType.Elem().Kind() != reflect.String {
			return nil
		}
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem()))
		}
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem()
	}

	if fieldType.Kind() == reflect.Slice {
		sliceElemType := fieldType.Elem()
		newSlice := reflect.MakeSlice(fieldType, len(strValues), len(strValues))
		for i, strVal := range strValues {
			if err := c.setScalarField(newSlice.Index(i), sliceElemType, strVal); err != nil {
				return fmt.Errorf("error setting slice element %d from value '%s': %w", i, strVal, err)
			}
		}
		fieldVal.Set(newSlice)
		return nil
	}
	return c.setScalarField(fieldVal, fieldType, strValues[0])
}

// setScalarField sets a scalar (non-slice) field (`fieldVal` of `fieldType`)
// from a single string value (`strValue`).
func (c *Context) setScalarField(fieldVal reflect.Value, fieldType reflect.Type, strValue string) error {
	if fieldType.Kind() == reflect.Ptr {
		if strValue == "" && fieldType.Elem().Kind() != reflect.String {
			if fieldVal.CanSet() && fieldVal.IsNil() {
				return nil
			}
		}
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldType.Elem()))
		}
		fieldVal = fieldVal.Elem()
		fieldType = fieldType.Elem()
	}

	if fieldType == reflect.TypeOf(time.Time{}) {
		if !fieldVal.CanSet() {
			return fmt.Errorf("field of type time.Time cannot be set")
		}
		if strValue == "" {
			return fmt.Errorf("cannot parse empty string as time.Time")
		}
		parsedTimeRFC3339, errRFC3339 := time.Parse(time.RFC3339, strValue)
		if errRFC3339 == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeRFC3339))
			return nil
		}
		parsedTimeDate, errDate := time.Parse("2006-01-02", strValue)
		if errDate == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeDate))
			return nil
		}
		return fmt.Errorf("cannot parse '%s' as time.Time (tried RFC3339: %v; tried YYYY-MM-DD: %v)", strValue, errRFC3339, errDate)
	}

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
		b, err := strconv.ParseBool(strValue)
		if err != nil {
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
