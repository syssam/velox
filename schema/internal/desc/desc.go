// Package desc holds the descriptors the schema DSL builds: the plain data a
// field, an edge or an index declares. The DSL packages fill them, the loader
// reads them, and none of them runs anything. The package imports only the
// standard library.
package desc

import (
	"encoding"
	"encoding/json"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Caller returns the program counter of the call to the function that calls
// Caller: a DSL constructor calls it, directly, to record the line of the
// schema that declares the element. It returns 0 when the stack does not say.
//
// It only captures the counter. PosOf turns it into file:line, which only the
// loader needs: the generated code also calls the constructors, to read their
// functions, and pays for no file lookup and no string.
func Caller() uintptr {
	var pc [1]uintptr
	// Skip runtime.Callers, Caller and the constructor. runtime.Callers counts
	// inlined frames, so this holds whatever the compiler inlines.
	if runtime.Callers(3, pc[:]) == 0 {
		return 0
	}
	return pc[0]
}

// PosOf returns the file:line of a counter that Caller returned, or "" for 0.
func PosOf(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	f, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if f.File == "" {
		return ""
	}
	return f.File + ":" + strconv.Itoa(f.Line)
}

// Annotation is extra data a schema attaches to itself, a field, an edge or an
// index. Name identifies the annotation; two annotations of one name on one
// element are merged when their type implements schema.Merger.
type Annotation interface{ Name() string }

// GoType describes a Go type as the generator must write it. GoTypeOf fills it
// from a reflect.Type.
type GoType struct {
	Ident   string   // as written in Go source, e.g. "[]string", "*schema.Info", "uuid.UUID"
	PkgPath string   // the named type's package; "" for builtin and unnamed composites of builtins
	Kind    string   // reflect.Kind's String(), of the type itself: "slice", "ptr", "struct", "string"…
	Imports []string // every package path the type mentions, sorted, without duplicates

	// Marshaler reports that the type, or a pointer to it, has its own JSON
	// or text encoding (json.Marshaler, encoding.TextMarshaler), so that its
	// JSON need not be that of its underlying type.
	Marshaler bool
}

// GoTypeOf describes t. Ident is t.String() and Kind is t.Kind().String().
// Marshaler says whether t or *t implements json.Marshaler or
// encoding.TextMarshaler.
// PkgPath is t.PkgPath(), or for a pointer the PkgPath of its element. Imports
// is the sorted, deduplicated list of the package paths of the named types t
// mentions, found by walking pointers, slices, arrays, maps (key and element)
// and channels; it is nil when t mentions none. The walk stops at a named
// type, whose name alone appears in Ident.
func GoTypeOf(t reflect.Type) *GoType {
	g := &GoType{Ident: t.String(), Kind: t.Kind().String(), PkgPath: t.PkgPath()}
	if t.Kind() == reflect.Pointer {
		g.PkgPath = t.Elem().PkgPath()
	}
	g.Imports = collectImports(t, nil)
	slices.Sort(g.Imports)
	g.Imports = slices.Compact(g.Imports)
	g.Marshaler = marshals(t) || t.Kind() != reflect.Pointer && marshals(reflect.PointerTo(t))
	return g
}

// marshals reports whether t has its own JSON or text encoding.
func marshals(t reflect.Type) bool {
	return t.Implements(reflect.TypeFor[json.Marshaler]()) || t.Implements(reflect.TypeFor[encoding.TextMarshaler]())
}

func collectImports(t reflect.Type, out []string) []string {
	if p := t.PkgPath(); p != "" {
		return append(out, p)
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		out = collectImports(t.Elem(), out)
	case reflect.Map:
		out = collectImports(t.Key(), out)
		out = collectImports(t.Elem(), out)
	case reflect.Struct:
		for sf := range t.Fields() {
			out = collectImports(sf.Type, out)
		}
	}
	return out
}

// FuncOf returns the function of a counter that Caller returned, as
// runtime.Frame.Function reports it (the package path, then the name, as in
// github.com/syssam/velox/schema/mixin.Time.Fields), or "" for 0.
func FuncOf(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	f, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	return f.Function
}

// Unwritable is why velox-gen cannot write t as Go source, or "": t
// reaches, through pointers, slices, arrays, maps, channels and the fields
// of unnamed structs, an instance of a generic type (reflect prints its type
// arguments with their full package paths), an unnamed func, an unnamed
// interface with methods, or an unnamed struct with an unexported field,
// which written in another package would be another type. An unnamed struct
// of exported fields is written inline, as veloxe wrote it: reflect prints
// it as Go source, its tags quoted, and GoTypeOf lists its fields' packages.
// interface{} and struct{} are writable.
func Unwritable(t reflect.Type) string {
	if t.Name() != "" {
		if strings.Contains(t.Name(), "[") {
			return "is an instance of generic type " + t.String()
		}
		return ""
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		return Unwritable(t.Elem())
	case reflect.Map:
		if r := Unwritable(t.Key()); r != "" {
			return r
		}
		return Unwritable(t.Elem())
	case reflect.Func:
		return "holds an unnamed func type"
	case reflect.Interface:
		if t.NumMethod() > 0 {
			return "holds an unnamed interface type"
		}
	case reflect.Struct:
		for sf := range t.Fields() {
			if !sf.IsExported() {
				return "holds an unnamed struct type with the unexported field " + sf.Name
			}
			if r := Unwritable(sf.Type); r != "" {
				return r
			}
		}
	}
	return ""
}

// Check is a built-in validator on a field. Kind names it and Args holds its
// arguments.
type Check struct {
	Kind string // NotEmpty, MinLen, MaxLen, Match, Range, Min, Max, Positive, Negative, NonNegative
	Args []any  // MinLen/MaxLen: [int]; Match: [pattern string]; Range: [min, max]; Min/Max: [v]
}

// Enum is one value of an enum field. Name is the suffix of the generated Go
// constant and Value is the string stored in the database.
type Enum struct{ Name, Value string } // Name is the Go name's suffix; Value is stored

// Field is the descriptor of one field. The field package's builders fill it.
type Field struct {
	Name          string
	Type          string  // bool int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 float32 float64 string bytes time uuid json enum other
	Size          int     // max length; 0 for the dialect default; math.MaxInt32 for Text
	GoType        *GoType // nil: the standard type for Type
	RType         any     // the reflect.Type of the field's Go type, to check functions; never serialized
	Optional      bool    // not required on create; the column stays NOT NULL, except a JSON field's
	Nillable      bool    // the column is NULL-able, and the Go field a pointer
	Immutable     bool    // refused in updates
	Version       bool    // the optimistic-locking version: velox bumps it on every update
	Unique        bool    // a single-column unique index
	Sensitive     bool    // left out of String(), JSON, logs and spans
	Default       any     // a value (or, on time and uuid, a func() T); nil when none
	DefaultFunc   any     // a func() T; nil when none
	UpdateDefault any     // a func() T; nil when none
	Enums         []Enum
	Checks        []Check
	Validators    []any // Validate's functions, each a func(T) error
	StorageKey    string
	SchemaType    map[string]string
	SchemaDefault map[string]string // the database default per dialect, as SQL
	Comment       string
	StructTag     string
	Annotations   []Annotation
	PC            uintptr // the constructor's call, for PosOf
	Errs          []error // every misuse of the builder, each a reason without the field's name
}

// Edge is the descriptor of one edge. The edge package's builders fill it.
type Edge struct {
	Name, Type, TypePkg string // Type is the target schema's type name; TypePkg its package path
	Inverse             bool   // declared with From
	RefName             string // From(...).Ref(name)
	Ref                 *Edge  // To(...).From(name): the inverse declared inline
	Unique              bool   // one-to-one or many-to-one
	Required            bool   // the foreign key may not be NULL
	Immutable           bool   // the edge cannot change after create
	Field               string // the foreign-key field this edge binds
	OnDelete            string // "CASCADE", "SET NULL", "RESTRICT" or "NO ACTION"; "" is unset
	Table               string // the join table or the foreign-key table
	Columns             []string
	Symbols             []string
	Comment             string
	Annotations         []Annotation
	PC                  uintptr // the constructor's call, for PosOf
	Errs                []error // every misuse of the builder, each a reason without the edge's name
}

// Index is the descriptor of one index. The index package's builders fill it.
type Index struct {
	Fields, Edges []string // the columns and the edges whose foreign keys the index covers
	Unique        bool
	StorageKey    string // the index name
	Where         string // a partial index's predicate, on PostgreSQL and SQLite
	Annotations   []Annotation
	PC            uintptr // the constructor's call, for PosOf
}
