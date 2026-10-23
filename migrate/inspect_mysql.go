package migrate

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// inspectMySQL reads tables through information_schema, the default schema
// being DATABASE(). MySQL has no enum types: an enum is a column type.
func inspectMySQL(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error) {
	r := &myReader{ctx: ctx, q: q}
	var def sql.NullString
	err := eachRow(ctx, q, `SELECT DATABASE()`, nil, func(rows *sql.Rows) error { return rows.Scan(&def) })
	if err != nil {
		return nil, nil, err
	}
	if !def.Valid || def.String == "" {
		return nil, nil, errors.New("the connection has no database; name one in the DSN")
	}
	r.def = def.String
	ts, err := r.tables(tables)
	if err != nil {
		return nil, nil, err
	}
	s := &Schema{Dialect: velox.MySQL}
	for _, mt := range ts {
		if err := r.table(&mt); err != nil {
			return nil, nil, err
		}
		s.Tables = append(s.Tables, mt.t)
	}
	return s, r.notes, nil
}

// myReader holds one inspection.
type myReader struct {
	ctx   context.Context
	q     querier
	def   string // DATABASE()
	notes []note
}

// myTable is a table inspectMySQL reads: its database and the model's
// table.
type myTable struct {
	schema string
	t      Table
}

// each runs query and calls scan for each row.
func (r *myReader) each(query string, args []any, scan func(*sql.Rows) error) error {
	return eachRow(r.ctx, r.q, query, args, scan)
}

// schema is the model's name of the database db: "" for DATABASE().
func (r *myReader) schema(db string) string {
	if db == r.def {
		return ""
	}
	return db
}

func (r *myReader) note(mt *myTable, name, message string) {
	object := mt.t.Ref().String()
	if name != "" {
		object += "." + name
	}
	r.notes = append(r.notes, note{table: mt.t.Ref(), object: object, message: message})
}

const myTablesQuery = `SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(ENGINE, ''), COALESCE(TABLE_COLLATION, ''),
	COALESCE(TABLE_COMMENT, ''), COALESCE(CREATE_OPTIONS, '')
FROM information_schema.TABLES
WHERE TABLE_TYPE = 'BASE TABLE' AND TABLE_SCHEMA = ?`

// tables finds the tables to read: those of refs that are there, once
// each, or with refs nil every table of DATABASE() but velox_migrations. An
// engine other than InnoDB, and options the model cannot hold, are a note.
func (r *myReader) tables(refs []TableRef) ([]myTable, error) {
	var out []myTable
	scan := func(rows *sql.Rows) error {
		var mt myTable
		var engine, options string
		if err := rows.Scan(&mt.schema, &mt.t.Name, &engine, &mt.t.Collation, &mt.t.Comment, &options); err != nil {
			return err
		}
		mt.t.Schema = r.schema(mt.schema)
		if slices.ContainsFunc(out, func(x myTable) bool { return x.t.Ref() == mt.t.Ref() }) {
			return nil
		}
		var says []string
		if !strings.EqualFold(engine, "InnoDB") {
			says = append(says, "the table's engine is "+engine+", not InnoDB, which the planner writes")
		}
		if options != "" {
			says = append(says, "the table has the options "+options+", which the model cannot hold")
		}
		if len(says) > 0 {
			r.note(&mt, "", strings.Join(says, "; "))
		}
		out = append(out, mt)
		return nil
	}
	if refs == nil {
		err := r.each(myTablesQuery+` AND TABLE_NAME <> 'velox_migrations'`, []any{r.def}, scan)
		return out, err
	}
	for _, ref := range refs {
		db := ref.Schema
		if db == "" {
			db = r.def
		}
		if err := r.each(myTablesQuery+` AND TABLE_NAME = ?`, []any{db, ref.Name}, scan); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// table reads mt's columns, indexes and constraints into mt.t.
func (r *myReader) table(mt *myTable) error {
	if err := r.columns(mt); err != nil {
		return err
	}
	if err := r.indexes(mt); err != nil {
		return err
	}
	if err := r.keys(mt); err != nil {
		return err
	}
	return r.checks(mt)
}

const myColumnsQuery = `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA, COLUMN_COMMENT,
	COALESCE(COLLATION_NAME, '')
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`

// columns reads mt's columns. What a column has that the model cannot hold
// is a note: a generated column, ON UPDATE CURRENT_TIMESTAMP, INVISIBLE,
// and a collation other than the table's.
func (r *myReader) columns(mt *myTable) error {
	return r.each(myColumnsQuery, []any{mt.schema, mt.t.Name}, func(rows *sql.Rows) error {
		var (
			c                              Column
			typ, nullable, extra, collated string
			def                            sql.NullString
		)
		if err := rows.Scan(&c.Name, &typ, &nullable, &def, &extra, &c.Comment, &collated); err != nil {
			return err
		}
		c.Nullable = nullable == "YES"
		c.Type = ddl.NormalizeType(velox.MySQL, typ)
		if values, ok := ddl.ParseMySQLEnum(c.Type); ok {
			c.Type = ddl.MySQLEnum(values)
		}
		words := strings.Fields(extra)
		has := func(w string) bool {
			return slices.ContainsFunc(words, func(x string) bool { return strings.EqualFold(x, w) })
		}
		c.Identity = has("auto_increment")
		var unheld []string
		if has("VIRTUAL") || has("STORED") { // VIRTUAL GENERATED, STORED GENERATED
			unheld = append(unheld, "generated from an expression")
		} else {
			c.Default = mysqlDefault(c.Type, has("DEFAULT_GENERATED"), def)
		}
		for i, w := range words {
			if strings.EqualFold(w, "update") && i > 0 && strings.EqualFold(words[i-1], "on") && i+1 < len(words) {
				unheld = append(unheld, "ON UPDATE "+words[i+1])
			}
		}
		if has("INVISIBLE") {
			unheld = append(unheld, "invisible")
		}
		if collated != "" && collated != mt.t.Collation {
			unheld = append(unheld, "the collation "+collated+", not the table's "+mt.t.Collation)
		}
		if len(unheld) > 0 {
			r.note(mt, c.Name, "the column is "+strings.Join(unheld, ", ")+", which the model cannot hold; it is compared without")
		}
		mt.t.Columns = append(mt.t.Columns, c)
		return nil
	})
}

// mysqlDefault is a COLUMN_DEFAULT in the model's spelling. NULL is no
// default. An expression (DEFAULT_GENERATED) is as printed, information
// schema's escapes read, but a lone literal, _utf8mb4'…', is the model's
// ('…'), and a number, true, false or NULL, which MySQL prints bare, is in
// parentheses again. A literal of a string, enum, set, date or time type is quoted
// again, MySQL printing it bare; a decimal loses the zeros its scale pads
// it with; any other is as printed.
func mysqlDefault(typ string, expr bool, def sql.NullString) string {
	if !def.Valid {
		return ""
	}
	s := def.String
	if expr {
		s = unescapeMySQL(s)
		lit := s
		for _, cs := range []string{"_utf8mb4", "_utf8mb3", "_utf8"} {
			if rest, ok := strings.CutPrefix(lit, cs); ok {
				lit = rest
				break
			}
		}
		if strings.HasPrefix(lit, "'") {
			if v, end, ok := unquoteAt(lit, 0, true); ok && end == len(lit) {
				return "(" + ddl.Literal(velox.MySQL, v) + ")"
			}
		}
		if literalDefault(velox.MySQL, s) {
			return "(" + s + ")" // (42), (TRUE) and (NULL) print bare
		}
		return s
	}
	word := typ
	if i := strings.IndexAny(word, "( "); i >= 0 {
		word = word[:i]
	}
	switch word {
	case "enum", "set", "date", "datetime", "timestamp", "time":
		return ddl.Literal(velox.MySQL, s)
	case "decimal":
		if strings.Contains(s, ".") {
			s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
		}
		return s
	}
	if ddl.StringType(velox.MySQL, typ) {
		return ddl.Literal(velox.MySQL, s)
	}
	return s
}

// unescapeMySQL is s with the escapes information_schema prints an
// expression with read: \' a quote and \\ a backslash.
func unescapeMySQL(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

const myIndexesQuery = `SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME, SUB_PART, COALESCE(COLLATION, ''), INDEX_TYPE, IS_VISIBLE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`

// myIndex is an index as its rows of STATISTICS give it.
type myIndex struct {
	ix              Index
	expr            bool
	prefix          []string
	desc, invisible bool
	kind            string
}

// indexes reads mt's indexes but the primary key, whose index gives notes
// only: a prefix or DESC. An index over an expression or a prefix of a
// column is a note and left out; what else an index has that the model
// cannot hold (a type but BTREE, DESC, INVISIBLE) is a note, and the index
// is read without it.
func (r *myReader) indexes(mt *myTable) error {
	var list []*myIndex
	err := r.each(myIndexesQuery, []any{mt.schema, mt.t.Name}, func(rows *sql.Rows) error {
		var (
			name, collation, kind, visible string
			nonUnique                      int
			col, part                      sql.NullString
		)
		if err := rows.Scan(&name, &nonUnique, &col, &part, &collation, &kind, &visible); err != nil {
			return err
		}
		if len(list) == 0 || list[len(list)-1].ix.Name != name {
			list = append(list, &myIndex{ix: Index{Name: name, Unique: nonUnique == 0}, kind: kind})
		}
		mi := list[len(list)-1]
		switch {
		case !col.Valid:
			mi.expr = true
		case part.Valid && kind != "SPATIAL": // a spatial index has a SUB_PART of 32 of its own
			mi.prefix = append(mi.prefix, col.String+"("+part.String+")")
		}
		mi.ix.Columns = append(mi.ix.Columns, col.String)
		mi.desc = mi.desc || collation == "D"
		mi.invisible = mi.invisible || visible == "NO"
		return nil
	})
	if err != nil {
		return err
	}
	for _, mi := range list {
		var has []string
		if mi.kind != "BTREE" {
			has = append(has, "the "+mi.kind+" type")
		}
		if mi.desc {
			has = append(has, "DESC")
		}
		if mi.invisible {
			has = append(has, "the invisible attribute")
		}
		switch {
		case mi.ix.Name == "PRIMARY":
			// The key's columns are keys'; its index is read for notes only.
			if len(mi.prefix) > 0 {
				has = append(has, "the prefix "+strings.Join(mi.prefix, ", "))
			}
			if len(has) > 0 {
				r.note(mt, mi.ix.Name, "the primary key has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
			}
			continue
		case mi.expr:
			r.note(mt, mi.ix.Name, "the index is over an expression, which the model cannot hold; it is not compared")
			continue
		case len(mi.prefix) > 0:
			r.note(mt, mi.ix.Name, "the index has the prefix "+strings.Join(mi.prefix, ", ")+", which the model cannot hold; it is not compared")
			continue
		case len(has) > 0:
			r.note(mt, mi.ix.Name, "the index has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
		}
		mt.t.Indexes = append(mt.t.Indexes, mi.ix)
	}
	return nil
}

// The primary key's and the foreign keys' columns, in order.
const myKeysQuery = `SELECT k.CONSTRAINT_NAME, tc.CONSTRAINT_TYPE, k.COLUMN_NAME,
	COALESCE(k.REFERENCED_TABLE_SCHEMA, ''), COALESCE(k.REFERENCED_TABLE_NAME, ''), COALESCE(k.REFERENCED_COLUMN_NAME, ''),
	COALESCE(rc.DELETE_RULE, ''), COALESCE(rc.UPDATE_RULE, '')
FROM information_schema.KEY_COLUMN_USAGE k
JOIN information_schema.TABLE_CONSTRAINTS tc ON tc.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
	AND tc.TABLE_NAME = k.TABLE_NAME AND tc.CONSTRAINT_NAME = k.CONSTRAINT_NAME
LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS rc ON rc.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
	AND rc.TABLE_NAME = k.TABLE_NAME AND rc.CONSTRAINT_NAME = k.CONSTRAINT_NAME
WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE IN ('PRIMARY KEY', 'FOREIGN KEY')
ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`

// keys reads mt's primary key, always named PRIMARY, and foreign keys. A
// foreign key's ON UPDATE other than no action or RESTRICT is a note.
func (r *myReader) keys(mt *myTable) error {
	var fks []*ForeignKey
	updates := map[string]string{}
	err := r.each(myKeysQuery, []any{mt.schema, mt.t.Name}, func(rows *sql.Rows) error {
		var name, typ, col, refDB, refTable, refCol, onDelete, onUpdate string
		if err := rows.Scan(&name, &typ, &col, &refDB, &refTable, &refCol, &onDelete, &onUpdate); err != nil {
			return err
		}
		if typ == "PRIMARY KEY" {
			mt.t.PrimaryKey = append(mt.t.PrimaryKey, col)
			mt.t.PrimaryKeyName = "PRIMARY"
			return nil
		}
		if len(fks) == 0 || fks[len(fks)-1].Name != name {
			if onDelete == "NO ACTION" {
				onDelete = ""
			}
			fks = append(fks, &ForeignKey{Name: name, OnDelete: onDelete, RefTable: TableRef{Schema: r.schema(refDB), Name: refTable}})
			updates[name] = onUpdate
		}
		fk := fks[len(fks)-1]
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
		return nil
	})
	if err != nil {
		return err
	}
	for _, fk := range fks {
		if u := updates[fk.Name]; u != "NO ACTION" && u != "RESTRICT" {
			r.note(mt, fk.Name, "the foreign key has ON UPDATE "+u+", which the model cannot hold; it is compared without")
		}
		mt.t.ForeignKeys = append(mt.t.ForeignKeys, *fk)
	}
	return nil
}

const myChecksQuery = `SELECT tc.CONSTRAINT_NAME, tc.ENFORCED, cc.CHECK_CLAUSE
FROM information_schema.TABLE_CONSTRAINTS tc
JOIN information_schema.CHECK_CONSTRAINTS cc ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'CHECK'
ORDER BY tc.CONSTRAINT_NAME`

// checks reads mt's checks: an enum check in the model's form, any other as
// MySQL prints it, information schema's escapes read. NOT ENFORCED is a
// note.
func (r *myReader) checks(mt *myTable) error {
	return r.each(myChecksQuery, []any{mt.schema, mt.t.Name}, func(rows *sql.Rows) error {
		var name, enforced, clause string
		if err := rows.Scan(&name, &enforced, &clause); err != nil {
			return err
		}
		if enforced != "YES" {
			r.note(mt, name, "the check is NOT ENFORCED, which the model cannot hold: the rows are not checked")
		}
		expr := unescapeMySQL(clause)
		if col, values, ok := parseEnumCheck(velox.MySQL, expr, mt.t.Columns); ok {
			expr = ddl.EnumCheck(velox.MySQL, col, values)
		}
		mt.t.Checks = append(mt.t.Checks, Check{Name: name, Expr: expr})
		return nil
	})
}
