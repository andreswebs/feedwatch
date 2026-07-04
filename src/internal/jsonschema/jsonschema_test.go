package jsonschema_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/jsonschema"
)

// parsed is the decoded shape of an emitted schema, used to assert on structure
// rather than raw bytes.
type parsed struct {
	Type        string                     `json:"type"`
	Properties  map[string]json.RawMessage `json:"properties"`
	Required    []string                   `json:"required"`
	Items       json.RawMessage            `json:"items"`
	Description string                     `json:"description"`
	OneOf       []json.RawMessage          `json:"oneOf"`
}

func decode(t *testing.T, raw json.RawMessage) parsed {
	t.Helper()
	var p parsed
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("schema is not valid JSON: %v\ngot: %s", err, raw)
	}
	return p
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestReflectStruct(t *testing.T) {
	type flat struct {
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Note    string `json:"note,omitempty"`
		Skipped string `json:"-"`
		hidden  string //nolint:unused // unexported: must be excluded from the schema
	}

	p := decode(t, jsonschema.Reflect(flat{}))

	if p.Type != "object" {
		t.Errorf("type = %q, want object", p.Type)
	}
	if got, want := keys(p.Properties), []string{"count", "name", "note"}; !reflect.DeepEqual(got, want) {
		t.Errorf("properties = %v, want %v", got, want)
	}
	if got, want := p.Required, []string{"name", "count"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v (declaration order, no omitempty/skip)", got, want)
	}
}

func TestReflectKindMapping(t *testing.T) {
	type kinds struct {
		S string  `json:"s"`
		B bool    `json:"b"`
		I int     `json:"i"`
		U uint64  `json:"u"`
		F float64 `json:"f"`
	}

	p := decode(t, jsonschema.Reflect(kinds{}))
	want := map[string]string{
		"s": "string", "b": "boolean", "i": "integer", "u": "integer", "f": "number",
	}
	for field, wantType := range want {
		if got := decode(t, p.Properties[field]).Type; got != wantType {
			t.Errorf("field %q type = %q, want %q", field, got, wantType)
		}
	}
}

func TestReflectNestedAndSlice(t *testing.T) {
	type inner struct {
		V string `json:"v"`
	}
	type outer struct {
		One  inner   `json:"one"`
		Many []inner `json:"many"`
	}

	p := decode(t, jsonschema.Reflect(outer{}))

	one := decode(t, p.Properties["one"])
	if one.Type != "object" || decode(t, one.Properties["v"]).Type != "string" {
		t.Errorf("nested struct did not recurse into an object: %s", p.Properties["one"])
	}

	many := decode(t, p.Properties["many"])
	if many.Type != "array" {
		t.Fatalf("slice field type = %q, want array", many.Type)
	}
	item := decode(t, many.Items)
	if item.Type != "object" || decode(t, item.Properties["v"]).Type != "string" {
		t.Errorf("slice items did not recurse into an object: %s", many.Items)
	}
}

func TestReflectOpaqueSlice(t *testing.T) {
	type element struct {
		Deep string `json:"deep"`
	}
	type holder struct {
		Items []element `json:"items" jsonschema:"opaque"`
	}

	p := decode(t, jsonschema.Reflect(holder{}))
	arr := decode(t, p.Properties["items"])
	if arr.Type != "array" {
		t.Fatalf("opaque field type = %q, want array", arr.Type)
	}
	item := decode(t, arr.Items)
	if item.Type != "object" {
		t.Errorf("opaque items type = %q, want object", item.Type)
	}
	if len(item.Properties) != 0 {
		t.Errorf("opaque items expanded into %v, want no expansion", keys(item.Properties))
	}
}

func TestOneOf(t *testing.T) {
	a := jsonschema.Reflect(struct {
		A int `json:"a"`
	}{})
	b := jsonschema.Reflect(struct {
		B string `json:"b"`
	}{})

	p := decode(t, jsonschema.OneOf(a, b))
	if len(p.OneOf) != 2 {
		t.Fatalf("oneOf has %d alternatives, want 2", len(p.OneOf))
	}
	if string(p.OneOf[0]) != string(a) || string(p.OneOf[1]) != string(b) {
		t.Errorf("oneOf did not preserve its alternatives in order: %s", p.OneOf)
	}
}

// parsedFull decodes both the Type field and Format, for asserting on time schemas.
type parsedFull struct {
	Type   json.RawMessage            `json:"type"`
	Format string                     `json:"format"`
	Props  map[string]json.RawMessage `json:"properties"`
	Items  json.RawMessage            `json:"items"`
}

func decodeFull(t *testing.T, raw json.RawMessage) parsedFull {
	t.Helper()
	var p parsedFull
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("schema is not valid JSON: %v\ngot: %s", err, raw)
	}
	return p
}

// TestReflectTimeFields covers that time.Time reflects as a date-time string
// and *time.Time reflects as a nullable date-time (type array or plain string
// with null allowed), both carrying format:"date-time".
func TestReflectTimeFields(t *testing.T) {
	type holder struct {
		At    time.Time  `json:"at"`
		MayAt *time.Time `json:"may_at"`
	}

	p := decodeFull(t, jsonschema.Reflect(holder{}))

	at := decodeFull(t, p.Props["at"])
	if at.Format != "date-time" {
		t.Errorf("time.Time format = %q, want date-time", at.Format)
	}
	var atType string
	if err := json.Unmarshal(at.Type, &atType); err != nil || atType != "string" {
		t.Errorf("time.Time type = %s, want \"string\"", at.Type)
	}

	mayAt := decodeFull(t, p.Props["may_at"])
	if mayAt.Format != "date-time" {
		t.Errorf("*time.Time format = %q, want date-time", mayAt.Format)
	}
}

func TestScalar(t *testing.T) {
	p := decode(t, jsonschema.Scalar("string", "desc"))
	if p.Type != "string" {
		t.Errorf("type = %q, want string", p.Type)
	}
	if p.Description != "desc" {
		t.Errorf("description = %q, want desc", p.Description)
	}
	if len(p.Properties) != 0 || p.Required != nil {
		t.Errorf("scalar emitted object keys: %s", jsonschema.Scalar("string", "desc"))
	}
}
