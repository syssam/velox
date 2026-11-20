package veloxtype

// FieldChange is one column that differs between two rows: in a recorded
// update's Change.Diff, or in velox.Diff. Name is the column's name. Old
// and New are plain values: nil for NULL, and a nullable column's value, not
// a pointer to it.
type FieldChange struct {
	Name     string
	Old, New any
}
