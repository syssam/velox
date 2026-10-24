package migrate

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2"
)

// LintFile checks f as velox-gen migrate lint does. Its error, *Error values
// with Code "file" joined, is for CONCURRENTLY or ALTER TYPE … ADD VALUE in
// a PostgreSQL file run in a transaction, and for BEGIN, START, COMMIT,
// ROLLBACK or END leading a statement of a PostgreSQL or SQLite file run in
// one. Its findings, for a file that is not generated (f.generated), are the
// destructive statements it reads: DROP TABLE t (drop-table t), ALTER TABLE t
// DROP COLUMN c (drop-column t.c), ALTER TABLE t ALTER COLUMN c [SET DATA]
// TYPE and MODIFY COLUMN c (alter-type t.c), ALTER COLUMN c SET NOT NULL
// (set-not-null t.c), TRUNCATE t and DELETE FROM t without WHERE
// (delete-rows t), UPDATE t without WHERE (update-rows t); Pos is
// "<path>:<line>", Change the statement's index.
//
// A target is "<table>" or "<schema>.<table>", and a column's adds
// ".<column>": a quoted name as written, an unquoted one folded to lower
// case on PostgreSQL. A statement LintFile cannot read, such as one in a
// DO block or a function, gives no finding: it cannot see everything a
// hand-written file does.
func LintFile(d velox.Dialect, f File) ([]Finding, error) {
	var errs []error
	var out []Finding
	inTx := d == velox.SQLite || d == velox.Postgres && !f.Directives.NoTransaction
	for i, sql := range f.Statements {
		pos := f.Path
		if i < len(f.lines) {
			pos += ":" + strconv.Itoa(f.lines[i])
		}
		l := &fileLinter{d: d, ws: lex(d, sql), pos: pos, change: i}
		refuse := func(format string, args ...any) {
			errs = append(errs, &Error{Code: "file", Pos: pos, Reason: fmt.Sprintf(format, args...)})
		}
		lead := l.upper(0)
		if inTx && slices.Contains([]string{"BEGIN", "START", "COMMIT", "ROLLBACK", "END"}, lead) {
			refuse("%s in a file run in a transaction; the runner begins and commits the file's transaction itself", lead)
		}
		if d == velox.Postgres && !f.Directives.NoTransaction {
			if l.concurrently() {
				refuse("CONCURRENTLY cannot run in a transaction; put the statement in a file with -- velox:no-transaction")
			}
			if lead == "ALTER" && l.upper(1) == "TYPE" && l.has("ADD", "VALUE") {
				refuse("ALTER TYPE … ADD VALUE in a transaction leaves the value unusable until it commits; put the statement in a file with -- velox:no-transaction")
			}
		}
		if !f.generated {
			l.classify()
			out = append(out, l.out...)
		}
	}
	return out, errors.Join(errs...)
}

// fileLinter reads one statement of a file for LintFile.
type fileLinter struct {
	d      velox.Dialect
	ws     []word
	pos    string
	change int
	out    []Finding
}

// upper is the i-th word upper-cased when it is not quoted, else "".
func (l *fileLinter) upper(i int) string {
	if i < 0 || i >= len(l.ws) || l.ws[i].quoted {
		return ""
	}
	return strings.ToUpper(l.ws[i].text)
}

// has reports whether the keywords kws follow one another somewhere in the
// statement.
func (l *fileLinter) has(kws ...string) bool {
	for i := range l.ws {
		j := 0
		for j < len(kws) && l.upper(i+j) == kws[j] {
			j++
		}
		if j == len(kws) {
			return true
		}
	}
	return false
}

// concurrently reports whether the statement builds, drops or refreshes
// CONCURRENTLY: CREATE [UNIQUE] INDEX CONCURRENTLY, DROP INDEX CONCURRENTLY,
// REFRESH MATERIALIZED VIEW CONCURRENTLY or REINDEX … CONCURRENTLY.
func (l *fileLinter) concurrently() bool {
	lead := l.upper(0)
	for i := range l.ws {
		if l.upper(i) != "CONCURRENTLY" {
			continue
		}
		switch prev := l.upper(i - 1); {
		case (lead == "CREATE" || lead == "DROP") && prev == "INDEX",
			lead == "REFRESH" && prev == "VIEW",
			lead == "REINDEX" && slices.Contains([]string{"INDEX", "TABLE", "SCHEMA", "DATABASE", "SYSTEM"}, prev):
			return true
		}
	}
	return false
}

// name reads a name, "a" or "a.b", at i and returns it and the index after
// it; "" when there is none.
func (l *fileLinter) name(i int) (string, int) {
	var parts []string
	for i < len(l.ws) {
		w := l.ws[i]
		if !w.quoted && (w.text == "" || !wordByte(w.text[0])) {
			break
		}
		part := w.text
		if !w.quoted && l.d == velox.Postgres {
			part = strings.ToLower(part)
		}
		parts = append(parts, part)
		i++
		if i >= len(l.ws) || l.ws[i].quoted || l.ws[i].text != "." {
			break
		}
		i++
	}
	return strings.Join(parts, "."), i
}

// skip is i past the keywords kws when they are there.
func (l *fileLinter) skip(i int, kws ...string) int {
	for j, kw := range kws {
		if l.upper(i+j) != kw {
			return i
		}
	}
	return i + len(kws)
}

// add records a destructive finding.
func (l *fileLinter) add(code, target, format string) {
	l.out = append(l.out, Finding{Code: code, Class: Destructive, Change: l.change, Target: target, Pos: l.pos, Message: fmt.Sprintf(format, target)})
}

// names reads a list of tables from i, each optionally after ONLY, joined by
// commas, and adds a finding of code for each.
func (l *fileLinter) names(i int, code, format string) {
	for {
		i = l.skip(i, "ONLY")
		n, j := l.name(i)
		if n == "" {
			return
		}
		l.add(code, n, format)
		if j < len(l.ws) && !l.ws[j].quoted && l.ws[j].text == "*" {
			j++
		}
		if j >= len(l.ws) || l.ws[j].quoted || l.ws[j].text != "," {
			return
		}
		i = j + 1
	}
}

// classify adds the statement's destructive findings.
func (l *fileLinter) classify() {
	const (
		dropTable  = "drops table %s and its rows"
		deleteRows = "deletes every row of %s"
		updateRows = "updates every row of %s"
	)
	switch l.upper(0) {
	case "DROP":
		if l.upper(1) == "TABLE" {
			l.names(l.skip(2, "IF", "EXISTS"), "drop-table", dropTable)
		}
	case "TRUNCATE":
		l.names(l.skip(1, "TABLE"), "delete-rows", deleteRows)
	case "DELETE":
		if l.upper(1) == "FROM" && !l.has("WHERE") {
			if n, _ := l.name(l.skip(2, "ONLY")); n != "" {
				l.add("delete-rows", n, deleteRows)
			}
		}
	case "UPDATE":
		if !l.has("WHERE") {
			i := l.skip(l.skip(l.skip(1, "LOW_PRIORITY"), "IGNORE"), "ONLY")
			if n, _ := l.name(i); n != "" {
				l.add("update-rows", n, updateRows)
			}
		}
	case "ALTER":
		if l.upper(1) == "TABLE" {
			l.alterTable()
		}
	}
}

// alterTable adds the findings of ALTER TABLE's actions.
func (l *fileLinter) alterTable() {
	table, i := l.name(l.skip(l.skip(2, "IF", "EXISTS"), "ONLY"))
	if table == "" {
		return
	}
	if i < len(l.ws) && !l.ws[i].quoted && l.ws[i].text == "*" {
		i++
	}
	// Each action runs to a comma outside parentheses.
	for i < len(l.ws) {
		end, depth := i, 0
		for ; end < len(l.ws); end++ {
			w := l.ws[end]
			if w.quoted {
				continue
			}
			switch w.text {
			case "(":
				depth++
			case ")":
				depth--
			}
			if depth == 0 && w.text == "," {
				break
			}
		}
		l.action(table, i, end)
		i = end + 1
	}
}

// notColumn are the words after DROP or ALTER in an ALTER TABLE action that
// name something other than a column.
var notColumn = []string{"CONSTRAINT", "INDEX", "KEY", "PRIMARY", "FOREIGN", "CHECK", "PARTITION", "DEFAULT"}

// action adds the findings of the ALTER TABLE action in words [i, end).
func (l *fileLinter) action(table string, i, end int) {
	column := func(j int) (string, int) {
		if slices.Contains(notColumn, l.upper(j)) {
			return "", j
		}
		j = l.skip(j, "COLUMN")
		if l.upper(j) == "IF" {
			j = l.skip(l.skip(j, "IF", "NOT", "EXISTS"), "IF", "EXISTS")
		}
		if j >= end {
			return "", j
		}
		n, k := l.name(j)
		return n, k
	}
	switch l.upper(i) {
	case "DROP":
		if c, _ := column(i + 1); c != "" {
			l.add("drop-column", table+"."+c, "drops column %s and its data")
		}
	case "MODIFY":
		if c, _ := column(i + 1); c != "" {
			l.add("alter-type", table+"."+c, "changes the type of column %s, which can fail or change values")
		}
	case "ALTER":
		c, j := column(i + 1)
		if c == "" {
			return
		}
		switch {
		case l.upper(j) == "TYPE", l.skip(j, "SET", "DATA", "TYPE") > j:
			l.add("alter-type", table+"."+c, "changes the type of column %s, which can fail or change values")
		case l.skip(j, "SET", "NOT", "NULL") > j:
			l.add("set-not-null", table+"."+c, "makes column %s NOT NULL; it fails if any row holds NULL")
		}
	}
}
