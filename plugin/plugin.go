package plugin

import "encoding/json"

// ProtocolVersion is the version of the plugin protocol, Request.Protocol and
// Response.Protocol: the shape of the request and the response, and the rules
// for a plugin's files. A plugin refuses a request whose Protocol it does not
// know, and velox-gen a response whose Protocol it does not know.
const ProtocolVersion = 2

// ModelVersion is the version of the model's JSON, Model.Version. It changes
// only when a field is removed or changes meaning; added fields do not change
// it. A plugin refuses a model whose Version it does not know.
const ModelVersion = 3

// Request is what velox-gen writes to a plugin's standard input.
type Request struct {
	Protocol   int    `json:"plugin_protocol"` // the protocol version, [ProtocolVersion]; a plugin checks it
	Generator  string `json:"generator"`       // "velox-gen " and the version of the velox module, e.g. "velox-gen v1.2.3", "velox-gen (devel)"; for messages, not for output
	OutPackage string `json:"out_package"`     // the generated package's name
	OutPath    string `json:"out_path"`        // the generated package's import path
	// Options are the plugin's options from the command line,
	// -plugin name:key=value,key=value; absent when there are none. Keys are
	// non-empty and distinct, a value may be empty, and neither holds a comma.
	Options map[string]string `json:"options,omitempty"`
	Model   *Model            `json:"model"` // the resolved model of the schema package
}

// Response is what a plugin writes to its standard output.
type Response struct {
	Protocol int      `json:"plugin_protocol"`    // the protocol the plugin speaks, [ProtocolVersion]; velox-gen refuses a response without it
	Files    []File   `json:"files"`              // written below the output directory; see [File]
	Warnings []string `json:"warnings,omitempty"` // shown to the user; they never fail the run
	Error    string   `json:"error,omitempty"`    // a non-empty Error fails the run
}

// File is one file of a plugin's output.
type File struct {
	// Path is a slash-separated path relative to the output directory, such
	// as "velox.graphqls" or "pb/user.proto". Its segments are not empty, ".",
	// or "..", its directories do not start with "." or "_" and are not
	// testdata or vendor, it holds no backslash or NUL, and it ends in .go,
	// .graphqls or .proto; it is ASCII, and a .go file's name is one go
	// build always builds (not _x.go, .x.go, x_test.go or x_linux.go), as
	// are its build constraints (not //go:build ignore, linux or prod).
	Path    string `json:"path"`
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
	Name        string   `json:"name"`                   // the Go type name, e.g. "User": the schema's struct name, or for a join entity the one velox-gen gives it
	Label       string   `json:"label"`                  // snake_case of Name
	Handles     string   `json:"handles"`                // GoPlural of Name, e.g. "Users", "StatusSlice"
	Table       string   `json:"table"`                  // the table's name
	TableSchema string   `json:"table_schema,omitempty"` // the database schema that holds Table, from schema.TableIn; "" for the connection's default
	Join        bool     `json:"join,omitempty"`         // a many-to-many join entity
	Comment     string   `json:"comment,omitempty"`      // the Comment annotation's text, for descriptions
	Fields      []*Field `json:"fields"`                 // Fields[0] is the ID
	Edges       []*Edge  `json:"edges"`
	// Indexes are the declared indexes, in declaration order (mixins'
	// first); a field's Unique is no index here. A join entity has none: its
	// PrimaryKey is what makes its rows unique.
	Indexes     []*Index                   `json:"indexes"`
	PrimaryKey  []int                      `json:"primary_key"`           // indexes into Fields
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"` // the schema's annotations by name, each the JSON of its value; absent on a join entity
}

// Index is an index of an entity's table.
type Index struct {
	Name   string   `json:"name"`             // the StorageKey, else ent's: lower(entity)_<columns>, with _<hash of Where> for a partial index whose name another index has. A derived name over 63 bytes is capped as ent caps it on PostgreSQL: its first 30 bytes, "_", and the md5 of the whole name. MySQL and SQLite cap at 64 bytes, so a derived name of exactly 64 bytes is capped only on PostgreSQL.
	Fields []string `json:"fields"`           // the Names of the fields it covers, in order; an edge's foreign key is its field, implicit or not
	Unique bool     `json:"unique,omitempty"` // a unique index
	Where  string   `json:"where,omitempty"`  // a partial index's predicate, as SQL
}

// Check is a built-in validator on a field, as the schema declares it.
type Check struct {
	Kind string            `json:"kind"`           // NotEmpty, MinLen, MaxLen, Match, Range, Min, Max, Positive, Negative, NonNegative
	Args []json.RawMessage `json:"args,omitempty"` // MinLen/MaxLen: [n]; Match: [pattern]; Range: [min, max]; Min/Max: [v]
}

// Field is a column of an entity.
//
// Type is the schema field type, one of:
//
//   - "bool";
//   - "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16",
//     "uint32", "uint64", "float32", "float64": a number, by its Go kind
//     (field.Number[T] and a GoType of a defined number type keep their
//     kind's Type);
//   - "string", from field.String and field.Text;
//   - "bytes": a []byte;
//   - "time": a time.Time;
//   - "uuid": a UUID type of the schema's, which implements driver.Valuer;
//   - "json": a Go value stored as JSON;
//   - "enum": a string stored as one of EnumValues;
//   - "other": a type of the schema's with its own SQL type (SchemaType).
//
// When the schema gives the field a Go type, ElemType is that type, Kind its
// reflect kind and PkgPath its package; Type stays the schema type. A later
// model may add a Type value without a new [ModelVersion]: a plugin refuses a
// Type it does not know with an error, or skips the field with a warning.
type Field struct {
	Name       string   `json:"name"`                  // the schema's field name, e.g. "created_at"
	GoName     string   `json:"go_name"`               // the struct field's name, e.g. "CreatedAt"
	Column     string   `json:"column"`                // the column's name: the StorageKey, else Name
	Type       string   `json:"type"`                  // the schema field type, one of those listed above
	GoType     string   `json:"go_type"`               // as written in the entity's struct, e.g. "*time.Time"
	ElemType   string   `json:"elem_type"`             // GoType without the pointer
	PkgPath    string   `json:"pkg_path,omitempty"`    // the import path of ElemType when it is a named type of another package, "time" for time.Time; "" for a builtin type, an unnamed one and velox-gen's own enum types
	Imports    []string `json:"imports,omitempty"`     // every import path ElemType mentions (map[string]x.T's x too), sorted; "time" for a time field without a Go type; nil for a builtin type and velox-gen's own enum types
	Kind       string   `json:"kind,omitempty"`        // the reflect kind of the schema's GoType ("string", "int64", "struct", "array", "slice", "ptr"…) when the schema gives one; "" for velox's standard type and velox-gen's own enum types
	Pointer    bool     `json:"pointer,omitempty"`     // Nillable, unless the Go type can already be nil (a slice, a map): the Go struct holds a pointer. This is about the struct: use Nullable for nullability
	Nullable   bool     `json:"nullable,omitempty"`    // Nillable || (Optional && Type == "json" && the Go type is a slice, a map or a pointer): the column is NULL-able; the flag a plugin uses for nullability. Any other Optional field stays NOT NULL, and unset is its zero value
	Optional   bool     `json:"optional,omitempty"`    // the field need not be given on create; it says nothing of nullability
	Immutable  bool     `json:"immutable,omitempty"`   // refused in updates: set only on create
	Sensitive  bool     `json:"sensitive,omitempty"`   // left out of String(), JSON, logs and spans; a plugin keeps it out of what it exposes
	Unique     bool     `json:"unique,omitempty"`      // a single-column unique index, which Entity.Indexes does not list
	AutoID     bool     `json:"auto_id,omitempty"`     // the ID velox added, as the schema declares none
	Size       int      `json:"size,omitempty"`        // the column's size, from MaxLen (bytes, string), or math.MaxInt32 for field.Text; 0 for none
	HasDefault bool     `json:"has_default,omitempty"` // an insert that does not set the field gets a value: a literal Default or a func
	// Default is the field's literal Default, as the JSON of its base value,
	// for these Types only:
	//   - "bool": a JSON bool;
	//   - a number type ("int", "int64", "float64", ...): a JSON number;
	//   - "string": a JSON string;
	//   - "enum": the stored value, a JSON string.
	// It is absent for every other Type (bytes, json, other; time and uuid
	// defaults are always funcs), for a func default (HasDefault tells), and
	// for a Go type with its own JSON or text encoding, whose JSON need not
	// be its base value. A plugin treats the database default of a field
	// without a Default as unknown.
	Default       json.RawMessage            `json:"default,omitempty"`
	UpdateDefault bool                       `json:"update_default,omitempty"` // an update that does not set the field gets a value from a func
	Implicit      string                     `json:"implicit,omitempty"`       // the edge whose foreign key this field is, when velox added it
	Enum          string                     `json:"enum,omitempty"`           // the enum type's name: the type velox-gen declares, or for a custom Go type that type's name without its package; "" when not an enum
	EnumValues    []string                   `json:"enum_values,omitempty"`    // the stored values in declaration order, for every field of Type "enum"
	Checks        []Check                    `json:"checks,omitempty"`         // the built-in validators, in declaration order; custom validators are not in the model
	SchemaType    map[string]string          `json:"schema_type,omitempty"`    // the column type per dialect ("postgres", "mysql", "sqlite"), when the schema sets one
	Comment       string                     `json:"comment,omitempty"`        // for descriptions
	Annotations   map[string]json.RawMessage `json:"annotations,omitempty"`    // the field's annotations by name, each the JSON of its value
}

// Edge is an edge of an entity. An edge declared with edge.To and the inverse
// edge.From(...).Ref(...) that mirrors it are a pair: one relation, which each
// entity lists from its own side, with the other's Name in Ref.
type Edge struct {
	Name      string   `json:"name"`                // the schema's edge name, e.g. "posts"
	GoName    string   `json:"go_name"`             // its Go name, e.g. "Posts"
	Target    string   `json:"target"`              // the target entity's Name
	Ref       string   `json:"ref,omitempty"`       // the Name of the paired edge on Target, set on both edges of a pair; "" for an edge without an inverse
	Kind      EdgeKind `json:"kind"`                // the relation, seen from this edge's entity
	Unique    bool     `json:"unique,omitempty"`    // the edge points to at most one row (O2O, M2O)
	Required  bool     `json:"required,omitempty"`  // such a row is created with its foreign key (Field, on FieldOn's table) set: the key is NOT NULL, or, on an edge to its own entity, NULL-able as in ent, and velox refuses it nil on insert and update (velox.ColumnDescriptor.RequiredEdge)
	Inverse   bool     `json:"inverse,omitempty"`   // the edge is the inverse of a pair, declared with edge.From
	Immutable bool     `json:"immutable,omitempty"` // the edge is set on create and refused in updates
	Field     string   `json:"field,omitempty"`     // the foreign-key field's Name, on whichever entity holds it; "" for M2M
	FieldOn   string   `json:"field_on,omitempty"`  // the Name of the entity whose table holds Field; "" for M2M
	// OwnsKey is true when the edge's own entity holds the foreign key on its
	// own row: an M2O edge, an inverse O2O edge, or a bidirectional O2O edge
	// (an O2O from an entity to itself with no inverse). Writing such an edge
	// sets the entity's own key; writing any other edge re-points the
	// target's rows (O2M, and an O2O whose key the target holds) or join
	// rows (M2M). For an edge from an entity to itself FieldOn names the
	// entity either way, so only OwnsKey tells.
	OwnsKey     bool                       `json:"owns_key,omitempty"`
	Comment     string                     `json:"comment,omitempty"`     // for descriptions
	Join        string                     `json:"join,omitempty"`        // the join entity's Name for M2M, else ""
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"` // the edge's annotations by name, each the JSON of its value
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
	Type   string   `json:"type"`   // the Go type's name in the generated package, e.g. "UserRole"; a Field's Enum names it
	Values []string `json:"values"` // stored values, in declaration order
}
