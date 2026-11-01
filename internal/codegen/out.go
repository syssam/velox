package codegen

// Config says where generated code goes and what the user's files declare.
type Config struct {
	Package    string           // the generated package's name; "gen" when empty
	SchemaPath string           // the schema package's import path; Graph.Package when empty
	Bound      map[string]Bound // per entity name: which methods the user's files declare
	UserDoc    bool             // a user's file has the package comment; velox.go then has none
}

// Bound says which of an entity's methods the user's files declare, so that
// its Desc binds them.
type Bound struct {
	Policy, Hooks bool
	// PolicyConds is "path:line" of the first Read or Write key of a
	// velox.Policy literal in the Policy method's body, "" when it has none.
	// The join entity of a bidirectional edge cannot have either.
	PolicyConds string
}

// Out is everything Render writes: the Graph lowered to Go names, types,
// handle kinds, enums, join entities and the code of defaults and checks.
type Out struct {
	Package    string
	Entities   []*OutEntity // the graph's, then the join entities, each sorted by Name
	Enums      []*OutEnum   // sorted by type name
	NeedsSch   bool         // some entity reads the schema package
	SchemaPath string       // the schema package's import path
	UserDoc    bool         // Config.UserDoc
}

// OutEntity is one generated entity: a schema's, or a many-to-many join entity.
type OutEntity struct {
	Name, Label, Handles, Table, TableSchema string // Handles = GoPlural(Name), e.g. "Users", "StatusSlice"; a join entity's is GoName(Table)
	Fields                                   []*OutField
	Rels                                     []*OutRel
	PrimaryKey                               []int
	Tenant                                   int // -1 none
	Indexes                                  []OutIndex
	TenantRefs                               []OutTenantRef // the foreign keys a write under a tenant checks, by column
	Join                                     bool           // a many-to-many join entity
	Bidi                                     bool           // the join entity of a bidirectional self edge: its Desc says Bidi
	Comment                                  string         // the Comment annotation's text, "" when none
	Bound                                    Bound

	// Imports are the import paths this entity's file needs besides velox and
	// the schema package: those of its fields' Go types, and the standard
	// packages CheckCode and CheckSetup use. Sorted.
	Imports []string

	// NeedsSch reports that this entity's code reads the schema package, which
	// the file imports under the alias sch.
	NeedsSch bool

	CheckCode  string // the cases of Check's switch on the column, "" when none
	CheckSetup string // statements run once inside Desc before the literal (regexps, validator funcs, descriptor reads), "" when none
}

// OutTenantRef is velox.TenantRef: a foreign key to a tenant-scoped entity.
type OutTenantRef struct {
	Column                     int
	Schema, Table, Key, Tenant string
	Kind                       string // the reflect kind of Tenant's Go type: Generate states it (tenantKinds)
}

// OutField is a column of an entity.
type OutField struct {
	GoName, Column, Name string
	GoType               string // as written in the struct, e.g. "*time.Time", "UserRole"
	ElemType             string // GoType without the pointer
	Pointer              bool
	Unset                string // an Optional value field: the condition on x, its value, that means unset, which Check lets pass; "" when none
	Handle               string // e.g. "velox.NullOrderedCol[User, time.Time]"
	Tag                  string // the struct tag, including backquotes
	Comment              string
	Sensitive, Immutable bool
	JSON                 bool   // a json field: its ColumnDesc's codec is encoding/json's, which the file imports
	RequiredEdge         string // the Required edge whose NULL-able key the field holds, an edge to its own schema: its ColumnDesc says RequiredEdge; "" when none
	NilIsNull            bool   // a slice or map field whose column holds NULL (an Optional or Nillable json field, a Nillable bytes field), which nil binds: its ColumnDesc says Nullable
	DefaultCode          string // "" or a Go expression for ColumnDesc.Default
	DefaultFuncCode      string // "" or "func() any { … }"
	UpdateDefaultCode    string // "" or "func() any { … }"
}

// OutRel is a relation of an entity.
type OutRel struct {
	GoName, Name, Target string
	Many                 bool   // Many[T] (O2M, M2M) or One[T] (O2O, M2O)
	Desc                 string // the velox.EdgeTo or velox.EdgeThrough expression
}

// OutIndex is an index, by the indexes of its columns in the entity's fields.
type OutIndex struct {
	Columns []int
	Unique  bool
}

// OutEnum is an enum type velox-gen declares.
type OutEnum struct {
	Type   string   // e.g. "UserRole"
	Consts []string // e.g. "UserRoleViewer"
	Values []string // stored values, in declaration order
}

// GeneratedCodeVersion is the version of the code velox-gen writes: the N of
// the velox.GeneratedCodeIsVersionN guard in every generated file.
const GeneratedCodeVersion = 7
