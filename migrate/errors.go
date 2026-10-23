package migrate

import "strings"

// An Error is a refusal of the migration planner.
type Error struct {
	Pos    string // the schema's file:line, when known
	Object string // the table, column, index or constraint it is about
	Code   string // names the failure for programs: "primary-key", "rename"
	Reason string
}

// Error is "migrate: " and the non-empty of Pos, Object and Reason, joined
// by ": ". Code names the failure for programs ("primary-key", "rename").
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("migrate: ")
	sep := ""
	for _, s := range [...]string{e.Pos, e.Object, e.Reason} {
		if s != "" {
			b.WriteString(sep)
			b.WriteString(s)
			sep = ": "
		}
	}
	return b.String()
}
