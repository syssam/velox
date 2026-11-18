// Package sqlschema holds the annotations of a schema that only a SQL database
// reads: an index's access method, its order and operator classes, and a
// column's DEFAULT as SQL text. A schema also feeds GraphQL and other targets,
// and the portable builders of packages field, edge and index say nothing of
// one backend, so an option of one is an annotation named for it, as GraphQL's
// are in package graphql. The names and fields are the old velox's, so
// `index.Fields("tags").Annotations(sqlschema.IndexType("GIN"))` is written
// the same.
//
// velox-gen reads these annotations and refuses what it cannot hold with a
// message that names the field: StorageParams, Prefix, PrefixColumns and
// IncludeColumns compile, for a port, and are refused when velox-gen runs.
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

// Annotation holds the SQL options of one field. Give it to a field with
// Annotations; several on one field combine, a later non-zero field winning.
type Annotation struct {
	// Default is the column's DEFAULT as SQL text, written as it is, on every
	// dialect: "100" for a number, "'new'" for a string.
	Default string
	// DefaultExpr is the column's DEFAULT as a SQL expression, on every dialect.
	DefaultExpr string
	// DefaultExprs is the column's DEFAULT for each dialect, by name (postgres,
	// mysql, sqlite; sqlite3 is read as sqlite).
	DefaultExprs map[string]string
}

// Name returns AnnotationName.
func (Annotation) Name() string { return AnnotationName }

// Merge returns a with the non-zero fields of other, an Annotation or a pointer
// to one, over it. DefaultExprs are combined. It leaves a and other as they
// are.
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
	out.DefaultExprs = mergeMaps(a.DefaultExprs, o.DefaultExprs)
	if o.Default != "" {
		out.Default = o.Default
	}
	if o.DefaultExpr != "" {
		out.DefaultExpr = o.DefaultExpr
	}
	return out
}

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
