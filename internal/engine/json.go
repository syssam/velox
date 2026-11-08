package engine

import (
	"errors"
	"fmt"
	"reflect"
	"unsafe"
)

// encodeCol is the argument bound for v, a value of the encoded column col
// of entity in: the bytes its Encode returns, or nil (SQL NULL) for a nil
// pointer, and for a nil slice or map when the column is nullable. In a
// column that is not, a nil slice or map is encoded (by encoding/json, as
// the JSON null).
func encodeCol(in *EntityInfo, col int, v any) (any, error) {
	c := &in.Columns[col]
	if c.encode == nil {
		return nil, errNoCodec(in, col)
	}
	if v == nil {
		return nil, nil
	}
	if c.Nullable {
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
	b, err := c.encode(v)
	if err != nil {
		return nil, fmt.Errorf("velox: %s: column %s: encoding its value: %w", in.Name, c.Name, err)
	}
	return b, nil
}

// errNoCodec is the error for a column flagged as encoded (ColumnInfo.Codec)
// without its codec: an EntityInfo a hook built or edited, as BuildDescriptor
// refuses a Descriptor that gives only one of Encode and Decode.
func errNoCodec(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (Codec) but has no Encode and Decode", in.Name, in.Columns[col].Name)
}

// codecScanner scans an encoded column into its field: it is the scan
// destination of column col of entity in, whose field dst points to.
type codecScanner struct {
	dst any // a pointer to the field
	in  *EntityInfo
	col int
}

// ScanDst is what a scan of column col of entity in reads into, given addr,
// a pointer to the column's field: addr itself, or, for an encoded column, a
// codecScanner decoding into it, the one allocation such a column costs a
// row.
func ScanDst(in *EntityInfo, col int, addr any) any {
	if !in.Columns[col].Codec {
		return addr
	}
	return &codecScanner{dst: addr, in: in, col: col}
}

// errNotBytes is why a value of a type other than []byte or string cannot
// be decoded.
var errNotBytes = errors.New("the driver handed over neither []byte nor string")

// Scan decodes src, the column's value as the driver hands it over: []byte or
// string. NULL sets the field to its zero value, nil for a pointer. A value
// that does not decode fails, naming the entity and the column. The field is
// reset first, so that decoding never merges into what it held.
func (s *codecScanner) Scan(src any) error {
	c := &s.in.Columns[s.col]
	if c.decode == nil {
		return errNoCodec(s.in, s.col)
	}
	var b []byte
	switch v := src.(type) {
	case nil:
		reflect.ValueOf(s.dst).Elem().SetZero()
		return nil
	case []byte:
		b = v
	case string:
		// Decode neither changes nor keeps its input (ColumnDescriptor.Decode), so
		// the string's bytes are read in place rather than copied.
		b = unsafe.Slice(unsafe.StringData(v), len(v))
	default:
		return fmt.Errorf("velox: %s: column %s: decoding a %T: %w", s.in.Name, c.Name, src, errNotBytes)
	}
	reflect.ValueOf(s.dst).Elem().SetZero()
	if err := c.decode(b, s.dst); err != nil {
		return fmt.Errorf("velox: %s: column %s: decoding its value: %w", s.in.Name, c.Name, err)
	}
	return nil
}

// errCodecCondition is the error for a condition that compares the encoded
// column col of in: equality of encoded values differs by dialect (jsonb
// normalises JSON, TEXT does not), so only IsNull and NotNull are allowed.
func errCodecCondition(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which a condition cannot compare: equality of encoded values differs by dialect; only IsNull and NotNull apply to it, and a JSON handle's Text, Number and HasKey to a value inside it", in.Name, in.Columns[col].Name)
}

// errCodecOrderBy is the error for ordering by the encoded column col of in:
// the order of encoded values differs by dialect (jsonb orders by its own
// rules, MySQL's JSON by its, TEXT by the text), as their equality does.
func errCodecOrderBy(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which a query cannot order by: the order of encoded values differs by dialect", in.Name, in.Columns[col].Name)
}

// errCodecGroupBy is the error for grouping by the encoded column col of in.
func errCodecGroupBy(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which a query cannot group by: encoded values compare differently by dialect", in.Name, in.Columns[col].Name)
}

// errCodecAdd is the error for adding to or subtracting from the encoded
// column col of in.
func errCodecAdd(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column, which cannot be added to or subtracted from", in.Name, in.Columns[col].Name)
}

// errCodecOrder is the error for paging After over the encoded column col of
// in: no condition can compare it.
func errCodecOrder(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: After cannot page over the column %s, an encoded column (JSON in velox-gen): a condition cannot compare it", in.Name, in.Columns[col].Name)
}
