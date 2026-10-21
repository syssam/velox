// Package loadjson holds the load model: the versioned JSON that schema/load's
// Marshal writes and velox-gen reads back. The program velox-gen writes imports
// the schema package, calls Marshal and prints the model. The format is
// internal to velox2: it changes with Version, and only velox-gen reads it.
package loadjson

import "encoding/json"

// Version is the version of the Model's JSON format.
const Version = 2

// SchemaPath is the import path of the schema DSL package. velox-gen uses it
// to find the schemas of a package by their embedding of schema.Schema.
const SchemaPath = "github.com/syssam/velox2/schema"

// Model describes the schemas of one package.
type Model struct {
	Version int       `json:"version"`
	Package string    `json:"package"`          // the schema package's import path
	Schemas []*Schema `json:"schemas"`          // sorted by Name
	Errors  []Error   `json:"errors,omitempty"` // every misuse Marshal found, sorted by Schema
}

// Error is one misuse of the DSL: a builder's, a schema declared twice, or a
// method a schema must not have. Pos is the file:line of the element, or "".
// Where is `field "name"`, `edge "posts"`, or "".
type Error struct {
	Pos    string `json:"pos,omitempty"`
	Schema string `json:"schema"`
	Where  string `json:"where,omitempty"`
	Reason string `json:"reason"`
}

// Position says where an element was declared: its index in the list that
// declared it and, for an element a mixin contributed, which mixin.
type Position struct {
	Index      int  `json:"index"` // in the Fields/Edges/Indexes list that declared it
	MixedIn    bool `json:"mixed_in,omitempty"`
	MixinIndex int  `json:"mixin_index,omitempty"` // in the schema's Mixin list
}

// Schema describes one schema, with the elements of its mixins merged in.
type Schema struct {
	Name        string                     `json:"name"`
	Fields      []*Field                   `json:"fields,omitempty"`  // the mixins' first, in mixin order, then the schema's own
	Edges       []*Edge                    `json:"edges,omitempty"`   // likewise
	Indexes     []*Index                   `json:"indexes,omitempty"` // likewise
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Check is a built-in validator on a field.
type Check struct {
	Kind string            `json:"kind"`
	Args []json.RawMessage `json:"args,omitempty"`
}

// Enum is one value of an enum field.
type Enum struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GoType is the Go type a field is stored as, when it is not the standard one.
type GoType struct {
	Ident   string   `json:"ident"`
	PkgPath string   `json:"pkg_path,omitempty"`
	Kind    string   `json:"kind"`
	Imports []string `json:"imports,omitempty"`
}

// Field describes one field.
type Field struct {
	Name          string                     `json:"name"`
	Type          string                     `json:"type"`
	Size          int                        `json:"size,omitempty"`
	GoType        *GoType                    `json:"go_type,omitempty"`
	Optional      bool                       `json:"optional,omitempty"`
	Nillable      bool                       `json:"nillable,omitempty"`
	Immutable     bool                       `json:"immutable,omitempty"`
	Unique        bool                       `json:"unique,omitempty"`
	Sensitive     bool                       `json:"sensitive,omitempty"`
	Tenant        bool                       `json:"tenant,omitempty"`
	Default       json.RawMessage            `json:"default,omitempty"`      // a literal Default
	DefaultFunc   bool                       `json:"default_func,omitempty"` // DefaultFunc, or a func Default on time/uuid
	UpdateDefault bool                       `json:"update_default,omitempty"`
	Enums         []Enum                     `json:"enums,omitempty"`
	Checks        []Check                    `json:"checks,omitempty"`
	Validators    int                        `json:"validators,omitempty"` // the number of custom validators
	StorageKey    string                     `json:"storage_key,omitempty"`
	SchemaType    map[string]string          `json:"schema_type,omitempty"`
	Comment       string                     `json:"comment,omitempty"`
	StructTag     string                     `json:"struct_tag,omitempty"`
	Annotations   map[string]json.RawMessage `json:"annotations,omitempty"`
	Position      Position                   `json:"position"`
	Pos           string                     `json:"pos,omitempty"` // file:line of the constructor's call
}

// Edge describes one edge. An inverse declared inline, with To(...).From(...),
// is a second Edge right after its assoc edge, with Inline set.
type Edge struct {
	Name        string                     `json:"name"`
	Type        string                     `json:"type"`
	TypePkg     string                     `json:"type_pkg"`
	Inverse     bool                       `json:"inverse,omitempty"`
	Ref         string                     `json:"ref,omitempty"` // an inverse's assoc edge on Type
	Unique      bool                       `json:"unique,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Immutable   bool                       `json:"immutable,omitempty"`
	Field       string                     `json:"field,omitempty"`
	Table       string                     `json:"table,omitempty"`
	Columns     []string                   `json:"columns,omitempty"`
	Symbols     []string                   `json:"symbols,omitempty"`
	Comment     string                     `json:"comment,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	Position    Position                   `json:"position"`
	Inline      bool                       `json:"inline,omitempty"` // an inverse declared with To(...).From(...)
	Pos         string                     `json:"pos,omitempty"`    // file:line of the constructor's call
}

// Index describes one index.
type Index struct {
	Fields      []string                   `json:"fields,omitempty"`
	Edges       []string                   `json:"edges,omitempty"`
	Unique      bool                       `json:"unique,omitempty"`
	StorageKey  string                     `json:"storage_key,omitempty"`
	Where       string                     `json:"where,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	Position    Position                   `json:"position"`
	Pos         string                     `json:"pos,omitempty"` // file:line of the constructor's call
}
