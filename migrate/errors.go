package migrate

import (
	"strconv"
	"strings"
)

// An Error is a refusal of this package.
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

// A BlockedError is NewPlan's refusal of a plan with destructive changes no
// Allow covers.
type BlockedError struct{ Findings []Finding }

// Error is "migrate: <n> destructive change(s) refused:", then one line per
// finding: "  [<pos>: ]<target>: <code>: <message>; allow with -allow <code>=<target>".
func (e *BlockedError) Error() string {
	var b strings.Builder
	b.WriteString("migrate: ")
	b.WriteString(strconv.Itoa(len(e.Findings)))
	if len(e.Findings) == 1 {
		b.WriteString(" destructive change refused:")
	} else {
		b.WriteString(" destructive changes refused:")
	}
	for _, f := range e.Findings {
		b.WriteString("\n  ")
		if f.Pos != "" {
			b.WriteString(f.Pos + ": ")
		}
		b.WriteString(f.Target + ": " + f.Code + ": " + f.Message + "; allow with -allow " + f.Code + "=" + f.Target)
	}
	return b.String()
}
