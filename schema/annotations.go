package schema

// TableAnnotation names the table a schema is stored in, and optionally the
// database schema that holds it.
type TableAnnotation struct{ Schema, Table string }

// Name returns "Table".
func (*TableAnnotation) Name() string { return "Table" }

// Table names the table of a schema.
func Table(name string) *TableAnnotation { return &TableAnnotation{Table: name} }

// TableIn names the table of a schema and the database schema that holds it.
func TableIn(schema, table string) *TableAnnotation {
	return &TableAnnotation{Schema: schema, Table: table}
}

// CommentAnnotation is the comment of a schema, written to the table where the
// dialect has table comments and to the generated entity's doc comment.
type CommentAnnotation struct{ Text string }

// Name returns "Comment".
func (*CommentAnnotation) Name() string { return "Comment" }

// Comment sets the comment of a schema.
func Comment(text string) *CommentAnnotation { return &CommentAnnotation{Text: text} }
