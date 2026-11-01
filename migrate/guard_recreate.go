package migrate

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox"
)

// checkRecreates refuses f's statements from index from on when one replaces
// what the database holds of a column or constraint and does not write what the
// model cannot hold of it: a MySQL MODIFY, CHANGE or CONVERT TO, a PostgreSQL
// ALTER COLUMN TYPE, and a constraint dropped and added again here or in a later
// pending file, as a retype of its column does. It runs before the file does: no
// statement is undone on MySQL, and what is refused has not begun on PostgreSQL.
// Each statement's names are followed back to what the database holds now. A
// statement it cannot read on a table with such an attribute is refused as well.
func (s *session) checkRecreates(ctx context.Context, f File, from int) error {
	w := &recreates{s: s, ctx: ctx, f: f, file: f, cur: s.home(), tables: map[string]*tableState{}}
	for _, q := range f.Statements[from:] {
		if err := w.statement(&fileLinter{d: s.d, ws: lex(s.d, q)}); err != nil {
			return err
		}
	}
	w.quiet = true
	for _, later := range s.later {
		if !w.anyDropped() {
			break
		}
		w.file = later
		for _, q := range later.Statements {
			if err := w.statement(&fileLinter{d: s.d, ws: lex(s.d, q)}); err != nil {
				return err
			}
		}
	}
	if len(w.lines) == 0 {
		return nil
	}
	return lossError(f, w.object, "a statement of it would lose what the schema model cannot hold", w.lines, w.targets,
		"to keep one, write it into the statement in the file")
}

// home is the default database or schema Up runs in.
func (s *session) home() string {
	if s.d == velox.MySQL {
		return s.db
	}
	return s.schema
}

// A recreates walks a file's statements, keeping what each name is now.
type recreates struct {
	s              *session
	ctx            context.Context
	f, file        File // f is checked; file is the one read now, f or a later one
	cur            string
	tables         map[string]*tableState
	quiet          bool // past f: only a constraint added again counts
	read           bool // every table's notes were lost: once is enough
	lines, targets []string
	object         string
}

// A tableState is a table of the walk: where it is in the database, its notes
// with keeps, and its columns' names in the database by their names now, ""
// for a column the file made.
type tableState struct {
	ref     TableRef
	made    bool // the file made it, so it holds nothing yet
	read    bool
	def     Table
	notes   []note
	cols    map[string]string
	dropped []droppedKey
}

// A droppedKey is a constraint with a keep the file dropped.
type droppedKey struct {
	name string
	cols []string // of a foreign key
	note note
}

func (w *recreates) anyDropped() bool {
	for _, st := range w.tables {
		if len(st.dropped) > 0 {
			return true
		}
	}
	return false
}

// key is a column's or constraint's name as it compares.
func (w *recreates) key(name string) string {
	if w.s.d == velox.MySQL {
		return strings.ToLower(name)
	}
	return name
}

// word is the i-th word as a name: PostgreSQL folds it unless quoted.
func (w *recreates) word(l *fileLinter, i int) string {
	if w.s.d == velox.Postgres && !l.ws[i].quoted {
		return strings.ToLower(l.ws[i].text)
	}
	return l.ws[i].text
}

// tableAt reads a table's name at i, against the walk's default database.
func (w *recreates) tableAt(l *fileLinter, i int) (TableRef, int, bool) {
	raw, j, ok := l.tableRef(i)
	if !ok || l.punct(j, ".") {
		return TableRef{}, i, false
	}
	ref := TableRef{Name: w.word(l, j-1)}
	if raw.Schema != "" {
		ref.Schema = w.word(l, i)
	}
	return w.norm(ref), j, true
}

// norm is ref with its database written only when it is not Up's.
func (w *recreates) norm(ref TableRef) TableRef {
	ref.Schema = cmp.Or(ref.Schema, w.cur)
	if ref.Schema == w.s.home() || w.s.d == velox.MySQL && strings.EqualFold(ref.Schema, w.s.home()) {
		ref.Schema = ""
	}
	return ref
}

// columnAt reads a column's name at i, the last part of a qualified one.
func (w *recreates) columnAt(l *fileLinter, i int) (string, int, bool) {
	for i < len(l.ws) && l.isName(i) {
		if !l.punct(i+1, ".") {
			return w.word(l, i), i + 1, true
		}
		i += 2
	}
	return "", i, false
}

// state is the walk's table named ref.
func (w *recreates) state(ref TableRef) *tableState {
	k := strings.ToLower(ref.String())
	if w.s.d != velox.MySQL {
		k = ref.String()
	}
	st := w.tables[k]
	if st == nil {
		st = &tableState{ref: ref, cols: map[string]string{}}
		w.tables[k] = st
	}
	return st
}

// put makes name the table st, or a new one when st is nil.
func (w *recreates) put(ref TableRef, st *tableState) {
	k := strings.ToLower(ref.String())
	if w.s.d != velox.MySQL {
		k = ref.String()
	}
	if st == nil {
		st = &tableState{ref: ref, made: true, cols: map[string]string{}}
	}
	w.tables[k] = st
}

// load reads st's notes that have a keep, once.
func (w *recreates) load(st *tableState) error {
	if st.read || st.made {
		return nil
	}
	st.read = true
	schema, notes, err := w.inspect([]TableRef{st.ref})
	if err != nil {
		return fmt.Errorf("migrate: %s: inspecting %s: %w", w.f.Path, st.ref, err)
	}
	if len(schema.Tables) > 0 {
		st.def = schema.Tables[0]
	}
	st.notes = slices.DeleteFunc(notes, func(n note) bool { return len(n.keeps) == 0 })
	return nil
}

func (w *recreates) inspect(refs []TableRef) (*Schema, []note, error) {
	if w.s.d == velox.MySQL {
		return inspectMySQL(w.ctx, w.s.conn, refs)
	}
	return inspectPostgres(w.ctx, w.s.conn, refs, nil)
}

// lost records a line, once, with the target that accepts it, unless a directive
// already does.
func (w *recreates) lost(ref TableRef, objects []string, target, line string) {
	for _, o := range append(objects, target) {
		if lossAllowed(w.f, o) || lossAllowed(w.file, o) {
			return
		}
	}
	w.object = cmp.Or(w.object, ref.String())
	if !slices.Contains(w.lines, line) {
		w.lines = append(w.lines, line)
	}
	if !slices.Contains(w.targets, target) {
		w.targets = append(w.targets, target)
	}
}

// statement walks one statement.
func (w *recreates) statement(l *fileLinter) error {
	switch lead := l.upper(0); lead {
	case "USE":
		if w.s.d == velox.MySQL && len(l.ws) > 1 {
			w.cur = l.ws[1].text
		}
	case "RENAME":
		if l.upper(1) != "TABLE" {
			return nil
		}
		for i := 2; ; {
			a, j, ok := w.tableAt(l, i)
			if !ok || l.upper(j) != "TO" {
				return w.opaque(l)
			}
			b, k, ok := w.tableAt(l, j+1)
			if !ok {
				return w.opaque(l)
			}
			w.rename(a, b)
			if !l.punct(k, ",") {
				return nil
			}
			i = k + 1
		}
	case "CREATE":
		i := 1
		for slices.Contains([]string{"TEMPORARY", "TEMP", "UNLOGGED", "GLOBAL", "LOCAL"}, l.upper(i)) {
			i++
		}
		if l.upper(i) == "TABLE" && l.upper(i+1) != "IF" {
			if ref, _, ok := w.tableAt(l, i+1); ok {
				w.put(ref, nil)
			}
		}
	case "DROP":
		if l.upper(1) != "TABLE" {
			return nil
		}
		for i := l.skip(2, "IF", "EXISTS"); ; {
			ref, j, ok := w.tableAt(l, i)
			if !ok {
				return nil
			}
			w.put(ref, nil)
			if !l.punct(j, ",") {
				return nil
			}
			i = j + 1
		}
	case "ALTER":
		if l.upper(1) == "TABLE" {
			return w.alter(l)
		}
	case "EXECUTE", "CALL", "DO":
		if lead != "DO" || w.s.d == velox.Postgres {
			return w.opaque(l)
		}
	case "SET":
		if w.s.d == velox.Postgres && strings.EqualFold(l.wordAt(l.skip(l.skip(1, "LOCAL"), "SESSION")), "search_path") {
			return w.opaque(l)
		}
	}
	return nil
}

// wordAt is the i-th word's text.
func (l *fileLinter) wordAt(i int) string {
	if i < 0 || i >= len(l.ws) {
		return ""
	}
	return l.ws[i].text
}

// rename moves table a to b.
func (w *recreates) rename(a, b TableRef) {
	st := w.state(a)
	w.put(a, nil)
	w.put(b, st)
}

// opaque refuses a statement the walk cannot read, on a database with any
// attribute it could lose: it may be anything.
func (w *recreates) opaque(l *fileLinter) error {
	if w.quiet || w.read {
		return nil
	}
	w.read = true
	_, notes, err := w.inspect(nil)
	if err != nil {
		return fmt.Errorf("migrate: %s: inspecting the database: %w", w.f.Path, err)
	}
	for _, n := range notes {
		w.unreadable(l, n, n.table)
	}
	return nil
}

// unreadable records that a statement that cannot be read may lose what n has.
func (w *recreates) unreadable(l *fileLinter, n note, ref TableRef) {
	for _, k := range n.keeps {
		w.lost(ref, nil, n.object, n.object+": "+k.what+", which a statement it cannot read may lose: "+l.lead())
	}
}

// lead is the first words of the statement, to name it.
func (l *fileLinter) lead() string {
	var out []string
	for i := 0; i < len(l.ws) && i < 6; i++ {
		out = append(out, l.ws[i].text)
	}
	return strings.Join(out, " ")
}

// alter walks ALTER TABLE's actions. A MySQL action reads its names as they
// stood when the statement began, as MySQL does, so that CHANGE a b, CHANGE b a
// swaps.
func (w *recreates) alter(l *fileLinter) error {
	i := l.skip(2, "IF", "EXISTS")
	if w.s.d == velox.Postgres {
		i = l.skip(i, "ONLY")
	}
	ref, i, ok := w.tableAt(l, i)
	if !ok {
		return w.opaque(l)
	}
	if l.punct(i, "*") {
		i++
	}
	st := w.state(ref)
	view := maps.Clone(st.cols)
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
		var err error
		if w.s.d == velox.MySQL {
			st, ref, err = w.mysqlAction(l, st, ref, view, i, end)
		} else {
			st, ref, err = w.postgresAction(l, st, ref, view, i, end)
		}
		if err != nil {
			return err
		}
		if w.s.d == velox.Postgres {
			view = maps.Clone(st.cols)
		}
		i = end + 1
	}
	return nil
}

// origin is what the column is in the database, by view.
func (w *recreates) origin(view map[string]string, name string) string {
	if o, ok := view[w.key(name)]; ok {
		return o
	}
	return w.key(name)
}

// notePart is the name a note of st is on.
func (st *tableState) notePart(n note) string {
	return strings.TrimPrefix(n.object, st.ref.String()+".")
}

// replaces checks a statement that replaces the column, in the database origin,
// and that writes [i, end) of it, which ref.name is afterwards. A type that holds
// no text has no collation to keep.
func (w *recreates) replaces(l *fileLinter, st *tableState, ref TableRef, origin, name string, i, end int) error {
	if w.quiet || origin == "" {
		return nil
	}
	if err := w.load(st); err != nil {
		return err
	}
	for _, n := range st.notes {
		if n.constraint || w.key(st.notePart(n)) != origin {
			continue
		}
		if strings.EqualFold(name, st.notePart(n)) {
			name = st.notePart(n)
		}
		target := colTarget(ref, name)
		for _, k := range n.keeps {
			if l.writes(i, end, k.words) || slices.Contains(k.words, "COLLATE") && noCollation(l.upper(i)) {
				continue
			}
			w.lost(ref, []string{n.object}, target, target+": "+k.what)
		}
	}
	return nil
}

// noCollation reports whether a column of the type named by word has none.
func noCollation(word string) bool {
	return slices.Contains([]string{
		"SMALLINT", "INT", "INTEGER", "BIGINT", "INT2", "INT4", "INT8", "TINYINT", "MEDIUMINT",
		"NUMERIC", "DECIMAL", "REAL", "FLOAT", "FLOAT4", "FLOAT8", "DOUBLE", "BOOLEAN", "BOOL", "BIT",
		"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "DATETIME", "YEAR", "UUID", "JSON", "JSONB", "BYTEA", "BLOB", "BINARY", "VARBINARY",
	}, word)
}

// unknown refuses an action it cannot read on st.
func (w *recreates) unknown(l *fileLinter, st *tableState, ref TableRef) error {
	if w.quiet {
		return nil
	}
	if err := w.load(st); err != nil {
		return err
	}
	for _, n := range st.notes {
		w.unreadable(l, n, ref)
	}
	return nil
}

// droppedAt records that a statement dropped the constraint named name.
func (w *recreates) droppedAt(st *tableState, name string) error {
	if w.quiet {
		return nil
	}
	if err := w.load(st); err != nil {
		return err
	}
	for _, n := range st.notes {
		if !n.constraint || w.key(st.notePart(n)) != w.key(name) {
			continue
		}
		d := droppedKey{name: name, note: n}
		for _, fk := range st.def.ForeignKeys {
			if w.key(fk.Name) == w.key(name) {
				d.cols = fk.Columns
			}
		}
		st.dropped = append(st.dropped, d)
	}
	return nil
}

// added checks an ADD of a constraint at i, against the ones the file dropped.
func (w *recreates) added(l *fileLinter, st *tableState, ref TableRef, i, end int) {
	if len(st.dropped) == 0 {
		return
	}
	j, name := i, ""
	if l.upper(j) == "CONSTRAINT" {
		j++
		if !slices.Contains([]string{"FOREIGN", "UNIQUE", "CHECK", "PRIMARY", "EXCLUDE"}, l.upper(j)) && j < end {
			name = w.word(l, j)
			j++
		}
	}
	var cols []string
	if l.upper(j) == "FOREIGN" && l.upper(j+1) == "KEY" {
		k := j + 2
		if !l.punct(k, "(") && k < end {
			name = cmp.Or(name, w.word(l, k))
			k++
		}
		if l.punct(k, "(") {
			for m := k + 1; m < end && !l.punct(m, ")"); m++ {
				if !l.punct(m, ",") {
					cols = append(cols, w.key(w.word(l, m)))
				}
			}
		}
	}
	st.dropped = slices.DeleteFunc(st.dropped, func(d droppedKey) bool {
		same := name != "" && w.key(name) == w.key(d.name)
		if !same && cols != nil && len(d.cols) > 0 {
			same = slices.EqualFunc(cols, d.cols, func(a, b string) bool { return a == w.key(b) })
		}
		if !same {
			return false
		}
		for _, k := range d.note.keeps {
			if !l.writes(i, end, k.words) {
				w.lost(ref, nil, d.note.object, d.note.object+": "+k.what+", and the statement that adds it again does not write it")
			}
		}
		return true
	})
}

// mysqlAction walks the action of [i, end), and returns the table as it is after.
func (w *recreates) mysqlAction(l *fileLinter, st *tableState, ref TableRef, view map[string]string, i, end int) (*tableState, TableRef, error) {
	switch act := l.upper(i); act {
	case "MODIFY", "CHANGE":
		col, k, ok := w.columnAt(l, l.skip(i+1, "COLUMN"))
		if !ok {
			return st, ref, w.unknown(l, st, ref)
		}
		name, origin := col, w.origin(view, col)
		if act == "CHANGE" {
			if k >= end || !l.isName(k) {
				return st, ref, w.unknown(l, st, ref)
			}
			name = w.word(l, k)
			k++
			st.cols[w.key(col)] = ""
			st.cols[w.key(name)] = origin
		}
		return st, ref, w.replaces(l, st, ref, origin, name, k, end)
	case "RENAME":
		switch l.upper(i + 1) {
		case "COLUMN":
			a, j, ok := w.columnAt(l, i+2)
			if !ok || l.upper(j) != "TO" {
				return st, ref, w.unknown(l, st, ref)
			}
			b, _, ok := w.columnAt(l, j+1)
			if !ok {
				return st, ref, w.unknown(l, st, ref)
			}
			origin := w.origin(view, a)
			st.cols[w.key(a)] = ""
			st.cols[w.key(b)] = origin
		case "INDEX", "KEY":
		default:
			j := i + 1
			if l.upper(j) == "TO" || l.upper(j) == "AS" {
				j++
			}
			b, _, ok := w.tableAt(l, j)
			if !ok {
				return st, ref, w.unknown(l, st, ref)
			}
			w.rename(ref, b)
			return w.state(b), b, nil
		}
	case "ADD":
		switch l.upper(i + 1) {
		case "INDEX", "KEY", "FULLTEXT", "SPATIAL", "PARTITION":
		case "CONSTRAINT", "UNIQUE", "PRIMARY", "FOREIGN", "CHECK":
			w.added(l, st, ref, i+1, end)
		default:
			j := l.skip(i+1, "COLUMN")
			if l.punct(j, "(") {
				depth := 0
				for k := j; k < end; k++ {
					switch {
					case l.punct(k, "("):
						depth++
					case l.punct(k, ")"):
						depth--
					case depth == 1 && (k == j+1 || l.punct(k-1, ",")) && l.isName(k):
						st.cols[w.key(w.word(l, k))] = ""
					}
				}
			} else if col, _, ok := w.columnAt(l, j); ok {
				st.cols[w.key(col)] = ""
			}
		}
	case "DROP":
		switch l.upper(i + 1) {
		case "INDEX", "KEY", "PRIMARY", "PARTITION":
		case "FOREIGN":
			if l.upper(i+2) == "KEY" && i+3 < end {
				return st, ref, w.droppedAt(st, w.word(l, i+3))
			}
		case "CONSTRAINT", "CHECK":
			if i+2 < end {
				return st, ref, w.droppedAt(st, w.word(l, i+2))
			}
		default:
			if col, _, ok := w.columnAt(l, l.skip(i+1, "COLUMN")); ok {
				st.cols[w.key(col)] = ""
			}
		}
	case "CONVERT":
		if w.quiet {
			return st, ref, nil
		}
		if err := w.load(st); err != nil {
			return st, ref, err
		}
		for _, n := range st.notes {
			origin := w.key(st.notePart(n))
			if gone, ok := view[origin]; n.constraint || ok && gone == "" {
				continue
			}
			target := colTarget(ref, st.notePart(n))
			for _, k := range n.keeps {
				if slices.Contains(k.words, "COLLATE") {
					w.lost(ref, []string{n.object}, target, target+": "+k.what)
				}
			}
		}
	case "ALTER", "ALGORITHM", "LOCK", "COMMENT", "ENGINE", "AUTO_INCREMENT", "DEFAULT", "CHARACTER", "CHARSET", "COLLATE",
		"FORCE", "ORDER", "ROW_FORMAT", "KEY_BLOCK_SIZE", "STATS_PERSISTENT", "STATS_AUTO_RECALC", "STATS_SAMPLE_PAGES",
		"PACK_KEYS", "CHECKSUM", "MIN_ROWS", "MAX_ROWS", "AVG_ROW_LENGTH", "COMPRESSION", "ENCRYPTION", "ENABLE", "DISABLE",
		"DELAY_KEY_WRITE", "INSERT_METHOD", "TABLESPACE", "VALIDATION", "WITH", "WITHOUT":
	default:
		return st, ref, w.unknown(l, st, ref)
	}
	return st, ref, nil
}

// postgresAction walks the action of [i, end), and returns the table as it is
// after.
func (w *recreates) postgresAction(l *fileLinter, st *tableState, ref TableRef, view map[string]string, i, end int) (*tableState, TableRef, error) {
	switch l.upper(i) {
	case "ALTER":
		col, k, ok := w.columnAt(l, l.skip(i+1, "COLUMN"))
		if !ok {
			return st, ref, w.unknown(l, st, ref)
		}
		if l.upper(k) == "SET" && l.upper(k+1) == "DATA" {
			k += 2
		}
		if l.upper(k) != "TYPE" {
			return st, ref, nil
		}
		to := end
		for m, depth := k+1, 0; m < end; m++ { // USING's COLLATE is not the column's
			if l.punct(m, "(") {
				depth++
			} else if l.punct(m, ")") {
				depth--
			} else if depth == 0 && l.upper(m) == "USING" {
				to = m
				break
			}
		}
		return st, ref, w.replaces(l, st, ref, w.origin(view, col), col, k+1, to)
	case "RENAME":
		switch l.upper(i + 1) {
		case "CONSTRAINT":
		case "TO":
			if i+2 >= end {
				return st, ref, w.unknown(l, st, ref)
			}
			b := TableRef{Schema: ref.Schema, Name: w.word(l, i+2)}
			w.rename(ref, b)
			return w.state(b), b, nil
		default:
			a, j, ok := w.columnAt(l, l.skip(i+1, "COLUMN"))
			if !ok || l.upper(j) != "TO" {
				return st, ref, w.unknown(l, st, ref)
			}
			b, _, ok := w.columnAt(l, j+1)
			if !ok {
				return st, ref, w.unknown(l, st, ref)
			}
			origin := w.origin(view, a)
			st.cols[w.key(a)] = ""
			st.cols[w.key(b)] = origin
		}
	case "SET":
		if l.upper(i+1) == "SCHEMA" && i+2 < end {
			b := w.norm(TableRef{Schema: w.word(l, i+2), Name: ref.Name})
			w.rename(ref, b)
			return w.state(b), b, nil
		}
	case "ADD":
		switch l.upper(i + 1) {
		case "CONSTRAINT", "UNIQUE", "PRIMARY", "FOREIGN", "CHECK", "EXCLUDE":
			w.added(l, st, ref, i+1, end)
		default:
			j := l.skip(l.skip(i+1, "COLUMN"), "IF", "NOT", "EXISTS")
			if col, _, ok := w.columnAt(l, j); ok {
				st.cols[w.key(col)] = ""
			}
		}
	case "DROP":
		if l.upper(i+1) == "CONSTRAINT" {
			j := l.skip(i+2, "IF", "EXISTS")
			if j < end {
				return st, ref, w.droppedAt(st, w.word(l, j))
			}
			return st, ref, nil
		}
		j := l.skip(l.skip(i+1, "COLUMN"), "IF", "EXISTS")
		if col, _, ok := w.columnAt(l, j); ok {
			st.cols[w.key(col)] = ""
		}
	case "VALIDATE", "RESET", "OWNER", "ENABLE", "DISABLE", "CLUSTER", "INHERIT", "NO", "OF", "NOT", "REPLICA", "FORCE", "ATTACH", "DETACH":
	default:
		return st, ref, w.unknown(l, st, ref)
	}
	return st, ref, nil
}
