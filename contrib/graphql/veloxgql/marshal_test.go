package veloxgql_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestFunctionMarshalers pins the gqlgen function marshalers, which bind the
// scalars to the Go types entities hold: each writes what the scalar type
// writes, and reads it back.
func TestFunctionMarshalers(t *testing.T) {
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, time.FixedZone("", 8*3600))
	u := uuid.MustParse("0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	for got, want := range map[string]string{
		marshal(veloxgql.MarshalTime(when)):                           `"2026-10-20T01:02:03.123456789+08:00"`,
		marshal(veloxgql.MarshalUUID(u)):                              `"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"`,
		marshal(veloxgql.MarshalUint64(math.MaxUint64)):               `"18446744073709551615"`,
		marshal(veloxgql.MarshalUint(7)):                              `"7"`,
		marshal(veloxgql.MarshalBytes([]byte("hi"))):                  `"aGk="`,
		marshal(veloxgql.MarshalFloat32(0.1)):                         `0.1`,
		marshal(veloxgql.MarshalFloat32(float32(math.Inf(1)))):        `null`,
		marshal(veloxgql.MarshalFloat32(float32(math.NaN()))):         `null`,
		marshal(veloxgql.MarshalFloat32(-3)):                          `-3`,
		marshal(veloxgql.MarshalFloat32(math.MaxFloat32)):             `3.4028235e+38`,
		marshal(veloxgql.MarshalFloat32(math.SmallestNonzeroFloat32)): `1e-45`,
	} {
		if got != want {
			t.Errorf("marshalled %s, want %s", got, want)
		}
	}

	if got, err := veloxgql.UnmarshalTime("2026-10-20T01:02:03.123456789+08:00"); err != nil || !got.Equal(when) {
		t.Errorf("UnmarshalTime = %v, %v", got, err)
	}
	if got, err := veloxgql.UnmarshalUUID("0193A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"); err != nil || got != u {
		t.Errorf("UnmarshalUUID = %v, %v", got, err)
	}
	if got, err := veloxgql.UnmarshalUint64("9223372036854775807"); err != nil || got != math.MaxInt64 {
		t.Errorf("UnmarshalUint64 = %v, %v", got, err)
	}
	if got, err := veloxgql.UnmarshalUint("7"); err != nil || got != 7 {
		t.Errorf("UnmarshalUint = %v, %v", got, err)
	}
	if got, err := veloxgql.UnmarshalBytes("aGk="); err != nil || string(got) != "hi" {
		t.Errorf("UnmarshalBytes = %q, %v", got, err)
	}
	for in, want := range map[any]float32{0.5: 0.5, int64(3): 3, "1.25": 1.25, int(2): 2} {
		if got, err := veloxgql.UnmarshalFloat32(in); err != nil || got != want {
			t.Errorf("UnmarshalFloat32(%v) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []any{1e39, -1e39, "x", true} {
		if got, err := veloxgql.UnmarshalFloat32(in); err == nil {
			t.Errorf("UnmarshalFloat32(%v) = %v, want an error", in, got)
		}
	}
	for name, err := range map[string]error{
		"time":   second(veloxgql.UnmarshalTime("yesterday")),
		"uuid":   second(veloxgql.UnmarshalUUID("x")),
		"uint64": second(veloxgql.UnmarshalUint64(int64(5))), // a decimal string both ways
		"uint":   second(veloxgql.UnmarshalUint("-1")),
		"bytes":  second(veloxgql.UnmarshalBytes("!!")),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }
