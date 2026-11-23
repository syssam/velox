package engine

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ScanPlan reads the rows of a projection into values of a Go type: a
// struct, one field per result column, found by the column's name, or, for a
// projection of one term, the value itself. An encoded column is decoded by
// its codec. NewScanPlan makes it before anything is sent.
type ScanPlan struct {
	in     *EntityInfo
	typ    reflect.Type
	fields [][]int // each result column's field index; nil: the value itself
	codec  []int   // the encoded column each result column reads, or -1
}

var scannerType = reflect.TypeFor[sql.Scanner]()

// NewScanPlan plans the reading of p's rows, a projection of entity in, into
// values of typ. A result column has a name: a column's own, or NamedTerm's.
// A struct's field takes the result column its tag velox:"name" names, else
// the one whose name is the field's name, or the column's Go field, with
// case and underscores ignored (OrgID takes org_id). A typ that is no struct,
// a time.Time, a sql.Scanner, or a struct with no field for the one term of a
// projection that reads an encoded column, takes the one term itself. Every
// result column needs a field, a field takes at most one, and a field whose
// type cannot hold the column's is refused.
func NewScanPlan(in *EntityInfo, p *Projection, typ reflect.Type) (*ScanPlan, error) {
	if p == nil {
		return nil, fmt.Errorf("velox: %s: Scan reads a projection: add Select", in.Name)
	}
	s := &ScanPlan{in: in, typ: typ, fields: make([][]int, len(p.Terms)), codec: make([]int, len(p.Terms))}
	for i, t := range p.Terms {
		if err := t.problem(); err != nil {
			return nil, err
		}
		s.codec[i] = -1
		if t.kind == termColumn && in.Columns[t.col].Codec {
			s.codec[i] = int(t.col)
		}
	}
	if s.whole(p) {
		if len(p.Terms) != 1 {
			return nil, fmt.Errorf("velox: %s: Scan into a %v reads one term, and the projection has %d; scan into a struct", in.Name, typ, len(p.Terms))
		}
		return s, s.check(p.Terms[0], 0, typ, "")
	}
	taken := map[string]int{}
	for i, t := range p.Terms {
		f, err := s.field(t, i)
		if err != nil {
			return nil, err
		}
		if j, dup := taken[f.Name]; dup {
			return nil, fmt.Errorf("velox: %s: Scan into %v: field %s takes result columns %d and %d", in.Name, typ, f.Name, j+1, i+1)
		}
		taken[f.Name] = i
		if err := s.check(t, i, f.Type, f.Name); err != nil {
			return nil, err
		}
		s.fields[i] = f.Index
	}
	return s, nil
}

// whole reports whether the plan's type takes the projection's one term
// itself, not a field per term.
func (s *ScanPlan) whole(p *Projection) bool {
	typ := s.typ
	if typ.Kind() != reflect.Struct || typ == timeType || reflect.PointerTo(typ).Implements(scannerType) {
		return true
	}
	if len(p.Terms) != 1 || s.codec[0] < 0 {
		return false
	}
	_, err := s.field(p.Terms[0], 0)
	return err != nil
}

// termNames are the names of the result column t: the name it was given,
// or a column's name and its Go field's; empty when it has none.
func (s *ScanPlan) termNames(t Term) (name, goName string) {
	switch {
	case t.name != "":
		return t.name, ""
	case t.kind == termColumn:
		c := &s.in.Columns[t.col]
		return c.Name, c.Field
	case t.kind == termJoined && t.of.kind == termColumn:
		// The target's column, by its own names: the result column is
		// named as the column it reads.
		if in, err := joinedTarget(t); err == nil && int(t.of.col) < len(in.Columns) {
			c := &in.Columns[t.of.col]
			return c.Name, c.Field
		}
	}
	return "", ""
}

// label describes result column i, t, in an error.
func (s *ScanPlan) label(t Term, i int) string {
	if name, _ := s.termNames(t); name != "" {
		return "result column " + strconv.Itoa(i+1) + " (" + name + ")"
	}
	return "result column " + strconv.Itoa(i+1)
}

// fold is a name with case and underscores ignored.
func fold(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// field returns the field of the plan's struct that takes result column i,
// t: by tag first, then by name.
func (s *ScanPlan) field(t Term, i int) (reflect.StructField, error) {
	name, goName := s.termNames(t)
	if name == "" {
		return reflect.StructField{}, fmt.Errorf("velox: %s: Scan into %v: %s has no name; name it with As", s.in.Name, s.typ, s.label(t, i))
	}
	var tagged, named []reflect.StructField
	for _, f := range reflect.VisibleFields(s.typ) {
		if !f.IsExported() || f.Anonymous || !settable(s.typ, f.Index) {
			continue
		}
		tag, hasTag := f.Tag.Lookup("velox")
		tag, _, _ = strings.Cut(tag, ",")
		switch {
		case tag == "-":
		case hasTag && tag != "":
			if tag == name || tag == goName {
				tagged = append(tagged, f)
			}
		case f.Name == goName || fold(f.Name) == fold(name):
			named = append(named, f)
		}
	}
	found := tagged
	if len(found) == 0 {
		found = named
	}
	switch len(found) {
	case 0:
		return reflect.StructField{}, fmt.Errorf("velox: %s: Scan into %v: no field takes %s; add a field of that name, or tag one velox:%q", s.in.Name, s.typ, s.label(t, i), name)
	case 1:
		return found[0], nil
	}
	return reflect.StructField{}, fmt.Errorf("velox: %s: Scan into %v: fields %s and %s both take %s; tag one velox:%q", s.in.Name, s.typ, found[0].Name, found[1].Name, s.label(t, i), name)
}

// termType is the Go type of the value of t, or nil when no one type says
// it: an expression, an encoded column, a sum or an average. numeric says t
// is a number of some type (a sum or an average).
func (s *ScanPlan) termType(t Term) (typ reflect.Type, numeric bool) {
	switch t.kind {
	case termColumn:
		if s.in.Columns[t.col].Codec {
			return nil, false
		}
		return GoType(s.in.Columns[t.col]), false
	case termJSON:
		switch t.path.Kind {
		case JSONKindText:
			return reflect.TypeFor[string](), false
		case JSONKindBool:
			return reflect.TypeFor[bool](), false
		}
		return reflect.TypeFor[float64](), false
	case termDate:
		return timeType, false
	case termJoined:
		in, err := joinedTarget(t)
		if err != nil {
			return nil, false
		}
		return (&ScanPlan{in: in}).termType(*t.of)
	case termAgg:
		switch t.fn {
		case TermCount, TermCountDistinct, TermCountAll:
			return reflect.TypeFor[int64](), false
		case TermMin, TermMax:
			return s.termType(*t.of)
		}
		return nil, true
	}
	return nil, false
}

// check refuses a field, of type dst, that cannot hold result column i, t.
func (s *ScanPlan) check(t Term, i int, dst reflect.Type, field string) error {
	src, numeric := s.termType(t)
	if src == nil && !numeric || holds(dst, src, numeric) {
		return nil
	}
	what := "a number"
	if src != nil {
		what = "a " + src.String()
	}
	into := "a " + dst.String()
	if field != "" {
		into = "field " + field + " (" + dst.String() + ")"
	}
	return fmt.Errorf("velox: %s: Scan into %v: %s is %s, which %s cannot hold", s.in.Name, s.typ, s.label(t, i), what, into)
}

// holds reports whether a destination of type dst can hold a value of type
// src, or of some number type when numeric: as database/sql converts, a
// number into any number, text into any string type, and a pointer for NULL.
func holds(dst, src reflect.Type, numeric bool) bool {
	for dst.Kind() == reflect.Pointer {
		dst = dst.Elem()
	}
	if dst.Kind() == reflect.Interface || reflect.PointerTo(dst).Implements(scannerType) {
		return true
	}
	if numeric {
		return isNumber(dst.Kind()) || dst.Kind() == reflect.String
	}
	for src.Kind() == reflect.Pointer {
		src = src.Elem()
	}
	switch {
	case src == dst:
		return true
	case isNumber(src.Kind()):
		return isNumber(dst.Kind())
	case src.Kind() == reflect.String:
		return dst.Kind() == reflect.String
	case src.Kind() == reflect.Bool:
		return dst.Kind() == reflect.Bool
	}
	return src.Kind() == dst.Kind() && src.ConvertibleTo(dst)
}

func isNumber(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Float64
}

// dests fills dst with the scan destination of each result column in v, a
// value of the plan's type. A nil pointer to an embedded struct on the way
// to a field gets a new struct.
func (s *ScanPlan) dests(v reflect.Value, dst []any) {
	for i, idx := range s.fields {
		f := v
		for j, x := range idx {
			if j > 0 && f.Kind() == reflect.Pointer {
				if f.IsNil() {
					f.Set(reflect.New(f.Type().Elem()))
				}
				f = f.Elem()
			}
			f = f.Field(x)
		}
		dst[i] = f.Addr().Interface()
		if c := s.codec[i]; c >= 0 {
			dst[i] = &codecScanner{dst: dst[i], in: s.in, col: c}
		}
	}
}

// ScanRows reads each row of rows into the value next returns a pointer to,
// a new value of the plan's type, by the plan. It stops at the first error.
func ScanRows(rows RawRows, s *ScanPlan, next func() any) error {
	dst := make([]any, len(s.fields))
	for row, err := range rows {
		if err != nil {
			return err
		}
		s.dests(reflect.ValueOf(next()).Elem(), dst)
		if err := row.Scan(dst...); err != nil {
			return fmt.Errorf("velox: %s: Scan into %v: %w", s.in.Name, s.typ, err)
		}
	}
	return nil
}

// settable reports whether the field at index idx of typ can be set in a
// value of typ: no unexported embedded pointer, which dests could not fill
// when nil, is on its way.
func settable(typ reflect.Type, idx []int) bool {
	for _, x := range idx[:len(idx)-1] {
		f := typ.Field(x)
		typ = f.Type
		if typ.Kind() == reflect.Pointer {
			if !f.IsExported() {
				return false
			}
			typ = typ.Elem()
		}
	}
	return true
}
