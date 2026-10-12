package orm

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox/schema/field"
)

// A driver hands a column to Scan in whatever Go type it chose: SQLite and
// Postgres give an int64 for an integer, MySQL may give its digits as bytes.
// The destinations take the driver's own types directly and anything else
// through database/sql's conversion; NULL is the zero value where the Go
// field is not a pointer, and an error where the column cannot be NULL.
func TestScanDestinations(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	var s string
	for _, c := range []struct {
		src  any
		want string
	}{{"a", "a"}, {[]byte("b"), "b"}, {int64(7), "7"}, {nil, ""}} {
		if err := (nullString{&s}).Scan(c.src); err != nil || s != c.want {
			t.Errorf("nullString.Scan(%#v) = %q, %v; want %q", c.src, s, err, c.want)
		}
	}

	var i int
	for _, c := range []struct {
		src  any
		want int
	}{{int64(7), 7}, {[]byte("12"), 12}, {"13", 13}, {nil, 0}} {
		if err := (nullInt{&i}).Scan(c.src); err != nil || i != c.want {
			t.Errorf("nullInt.Scan(%#v) = %d, %v; want %d", c.src, i, err, c.want)
		}
	}
	if err := (nullInt{&i}).Scan("x"); err == nil {
		t.Error("nullInt.Scan of a word succeeded")
	}

	var i64 int64
	for _, c := range []struct {
		src  any
		want int64
	}{{int64(1) << 40, 1 << 40}, {[]byte("12"), 12}, {nil, 0}} {
		if err := (nullInt64{&i64}).Scan(c.src); err != nil || i64 != c.want {
			t.Errorf("nullInt64.Scan(%#v) = %d, %v; want %d", c.src, i64, err, c.want)
		}
	}
	if err := (nullInt64{&i64}).Scan("x"); err == nil {
		t.Error("nullInt64.Scan of a word succeeded")
	}

	var f float64
	for _, c := range []struct {
		src  any
		want float64
	}{{1.5, 1.5}, {[]byte("2.5"), 2.5}, {int64(3), 3}, {nil, 0}} {
		if err := (nullFloat64{&f}).Scan(c.src); err != nil || f != c.want {
			t.Errorf("nullFloat64.Scan(%#v) = %v, %v; want %v", c.src, f, err, c.want)
		}
	}
	if err := (nullFloat64{&f}).Scan("x"); err == nil {
		t.Error("nullFloat64.Scan of a word succeeded")
	}

	var b bool
	for _, c := range []struct {
		src  any
		want bool
	}{{true, true}, {int64(0), false}, {int64(1), true}, {[]byte("true"), true}, {nil, false}} {
		if err := (nullBool{&b}).Scan(c.src); err != nil || b != c.want {
			t.Errorf("nullBool.Scan(%#v) = %v, %v; want %v", c.src, b, err, c.want)
		}
	}
	if err := (nullBool{&b}).Scan("maybe"); err == nil {
		t.Error("nullBool.Scan of a word succeeded")
	}

	var tm time.Time
	if err := (nullTime{&tm}).Scan(when); err != nil || !tm.Equal(when) {
		t.Errorf("nullTime.Scan(time) = %v, %v", tm, err)
	}
	if err := (nullTime{&tm}).Scan(nil); err != nil || !tm.IsZero() {
		t.Errorf("nullTime.Scan(nil) = %v, %v", tm, err)
	}
	if err := (nullTime{&tm}).Scan(int64(5)); err == nil {
		t.Error("nullTime.Scan of a number succeeded")
	}

	// A column that is never NULL: NULL is an error, not a silent zero.
	if err := (intDest{&i}).Scan(int64(9)); err != nil || i != 9 {
		t.Errorf("intDest.Scan(9) = %d, %v", i, err)
	}
	if err := (intDest{&i}).Scan([]byte("10")); err != nil || i != 10 {
		t.Errorf("intDest.Scan(bytes) = %d, %v", i, err)
	}
	if err := (intDest{&i}).Scan(nil); err == nil {
		t.Error("intDest.Scan(nil) succeeded")
	}
	if err := (intDest{&i}).Scan("x"); err == nil {
		t.Error("intDest.Scan of a word succeeded")
	}

	// Any other type: converted when Go allows it, the buffer of a byte
	// column copied, NULL left alone.
	type label string
	var l label = "kept"
	if err := (nullable{&l}).Scan(nil); err != nil || l != "kept" {
		t.Errorf("nullable.Scan(nil) = %q, %v", l, err)
	}
	if err := (nullable{&l}).Scan([]byte("x")); err != nil || l != "x" {
		t.Errorf("nullable.Scan(bytes) into a named string = %q, %v", l, err)
	}
	if err := (nullable{&l}).Scan("y"); err != nil || l != "y" {
		t.Errorf("nullable.Scan(string) into a named string = %q, %v", l, err)
	}
	var i8 int8
	if err := (nullable{&i8}).Scan(int64(5)); err != nil || i8 != 5 {
		t.Errorf("nullable.Scan(int64) into an int8 = %d, %v", i8, err)
	}
	if err := (nullable{&l}).Scan(int64(65)); err == nil {
		t.Error("an integer was stored in a string as a code point")
	}
	buf := []byte("abc")
	var raw []byte
	if err := (nullable{&raw}).Scan(buf); err != nil || string(raw) != "abc" {
		t.Fatalf("nullable.Scan(bytes) = %q, %v", raw, err)
	}
	buf[0] = 'z' // the driver reuses its buffer
	if string(raw) != "abc" {
		t.Error("a scanned byte column shares the driver's buffer")
	}
	if err := (nullable{l}).Scan("x"); err == nil {
		t.Error("nullable.Scan into something that is not a pointer succeeded")
	}

	// JSON: text or bytes, NULL and the empty value leave the field alone.
	tags := []string{"kept"}
	for _, src := range []any{nil, "", []byte(nil)} {
		if err := (jsonDest{&tags}).Scan(src); err != nil || len(tags) != 1 {
			t.Errorf("jsonDest.Scan(%#v) = %v, %v", src, tags, err)
		}
	}
	if err := (jsonDest{&tags}).Scan(`["a","b"]`); err != nil || len(tags) != 2 {
		t.Errorf("jsonDest.Scan(text) = %v, %v", tags, err)
	}
	if err := (jsonDest{&tags}).Scan([]byte(`["c"]`)); err != nil || len(tags) != 1 || tags[0] != "c" {
		t.Errorf("jsonDest.Scan(bytes) = %v, %v", tags, err)
	}
	if err := (jsonDest{&tags}).Scan(int64(1)); err == nil {
		t.Error("jsonDest.Scan of a number succeeded")
	}
	if err := (jsonDest{&tags}).Scan("{"); err == nil {
		t.Error("jsonDest.Scan of broken JSON succeeded")
	}

	// A named bool, and a pointer to one.
	type flag bool
	var fl flag
	if err := (&namedBool{&fl}).Scan(int64(1)); err != nil || !fl {
		t.Errorf("namedBool.Scan(1) = %v, %v", fl, err)
	}
	if err := (&namedBool{&fl}).Scan(nil); err != nil || fl {
		t.Errorf("namedBool.Scan(nil) = %v, %v", fl, err)
	}
	var pf *flag
	if err := (&namedBool{&pf}).Scan(true); err != nil || pf == nil || !*pf {
		t.Errorf("namedBool.Scan(true) into a pointer = %v, %v", pf, err)
	}
	if err := (&namedBool{&pf}).Scan(nil); err != nil || pf != nil {
		t.Errorf("namedBool.Scan(nil) into a pointer = %v, %v", pf, err)
	}
	if err := (&namedBool{&fl}).Scan("maybe"); err == nil {
		t.Error("namedBool.Scan of a word succeeded")
	}
	if err := (discard{}).Scan("anything"); err != nil {
		t.Errorf("discard.Scan = %v", err)
	}
}

// The same key read from two places must land in one map bucket whatever its
// Go type: that is what joins a loaded neighbor to its parent.
func TestKeyNormalization(t *testing.T) {
	type name string
	type num uint16
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	for _, c := range []struct{ a, b any }{
		{int(7), int64(7)}, {int8(7), int64(7)}, {int16(7), int64(7)}, {int32(7), int64(7)},
		{uint(7), int64(7)}, {uint32(7), int64(7)}, {uint64(7), int64(7)}, {num(7), int64(7)},
		{name("k"), "k"}, {[]byte("k"), "k"},
		{id, id.String()}, {id[:], id.String()}, {[]byte(id.String()), id.String()},
	} {
		if key(c.a) != key(c.b) {
			t.Errorf("key(%T %v) = %#v, key(%T %v) = %#v", c.a, c.a, key(c.a), c.b, c.b, key(c.b))
		}
	}
	if key(uint64(1)<<63) == key(int64(-1)<<63) {
		t.Error("an unsigned key above the int64 range equals a negative one")
	}
	if key(int64(1)) == key("1") {
		t.Error("a number and its text are one key")
	}
}

// A slice or map default is one value in the schema; each new row gets its own.
func TestFreshDefault(t *testing.T) {
	js := &FieldMeta{Type: field.TypeJSON}
	list := []string{"a"}
	got := fresh(js, list).([]string)
	got[0] = "changed"
	if list[0] != "a" {
		t.Error("a row shares the schema's slice default")
	}
	m := map[string]int{"a": 1}
	gm := fresh(js, m).(map[string]int)
	gm["b"] = 2
	if len(m) != 1 || gm["a"] != 1 {
		t.Errorf("a row shares the schema's map default: %v %v", m, gm)
	}
	if fresh(js, []string(nil)).([]string) != nil || fresh(js, map[string]int(nil)).(map[string]int) != nil {
		t.Error("a nil default became a value")
	}
	if fresh(&FieldMeta{Type: field.TypeString}, "s") != "s" || fresh(js, 3) != 3 {
		t.Error("a scalar default was changed")
	}
}
