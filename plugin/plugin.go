package plugin

import "encoding/json"

// ProtocolVersion is the version of the plugin protocol, Request.Protocol: the
// shape of the request and the response, and the rules for a plugin's files. A
// plugin refuses a request whose Protocol it does not know.
const ProtocolVersion = 1

// ModelVersion is the version of the model's JSON, Model.Version. It changes
// only when a field is removed or changes meaning; added fields do not change
// it. A plugin refuses a model whose Version it does not know.
const ModelVersion = 2

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
	TableSchema string                     `json:"table_schema,omitempty"` // the database schema that holds Table, from schema.TableIn; "" for the connection's default
	Join        bool                       `json:"join,omitempty"`         // a many-to-many join entity
	Comment     string                     `json:"comment,omitempty"`      // the Comment annotation's text, for descriptions
	Fields      []*Field                   `json:"fields"`                 // Fields[0] is the ID
	Edges       []*Edge                    `json:"edges"`
	Indexes     []*Index                   `json:"indexes"`     // the declared indexes, in declaration order (mixins' first); a field's Unique is no index here
	PrimaryKey  []int                      `json:"primary_key"` // indexes into Fields
	Tenant      int                        `json:"tenant"`      // the index in Fields of the tenant field, -1 for none
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Index is an index of an entity's table.
type Index struct {
	Name   string   `json:"name"`   // the name the database gets: the StorageKey, else <table>_<columns>[_unique][_<hash of Where>]
	Fields []string `json:"fields"` // the Names of the fields it covers, in order; an edge's foreign key is its field, implicit or not
	Unique bool     `json:"unique,omitempty"`
	Where  string   `json:"where,omitempty"` // a partial index's predicate, as SQL
}

// Check is a built-in validator on a field, as the schema declares it.
type Check struct {
	Kind string            `json:"kind"`           // NotEmpty, MinLen, MaxLen, Match, Range, Min, Max, Positive, Negative, NonNegative
	Args []json.RawMessage `json:"args,omitempty"` // MinLen/MaxLen: [n]; Match: [pattern]; Range: [min, max]; Min/Max: [v]
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
	Imports       []string                   `json:"imports,omitempty"`  // every import path ElemType mentions (map[string]x.T's x too), sorted; "time" for a time field without a Go type; nil for a builtin type and velox-gen's own enum types
	Kind          string                     `json:"kind,omitempty"`     // the reflect kind of the schema's GoType ("string", "int64", "struct", "array", "slice", "ptr"…) when the schema gives one; "" for velox's standard type and velox-gen's own enum types
	Pointer       bool                       `json:"pointer,omitempty"`  // the Go struct holds a pointer: the schema's Nillable, unless the Go type is nil-able already (a slice, a map); this is about the struct: use Nullable for nullability
	Nullable      bool                       `json:"nullable,omitempty"` // Nillable || (Optional && Type == "json"): the column is NULL-able; the flag a plugin uses for nullability. An Optional field that is not Nillable stays NOT NULL, and unset is its zero value
	Optional      bool                       `json:"optional,omitempty"` // the field need not be given on create; it says nothing of nullability
	Immutable     bool                       `json:"immutable,omitempty"`
	Sensitive     bool                       `json:"sensitive,omitempty"`
	Unique        bool                       `json:"unique,omitempty"`
	AutoID        bool                       `json:"auto_id,omitempty"` // the ID velox added, as the schema declares none
	Size          int                        `json:"size,omitempty"`    // the column's size, from MaxLen; 0 for none
	HasDefault    bool                       `json:"has_default,omitempty"`
	Default       json.RawMessage            `json:"default,omitempty"` // the literal Default as JSON, an enum's stored value; absent for none and for a func default
	UpdateDefault bool                       `json:"update_default,omitempty"`
	Implicit      string                     `json:"implicit,omitempty"`    // the edge whose foreign key this field is, when velox added it
	Enum          string                     `json:"enum,omitempty"`        // the enum type's name: the type velox-gen declares, or for a custom Go type that type's name without its package; "" when not an enum
	EnumValues    []string                   `json:"enum_values,omitempty"` // the stored values in declaration order, for every field of Type "enum"
	Checks        []Check                    `json:"checks,omitempty"`      // the built-in validators, in declaration order; custom validators are not in the model
	SchemaType    map[string]string          `json:"schema_type,omitempty"` // the column type per dialect ("postgres", "mysql", "sqlite"), when the schema sets one
	Comment       string                     `json:"comment,omitempty"`     // for descriptions
	Annotations   map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Edge is an edge of an entity.
type Edge struct {
	Name      string   `json:"name"`
	GoName    string   `json:"go_name"`
	Target    string   `json:"target"` // the target entity's Name
	Ref       string   `json:"ref,omitempty"`
	Kind      EdgeKind `json:"kind"`
	Unique    bool     `json:"unique,omitempty"`
	Required  bool     `json:"required,omitempty"`
	Inverse   bool     `json:"inverse,omitempty"`
	Immutable bool     `json:"immutable,omitempty"`
	Field     string   `json:"field,omitempty"`    // the foreign-key field's Name, on whichever entity holds it; "" for M2M
	FieldOn   string   `json:"field_on,omitempty"` // the Name of the entity whose table holds Field; "" for M2M
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

// EdgeKind is the kind of relation an edge stands for.
type EdgeKind string

// The edge kinds.
const (
	O2O EdgeKind = "O2O" // one to one
	O2M EdgeKind = "O2M" // one to many: the targets' rows hold the key
	M2O EdgeKind = "M2O" // many to one: the edge's own row holds the key
	M2M EdgeKind = "M2M" // many to many, through a join entity
)

// Enum is an enum type velox-gen declares.
type Enum struct {
	Type   string   `json:"type"`
	Values []string `json:"values"` // stored values, in declaration order
}
