package schema

// CommentAnnotation is the comment of a schema, written to the table where the
// dialect has table comments and to the generated entity's doc comment.
type CommentAnnotation struct {
	Text string `json:"text"`
}

// Name returns "Comment".
func (*CommentAnnotation) Name() string { return "Comment" }

// Comment sets the comment of a schema.
func Comment(text string) *CommentAnnotation { return &CommentAnnotation{Text: text} }
