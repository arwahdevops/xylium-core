package xylium

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
)

type HandlerWithParamsFunc[P any] func(c *Context, params P) error

func RegisterParams[P any](r *Router, method string, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	wrappedHandler := func(c *Context) error {
		var params P
		paramsValue := reflect.ValueOf(&params).Elem()
		paramsType := paramsValue.Type()

		if paramsType.Kind() != reflect.Struct {
			err := fmt.Errorf("type P in RegisterParams must be a struct, got %s", paramsType.Kind())
			c.Logger().Errorf("Internal configuration error for generic handler: %v", err)
			return NewHTTPError(http.StatusInternalServerError, "Generic handler misconfiguration").WithInternal(err)
		}

		for i := 0; i < paramsValue.NumField(); i++ {
			fieldStruct := paramsType.Field(i)
			fieldValue := paramsValue.Field(i)

			if !fieldValue.CanSet() {
				continue
			}

			pathTag := fieldStruct.Tag.Get("path")
			if pathTag != "" && pathTag != "-" {
				paramName := strings.Split(pathTag, ",")[0]
				rawParamValue := c.Param(paramName)

				if err := setStructFieldFromStrings(fieldValue, fieldStruct.Type, []string{rawParamValue}, "path parameter", paramName, ""); err != nil {
					return NewHTTPError(http.StatusBadRequest,
						fmt.Sprintf("Invalid path parameter '%s' for field '%s': %v", paramName, fieldStruct.Name, err)).WithInternal(err)
				}
				continue
			}

			queryTag := fieldStruct.Tag.Get("query")
			if queryTag != "" && queryTag != "-" {
				paramName := strings.Split(queryTag, ",")[0]
				defaultValue := fieldStruct.Tag.Get("default")

				var rawParamValues []string
				queryArgs := c.Ctx.QueryArgs()
				byteValues := queryArgs.PeekMulti(paramName)

				if len(byteValues) > 0 {
					rawParamValues = make([]string, len(byteValues))
					for k, bv := range byteValues {
						rawParamValues[k] = string(bv)
					}
				} else if defaultValue != "" {
					rawParamValues = []string{defaultValue}
				}

				if err := setStructFieldFromStrings(fieldValue, fieldStruct.Type, rawParamValues, "query parameter", paramName, defaultValue); err != nil {
					return NewHTTPError(http.StatusBadRequest,
						fmt.Sprintf("Invalid query parameter '%s' for field '%s': %v", paramName, fieldStruct.Name, err)).WithInternal(err)
				}
			}
		}

		if enableValidation {
			currentValidator := GetValidator()
			if err := currentValidator.Struct(params); err != nil {
				return formatValidationErrors(err, "Parameter")
			}
		}
		return handler(c, params)
	}
	r.addRoute(method, path, wrappedHandler, routeMiddleware...)
}

func RegisterGETParams[P any](r *Router, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	RegisterParams(r, http.MethodGet, path, handler, enableValidation, routeMiddleware...)
}

func RegisterPOSTParams[P any](r *Router, path string, handler HandlerWithParamsFunc[P], enableValidation bool, routeMiddleware ...Middleware) {
	RegisterParams(r, http.MethodPost, path, handler, enableValidation, routeMiddleware...)
}

func setStructFieldFromStrings(fieldValue reflect.Value, fieldType reflect.Type, strValues []string, sourceDesc string, paramName string, defaultValue string) error {
	isPtrField := fieldType.Kind() == reflect.Ptr
	underlyingFieldType := fieldType
	if isPtrField {
		underlyingFieldType = fieldType.Elem()
	}

	if len(strValues) == 0 {
		if isPtrField && fieldValue.IsNil() {
			return nil
		}
		if underlyingFieldType.Kind() == reflect.String {
			targetVal := fieldValue
			if isPtrField {
				if targetVal.IsNil() {
					targetVal.Set(reflect.New(underlyingFieldType))
				}
				targetVal = targetVal.Elem()
			}
			targetVal.SetString("")
			return nil
		}
		if underlyingFieldType.Kind() == reflect.Slice {
			targetVal := fieldValue
			if isPtrField {
				if targetVal.IsNil() {
					targetVal.Set(reflect.New(underlyingFieldType))
				}
				targetVal = targetVal.Elem()
			}
			targetVal.Set(reflect.MakeSlice(underlyingFieldType, 0, 0))
			return nil
		}
		return nil
	}

	if isPtrField && fieldValue.IsNil() {
		if len(strValues) == 1 && strValues[0] == "" &&
			underlyingFieldType.Kind() != reflect.String &&
			underlyingFieldType.Kind() != reflect.Slice {
			return nil
		}
		fieldValue.Set(reflect.New(underlyingFieldType))
	}

	targetValueToSet := fieldValue
	if isPtrField {
		targetValueToSet = fieldValue.Elem()
	}

	if targetValueToSet.Kind() == reflect.Slice {
		sliceElemType := targetValueToSet.Type().Elem()
		if len(strValues) == 0 {
			targetValueToSet.Set(reflect.MakeSlice(targetValueToSet.Type(), 0, 0))
			return nil
		}
		newSlice := reflect.MakeSlice(targetValueToSet.Type(), len(strValues), len(strValues))
		for i, strVal := range strValues {
			elemVal := newSlice.Index(i)
			if err := setScalar(elemVal, sliceElemType, strVal, sourceDesc, paramName); err != nil {
				return fmt.Errorf("error setting slice element %d for %s '%s' from value '%s': %w", i, sourceDesc, paramName, strVal, err)
			}
		}
		targetValueToSet.Set(newSlice)
		return nil
	}

	if len(strValues) > 0 {
		return setScalar(targetValueToSet, targetValueToSet.Type(), strValues[0], sourceDesc, paramName)
	}
	return nil
}

func setScalar(scalarValue reflect.Value, scalarType reflect.Type, strValue string, sourceDesc string, paramName string) error {
	isPtrElement := scalarType.Kind() == reflect.Ptr
	actualScalarType := scalarType
	targetScalarValue := scalarValue

	if isPtrElement {
		actualScalarType = scalarType.Elem()
		if strValue == "" && actualScalarType.Kind() != reflect.String {
			if targetScalarValue.CanSet() && targetScalarValue.IsNil() {
				return nil
			}
		}
		if targetScalarValue.IsNil() {
			targetScalarValue.Set(reflect.New(actualScalarType))
		}
		targetScalarValue = targetScalarValue.Elem()
	}

	if actualScalarType == reflect.TypeOf(time.Time{}) {
		if strValue == "" {
			if isPtrElement {
				return nil
			}
			return errors.New("cannot parse empty string as time.Time")
		}
		formats := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}
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

	if strValue == "" && actualScalarType.Kind() != reflect.String && !isPtrElement {
		return nil
	}

	switch actualScalarType.Kind() {
	case reflect.String:
		targetScalarValue.SetString(strValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(strValue, 10, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as integer for %s '%s': %w", strValue, sourceDesc, paramName, err)
		}
		targetScalarValue.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(strValue, 10, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as unsigned integer for %s '%s': %w", strValue, sourceDesc, paramName, err)
		}
		targetScalarValue.SetUint(u)
	case reflect.Bool:
		b, err := strconv.ParseBool(strValue)
		if err != nil {
			lowerVal := strings.ToLower(strValue)
			if lowerVal == "on" || lowerVal == "yes" {
				b, err = true, nil
			} else if lowerVal == "off" || lowerVal == "no" {
				b, err = false, nil
			}
			if err != nil {
				return fmt.Errorf("failed to parse '%s' as boolean for %s '%s': %w", strValue, sourceDesc, paramName, err)
			}
		}
		targetScalarValue.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(strValue, actualScalarType.Bits())
		if err != nil {
			return fmt.Errorf("failed to parse '%s' as float for %s '%s': %w", strValue, sourceDesc, paramName, err)
		}
		targetScalarValue.SetFloat(f)
	default:
		return fmt.Errorf("unsupported field type '%s' for %s '%s' binding", actualScalarType.Kind(), sourceDesc, paramName)
	}
	return nil
}

func formatValidationErrors(err error, contextName string) *HTTPError {
	if vErrs, ok := err.(validator.ValidationErrors); ok {
		errFields := make(map[string]string)
		for _, fe := range vErrs {
			fieldName := fe.Field()
			errMsg := fmt.Sprintf("field '%s' failed validation on tag '%s'", fieldName, fe.Tag())
			if fe.Param() != "" {
				errMsg += fmt.Sprintf(" with value '%s'", fe.Param())
			}
			if fe.Value() != nil && fe.Value() != "" {
				errMsg += fmt.Sprintf(" (actual value: '%v')", fe.Value())
			}
			errFields[fieldName] = errMsg
		}
		title := fmt.Sprintf("%s validation failed.", contextName)
		return NewHTTPError(http.StatusBadRequest, M{"message": title, "details": errFields}).WithInternal(err)
	}
	return NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("%s validation processing error.", contextName)).WithInternal(err)
}
