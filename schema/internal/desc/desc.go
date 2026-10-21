// Package desc holds the descriptors the schema DSL builds: the plain data a
// field, an edge or an index declares. The DSL packages fill them, the loader
// reads them, and none of them runs anything. The package imports only the
// standard library.
package desc

import (
	"reflect"
	"runtime"
	"slices"
	"strconv"
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
}

// GoTypeOf describes t. Ident is t.String() and Kind is t.Kind().String().
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
	return g
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
	}
	return out
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
	Unique        bool    // a single-column unique index
	Sensitive     bool    // left out of String(), JSON, logs and spans
	Tenant        bool    // the tenant column
	Default       any     // a value (or, on time and uuid, a func() T); nil when none
	DefaultFunc   any     // a func() T; nil when none
	UpdateDefault any     // a func() T; nil when none
	Enums         []Enum
	Checks        []Check
	Validators    []any // Validate's functions, each a func(T) error
	StorageKey    string
	SchemaType    map[string]string
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
	Where         string // a partial index's predicate, on PostgreSQL
	Annotations   []Annotation
	PC            uintptr // the constructor's call, for PosOf
}
