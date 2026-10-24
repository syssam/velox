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
