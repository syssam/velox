package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// inspectSQLite reads tables through the pragmas and the CREATE text of
// sqlite_schema, the default schema being main. The pragmas give columns,
// keys and indexes; the CREATE text what they do not: constraint names,
// checks, AUTOINCREMENT, a column's collation, DEFERRABLE and the table's
// options. A table whose CREATE text parseCreateTable cannot read has no
// checks and unnamed foreign keys, and a note says so.
func inspectSQLite(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error) {
	r := &liteReader{ctx: ctx, q: q}
	ts, err := r.tables(tables)
	if err != nil {
		return nil, nil, err
	}
	s := &Schema{Dialect: velox.SQLite}
	for _, lt := range ts {
		if err := r.table(&lt); err != nil {
			return nil, nil, err
		}
		s.Tables = append(s.Tables, lt.t)
	}
	return s, r.notes, nil
}

// liteReader holds one inspection.
type liteReader struct {
	ctx   context.Context
	q     querier
	notes []note
}

// liteTable is a table inspectSQLite reads: its database (main or an
// attached one), its CREATE text and what parseCreateTable read of it, the
// model's table, and what its note on the table itself says.
type liteTable struct {
	db   string
	sql  string
	ct   createTable
	t    Table
	cols []string // every column's name, generated ones too
	says []string
}

func (r *liteReader) each(query string, args []any, scan func(*sql.Rows) error) error {
	return eachRow(r.ctx, r.q, query, args, scan)
}

func (r *liteReader) note(lt *liteTable, name, message string) {
	object := lt.t.Ref().String()
	if name != "" {
		object += "." + name
	}
	r.notes = append(r.notes, note{table: lt.t.Ref(), object: object, message: message})
}

// liteTablesQuery reads the tables of the database db, but SQLite's own.
func liteTablesQuery(db string) string {
	return `SELECT name, coalesce(sql, '') FROM ` + ddl.Ident(velox.SQLite, db) + `.sqlite_schema
WHERE type = 'table' AND substr(name, 1, 7) <> 'sqlite_'`
}

// tables finds the tables to read: those of refs that are there, once each,
// or with refs nil every table of main but velox_migrations. A ref's schema
// is an attached database; main is the default one.
func (r *liteReader) tables(refs []TableRef) ([]liteTable, error) {
	var out []liteTable
	scan := func(db string) func(*sql.Rows) error {
		return func(rows *sql.Rows) error {
			lt := liteTable{db: db}
			if err := rows.Scan(&lt.t.Name, &lt.sql); err != nil {
				return err
			}
			if db != "main" {
				lt.t.Schema = db
			}
			if slices.ContainsFunc(out, func(x liteTable) bool { return x.t.Ref() == lt.t.Ref() }) {
				return nil
			}
			out = append(out, lt)
			return nil
		}
	}
	if refs == nil {
		err := r.each(liteTablesQuery("main")+` AND name <> 'velox_migrations' ORDER BY name`, nil, scan("main"))
		return out, err
	}
	var dbs []string
	err := r.each(`SELECT name FROM pragma_database_list`, nil, func(rows *sql.Rows) error {
		var name string
		err := rows.Scan(&name)
		dbs = append(dbs, name)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		db := cmp.Or(ref.Schema, "main")
		if !slices.Contains(dbs, db) {
			continue
		}
		if err := r.each(liteTablesQuery(db)+` AND name = ?`, []any{ref.Name}, scan(db)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// table reads lt's columns, indexes and foreign keys into lt.t, and its
// checks from its CREATE text. What the table itself has that the model
// cannot hold is one note on it.
func (r *liteReader) table(lt *liteTable) error {
	var ok bool
	if lt.ct, ok = parseCreateTable(lt.sql); !ok {
		lt.says = append(lt.says, "its CREATE TABLE cannot be read, so its checks, its foreign keys' names and AUTOINCREMENT are not read")
	} else if len(lt.ct.options) > 0 {
		lt.says = append(lt.says, "the table is "+strings.Join(lt.ct.options, ", ")+", which the model cannot hold; it is compared without")
	}
	if err := r.columns(lt); err != nil {
		return err
	}
	if err := r.indexes(lt); err != nil {
		return err
	}
	if err := r.keys(lt); err != nil {
		return err
	}
	lt.t.Checks = slices.Clone(lt.ct.checks)
	if len(lt.says) > 0 {
		r.note(lt, "", strings.Join(lt.says, "; "))
	}
	return nil
}

const liteColumnsQuery = `SELECT name, type, "notnull", dflt_value, pk, hidden
FROM pragma_table_xinfo(?, ?) ORDER BY cid`

// columns reads lt's columns: a generated column is a note and left out, a
// column's collation other than BINARY a note.
func (r *liteReader) columns(lt *liteTable) error {
	type keyed struct {
		pk   int
		name string
	}
	var key []keyed
	err := r.each(liteColumnsQuery, []any{lt.t.Name, lt.db}, func(rows *sql.Rows) error {
		var (
			c               Column
			typ             string
			notNull, pk, hd int
			def             sql.NullString
		)
		if err := rows.Scan(&c.Name, &typ, &notNull, &def, &pk, &hd); err != nil {
			return err
		}
		lt.cols = append(lt.cols, c.Name)
		switch hd {
		case 0:
		case 2, 3:
			r.note(lt, c.Name, "the column is generated from an expression, which the model cannot hold; it is not compared")
			return nil
		default:
			return nil // a virtual table's hidden column
		}
		c.Type = ddl.NormalizeType(velox.SQLite, typ)
		c.Nullable = notNull == 0
		if w, ok := lt.ct.defaults[c.Name]; ok {
			def = sql.NullString{String: w, Valid: true} // dflt_value drops a group's parentheses
		}
		if def.Valid && !strings.EqualFold(def.String, "NULL") {
			c.Default = def.String // as written
		}
		if pk > 0 {
			key = append(key, keyed{pk, c.Name})
		}
		if coll := lt.ct.collations[c.Name]; coll != "" && !strings.EqualFold(coll, "BINARY") {
			r.note(lt, c.Name, "the column has COLLATE "+coll+", which the model cannot hold; it is compared without")
		}
		lt.t.Columns = append(lt.t.Columns, c)
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(key, func(a, b keyed) int { return cmp.Compare(a.pk, b.pk) })
	for _, k := range key {
		lt.t.PrimaryKey = append(lt.t.PrimaryKey, k.name)
	}
	// Only an integer key with AUTOINCREMENT never reuses a deleted top ID.
	if len(lt.t.PrimaryKey) == 1 && lt.ct.autoincrement {
		if c := lt.t.column(lt.t.PrimaryKey[0]); c.Type == "integer" {
			c.Identity = true
		}
	}
	return nil
}

// liteIndex is an index as pragma_index_list gives it.
type liteIndex struct {
	name            string
	unique, partial bool
	origin          string
}

// indexes reads lt's indexes but the primary key's: a UNIQUE constraint's
// unnamed, as SQLite keeps no name for it, a partial index's predicate the
// text after WHERE in its CREATE INDEX. An index over an expression is a
// note and left out; DESC or a collation other than BINARY is a note, and
// the index is read without it.
func (r *liteReader) indexes(lt *liteTable) error {
	var list []liteIndex
	err := r.each(`SELECT name, "unique", origin, partial FROM pragma_index_list(?, ?) ORDER BY name`, []any{lt.t.Name, lt.db}, func(rows *sql.Rows) error {
		var li liteIndex
		err := rows.Scan(&li.name, &li.unique, &li.origin, &li.partial)
		if li.origin != "pk" {
			list = append(list, li)
		}
		return err
	})
	if err != nil {
		return err
	}
	for _, li := range list {
		ix := Index{Name: li.name, Unique: li.unique}
		if li.origin == "u" {
			ix.Name = ""
		}
		var expr, desc bool
		var colls []string
		err := r.each(`SELECT cid, coalesce(name, ''), "desc", coalesce(coll, '') FROM pragma_index_xinfo(?, ?) WHERE key = 1 ORDER BY seqno`,
			[]any{li.name, lt.db}, func(rows *sql.Rows) error {
				var cid int
				var name, coll string
				var d bool
				if err := rows.Scan(&cid, &name, &d, &coll); err != nil {
					return err
				}
				expr = expr || cid == -2
				desc = desc || d
				if coll != "" && !strings.EqualFold(coll, "BINARY") && !slices.Contains(colls, coll) {
					colls = append(colls, coll)
				}
				ix.Columns = append(ix.Columns, name)
				return nil
			})
		if err != nil {
			return err
		}
		object := ix.Name
		if object == "" {
			object = "(" + strings.Join(ix.Columns, ", ") + ")"
		}
		if expr {
			r.note(lt, object, "the index is over an expression, which the model cannot hold; it is not compared")
			continue
		}
		var has []string
		if desc {
			has = append(has, "DESC")
		}
		for _, c := range colls {
			has = append(has, "COLLATE "+c)
		}
		if li.partial {
			var text string
			q := `SELECT coalesce(sql, '') FROM ` + ddl.Ident(velox.SQLite, lt.db) + `.sqlite_schema WHERE type = 'index' AND name = ?`
			if err := r.each(q, []any{li.name}, func(rows *sql.Rows) error { return rows.Scan(&text) }); err != nil {
				return err
			}
			where, ok := indexWhere(text, lt.cols)
			if !ok {
				has = append(has, "a predicate that cannot be read")
			}
			ix.Where = where
		}
		if len(has) > 0 {
			r.note(lt, object, "the index has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
		}
		lt.t.Indexes = append(lt.t.Indexes, ix)
	}
	return nil
}

const liteKeysQuery = `SELECT id, "table", "from", "to", on_update, on_delete
FROM pragma_foreign_key_list(?, ?) ORDER BY id DESC, seq`

// keys reads lt's foreign keys. SQLite numbers them last first, so they are
// read in the order the CREATE text declares them; each takes the name and
// DEFERRABLE of the first key of the text not taken yet with its columns,
// table and referenced columns. The referenced table and columns are named
// as declared; a key that names no columns references its table's primary
// key. ON UPDATE other than no action, and DEFERRABLE, are a note.
func (r *liteReader) keys(lt *liteTable) error {
	type liteKey struct {
		fk       ForeignKey
		onUpdate string
		to       []sql.NullString
	}
	var keys []*liteKey
	last := -1
	err := r.each(liteKeysQuery, []any{lt.t.Name, lt.db}, func(rows *sql.Rows) error {
		var (
			id                         int
			ref, from, onUpdate, onDel string
			to                         sql.NullString
		)
		if err := rows.Scan(&id, &ref, &from, &to, &onUpdate, &onDel); err != nil {
			return err
		}
		if id != last {
			last = id
			if onDel == "NO ACTION" {
				onDel = ""
			}
			keys = append(keys, &liteKey{fk: ForeignKey{RefTable: TableRef{Schema: lt.t.Schema, Name: ref}, OnDelete: onDel}, onUpdate: onUpdate})
		}
		k := keys[len(keys)-1]
		k.fk.Columns = append(k.fk.Columns, declared(from, lt.cols))
		k.to = append(k.to, to)
		return nil
	})
	if err != nil {
		return err
	}
	taken := make([]bool, len(lt.ct.foreignKeys))
	for _, k := range keys {
		fk := &k.fk
		var written []string // the referenced columns as written
		for _, s := range k.to {
			if s.Valid {
				written = append(written, s.String)
			}
		}
		var tk *textKey
		for i := range lt.ct.foreignKeys {
			t := &lt.ct.foreignKeys[i]
			if !taken[i] && sameNames(t.columns, fk.Columns) && sameName(t.refTable, fk.RefTable.Name) && sameNames(t.refColumns, written) {
				taken[i], tk = true, t
				break
			}
		}
		table, cols, key, err := r.parent(lt.db, fk.RefTable.Name)
		if err != nil {
			return err
		}
		fk.RefTable.Name = table
		if len(written) == 0 {
			fk.RefColumns = key
		} else {
			for _, c := range written {
				fk.RefColumns = append(fk.RefColumns, declared(c, cols))
			}
		}
		var has []string
		if k.onUpdate != "NO ACTION" {
			has = append(has, "ON UPDATE "+k.onUpdate)
		}
		if tk != nil {
			fk.Name = tk.name
			if tk.deferrable {
				has = append(has, "DEFERRABLE")
			}
		}
		if len(has) > 0 {
			if fk.Name != "" {
				r.note(lt, fk.Name, "the foreign key has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
			} else {
				lt.says = append(lt.says, "the foreign key ("+strings.Join(fk.Columns, ", ")+") has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
			}
		}
		lt.t.ForeignKeys = append(lt.t.ForeignKeys, *fk)
	}
	return nil
}

// parent is the table name of db, as it is declared, its columns and its
// primary key's columns in order; name itself and none when it is not
// there.
func (r *liteReader) parent(db, name string) (table string, cols, key []string, err error) {
	table = name
	q := `SELECT name FROM ` + ddl.Ident(velox.SQLite, db) + `.sqlite_schema WHERE type = 'table' AND name = ? COLLATE NOCASE`
	if err := r.each(q, []any{name}, func(rows *sql.Rows) error { return rows.Scan(&table) }); err != nil {
		return "", nil, nil, err
	}
	type keyed struct {
		pk   int
		name string
	}
	var pk []keyed
	err = r.each(`SELECT name, pk FROM pragma_table_xinfo(?, ?) ORDER BY cid`, []any{table, db}, func(rows *sql.Rows) error {
		var c keyed
		if err := rows.Scan(&c.name, &c.pk); err != nil {
			return err
		}
		cols = append(cols, c.name)
		if c.pk > 0 {
			pk = append(pk, c)
		}
		return nil
	})
	slices.SortFunc(pk, func(a, b keyed) int { return cmp.Compare(a.pk, b.pk) })
	for _, c := range pk {
		key = append(key, c.name)
	}
	return table, cols, key, err
}

// createTable is what the CREATE TABLE text of sqlite_schema names that the
// pragmas do not: constraint names, CHECK texts and AUTOINCREMENT, and what
// inspect notes: a column's collation, a DEFERRABLE key, and the table's
// options. A column a constraint names is the column's declared name,
// matched by ASCII case as SQLite matches it, and a text has no comments.
type createTable struct {
	autoincrement bool
	checks        []Check           // in order; Name "" for an unnamed one
	foreignKeys   []textKey         // in order
	defaults      map[string]string // a column's DEFAULT as written, parentheses kept, by column
	collations    map[string]string // a column's COLLATE, by column
	options       []string          // WITHOUT ROWID, STRICT
}

// textKey is a foreign key as the CREATE TABLE text gives it: its
// referenced table and columns as written, none when it names none.
type textKey struct {
	name       string // "" for an unnamed one
	columns    []string
	refTable   string
	refColumns []string
	deferrable bool
}

// sameName reports whether a and b are one name to SQLite, which folds
// ASCII letters only.
func sameName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// declared is name as one of cols, the declared names, spells it, or name
// when none is it.
func declared(name string, cols []string) string {
	for _, c := range cols {
		if sameName(c, name) {
			return c
		}
	}
	return name
}

// sameNames reports whether a and b are the same names, in order.
func sameNames(a, b []string) bool {
	return slices.EqualFunc(a, b, sameName)
}

// tokenText is the text of toks[from:to] of src without its comments, the
// space between tokens kept where it holds none, and each name of a column
// of cols spelled as cols declares it, in its own quotes.
func tokenText(src string, toks []sqlTok, from, to int, cols []string) string {
	var b strings.Builder
	for k := from; k < to; k++ {
		t := toks[k]
		if k > from {
			gap := src[toks[k-1].end:t.start]
			if strings.TrimSpace(gap) != "" {
				gap = " " // a comment
			}
			b.WriteString(gap)
		}
		text := t.text
		if n, ok := t.name(); ok && t.kind != 's' {
			if c := declared(n, cols); c != n {
				switch t.text[0] {
				case '"', '`':
					q := t.text[:1]
					text = q + strings.ReplaceAll(c, q, q+q) + q
				case '[':
					text = "[" + c + "]"
				default:
					text = c
				}
			}
		}
		b.WriteString(text)
	}
	return b.String()
}

// parseCreateTable reads sql, a CREATE TABLE as sqlite_schema holds it:
// velox's own shape and the hand-written forms, with CONSTRAINT <name>
// FOREIGN KEY or CHECK, unnamed CHECK (…), column-level REFERENCES and
// CHECK, the four quote forms and comments. false: not a form it reads.
func parseCreateTable(sql string) (createTable, bool) {
	toks, ok := lexSQL(sql)
	if !ok {
		return createTable{}, false
	}
	p := &ctParser{src: sql, toks: toks, ct: createTable{defaults: map[string]string{}, collations: map[string]string{}},
		defaults: map[string][2]int{}}
	if !p.parse() {
		return createTable{}, false
	}
	// The columns are all declared now: a check may name a later one.
	for _, c := range p.checks {
		p.ct.checks = append(p.ct.checks, Check{Name: c.name, Expr: tokenText(sql, toks, c.from, c.to, p.cols)})
	}
	for col, r := range p.defaults {
		p.ct.defaults[col] = tokenText(sql, toks, r[0], r[1], p.cols)
	}
	for i := range p.ct.foreignKeys {
		k := &p.ct.foreignKeys[i]
		for j, c := range k.columns {
			k.columns[j] = declared(c, p.cols)
		}
	}
	return p.ct, true
}

// indexWhere is the predicate of sql, a CREATE INDEX on a table of the
// columns cols: the text after its WHERE, as tokenText gives it. false: no
// WHERE, or not a text it reads.
func indexWhere(sql string, cols []string) (string, bool) {
	toks, ok := lexSQL(sql)
	if !ok {
		return "", false
	}
	depth := 0
	for k, t := range toks {
		switch {
		case t.is("("):
			depth++
		case t.is(")"):
			depth--
		case depth == 0 && t.word("WHERE"):
			where := tokenText(sql, toks, k+1, len(toks), cols)
			return where, where != ""
		}
	}
	return "", false
}

// A sqlTok is a token of SQLite's SQL: a word (a keyword, a bare name or a
// number), a quoted name (in double quotes, brackets or backquotes), a '…'
// literal, or one other byte.
type sqlTok struct {
	kind       byte // 'w', 'q', 's' or 'p'
	text       string
	start, end int
}

// is reports whether t is the punctuation s.
func (t sqlTok) is(s string) bool { return t.kind == 'p' && t.text == s }

// word reports whether t is the bare word w, in any case.
func (t sqlTok) word(w string) bool { return t.kind == 'w' && strings.EqualFold(t.text, w) }

// name is t as a name: a quoted one unquoted. ok is false for punctuation.
func (t sqlTok) name() (string, bool) {
	switch t.kind {
	case 'w':
		return t.text, true
	case 'q', 's':
		q := t.text[0]
		closing := q
		if q == '[' {
			return t.text[1 : len(t.text)-1], true
		}
		return strings.ReplaceAll(t.text[1:len(t.text)-1], string([]byte{closing, closing}), string(closing)), true
	}
	return "", false
}

// lexSQL is src's tokens, white space and comments left out. false: a
// quote or block comment is not closed.
func lexSQL(src string) ([]sqlTok, bool) {
	var toks []sqlTok
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += 2 + end + 2
		case c == '"' || c == '`' || c == '\'' || c == '[':
			closing := c
			if c == '[' {
				closing = ']'
			}
			j := i + 1
			for {
				k := strings.IndexByte(src[j:], closing)
				if k < 0 {
					return nil, false
				}
				j += k + 1
				if c != '[' && j < len(src) && src[j] == closing {
					j++ // a doubled quote
					continue
				}
				break
			}
			kind := byte('q')
			if c == '\'' {
				kind = 's'
			}
			toks = append(toks, sqlTok{kind: kind, text: src[i:j], start: i, end: j})
			i = j
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			// A number: 1, 1.5, .5, 1e-5, 0x1F.
			j := i + 1
			for j < len(src) && (wordByte(src[j]) || src[j] == '.' ||
				(src[j] == '+' || src[j] == '-') && (src[j-1] == 'e' || src[j-1] == 'E') && !strings.HasPrefix(src[i:], "0x")) {
				j++
			}
			toks = append(toks, sqlTok{kind: 'w', text: src[i:j], start: i, end: j})
			i = j
		case wordByte(c) || c == '$':
			j := i
			for j < len(src) && (wordByte(src[j]) || src[j] == '$') {
				j++
			}
			toks = append(toks, sqlTok{kind: 'w', text: src[i:j], start: i, end: j})
			i = j
		default:
			toks = append(toks, sqlTok{kind: 'p', text: src[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return toks, true
}

// ctParser is parseCreateTable's reader: toks from i.
type ctParser struct {
	src      string
	toks     []sqlTok
	i        int
	ct       createTable
	cols     []string          // the declared columns, in order
	autoinc  bool              // names read AUTOINCREMENT
	checks   []textCheck       // made texts once every column is declared
	defaults map[string][2]int // a column's DEFAULT, as tokens from and to
}

// textCheck is a check as its tokens from and to.
type textCheck struct {
	name     string
	from, to int
}

func (p *ctParser) done() bool { return p.i >= len(p.toks) }

func (p *ctParser) peek() sqlTok {
	if p.done() {
		return sqlTok{}
	}
	return p.toks[p.i]
}

// word takes the next token when it is one of the bare words ws.
func (p *ctParser) word(ws ...string) bool {
	for _, w := range ws {
		if p.peek().word(w) {
			p.i++
			return true
		}
	}
	return false
}

// punct takes the next token when it is the punctuation s.
func (p *ctParser) punct(s string) bool {
	if p.peek().is(s) {
		p.i++
		return true
	}
	return false
}

// name takes a name.
func (p *ctParser) name() (string, bool) {
	if p.done() {
		return "", false
	}
	n, ok := p.toks[p.i].name()
	if ok {
		p.i++
	}
	return n, ok
}

// group takes a parenthesised group, the next token its "(", and returns
// the tokens between its parentheses, from and to.
func (p *ctParser) group() (from, to int, ok bool) {
	if !p.peek().is("(") {
		return 0, 0, false
	}
	from = p.i + 1
	depth := 0
	for ; p.i < len(p.toks); p.i++ {
		switch t := p.toks[p.i]; {
		case t.is("("):
			depth++
		case t.is(")"):
			depth--
			if depth == 0 {
				to = p.i
				p.i++
				return from, to, true
			}
		}
	}
	return 0, 0, false
}

// names takes a parenthesised list of column names, each perhaps with
// COLLATE and ASC or DESC, and for a table's primary key AUTOINCREMENT.
func (p *ctParser) names() ([]string, bool) {
	if !p.punct("(") {
		return nil, false
	}
	var out []string
	for {
		n, ok := p.name()
		if !ok {
			return nil, false
		}
		out = append(out, n)
		if p.word("COLLATE") {
			if _, ok := p.name(); !ok {
				return nil, false
			}
		}
		p.word("ASC", "DESC")
		if p.word("AUTOINCREMENT") {
			p.autoinc = true
		}
		if p.punct(")") {
			return out, true
		}
		if !p.punct(",") {
			return nil, false
		}
	}
}

// parse reads the whole statement.
func (p *ctParser) parse() bool {
	if !p.word("CREATE") {
		return false
	}
	p.word("TEMP", "TEMPORARY")
	if !p.word("TABLE") {
		return false
	}
	if p.word("IF") && (!p.word("NOT") || !p.word("EXISTS")) {
		return false
	}
	if _, ok := p.name(); !ok {
		return false
	}
	if p.punct(".") {
		if _, ok := p.name(); !ok {
			return false
		}
	}
	if !p.punct("(") {
		return false
	}
	for {
		if !p.definition() {
			return false
		}
		if p.punct(")") {
			break
		}
		if !p.punct(",") {
			return false
		}
	}
	for !p.done() {
		switch {
		case p.word("WITHOUT"):
			if !p.word("ROWID") {
				return false
			}
			p.ct.options = append(p.ct.options, "WITHOUT ROWID")
		case p.word("STRICT"):
			p.ct.options = append(p.ct.options, "STRICT")
		default:
			return false
		}
		if !p.done() && !p.punct(",") {
			return false
		}
	}
	return true
}

// definition reads a column's definition or a table constraint.
func (p *ctParser) definition() bool {
	if t := p.peek(); t.word("CONSTRAINT") || t.word("PRIMARY") || t.word("UNIQUE") || t.word("CHECK") || t.word("FOREIGN") {
		return p.tableConstraint()
	}
	col, ok := p.name()
	if !ok {
		return false
	}
	p.cols = append(p.cols, col)
	// The type: names, then perhaps (n) or (n, m).
	for t := p.peek(); (t.kind == 'w' || t.kind == 'q' || t.kind == 's') && !columnKeyword(t); t = p.peek() {
		p.i++
	}
	if p.peek().is("(") {
		if _, _, ok := p.group(); !ok {
			return false
		}
	}
	name := ""
	for t := p.peek(); !t.is(",") && !t.is(")"); t = p.peek() {
		if p.done() {
			return false
		}
		named := name
		name = ""
		switch {
		case p.word("CONSTRAINT"):
			if name, ok = p.name(); !ok {
				return false
			}
		case p.word("PRIMARY"):
			if !p.word("KEY") {
				return false
			}
			p.word("ASC", "DESC")
			if !p.conflict() {
				return false
			}
			if p.word("AUTOINCREMENT") {
				p.ct.autoincrement = true
			}
		case p.word("NOT"):
			if !p.word("NULL") || !p.conflict() {
				return false
			}
		case p.word("NULL"), p.word("UNIQUE"):
			if !p.conflict() {
				return false
			}
		case p.word("CHECK"):
			from, to, ok := p.group()
			if !ok {
				return false
			}
			p.checks = append(p.checks, textCheck{named, from, to})
		case p.word("DEFAULT"):
			from := p.i
			if !p.defaultValue() {
				return false
			}
			p.defaults[col] = [2]int{from, p.i}
		case p.word("COLLATE"):
			coll, ok := p.name()
			if !ok {
				return false
			}
			p.ct.collations[col] = coll
		case p.word("REFERENCES"):
			if !p.references([]string{col}, named) {
				return false
			}
		case p.word("GENERATED"):
			if !p.word("ALWAYS") || !p.peek().word("AS") {
				return false
			}
		case p.word("AS"):
			if _, _, ok := p.group(); !ok {
				return false
			}
			p.word("STORED", "VIRTUAL")
		default:
			return false
		}
	}
	return true
}

// columnKeyword reports whether t starts a column constraint, so ends the
// column's type.
func columnKeyword(t sqlTok) bool {
	for _, w := range []string{"CONSTRAINT", "PRIMARY", "NOT", "NULL", "UNIQUE", "CHECK", "DEFAULT", "COLLATE", "REFERENCES", "GENERATED", "AS"} {
		if t.word(w) {
			return true
		}
	}
	return false
}

// conflict takes an ON CONFLICT clause, when there is one.
func (p *ctParser) conflict() bool {
	if !p.word("ON") {
		return true
	}
	return p.word("CONFLICT") && p.word("ROLLBACK", "ABORT", "FAIL", "IGNORE", "REPLACE")
}

// defaultValue takes a default: a group, a signed number, a literal or a
// word.
func (p *ctParser) defaultValue() bool {
	if p.peek().is("(") {
		_, _, ok := p.group()
		return ok
	}
	if p.punct("-") || p.punct("+") {
		return p.peek().kind == 'w' && p.word(p.peek().text)
	}
	if t := p.peek(); t.kind == 'w' || t.kind == 's' || t.kind == 'q' {
		p.i++
		if (t.word("x") || t.word("X")) && p.peek().kind == 's' && p.peek().start == t.end {
			p.i++ // a blob literal, x'00'
		}
		return true
	}
	return false
}

// tableConstraint reads [CONSTRAINT name] PRIMARY KEY, UNIQUE, CHECK or
// FOREIGN KEY.
func (p *ctParser) tableConstraint() bool {
	name := ""
	if p.word("CONSTRAINT") {
		var ok bool
		if name, ok = p.name(); !ok {
			return false
		}
	}
	switch {
	case p.word("PRIMARY"):
		if !p.word("KEY") {
			return false
		}
		p.autoinc = false
		cols, ok := p.names()
		if !ok {
			return false
		}
		if p.autoinc {
			// SQLite refuses AUTOINCREMENT but on one INTEGER column.
			p.ct.autoincrement = len(cols) == 1
		}
		return p.conflict()
	case p.word("UNIQUE"):
		p.autoinc = false
		if _, ok := p.names(); !ok || p.autoinc {
			return false
		}
		return p.conflict()
	case p.word("CHECK"):
		from, to, ok := p.group()
		if !ok {
			return false
		}
		p.checks = append(p.checks, textCheck{name, from, to})
		return true
	case p.word("FOREIGN"):
		if !p.word("KEY") {
			return false
		}
		p.autoinc = false
		cols, ok := p.names()
		return ok && !p.autoinc && p.word("REFERENCES") && p.references(cols, name)
	}
	return false
}

// references reads a foreign key clause after REFERENCES, for the key of
// cols named name.
func (p *ctParser) references(cols []string, name string) bool {
	k := textKey{name: name, columns: cols}
	var ok bool
	if k.refTable, ok = p.name(); !ok {
		return false
	}
	if p.peek().is("(") {
		p.autoinc = false
		if k.refColumns, ok = p.names(); !ok || p.autoinc {
			return false
		}
	}
	for {
		switch {
		case p.word("ON"):
			if !p.word("DELETE", "UPDATE") {
				return false
			}
			switch {
			case p.word("SET"):
				if !p.word("NULL", "DEFAULT") {
					return false
				}
			case p.word("NO"):
				if !p.word("ACTION") {
					return false
				}
			case p.word("CASCADE", "RESTRICT"):
			default:
				return false
			}
		case p.word("MATCH"):
			if _, ok := p.name(); !ok {
				return false
			}
		case p.peek().word("NOT") && p.i+1 < len(p.toks) && p.toks[p.i+1].word("DEFERRABLE"):
			p.i += 2
			p.initially()
		case p.word("DEFERRABLE"):
			k.deferrable = true
			p.initially()
		default:
			p.ct.foreignKeys = append(p.ct.foreignKeys, k)
			return true
		}
	}
}

// initially takes INITIALLY DEFERRED or IMMEDIATE, when there is one.
func (p *ctParser) initially() {
	if p.word("INITIALLY") {
		p.word("DEFERRED", "IMMEDIATE")
	}
}
