package gqlinput

import (
	"encoding/json"
	"reflect"
	"testing"
)

type specs struct {
	Weight float64  `json:"weight_kg"`
	Tags   []string `json:"tags,omitempty"`
}

type color string

func TestDecodeScalar(t *testing.T) {
	want := specs{Weight: 1.5, Tags: []string{"a"}}
	const text = `{"weight_kg":1.5,"tags":["a"]}`
	// Every form an engine hands the value over in is the same value.
	for name, v := range map[string]any{
		"the object as JSON decodes it": map[string]any{"weight_kg": 1.5, "tags": []any{"a"}},
		"its text as a string":          text,
		"its text as bytes":             []byte(text),
		"its text as a raw message":     json.RawMessage(text),
	} {
		got, err := DecodeScalar[specs](v)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	// A list is a value like an object is.
	if got, err := DecodeScalar[[]specs]([]any{map[string]any{"weight_kg": 2.0}}); err != nil || len(got) != 1 || got[0].Weight != 2 {
		t.Errorf("a list: %+v, %v", got, err)
	}
	// A string that is not JSON text is the value of a type that is a
	// string in JSON; as JSON text it is that string too.
	if got, err := DecodeScalar[color]("red"); err != nil || got != "red" {
		t.Errorf("a plain string: %q, %v", got, err)
	}
	if got, err := DecodeScalar[color](`"red"`); err != nil || got != "red" {
		t.Errorf("a string as JSON text: %q, %v", got, err)
	}
	// What the type does not decode from is refused, with the zero value.
	for name, v := range map[string]any{
		"a string for a struct":   "red",
		"a number for a struct":   7,
		"text that is not JSON":   []byte(`{"weight_kg":`),
		"a field of another type": map[string]any{"weight_kg": "heavy"},
		"null":                    nil,
		"no text at all":          []byte(nil),
	} {
		if got, err := DecodeScalar[specs](v); err == nil || !reflect.DeepEqual(got, specs{}) {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

func TestDecodeScalarPtr(t *testing.T) {
	if got, err := DecodeScalarPtr[specs](nil); got != nil || err != nil {
		t.Errorf("null: %+v, %v", got, err)
	}
	got, err := DecodeScalarPtr[specs](map[string]any{"weight_kg": 3.0})
	if err != nil || got == nil || got.Weight != 3 {
		t.Errorf("a value: %+v, %v", got, err)
	}
	if got, err := DecodeScalarPtr[specs](7); got != nil || err == nil {
		t.Errorf("what is refused: %+v, %v", got, err)
	}
}

func TestEncodeScalar(t *testing.T) {
	v := specs{Weight: 1.5}
	if b, err := EncodeScalar(v); err != nil || string(b) != `{"weight_kg":1.5}` {
		t.Errorf("a value: %s, %v", b, err)
	}
	if b, err := EncodeScalarPtr(&v); err != nil || string(b) != `{"weight_kg":1.5}` {
		t.Errorf("a pointer: %s, %v", b, err)
	}
	if b, err := EncodeScalarPtr[specs](nil); err != nil || string(b) != "null" {
		t.Errorf("nil: %s, %v", b, err)
	}
	// What is written is read back as it was.
	b, _ := EncodeScalar(specs{Weight: 2, Tags: []string{"x"}})
	if got, err := DecodeScalar[specs](b); err != nil || got.Weight != 2 || len(got.Tags) != 1 {
		t.Errorf("round trip: %+v, %v", got, err)
	}
}
