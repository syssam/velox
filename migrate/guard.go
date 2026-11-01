package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// What a file run by Up would destroy that its change list does not say is only
// visible at apply time: a plan is made with no database. A SQLite rebuild writes
// only what the snapshot holds, and a MySQL MODIFY COLUMN, a PostgreSQL ALTER
// COLUMN TYPE and a foreign key dropped and added again replace a whole
// definition. See decision 0026.

// lossCode is the allow directive's code that lets such a loss through.
const lossCode = "unmodeled-loss"

// lossError is Up's refusal of a loss, the lines saying what is lost, and
// allow lines for each target; keep says how to keep it.
func lossError(f File, object, what string, lines, targets []string, keep string) error {
	var b strings.Builder
	b.WriteString(what + "; nothing was changed:")
	for _, l := range lines {
		b.WriteString("\n  " + l)
	}
	b.WriteString("\n  " + keep + "; or to accept the loss, add above the file's first statement:")
	for _, t := range targets {
		b.WriteString("\n    -- velox:allow " + lossCode + " " + allowTarget(t))
	}
	b.WriteString("\n  and run velox-gen migrate hash")
	return &Error{Code: "unmodeled", Pos: f.Path, Object: object, Reason: b.String()}
}

// guardError is err as applyTx returns it: an *Error of the guard as it is,
// and its failure to read as the file's rollback.
func guardError(err error, rolledBack func(string, ...any) error) error {
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	return rolledBack("%w", err)
}

// lossAllowed reports whether f accounts for the loss of object: a directive
// accepts it, or its change list, -- velox:drops, says the file takes it away.
func lossAllowed(f File, object string) bool {
	return slices.Contains(f.Directives.Drops, object) || slices.ContainsFunc(f.Directives.Allow, func(a Allow) bool {
		return a.Code == lossCode && (a.Target == "" || a.Target == object) ||
			a.Code == "drop-column" && a.Target == object // the column goes on purpose, and with it what it has
	})
}

// A rebuildGuard watches one file run on SQLite, in its transaction: its
// rebuilds, and its DROP TABLEs of tables others reference. A nil one, of a file
// with neither, watches nothing.
type rebuildGuard struct {
	s       *session
	f       File
	starts  map[int]rebuildOf  // the statement that makes a rebuild's new table: the rebuild
	drops   map[int][]TableRef // the DROP TABLE statements: what each drops
	rebuilt map[string]bool    // the tables f rebuilds, by foldRef
	gone    map[string]bool    // the tables f rebuilds or drops, by foldRef
	seen    []rebuildSeen
}

// rebuildSeen is a rebuilt table's notes and definition as the rebuild found
// them.
type rebuildSeen struct {
	table TableRef
	tmp   TableRef
	notes []note
	had   Table
}

// guardRebuilds is the guard of f on SQLite, when f has a rebuild or drops a
// table.
func (s *session) guardRebuilds(f File) *rebuildGuard {
	if s.d != velox.SQLite {
		return nil
	}
	g := &rebuildGuard{s: s, f: f, starts: map[int]rebuildOf{}, drops: map[int][]TableRef{}, rebuilt: map[string]bool{}, gone: map[string]bool{}}
	for _, r := range fileRebuilds(s.d, f) {
		g.starts[r.start] = r
		g.rebuilt[foldRef(r.table)] = true
		g.gone[foldRef(r.table)] = true
	}
	for i, q := range f.Statements {
		l := &fileLinter{d: s.d, ws: lex(s.d, q)}
		if refs := l.droppedTables(); len(refs) > 0 {
			g.drops[i] = refs
			for _, ref := range refs {
				g.gone[foldRef(ref)] = true
			}
		}
	}
	if len(g.starts) == 0 && len(g.drops) == 0 {
		return nil
	}
	return g
}

// before runs ahead of statement i. At a rebuild's first statement it refuses
// a view or another table's trigger that uses the table, whose ALTER TABLE
// RENAME would fail on them, and reads the table for after; at a DROP TABLE, it
// refuses what the drop would do to the tables that reference it.
func (g *rebuildGuard) before(ctx context.Context, i int) error {
	if g == nil {
		return nil
	}
	for _, ref := range g.drops[i] {
		if err := g.children(ctx, ref); err != nil {
			return err
		}
	}
	r, ok := g.starts[i]
	if !ok {
		return nil
	}
	s, t := g.s, r.table
	lines, err := s.dependents(ctx, t)
	if err != nil {
		return fmt.Errorf("reading what uses %s: %w", t, err)
	}
	if len(lines) > 0 {
		var b strings.Builder
		b.WriteString("its rebuild cannot run while another object uses it; nothing was changed:")
		for _, l := range lines {
			b.WriteString("\n  " + l)
		}
		b.WriteString("\n  SQLite's RENAME fails on them. Add DROP VIEW above the rebuild in the file and CREATE VIEW after it, then run velox-gen migrate hash; a trigger, which a file cannot hold, is dropped by hand before applying and made again after")
		return &Error{Code: "unmodeled", Pos: g.f.Path, Object: t.String(), Reason: b.String()}
	}
	schema, notes, err := inspectSQLite(ctx, s.conn, []TableRef{t})
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", t, err)
	}
	if len(schema.Tables) == 0 {
		return nil
	}
	seen := rebuildSeen{table: t, tmp: r.tmp, notes: notes, had: schema.Tables[0]}
	g.seen = append(g.seen, seen)
	return g.checkRowid(ctx, seen)
}

// checkRowid refuses a copy of the rowid of a table that has none, WITHOUT
// ROWID, which no file can run; the plan cannot know of it.
func (g *rebuildGuard) checkRowid(ctx context.Context, seen rebuildSeen) error {
	c := g.copied(seen.tmp)
	name := ""
	for _, n := range [...]string{"rowid", "_rowid_", "oid"} {
		if c.kept[n] && !slices.ContainsFunc(seen.had.Columns, func(col Column) bool { return strings.EqualFold(col.Name, n) }) {
			name = n
		}
	}
	if name == "" {
		return nil
	}
	rows, err := g.s.conn.QueryContext(ctx, "SELECT "+name+" FROM "+sqliteRef(seen.table)+" LIMIT 0")
	if err == nil {
		return rows.Close()
	}
	if !strings.Contains(err.Error(), "no such column") {
		return fmt.Errorf("reading %s's %s: %w", seen.table, name, err)
	}
	return &Error{Code: "unmodeled", Pos: g.f.Path, Object: seen.table.String(),
		Reason: "the table is WITHOUT ROWID, so its rebuild's INSERT cannot copy " + name + "; nothing was changed. Take " + name + " out of the INSERT's two lists in the file, then run velox-gen migrate hash"}
}

// sqliteRef is ref as a statement names it.
func sqliteRef(ref TableRef) string {
	if ref.Schema == "" {
		return ddl.Ident(velox.SQLite, ref.Name)
	}
	return ddl.Ident(velox.SQLite, ref.Schema) + "." + ddl.Ident(velox.SQLite, ref.Name)
}

// children refuses what DROP TABLE ref does to the tables that reference ref.
// With foreign keys on it deletes their rows at once, so only a child already gone
// by the drop counts: rebuilding it later copies nothing. With them off a rebuild
// of ref does nothing to its children, and a plain drop leaves them a key to
// nothing unless the file goes on to drop or rebuild them.
func (g *rebuildGuard) children(ctx context.Context, ref TableRef) error {
	rebuild := g.rebuilt[foldRef(ref)]
	var on int
	if err := g.s.conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&on); err != nil {
		return fmt.Errorf("reading foreign_keys: %w", err)
	}
	if rebuild && on == 0 {
		return nil
	}
	db := cmp.Or(ref.Schema, "main")
	q := `SELECT DISTINCT m.name FROM ` + ddl.Ident(velox.SQLite, db) + `.sqlite_schema m, pragma_foreign_key_list(m.name, ?) k
WHERE m.type = 'table' AND k."table" = ? COLLATE NOCASE AND m.name <> ? COLLATE NOCASE ORDER BY m.name`
	var lines, targets []string
	err := eachRow(ctx, g.s.conn, q, []any{db, ref.Name, ref.Name}, func(rows *sql.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		child := TableRef{Schema: ref.Schema, Name: name}
		if on == 0 && g.gone[foldRef(child)] || lossAllowed(g.f, child.String()) {
			return nil
		}
		lines = append(lines, "the table "+child.String()+" has a foreign key to it, whose rows its DROP TABLE would delete or leave pointing at nothing")
		targets = append(targets, child.String())
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading what references %s: %w", ref, err)
	}
	if len(lines) == 0 {
		return nil
	}
	keep := "to keep them, drop the child first, or write -- velox:foreign-keys off above the file's first statement and drop or rebuild it in the file"
	if rebuild {
		keep = "to keep them, write -- velox:foreign-keys off above the file's first statement"
	}
	return lossError(g.f, ref.String(), "its DROP TABLE reaches a table the file does not account for", lines, targets, keep)
}

// after runs behind the file's last statement, with the table as the file left
// it: what a rebuilt table had and has no longer is a loss, unless the file
// accounts for it (lossAllowed). What the file made again, or kept in its new
// table, is no loss. Its refusal rolls the file back.
func (g *rebuildGuard) after(ctx context.Context) error {
	if g == nil {
		return nil
	}
	for _, seen := range g.seen {
		schema, now, err := inspectSQLite(ctx, g.s.conn, []TableRef{seen.table})
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", seen.table, err)
		}
		if len(schema.Tables) == 0 {
			continue // the file dropped it after the rebuild: a change of its own
		}
		c := g.copied(seen.tmp)
		var lines, targets []string
		lost := func(object, line string) {
			lines = append(lines, line)
			target, _, _ := strings.Cut(object, ":")
			if !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
		for _, n := range seen.notes {
			object := n.object
			if name, ok := strings.CutPrefix(object, seen.table.String()+"."); ok {
				if to, ok := c.renamed[strings.ToLower(name)]; ok {
					object = colTarget(seen.table, to)
				}
			}
			kept := slices.ContainsFunc(now, func(m note) bool { return m.object == object && m.message == n.message })
			if !kept && !lossAllowed(g.f, n.object) && !lossAllowed(g.f, object) {
				lost(n.object, n.object+": "+n.message)
			}
		}
		if err := g.rebuildLoss(seen, c, schema.Tables[0], lost); err != nil {
			return fmt.Errorf("comparing %s with its rebuild: %w", seen.table, err)
		}
		if len(lines) > 0 {
			return lossError(g.f, seen.table.String(), "its rebuild would lose what the snapshot does not hold or the model cannot", lines, targets,
				"to keep one, write it into the file after the rebuild (a trigger, which a file cannot hold, is made by hand after applying, under the directive)")
		}
	}
	return nil
}

// A rowCopy is what a rebuild's INSERT reads of the old table: every name of its
// SELECT list, in lower case, and each column it copies under another name.
type rowCopy struct {
	all     bool // SELECT *
	kept    map[string]bool
	renamed map[string]string // by the old name, in lower case
}

// copied reads the INSERT statements into tmp.
func (g *rebuildGuard) copied(tmp TableRef) rowCopy {
	c := rowCopy{kept: map[string]bool{}, renamed: map[string]string{}}
	for _, q := range g.f.Statements {
		l := &fileLinter{d: g.s.d, ws: lex(g.s.d, q)}
		l.insertInto(tmp, &c)
	}
	return c
}

// insertInto adds to c what the statement copies, when it is an INSERT into tmp.
func (l *fileLinter) insertInto(tmp TableRef, c *rowCopy) {
	i := 1
	if l.upper(0) != "INSERT" && l.upper(0) != "REPLACE" {
		return
	}
	if l.upper(i) == "OR" {
		i += 2
	}
	if l.upper(i) != "INTO" {
		return
	}
	ref, i, ok := l.tableRef(i + 1)
	if !ok || foldRef(ref) != foldRef(tmp) {
		return
	}
	var into []string
	if l.punct(i, "(") {
		end := l.closes(i)
		if end < 0 {
			return
		}
		for j := i + 1; j < end; j++ {
			if !l.punct(j, ",") {
				into = append(into, l.ws[j].text)
			}
		}
		i = end + 1
	}
	depth := 0
	for ; i < len(l.ws) && (depth != 0 || l.upper(i) != "SELECT"); i++ {
		if l.punct(i, "(") {
			depth++
		} else if l.punct(i, ")") {
			depth--
		}
	}
	item, names := 0, []string(nil)
	flush := func() {
		for _, n := range names {
			c.kept[strings.ToLower(n)] = true
		}
		if len(names) > 0 && item < len(into) && !strings.EqualFold(names[len(names)-1], into[item]) && len(names) == 1 {
			c.renamed[strings.ToLower(names[0])] = into[item]
		}
		names = nil
	}
	for i++; i < len(l.ws); i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && l.punct(i, ","):
			flush()
			item++
		case depth == 0 && l.upper(i) == "FROM":
			flush()
			return
		case l.punct(i, "*") && depth == 0:
			c.all = true
		case l.isName(i) && !l.punct(i+1, "."): // of a.b, b
			names = append(names, l.ws[i].text)
		}
	}
	flush()
}

// rebuildLoss calls lost for what the table had and now has not, by its
// definition, names apart: a column the INSERT does not read, and of what stays,
// a column's attribute, an index, a foreign key, a check or the primary key.
// What is over a column that goes, goes with it.
func (g *rebuildGuard) rebuildLoss(seen rebuildSeen, c rowCopy, now Table, lost func(object, line string)) error {
	t := seen.table
	gone := map[string]bool{}
	for _, col := range seen.had.Columns {
		if c.all || c.kept[strings.ToLower(col.Name)] {
			continue
		}
		gone[col.Name] = true
		if !lossAllowed(g.f, colTarget(t, col.Name)) {
			lost(colTarget(t, col.Name), colTarget(t, col.Name)+": the column is in the database, and its rebuild's INSERT does not copy it")
		}
	}
	had := (&Schema{Tables: []Table{seen.had}}).clone().Tables[0]
	for from, to := range c.renamed {
		i := slices.IndexFunc(had.Columns, func(col Column) bool { return strings.EqualFold(col.Name, from) })
		if i >= 0 && now.column(to) != nil && had.column(to) == nil {
			renameColumnIn(&had, had.Columns[i].Name, to)
		}
	}
	had.Columns = slices.DeleteFunc(had.Columns, func(col Column) bool { return gone[col.Name] })
	over := func(cols []string, exprs ...string) bool {
		for name := range gone {
			if slices.Contains(cols, name) || slices.ContainsFunc(exprs, func(e string) bool { return e != "" && namesColumn(velox.SQLite, e, name) }) {
				return true
			}
		}
		return false
	}
	if !over(had.PrimaryKey) && !slices.Equal(had.PrimaryKey, now.PrimaryKey) && !lossAllowed(g.f, t.String()) {
		lost(t.String(), t.String()+": the table has "+keyText(had.PrimaryKey)+", and its rebuild writes "+keyText(now.PrimaryKey))
	}
	cmpr, err := compare(&Schema{Dialect: velox.SQLite, Tables: []Table{had}}, nil, &Schema{Dialect: velox.SQLite, Tables: []Table{now}}, false)
	if err != nil {
		return err
	}
	changes, err := Diff(cmpr.db, cmpr.snap, DiffOptions{})
	if err != nil {
		return err
	}
	attr := func(col, name, what string) {
		object := colTarget(t, col)
		if !lossAllowed(g.f, object+":"+name) && !lossAllowed(g.f, object) {
			lost(object+":"+name, object+": "+what)
		}
	}
	object := func(object, what string) {
		if !lossAllowed(g.f, object) {
			lost(object, object+": "+what+" is in the database, and its rebuild does not make it")
		}
	}
	for _, ch := range changes {
		switch ch := ch.(type) {
		case alterColumn:
			from, to := ch.From, ch.To
			if ch.What&columnType != 0 {
				attr(to.Name, "type", "the column is "+from.Type+", and its rebuild makes it "+to.Type)
			}
			if ch.What&columnNullable != 0 && !from.Nullable {
				attr(to.Name, "not-null", "the column is NOT NULL, and its rebuild makes it nullable")
			}
			if ch.What&columnDefault != 0 && from.Default != "" {
				attr(to.Name, "default", "the column has the default "+from.Default+", and its rebuild does not keep it")
			}
			if ch.What&columnIdentity != 0 && from.Identity {
				attr(to.Name, "identity", "the column is AUTOINCREMENT, and its rebuild does not keep it")
			}
		case dropIndex:
			if !over(ch.Index.Columns, ch.Index.Where) {
				object(indexObject(t, ch.Index), "the index")
			}
		case dropForeignKey:
			again := slices.ContainsFunc(changes, func(o Change) bool {
				a, ok := o.(addForeignKey)
				return ok && sameForeignKey(a.ForeignKey, ch.ForeignKey)
			})
			if !again && !over(ch.ForeignKey.Columns) {
				object(keyObject(t, ch.ForeignKey), "the foreign key")
			}
		case dropCheck:
			if !over(nil, ch.Check.Expr) {
				object(checkObject(t, ch.Check), "the check")
			}
		}
	}
	// Drift's comparison takes one expression default for another with a
	// warning; here they are one only when they read the same.
	for _, col := range had.Columns {
		u := now.column(col.Name)
		if u == nil || u.Default == col.Default || u.Default == "" || col.Default == "" || literalDefault(velox.SQLite, u.Default) || literalDefault(velox.SQLite, col.Default) {
			continue
		}
		if same, _ := cmpr.same(col.Default, u.Default); !same {
			attr(col.Name, "default", "the column has the default "+col.Default+", and its rebuild does not keep it")
		}
	}
	return nil
}

// renameColumnIn renames the column of t, and what names it there.
func renameColumnIn(t *Table, from, to string) {
	re := func(cols []string) {
		for i, c := range cols {
			if c == from {
				cols[i] = to
			}
		}
	}
	for i := range t.Columns {
		if t.Columns[i].Name == from {
			t.Columns[i].Name = to
		}
	}
	re(t.PrimaryKey)
	for i := range t.Indexes {
		re(t.Indexes[i].Columns)
		t.Indexes[i].Where = renameIdent(velox.SQLite, t.Indexes[i].Where, from, to)
	}
	for i := range t.ForeignKeys {
		re(t.ForeignKeys[i].Columns)
		if t.ForeignKeys[i].RefTable == t.Ref() {
			re(t.ForeignKeys[i].RefColumns)
		}
	}
	for i := range t.Checks {
		t.Checks[i].Expr = renameIdent(velox.SQLite, t.Checks[i].Expr, from, to)
	}
}

// dependents is a line for each view, and each trigger of another table, of t's
// database that uses t, which SQLite's RENAME fails on once t is dropped. SQLite
// says which itself: in a savepoint, a rename of t rewrites its uses, and only
// them, not a column of another table or an alias that has its name. When that
// rename fails, on a view broken already, the text is read, which errs on the
// safe side.
func (s *session) dependents(ctx context.Context, t TableRef) ([]string, error) {
	db := cmp.Or(t.Schema, "main")
	schema := ddl.Ident(velox.SQLite, db) + ".sqlite_schema"
	var has int
	err := eachRow(ctx, s.conn, `SELECT count(*) FROM `+schema+` WHERE type = 'table' AND name = ?`, []any{t.Name}, func(rows *sql.Rows) error { return rows.Scan(&has) })
	if err != nil || has == 0 {
		return nil, err
	}
	const point, probe = "velox_migrate_dependents", "velox_migrate_probe"
	if _, err := s.conn.ExecContext(ctx, "SAVEPOINT "+point); err != nil {
		return nil, err
	}
	defer func() {
		s.undo(ctx, "ROLLBACK TO "+point)
		s.undo(ctx, "RELEASE "+point)
	}()
	q := `SELECT type, name, tbl_name, coalesce(sql, '') FROM ` + schema + ` WHERE type IN ('view', 'trigger') ORDER BY type DESC, name`
	used := func(string) bool { return false }
	other := func(table string) bool { return !strings.EqualFold(table, probe) }
	if _, err := s.conn.ExecContext(ctx, "ALTER TABLE "+sqliteRef(t)+" RENAME TO "+probe); err == nil {
		used = func(text string) bool {
			return slices.ContainsFunc(lex(velox.SQLite, text), func(w word) bool { return strings.EqualFold(w.text, probe) })
		}
	} else {
		other = func(table string) bool { return !strings.EqualFold(table, t.Name) }
		used = func(text string) bool {
			return slices.ContainsFunc(lex(velox.SQLite, text), func(w word) bool { return strings.EqualFold(w.text, t.Name) })
		}
	}
	var lines []string
	err = eachRow(ctx, s.conn, q, nil, func(rows *sql.Rows) error {
		var typ, name, table, text string
		if err := rows.Scan(&typ, &name, &table, &text); err != nil {
			return err
		}
		if typ == "trigger" && !other(table) {
			return nil // it goes with the table: a note
		}
		if used(text) {
			lines = append(lines, fmt.Sprintf("the %s %s names %s", typ, name, t))
		}
		return nil
	})
	return lines, err
}

// tableRef reads a table's name at word i, with its schema when it has one,
// and returns the index after it.
func (l *fileLinter) tableRef(i int) (TableRef, int, bool) {
	if i >= len(l.ws) || !l.isName(i) {
		return TableRef{}, i, false
	}
	first := l.ws[i].text
	if !l.punct(i+1, ".") {
		return TableRef{Name: first}, i + 1, true
	}
	if i+2 >= len(l.ws) || !l.isName(i+2) {
		return TableRef{}, i, false
	}
	return TableRef{Schema: first, Name: l.ws[i+2].text}, i + 3, true
}

// A rebuildOf is one table's rebuild in a file: tmp, made by statement start,
// takes the place of table.
type rebuildOf struct {
	tmp, table TableRef
	start      int
}

// foldRef is a SQLite table's name as it compares: without main, in lower case.
func foldRef(ref TableRef) string {
	if strings.EqualFold(ref.Schema, "main") {
		ref.Schema = ""
	}
	return strings.ToLower(ref.String())
}

// fileRebuilds is f's rebuilds, by their statements, so that they survive a hand
// edit: a CREATE TABLE of tmp with an ALTER TABLE tmp RENAME TO t, where tmp is
// ddl.Rebuild(t) or the file has DROP TABLE t.
func fileRebuilds(d velox.Dialect, f File) []rebuildOf {
	created, dropped := map[string]int{}, map[string]bool{}
	for i, q := range f.Statements {
		l := &fileLinter{d: d, ws: lex(d, q)}
		if ref, ok := l.createdTable(); ok {
			created[foldRef(ref)] = i
		}
		for _, ref := range l.droppedTables() {
			dropped[foldRef(ref)] = true
		}
	}
	var out []rebuildOf
	for _, q := range f.Statements {
		l := &fileLinter{d: d, ws: lex(d, q)}
		tmp, to, ok := l.renamedTo()
		if !ok {
			continue
		}
		t := TableRef{Schema: tmp.Schema, Name: to.Name}
		start, made := created[foldRef(tmp)]
		if made && (tmp.Name == ddl.Rebuild(to.Name) || dropped[foldRef(t)]) {
			out = append(out, rebuildOf{tmp: tmp, table: t, start: start})
		}
	}
	return out
}

// createdTable reads CREATE TABLE t.
func (l *fileLinter) createdTable() (TableRef, bool) {
	if l.upper(0) != "CREATE" || l.upper(1) != "TABLE" {
		return TableRef{}, false
	}
	ref, _, ok := l.tableRef(l.skip(2, "IF", "NOT", "EXISTS"))
	return ref, ok
}

// droppedTables reads DROP TABLE t[, …].
func (l *fileLinter) droppedTables() []TableRef {
	if l.upper(0) != "DROP" || l.upper(1) != "TABLE" {
		return nil
	}
	var out []TableRef
	for i := l.skip(2, "IF", "EXISTS"); ; {
		ref, j, ok := l.tableRef(i)
		if !ok {
			return out
		}
		out = append(out, ref)
		if !l.punct(j, ",") {
			return out
		}
		i = j + 1
	}
}

// renamedTo reads ALTER TABLE tmp RENAME TO t, t with no schema.
func (l *fileLinter) renamedTo() (tmp, to TableRef, ok bool) {
	if l.upper(0) != "ALTER" || l.upper(1) != "TABLE" {
		return TableRef{}, TableRef{}, false
	}
	tmp, i, ok := l.tableRef(2)
	if !ok || l.upper(i) != "RENAME" || l.upper(i+1) != "TO" {
		return TableRef{}, TableRef{}, false
	}
	to, _, ok = l.tableRef(i + 2)
	return tmp, to, ok && to.Schema == ""
}

// writes reports whether one of words is a word of [i, end), outside
// parentheses.
func (l *fileLinter) writes(i, end int, words []string) bool {
	depth := 0
	for ; i < end; i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && slices.Contains(words, l.upper(i)):
			return true
		}
	}
	return false
}
