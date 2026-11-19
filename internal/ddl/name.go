package ddl

import (
	"fmt"
	"hash/fnv"
	"unicode/utf8"

	"github.com/syssam/velox"
)

// MaxName is the length in bytes of the longest database name: PostgreSQL
// truncates a name at 63 bytes, and MySQL refuses one over 64.
const MaxName = 63

// Cap caps the names velox makes and neither ent nor PostgreSQL does: checks,
// enum types and velox's own foreign-key indexes. Index and foreign-key
// names are capped with Symbol, and PostgreSQL's key and sequence names are
// PostgresName's.
//
// Cap returns name when it is MaxName bytes or less; otherwise its first 54
// bytes, cut back to a UTF-8 boundary, then "_", then the 8 lower-case hex
// digits of the FNV-1a 32 hash of the whole name.
func Cap(name string) string {
	if len(name) <= MaxName {
		return name
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%s_%08x", Clip(name, MaxName-9), h.Sum32())
}

// Symbol is veloxe's cap on an index or foreign-key name
// (dialect/sql/schema/atlas.go, symbol): name when it is at most limit
// bytes, else name[:limit-33], cut back to a UTF-8 boundary, then "_", then
// the 32 lower-case hex digits of the 128-bit FNV-1 hash of name. limit is
// 63 on PostgreSQL and 64 on MySQL and SQLite. ent caps the same way with an
// MD5; velox takes veloxe's, so that a database veloxe migrated keeps its
// names (decision 0083).
func Symbol(d velox.Dialect, name string) string {
	limit := 64
	if d == velox.Postgres {
		limit = 63
	}
	if len(name) <= limit {
		return name
	}
	h := fnv.New128()
	h.Write([]byte(name))
	return fmt.Sprintf("%s_%x", Clip(name, limit-33), h.Sum(nil))
}

// PostgresName is the name PostgreSQL gives an object it names itself, as its
// makeObjectName does (src/backend/commands/indexcmds.c): name1, "_" and name2
// when name2 is not "", "_", label. When that is over 63 bytes, it shortens
// the longer of name1 and name2 by one byte at a time (name2 on a tie) until
// it fits, then cuts each back to a character boundary (pg_mbcliplen).
func PostgresName(name1, name2, label string) string {
	avail := MaxName - len(label) - 1
	if name2 != "" {
		avail--
	}
	n1, n2 := len(name1), len(name2)
	for n1+n2 > avail {
		if n1 > n2 {
			n1--
		} else {
			n2--
		}
	}
	name := Clip(name1, n1)
	if name2 != "" {
		name += "_" + Clip(name2, n2)
	}
	return name + "_" + label
}

// Clip is the first n bytes of s, or fewer, cut back to a UTF-8 boundary:
// how PostgreSQL truncates a name over MaxName bytes, as Clip(name, MaxName).
func Clip(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NotNull is the name of the check that stands for NOT NULL on column while
// PostgreSQL validates it.
func NotNull(table, column string) string { return Cap(table + "_" + column + "_not_null") }

// Rebuild is the name of the table SQLite's rebuild of table copies into.
func Rebuild(table string) string { return Cap("velox_new_" + table) }

// OldEnum is the name PostgreSQL's enum type typ takes while it is swapped
// for a new one.
func OldEnum(typ string) string { return Cap(typ + "_velox_old") }

// NewIndex is the name PostgreSQL's index name takes while it is built
// again beside the one it replaces.
func NewIndex(name string) string { return Cap(name + "_velox_new") }

// OldSequence is the name a serial's sequence seq takes while an identity
// takes its place.
func OldSequence(seq string) string { return Cap(seq + "_velox_old") }
