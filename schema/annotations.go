package schema

import "slices"

// TableAnnotation names the table a schema is stored in, and optionally the
// database schema that holds it.
type TableAnnotation struct {
	Schema string `json:"schema,omitempty"` // the database schema; "" for the connection's default
	Table  string `json:"table"`
}

// Name returns "Table".
func (*TableAnnotation) Name() string { return "Table" }

// Table names the table of a schema. Without it the table is ent's: the
// snake_case of the plural of the schema's Go name, which is case-sensitive,
// so User gives users, Category categories, Person persons and Status status.
func Table(name string) *TableAnnotation { return &TableAnnotation{Table: name} }

// TableIn names the table of a schema and the database schema that holds it.
func TableIn(schema, table string) *TableAnnotation {
	return &TableAnnotation{Schema: schema, Table: table}
}

// CommentAnnotation is the comment of a schema, written to the table where the
// dialect has table comments and to the generated entity's doc comment.
type CommentAnnotation struct {
	Text string `json:"text"`
}

// Name returns "Comment".
func (*CommentAnnotation) Name() string { return "Comment" }

// Comment sets the comment of a schema.
func Comment(text string) *CommentAnnotation { return &CommentAnnotation{Text: text} }

// CollationAnnotation is the collation of a schema's table on MySQL.
type CollationAnnotation struct {
	Collation string `json:"collation"`
}

// Name returns "Collation".
func (*CollationAnnotation) Name() string { return "Collation" }

// Collation states the collation of a schema's table on MySQL, and says nothing on
// the other dialects. Without it a new table gets utf8mb4_bin and one that exists
// keeps what it has: a plan changes a table's collation only when one is stated.
func Collation(name string) *CollationAnnotation { return &CollationAnnotation{Collation: name} }

// CheckAnnotation holds a schema's CHECK constraints.
type CheckAnnotation struct {
	Checks []CheckConstraint `json:"checks"`
}

// CheckConstraint is one CHECK constraint.
type CheckConstraint struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

// Name returns "Check".
func (*CheckAnnotation) Name() string { return "Check" }

// Merge returns the constraints of a and of other, as schema.Check's repeated
// calls on one schema, or a mixin's and its schema's, combine. velox-gen refuses
// one name given two expressions.
func (a *CheckAnnotation) Merge(other Annotation) Annotation {
	o, ok := other.(*CheckAnnotation)
	if !ok {
		return a
	}
	return &CheckAnnotation{Checks: slices.Concat(o.Checks, a.Checks)}
}

// Check adds the CHECK constraint name to a schema's table. expr is SQL of the
// schema's dialect, and velox writes it as it is, so its identifiers must be
// quoted the dialect's way if at all. A violation fails the statement with a
// *velox.ConstraintError of kind ConstraintCheck.
//
//	func (Invoice) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			schema.Check("invoices_total_positive", "total > 0"),
//			schema.Check("invoices_dates", "due_at >= issued_at"),
//		}
//	}
func Check(name, expr string) *CheckAnnotation {
	return &CheckAnnotation{Checks: []CheckConstraint{{Name: name, Expr: expr}}}
}
