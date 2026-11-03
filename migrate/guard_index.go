package migrate

import (
	"cmp"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// modelShape is ix as a plan's statement writes it.
func modelShape(ix Index) indexShape {
	s := indexShape{unique: ix.Unique, where: ix.Where}
	for _, c := range ix.Columns {
		s.keys = append(s.keys, indexKey{name: c})
	}
	return s
}

// An index dropped and made again under one name is the same case as a column
// replaced: what the statement that makes it does not write is gone. A shape is an
// index as a statement writes it or the database holds it.
type indexShape struct {
	unique    bool
	notNulls  bool // PostgreSQL's NULLS NOT DISTINCT
	invisible bool // MySQL
	method    string
	keys      []indexKey
	include   []string
	with      string
	space     string
	comment   string
	where     string
}

// An indexKey is one key of an index: its column, or the text of its expression, and
// the rest: an operator class, a collation, DESC, a prefix length.
type indexKey struct{ name, options string }

func (k indexKey) String() string { return strings.TrimSpace(k.name + " " + k.options) }

// shapeDiffers is a line for each thing the index was, of, that stated writes
// otherwise, by the name a velox:drops line gives it.
func shapeDiffers(d velox.Dialect, enums map[string]bool, was, stated indexShape) map[string]string {
	out := map[string]string{}
	differs := func(on bool, attr, had, wrote string) {
		if on {
			out[attr] = "the index has " + had + ", and the statement that makes it again writes " + wrote
		}
	}
	yes := func(b bool, what string) string {
		if b {
			return what
		}
		return "no " + what
	}
	differs(was.unique != stated.unique, "unique", yes(was.unique, "UNIQUE"), yes(stated.unique, "UNIQUE"))
	differs(was.notNulls != stated.notNulls, "nulls", yes(was.notNulls, "NULLS NOT DISTINCT"), yes(stated.notNulls, "NULLS NOT DISTINCT"))
	differs(was.invisible != stated.invisible, "invisible", yes(was.invisible, "INVISIBLE"), yes(stated.invisible, "INVISIBLE"))
	method := func(m string) string {
		if m == "" {
			return "the default method"
		}
		return "the method " + m
	}
	differs(was.method != stated.method, "method", method(was.method), method(stated.method))
	names := func(ks []indexKey) []string {
		out := make([]string, len(ks))
		for i, k := range ks {
			out[i] = k.name
		}
		return out
	}
	differs(!slices.Equal(names(was.keys), names(stated.keys)), "columns",
		"the keys ("+strings.Join(names(was.keys), ", ")+")", "("+strings.Join(names(stated.keys), ", ")+")")
	for _, k := range was.keys {
		i := slices.IndexFunc(stated.keys, func(s indexKey) bool { return s.name == k.name })
		if i >= 0 && stated.keys[i].options != k.options {
			differs(true, "key-options", "the key "+k.String(), stated.keys[i].String())
		}
	}
	list := func(l []string) string {
		if len(l) == 0 {
			return "no INCLUDE"
		}
		return "INCLUDE (" + strings.Join(l, ", ") + ")"
	}
	differs(!slices.Equal(was.include, stated.include), "include", list(was.include), list(stated.include))
	text := func(what, s string) string {
		if s == "" {
			return "no " + what
		}
		return what + " " + s
	}
	differs(was.with != stated.with, "with", text("WITH", was.with), text("WITH", stated.with))
	differs(was.space != stated.space, "tablespace", text("TABLESPACE", was.space), text("TABLESPACE", stated.space))
	differs(was.comment != stated.comment, "comment", text("COMMENT", was.comment), text("COMMENT", stated.comment))
	same := was.where == stated.where
	if !same && was.where != "" && stated.where != "" {
		same, _ = samePredicateIn(d, was.where, stated.where, enums)
	}
	differs(!same, "where", text("WHERE", was.where), text("WHERE", stated.where))
	return out
}

// plainWords is the words of [i, j) in lower case as the database folds them, a
// quoted name as it is, and the literals between them kept, one space between all.
func (l *fileLinter) plainWords(i, j int) string {
	var b strings.Builder
	put := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	for k := i; k < j && k < len(l.ws); k++ {
		w := l.ws[k]
		if w.quoted {
			put(w.text)
		} else {
			put(strings.ToLower(w.text))
		}
		if k+1 < j && k+1 < len(l.ws) {
			put(strings.TrimSpace(l.src[w.end:l.ws[k+1].start]))
		}
	}
	return b.String()
}

// indexKeyAt reads the key of words [i, j): the leading name when it is one, or a
// MySQL prefix's, else the whole as its name. ASC, and PostgreSQL's NULLS default for
// the order, are no options.
func (l *fileLinter) indexKeyAt(i, j int) indexKey {
	var k indexKey
	switch {
	case i >= j:
	case l.isName(i) && !l.punct(i+1, "("):
		k.name = l.ws[i].text
		if !l.ws[i].quoted || l.d == velox.MySQL {
			k.name = strings.ToLower(k.name)
		}
		k.options = l.plainWords(i+1, j)
	case l.d == velox.MySQL && l.isName(i) && l.punct(i+1, "(") && l.closes(i+1) > 0 && l.closes(i+1) < j:
		shut := l.closes(i + 1)
		k.name = strings.ToLower(l.ws[i].text)
		k.options = strings.TrimSpace(l.plainWords(i+1, shut+1) + " " + l.plainWords(shut+1, j))
	default:
		if l.d == velox.MySQL && l.punct(i, "(") && l.closes(i) == j-1 { // MySQL prints the expression without them
			i, j = i+1, j-1
		}
		end := j
		for end > i+1 && slices.Contains([]string{"ASC", "DESC", "NULLS", "FIRST", "LAST"}, l.upper(end-1)) {
			end--
		}
		k.name, k.options = l.plainWords(i, end), l.plainWords(end, j)
	}
	desc := strings.Contains(" "+k.options+" ", " desc ")
	k.options = strings.TrimSpace(strings.ReplaceAll(" "+k.options+" ", " asc ", " "))
	if desc {
		k.options = strings.TrimSpace(strings.ReplaceAll(" "+k.options+" ", " nulls first ", " "))
	} else {
		k.options = strings.TrimSpace(strings.ReplaceAll(" "+k.options+" ", " nulls last ", " "))
	}
	return k
}

// indexAt reads an index's definition, from its USING or column list at word i up to
// end, as a statement or pg_get_indexdef writes it.
func (l *fileLinter) indexAt(i, end int) (indexShape, bool) {
	var s indexShape
	if l.upper(i) == "USING" && i+1 < end {
		s.method = strings.ToLower(l.ws[i+1].text)
		i += 2
	}
	if !l.punct(i, "(") {
		return s, false
	}
	shut := l.closes(i)
	if shut < 0 || shut >= end {
		return s, false
	}
	for from, depth, k := i+1, 0, i+1; k <= shut; k++ {
		switch {
		case l.punct(k, "(") && k < shut:
			depth++
		case l.punct(k, ")") && k < shut:
			depth--
		case k == shut || depth == 0 && l.punct(k, ","):
			s.keys = append(s.keys, l.indexKeyAt(from, k))
			from = k + 1
		}
	}
	for i = shut + 1; i < end; {
		switch l.upper(i) {
		case "USING":
			s.method = strings.ToLower(l.wordAt(i + 1))
			i += 2
		case "NULLS":
			s.notNulls = l.upper(i+1) == "NOT"
			i = l.skip(l.skip(i+1, "NOT"), "DISTINCT")
		case "INCLUDE":
			shut := l.closes(i + 1)
			if shut < 0 || shut >= end {
				return s, false
			}
			for k := i + 2; k < shut; k++ {
				if !l.punct(k, ",") {
					s.include = append(s.include, l.plainWords(k, k+1))
				}
			}
			i = shut + 1
		case "WITH":
			shut := l.closes(i + 1)
			if shut < 0 || shut >= end {
				return s, false
			}
			s.with = strings.NewReplacer(" ", "", "'", "").Replace(strings.ToLower(l.raw(i+2, shut)))
			i = shut + 1
		case "TABLESPACE":
			s.space = l.wordAt(i + 1)
			i += 2
		case "WHERE":
			s.where = l.raw(i+1, end)
			i = end
		case "INVISIBLE":
			s.invisible = true
			i++
		case "COMMENT":
			next := i + 1
			for next < end && !l.isName(next) {
				next++
			}
			if text, ok := ddl.Unquote(l.d, l.rawAfter(i, next)); ok {
				s.comment = text
			}
			i = next
		default:
			i++
		}
	}
	if s.method == "btree" {
		s.method = ""
	}
	return s, true
}

// A droppedIndex is an index a statement of the walk dropped, as it was: of st on
// MySQL, where a table holds the names, in schema on PostgreSQL, of the table on.
type droppedIndex struct {
	name   string
	st     *tableState
	schema string
	on     TableRef // PostgreSQL: its table, as the database names it
	shape  indexShape
}

// index is the place in w.indexes of the dropped index named name, or -1.
func (w *recreates) index(st *tableState, schema, name string) int {
	return slices.IndexFunc(w.indexes, func(d droppedIndex) bool {
		if w.key(d.name) != w.key(name) {
			return false
		}
		if w.s.d == velox.MySQL {
			return d.st == st
		}
		return d.schema == schema
	})
}

// dropIndex walks a top-level DROP INDEX: on PostgreSQL a list, read by the server's
// own resolution, on MySQL one of a table.
func (w *recreates) dropIndex(l *fileLinter) error {
	if w.quiet {
		return nil
	}
	i := l.skip(l.skip(2, "CONCURRENTLY"), "IF", "EXISTS")
	if w.s.d == velox.MySQL {
		if i >= len(l.ws) || l.upper(i+1) != "ON" {
			return nil
		}
		ref, _, ok := w.tableAt(l, i+2)
		if !ok {
			return nil
		}
		return w.mysqlIndexDrop(w.state(ref), l.ws[i].text)
	}
	for {
		raw, j, ok := l.tableRef(i)
		if !ok {
			return nil
		}
		ref := TableRef{Name: w.word(l, j-1)}
		if raw.Schema != "" {
			ref.Schema = w.word(l, i)
		}
		if l.upper(2) == "CONCURRENTLY" {
			if err := w.partitionedDrop(ref); err != nil {
				return err
			}
		}
		if err := w.pgIndexDrop(ref); err != nil {
			return err
		}
		if !l.punct(j, ",") {
			return nil
		}
		i = j + 1
	}
}

// pgIndexDrop records the index ref as it stands. An invalid one, as a failed CREATE
// INDEX CONCURRENTLY leaves it, holds nothing.
func (w *recreates) pgIndexDrop(ref TableRef) error {
	name := ddl.Ident(velox.Postgres, ref.Name)
	if ref.Schema != "" {
		name = ddl.Ident(velox.Postgres, ref.Schema) + "." + name
	}
	var nsp, table, def string
	var valid, found bool
	err := eachRow(w.ctx, w.s.conn, `SELECT n.nspname, t.relname, i.indisvalid, pg_get_indexdef(i.indexrelid)
FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class t ON t.oid = i.indrelid
WHERE i.indexrelid = to_regclass($1)`, []any{name}, func(rows *sql.Rows) error {
		found = true
		return rows.Scan(&nsp, &table, &valid, &def)
	})
	if err != nil {
		return fmt.Errorf("migrate: %s: reading the index %s: %w", w.f.Path, ref, err)
	}
	if !found || !valid {
		return nil
	}
	schema := w.norm(TableRef{Schema: nsp, Name: ref.Name}).Schema
	on := w.norm(TableRef{Schema: nsp, Name: table})
	if meant, ok := w.meantOn(ref.Name, on); !ok {
		// PostgreSQL names an index in its schema: the plan's DROP INDEX
		// would drop another table's.
		other := colTarget(on, ref.Name)
		w.lost(on, nil, other, other+": the index is on "+on.String()+", and the plan drops "+colTarget(meant, ref.Name)+"; rename one of them")
		return nil
	}
	shape, ok := pgDefShape(def)
	if ok && w.index(nil, schema, ref.Name) < 0 {
		w.indexes = append(w.indexes, droppedIndex{name: ref.Name, schema: schema, on: on, shape: shape})
	}
	return nil
}

// meantOn reports whether the table on, where the database has the index name,
// is the one a drops line of the file names it on, as the line "t.x" a plan
// writes for a PostgreSQL DROP INDEX does. With no such line any table is.
// Otherwise it gives the table a line names.
func (w *recreates) meantOn(name string, on TableRef) (TableRef, bool) {
	var meant []TableRef
	for _, f := range []File{w.f, w.file} {
		for _, line := range f.Directives.Drops {
			parts := strings.Split(line, ".")
			if strings.Contains(line, ":") || len(parts) < 2 || len(parts) > 3 || parts[len(parts)-1] != name {
				continue
			}
			t := TableRef{Name: parts[len(parts)-2]}
			if len(parts) == 3 {
				t.Schema = parts[0]
			}
			meant = append(meant, w.norm(t))
		}
	}
	for _, t := range meant {
		if st := w.tables[t.String()]; t == on || st != nil && st.ref == on {
			return t, true
		}
	}
	if len(meant) == 0 {
		return TableRef{}, true
	}
	return meant[0], false
}

// pgDefShape reads what pg_get_indexdef prints.
func pgDefShape(def string) (indexShape, bool) {
	l := &fileLinter{d: velox.Postgres, src: def, ws: lex(velox.Postgres, def)}
	i := l.skip(1, "UNIQUE")
	unique := i > 1
	if l.upper(i) != "INDEX" {
		return indexShape{}, false
	}
	for i < len(l.ws) && l.upper(i) != "ON" {
		i++
	}
	_, j, ok := l.tableRef(l.skip(i+1, "ONLY"))
	if !ok {
		return indexShape{}, false
	}
	s, ok := l.indexAt(j, len(l.ws))
	s.unique = unique
	return s, ok
}

// mysqlShapes reads st's indexes, once, with what the model leaves out of them.
func (w *recreates) mysqlShapes(st *tableState) (map[string]indexShape, error) {
	if st.shapes != nil {
		return st.shapes, nil
	}
	st.shapes = map[string]indexShape{}
	if st.made {
		return st.shapes, nil
	}
	err := eachRow(w.ctx, w.s.conn, `SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME, SUB_PART, COALESCE(COLLATION, ''), INDEX_TYPE, IS_VISIBLE, COALESCE(EXPRESSION, ''), INDEX_COMMENT
FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX`, []any{cmp.Or(st.ref.Schema, w.s.home()), st.ref.Name}, func(rows *sql.Rows) error {
		var (
			name, collation, kind, visible, expr, comment string
			nonUnique                                     int
			col, part                                     sql.NullString
		)
		if err := rows.Scan(&name, &nonUnique, &col, &part, &collation, &kind, &visible, &expr, &comment); err != nil {
			return err
		}
		s := st.shapes[strings.ToLower(name)]
		s.unique, s.comment = nonUnique == 0, comment
		s.invisible = s.invisible || visible == "NO"
		if kind != "BTREE" {
			s.method = strings.ToLower(kind)
		}
		var k indexKey
		if col.Valid {
			k.name = strings.ToLower(col.String)
		} else {
			e := &fileLinter{d: velox.MySQL, src: expr, ws: lex(velox.MySQL, expr)}
			k.name = e.plainWords(0, len(e.ws))
		}
		var options []string
		if part.Valid && kind != "SPATIAL" {
			options = append(options, "( "+part.String+" )")
		}
		if collation == "D" {
			options = append(options, "desc")
		}
		k.options = strings.Join(options, " ")
		s.keys = append(s.keys, k)
		st.shapes[strings.ToLower(name)] = s
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("migrate: %s: reading the indexes of %s: %w", w.f.Path, st.ref, err)
	}
	return st.shapes, nil
}

// mysqlIndexDrop records the index of st named name as it stands.
func (w *recreates) mysqlIndexDrop(st *tableState, name string) error {
	if w.quiet || st.made {
		return nil
	}
	shapes, err := w.mysqlShapes(st)
	if err != nil {
		return err
	}
	if s, ok := shapes[strings.ToLower(name)]; ok && w.index(st, "", name) < 0 {
		w.indexes = append(w.indexes, droppedIndex{name: name, st: st, shape: s})
	}
	return nil
}

// mysqlIndexDrops records each DROP INDEX among the actions from word i on.
func (w *recreates) mysqlIndexDrops(l *fileLinter, st *tableState, i int) error {
	for depth, start := 0, true; i < len(l.ws); i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && l.punct(i, ","):
			start = true
			continue
		case start && l.upper(i) == "DROP" && (l.upper(i+1) == "INDEX" || l.upper(i+1) == "KEY") && i+2 < len(l.ws):
			if err := w.mysqlIndexDrop(st, l.ws[i+2].text); err != nil {
				return err
			}
		}
		start = false
	}
	return nil
}

// createIndex checks a top-level CREATE INDEX against the index the walk dropped.
func (w *recreates) createIndex(l *fileLinter) {
	i, unique, method := 1, false, ""
	switch l.upper(1) {
	case "UNIQUE":
		i, unique = 2, true
	case "FULLTEXT", "SPATIAL":
		i, method = 2, strings.ToLower(l.upper(1))
	}
	if l.upper(i) != "INDEX" {
		return
	}
	i = l.skip(l.skip(i+1, "CONCURRENTLY"), "IF", "NOT", "EXISTS")
	if i >= len(l.ws) || l.upper(i) == "ON" || !l.isName(i) {
		return
	}
	name := w.word(l, i)
	for i < len(l.ws) && l.upper(i) != "ON" {
		i++
	}
	ref, j, ok := w.tableAt(l, l.skip(i+1, "ONLY"))
	if !ok {
		return
	}
	stated, read := l.indexAt(j, len(l.ws))
	stated.unique = unique
	stated.method = cmp.Or(method, stated.method)
	if w.s.d == velox.Postgres {
		w.built = append(w.built, builtIndex{name: name, st: w.state(ref), ref: ref, shape: stated, read: read})
	}
	w.remade(w.state(ref), ref, name, stated, read)
}

// A builtIndex is an index a statement of the walk made, on the table st, named ref
// then.
type builtIndex struct {
	name  string
	st    *tableState
	ref   TableRef
	shape indexShape
	read  bool
}

// alterIndex walks PostgreSQL's ALTER INDEX [IF EXISTS] a RENAME TO b. An index the
// walk made, renamed to the name of one it dropped, makes that one again: a plan's
// change of an index builds the new one beside the old and renames it.
func (w *recreates) alterIndex(l *fileLinter) {
	raw, j, ok := l.tableRef(l.skip(2, "IF", "EXISTS"))
	if !ok || l.upper(j) != "RENAME" || l.upper(j+1) != "TO" || j+2 >= len(l.ws) {
		return
	}
	name, schema := w.word(l, j-1), ""
	if raw.Schema != "" {
		schema = w.norm(TableRef{Schema: w.word(l, j-3)}).Schema
	}
	k := slices.IndexFunc(w.built, func(b builtIndex) bool {
		return b.name == name && (raw.Schema == "" || b.ref.Schema == schema)
	})
	if k < 0 {
		return
	}
	b := w.built[k]
	w.built = slices.Delete(w.built, k, k+1)
	to := w.word(l, j+2)
	b.name = to
	w.built = append(w.built, b)
	w.remade(b.st, b.ref, to, b.shape, b.read)
}

// mysqlIndexAdd checks an action's ADD of an index, at i after ADD: [CONSTRAINT [x]]
// [UNIQUE|FULLTEXT|SPATIAL] [INDEX|KEY] [name] (...).
func (w *recreates) mysqlIndexAdd(l *fileLinter, st *tableState, ref TableRef, i, end int) {
	name := ""
	if l.upper(i) == "CONSTRAINT" {
		i++
		if l.upper(i) != "UNIQUE" && i < end {
			name = w.word(l, i)
			i++
		}
	}
	unique, method := false, ""
	switch l.upper(i) {
	case "UNIQUE":
		unique = true
		i++
	case "FULLTEXT", "SPATIAL":
		method = strings.ToLower(l.upper(i))
		i++
	case "INDEX", "KEY":
	default:
		return
	}
	if l.upper(i) == "INDEX" || l.upper(i) == "KEY" {
		i++
	}
	if i < end && !l.punct(i, "(") && l.upper(i) != "USING" {
		name = w.word(l, i)
		i++
	}
	if name == "" {
		return
	}
	stated, read := l.indexAt(i, end)
	stated.unique = unique
	stated.method = cmp.Or(method, stated.method)
	w.remade(st, ref, name, stated, read)
}

// modelAttrs are what a plan's next file may well change of an index a file before it
// dropped on purpose: past the file, the rest alone is a loss.
var modelAttrs = []string{"unique", "columns", "where"}

// remade checks the statement that makes the index name against the one the walk
// dropped under that name, when it did; read is false when it cannot be read.
func (w *recreates) remade(st *tableState, ref TableRef, name string, stated indexShape, read bool) {
	i := w.index(st, ref.Schema, name)
	if i < 0 {
		return
	}
	was, on := w.indexes[i].shape, w.indexes[i].on
	w.indexes = slices.Delete(w.indexes, i, i+1)
	object := colTarget(ref, name)
	if w.s.d == velox.Postgres && on != st.ref && !w.quiet {
		// PostgreSQL names an index in its schema: the one dropped was another
		// table's, which loses it whatever this one is. A file before a later one
		// that makes the name on another table dropped it on purpose.
		other := colTarget(on, name)
		w.lost(on, nil, other, other+": the index is on "+on.String()+", and the statement that makes one of its name is on "+ref.String()+"; rename one of them")
		return
	}
	if !read {
		w.lost(ref, nil, object, object+": the statement that makes the index again cannot be read, and may lose what it has")
		return
	}
	if err := w.load(st); err != nil {
		w.fail = cmp.Or(w.fail, err)
	}
	lines := shapeDiffers(w.s.d, st.enums, was, w.beforeRenames(st, stated))
	for _, attr := range slices.Sorted(maps.Keys(lines)) {
		if w.quiet && slices.Contains(modelAttrs, attr) {
			continue
		}
		w.attrLost(ref, object, attr, lines[attr], shapeValue(was, attr), func(want string) bool {
			if attr == "where" && was.where != "" && want != "" {
				same, _ := samePredicateIn(w.s.d, was.where, want, st.enums)
				return same
			}
			return strings.EqualFold(shapeValue(was, attr), want)
		})
	}
}

// beforeRenames is the shape a statement writes, with each column the file
// renamed before it by its name in the database: the index the walk dropped
// was read from there, before the file ran.
func (w *recreates) beforeRenames(st *tableState, s indexShape) indexShape {
	type rename struct{ now, was, tmp string }
	var renames []rename
	for _, now := range slices.Sorted(maps.Keys(st.cols)) {
		if was := st.cols[now]; was != "" && was != now {
			renames = append(renames, rename{now, was, fmt.Sprintf("velox_rename_%d", len(renames))})
		}
	}
	if len(renames) == 0 {
		return s
	}
	// Through a name of its own each, so that a swap of two names holds.
	back := func(expr string) string {
		for _, r := range renames {
			expr = renameIdent(w.s.d, expr, r.now, r.tmp)
		}
		for _, r := range renames {
			expr = renameIdent(w.s.d, expr, r.tmp, r.was)
		}
		return expr
	}
	s.keys = slices.Clone(s.keys)
	for i := range s.keys {
		s.keys[i].name = back(s.keys[i].name)
	}
	s.include = slices.Clone(s.include)
	for i := range s.include {
		s.include[i] = back(s.include[i])
	}
	s.where = back(s.where)
	return s
}

// shapeValue is the attribute attr of an index as a velox:drops line records it, for
// what a model's index holds.
func shapeValue(s indexShape, attr string) string {
	switch attr {
	case "unique":
		return strconv.FormatBool(s.unique)
	case "columns":
		names := make([]string, len(s.keys))
		for i, k := range s.keys {
			names[i] = k.name
		}
		return strings.Join(names, ",")
	case "where":
		return s.where
	}
	return ""
}

// shrunk records each UNIQUE index of st the drop of the column col leaves narrower,
// as MySQL does in silence: a UNIQUE (a, b) becomes a UNIQUE (a), which holds rows to
// more than it did. A plain index only serves other queries, and gets a note. An index
// the file drops itself, or all of whose columns go, is no loss.
func (w *recreates) shrunk(l *fileLinter, st *tableState, ref TableRef, col string) error {
	if w.quiet || st.made || col == "" {
		return nil
	}
	shapes, err := w.mysqlShapes(st)
	if err != nil {
		return err
	}
	gone := l.droppedColumns()
	for _, name := range slices.Sorted(maps.Keys(shapes)) {
		s := shapes[name]
		on := false
		var left []string
		for _, k := range s.keys {
			if k.name == col {
				on = true
			} else if !slices.Contains(gone, k.name) {
				left = append(left, k.name)
			}
		}
		if !on || len(left) == 0 || w.fileDropsIndex(name) {
			continue
		}
		object := colTarget(ref, name)
		if !s.unique {
			w.s.say(w.f.Path + ": " + object + ": DROP COLUMN " + col + " leaves the index over (" + strings.Join(left, ", ") + ")")
			continue
		}
		w.lost(ref, nil, object, object+": the index is over "+strconv.Itoa(len(s.keys))+" columns, and DROP COLUMN "+col+" leaves it over ("+strings.Join(left, ", ")+"), which holds rows to more than it did; drop the index in the file")
	}
	return nil
}

// droppedColumns is the columns the statement's actions drop, in lower case.
func (l *fileLinter) droppedColumns() []string {
	var out []string
	for i := range l.ws {
		if l.upper(i) != "DROP" {
			continue
		}
		j := l.skip(i+1, "COLUMN")
		if j < len(l.ws) && l.isName(j) && !slices.Contains(notColumn, l.upper(j)) {
			out = append(out, strings.ToLower(l.ws[j].text))
		}
	}
	return out
}

// fileDropsIndex reports whether a statement of the file drops an index named name.
func (w *recreates) fileDropsIndex(name string) bool {
	return slices.ContainsFunc(w.f.Statements, func(q string) bool {
		l := &fileLinter{d: w.s.d, ws: lex(w.s.d, q)}
		for i := range l.ws {
			if l.upper(i) == "DROP" && (l.upper(i+1) == "INDEX" || l.upper(i+1) == "KEY") && i+2 < len(l.ws) && strings.EqualFold(l.ws[i+2].text, name) {
				return true
			}
		}
		return false
	})
}

// A mysqlUse is a view or trigger, with the words its text is made of.
type mysqlUse struct {
	kind, name string
	table      string // a trigger's
	words      map[string]bool
}

// mysqlUsesOf reads the views and triggers of database db, once.
func (w *recreates) mysqlUsesOf(db string) ([]mysqlUse, error) {
	if uses, ok := w.uses[db]; ok {
		return uses, nil
	}
	if w.uses == nil {
		w.uses = map[string][]mysqlUse{}
	}
	var uses []mysqlUse
	text := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, wd := range lex(velox.MySQL, s) {
			out[strings.ToLower(wd.text)] = true
		}
		return out
	}
	err := eachRow(w.ctx, w.s.conn, `SELECT TABLE_NAME, VIEW_DEFINITION FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ?`, []any{db}, func(rows *sql.Rows) error {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return err
		}
		uses = append(uses, mysqlUse{kind: "view", name: name, words: text(def)})
		return nil
	})
	if err == nil {
		err = eachRow(w.ctx, w.s.conn, `SELECT TRIGGER_NAME, EVENT_OBJECT_TABLE, ACTION_STATEMENT FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ?`, []any{db}, func(rows *sql.Rows) error {
			var name, table, def string
			if err := rows.Scan(&name, &table, &def); err != nil {
				return err
			}
			uses = append(uses, mysqlUse{kind: "trigger", name: name, table: table, words: text(def)})
			return nil
		})
	}
	if err != nil {
		return nil, fmt.Errorf("migrate: %s: reading the views and triggers of %s: %w", w.f.Path, db, err)
	}
	w.uses[db] = uses
	return uses, nil
}

// pgFunctionsBreak records each PL/pgSQL function of a trigger whose body names the
// column col of st, which the statement renames or drops; "" is a column the file
// made, which nothing names yet.
func (w *recreates) pgFunctionsBreak(st *tableState, ref TableRef, col string) error {
	if col == "" {
		return nil
	}
	return w.pgTriggersNaming(st, ref, col, false)
}

// pgTableBreaks records each PL/pgSQL function of a trigger whose body names st,
// which the statement renames, moves, or drops when drops says so.
func (w *recreates) pgTableBreaks(st *tableState, ref TableRef, drops bool) error {
	return w.pgTriggersNaming(st, ref, "", drops)
}

// pgTriggersNaming records each PL/pgSQL function of a trigger whose body names what
// the statement renames, moves or drops: the column col of st, or st itself when col
// is "". PostgreSQL lets it, as it does not read the body until the trigger fires, and
// every write to the trigger's table then fails. A trigger on st counts when its body
// names col; a trigger on another table when it names st, and col too when there is
// one. A trigger on st goes with st when drops says the statement drops the table. The
// body is read as words, which errs on the safe side; a name built into an EXECUTE's
// string is not seen.
func (w *recreates) pgTriggersNaming(st *tableState, ref TableRef, col string, drops bool) error {
	if w.quiet || w.s.d != velox.Postgres || st.made || col == "" && w.fileRemakes(st.ref.Name) {
		return nil
	}
	name := ddl.Ident(velox.Postgres, st.ref.Name)
	if st.ref.Schema != "" {
		name = ddl.Ident(velox.Postgres, st.ref.Schema) + "." + name
	}
	err := eachRow(w.ctx, w.s.conn, `SELECT t.tgname, p.proname, p.prosrc, t.tgrelid::regclass::text, t.tgrelid = to_regclass($1)
FROM pg_trigger t JOIN pg_proc p ON p.oid = t.tgfoid JOIN pg_language g ON g.oid = p.prolang
WHERE NOT t.tgisinternal AND g.lanname = 'plpgsql'
ORDER BY t.tgname`, []any{name}, func(rows *sql.Rows) error {
		var trigger, function, body, on string
		var own bool
		if err := rows.Scan(&trigger, &function, &body, &on, &own); err != nil {
			return err
		}
		words := lex(velox.Postgres, body)
		names := func(s string) bool {
			return slices.ContainsFunc(words, func(wd word) bool { return strings.EqualFold(wd.text, s) })
		}
		hit := names(st.ref.Name) && (!own || !drops)
		what := ref.String()
		if col != "" {
			hit, what = names(col) && (own || names(st.ref.Name)), colTarget(ref, col)
		}
		if hit && !w.fileRemakes(function) && !w.fileRemakes(trigger) {
			w.lost(ref, nil, function, fmt.Sprintf("the function %s of the trigger %s on %s names %s, and every write to %s fails once the statement has renamed, moved or dropped it; replace the function in the file", function, trigger, on, what, on))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate: %s: reading the triggers that name %s: %w", w.f.Path, ref, err)
	}
	return nil
}

// statesOnline reports whether the statement says ALGORITHM=INPLACE or LOCK=NONE.
func (l *fileLinter) statesOnline() bool {
	for i := range l.ws {
		j := i + 1
		if l.punct(j, "=") {
			j++
		}
		if l.upper(i) == "ALGORITHM" && l.upper(j) == "INPLACE" || l.upper(i) == "LOCK" && l.upper(j) == "NONE" {
			return true
		}
	}
	return false
}

// metadataOnly reports whether each action of the ALTER TABLE from i on only renames
// or sets a default, which is all MyISAM runs as ALGORITHM=INPLACE or LOCK=NONE.
func (l *fileLinter) metadataOnly(i int) bool {
	for depth, lead := 0, true; i < len(l.ws); i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && l.punct(i, ","):
			lead = true
		case lead:
			if !slices.Contains([]string{"RENAME", "ALTER", "ALGORITHM", "LOCK"}, l.upper(i)) {
				return false
			}
			lead = false
		}
	}
	return true
}

// myisamOnline refuses a statement that says ALGORITHM=INPLACE or LOCK=NONE on a
// MyISAM table, as the server does with 1845 once the file is under way. It is only
// for MyISAM that this was tried, and other engines are left to the server.
func (w *recreates) myisamOnline(l *fileLinter, st *tableState, ref TableRef) error {
	if w.quiet || st.made || !l.statesOnline() {
		return nil
	}
	engine := ""
	err := eachRow(w.ctx, w.s.conn, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`,
		[]any{cmp.Or(st.ref.Schema, w.s.home()), st.ref.Name}, func(rows *sql.Rows) error {
			var e sql.NullString
			err := rows.Scan(&e)
			engine = e.String
			return err
		})
	if err != nil {
		return fmt.Errorf("migrate: %s: reading the engine of %s: %w", w.f.Path, ref, err)
	}
	if !strings.EqualFold(engine, "MyISAM") {
		return nil
	}
	return &Error{Code: "unsupported", Pos: w.f.Path, Object: ref.String(), Reason: "the table is MyISAM, which cannot run " + l.lead() +
		" under ALGORITHM=INPLACE or LOCK=NONE, and nothing was changed: take those out of a file not yet applied anywhere, or first run ALTER TABLE " + ref.String() + " ENGINE=InnoDB"}
}

// pgRelkind is pg_class's kind of the table or index ref, "" for none.
func (w *recreates) pgRelkind(ref TableRef) (string, error) {
	name := ddl.Ident(velox.Postgres, ref.Name)
	if ref.Schema != "" {
		name = ddl.Ident(velox.Postgres, ref.Schema) + "." + name
	}
	kind := ""
	err := eachRow(w.ctx, w.s.conn, `SELECT relkind::text FROM pg_class WHERE oid = to_regclass($1)`, []any{name}, func(rows *sql.Rows) error {
		return rows.Scan(&kind)
	})
	if err != nil {
		return "", fmt.Errorf("migrate: %s: reading %s: %w", w.f.Path, ref, err)
	}
	return kind, nil
}

// partitionedCreate refuses CREATE INDEX CONCURRENTLY on a partitioned table, which
// the server refuses with the file under way.
func (w *recreates) partitionedCreate(l *fileLinter) error {
	if w.quiet || w.s.d != velox.Postgres {
		return nil
	}
	i := l.skip(1, "UNIQUE")
	if l.upper(i) != "INDEX" || l.upper(i+1) != "CONCURRENTLY" {
		return nil
	}
	for i < len(l.ws) && l.upper(i) != "ON" {
		i++
	}
	ref, _, ok := w.tableAt(l, l.skip(i+1, "ONLY"))
	if !ok || w.state(ref).made {
		return nil
	}
	kind, err := w.pgRelkind(ref)
	if err != nil || kind != "p" {
		return err
	}
	return &Error{Code: "unsupported", Pos: w.f.Path, Object: ref.String(), Reason: "the table is partitioned, and PostgreSQL has no CREATE INDEX CONCURRENTLY on one; nothing was changed. " +
		"Build the index with CONCURRENTLY on each partition, then CREATE INDEX ON ONLY the parent and ALTER INDEX ... ATTACH PARTITION each, in place of this statement on a file not yet applied anywhere, or in a new migration"}
}

// partitionedDrop refuses DROP INDEX CONCURRENTLY of a partitioned index.
func (w *recreates) partitionedDrop(ref TableRef) error {
	kind, err := w.pgRelkind(ref)
	if err != nil || kind != "I" {
		return err
	}
	return &Error{Code: "unsupported", Pos: w.f.Path, Object: ref.String(), Reason: "the index is partitioned, and PostgreSQL has no DROP INDEX CONCURRENTLY of one; nothing was changed. " +
		"Drop it without CONCURRENTLY, which locks the parent and its partitions, in place of this statement on a file not yet applied anywhere, or in a new migration"}
}

// fileRemakes reports whether the file drops, makes or replaces the view, trigger or
// function name, or, for a table, makes it.
func (w *recreates) fileRemakes(name string) bool {
	if w.remakes == nil {
		w.remakes = map[string]bool{}
		for _, q := range w.f.Statements {
			l := &fileLinter{d: w.s.d, ws: lex(w.s.d, q)}
			lead := l.upper(0)
			if lead != "DROP" && lead != "CREATE" && lead != "ALTER" {
				continue
			}
			i := l.skip(1, "OR", "REPLACE") // CREATE TRIGGER's EXECUTE FUNCTION makes none
			if lead == "ALTER" || l.upper(i) != "FUNCTION" {
				i = slices.IndexFunc(l.ws, func(wd word) bool {
					up := strings.ToUpper(wd.text)
					return !wd.quoted && (up == "VIEW" || up == "TRIGGER" && lead == "DROP" || up == "TABLE" && lead == "CREATE")
				})
			}
			if i < 0 {
				continue
			}
			for j := l.skip(l.skip(i+1, "IF", "EXISTS"), "IF", "NOT", "EXISTS"); j < len(l.ws) && l.isName(j); j += 2 {
				for l.punct(j+1, ".") {
					j += 2
				}
				w.remakes[strings.ToLower(l.ws[j].text)] = true
				if lead != "DROP" || !l.punct(j+1, ",") {
					break
				}
			}
		}
	}
	return w.remakes[strings.ToLower(name)]
}

// breaks records each view and trigger that names the column col of st, or st itself
// when col is "", which the statement renames or drops: MySQL lets it, and the view
// then fails 1356, the trigger's INSERT 1054. A trigger of the table moves and goes
// with the table, and it names the table's columns without naming the table. The
// text is read as words, which errs on the safe side.
func (w *recreates) breaks(st *tableState, ref TableRef, col string) error {
	if w.quiet || w.s.d != velox.MySQL || st.made {
		return nil
	}
	uses, err := w.mysqlUsesOf(cmp.Or(st.ref.Schema, w.s.home()))
	if err != nil {
		return err
	}
	table := strings.ToLower(st.ref.Name)
	if col == "" && w.fileRemakes(table) {
		return nil
	}
	for _, u := range uses {
		own := strings.EqualFold(u.table, table)
		hit := u.words[table] && !own
		if col != "" {
			hit = (u.words[table] || own) && u.words[strings.ToLower(col)]
		}
		if !hit || w.fileRemakes(u.name) {
			continue
		}
		what := ref.String()
		if col != "" {
			what = colTarget(ref, col)
		}
		// A file can remake a view, but cannot hold a CREATE TRIGGER (ParseFile refuses
		// one on MySQL): a trigger is dropped and made again by hand around the run.
		way := "remake the view in the file, or drop it first"
		if u.kind == "trigger" {
			way = "drop the trigger by hand before migrate apply and create it again after; a file cannot hold CREATE TRIGGER"
		}
		w.lost(ref, nil, u.name, fmt.Sprintf("the %s %s names %s, and fails once the statement has renamed or dropped it; %s", u.kind, u.name, what, way))
	}
	return nil
}
