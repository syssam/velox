package codegen

// Config says where generated code goes and what the user's files declare.
type Config struct {
	Package    string           // the generated package's name; "veloxgen" when empty
	SchemaPath string           // the schema package's import path; Graph.Package when empty
	SchemaName string           // the schema package's name; the last element of SchemaPath when empty
	Bound      map[string]Bound // per entity name: which methods the schema package declares (see SchemaMethods)
	// Mixins are, per entity name, its mixins that declare behaviour, in
	// Mixin order (see MixinBehaviour).
	Mixins  map[string][]MixinBound
	UserDoc bool // a user's file has the package comment; velox.go then has none
	// Funcs are the packages of the functions the schemas name as defaults
	// and validators, by import path (see FuncPackages). A function of a
	// package that is not here, or that the entities cannot import, is read
	// from the schema.
	Funcs map[string]FuncPkg

	// GenPath is the generated package's import path.
	GenPath string
}

// Bound says which of an entity's methods the schema package declares, so
// that its Descriptor binds them.
type Bound struct {
	Policy, Hooks, Interceptors bool
	// Privacy says the file of the Policy method imports velox's privacy
	// package: the schema is veloxe's, whose applications imported the
	// generated package's privacy, so velox-gen writes it (Out.Privacy).
	Privacy bool
	// PolicyConds is "path:line" of the first Read, Write, Query or
	// Mutation key of a velox.Policy (or privacy.Policy) literal in the
	// Policy method's body, "" when it has none. The join entity of a
	// bidirectional edge cannot have any of them.
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
	// SchemaName is the name the generated code gives the schema package:
	// its own, unless the generated code uses that name for another.
	SchemaName string
	UserDoc    bool   // Config.UserDoc
	GenPath    string // Config.GenPath
	// EntityTable says some entity has behaviour: velox.go then declares the
	// package's velox.EntityTable, and every Descriptor names it, so that a
	// condition by name finds its target when its statement runs.
	EntityTable bool
	// Privacy says some schema's Policy is written with velox's privacy
	// package (Bound.Privacy): Render then writes privacy/privacy.go, the
	// package veloxe generated beside the entities, which gives veloxe's
	// names of velox's privacy package to code that imports it.
	Privacy bool
}

// OutEntity is one generated entity: a schema's, or a many-to-many join entity.
type OutEntity struct {
	Name, Label, Handles, Table, TableSchema string // Handles = GoPlural(Name), e.g. "Users", "StatusSlice"; a join entity's is GoName(Table)
	Fields                                   []*OutField
	Rels                                     []*OutRel
	PrimaryKey                               []int
	Indexes                                  []OutIndex
	Join                                     bool   // a many-to-many join entity
	Bidi                                     bool   // the join entity of a bidirectional self edge: its Descriptor says Bidi
	Comment                                  string // the Comment annotation's text, "" when none
	Bound                                    Bound

	// Imports are the import paths this entity's file needs besides velox and
	// the schema package: those of its fields' Go types, and the standard
	// packages CheckCode and CheckSetup use. Sorted.
	Imports []string

	// NeedsSch reports that this entity's code reads the schema package, which
	// the file imports as Out.SchemaName.
	NeedsSch bool

	// ImportAs names the imports, by path, that the entity's file imports
	// under a name other than the package's own: velox's schema package,
	// when the schema package is named schema too.
	ImportAs map[string]string

	// Schema is the value the Descriptor names for velox to read the
	// entity's behaviour from, such as "schema.Issue{}": "" when neither
	// its schema nor a mixin it lists declares any.
	Schema string

	CheckCode  string // the cases of Check's switch on the column, "" when none
	CheckSetup string // statements run once inside Descriptor before the literal (regexps, validator funcs, descriptor reads), "" when none
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
	Version              bool   // the optimistic-locking version: its ColumnDescriptor says Version
	Implicit             bool   // the foreign key velox added for an edge, which ent keeps unexported: not in JSON, not in String
	JSON                 bool   // a json field: its ColumnDescriptor's codec is encoding/json's, which the file imports
	RequiredEdge         string // the Required edge whose NULL-able key the field holds, an edge to its own schema: its ColumnDescriptor says RequiredEdge; "" when none
	NilIsNull            bool   // a slice or map field whose column holds NULL (an Optional or Nillable json field, a Nillable bytes field), which nil binds: its ColumnDescriptor says Nullable
	DefaultCode          string // "" or a Go expression for ColumnDescriptor.Default
	DefaultFuncCode      string // "" or "func() any { … }"
	ZeroFillCode         string // "" or "func() any { … }": the value an unset Optional JSON slice, map or pointer is written as, ColumnDescriptor.DefaultFunc when DefaultFuncCode is ""
	UpdateDefaultCode    string // "" or "func() any { … }"
}

// OutRel is a relation of an entity.
type OutRel struct {
	GoName, Name, Target string
	Many                 bool   // Many[T] (O2M, M2M) or One[T] (O2O, M2O)
	Desc                 string // the velox.EdgeTo or velox.EdgeThrough expression
	Comment              string // the edge's documentation, from the schema
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
	Of     string   // the field that declares it, e.g. "User's role field"
}

// veloxPkg is velox's import path.
const veloxPkg = "github.com/syssam/velox"

// privacyPkg is velox's privacy package, whose Policy is velox.Policy.
const privacyPkg = "github.com/syssam/velox/privacy"

// GeneratedCodeVersion is the version of the code velox-gen writes: the N of
// the velox.GeneratedCodeIsVersionN guard in every generated file.
const GeneratedCodeVersion = 23
