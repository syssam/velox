package gqlgenx_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestFunctionMarshalers pins the gqlgen function marshalers, which bind the
// scalars to the Go types entities hold: each writes what the scalar type
// writes, and reads it back.
func TestFunctionMarshalers(t *testing.T) {
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, time.FixedZone("", 8*3600))
	u := uuid.MustParse("0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	for got, want := range map[string]string{
		marshal(gqlgenx.MarshalTime(when)):                           `"2026-10-20T01:02:03.123456789+08:00"`,
		marshal(gqlgenx.MarshalUUID(u)):                              `"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"`,
		marshal(gqlgenx.MarshalUint64(math.MaxUint64)):               `"18446744073709551615"`,
		marshal(gqlgenx.MarshalUint(7)):                              `"7"`,
		marshal(gqlgenx.MarshalBytes([]byte("hi"))):                  `"aGk="`,
		marshal(gqlgenx.MarshalFloat32(0.1)):                         `0.1`,
		marshal(gqlgenx.MarshalFloat32(float32(math.Inf(1)))):        `null`,
		marshal(gqlgenx.MarshalFloat32(float32(math.NaN()))):         `null`,
		marshal(gqlgenx.MarshalFloat32(-3)):                          `-3`,
		marshal(gqlgenx.MarshalFloat32(math.MaxFloat32)):             `3.4028235e+38`,
		marshal(gqlgenx.MarshalFloat32(math.SmallestNonzeroFloat32)): `1e-45`,
	} {
		if got != want {
			t.Errorf("marshalled %s, want %s", got, want)
		}
	}

	if got, err := gqlgenx.UnmarshalTime("2026-10-20T01:02:03.123456789+08:00"); err != nil || !got.Equal(when) {
		t.Errorf("UnmarshalTime = %v, %v", got, err)
	}
	if got, err := gqlgenx.UnmarshalUUID("0193A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"); err != nil || got != u {
		t.Errorf("UnmarshalUUID = %v, %v", got, err)
	}
	if got, err := gqlgenx.UnmarshalUint64("9223372036854775807"); err != nil || got != math.MaxInt64 {
		t.Errorf("UnmarshalUint64 = %v, %v", got, err)
	}
	if got, err := gqlgenx.UnmarshalUint("7"); err != nil || got != 7 {
		t.Errorf("UnmarshalUint = %v, %v", got, err)
	}
	if got, err := gqlgenx.UnmarshalBytes("aGk="); err != nil || string(got) != "hi" {
		t.Errorf("UnmarshalBytes = %q, %v", got, err)
	}
	for in, want := range map[any]float32{0.5: 0.5, int64(3): 3, "1.25": 1.25, int(2): 2} {
		if got, err := gqlgenx.UnmarshalFloat32(in); err != nil || got != want {
			t.Errorf("UnmarshalFloat32(%v) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []any{1e39, -1e39, "x", true} {
		if got, err := gqlgenx.UnmarshalFloat32(in); err == nil {
			t.Errorf("UnmarshalFloat32(%v) = %v, want an error", in, got)
		}
	}
	for name, err := range map[string]error{
		"time":   second(gqlgenx.UnmarshalTime("yesterday")),
		"uuid":   second(gqlgenx.UnmarshalUUID("x")),
		"uint64": second(gqlgenx.UnmarshalUint64(int64(5))), // a decimal string both ways
		"uint":   second(gqlgenx.UnmarshalUint("-1")),
		"bytes":  second(gqlgenx.UnmarshalBytes("!!")),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

// TestUint64OverSaysWhy: a Uint64 over 2^63-1 is refused, and the message says
// why: database/sql cannot send it, so a client knows the limit is not its own.
func TestUint64OverSaysWhy(t *testing.T) {
	_, err := gqlgenx.UnmarshalUint64("18446744073709551615")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"9223372036854775807", "database/sql", "high bit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

// marshal is what m writes.
func marshal(m graphql.Marshaler) string {
	var buf bytes.Buffer
	m.MarshalGQL(&buf)
	return buf.String()
}
