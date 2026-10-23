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

// SessionClose opens a SQLite session on db and closes it, discarding its
// connection when discard is set, as a failed clean-up does.
func SessionClose(ctx context.Context, db *sql.DB, discard bool) error {
	s, err := open(ctx, db, Config{Dialect: velox.SQLite, Table: defaultTable})
	if err != nil {
		return err
	}
	s.broken = discard
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
