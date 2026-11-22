package migrate

import (
	"context"
	"database/sql"
	"strings"

	"github.com/syssam/velox"
)

// For the tests of package migrate_test.
var (
	ApplyChanges = applyChanges
	Clone        = (*Schema).clone
	PgUsing      = pgUsing
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
	Block          bool
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
	s, err := open(ctx, db, Config{Dialect: velox.SQLite, Table: defaultTable}, 0)
	if err != nil {
		return err
	}
	s.close()
	return nil
}

// WithUnitDone makes Up call f after each unit of a file run statement by
// statement, before its count is recorded.
func WithUnitDone(f func(version uint64, end int)) Option {
	return Option{func(o *options) { o.unitDone = f }}
}

// LostMessage is lostMessage.
var LostMessage = lostMessage

// A Note is a note, as tests write and read it.
type Note struct {
	Table           TableRef
	Object, Message string
	Baseline        bool
}

// notesOf is notes as notes.
func notesOf(notes []Note) []note {
	var ns []note
	for _, n := range notes {
		ns = append(ns, note{table: n.Table, object: n.Object, message: n.Message, baseline: n.Baseline})
	}
	return ns
}

// DriftOf is drift.
func DriftOf(inspected, snapshot *Schema, notes ...Note) (*DriftReport, error) {
	return drift(inspected, notesOf(notes), snapshot)
}

// AdoptOf is adopt.
func AdoptOf(inspected, want *Schema, notes ...Note) (*Schema, []Warning, error) {
	return adopt(inspected, notesOf(notes), want)
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
		out = append(out, Note{Table: n.table, Object: n.object, Message: n.message, Baseline: n.baseline})
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

// MySQLBinaryDefault is mysqlBinaryDefault.
var MySQLBinaryDefault = mysqlBinaryDefault

// MyShownDefault is myShownDefault.
var MyShownDefault = myShownDefault

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

// WithoutTxControlCheck makes Up skip its refusal, before the run, of a
// transaction file's BEGIN, COMMIT or ROLLBACK.
func WithoutTxControlCheck() Option {
	return Option{func(o *options) { o.noTxCheck = true }}
}

// SetBeforeBaselining makes Up call f between its read of the history and
// its baseline, and returns the function that undoes it.
func SetBeforeBaselining(f func()) (restore func()) {
	beforeBaselining = f
	return func() { beforeBaselining = nil }
}

// recorder is a querier that records each query it runs.
type recorder struct {
	q       querier
	queries []string
}

func (r *recorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries = append(r.queries, query)
	return r.q.QueryContext(ctx, query, args...)
}

// InspectMySQLQueries is inspectMySQL on db, and the queries it ran.
func InspectMySQLQueries(ctx context.Context, db *sql.DB, tables []TableRef) (*Schema, []string, error) {
	r := &recorder{q: db}
	s, _, err := inspectMySQL(ctx, r, tables)
	return s, r.queries, err
}

// The kinds of change, which only the tests name: an application reads a Change
// through its Kind, Target and String.
type (
	CreateTable       = createTable
	AddCheck          = addCheck
	AddColumn         = addColumn
	AddForeignKey     = addForeignKey
	AddIndex          = addIndex
	AlterColumn       = alterColumn
	AlterEnum         = alterEnum
	ColumnChange      = columnMask
	CreateEnum        = createEnum
	DropCheck         = dropCheck
	DropColumn        = dropColumn
	DropEnum          = dropEnum
	DropForeignKey    = dropForeignKey
	DropIndex         = dropIndex
	DropTable         = dropTable
	RenameColumn      = renameColumn
	RenameConstraint  = renameConstraint
	RenameEnum        = renameEnum
	RenameIndex       = renameIndex
	RenameSequence    = renameSequence
	RenameTable       = renameTable
	SetTableCollation = setTableCollation
	SetTableComment   = setTableComment
)

// The bits of a columnMask.
const (
	ColumnComment  = columnComment
	ColumnDefault  = columnDefault
	ColumnIdentity = columnIdentity
	ColumnNullable = columnNullable
	ColumnType     = columnType
)

// MarkUnsure is markUnsure.
var MarkUnsure = markUnsure

// AtomicUnit is atomicUnit.
var AtomicUnit = atomicUnit

// SetEveryTxBlock sets everyTxBlock, and returns the function that undoes it.
func SetEveryTxBlock() (restore func()) {
	everyTxBlock = true
	return func() { everyTxBlock = false }
}
