// This file holds the encoding and decoding of JSON columns
// (ColumnInfo.JSON). encoding/json uses reflect, so none of it is generic
// (design §3 rule 7): the generic code calls scanDst, and the writer encodeArg.

package velox

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unsafe"
)

// encodeJSON is the argument bound for v, a value of the JSON column col of
// entity in: the bytes of its JSON, or nil (SQL NULL) for a nil pointer, and
// for a nil slice or map when the column is nullable. In a column that is not,
// a nil slice or map is the JSON null.
func encodeJSON(in *EntityInfo, col int, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if in.Columns[col].Nullable {
		rv := reflect.ValueOf(v)
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return nil, nil
			}
			rv = rv.Elem()
		}
		if k := rv.Kind(); (k == reflect.Slice || k == reflect.Map) && rv.IsNil() {
			return nil, nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("velox: %s: column %s: encoding JSON: %w", in.Name, in.Columns[col].Name, err)
	}
	return b, nil
}

// jsonScanner scans a JSON column into its field: it is the scan destination
// of column col of entity in, whose field dst points to.
type jsonScanner struct {
	dst any // a pointer to the field
	in  *EntityInfo
	col int
}

// scanDst is what a scan of column col of entity in reads into, given addr,
// a pointer to the column's field: addr itself, or, for a JSON column, a
// jsonScanner decoding into it, the one allocation a JSON column costs a row.
func scanDst(in *EntityInfo, col int, addr any) any {
	if !in.Columns[col].JSON {
		return addr
	}
	return &jsonScanner{dst: addr, in: in, col: col}
}

// Scan decodes src, the column's value as the driver hands it over: []byte or
// string. NULL sets the field to its zero value, nil for a pointer. A value
// that is not JSON of the field's type fails, naming the entity and the
// column. The field is reset first, so that decoding never merges into what
// it held.
func (s *jsonScanner) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		reflect.ValueOf(s.dst).Elem().SetZero()
		return nil
	case []byte:
		b = v
	case string:
		// json.Unmarshal neither changes nor keeps its input, so the
		// string's bytes are read in place rather than copied.
		b = unsafe.Slice(unsafe.StringData(v), len(v))
	default:
		return fmt.Errorf("velox: %s: column %s: cannot decode JSON from a %T", s.in.Name, s.in.Columns[s.col].Name, src)
	}
	reflect.ValueOf(s.dst).Elem().SetZero()
	if err := json.Unmarshal(b, s.dst); err != nil {
		return fmt.Errorf("velox: %s: column %s: decoding JSON: %w", s.in.Name, s.in.Columns[s.col].Name, err)
	}
	return nil
}

// errJSONCondition is the error for a condition that compares the JSON
// column col of in: JSON equality differs by dialect (jsonb normalises, TEXT
// does not), so only IsNull and NotNull are allowed.
func errJSONCondition(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s holds JSON, which a condition cannot compare (JSON equality differs by dialect); only IsNull and NotNull apply to it", in.Name, in.Columns[col].Name)
}
