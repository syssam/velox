package engine_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// money is a value type with an Equal method, as a decimal type has: 1.50
// and 1.5 are equal by it and not by their fields.
type money struct {
	units int64
	scale int8
}

func (m money) Equal(o money) bool {
	a, b := m, o
	for a.scale < b.scale {
		a.units, a.scale = a.units*10, a.scale+1
	}
	for b.scale < a.scale {
		b.units, b.scale = b.units*10, b.scale+1
	}
	return a.units == b.units
}

// badEqual has an Equal method of another signature, which is not used.
type badEqual struct{ v int }

func (badEqual) Equal(any) bool { return true }

// TestEqualValues: equality by value, each rule in turn.
func TestEqualValues(t *testing.T) {
	utc := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"nils", nil, nil, true},
		{"nil and a value", nil, int64(0), false},
		{"a value and nil", "", nil, false},
		{"ints", int64(3), int64(3), true},
		{"different ints", int64(3), int64(4), false},
		{"different types", int64(3), int32(3), false},
		{"time in two zones", utc, utc.In(time.FixedZone("x", 3600)), true},
		{"later time", utc, utc.Add(time.Second), false},
		{"Equal method", money{150, 2}, money{15, 1}, true},
		{"Equal method, different", money{150, 2}, money{16, 1}, false},
		{"Equal of another signature", badEqual{1}, badEqual{2}, false},
		{"bytes", []byte("ab"), []byte("ab"), true},
		{"nil and empty bytes", []byte(nil), []byte{}, true},
		{"different bytes", []byte("ab"), []byte("ac"), false},
		{"JSON raw", json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":1}`), true},
		{"maps", map[string]any{"a": 1.0}, map[string]any{"a": 1.0}, true},
		{"different maps", map[string]any{"a": 1.0}, map[string]any{"a": 2.0}, false},
		{"string slices", []string{"a"}, []string{"a"}, true},
	}
	for _, c := range cases {
		if got := engine.EqualValues(c.a, c.b); got != c.want {
			t.Errorf("%s: EqualValues(%v, %v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

// TestEqualValuesNaN: NaN equals NaN, by value and through a pointer, as a
// column that held NaN and still does has not changed.
func TestEqualValuesNaN(t *testing.T) {
	nan64, nan32 := math.NaN(), float32(math.NaN())
	one := 1.0
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"float64 NaNs", nan64, math.NaN(), true},
		{"float32 NaNs", nan32, float32(math.NaN()), true},
		{"NaN and a number", nan64, 1.0, false},
		{"a number and NaN", float32(1), nan32, false},
		{"zero and negative zero", 0.0, math.Copysign(0, -1), true},
		{"pointers to NaN", &nan64, new(math.NaN()), true},
		{"pointers to float32 NaN", &nan32, new(float32(math.NaN())), true},
		{"pointer to NaN and to a number", &nan64, &one, false},
		{"nil pointer and a pointer", (*float64)(nil), &nan64, false},
		{"nil pointers", (*float64)(nil), (*float64)(nil), true},
	}
	for _, c := range cases {
		if got := engine.EqualValues(c.a, c.b); got != c.want {
			t.Errorf("%s: EqualValues(%v, %v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

// BenchmarkEqualValues: the per-type lookup of an Equal method is cached,
// so a comparison does not walk the method set each time.
func BenchmarkEqualValues(b *testing.B) {
	utc := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		a, b any
	}{
		{"int64", int64(3), int64(3)},
		{"string", "abc", "abc"},
		{"time", utc, utc.Add(time.Second)},
		{"Equal method", money{150, 2}, money{15, 1}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				engine.EqualValues(c.a, c.b)
			}
		})
	}
}
