package gen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
	"github.com/syssam/velox2/migrate"
)

// MySQL's limits.
const (
	mysqlKeyBytes      = 3072
	mysqlRowBytes      = 65535
	mysqlEnumChars     = 255
	mysqlColumnComment = 1024
	mysqlTableComment  = 2048
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

// check records what tb, the table of e, holds that the dialect cannot: the
// table's, then each column's, then each index's, then the row's. at is the
// element that makes each of tb's indexes.
func (t *tabler) check(e *Entity, tb *migrate.Table, at []loc) {
	fail := func(l loc, format string, args ...any) {
		t.errs = append(t.errs, &Error{Pos: l.pos, Schema: e.Name, Where: l.where, Reason: fmt.Sprintf(format, args...)})
	}
	table := loc{pos: tb.Pos}
	if t.d == velox.SQLite && e.TableSchema != "" {
		fail(table, "schema.TableIn puts the table in database schema %s, which SQLite does not have", e.TableSchema)
	}
	if n := utf8.RuneCountInString(tb.Comment); t.d == velox.MySQL && n > mysqlTableComment {
		fail(table, "the table comment is %d characters on MySQL, over its %d", n, mysqlTableComment)
	}
	for i, f := range e.Fields {
		t.checkColumn(i, f, &tb.Columns[i], fail)
	}
	if t.d != velox.MySQL {
		return
	}
	for i, ix := range tb.Indexes {
		if ix.Where != "" {
			fail(at[i], "MySQL has no partial indexes (Where); drop Where or use a separate MySQL schema")
		}
		n := 0
		for _, c := range ix.Columns {
			typ := columnType(tb, c)
			if mysqlExprDefault[typ] {
				fail(at[i], "column %s is %s on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType", c, typ)
			}
			n += mysqlKeySize(typ)
		}
		if n > mysqlKeyBytes {
			fail(at[i], "the key is %d bytes on MySQL, over its %d", n, mysqlKeyBytes)
		}
	}
	t.checkRow(e, tb, fail)
}

// checkColumn records what the column c of f, the i-th field, holds that the
// dialect cannot.
func (t *tabler) checkColumn(i int, f *Field, c *migrate.Column, fail func(loc, string, ...any)) {
	at := fieldLoc(f)
	st := f.SchemaType[t.d.String()]
	if f.Type == "other" && st == "" {
		fail(at, "an Other field needs a SchemaType for %s", dialectName(t.d))
	}
	if i == 0 && integer(f.Type) && st != "" && !integerType(t.d, ddl.NormalizeType(t.d, st)) {
		fail(at, "the ID is an integer, but its SchemaType %s is not an integer type on %s", st, dialectName(t.d))
	}
	switch t.d {
	case velox.Postgres:
		if f.Type == "enum" && st != "" && !ddl.StringType(t.d, st) && !pgTypeName(st) {
			fail(at, "its PostgreSQL SchemaType %s is neither a string type nor an enum type name", st)
		}
		if hasNUL(f) {
			fail(at, "PostgreSQL text cannot hold a NUL byte")
		}
	case velox.MySQL:
		if f.Type == "enum" {
			for j, v := range f.Enums {
				if n := utf8.RuneCountInString(v.Value); n > mysqlEnumChars {
					fail(at, "enum value %d is %d characters on MySQL, over its %d", j, n, mysqlEnumChars)
				}
			}
			if st != "" && !ddl.StringType(t.d, st) {
				fail(at, "its MySQL SchemaType %s is not a string type; drop it for an enum(…) column, or give a string type for a check", st)
			}
		}
		if n := utf8.RuneCountInString(c.Comment); n > mysqlColumnComment {
			if f.Implicit != "" {
				at = loc{edgeAt(f.Implicit), c.Pos} // the comment is its edge's
			}
			fail(at, "its comment is %d characters on MySQL, over its %d", n, mysqlColumnComment)
		}
	}
}

// checkRow records a MySQL row whose varchar and varbinary columns need more
// than MySQL's 65535 bytes, counting 4 bytes per varchar character and each
// column's 1 or 2 length bytes.
func (t *tabler) checkRow(e *Entity, tb *migrate.Table, fail func(loc, string, ...any)) {
	total := 0
	var lines []string
	for i, c := range tb.Columns {
		n, ok := sizeOf(c.Type, "varbinary")
		if chars, isVarchar := sizeOf(c.Type, "varchar"); isVarchar {
			n, ok = 4*chars, true
		}
		if !ok {
			continue
		}
		if n <= 255 {
			n++
		} else {
			n += 2
		}
		total += n
		lines = append(lines, fmt.Sprintf("  %s: %s: %s, %d bytes", c.Pos, fieldAt(e.Fields[i].Name), c.Type, n))
	}
	if total <= mysqlRowBytes {
		return
	}
	file := tb.Pos
	if i := strings.LastIndexByte(file, ':'); i >= 0 {
		file = file[:i]
	}
	fail(loc{pos: file}, "MySQL rows hold at most %d bytes in varchar and varbinary columns; these need %d:\n%s\n  use Text() or a smaller MaxLen",
		mysqlRowBytes, total, strings.Join(lines, "\n"))
}

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

// pgTypeName reports whether st names an enum type of its own on PostgreSQL:
// a lower-case identifier that is not a built-in type.
func pgTypeName(st string) bool { return pgIdent.MatchString(st) && !pgBuiltin[st] }

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
