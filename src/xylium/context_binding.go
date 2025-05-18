package xylium

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

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
				fieldName := fe.Field()
				errFields[fieldName] = fmt.Sprintf("validation failed on '%s' tag", fe.Tag())
				if fe.Param() != "" {
					errFields[fieldName] += fmt.Sprintf(" (param: %s)", fe.Param())
				}
			}
			return NewHTTPError(StatusBadRequest, map[string]interface{}{"message": "Validation failed", "details": errFields}).WithInternal(err)
		}
		return NewHTTPError(StatusBadRequest, "Validation processing error").WithInternal(err)
	}
	return nil
}

// Bind attempts to bind request data to the `out` interface.
func (c *Context) Bind(out interface{}) error {
	if c.Ctx.Request.Header.ContentLength() == 0 &&
		c.Method() != MethodGet && c.Method() != MethodDelete && c.Method() != MethodHead {
		return nil
	}

	contentType := c.ContentType()

	if c.Method() == MethodGet || c.Method() == MethodDelete || c.Method() == MethodHead {
		if c.queryArgs == nil {
			c.queryArgs = c.Ctx.QueryArgs()
		}
		return c.bindDataFromArgs(out, c.queryArgs, "query", "query")
	}

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		if len(c.Body()) == 0 { return nil }
		if err := json.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid JSON data").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/xml"), strings.HasPrefix(contentType, "text/xml"):
		if len(c.Body()) == 0 { return nil }
		if err := xml.Unmarshal(c.Body(), out); err != nil {
			return NewHTTPError(StatusBadRequest, "Invalid XML data").WithInternal(err)
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"),
		strings.HasPrefix(contentType, "multipart/form-data"):
		if c.formArgs == nil {
			_ = c.Ctx.PostArgs()
			c.formArgs = c.Ctx.PostArgs()
		}
		return c.bindDataFromArgs(out, c.formArgs, "form", "form")
	default:
		if len(c.Body()) > 0 {
			return NewHTTPError(StatusUnsupportedMediaType, "Unsupported Content-Type for binding: "+contentType)
		}
	}
	return nil
}

func (c *Context) bindDataFromArgs(out interface{}, args *fasthttp.Args, source string, tagKey string) error {
	if args == nil {
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
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Struct {
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

	for i := 0; i < numFields; i++ {
		field := typ.Field(i)
		fieldVal := elem.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		tagValue := field.Tag.Get(tagKey)
		if tagValue == "" || tagValue == "-" {
			tagValue = field.Name
		}

		formFieldName := strings.Split(tagValue, ",")[0]
		if formFieldName == "" {
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
				fmt.Sprintf("Error binding %s parameter '%s' to field '%s': %v", source, formFieldName, field.Name, err)).WithInternal(err)
		}
	}
	return nil
}

func (c *Context) setStructField(fieldVal reflect.Value, fieldType reflect.Type, strValues []string) error {
	if len(strValues) == 0 {
		return nil
	}

	if fieldType.Kind() == reflect.Ptr {
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
				return fmt.Errorf("error setting slice element %d: %w", i, err)
			}
		}
		fieldVal.Set(newSlice)
		return nil
	}
	return c.setScalarField(fieldVal, fieldType, strValues[0])
}

func (c *Context) setScalarField(fieldVal reflect.Value, fieldType reflect.Type, strValue string) error {
	// Handle time.Time separately due to multiple parsing formats.
	if fieldType == reflect.TypeOf(time.Time{}) {
		if !fieldVal.CanSet() {
			return fmt.Errorf("field cannot be set (type: %s)", fieldType.String())
		}

		// Try parsing as RFC3339
		parsedTimeRFC3339, errRFC3339 := time.Parse(time.RFC3339, strValue)
		if errRFC3339 == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeRFC3339))
			return nil
		}

		// Try parsing as YYYY-MM-DD
		parsedTimeDate, errDate := time.Parse("2006-01-02", strValue)
		if errDate == nil {
			fieldVal.Set(reflect.ValueOf(parsedTimeDate))
			return nil
		}

		// If both parsing attempts fail
		return fmt.Errorf("cannot parse '%s' as time.Time (tried RFC3339 and YYYY-MM-DD): %v / %v", strValue, errRFC3339, errDate)
	}

	switch fieldType.Kind() {
	case reflect.String:
		fieldVal.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strValue == "" {
			fieldVal.SetInt(0)
			return nil
		}
		i, err := strconv.ParseInt(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as integer: %w", strValue, err)
		}
		fieldVal.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strValue == "" {
			fieldVal.SetUint(0)
			return nil
		}
		u, err := strconv.ParseUint(strValue, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as unsigned integer: %w", strValue, err)
		}
		fieldVal.SetUint(u)
	case reflect.Bool:
		if strValue == "" {
			fieldVal.SetBool(false)
			return nil
		}
		b, err := strconv.ParseBool(strValue)
		if err != nil {
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b = true
			} else if lowerVal == "off" || lowerVal == "no" {
				b = false
			} else if i, numErr := strconv.ParseInt(lowerVal, 10, 8); numErr == nil {
				b = (i == 1)
			} else {
				return fmt.Errorf("cannot parse '%s' as boolean: %w", strValue, err)
			}
		}
		fieldVal.SetBool(b)
	case reflect.Float32, reflect.Float64:
		if strValue == "" {
			fieldVal.SetFloat(0)
			return nil
		}
		f, err := strconv.ParseFloat(strValue, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse '%s' as float: %w", strValue, err)
		}
		fieldVal.SetFloat(f)
	default:
		return fmt.Errorf("unsupported scalar field type %s for form/query binding", fieldType.Kind())
	}
	return nil
}
