package schema

import "maps"

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

// CheckAnnotation holds a schema's CHECK constraints: their expressions by name.
type CheckAnnotation struct {
	Checks map[string]string `json:"checks"`
}

// Name returns "Check".
func (*CheckAnnotation) Name() string { return "Check" }

// Merge returns the constraints of a and of other. Of one name given twice, other's
// expression wins.
func (a *CheckAnnotation) Merge(other Annotation) Annotation {
	o, ok := other.(*CheckAnnotation)
	if !ok {
		return a
	}
	m := make(map[string]string, len(a.Checks)+len(o.Checks))
	maps.Copy(m, a.Checks)
	maps.Copy(m, o.Checks)
	return &CheckAnnotation{Checks: m}
}

// Check adds the CHECK constraint name to a schema's table. expr is SQL of the
// schema's dialect, and velox2 writes it as it is, so its identifiers must be quoted
// the dialect's way if at all. Give it once per constraint. A violation fails the
// statement with a *velox.ConstraintError of kind ConstraintCheck.
//
//	func (Invoice) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			schema.Check("invoices_total_positive", "total > 0"),
//			schema.Check("invoices_dates", "due_at >= issued_at"),
//		}
//	}
func Check(name, expr string) *CheckAnnotation {
	return &CheckAnnotation{Checks: map[string]string{name: expr}}
}
