// Package sqlschema holds the annotations of a schema that only a SQL database
// reads: a table's name, an index's access method, its order and operator
// classes, and a column's DEFAULT as SQL text. A schema also feeds GraphQL and other targets,
// and the portable builders of packages field, edge and index say nothing of
// one backend, so an option of one is an annotation named for it, as GraphQL's
// are in package graphql. The names and fields are the old velox's, so
// `index.Fields("tags").Annotations(sqlschema.IndexType("GIN"))` is written
// the same.
//
// velox-gen refuses an annotation on the wrong kind of element (the old velox
// ignored one without a word): Table on a field, Default on a schema, an
// IndexAnnotation anywhere but an index. The old velox's options that velox
// does not hold, such as Size, ColumnType, Skip or Prefix, are not here: a
// port that names one does not compile, and docs/coming-from-veloxe.md names
// velox's spelling.
//
// # Schemas
//
// Table, SchemaTable, Schema, Collation and Checks (or the same fields of an
// Annotation literal, `sqlschema.Annotation{Table: "users"}`) are a
// schema's: its table, the database schema that holds it, its collation on
// MySQL and its named CHECK constraints.
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
// dialect, and DefaultExprs one for each, written as the database prints it.
// An insert leaves the column out when every row holds the field's zero
// value, so the DEFAULT applies, and reads the stored value back into the
// entity (RETURNING, or a SELECT by key on MySQL; ADR 0093). A non-zero
// value, or a non-nil pointer to a zero, is written. A zero left to the
// database skips the field's validators, so an enum inserts with its zero;
// velox-gen warns of a non-zero literal default on a field that is not
// Nillable, whose zero no Insert can store. A DEFAULT does not count as the
// value of an Optional field. A MySQL text, blob or JSON column takes only
// an expression, in parentheses. A time's default on SQLite must write
// velox's form, not CURRENT_TIMESTAMP's: see the velox package's SQLite
// section.
package sqlschema

import (
	"maps"

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
	// Desc sorts every key of the index descending.
	Desc bool
	// DescColumns sorts the named keys descending, by field or edge name.
	DescColumns map[string]bool
	// OpClass is the operator class of every key of the index.
	OpClass string
	// OpClassColumns is the operator class of each named key, by field or edge
	// name.
	OpClassColumns map[string]string
}

// Name returns IndexAnnotationName.
func (IndexAnnotation) Name() string { return IndexAnnotationName }

// Merge returns a with the non-zero fields of other, an IndexAnnotation or a
// pointer to one, over it. Maps are combined. It leaves a and other as they
// are.
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
	if o.Type != "" {
		out.Type = o.Type
	}
	if o.Where != "" {
		out.Where = o.Where
	}
	out.Desc = a.Desc || o.Desc
	if o.OpClass != "" {
		out.OpClass = o.OpClass
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

// Annotation holds the SQL options of a schema or a field. Give it to either
// with Annotations; several on one element combine, a later non-zero field
// winning, and a struct literal and the constructors are the same thing:
// `sqlschema.Annotation{Table: "users"}` is `sqlschema.Table("users")`.
//
// A schema's options are Table, Schema, Collation and Checks. A field's are
// Default, DefaultExpr and DefaultExprs, and an edge's NonUniqueFK. velox-gen
// refuses an option on the wrong kind of element.
type Annotation struct {
	// Table is the table a schema is stored in.
	Table string
	// Schema is the database schema that holds the table.
	Schema string
	// Collation is a schema's table collation, on MySQL. Without it a new
	// table gets utf8mb4_bin and one that exists keeps what it has.
	Collation string
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

	// NonUniqueFK is an edge's: its key is not unique (see NonUniqueFK).
	NonUniqueFK bool
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
		{&out.Default, o.Default}, {&out.DefaultExpr, o.DefaultExpr},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	out.NonUniqueFK = a.NonUniqueFK || o.NonUniqueFK
	return out
}

// Table names the table of a schema. Without it the table is ent's: the
// snake_case of the plural of the schema's Go name, which is case-sensitive,
// so User gives users, Category categories, Person persons and Status status.
func Table(name string) Annotation { return Annotation{Table: name} }

// SchemaTable names the table of a schema and the database schema that holds
// it.
func SchemaTable(schema, table string) Annotation {
	return Annotation{Schema: schema, Table: table}
}

// Schema names the database schema that holds a schema's table.
func Schema(name string) Annotation { return Annotation{Schema: name} }

// Collation states the collation of a schema's table on MySQL, and says
// nothing on the other dialects. Without it a new table gets utf8mb4_bin and
// one that exists keeps what it has: a plan changes a table's collation only
// when one is stated.
func Collation(c string) Annotation { return Annotation{Collation: c} }

// Checks adds named CHECK constraints to a schema's table, name to
// expression. An expression is SQL of the schema's dialect, and velox writes
// it as it is, so its identifiers must be quoted the dialect's way if at
// all. A violation fails the statement with a *velox.ConstraintError of kind
// ConstraintCheck. velox-gen refuses one name given two expressions, by a
// mixin and its schema.
//
//	func (Invoice) Annotations() []schema.Annotation {
//		return []schema.Annotation{sqlschema.Checks(map[string]string{
//			"invoices_total_positive": "total > 0",
//			"invoices_dates":          "due_at >= issued_at",
//		})}
//	}
func Checks(c map[string]string) Annotation { return Annotation{Checks: maps.Clone(c)} }

// NonUniqueFK declares, on an edge, that a to-one edge is many-to-one in
// intent, as in veloxe: its foreign key is not unique. A unique edge to its
// own schema with no inverse is a bidirectional one-to-one edge, whose key is
// unique and whose partner is re-pointed with it; with NonUniqueFK it is a
// plain many-to-one edge whose key many rows may hold. On a one-to-one pair
// it only drops the key's uniqueness.
//
//	edge.To("merged_into", Customer.Type).
//	    Field("merged_into_id").
//	    Unique().
//	    Annotations(sqlschema.NonUniqueFK())
func NonUniqueFK() Annotation { return Annotation{NonUniqueFK: true} }

// Default sets a column's DEFAULT to the SQL text value on every dialect:
// Default("100"), Default("'new'"). An insert of the field's zero value
// leaves the column to it and reads the stored value back (ADR 0093); a
// Go-side default is the field's Default.
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
