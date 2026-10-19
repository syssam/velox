package plugin

import "encoding/json"

// Version is the protocol version, sent as Request.Protocol and Model.Version.
const Version = 1

// Request is what velox-gen writes to a plugin's standard input.
type Request struct {
	Protocol   int    `json:"plugin_protocol"`
	OutPackage string `json:"out_package"` // the generated package's name
	Model      *Model `json:"model"`
}

// Response is what a plugin writes to its standard output.
type Response struct {
	Files []File `json:"files"`
	Error string `json:"error,omitempty"` // a non-empty Error fails the run
}

// File is one file of a plugin's output.
type File struct {
	Path    string `json:"path"`    // relative to the output directory, no directory part
	Content string `json:"content"` // without the generated-file header
}

// Model is the resolved model of a schema package, as velox-gen generates it.
type Model struct {
	Version    int       `json:"version"`
	SchemaPath string    `json:"schema_path"` // the schema package's import path
	Entities   []*Entity `json:"entities"`    // the schemas', then the join entities', each sorted by Name
	Enums      []*Enum   `json:"enums"`       // sorted by Type
}

// Entity is a schema, or a many-to-many join entity.
type Entity struct {
	Name        string                     `json:"name"`
	Label       string                     `json:"label"`   // snake_case of Name
	Handles     string                     `json:"handles"` // the plural of Name, e.g. "Users"
	Table       string                     `json:"table"`
	Join        bool                       `json:"join,omitempty"` // a many-to-many join entity
	Fields      []*Field                   `json:"fields"`         // Fields[0] is the ID
	Edges       []*Edge                    `json:"edges"`
	PrimaryKey  []int                      `json:"primary_key"` // indexes into Fields
	Tenant      int                        `json:"tenant"`      // the index in Fields of the tenant field, -1 for none
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Field is a column of an entity.
type Field struct {
	Name          string                     `json:"name"`
	GoName        string                     `json:"go_name"`
	Column        string                     `json:"column"`
	Type          string                     `json:"type"`      // the schema field type, e.g. "string", "enum", "time"
	GoType        string                     `json:"go_type"`   // as written in the entity's struct, e.g. "*time.Time"
	ElemType      string                     `json:"elem_type"` // GoType without the pointer
	Pointer       bool                       `json:"pointer,omitempty"`
	Optional      bool                       `json:"optional,omitempty"`
	Nillable      bool                       `json:"nillable,omitempty"`
	Immutable     bool                       `json:"immutable,omitempty"`
	Sensitive     bool                       `json:"sensitive,omitempty"`
	Unique        bool                       `json:"unique,omitempty"`
	AutoID        bool                       `json:"auto_id,omitempty"` // the ID velox added, as the schema declares none
	HasDefault    bool                       `json:"has_default,omitempty"`
	UpdateDefault bool                       `json:"update_default,omitempty"`
	Implicit      string                     `json:"implicit,omitempty"` // the edge whose foreign key this field is, when velox added it
	Enum          string                     `json:"enum,omitempty"`     // the enum type's name, "" when the field is not an enum velox-gen declares
	Annotations   map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Edge is an edge of an entity.
type Edge struct {
	Name        string                     `json:"name"`
	GoName      string                     `json:"go_name"`
	Target      string                     `json:"target"` // the target entity's Name
	Ref         string                     `json:"ref,omitempty"`
	Kind        string                     `json:"kind"` // "O2O", "O2M", "M2O" or "M2M"
	Unique      bool                       `json:"unique,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Inverse     bool                       `json:"inverse,omitempty"`
	Immutable   bool                       `json:"immutable,omitempty"`
	Field       string                     `json:"field,omitempty"` // the foreign-key field's Name, on whichever entity holds it; "" for M2M
	Join        string                     `json:"join,omitempty"`  // the join entity's Name for M2M, else ""
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Enum is an enum type velox-gen declares.
type Enum struct {
	Type   string   `json:"type"`
	Values []string `json:"values"` // stored values, in declaration order
}
