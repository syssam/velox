package veloxtype

// Change is one row a write changed, as Mutation.Changes yields it. Key is
// the row's primary key, a value per key column in the key's order: before
// the write for an update or a delete, after it for an insert. Fields holds
// one FieldChange per column the hook tracked, in the entity's column order.
type Change struct {
	Key    []any
	Fields []FieldChange
}

// Changed returns the fields whose old and new values differ, in order: what
// an audit log records. It is nil when none differ.
func (c Change) Changed() []FieldChange {
	var out []FieldChange
	for _, f := range c.Fields {
		if f.Changed {
			out = append(out, f)
		}
	}
	return out
}

// FieldChange is one column of a Change, or of velox.Diff. Name is the
// column's name. Old and New are plain values: nil for NULL, and a nullable
// column's value, not a pointer to it. An insert's Old is nil, and so is a
// delete's New. Changed reports that Old and New differ by value.
type FieldChange struct {
	Name     string
	Old, New any
	Changed  bool
}
