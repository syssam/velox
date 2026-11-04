package codegen

// Config says where generated code goes and what the user's files declare.
type Config struct {
	Package    string           // the generated package's name; "gen" when empty
	SchemaPath string           // the schema package's import path; Graph.Package when empty
	Bound      map[string]Bound // per entity name: which methods the schema package declares (see SchemaMethods)
	UserDoc    bool             // a user's file has the package comment; velox.go then has none
	// Funcs are the packages of the functions the schemas name as defaults
	// and validators, by import path (see FuncPackages). A function of a
	// package that is not here, or that the entities cannot import, is read
	// from the schema.
	Funcs map[string]FuncPkg

	// SchemaImportsGen says the schema package imports the generated
	// package, as it does when it declares Hooks or Policy; a non-empty
	// Bound implies it. The generated package then does not import the
	// schema package: what its entities need from it, the schema's Hooks
	// and Policy methods and the defaults and validators they cannot call
	// by name, package runtime (RuntimeDir below the output directory)
	// gives them when the program starts. It imports the generated package
	// and the schema package, and a program imports it for its effect.
	// GenPath is the generated package's import path.
	SchemaImportsGen bool
	GenPath          string
}

// RuntimeDir is the directory, below the output directory, of package
// runtime, which binds the schema when it imports the generated package, and
// the package's name.
const RuntimeDir = "runtime"

// Bound says which of an entity's methods the schema package declares, so
// that its Descriptor binds them.
type Bound struct {
	Policy, Hooks bool
	// PolicyPtr and HooksPtr say the method has a pointer receiver.
	PolicyPtr, HooksPtr bool
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
	// SchemaImportsGen is Config.SchemaImportsGen, or a non-empty
	// Config.Bound: the generated package does not import the schema.
	SchemaImportsGen bool
	// Runtime says some entity reads a value of VeloxRuntime, which package
	// runtime sets (OutEntity.Binds); only when SchemaImportsGen.
	Runtime bool
	GenPath string // Config.GenPath

	// ScopeKeys are the keys the schema's Scoped fields name, sorted by Var.
	ScopeKeys []*OutScopeKey
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
	Scope                                    *OutScope // the entity's Scoped field, nil when it has none

	// Imports are the import paths this entity's file needs besides velox and
	// the schema package: those of its fields' Go types, and the standard
	// packages CheckCode and CheckSetup use. Sorted.
	Imports []string

	// NeedsSch reports that this entity's code reads the schema package, which
	// the file imports under the alias sch.
	NeedsSch bool

	// Binds are what package runtime gives this entity when the schema
	// imports the generated package, as fields of VeloxRuntime: its Policy
	// and Hooks methods, and the defaults and validators the entity cannot
	// call by name.
	Binds []*OutBind

	CheckCode  string // the cases of Check's switch on the column, "" when none
	CheckSetup string // statements run once inside Descriptor before the literal (regexps, validator funcs, descriptor reads), "" when none
}

// OutScope is the scope of an entity: its Scoped field.
type OutScope struct {
	Key    string // the annotation's key, e.g. "org"
	KeyVar string // the package variable that is the key, e.g. "OrgKey"
	Var    string // the entity's scope variable, e.g. "noteScope"
	Field  string // the Go name of the scoped column, e.g. "OrgID"
	Handle string // the equality function of the column's handle, e.g. "Notes.OrgID.EQ"
}

// OutScopeKey is a scope key: one variable shared by every entity that names it.
type OutScopeKey struct {
	Name    string   // the annotation's key, e.g. "org"
	Var     string   // e.g. "OrgKey"
	GoType  string   // the type of the value, as the generated code writes it, e.g. "int64"
	Imports []string // the import paths GoType needs, sorted
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
	Implicit             bool   // the foreign key velox added for an edge, which ent keeps unexported: not in JSON, not in String
	JSON                 bool   // a json field: its ColumnDescriptor's codec is encoding/json's, which the file imports
	RequiredEdge         string // the Required edge whose NULL-able key the field holds, an edge to its own schema: its ColumnDescriptor says RequiredEdge; "" when none
	NilIsNull            bool   // a slice or map field whose column holds NULL (an Optional or Nillable json field, a Nillable bytes field), which nil binds: its ColumnDescriptor says Nullable
	DefaultCode          string // "" or a Go expression for ColumnDescriptor.Default
	DefaultFuncCode      string // "" or "func() any { … }"
	UpdateDefaultCode    string // "" or "func() any { … }"
}

// OutBind is a field of the generated package's VeloxRuntime, which package
// runtime sets.
type OutBind struct {
	Field string // e.g. "CommentPolicy", "GroupTokenDefault"
	Type  string // its type, as the generated package writes it, e.g. "func() velox.Policy[Comment]"
	// Value is the expression package runtime sets it to, e.g. "sch.Comment{}.Policy",
	// or, when Desc is set, a read of the variable DescVar, e.g.
	// "groupToken.DefaultFunc.(func() string)".
	Value string
	// Desc is the name of the schema's field whose descriptor Value reads,
	// DescVar the variable package runtime holds it in; "" when Value reads none.
	Desc, DescVar string
	// Imports are the packages Value names, but sch and the schema DSL
	// package: import name → path.
	Imports map[string]string
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
const GeneratedCodeVersion = 11
