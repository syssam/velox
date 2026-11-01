package migrate

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
)

// LintFile checks f as velox-gen migrate lint does. Its error, *Error values
// with Code "file" joined, is for CONCURRENTLY or ALTER TYPE … ADD VALUE in
// a PostgreSQL file run in a transaction, and for BEGIN, START, COMMIT,
// ROLLBACK or END leading a statement of a PostgreSQL or SQLite file run in
// one, but ROLLBACK TO a savepoint. Its findings are the destructive
// statements it reads: DROP TABLE t (drop-table t), DROP SCHEMA s
// (drop-schema s), DROP DATABASE d (drop-database d), DROP DOMAIN, TYPE or
// COLLATION x (drop-domain x, drop-type x, drop-collation x), ALTER TABLE t DROP
// COLUMN c (drop-column t.c), ALTER TABLE t DROP PARTITION p (drop-partition t.p),
// ALTER TABLE t TRUNCATE PARTITION p (delete-rows t.p), ALTER TABLE t ALTER COLUMN c [SET DATA] TYPE,
// MODIFY COLUMN c and CHANGE COLUMN c (alter-type t.c), ALTER COLUMN c SET
// NOT NULL (set-not-null t.c), TRUNCATE t and DELETE FROM t without WHERE
// (delete-rows t), UPDATE t without WHERE (update-rows t); Pos is
// "<path>:<line>", Change the statement's index.
//
// A file that starts with Dir.Add's header (f.generated) is read too, since
// anyone can add to it, but for what Render writes with no allow directive
// to cover it: a rebuild's DROP TABLE, a DROP TYPE without CASCADE, and any
// alter-type, as a widening or a change of a column's comment is. A type change
// added by hand to such a file therefore goes unseen.
//
// A WHERE counts only outside parentheses, so a subquery's does not, and
// only if it can leave a row out: WHERE true, WHERE 1 and WHERE 1 = 1 are
// none. A DELETE or UPDATE may follow a WITH clause; MySQL's multi-table
// DELETE t1, t2 FROM … (aliases read as their tables) and UPDATE t1, t2
// SET … name each table. The DELETE and UPDATE statements in a WITH clause's
// bodies count like any other, and a MERGE counts by its WHEN clauses, whatever
// its ON says: THEN DELETE as delete-rows, THEN UPDATE as update-rows. SQLite's
// UPDATE OR <action> names the table after the action. PostgreSQL's ABORT counts
// as ROLLBACK.
//
// A SQLite file that rebuilds a table (CREATE TABLE of ddl.Rebuild(t), or of a
// name the file renames to t and with DROP TABLE t) without -- velox:foreign-keys
// off is refused too: its DROP TABLE deletes the children's rows.
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
	rebuilt := map[string]bool{} // the tables a rebuild of f drops and makes again
	if d == velox.SQLite {
		for _, r := range fileRebuilds(d, f) {
			if f.generated {
				rebuilt[r.table.String()] = true
			}
			if !f.Directives.ForeignKeysOff {
				pos := f.Path
				if r.start < len(f.lines) {
					pos += ":" + strconv.Itoa(f.lines[r.start])
				}
				errs = append(errs, &Error{Code: "file", Pos: pos, Reason: fmt.Sprintf("the rebuild of %s has no -- velox:foreign-keys off; its DROP TABLE deletes the rows of the tables that reference it", r.table)})
			}
		}
	}
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
		if inTx && txControl(d, l) {
			refuse(txControlReason, lead)
		}
		if d == velox.Postgres && !f.Directives.NoTransaction {
			if l.concurrently() {
				refuse("CONCURRENTLY cannot run in a transaction; put the statement in a file with -- velox:no-transaction")
			}
			if lead == "ALTER" && l.upper(1) == "TYPE" && l.has("ADD", "VALUE") {
				refuse("ALTER TYPE … ADD VALUE in a transaction leaves the value unusable until it commits; put the statement in a file with -- velox:no-transaction")
			}
		}
		l.classify()
		for _, fd := range l.out {
			rendered := fd.Code == "alter-type" || fd.Code == "drop-table" && rebuilt[fd.Target] ||
				fd.Code == "drop-type" && !l.has("CASCADE")
			if !f.generated || !rendered {
				out = append(out, fd)
			}
		}
	}
	return out, errors.Join(errs...)
}

// txControlReason is the refusal of a statement led by BEGIN, COMMIT and
// the like in a file run in a transaction.
const txControlReason = "%s in a file run in a transaction; the runner begins and commits the file's transaction itself"

// txControl reports whether l's statement begins or ends a transaction: it
// is led by BEGIN, START, COMMIT, ROLLBACK, END, or on PostgreSQL ABORT.
// ROLLBACK [WORK | TRANSACTION] TO, to a savepoint, ends none.
func txControl(d velox.Dialect, l *fileLinter) bool {
	lead := l.upper(0)
	if lead == "ROLLBACK" {
		next := l.upper(1)
		if next == "WORK" || next == "TRANSACTION" {
			next = l.upper(2)
		}
		return next != "TO"
	}
	return slices.Contains([]string{"BEGIN", "START", "COMMIT", "END"}, lead) || d == velox.Postgres && lead == "ABORT"
}

// checkTxControl is LintFile's refusal of the first statement of f, a file
// run in a transaction, that begins or ends one, at its line; nil when none
// does.
func checkTxControl(d velox.Dialect, f File) error {
	for i, sql := range f.Statements {
		l := &fileLinter{d: d, ws: lex(d, sql)}
		if lead := l.upper(0); txControl(d, l) {
			pos := f.Path
			if i < len(f.lines) {
				pos += ":" + strconv.Itoa(f.lines[i])
			}
			return &Error{Code: "file", Pos: pos, Reason: fmt.Sprintf(txControlReason, lead)}
		}
	}
	return nil
}

// touchedTables is the tables of main f's statements write rows or
// definitions of, lower-cased. known is false when a statement is not one
// it reads, or names a table of another database.
func touchedTables(d velox.Dialect, f File) (tables map[string]bool, known bool) {
	tables = map[string]bool{}
	for _, sql := range f.Statements {
		l := &fileLinter{d: d, ws: lex(d, sql)}
		add := func(i int) (int, bool) {
			n, j := l.name(i)
			n = strings.TrimPrefix(n, "main.")
			tables[strings.ToLower(n)] = true
			return j, n != "" && !strings.Contains(n, ".")
		}
		ok := true
		switch l.upper(0) {
		case "SELECT", "SAVEPOINT", "RELEASE", "ROLLBACK", "ANALYZE", "REINDEX", "":
		case "CREATE":
			switch l.upper(1) {
			case "INDEX", "UNIQUE", "VIEW":
			case "TABLE":
				_, ok = add(l.skip(2, "IF", "NOT", "EXISTS"))
			default:
				return nil, false
			}
		case "DROP":
			switch l.upper(1) {
			case "INDEX", "VIEW", "TRIGGER":
			case "TABLE":
				_, ok = add(l.skip(2, "IF", "EXISTS"))
			default:
				return nil, false
			}
		case "ALTER":
			if l.upper(1) != "TABLE" {
				return nil, false
			}
			var j int
			j, ok = add(2)
			if ok && l.upper(j) == "RENAME" && l.upper(j+1) == "TO" {
				_, ok = add(j + 2)
			}
		case "INSERT", "REPLACE":
			i := 1
			if l.upper(1) == "OR" {
				i = 3
			}
			if l.upper(i) != "INTO" {
				return nil, false
			}
			_, ok = add(i + 1)
		case "UPDATE":
			i := 1
			if l.upper(1) == "OR" {
				i = 3
			}
			_, ok = add(i)
		case "DELETE":
			if l.upper(1) != "FROM" {
				return nil, false
			}
			_, ok = add(2)
		default:
			return nil, false
		}
		if !ok {
			return nil, false
		}
	}
	return tables, true
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

// classify adds the statement's destructive findings. A WITH clause before
// a DELETE or UPDATE is passed over.
func (l *fileLinter) classify() {
	const (
		dropTable  = "drops table %s and its rows"
		dropSchema = "drops schema %s and everything in it"
		dropDB     = "drops database %s and everything in it"
		dropType   = "drops %s; with CASCADE, the columns of that type and their data go too"
		deleteRows = "deletes every row of %s"
		updateRows = "updates every row of %s"
	)
	b := 0
	if l.upper(0) == "WITH" {
		b = l.afterWith()
		l.withBodies(b)
	}
	switch l.upper(b) {
	case "DROP":
		if b != 0 {
			return
		}
		switch l.upper(1) {
		case "TABLE":
			l.names(l.skip(2, "IF", "EXISTS"), "drop-table", dropTable)
		case "SCHEMA":
			l.names(l.skip(2, "IF", "EXISTS"), "drop-schema", dropSchema)
		case "DATABASE":
			l.names(l.skip(2, "IF", "EXISTS"), "drop-database", dropDB)
		case "DOMAIN", "TYPE":
			l.names(l.skip(2, "IF", "EXISTS"), "drop-"+strings.ToLower(l.upper(1)), dropType)
		case "COLLATION":
			l.names(l.skip(2, "IF", "EXISTS"), "drop-collation", "drops collation %s; with CASCADE, the columns that use it and their data go too")
		}
	case "TRUNCATE":
		if b == 0 {
			l.names(l.skip(1, "TABLE"), "delete-rows", deleteRows)
		}
	case "DELETE":
		if l.topWhere(b) {
			return
		}
		i := b + 1
		for slices.Contains([]string{"LOW_PRIORITY", "QUICK", "IGNORE"}, l.upper(i)) {
			i++
		}
		if l.upper(i) == "FROM" {
			// DELETE FROM t, or MySQL's DELETE FROM t1, t2 USING ….
			l.names(i+1, "delete-rows", deleteRows)
			return
		}
		// MySQL's DELETE t1[, t2] FROM …: the tables, or their aliases, come
		// before FROM.
		for _, n := range l.nameList(i) {
			l.add("delete-rows", l.unalias(n, i), deleteRows)
		}
	case "UPDATE":
		if l.topWhere(b) {
			return
		}
		i := b + 1
		for slices.Contains([]string{"LOW_PRIORITY", "IGNORE"}, l.upper(i)) {
			i++
		}
		if l.upper(i) == "OR" { // SQLite's UPDATE OR REPLACE t
			i += 2
		}
		// UPDATE t [alias] [JOIN …], or MySQL's UPDATE t1 [a1], t2 [a2] SET ….
		for j := i; ; {
			j = l.skip(j, "ONLY")
			n, k := l.name(j)
			if n == "" {
				return
			}
			l.add("update-rows", n, updateRows)
			if l.upper(k) == "AS" {
				k++
			}
			if k < len(l.ws) && l.isName(k) && !slices.Contains([]string{"SET", "JOIN", "INNER", "LEFT", "RIGHT", "CROSS", "STRAIGHT_JOIN", "NATURAL"}, l.upper(k)) {
				k++ // an alias
			}
			if !l.punct(k, ",") {
				return
			}
			j = k + 1
		}
	case "MERGE":
		n, _ := l.name(l.skip(l.skip(b+1, "INTO"), "ONLY"))
		if n == "" {
			return
		}
		if l.has("THEN", "DELETE") {
			l.add("delete-rows", n, "merges into %s, and can delete any row its ON matches")
		}
		if l.has("THEN", "UPDATE") {
			l.add("update-rows", n, "merges into %s, and can update any row its ON matches")
		}
	case "ALTER":
		if b == 0 && l.upper(1) == "TABLE" {
			l.alterTable()
		}
	}
}

// withBodies adds the findings of the bodies of the WITH clause that ends at
// word b: each "AS [[NOT] MATERIALIZED] (…)" outside parentheses.
func (l *fileLinter) withBodies(b int) {
	for i, depth := 0, 0; i < b; i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && l.upper(i) == "AS":
			open := l.skip(l.skip(i+1, "NOT"), "MATERIALIZED")
			end := l.closes(open)
			if !l.punct(open, "(") || end < 0 {
				continue
			}
			body := &fileLinter{d: l.d, ws: l.ws[open+1 : end], pos: l.pos, change: l.change}
			body.classify()
			l.out = append(l.out, body.out...)
			i = end
		}
	}
}

// afterWith is the index of the statement after a WITH clause: the first
// word outside parentheses after its last "AS (…)".
func (l *fileLinter) afterWith() int {
	depth := 0
	for i, w := range l.ws {
		if w.quoted {
			continue
		}
		switch w.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && i > 0 && slices.Contains([]string{"SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "TABLE"}, l.upper(i)) && l.punct(i-1, ")") {
			return i
		}
	}
	return len(l.ws)
}

// topWhere reports whether a WHERE from word b on is the statement's own,
// outside parentheses, and can leave a row out.
func (l *fileLinter) topWhere(b int) bool {
	depth := 0
	for i := b; i < len(l.ws); i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && l.upper(i) == "WHERE":
			end := i + 1
			for d := 0; end < len(l.ws); end++ {
				if l.punct(end, "(") {
					d++
				} else if l.punct(end, ")") {
					d--
				}
				if d == 0 && slices.Contains([]string{"RETURNING", "ORDER", "LIMIT"}, l.upper(end)) {
					break
				}
			}
			return !l.alwaysTrue(i+1, end)
		}
	}
	return false
}

// alwaysTrue reports whether the condition in words [i, j) holds for every
// row: TRUE, a number but 0, NOT FALSE, a literal or number equal to itself,
// a name equal to itself, and those joined by AND and OR. A literal is no
// word, so 'a' = 'b' reads as 'a' = 'a': a condition nobody writes, and the
// finding errs on the safe side.
func (l *fileLinter) alwaysTrue(i, j int) bool {
	for i < j && l.punct(i, "(") && l.closes(i) == j-1 {
		i, j = i+1, j-1
	}
	if i >= j {
		return false
	}
	for _, kw := range [...]string{"OR", "AND"} {
		var cuts []int // the top-level kw's
		for k, depth := i, 0; k < j; k++ {
			if l.punct(k, "(") {
				depth++
			} else if l.punct(k, ")") {
				depth--
			}
			if depth == 0 && l.upper(k) == kw {
				cuts = append(cuts, k)
			}
		}
		if len(cuts) == 0 {
			continue
		}
		cuts = append(cuts, j)
		from, some, every := i, false, true
		for _, k := range cuts {
			t := l.alwaysTrue(from, k)
			some, every = some || t, every && t
			from = k + 1
		}
		if kw == "OR" {
			return some
		}
		return every
	}
	number := func(k int) (int, bool) {
		if l.ws[k].quoted {
			return 0, false
		}
		n, err := strconv.Atoi(l.ws[k].text)
		return n, err == nil
	}
	switch j - i {
	case 1:
		n, isNumber := number(i)
		return l.upper(i) == "TRUE" || isNumber && n != 0 || l.punct(i, "=")
	case 2:
		n, isNumber := number(i + 1)
		return l.upper(i) == "NOT" && (l.upper(i+1) == "FALSE" || isNumber && n == 0)
	case 3:
		return l.punct(i+1, "=") && l.isName(i) && l.isName(i+2) && l.ws[i].text == l.ws[i+2].text && l.ws[i].quoted == l.ws[i+2].quoted
	}
	return false
}

// closes is the index of the parenthesis that closes the one at i, or -1.
func (l *fileLinter) closes(i int) int {
	depth := 0
	for k := i; k < len(l.ws); k++ {
		if l.punct(k, "(") {
			depth++
		} else if l.punct(k, ")") {
			if depth--; depth == 0 {
				return k
			}
		}
	}
	return -1
}

// punct reports whether the i-th word is the punctuation p.
func (l *fileLinter) punct(i int, p string) bool {
	return i >= 0 && i < len(l.ws) && !l.ws[i].quoted && l.ws[i].text == p
}

// isName reports whether the i-th word can be a name: quoted, or a word.
func (l *fileLinter) isName(i int) bool {
	w := l.ws[i]
	return w.quoted || w.text != "" && wordByte(w.text[0])
}

// nameList reads names joined by commas from i.
func (l *fileLinter) nameList(i int) []string {
	var out []string
	for {
		n, j := l.name(i)
		if n == "" {
			return out
		}
		out = append(out, n)
		if !l.punct(j, ",") {
			return out
		}
		i = j + 1
	}
}

// unalias is the table that alias names in the statement after word from:
// the table written before it, as "t alias" or "t AS alias"; alias itself
// when none is.
func (l *fileLinter) unalias(alias string, from int) string {
	for i := from; i < len(l.ws); i++ {
		n, j := l.name(i)
		if n == "" || slices.Contains(notAlias, l.upper(i)) {
			continue
		}
		if l.upper(j) == "AS" {
			j++
		}
		if a, _ := l.name(j); a == alias && !strings.Contains(alias, ".") && j < len(l.ws) && !slices.Contains(notAlias, l.upper(j)) {
			return n
		}
		i = j - 1
	}
	return alias
}

// notAlias are the words after a table that are not its alias.
var notAlias = []string{"FROM", "JOIN", "ON", "USING", "WHERE", "SET", "INNER", "LEFT", "RIGHT", "CROSS", "NATURAL", "STRAIGHT_JOIN", "AS"}

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
		if l.partitions(table, i) {
			return
		}
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

// partitions adds the findings of MySQL's DROP or TRUNCATE PARTITION at word i,
// whose names, joined by commas, end the statement, and reports whether it is one.
func (l *fileLinter) partitions(table string, i int) bool {
	code, format := "drop-partition", "drops partition %s and its rows"
	switch {
	case l.upper(i) == "DROP" && l.upper(i+1) == "PARTITION":
	case l.upper(i) == "TRUNCATE" && l.upper(i+1) == "PARTITION":
		code, format = "delete-rows", "deletes every row of partition %s"
	default:
		return false
	}
	for j := i + 2; ; {
		n, k := l.name(j)
		if n == "" {
			return true
		}
		l.add(code, table+"."+n, format)
		if !l.punct(k, ",") {
			return true
		}
		j = k + 1
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
	case "MODIFY", "CHANGE":
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
