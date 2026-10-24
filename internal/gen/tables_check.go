package gen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
	"github.com/syssam/velox2/internal/gen/loadjson"
	"github.com/syssam/velox2/migrate"
)

// MySQL's limits, and PostgreSQL's.
const (
	mysqlKeyBytes      = 3072
	mysqlRowLimit      = 65535
	mysqlEnumChars     = 255
	mysqlColumnComment = 1024
	mysqlTableComment  = 2048
	pgEnumBytes        = 63 // an enum label's
)

// dialectName is d as its users write it.
func dialectName(d velox.Dialect) string {
	switch d {
	case velox.Postgres:
		return "PostgreSQL"
	case velox.MySQL:
		return "MySQL"
	}
	return "SQLite"
}

// The ranks of a schema's elements, in the order Tables reports their errors.
const (
	rankTable = iota
	rankField
	rankEdge
	rankIndex
	rankRow
)

// place is where an error goes: the schema e, the rank and the i-th element
// of that rank, and the element's loc.
type place struct {
	e       *Entity
	rank, i int
	loc     loc
}

// refused is an error and its place, which orders it.
type refused struct {
	at  place
	err *Error
}

// edgePlace is the place of e's edge ed.
func edgePlace(e *Entity, ed *Edge) place {
	return place{e, rankEdge, slices.Index(e.Edges, ed), edgeLoc(ed)}
}

// fail records an error at p.
func (t *tabler) fail(p place, format string, args ...any) {
	t.errs = append(t.errs, refused{p, &Error{Pos: p.loc.pos, Schema: p.e.Name, Where: p.loc.where, Reason: fmt.Sprintf(format, args...)}})
}

// check records what tb, the table of e, holds that the dialect cannot. at is
// the place of each of tb's indexes. Tables orders the errors of a schema:
// the table's, then each field's (its column's, then its Unique index's),
// then each edge's (its foreign key's column and index, or its join table's),
// then each declared index's, then the row's.
func (t *tabler) check(e *Entity, tb *migrate.Table, at []place) {
	table := place{e, rankTable, 0, loc{pos: tb.Pos}}
	if t.d == velox.SQLite && e.TableSchema != "" {
		t.fail(table, "schema.TableIn puts the table in database schema %s, which SQLite does not have", e.TableSchema)
	}
	if n := utf8.RuneCountInString(tb.Comment); t.d == velox.MySQL && n > mysqlTableComment {
		t.fail(table, "the table comment is %d characters on MySQL, over its %d", n, mysqlTableComment)
	}
	for i, f := range e.Fields {
		t.checkColumn(e, i, f, &tb.Columns[i])
	}
	if t.d != velox.MySQL {
		return
	}
	t.checkKey(tb, tb.PrimaryKey, place{e, rankField, 0, fieldLoc(e.Fields[0])})
	for i, ix := range tb.Indexes {
		if ix.Where != "" {
			t.fail(at[i], "MySQL has no partial indexes (Where); drop Where or use a separate MySQL schema")
		}
		t.checkKey(tb, ix.Columns, at[i])
	}
	file := tb.Pos
	if i := strings.LastIndexByte(file, ':'); i >= 0 {
		file = file[:i]
	}
	t.checkRow(tb, place{e, rankRow, 0, loc{pos: file}}, func(i int) string { return fieldAt(e.Fields[i].Name) })
}

// checkJoin records what the join table tb holds that MySQL cannot, at the
// place of its assoc edge: its key and its row.
func (t *tabler) checkJoin(tb *migrate.Table, at place) {
	if t.d != velox.MySQL {
		return
	}
	t.checkKey(tb, tb.PrimaryKey, at)
	t.checkRow(tb, at, func(i int) string { return fmt.Sprintf("column %q", tb.Columns[i].Name) })
}

// checkKey records a MySQL key over tb's columns cols that holds a column
// an index covers only by a prefix, or that is over 3072 bytes.
func (t *tabler) checkKey(tb *migrate.Table, cols []string, at place) {
	n := 0
	for _, c := range cols {
		typ := columnType(tb, c)
		if mysqlExprDefault[typ] {
			t.fail(at, "column %s is %s on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType", c, typ)
		}
		n += mysqlKeySize(typ)
	}
	if n > mysqlKeyBytes {
		t.fail(at, "the key is %d bytes on MySQL, over its %d", n, mysqlKeyBytes)
	}
}

// checkColumn records what the column c of e's field f, the i-th, holds that
// the dialect cannot.
func (t *tabler) checkColumn(e *Entity, i int, f *Field, c *migrate.Column) {
	at := place{e, rankField, i, fieldLoc(f)}
	st := f.SchemaType[t.d.String()]
	if f.Type == "other" && st == "" {
		t.fail(at, "an Other field needs a SchemaType for %s", dialectName(t.d))
	}
	if i == 0 && integer(f.Type) && st != "" && !integerType(t.d, ddl.NormalizeType(t.d, st)) {
		t.fail(at, "the ID is an integer, but its SchemaType %s is not an integer type on %s; Tables makes the column an identity itself", st, dialectName(t.d))
	}
	switch t.d {
	case velox.Postgres:
		if f.Type == "enum" && st != "" && !ddl.StringType(t.d, st) {
			switch {
			case !pgIdent.MatchString(st):
				t.fail(at, "SchemaType %s is not a lower-case identifier", st)
			case pgBuiltin[st]:
				t.fail(at, "SchemaType %s is a built-in PostgreSQL type, not an enum type name", st)
			}
		}
		if _, native := enumType(e, f); f.Type == "enum" && native {
			for _, v := range f.Enums {
				if n := len(v.Value); n > pgEnumBytes {
					t.fail(at, "enum value %s is %d bytes, over PostgreSQL's %d", enumValueName(v), n, pgEnumBytes)
				}
			}
		}
		if hasNUL(f) {
			t.fail(at, "PostgreSQL text cannot hold a NUL byte")
		}
	case velox.MySQL:
		if f.Type == "enum" {
			for _, v := range f.Enums {
				if n := utf8.RuneCountInString(v.Value); n > mysqlEnumChars {
					t.fail(at, "enum value %s is %d characters on MySQL, over its %d", enumValueName(v), n, mysqlEnumChars)
				}
			}
			if st != "" && !ddl.StringType(t.d, st) {
				t.fail(at, "its MySQL SchemaType %s is not a string type; drop it to get an enum(…) column, or give a string type to get a CHECK constraint", st)
			}
		}
		if n := utf8.RuneCountInString(c.Comment); n > mysqlColumnComment {
			if fk, ok := t.fks[[3]string{e.TableSchema, e.Table, f.Column}]; ok && f.Implicit != "" {
				at = edgePlace(fk.Owner, fk.Edge) // the comment is its edge's
			}
			t.fail(at, "its comment is %d characters on MySQL, over its %d", n, mysqlColumnComment)
		}
	}
}

// enumValueName names the enum value v in an error: its Go name when
// NamedValues gave one, else its value, quoted and cut to 32 characters.
func enumValueName(v loadjson.Enum) string {
	if v.Name != v.Value {
		return v.Name
	}
	if r := []rune(v.Value); len(r) > 32 {
		return strconv.Quote(string(r[:32]) + "…")
	}
	return strconv.Quote(v.Value)
}

// checkRow records a MySQL row of tb that needs more than MySQL's 65535
// bytes: every column as mysqlRowBytes counts it, and the NULL bitmap. The
// error lists the varchar and varbinary columns, which Text() or a smaller
// MaxLen shrinks. name names the i-th column's element.
func (t *tabler) checkRow(tb *migrate.Table, at place, name func(i int) string) {
	total, other, nulls := 0, 0, 0
	var lines []string
	for i, c := range tb.Columns {
		n, _ := mysqlRowBytes(c.Type)
		total += n
		if c.Nullable {
			nulls++
		}
		_, varchar := sizeOf(c.Type, "varchar")
		_, varbinary := sizeOf(c.Type, "varbinary")
		if varchar || varbinary {
			lines = append(lines, fmt.Sprintf("  %s: %s: %s, %d bytes", c.Pos, name(i), c.Type, n))
		} else {
			other += n
		}
	}
	bitmap := (nulls + 7) / 8
	total += bitmap
	if total > mysqlRowLimit {
		t.fail(at, "MySQL rows hold at most %d bytes; these need %d:\n%s\n  the other columns and the NULL bitmap: %d bytes\n  use Text() or a smaller MaxLen",
			mysqlRowLimit, total, strings.Join(lines, "\n"), other+bitmap)
	}
}

// mysqlRowBytes is what a column of MySQL type t (normalised) counts toward
// the 65535-byte row limit, as MySQL 8.4 counts it with utf8mb4; ok is false
// for a type it does not know, which counts 0.
func mysqlRowBytes(t string) (n int, ok bool) {
	if n, ok := mysqlRowFixed[strings.TrimSuffix(t, " unsigned")]; ok {
		return n, true
	}
	if n, ok := sizeOf(t, "varchar"); ok {
		return lengthBytes(4 * n), true
	}
	if n, ok := sizeOf(t, "varbinary"); ok {
		return lengthBytes(n), true
	}
	if n, ok := sizeOf(t, "char"); ok {
		return 4 * n, true
	}
	if n, ok := sizeOf(t, "binary"); ok {
		return n, true
	}
	if n, ok := sizeOf(t, "bit"); ok && n >= 1 && n <= 64 {
		return (n + 7) / 8, true
	}
	for base, size := range mysqlRowTimes {
		if p, ok := sizeOf(t, base); ok && p <= 6 {
			return size + (p+1)/2, true
		}
	}
	if vs, ok := ddl.ParseMySQLEnum(t); ok {
		if len(vs) > 255 {
			return 2, true
		}
		return 1, true
	}
	if m, ok := sizeOf(t, "decimal"); ok && m <= 65 {
		return decimalBytes(m), true
	}
	if s, ok := strings.CutPrefix(t, "decimal("); ok && strings.HasSuffix(s, ")") {
		m, d, _ := strings.Cut(strings.TrimSuffix(s, ")"), ",")
		mi, err1 := strconv.Atoi(m)
		di, err2 := strconv.Atoi(d)
		if err1 == nil && err2 == nil && di >= 0 && di <= mi && mi <= 65 {
			return decimalBytes(mi-di) + decimalBytes(di), true
		}
	}
	return 0, false
}

// lengthBytes is n bytes of a varchar or varbinary and its 1 or 2 length
// bytes.
func lengthBytes(n int) int {
	if n <= 255 {
		return n + 1
	}
	return n + 2
}

// decimalBytes is what n digits of a decimal take: 4 bytes per 9, and the
// rest by mysqlDigits.
func decimalBytes(n int) int { return n/9*4 + mysqlDigits[n%9] }

// mysqlDigits are the bytes of 0 to 8 leftover decimal digits.
var mysqlDigits = [9]int{0, 1, 1, 2, 2, 3, 3, 4, 4}

// mysqlRowFixed are the row bytes of MySQL's types of a fixed size, unsigned
// or not, and of the text, blob and json types' in-row pointers.
var mysqlRowFixed = map[string]int{
	"tinyint": 1, "tinyint(1)": 1, "smallint": 2, "mediumint": 3, "int": 4, "integer": 4, "bigint": 8,
	"float": 4, "double": 8, "date": 3, "datetime": 5, "timestamp": 4, "time": 3, "year": 1,
	"tinytext": 9, "tinyblob": 9, "text": 10, "blob": 10, "mediumtext": 11, "mediumblob": 11,
	"longtext": 12, "longblob": 12, "json": 12,
	"char": 4, "binary": 1, "bit": 1, "decimal": 5, // char(1), binary(1), bit(1), decimal(10,0)
}

// mysqlRowTimes are the row bytes of datetime(p), timestamp(p) and time(p)
// for p 0; a precision p adds (p+1)/2 bytes.
var mysqlRowTimes = map[string]int{"datetime": 5, "timestamp": 4, "time": 3}

// columnType is the type of tb's column name.
func columnType(tb *migrate.Table, name string) string {
	for _, c := range tb.Columns {
		if c.Name == name {
			return c.Type
		}
	}
	return ""
}

// mysqlFixedKey are the bytes a MySQL index key takes for a column of a
// fixed-size type, unsigned or not.
var mysqlFixedKey = map[string]int{
	"tinyint": 1, "tinyint(1)": 1, "smallint": 2, "int": 4, "bigint": 8,
	"float": 4, "double": 8, "datetime(6)": 8,
}

// mysqlKeySize is the bytes a MySQL index key takes for a column of type typ:
// 4 per varchar or char character, a varbinary's length, the fixed size of
// the types of the field types, 2 for an enum, and nothing for another type.
func mysqlKeySize(typ string) int {
	if n, ok := sizeOf(typ, "varchar"); ok {
		return 4 * n
	}
	if n, ok := sizeOf(typ, "char"); ok {
		return 4 * n
	}
	if n, ok := sizeOf(typ, "varbinary"); ok {
		return n
	}
	if strings.HasPrefix(typ, "enum(") {
		return 2
	}
	return mysqlFixedKey[strings.TrimSuffix(typ, " unsigned")]
}

// sizeOf is n of the type base(n).
func sizeOf(typ, base string) (int, bool) {
	s, ok := strings.CutPrefix(typ, base+"(")
	if !ok {
		return 0, false
	}
	s, ok = strings.CutSuffix(s, ")")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0
}

// integerType reports whether typ, normalised, is an integer type of d, which
// an identity or auto-increment column needs; on SQLite, any type with
// integer affinity.
func integerType(d velox.Dialect, typ string) bool {
	switch d {
	case velox.Postgres:
		return typ == "smallint" || typ == "integer" || typ == "bigint"
	case velox.MySQL:
		typ = strings.TrimSuffix(typ, " unsigned")
		return mysqlInts[typ] || typ == "tinyint(1)"
	}
	return strings.Contains(typ, "int")
}

// mysqlInts are MySQL's integer types.
var mysqlInts = map[string]bool{"tinyint": true, "smallint": true, "mediumint": true, "int": true, "integer": true, "bigint": true}

var pgIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// pgBuiltin are the one-word PostgreSQL types of the field types, and the
// aliases ddl.NormalizeType maps.
var pgBuiltin = map[string]bool{
	"boolean": true, "smallint": true, "integer": true, "bigint": true, "numeric": true, "real": true,
	"text": true, "character": true, "bytea": true, "uuid": true, "jsonb": true,
	"varchar": true, "char": true, "int4": true, "int8": true, "float8": true, "bool": true,
	"timestamptz": true, "decimal": true, "int": true,
}

// hasNUL reports whether f's string or enum default, or one of its enum
// values, holds a NUL byte.
func hasNUL(f *Field) bool {
	for _, v := range f.Enums {
		if strings.ContainsRune(v.Value, 0) {
			return true
		}
	}
	var s string
	return (f.Type == "string" || f.Type == "enum") && json.Unmarshal(f.Default, &s) == nil && strings.ContainsRune(s, 0)
}
