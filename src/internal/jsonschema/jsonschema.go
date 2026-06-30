package jsonschema

import (
	"encoding/json"
	"reflect"
	"strings"
)

// schema is the internal draft-07 representation marshaled to JSON. Every field
// is omitempty so each shape (object, array, scalar, oneOf) emits only the keys
// it uses.
type schema struct {
	Type        string                     `json:"type,omitempty"`
	Properties  map[string]json.RawMessage `json:"properties,omitempty"`
	Required    []string                   `json:"required,omitempty"`
	Items       json.RawMessage            `json:"items,omitempty"`
	Description string                     `json:"description,omitempty"`
	OneOf       []json.RawMessage          `json:"oneOf,omitempty"`
}

// Reflect returns the draft-07 JSON Schema for the struct type of zero.
func Reflect(zero any) json.RawMessage {
	return reflectType(reflect.TypeOf(zero))
}

// OneOf wraps alternative schemas as {"oneOf": [...]}, for a command whose
// output has more than one shape.
func OneOf(alts ...json.RawMessage) json.RawMessage {
	return marshal(schema{OneOf: alts})
}

// Scalar returns a primitive-typed schema with an optional description, for a
// command whose output is not a JSON object.
func Scalar(typ, description string) json.RawMessage {
	return marshal(schema{Type: typ, Description: description})
}

// reflectType maps a Go type to its schema, recursing through pointers,
// structs, slices, and maps.
func reflectType(t reflect.Type) json.RawMessage {
	switch t.Kind() {
	case reflect.Pointer:
		return reflectType(t.Elem())
	case reflect.Struct:
		return reflectStruct(t)
	case reflect.Slice, reflect.Array:
		return marshal(schema{Type: "array", Items: reflectType(t.Elem())})
	case reflect.Map:
		return marshal(schema{Type: "object"})
	case reflect.String:
		return marshal(schema{Type: "string"})
	case reflect.Bool:
		return marshal(schema{Type: "boolean"})
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return marshal(schema{Type: "integer"})
	case reflect.Float32, reflect.Float64:
		return marshal(schema{Type: "number"})
	default:
		return marshal(schema{Type: "object"})
	}
}

// reflectStruct walks a struct's exported fields in declaration order into an
// object schema. A field is required unless its json tag carries ",omitempty";
// a json:"-" field is skipped; a jsonschema:"opaque" field halts recursion.
func reflectStruct(t reflect.Type) json.RawMessage {
	s := schema{Type: "object", Properties: map[string]json.RawMessage{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, omitempty, skip := jsonField(f)
		if skip {
			continue
		}
		if f.Tag.Get("jsonschema") == "opaque" {
			s.Properties[name] = opaqueSchema(f.Type)
		} else {
			s.Properties[name] = reflectType(f.Type)
		}
		if !omitempty {
			s.Required = append(s.Required, name)
		}
	}
	return marshal(s)
}

// jsonField resolves a struct field's schema property name and whether it is
// optional from its json tag. The name is the tag's first segment, falling back
// to the Go field name; skip is true for a json:"-" tag.
func jsonField(f reflect.StructField) (name string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// opaqueSchema renders a field's type as a bare object, or an array of bare
// objects for a slice or array, without descending into the element type. It
// backs the jsonschema:"opaque" tag, used where the per-element shape is
// dynamic (a caller-projected item) so no fixed schema is correct.
func opaqueSchema(t reflect.Type) json.RawMessage {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		return marshal(schema{Type: "array", Items: marshal(schema{Type: "object"})})
	}
	return marshal(schema{Type: "object"})
}

// marshal encodes a schema to JSON. A schema is always marshalable, so an error
// here is impossible and treated as a programmer bug.
func marshal(s schema) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic("jsonschema: marshal schema: " + err.Error())
	}
	return b
}
