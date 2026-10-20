package plugin

import "encoding/json"

// ProtocolVersion is the version of the plugin protocol, Request.Protocol: the
// shape of the request and the response, and the rules for a plugin's files. A
// plugin refuses a request whose Protocol it does not know.
const ProtocolVersion = 1

// ModelVersion is the version of the model's JSON, Model.Version. It changes
// only when a field is removed or changes meaning; added fields do not change
// it. A plugin refuses a model whose Version it does not know.
const ModelVersion = 1

// Request is what velox-gen writes to a plugin's standard input.
type Request struct {
	Protocol   int    `json:"plugin_protocol"` // the protocol version, [ProtocolVersion]; a plugin checks it
	OutPackage string `json:"out_package"`     // the generated package's name
	OutPath    string `json:"out_path"`        // the generated package's import path
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
	Version    int       `json:"version"`     // the version of the model's JSON, [ModelVersion]
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
	Join        bool                       `json:"join,omitempty"`    // a many-to-many join entity
	Comment     string                     `json:"comment,omitempty"` // the Comment annotation's text, for descriptions
	Imports     []string                   `json:"imports"`           // the generated file's imports besides velox and the schema package, check code's included (errors, regexp, ...), sorted; paths only, no qualifiers
	Fields      []*Field                   `json:"fields"`            // Fields[0] is the ID
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
	Type          string                     `json:"type"`               // the schema field type, e.g. "string", "enum", "time"
	GoType        string                     `json:"go_type"`            // as written in the entity's struct, e.g. "*time.Time"
	ElemType      string                     `json:"elem_type"`          // GoType without the pointer
	PkgPath       string                     `json:"pkg_path,omitempty"` // the import path of ElemType when it is a named type of another package, "time" for time.Time; "" for a builtin type, an unnamed one and velox-gen's own enum types
	Kind          string                     `json:"kind,omitempty"`     // the reflect kind of the schema's GoType ("string", "int64", "struct", "array", "slice", "ptr"…) when the schema gives one; "" for velox's standard type and velox-gen's own enum types
	Pointer       bool                       `json:"pointer,omitempty"`  // the Go struct holds a pointer: Nillable, unless the Go type is nil-able already (a slice, a map); this is about the struct: use Nullable for nullability
	Nullable      bool                       `json:"nullable,omitempty"` // Nillable || (Optional && Type == "json"): the column is NULL-able; the flag a plugin uses for nullability. An Optional field that is not Nillable stays NOT NULL, and unset is its zero value
	Optional      bool                       `json:"optional,omitempty"`
	Nillable      bool                       `json:"nillable,omitempty"`
	Immutable     bool                       `json:"immutable,omitempty"`
	Sensitive     bool                       `json:"sensitive,omitempty"`
	Unique        bool                       `json:"unique,omitempty"`
	AutoID        bool                       `json:"auto_id,omitempty"` // the ID velox added, as the schema declares none
	HasDefault    bool                       `json:"has_default,omitempty"`
	UpdateDefault bool                       `json:"update_default,omitempty"`
	Implicit      string                     `json:"implicit,omitempty"`    // the edge whose foreign key this field is, when velox added it
	Enum          string                     `json:"enum,omitempty"`        // the enum type's name: the type velox-gen declares, or for a custom Go type that type's name without its package; "" when not an enum
	EnumValues    []string                   `json:"enum_values,omitempty"` // the stored values in declaration order, for every field of Type "enum"
	Comment       string                     `json:"comment,omitempty"`     // for descriptions
	Annotations   map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Edge is an edge of an entity.
type Edge struct {
	Name      string `json:"name"`
	GoName    string `json:"go_name"`
	Target    string `json:"target"` // the target entity's Name
	Ref       string `json:"ref,omitempty"`
	Kind      string `json:"kind"` // "O2O", "O2M", "M2O" or "M2M"
	Unique    bool   `json:"unique,omitempty"`
	Required  bool   `json:"required,omitempty"`
	Inverse   bool   `json:"inverse,omitempty"`
	Immutable bool   `json:"immutable,omitempty"`
	Field     string `json:"field,omitempty"`    // the foreign-key field's Name, on whichever entity holds it; "" for M2M
	FieldOn   string `json:"field_on,omitempty"` // the Name of the entity whose table holds Field; "" for M2M
	// OwnsKey is true when the edge's own entity holds the foreign key on its
	// own row: an M2O edge, an inverse O2O edge, or a bidirectional O2O edge
	// (an O2O from an entity to itself with no inverse). Writing such an edge
	// sets the entity's own key; writing any other edge re-points the
	// target's rows (O2M, and an O2O whose key the target holds) or join
	// rows (M2M). For an edge from an entity to itself FieldOn names the
	// entity either way, so only OwnsKey tells.
	OwnsKey     bool                       `json:"owns_key,omitempty"`
	Comment     string                     `json:"comment,omitempty"` // for descriptions
	Join        string                     `json:"join,omitempty"`    // the join entity's Name for M2M, else ""
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Enum is an enum type velox-gen declares.
type Enum struct {
	Type   string   `json:"type"`
	Values []string `json:"values"` // stored values, in declaration order
}
