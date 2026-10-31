package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
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
// and a collation other than the table's. A binary or varbinary column's
// literal default is read from SHOW CREATE TABLE, information_schema
// cutting it at its first NUL byte.
func (r *myReader) columns(mt *myTable) error {
	var shown []int // the columns whose default SHOW CREATE TABLE gives
	err := r.each(myColumnsQuery, []any{mt.schema, mt.t.Name}, func(rows *sql.Rows) error {
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
			if def.Valid && !has("DEFAULT_GENERATED") && mysqlBinaryType(c.Type) {
				shown = append(shown, len(mt.t.Columns))
			}
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
	if err != nil || len(shown) == 0 {
		return err
	}
	var name, create string
	q := "SHOW CREATE TABLE " + ddl.Ident(velox.MySQL, mt.schema) + "." + ddl.Ident(velox.MySQL, mt.t.Name)
	if err := r.each(q, nil, func(rows *sql.Rows) error { return rows.Scan(&name, &create) }); err != nil {
		return err
	}
	for _, i := range shown {
		c := &mt.t.Columns[i]
		lit, ok := myShownDefault(create, c.Name)
		if !ok {
			return fmt.Errorf("SHOW CREATE TABLE %s shows no default for the column %s, though information_schema has one; the session's sql_mode or sql_quote_show_create may print it in a form not read", mt.t.Ref(), c.Name)
		}
		c.Default = mysqlBinaryDefault(c.Type, lit)
	}
	return nil
}

// mysqlDefault is a COLUMN_DEFAULT in the model's spelling. NULL is no
// default. An expression (DEFAULT_GENERATED) is as printed, information
// schema's escapes read, but a lone literal is the model's ('…'): one of a
// UTF-8 character set, _utf8mb4'…', one of _latin1 or _ascii whose every
// byte is ASCII, and on a binary type _binary'…' and 0x… as
// mysqlBinaryDefault reads it. A number, true, false or NULL, which MySQL
// prints bare, is in parentheses again. A literal of a string, enum, set,
// date or time type is quoted again, MySQL printing it bare; a decimal
// loses the zeros its scale pads it with; any other is as printed.
func mysqlDefault(typ string, expr bool, def sql.NullString) string {
	if !def.Valid {
		return ""
	}
	s := def.String
	word := typeWord(typ)
	if expr {
		s = unescapeMySQL(s)
		binary := slices.Contains([]string{"binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob"}, word)
		if binary && len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
			if lit := mysqlBinaryDefault(typ, s); strings.HasPrefix(lit, "'") {
				return "(" + lit + ")"
			}
			return s
		}
		if i := strings.IndexByte(s, '\''); i > 0 {
			if v, end, ok := unquoteAt(s, i, true); ok && end == len(s) && dropIntroducer(s[:i], v, binary) {
				return "(" + ddl.Literal(velox.MySQL, v) + ")"
			}
		} else if i == 0 {
			if v, end, ok := unquoteAt(s, 0, true); ok && end == len(s) {
				return "(" + ddl.Literal(velox.MySQL, v) + ")"
			}
		}
		if literalDefault(velox.MySQL, s) {
			return "(" + s + ")" // (42), (TRUE) and (NULL) print bare
		}
		return s
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

// typeWord is typ's first word: varbinary of varbinary(8).
func typeWord(typ string) string {
	if i := strings.IndexAny(typ, "( "); i >= 0 {
		return typ[:i]
	}
	return typ
}

// dropIntroducer reports whether the character set introducer before a
// literal of the value v drops without changing it: a UTF-8 one, _latin1
// or _ascii when every byte of v is ASCII, and _binary when binary.
func dropIntroducer(introducer, v string, binary bool) bool {
	switch strings.ToLower(introducer) {
	case "_utf8mb4", "_utf8mb3", "_utf8":
		return true
	case "_latin1", "_ascii":
		return asciiOnly(v)
	case "_binary":
		return binary
	}
	return false
}

// asciiOnly reports whether every byte of s is below 0x80.
func asciiOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// mysqlBinaryDefault is a binary or varbinary column's literal default, as
// SHOW CREATE TABLE prints it, in the model's spelling. It decodes the
// literal as mysqlBinaryBytes does; when every byte is printable ASCII
// (0x20–0x7e), it is ddl.Literal(velox.MySQL, …) of them; otherwise it is
// shown as printed.
func mysqlBinaryDefault(typ, shown string) string {
	b, ok := mysqlBinaryBytes(typ, shown)
	if !ok {
		return shown
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return shown
		}
	}
	return ddl.Literal(velox.MySQL, string(b))
}

// mysqlBinaryBytes is the value of a binary or varbinary column's literal
// default: '…' with MySQL's escapes and doubled quotes, the same after
// _binary, 0x…, or X'…', each prefix in either case. On binary(n) the
// trailing 0x00 bytes that pad the value are dropped. False when def is none
// of these.
func mysqlBinaryBytes(typ, def string) ([]byte, bool) {
	var b []byte
	if len(def) > 7 && strings.EqualFold(def[:7], "_binary") && def[7] == '\'' {
		def = def[7:]
	}
	switch {
	case strings.HasPrefix(def, "'"):
		v, end, ok := unquoteAt(def, 0, true)
		if !ok || end != len(def) {
			return nil, false
		}
		b = []byte(v)
	case len(def) > 2 && (def[:2] == "0x" || def[:2] == "0X"):
		var err error
		if b, err = hex.DecodeString(def[2:]); err != nil {
			return nil, false
		}
	case mysqlHexQuoted(def):
		var err error
		if b, err = hex.DecodeString(def[2 : len(def)-1]); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	if typeWord(typ) == "binary" {
		b = bytes.TrimRight(b, "\x00")
	}
	return b, true
}

// mysqlBinaryLiteral reports whether def is no default or written as a
// literal mysqlBinaryBytes reads: '…', 0x…, X'…' or _binary'…'. An
// expression is not one.
func mysqlBinaryLiteral(def string) bool {
	return def == "" || def[0] == '\'' || len(def) > 1 && (def[:2] == "0x" || def[:2] == "0X") || mysqlHexQuoted(def) ||
		len(def) > 7 && strings.EqualFold(def[:7], "_binary") && def[7] == '\''
}

// mysqlHexQuoted reports whether def is written X'…' or x'…', a hexadecimal
// literal in standard SQL's spelling.
func mysqlHexQuoted(def string) bool {
	return len(def) >= 3 && (def[0] == 'X' || def[0] == 'x') && def[1] == '\'' && def[len(def)-1] == '\'' &&
		!strings.Contains(def[2:len(def)-1], "'")
}

// mysqlBinaryType reports whether typ is binary or varbinary.
func mysqlBinaryType(typ string) bool {
	w := typeWord(typ)
	return w == "binary" || w == "varbinary"
}

// myShownDefault is the literal of column col's DEFAULT in create, SHOW
// CREATE TABLE's text: a quoted string as MySQL prints it, or 0x…; false
// when the column has no DEFAULT. It reads backquoted names and quoted
// literals, so a name or literal holding " DEFAULT " is not misread. The
// name is backquoted, in double quotes under ANSI_QUOTES, or bare under
// sql_quote_show_create=OFF.
func myShownDefault(create, col string) (string, bool) {
	// The table's name, which may hold a parenthesis, up to the definitions.
	i := 0
	for {
		tok, next := myToken(create, i)
		if tok == "" {
			return "", false
		}
		if tok[0] == '(' {
			i = next - len(tok) + 1
			break
		}
		i = next
	}
	// The definitions, each a list of tokens, up to the closing parenthesis.
	var def []string
	for i < len(create) {
		tok, next := myToken(create, i)
		switch tok {
		case "", ")", ",":
			// The name in backquotes, in double quotes, or bare when it
			// holds nothing a bare name could be misread by.
			if len(def) > 0 && (def[0] == ddl.Ident(velox.MySQL, col) || def[0] == ddl.Ident(velox.Postgres, col) ||
				def[0] == col && !strings.ContainsAny(col, "'`\" ,()\t\r\n")) {
				for k := 1; k+1 < len(def); k++ {
					if strings.EqualFold(def[k], "DEFAULT") {
						return def[k+1], true
					}
				}
			}
			if tok != "," {
				return "", false
			}
			def = def[:0]
		default:
			def = append(def, tok)
		}
		i = next
	}
	return "", false
}

// myToken is the token of SHOW CREATE TABLE's text at s[i], white space
// skipped, and the index after it: a backquoted name, a quoted literal, a
// parenthesised group, "," or ")", or a word; "" at the end.
func myToken(s string, i int) (string, int) {
	for i < len(s) && strings.IndexByte(" \t\r\n", s[i]) >= 0 {
		i++
	}
	if i == len(s) {
		return "", i
	}
	start := i
	switch s[i] {
	case '`', '"': // a name: in backquotes, or in double quotes under ANSI_QUOTES
		end := stringEnd(s, i, false)
		return s[start:end], end
	case '\'':
		end := stringEnd(s, i, true)
		return s[start:end], end
	case ',', ')':
		return s[i : i+1], i + 1
	case '(':
		depth := 0
		for i < len(s) {
			switch s[i] {
			case '\'', '"', '`':
				i = stringEnd(s, i, s[i] == '\'')
				continue
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return s[start : i+1], i + 1
				}
			}
			i++
		}
		return s[start:], i
	}
	for i < len(s) && strings.IndexByte(" \t\r\n,()'\"`", s[i]) < 0 {
		i++
	}
	if i > start && i < len(s) && s[i] == '\'' { // _binary'…', b'…'
		i = stringEnd(s, i, true)
	}
	return s[start:i], i
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
