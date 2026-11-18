// Package sqlschema holds the annotations of a schema that only a SQL database
// reads: a table's name, an index's access method, its order and operator
// classes, and a column's DEFAULT as SQL text. A schema also feeds GraphQL and other targets,
// and the portable builders of packages field, edge and index say nothing of
// one backend, so an option of one is an annotation named for it, as GraphQL's
// are in package graphql. The names and fields are the old velox's, so
// `index.Fields("tags").Annotations(sqlschema.IndexType("GIN"))` is written
// the same.
//
// velox-gen reads these annotations and refuses what it cannot hold with a
// message that names where velox spells it: StorageParams, Prefix, PrefixColumns,
// IncludeColumns, Size, ColumnType, OnDelete and the like compile, for a port,
// and are refused when velox-gen runs. So is an annotation on the wrong kind of
// element (the old velox ignored one without a word): Table on a field, Default
// on a schema, an IndexAnnotation anywhere but an index.
//
// # Schemas
//
// Table, SchemaTable, Collation and Checks (or the same fields of an Annotation
// literal, `sqlschema.Annotation{Table: "users"}`) are a schema's, and are
// lowered to schema.TableIn, schema.Collation and schema.Check.
//
// # Indexes
//
// IndexType gives an index an access method, on PostgreSQL (btree, hash, gin,
// gist, spgist, brin); a GIN index may cover a JSON field. Desc and
// DescColumns sort keys descending, OpClass and OpClassColumn give a key an
// operator class, and IndexWhere makes the index partial. The columns are named
// as the index names them, by field or edge. They are PostgreSQL's: velox-gen
// refuses them on MySQL and SQLite. A Method is a property of an index's
// identity, so changing one is a drop and a create built beside the old index.
//
// # Defaults
//
// Default and DefaultExpr give a column a DEFAULT of one SQL text on every
// dialect, and DefaultExprs one for each. The column's default is all they set:
// velox's inserts write every column, so it serves rows written outside velox.
// A MySQL text, blob or JSON column takes only an expression, in parentheses.
package sqlschema

import (
	"maps"
	"slices"

	"github.com/syssam/velox/schema"
)

// AnnotationName is the name of Annotation.
const AnnotationName = "sql"

// IndexAnnotationName is the name of IndexAnnotation.
const IndexAnnotationName = "sqlindex"

// IndexAnnotation holds the SQL options of one index. Give it to an index with
// Annotations; several on one index combine, a later non-zero field winning.
type IndexAnnotation struct {
	// Type is the index's access method: BTREE, HASH, GIN, GIST, SPGIST or
	// BRIN, in any case. PostgreSQL only; BTREE is the default and is no method.
	Type string
	// Types is Type for each dialect, by name (postgres, mysql, sqlite). Only
	// postgres is held: another is refused.
	Types map[string]string
	// Where is the predicate of a partial index.
	Where string
	// StorageParams is not held; velox-gen refuses it.
	StorageParams string
	// Desc sorts every key of the index descending.
	Desc bool
	// DescColumns sorts the named keys descending, by field or edge name.
	DescColumns map[string]bool
	// OpClass is the operator class of every key of the index.
	OpClass string
	// OpClassColumns is the operator class of each named key, by field or edge
	// name.
	OpClassColumns map[string]string
	// Prefix is not held; velox-gen refuses it.
	Prefix uint
	// PrefixColumns is not held; velox-gen refuses it.
	PrefixColumns map[string]uint
	// IncludeColumns is not held; velox-gen refuses it.
	IncludeColumns []string
}

// Name returns IndexAnnotationName.
func (IndexAnnotation) Name() string { return IndexAnnotationName }

// Merge returns a with the non-zero fields of other, an IndexAnnotation or a
// pointer to one, over it. Maps are combined, and IncludeColumns appended. It
// leaves a and other as they are.
func (a IndexAnnotation) Merge(other schema.Annotation) schema.Annotation {
	var o IndexAnnotation
	switch v := other.(type) {
	case IndexAnnotation:
		o = v
	case *IndexAnnotation:
		if v == nil {
			return a
		}
		o = *v
	default:
		return a
	}
	out := a
	out.Types = mergeMaps(a.Types, o.Types)
	out.DescColumns = mergeMaps(a.DescColumns, o.DescColumns)
	out.OpClassColumns = mergeMaps(a.OpClassColumns, o.OpClassColumns)
	out.PrefixColumns = mergeMaps(a.PrefixColumns, o.PrefixColumns)
	out.IncludeColumns = slices.Concat(a.IncludeColumns, o.IncludeColumns)
	if o.Type != "" {
		out.Type = o.Type
	}
	if o.Where != "" {
		out.Where = o.Where
	}
	if o.StorageParams != "" {
		out.StorageParams = o.StorageParams
	}
	out.Desc = a.Desc || o.Desc
	if o.OpClass != "" {
		out.OpClass = o.OpClass
	}
	if o.Prefix != 0 {
		out.Prefix = o.Prefix
	}
	return out
}

// mergeMaps returns a copy of a with b's entries over it, nil when both are
// empty.
func mergeMaps[V any](a, b map[string]V) map[string]V {
	if len(a)+len(b) == 0 {
		return nil
	}
	out := maps.Clone(a)
	if out == nil {
		out = map[string]V{}
	}
	maps.Copy(out, b)
	return out
}

// IndexType sets an index's access method, on PostgreSQL: IndexType("GIN").
func IndexType(typ string) *IndexAnnotation { return &IndexAnnotation{Type: typ} }

// IndexTypes sets an index's access method for each dialect, by name. Only
// "postgres" is held.
func IndexTypes(types map[string]string) *IndexAnnotation {
	return &IndexAnnotation{Types: maps.Clone(types)}
}

// IndexWhere makes an index partial: it holds the rows where pred is true.
func IndexWhere(pred string) *IndexAnnotation { return &IndexAnnotation{Where: pred} }

// Desc sorts every key of an index descending, on PostgreSQL.
func Desc() *IndexAnnotation { return &IndexAnnotation{Desc: true} }

// DescColumns sorts the named keys of an index descending, on PostgreSQL. A
// name is a field's or an edge's, as the index names it.
func DescColumns(names ...string) *IndexAnnotation {
	a := &IndexAnnotation{DescColumns: make(map[string]bool, len(names))}
	for _, n := range names {
		a.DescColumns[n] = true
	}
	return a
}

// OpClass sets the operator class of every key of an index, on PostgreSQL:
// OpClass("gin_trgm_ops"). Name only a class that is not its type's default.
// The class's extension, pg_trgm or btree_gin, must be in the baseline: velox
// does not create extensions.
func OpClass(op string) *IndexAnnotation { return &IndexAnnotation{OpClass: op} }

// OpClassColumn sets the operator class of one key of an index, by field or
// edge name, on PostgreSQL.
func OpClassColumn(name, op string) *IndexAnnotation {
	return &IndexAnnotation{OpClassColumns: map[string]string{name: op}}
}

// StorageParams is the old velox's index storage parameters. velox-gen refuses
// it: velox does not hold them.
func StorageParams(params string) *IndexAnnotation { return &IndexAnnotation{StorageParams: params} }

// Prefix is the old velox's MySQL index prefix length. velox-gen refuses it:
// velox does not hold one.
func Prefix(n uint) *IndexAnnotation { return &IndexAnnotation{Prefix: n} }

// PrefixColumn is the old velox's MySQL prefix length of one key. velox-gen
// refuses it: velox does not hold one.
func PrefixColumn(name string, n uint) *IndexAnnotation {
	return &IndexAnnotation{PrefixColumns: map[string]uint{name: n}}
}

// IncludeColumns is the old velox's covering-index columns. velox-gen refuses
// it: velox does not hold them.
func IncludeColumns(names ...string) *IndexAnnotation {
	return &IndexAnnotation{IncludeColumns: slices.Clone(names)}
}

// CascadeAction is a foreign key's action on delete or update, as the old velox
// has it. velox holds an edge's as edge.OnDelete, so an Annotation that sets one
// is refused by velox-gen.
type CascadeAction string

// The cascade actions.
const (
	Cascade    CascadeAction = "CASCADE"
	SetNull    CascadeAction = "SET NULL"
	Restrict   CascadeAction = "RESTRICT"
	SetDefault CascadeAction = "SET DEFAULT"
	NoAction   CascadeAction = "NO ACTION"
)

// Annotation holds the SQL options of a schema or a field. Give it to either
// with Annotations; several on one element combine, a later non-zero field
// winning, and a struct literal and the constructors are the same thing:
// `sqlschema.Annotation{Table: "users"}` is `sqlschema.Table("users")`.
//
// A schema's options are Table, Schema, Collation, Check and Checks, which
// velox-gen lowers to schema.TableIn, schema.Collation and schema.Check. A
// field's are Default, DefaultExpr and DefaultExprs. The other fields are the
// old velox's and compile, for a port, and velox-gen refuses them naming
// velox's own spelling; velox-gen also refuses an option on the wrong kind of
// element.
type Annotation struct {
	// Table is the table a schema is stored in.
	Table string
	// Schema is the database schema that holds the table.
	Schema string
	// Collation is a schema's table collation, on MySQL.
	Collation string
	// Check is one unnamed CHECK constraint. velox-gen refuses it: velox names
	// every constraint, so write Checks or schema.Check.
	Check string
	// Checks are named CHECK constraints of a schema, name to expression.
	Checks map[string]string

	// Default is a field's column DEFAULT as SQL text, written as it is, on every
	// dialect: "100" for a number, "'new'" for a string.
	Default string
	// DefaultExpr is a field's column DEFAULT as a SQL expression, on every
	// dialect.
	DefaultExpr string
	// DefaultExprs is a field's column DEFAULT for each dialect, by name
	// (postgres, mysql, sqlite; sqlite3 is read as sqlite).
	DefaultExprs map[string]string

	// Held by the old velox and refused by velox-gen, each naming its spelling in
	// velox: Skip, Size (field.MaxLen), ColumnType (field.SchemaType), OnDelete and
	// OnUpdate (edge.OnDelete), NonUniqueFK, Charset, IncrementStart and
	// WithComments.
	Skip           bool
	Size           int64
	ColumnType     string
	OnDelete       CascadeAction
	OnUpdate       CascadeAction
	NonUniqueFK    bool
	Charset        string
	IncrementStart *int
	WithComments   *bool
}

// Name returns AnnotationName.
func (Annotation) Name() string { return AnnotationName }

// Merge returns a with the non-zero fields of other, an Annotation or a pointer
// to one, over it. Maps are combined. It leaves a and other as they are.
func (a Annotation) Merge(other schema.Annotation) schema.Annotation {
	var o Annotation
	switch v := other.(type) {
	case Annotation:
		o = v
	case *Annotation:
		if v == nil {
			return a
		}
		o = *v
	default:
		return a
	}
	out := a
	out.Checks = mergeMaps(a.Checks, o.Checks)
	out.DefaultExprs = mergeMaps(a.DefaultExprs, o.DefaultExprs)
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&out.Table, o.Table}, {&out.Schema, o.Schema}, {&out.Collation, o.Collation},
		{&out.Check, o.Check}, {&out.Default, o.Default}, {&out.DefaultExpr, o.DefaultExpr},
		{&out.ColumnType, o.ColumnType}, {&out.Charset, o.Charset},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	out.Skip = a.Skip || o.Skip
	out.NonUniqueFK = a.NonUniqueFK || o.NonUniqueFK
	if o.Size != 0 {
		out.Size = o.Size
	}
	if o.OnDelete != "" {
		out.OnDelete = o.OnDelete
	}
	if o.OnUpdate != "" {
		out.OnUpdate = o.OnUpdate
	}
	if o.IncrementStart != nil {
		out.IncrementStart = o.IncrementStart
	}
	if o.WithComments != nil {
		out.WithComments = o.WithComments
	}
	return out
}

// Table names the table of a schema, as schema.Table does.
func Table(name string) Annotation { return Annotation{Table: name} }

// SchemaTable names the table of a schema and the database schema that holds
// it, as schema.TableIn does.
func SchemaTable(schema, table string) Annotation {
	return Annotation{Schema: schema, Table: table}
}

// Schema names the database schema that holds a schema's table.
func Schema(name string) Annotation { return Annotation{Schema: name} }

// Collation states the collation of a schema's table on MySQL, as
// schema.Collation does.
func Collation(c string) Annotation { return Annotation{Collation: c} }

// Checks adds named CHECK constraints to a schema's table, name to expression,
// as schema.Check does one at a time.
func Checks(c map[string]string) Annotation { return Annotation{Checks: maps.Clone(c)} }

// Check is the old velox's one unnamed CHECK constraint. velox-gen refuses it:
// use Checks or schema.Check, which name the constraint.
func Check(expr string) Annotation { return Annotation{Check: expr} }

// Size is the old velox's column size. velox-gen refuses it: use field.MaxLen.
func Size(n int64) Annotation { return Annotation{Size: n} }

// ColumnType is the old velox's column type. velox-gen refuses it: use
// field.SchemaType, which names the type for each dialect.
func ColumnType(typ string) Annotation { return Annotation{ColumnType: typ} }

// OnDelete is the old velox's foreign key action. velox-gen refuses it: use
// edge.OnDelete.
func OnDelete(a CascadeAction) Annotation { return Annotation{OnDelete: a} }

// OnUpdate is the old velox's foreign key action on update. velox-gen refuses
// it: velox does not hold one.
func OnUpdate(a CascadeAction) Annotation { return Annotation{OnUpdate: a} }

// NonUniqueFK is the old velox's switch for a one-to-one edge's key. velox-gen
// refuses it: velox does not hold it.
func NonUniqueFK() Annotation { return Annotation{NonUniqueFK: true} }

// Charset is the old velox's column character set. velox-gen refuses it.
func Charset(charset string) Annotation { return Annotation{Charset: charset} }

// Skip is the old velox's switch that leaves a field or schema out of the SQL.
// velox-gen refuses it.
func Skip() Annotation { return Annotation{Skip: true} }

// IncrementStart is the old velox's auto-increment start. velox-gen refuses it.
func IncrementStart(n int) Annotation { return Annotation{IncrementStart: &n} }

// WithComments is the old velox's switch for column comments. velox-gen refuses
// it: velox writes a comment where the dialect has them.
func WithComments(on bool) Annotation { return Annotation{WithComments: &on} }

// Default sets a column's DEFAULT to the SQL text value on every dialect:
// Default("100"), Default("'new'"). It is the column's default only, for rows
// written outside velox; a Go-side default is the field's Default.
func Default(value string) Annotation { return Annotation{Default: value} }

// DefaultExpr sets a column's DEFAULT to the SQL expression expr on every
// dialect: DefaultExpr("gen_random_uuid()"). A MySQL text, blob or JSON column
// takes only an expression in parentheses.
func DefaultExpr(expr string) Annotation { return Annotation{DefaultExpr: expr} }

// DefaultExprs sets a column's DEFAULT for each dialect, by name: postgres,
// mysql and sqlite. A dialect it leaves out has none.
func DefaultExprs(exprs map[string]string) Annotation {
	return Annotation{DefaultExprs: maps.Clone(exprs)}
}
