package schema

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

type encoderFunc func(reflect.Value) (string, error)

// Encoder encodes values from a struct into url.Values.
type Encoder struct {
	cache          *cache
	regenc         map[reflect.Type]encoderFunc
	useTextMarshal bool
}

// NewEncoder returns a new Encoder with defaults.
func NewEncoder() *Encoder {
	return &Encoder{cache: newCache(), regenc: make(map[reflect.Type]encoderFunc)}
}

// UseTextMarshal controls the behaviour when the decoder encounters values
// that implements encoding.TextMarshaler.
// If u is true and such value encountered, MarshalText will be used.
//
// To preserve backwards compatibility, the default value is false.
func (e *Encoder) UseTextMarshal(u bool) {
	e.useTextMarshal = u
}

// Encode encodes a struct into map[string][]string.
//
// Intended for use with url.Values.
func (e *Encoder) Encode(src interface{}, dst map[string][]string) error {
	v := reflect.ValueOf(src)

	return e.encode(v, dst)
}

// RegisterEncoder registers a converter for encoding a custom type.
func (e *Encoder) RegisterEncoder(value interface{}, encoder func(reflect.Value) string) {
	e.regenc[reflect.TypeOf(value)] = func(v reflect.Value) (string, error) {
		return encoder(v), nil
	}
}

// SetAliasTag changes the tag used to locate custom field aliases.
// The default tag is "schema".
func (e *Encoder) SetAliasTag(tag string) {
	e.cache.tag = tag
}

// isValidStructPointer test if input value is a valid struct pointer.
func isValidStructPointer(v reflect.Value) bool {
	return v.Type().Kind() == reflect.Ptr && v.Elem().IsValid() && v.Elem().Type().Kind() == reflect.Struct
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Func:
	case reflect.Map, reflect.Slice:
		return v.IsNil() || v.Len() == 0
	case reflect.Array:
		z := true
		for i := 0; i < v.Len(); i++ {
			z = z && isZero(v.Index(i))
		}
		return z
	case reflect.Struct:
		z := true
		for i := 0; i < v.NumField(); i++ {
			z = z && isZero(v.Field(i))
		}
		return z
	}
	// Compare other types directly:
	z := reflect.Zero(v.Type())
	return v.Interface() == z.Interface()
}

func (e *Encoder) encode(v reflect.Value, dst map[string][]string) error {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return errors.New("schema: interface must be a struct")
	}
	t := v.Type()

	errors := MultiError{}

	for i := 0; i < v.NumField(); i++ {
		name, opts := fieldAlias(t.Field(i), e.cache.tag)
		if name == "-" {
			continue
		}

		// Encode struct pointer types if the field is a valid pointer and a struct.
		if isValidStructPointer(v.Field(i)) {
			e.encode(v.Field(i).Elem(), dst)
			continue
		}

		encFunc := e.typeEncoder(v.Field(i).Type(), e.regenc)

		// Encode non-slice types and custom implementations immediately.
		if encFunc != nil {
			value, err := encFunc(v.Field(i))
			if err != nil {
				errors[v.Field(i).Type().String()] = fmt.Errorf("schema: failed to encode field: %s", err)
			}
			if opts.Contains("omitempty") && isZero(v.Field(i)) {
				continue
			}

			dst[name] = append(dst[name], value)
			continue
		}

		if v.Field(i).Type().Kind() == reflect.Struct {
			e.encode(v.Field(i), dst)
			continue
		}

		if v.Field(i).Type().Kind() == reflect.Slice {
			encFunc = e.typeEncoder(v.Field(i).Type().Elem(), e.regenc)
		}

		if encFunc == nil {
			errors[v.Field(i).Type().String()] = fmt.Errorf("schema: encoder not found for %v", v.Field(i))
			continue
		}

		// Encode a slice.
		if v.Field(i).Len() == 0 && opts.Contains("omitempty") {
			continue
		}

		dst[name] = []string{}
		for j := 0; j < v.Field(i).Len(); j++ {
			value, err := encFunc(v.Field(i).Index(j))
			if err != nil {
				errors[v.Field(i).Type().String()] = fmt.Errorf("schema: failed to encode slice element: %s", err)
				continue
			}
			dst[name] = append(dst[name], value)
		}
	}

	if len(errors) > 0 {
		return errors
	}
	return nil
}

func (e *Encoder) typeEncoder(t reflect.Type, reg map[reflect.Type]encoderFunc) encoderFunc {
	if f, ok := reg[t]; ok {
		return f
	}

	if e.useTextMarshal && t.Implements(reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()) {
		return encodeTextMarshaler
	}

	switch t.Kind() {
	case reflect.Bool:
		return encodeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return encodeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return encodeUint
	case reflect.Float32:
		return encodeFloat32
	case reflect.Float64:
		return encodeFloat64
	case reflect.Ptr:
		f := e.typeEncoder(t.Elem(), reg)
		return func(v reflect.Value) (string, error) {
			if v.IsNil() {
				return "null", nil
			}
			return f(v.Elem())
		}
	case reflect.String:
		return encodeString
	default:
		return nil
	}
}

func encodeTextMarshaler(v reflect.Value) (string, error) {
	text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
	if err != nil {
		return "", err
	}

	return string(text), nil
}

func encodeBool(v reflect.Value) (string, error) {
	return strconv.FormatBool(v.Bool()), nil
}

func encodeInt(v reflect.Value) (string, error) {
	return strconv.FormatInt(int64(v.Int()), 10), nil
}

func encodeUint(v reflect.Value) (string, error) {
	return strconv.FormatUint(uint64(v.Uint()), 10), nil
}

func encodeFloat(v reflect.Value, bits int) (string, error) {
	return strconv.FormatFloat(v.Float(), 'f', 6, bits), nil
}

func encodeFloat32(v reflect.Value) (string, error) {
	return encodeFloat(v, 32)
}

func encodeFloat64(v reflect.Value) (string, error) {
	return encodeFloat(v, 64)
}

func encodeString(v reflect.Value) (string, error) {
	return v.String(), nil
}
