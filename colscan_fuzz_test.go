package velox_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel/kind"
)

// fuzzMoment is a struct type that converts to time.Time, with no methods:
// the case of scanWraps that database/sql converts by reflection.
type fuzzMoment time.Time

// fuzzDests are the destination types FuzzColumnScan scans into: the wrapped
// types of scanWraps that tests outside the package can name.
var fuzzDests = []reflect.Type{
	reflect.TypeFor[*int](),
	reflect.TypeFor[*int8](),
	reflect.TypeFor[*int16](),
	reflect.TypeFor[*int32](),
	reflect.TypeFor[*int64](),
	reflect.TypeFor[*uint](),
	reflect.TypeFor[*uint8](),
	reflect.TypeFor[*uint16](),
	reflect.TypeFor[*uint32](),
	reflect.TypeFor[*uint64](),
	reflect.TypeFor[*float32](),
	reflect.TypeFor[*float64](),
	reflect.TypeFor[*bool](),
	reflect.TypeFor[*string](),
	reflect.TypeFor[*[]byte](),
	reflect.TypeFor[*time.Time](),
	reflect.TypeFor[**string](),
	reflect.TypeFor[**int64](),
	reflect.TypeFor[**time.Time](),
	reflect.TypeFor[**float64](),
	reflect.TypeFor[**bool](),
	reflect.TypeFor[*json.RawMessage](),
	reflect.TypeFor[*kind.Status](),
	reflect.TypeFor[**kind.Status](),
	reflect.TypeFor[*kind.Cents](),
	reflect.TypeFor[*fuzzMoment](),
	reflect.TypeFor[**fuzzMoment](),
}

// fuzzDest is the index of t in fuzzDests.
func fuzzDest(t reflect.Type) uint8 {
	for i, d := range fuzzDests {
		if d == t {
			return uint8(i)
		}
	}
	panic("not a fuzz destination: " + t.String())
}

// sameScanned reports whether two scanned destinations hold the same value.
// A NaN equals a NaN here.
func sameScanned(a, b reflect.Value) bool {
	for a.Kind() == reflect.Pointer && b.Kind() == reflect.Pointer {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		a, b = a.Elem(), b.Elem()
	}
	if a.Kind() == reflect.Float32 || a.Kind() == reflect.Float64 {
		return a.Kind() == b.Kind() && math.Float64bits(a.Float()) == math.Float64bits(b.Float())
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// FuzzColumnScan: scanning through a colScanner, in the call pgx's stdlib
// driver makes, gives what database/sql gives for the destination itself: the
// same value, or the same error.
func FuzzColumnScan(f *testing.F) {
	for pick := range uint8(7) {
		for _, dst := range []reflect.Type{
			reflect.TypeFor[*int8](), reflect.TypeFor[*uint8](),
			reflect.TypeFor[*string](), reflect.TypeFor[**time.Time](),
		} {
			f.Add(pick, int64(300), 1.5, "42", []byte("7"), int64(1_700_000_000), fuzzDest(dst))
		}
	}
	for _, dst := range []reflect.Type{reflect.TypeFor[*fuzzMoment](), reflect.TypeFor[**fuzzMoment]()} {
		f.Add(uint8(6), int64(0), 0.0, "", []byte(nil), int64(1_700_000_000), fuzzDest(dst))
		f.Add(uint8(0), int64(0), 0.0, "", []byte(nil), int64(0), fuzzDest(dst))
		f.Add(uint8(4), int64(0), 0.0, "abc", []byte(nil), int64(0), fuzzDest(dst))
	}
	f.Add(uint8(1), int64(-1), 0.0, "", []byte(nil), int64(0), fuzzDest(reflect.TypeFor[*uint8]()))
	f.Add(uint8(4), int64(0), 0.0, "abc", []byte(nil), int64(0), fuzzDest(reflect.TypeFor[*int64]()))
	f.Add(uint8(0), int64(0), 0.0, "", []byte(nil), int64(0), fuzzDest(reflect.TypeFor[*string]()))
	f.Add(uint8(0), int64(0), 0.0, "", []byte(nil), int64(0), fuzzDest(reflect.TypeFor[**time.Time]()))
	f.Fuzz(func(t *testing.T, pick uint8, i int64, fl float64, s string, b []byte, sec int64, which uint8) {
		var src any
		switch pick % 7 {
		case 0:
			src = nil
		case 1:
			src = i
		case 2:
			src = fl
		case 3:
			src = i%2 == 0
		case 4:
			src = s
		case 5:
			src = b
		case 6:
			src = time.Unix(sec%1e11, 0).UTC()
		}
		typ := fuzzDests[int(which)%len(fuzzDests)]
		d1, d2 := reflect.New(typ.Elem()), reflect.New(typ.Elem())
		err1 := velox.ColumnScan(d1.Interface(), src)
		err2 := sql.ConvertAssign(driver.ScanContext{}, d2.Interface(), src)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%s from %#v: wrapped error %v, plain error %v", typ, src, err1, err2)
		}
		if err1 != nil {
			if err1.Error() != err2.Error() {
				t.Fatalf("%s from %#v: wrapped error %q, plain error %q", typ, src, err1, err2)
			}
			return
		}
		if !sameScanned(d1.Elem(), d2.Elem()) {
			t.Fatalf("%s from %#v: wrapped %v, plain %v", typ, src, d1.Elem(), d2.Elem())
		}
	})
}
