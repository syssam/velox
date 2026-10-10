package notes

// slices is a name of the application's own at package level, in another
// file than the one that comes to need the package slices.
func slices() []string { return nil }

var _ = slices
