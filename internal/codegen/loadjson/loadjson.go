// Package loadjson holds the load model: the versioned JSON that schema/load's
// Marshal writes and velox-gen reads back. The program velox-gen writes imports
// the schema package, calls Marshal and prints the model. The format is
// internal to velox: it changes with Version, and only velox-gen reads it.
package loadjson

import "encoding/json"

// Version is the version of the Model's JSON format.
const Version = 7

// SchemaPath is the import path of the schema DSL package. velox-gen uses it
// to find the schemas of a package by their embedding of schema.Schema.
const SchemaPath = "github.com/syssam/velox/schema"

// Model describes the schemas of one package.
type Model struct {
	Version int       `json:"version"`
	Package string    `json:"package"`           // the schema package's import path
	Schemas []*Schema `json:"schemas"`           // sorted by Name
	Errors  []Error   `json:"errors,omitempty"`  // every misuse Marshal found, sorted by Schema
	Options *Options  `json:"options,omitempty"` // the package's schema.Package; nil for the zero one
}

// Options is a schema package's schema.Package.
type Options struct {
	PrefixIndexNames bool   `json:"prefix_index_names,omitempty"`
	Names            string `json:"names,omitempty"` // "veloxe", or "" for ent's
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
	// Mixins are the values its Mixin method returns, in order.
	Mixins []Mixin `json:"mixins,omitempty"`
	// Pointer says the schema implements schema.Interface only through a
	// pointer, so the generated code names &schema.<Name>{}.
	Pointer bool `json:"pointer,omitempty"`
}

// Mixin is one value of a schema's Mixin method: its type, by which
// velox-gen finds the behaviour methods it declares in its package's
// source.
type Mixin struct {
	PkgPath string `json:"pkg_path,omitempty"` // "" for a type with no name
	Name    string `json:"name,omitempty"`     // the type's name, without type arguments
	Generic bool   `json:"generic,omitempty"`  // an instance of a generic type
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
	Ident     string   `json:"ident"`
	PkgPath   string   `json:"pkg_path,omitempty"`
	Kind      string   `json:"kind"`
	Imports   []string `json:"imports,omitempty"`
	Marshaler bool     `json:"marshaler,omitempty"` // the type, or a pointer to it, has its own JSON or text encoding
}

// FuncRef is a function value of a schema. Pkg and Name are set when it is
// an exported function declared at package level, which generated code can
// call as name.Name if it can import Pkg; Why says why not otherwise. Type
// is the function's type, as reflect prints it.
type FuncRef struct {
	Pkg  string `json:"pkg,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Why  string `json:"why,omitempty"`
}

// Field describes one field.
type Field struct {
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Size          int             `json:"size,omitempty"`
	GoType        *GoType         `json:"go_type,omitempty"`
	Optional      bool            `json:"optional,omitempty"`
	Nillable      bool            `json:"nillable,omitempty"`
	Immutable     bool            `json:"immutable,omitempty"`
	Version       bool            `json:"version,omitempty"`
	Unique        bool            `json:"unique,omitempty"`
	Sensitive     bool            `json:"sensitive,omitempty"`
	Default       json.RawMessage `json:"default,omitempty"`      // a literal Default
	DefaultFunc   bool            `json:"default_func,omitempty"` // DefaultFunc, or a func Default on time/uuid
	UpdateDefault bool            `json:"update_default,omitempty"`
	Enums         []Enum          `json:"enums,omitempty"`
	Checks        []Check         `json:"checks,omitempty"`
	Validators    int             `json:"validators,omitempty"` // the number of custom validators
	// DefaultRef, UpdateDefaultRef and ValidatorRefs describe the function
	// of the DefaultFunc (or a func Default), of the UpdateDefault and of
	// each validator: by name, when generated code can call it so.
	DefaultRef       *FuncRef                   `json:"default_ref,omitempty"`
	UpdateDefaultRef *FuncRef                   `json:"update_default_ref,omitempty"`
	ValidatorRefs    []FuncRef                  `json:"validator_refs,omitempty"` // one per validator
	StorageKey       string                     `json:"storage_key,omitempty"`
	SchemaType       map[string]string          `json:"schema_type,omitempty"`
	SchemaDefault    map[string]string          `json:"schema_default,omitempty"`
	Comment          string                     `json:"comment,omitempty"`
	StructTag        string                     `json:"struct_tag,omitempty"`
	Annotations      map[string]json.RawMessage `json:"annotations,omitempty"`
	Position         Position                   `json:"position"`
	Pos              string                     `json:"pos,omitempty"` // file:line of the constructor's call
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
	OnDelete    string                     `json:"on_delete,omitempty"`
	Table       string                     `json:"table,omitempty"`
	Columns     []string                   `json:"columns,omitempty"`
	Symbols     []string                   `json:"symbols,omitempty"`
	Comment     string                     `json:"comment,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	Position    Position                   `json:"position"`
	Inline      bool                       `json:"inline,omitempty"` // an inverse declared with To(...).From(...)
	Pos         string                     `json:"pos,omitempty"`    // file:line of the constructor's call
}

// OpClass is the operator class of one field or edge an index covers.
type OpClass struct {
	Name  string `json:"name"`
	Class string `json:"class"`
}

// Index describes one index.
type Index struct {
	Fields      []string                   `json:"fields,omitempty"`
	Edges       []string                   `json:"edges,omitempty"`
	Unique      bool                       `json:"unique,omitempty"`
	StorageKey  string                     `json:"storage_key,omitempty"`
	Where       string                     `json:"where,omitempty"`
	Method      string                     `json:"method,omitempty"`
	Desc        []string                   `json:"desc,omitempty"`
	OpClass     []OpClass                  `json:"op_class,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	Position    Position                   `json:"position"`
	Pos         string                     `json:"pos,omitempty"` // file:line of the constructor's call
}
