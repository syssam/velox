package migrate

import (
	"context"
	"database/sql"
	"strings"

	"github.com/syssam/velox2"
)

// For the tests of package migrate_test.
var (
	ApplyChanges = applyChanges
	Clone        = (*Schema).clone
)

// WriteScript is writeScript, for the goldens.
func WriteScript(s Script) string {
	var b strings.Builder
	writeScript(&b, s)
	return b.String()
}

// A Piece is a piece of split.
type Piece struct {
	SQL, Directive string
	Line           int
}

// Split is split.
func Split(d velox.Dialect, src string) ([]Piece, error) {
	ps, err := split(d, src)
	var out []Piece
	for _, p := range ps {
		out = append(out, Piece(p))
	}
	return out, err
}

// FileBackslash is f.backslash.
func FileBackslash(f File) bool { return f.backslash }

// FileGenerated is f.generated.
func FileGenerated(f File) bool { return f.generated }

// FileLines is f.lines.
func FileLines(f File) []int { return f.lines }

// FileDquote is f.dquote.
func FileDquote(f File) bool { return f.dquote }

// DefaultName is defaultName.
var DefaultName = defaultName

// Generator is generator.
var Generator = generator

// ConfigJSON is the velox.json Add writes for c.
func ConfigJSON(c Config) []byte { return c.marshal() }

// LockName is lockName.
var LockName = lockName

// LockKey is lockKey.
var LockKey = lockKey

// CheckVersion is checkVersion.
var CheckVersion = checkVersion

// WaitMillis and WaitSeconds are waitMillis and waitSeconds.
var (
	WaitMillis  = waitMillis
	WaitSeconds = waitSeconds
)

// SessionClose opens a SQLite session on db and closes it.
func SessionClose(ctx context.Context, db *sql.DB) error {
	s, err := open(ctx, db, Config{Dialect: velox.SQLite, Table: defaultTable})
	if err != nil {
		return err
	}
	s.close()
	return nil
}

// SetUnitDone sets the hook called after each unit of a file run statement
// by statement, and returns what sets it back.
func SetUnitDone(f func(version uint64, end int)) (restore func()) {
	was := unitDone
	unitDone = f
	return func() { unitDone = was }
}

// LostMessage is lostMessage.
var LostMessage = lostMessage

// A Note is a note, as tests write and read it.
type Note struct {
	Table           TableRef
	Object, Message string
}

// DriftOf is drift.
func DriftOf(inspected, snapshot *Schema, notes ...Note) (*DriftReport, error) {
	var ns []note
	for _, n := range notes {
		ns = append(ns, note{table: n.Table, object: n.Object, message: n.Message})
	}
	return drift(inspected, ns, snapshot)
}

// NormalizePredicate is normalizePredicate.
func NormalizePredicate(d velox.Dialect, expr string) string { return normalizePredicate(d, expr) }

// SamePredicate is samePredicate.
var SamePredicate = samePredicate

// InspectNotes is inspect, its notes as Notes.
func InspectNotes(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, []Note, error) {
	s, ns, err := inspect(ctx, db, d, tables)
	var out []Note
	for _, n := range ns {
		out = append(out, Note{Table: n.table, Object: n.object, Message: n.message})
	}
	return s, out, err
}

// NormalizeDefault is normalizeDefault.
func NormalizeDefault(d velox.Dialect, s string) string { return normalizeDefault(d, s) }

// UnescapeMySQL is unescapeMySQL.
var UnescapeMySQL = unescapeMySQL

// MySQLDefault is mysqlDefault, def never NULL.
func MySQLDefault(typ string, expr bool, def string) string {
	return mysqlDefault(typ, expr, sql.NullString{String: def, Valid: true})
}

// ParseCreateTable is parseCreateTable, each key of the text as a
// ForeignKey: its name, its columns, and its referenced table and columns
// as written.
func ParseCreateTable(sql string) (autoincrement bool, checks []Check, foreignKeys []ForeignKey, ok bool) {
	ct, ok := parseCreateTable(sql)
	for _, k := range ct.foreignKeys {
		foreignKeys = append(foreignKeys, ForeignKey{Name: k.name, Columns: k.columns, RefTable: TableRef{Name: k.refTable}, RefColumns: k.refColumns})
	}
	return ct.autoincrement, ct.checks, foreignKeys, ok
}

// Words is words.
var Words = words
