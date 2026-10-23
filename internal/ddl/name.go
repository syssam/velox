package ddl

import (
	"fmt"
	"hash/fnv"
	"unicode/utf8"
)

// MaxName is the length in bytes of the longest database name: PostgreSQL
// truncates a name at 63 bytes, and MySQL refuses one over 64.
const MaxName = 63

// Cap returns name when it is MaxName bytes or less; otherwise its first 54
// bytes, cut back to a UTF-8 boundary, then "_", then the 8 lower-case hex
// digits of the FNV-1a 32 hash of the whole name.
func Cap(name string) string {
	if len(name) <= MaxName {
		return name
	}
	i := MaxName - 9
	for i > 0 && !utf8.RuneStart(name[i]) {
		i--
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%s_%08x", name[:i], h.Sum32())
}

// NotNull is the name of the check that stands for NOT NULL on column while
// PostgreSQL validates it.
func NotNull(table, column string) string { return Cap(table + "_" + column + "_not_null") }

// Rebuild is the name of the table SQLite's rebuild of table copies into.
func Rebuild(table string) string { return Cap("velox_new_" + table) }

// OldEnum is the name PostgreSQL's enum type typ takes while it is swapped
// for a new one.
func OldEnum(typ string) string { return Cap(typ + "_velox_old") }
