package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// unmarshalWire fills target, which must be a non-nil pointer, from the JSON object in data.
// Each field is keyed by its wire tag, or by the name in its json tag when no wire tag is set.
// Fields tagged "-" and unexported fields are skipped. Embedded structs contribute their fields.
func unmarshalWire(data []byte, target any) error {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("wire: target must be a non-nil pointer, got %T", target)
	}
	if err := decodeWireValue(data, value.Elem()); err != nil {
		return fmt.Errorf("wire: decode value: %w", err)
	}
	return nil
}

func decodeWireValue(data json.RawMessage, value reflect.Value) error {
	if value.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			value.SetZero()
			return nil
		}
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
		}
		return decodeWireValue(data, value.Elem())
	}
	if value.CanAddr() && value.Addr().Type().Implements(reflect.TypeFor[json.Unmarshaler]()) {
		if err := json.Unmarshal(data, value.Addr().Interface()); err != nil {
			return fmt.Errorf("invoke JSON unmarshaler: %w", err)
		}
		return nil
	}

	switch value.Kind() {
	case reflect.Struct:
		return decodeWireStruct(data, value)
	case reflect.Slice:
		return decodeWireSlice(data, value)
	default:
		if err := json.Unmarshal(data, value.Addr().Interface()); err != nil {
			return fmt.Errorf("decode %s: %w", value.Type(), err)
		}
		return nil
	}
}

func decodeWireStruct(data json.RawMessage, value reflect.Value) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("decode %s object: %w", value.Type(), err)
	}
	return decodeWireStructFields(object, value)
}

func decodeWireStructFields(object map[string]json.RawMessage, value reflect.Value) error {
	valueType := value.Type()
	for index := range value.NumField() {
		fieldType := valueType.Field(index)
		fieldValue := value.Field(index)
		if fieldType.Anonymous && fieldValue.Kind() == reflect.Pointer {
			return fmt.Errorf("embedded pointer field %s is not supported", fieldType.Type)
		}
		if fieldType.Anonymous && fieldValue.Kind() == reflect.Struct {
			if err := decodeWireStructFields(object, fieldValue); err != nil {
				return err
			}
			continue
		}
		if fieldType.PkgPath != "" {
			continue
		}
		if err := decodeWireField(object, fieldType, fieldValue); err != nil {
			return err
		}
	}
	return nil
}

func decodeWireField(
	object map[string]json.RawMessage,
	fieldType reflect.StructField,
	fieldValue reflect.Value,
) error {
	name := fieldType.Tag.Get("wire")
	if name == "" {
		name, _, _ = strings.Cut(fieldType.Tag.Get("json"), ",")
	}
	if name == "" || name == "-" {
		return nil
	}
	raw, ok := object[name]
	if !ok {
		return nil
	}
	if err := decodeWireValue(raw, fieldValue); err != nil {
		return fmt.Errorf("decode field %s: %w", fieldType.Name, err)
	}
	return nil
}

func decodeWireSlice(data json.RawMessage, value reflect.Value) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.SetZero()
		return nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(data, &elements); err != nil {
		return fmt.Errorf("decode %s array: %w", value.Type(), err)
	}
	decoded := reflect.MakeSlice(value.Type(), len(elements), len(elements))
	for index, element := range elements {
		if err := decodeWireValue(element, decoded.Index(index)); err != nil {
			return fmt.Errorf("decode %s element %d: %w", value.Type(), index, err)
		}
	}
	value.Set(decoded)
	return nil
}
