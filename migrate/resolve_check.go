package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/syssam/velox"
)

// WithForce makes Resolve record ResolveApplied or ResolveAdopted though the
// database differs from what the directory says it holds, or though that
// cannot be read. The differences come to WithNotes, and the row's Error
// holds "forced: ", the reason and the differences. reason must not be "".
func WithForce(reason string) Option {
	return Option{func(o *options) { o.force, o.forceSet = reason, true }}
}

// WithScratch gives Resolve an empty database of the directory's dialect.
// When files above the version are pending, Resolve applies the files up to
// it there and compares the database with the result. Resolve leaves them
// applied. Without it, on SQLite, Resolve makes a scratch database in a
// temporary file with db's driver and removes it after.
func WithScratch(db *sql.DB) Option {
	return Option{func(o *options) { o.scratch = db }}
}

// forcedPrefix starts the Error of a row Resolve recorded by WithForce.
const forcedPrefix = "forced: "

// A claim is what a Resolve would record: the history after it, its words
// for a refusal and for a forced record, and how to go on instead.
type claim struct {
	after []Applied
	not   string   // "version 2 is not marked applied"
	done  string   // "version 2 is marked applied"
	ways  []string // the ways on but -force: "finish its statements by hand and resolve again"
}

// checkClaim compares the database with what the history after c says it holds:
// see expected. Changes refuse c with an *Error of Code "drift"; a schema it
// cannot read refuses c with one of Code "unverified". WithForce lets both
// through, and forced is then the row's Error. Warnings come as notes.
func (s *session) checkClaim(ctx context.Context, fsys fs.FS, dir *Dir, c claim, o options) (forced string, err error) {
	want, against, why, err := s.expected(ctx, fsys, dir, c.after, o)
	if err != nil {
		return "", err
	}
	if why != "" {
		if !o.forceSet {
			return "", &Error{Code: "unverified", Reason: c.not + ": " + why + "; or pass -force with the reason"}
		}
		s.say(fmt.Sprintf("%s by force (%s), unchecked: %s", c.done, o.force, why))
		return forcedPrefix + o.force + "; unchecked: " + why, nil
	}
	refs, enums := refsOf(want)
	live, notes, err := s.inspectLive(ctx, refs, enums)
	if err != nil {
		return "", wrap(ctx, "inspecting the database", err)
	}
	r, err := drift(live, notes, want)
	if err != nil {
		return "", err
	}
	if len(r.Changes) == 0 {
		for _, w := range r.Warnings {
			s.say(fmt.Sprintf("checked against %s, with a warning: %s", against, w))
		}
		return "", nil
	}
	lines := make([]string, 0, len(r.Changes)+len(r.Warnings))
	changes := make([]string, 0, len(r.Changes))
	for _, ch := range r.Changes {
		lines = append(lines, "change: "+ch.String())
		changes = append(changes, ch.String())
	}
	for _, w := range r.Warnings {
		lines = append(lines, "warning: "+w.String())
	}
	differs := "the database differs from " + against + "; what would bring it there:\n  " + strings.Join(lines, "\n  ")
	if !o.forceSet {
		return "", &Error{Code: "drift", Reason: c.not + ": " + differs + "\n" + strings.Join(c.ways, "; ") + "; or pass -force with the reason"}
	}
	s.say(fmt.Sprintf("%s by force (%s), though %s", c.done, o.force, differs))
	return forcedPrefix + o.force + "; the database differed from " + against + ": " + strings.Join(changes, "; "), nil
}

// expected is what the history after says the database holds, and how
// messages name it. With m the highest version of after, it is schema.json
// when m is the directory's last file; else the files up to m, applied to a
// scratch database and inspected there. why says what stops a reading: a
// dirty version, one above the directory's last file, a file pending below
// m, no scratch database on PostgreSQL or MySQL, or files that fail there.
func (s *session) expected(ctx context.Context, fsys fs.FS, dir *Dir, after []Applied, o options) (want *Schema, against, why string, err error) {
	var m uint64
	held := map[uint64]bool{}
	for _, a := range after {
		if a.Dirty {
			return nil, "", fmt.Sprintf("version %d is dirty, so the database holds part of it", a.Version), nil
		}
		m, held[a.Version] = max(m, a.Version), true
	}
	last := dir.Files[len(dir.Files)-1]
	if m > last.Version {
		return nil, "", fmt.Sprintf("the history has version %d, above the directory's last file, which nothing in the directory describes", m), nil
	}
	for _, f := range dir.Files {
		if f.Version < m && !held[f.Version] {
			return nil, "", fmt.Sprintf("%s is pending below version %d", f.Path, m), nil
		}
	}
	if m == last.Version {
		return dir.Snapshot, "schema.json", "", nil
	}
	j := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == m })
	if j < 0 {
		return nil, "", fmt.Sprintf("the directory has no file for version %d", m), nil
	}
	against = "what the files up to " + dir.Files[j].Path + " make on an empty database"
	scratch := o.scratch
	if scratch == nil {
		if s.d != velox.SQLite {
			return nil, "", fmt.Sprintf("files above version %d are pending, so only the files up to it, applied to an empty database, say what the database holds; give one with -scratch-db", m), nil
		}
		var done func()
		if scratch, done, err = tempSQLite(s.pool); err != nil {
			return nil, "", "", err
		}
		defer done()
	}
	want, why, err = replay(ctx, scratch, fsys, dir.Config, m)
	return want, against, why, err
}

// replay applies the files of fsys up to version m to scratch, which must be
// empty, and inspects it: every table of its default schema but the history.
// failed says why the files did not apply: one may need rows that a later
// hand fix made good, and fail on an empty database.
func replay(ctx context.Context, scratch *sql.DB, fsys fs.FS, cfg Config, m uint64) (want *Schema, failed string, err error) {
	if err := verifyEmpty(ctx, scratch, cfg, "the scratch database must be empty"); err != nil {
		return nil, "", err
	}
	if _, err := Up(ctx, scratch, fsys, WithTarget(m)); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, fmt.Sprintf("the files up to version %d fail on the scratch database: %s", m, strings.TrimPrefix(err.Error(), "migrate: ")), nil
	}
	if want, err = Inspect(ctx, scratch, cfg.Dialect, nil); err != nil {
		return nil, "", err
	}
	want.Tables = slices.DeleteFunc(want.Tables, func(t Table) bool { return t.Schema == "" && t.Name == cfg.Table })
	return want, "", nil
}

// tempSQLite is a SQLite database in a new temporary file, opened with db's
// driver, and what closes and removes it.
func tempSQLite(db *sql.DB) (*sql.DB, func(), error) {
	dir, err := os.MkdirTemp("", "velox-resolve-")
	if err != nil {
		return nil, nil, &Error{Code: "unverified", Reason: "making a scratch database: " + err.Error(), cause: err}
	}
	path := filepath.Join(dir, "scratch.db")
	var c driver.Connector = pathConnector{path, db.Driver()}
	if dc, ok := db.Driver().(driver.DriverContext); ok {
		if c, err = dc.OpenConnector(path); err != nil {
			_ = os.RemoveAll(dir)
			return nil, nil, &Error{Code: "unverified", Reason: "making a scratch database: " + err.Error(), cause: err}
		}
	}
	scratch := sql.OpenDB(c)
	return scratch, func() { _ = scratch.Close(); _ = os.RemoveAll(dir) }, nil
}

// pathConnector opens name with a driver that has no connector of its own.
type pathConnector struct {
	name string
	d    driver.Driver
}

// Connect opens name.
func (p pathConnector) Connect(context.Context) (driver.Conn, error) { return p.d.Open(p.name) }

// Driver is the driver.
func (p pathConnector) Driver() driver.Driver { return p.d }

// inspectLive is inspectWith on the session's own connection, which holds
// the lock, and on SQLite its write transaction: a pool of one connection
// has no other to give.
func (s *session) inspectLive(ctx context.Context, tables, enums []TableRef) (*Schema, []note, error) {
	var sch *Schema
	var notes []note
	var err error
	switch s.d {
	case velox.Postgres:
		sch, notes, err = inspectPostgresConn(ctx, s.conn, tables, enums)
	case velox.MySQL:
		sch, notes, err = inspectMySQLConn(ctx, s.conn, tables)
	default:
		sch, notes, err = inspectSQLite(ctx, s.conn, tables)
	}
	if err != nil {
		return nil, nil, err
	}
	sch.sort()
	return sch, notes, nil
}
