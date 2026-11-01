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

// What a file run by Up would destroy and the schema model cannot hold is
// only visible at apply time: a plan is made with no database. A generated
// SQLite rebuild writes only what the model holds, and a MySQL MODIFY COLUMN
// replaces the column's whole definition. See decision 0026.

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

// lossAllowed reports whether f's directives accept the loss of object.
func lossAllowed(f File, object string) bool {
	return slices.ContainsFunc(f.Directives.Allow, func(a Allow) bool {
		return a.Code == lossCode && (a.Target == "" || a.Target == object) ||
			a.Code == "drop-column" && a.Target == object // the column goes on purpose, and with it what it has
	})
}

// A rebuildGuard watches the generated rebuilds of one file run on SQLite, in
// its transaction. A nil one, of a file with none, watches nothing.
type rebuildGuard struct {
	s      *session
	f      File
	starts map[int]TableRef // the statement that makes a rebuild's new table: the table
	seen   []rebuildSeen
}

// rebuildSeen is a rebuilt table's notes as the rebuild found them.
type rebuildSeen struct {
	table TableRef
	notes []note
}

// guardRebuilds is the guard of f, on SQLite when f has a rebuild.
func (s *session) guardRebuilds(f File) *rebuildGuard {
	if s.d != velox.SQLite {
		return nil
	}
	rebuilds := fileRebuilds(s.d, f)
	if len(rebuilds) == 0 {
		return nil
	}
	g := &rebuildGuard{s: s, f: f, starts: map[int]TableRef{}}
	for _, r := range rebuilds {
		g.starts[r.start] = r.table
	}
	return g
}

// before runs ahead of statement i. At a rebuild's first statement it refuses
// a view or another table's trigger that names the table, whose ALTER TABLE
// RENAME would fail on them, and reads the table's notes for after.
func (g *rebuildGuard) before(ctx context.Context, i int) error {
	if g == nil {
		return nil
	}
	t, ok := g.starts[i]
	if !ok {
		return nil
	}
	s := g.s
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
	_, notes, err := inspectSQLite(ctx, s.conn, []TableRef{t})
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", t, err)
	}
	g.seen = append(g.seen, rebuildSeen{t, notes})
	return nil
}

// after runs behind the file's last statement: a note of a rebuilt table the
// table no longer has is a loss, unless a directive accepts it. What the file
// made again, or kept in its new table, is no loss. Its refusal rolls the file
// back.
func (g *rebuildGuard) after(ctx context.Context) error {
	if g == nil {
		return nil
	}
	for _, seen := range g.seen {
		if len(seen.notes) == 0 {
			continue
		}
		_, now, err := inspectSQLite(ctx, g.s.conn, []TableRef{seen.table})
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", seen.table, err)
		}
		var lines, targets []string
		for _, n := range seen.notes {
			kept := slices.ContainsFunc(now, func(m note) bool { return m.object == n.object && m.message == n.message })
			if kept || lossAllowed(g.f, n.object) {
				continue
			}
			lines = append(lines, n.object+": "+n.message)
			if !slices.Contains(targets, n.object) {
				targets = append(targets, n.object)
			}
		}
		if len(lines) > 0 {
			return lossError(g.f, seen.table.String(), "its rebuild would lose what the schema model cannot hold", lines, targets,
				"to keep one, write it into the file after the rebuild (a trigger, which a file cannot hold, is made by hand after applying, under the directive)")
		}
	}
	return nil
}

// dependents is a line for each view, and each trigger of another table, of
// t's database that names t in its text.
func (s *session) dependents(ctx context.Context, t TableRef) ([]string, error) {
	db := "main"
	if t.Schema != "" {
		db = t.Schema
	}
	q := `SELECT type, name, tbl_name, coalesce(sql, '') FROM ` + ddl.Ident(velox.SQLite, db) + `.sqlite_schema WHERE type IN ('view', 'trigger') ORDER BY type DESC, name`
	var lines []string
	err := eachRow(ctx, s.conn, q, nil, func(rows *sql.Rows) error {
		var typ, name, table, text string
		if err := rows.Scan(&typ, &name, &table, &text); err != nil {
			return err
		}
		if typ == "trigger" && strings.EqualFold(table, t.Name) {
			return nil // it goes with the table: a note
		}
		// A column of another table named as t reads as a use, which errs on
		// the safe side: the file drops the view and makes it again.
		if slices.ContainsFunc(lex(velox.SQLite, text), func(w word) bool { return strings.EqualFold(w.text, t.Name) }) {
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

// checkModify refuses f's statements from index from on when a MODIFY or
// CHANGE COLUMN would lose what the model cannot hold of a column as it stands
// now: an attribute its statement does not write, or a directive accepts. It
// runs before the file does, as MySQL has no transaction to undo DDL with; the
// renames of the statements before each are followed back to the names the
// database holds.
func (s *session) checkModify(ctx context.Context, f File, from int) error {
	tables := map[TableRef]TableRef{}           // a renamed table's name now, by its name in the file
	columns := map[TableRef]map[string]string{} // the same for columns, by the table's name now
	noted := map[TableRef][]note{}              // by the table's name now
	read := map[TableRef]bool{}
	var lines, targets []string
	var object string
	now := func(ref TableRef) TableRef {
		if o, ok := tables[ref]; ok {
			return o
		}
		return ref
	}
	for _, q := range f.Statements[from:] {
		l := &fileLinter{d: s.d, ws: lex(s.d, q)}
		if l.upper(0) == "RENAME" && l.upper(1) == "TABLE" {
			for i := 2; ; {
				a, j, ok := l.tableRef(i)
				if !ok || l.upper(j) != "TO" {
					break
				}
				b, k, ok := l.tableRef(j + 1)
				if !ok {
					break
				}
				tables[b] = now(a)
				if !l.punct(k, ",") {
					break
				}
				i = k + 1
			}
			continue
		}
		if l.upper(0) != "ALTER" || l.upper(1) != "TABLE" {
			continue
		}
		ref, i, ok := l.tableRef(2)
		if !ok {
			continue
		}
		for i < len(l.ws) {
			end, depth := i, 0
			for ; end < len(l.ws); end++ {
				if l.punct(end, "(") {
					depth++
				} else if l.punct(end, ")") {
					depth--
				} else if depth == 0 && l.punct(end, ",") {
					break
				}
			}
			act := l.upper(i)
			switch {
			case act == "RENAME" && l.upper(i+1) == "COLUMN":
				a, j, ok := l.tableRef(i + 2)
				if ok && l.upper(j) == "TO" {
					if b, _, ok := l.tableRef(j + 1); ok {
						o := now(ref)
						if columns[o] == nil {
							columns[o] = map[string]string{}
						}
						was := a.Name
						if w, ok := columns[o][a.Name]; ok {
							was = w
						}
						columns[o][b.Name] = was
					}
				}
			case act == "RENAME" && l.upper(i+1) != "INDEX" && l.upper(i+1) != "KEY":
				j := i + 1
				if l.upper(j) == "TO" || l.upper(j) == "AS" {
					j++
				}
				if b, _, ok := l.tableRef(j); ok {
					tables[b] = now(ref)
				}
			case act == "MODIFY" || act == "CHANGE":
				j := l.skip(i+1, "COLUMN")
				col, k, ok := l.tableRef(j)
				if !ok || col.Schema != "" {
					break
				}
				o := now(ref)
				if !read[o] {
					read[o] = true
					_, ns, err := inspectMySQL(ctx, s.conn, []TableRef{o})
					if err != nil {
						return fmt.Errorf("migrate: %s: inspecting %s: %w", f.Path, o, err)
					}
					noted[o] = ns
				}
				was := col.Name
				if w, ok := columns[o][col.Name]; ok {
					was = w
				}
				for _, n := range noted[o] {
					if n.object != colTarget(o, was) || lossAllowed(f, colTarget(ref, col.Name)) || lossAllowed(f, n.object) {
						continue
					}
					for _, keep := range n.keeps {
						if l.writes(k, end, keep.words) {
							continue
						}
						object = cmp.Or(object, ref.String())
						target := colTarget(ref, col.Name)
						lines = append(lines, target+": "+keep.what)
						if !slices.Contains(targets, target) {
							targets = append(targets, target)
						}
					}
				}
			}
			i = end + 1
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return lossError(f, object, "its MODIFY COLUMN would lose what the schema model cannot hold", lines, targets,
		"to keep one, write it into the statement in the file")
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
