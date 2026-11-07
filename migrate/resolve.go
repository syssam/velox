package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox"
)

// A Resolution is how a human ended a dirty version.
type Resolution uint8

// The ways Resolve ends a dirty version or mends the history.
const (
	ResolveApplied    Resolution = iota + 1 // a human finished the version
	ResolveUndone                           // a human reverted it; the row is deleted
	ResolveStatements                       // WithStatements' count of the file ran; the version stays dirty, to resume
	ResolveAdopted                          // the directory's file is what this applied version is now; nothing runs
	ResolveForgotten                        // the version's row goes; nothing is undone
)

// WithStatements is how many of the file's statements ResolveStatements records as run.
func WithStatements(k int) Option {
	return Option{func(o *options) { o.statements, o.statementsSet = k, true }}
}

// Resolve ends the dirty version of db's history, or mends a history the directory
// has moved away from, under the migration lock.
//
// ResolveAdopted accepts the directory's file, its checksum and name, for a clean
// applied version, as when two branches took one number or a file was edited, and
// runs none of it. ResolveForgotten deletes the row of a clean version whose file is
// gone, differs, or never ran (it counts no statements), and refuses one the
// directory's file would run again; it undoes nothing, and says so through
// WithNotes. ResolveApplied of a version not in the history records its file as
// applied, with no statements, running none of it, as a baseline file on a database
// already there needs.
//
// ResolveStatements records that the first k of its file's statements ran, for
// WithStatements' k from 0 to their number: a run that died may have left the count
// behind what the server finished, and WithResume then goes on from statement k+1.
// ResolveApplied marks it applied, with every statement of its file, which
// must be in dir with the row's checksum (else a *ChecksumError);
// ResolveUndone deletes its row, so the file may be edited and applied
// again, and reads only dir's velox.json, so an edit not yet hashed into
// velox.sum does not stop it. Resolve reads the history itself, not against
// dir's other files, so an edited file does not stop it either. A version
// not in the state the Resolution needs is an *Error of Code "history";
// an unknown Resolution, a count out of range, or WithForce with no reason,
// one of Code "resolution".
//
// ResolveApplied and ResolveAdopted check the database before they record.
// With m the highest version the history would then hold, the database must
// hold what the directory says it holds at m: schema.json when m is the
// directory's last file, else the files up to m applied to WithScratch's
// database and inspected there. As Drift does, only the tables and enum
// types of that schema are read, so a table it does not name is no
// difference. A change is an *Error of Code "drift" listing each, as
// velox-gen migrate drift prints them. A claim that cannot be checked is
// one of Code "unverified": a dirty version left, a version above the
// directory's last file, a file pending below m, no scratch database on
// PostgreSQL or MySQL, or files that fail there, as one whose rows a hand
// fix made good may. WithForce records either, and drift warnings alone
// come as notes. ResolveUndone, ResolveStatements and ResolveForgotten
// check nothing: each leaves the version for a run, or takes the claim away.
//
// Like Up, it uses one connection of db, its session in UTC, and closes it
// after. Of the Options, it takes WithLockWait, WithLogger, WithNotes,
// WithStatements, WithForce and WithScratch.
func Resolve(ctx context.Context, db *sql.DB, dir fs.FS, version uint64, r Resolution, opts ...Option) error {
	if r < ResolveApplied || r > ResolveForgotten {
		return &Error{Code: "resolution", Reason: fmt.Sprintf("no resolution %d; use ResolveApplied, ResolveUndone, ResolveStatements, ResolveAdopted or ResolveForgotten", r)}
	}
	o := optionsOf(opts)
	if r == ResolveStatements && !o.statementsSet {
		return &Error{Code: "resolution", Reason: "ResolveStatements needs WithStatements"}
	}
	if o.forceSet && strings.TrimSpace(o.force) == "" {
		return &Error{Code: "resolution", Reason: "WithForce needs a reason"}
	}
	var d *Dir
	var cfg Config
	var err error
	if r != ResolveUndone {
		if d, err = ReadDir(dir); err != nil {
			return err
		}
		cfg = d.Config
	} else if cfg, err = readConfig(dir); err != nil {
		return err
	}
	if db == nil {
		return &Error{Code: "nil", Reason: "Resolve needs a database"}
	}
	s, err := open(ctx, db, cfg, o.lockWait)
	if err != nil {
		return err
	}
	defer s.close()
	unlock, err := s.lock(ctx, o.lockWait)
	if err != nil {
		return err
	}
	defer unlock()
	var said []string
	s.notify = func(note string) { said = append(said, note) }
	what := fmt.Sprintf("resolving version %d in %s", version, s.cfg.Table)
	if s.d == velox.SQLite {
		if err := s.begin(ctx); err != nil {
			return wrap(ctx, what, err)
		}
	}
	err = s.resolve(ctx, dir, d, version, r, o)
	if s.d == velox.SQLite {
		if err == nil {
			if _, cerr := s.conn.ExecContext(ctx, "COMMIT"); cerr != nil {
				err = wrap(ctx, what+": COMMIT", cerr)
			}
		}
		if err != nil {
			s.rollback(ctx)
		}
	}
	if err != nil {
		return err
	}
	for _, note := range said {
		o.notify(ctx)(note)
	}
	if o.logger != nil {
		o.logger.InfoContext(ctx, "migrate: resolved", "version", version, "applied", r == ResolveApplied)
	}
	return nil
}

// readConfig is dir's velox.json, read alone.
func readConfig(dir fs.FS) (Config, error) {
	raw, err := fs.ReadFile(dir, "velox.json")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Config{}, &Error{Code: "config", Pos: "velox.json", Reason: "the file is missing"}
	case err != nil:
		return Config{}, &Error{Code: "config", Pos: "velox.json", Reason: "the file cannot be read: " + err.Error(), cause: err}
	}
	return parseConfig(raw)
}

// History is db's history table, oldest first, read against nothing: dir gives only
// its velox.json, and a database with no history table has no rows. It takes no lock,
// and is for a look: velox-gen migrate diff's, at the versions a new file must not
// reuse.
func History(ctx context.Context, db *sql.DB, dir fs.FS) ([]Applied, error) {
	cfg, err := readConfig(dir)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, &Error{Code: "nil", Reason: "History needs a database"}
	}
	s, err := open(ctx, db, cfg, defaultLockWait)
	if err != nil {
		return nil, err
	}
	defer s.close()
	rows, err := s.history(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b Applied) int { return cmp.Compare(a.Version, b.Version) })
	return rows, nil
}

// resolve is Resolve's read and write of the history; fsys is dir's files.
func (s *session) resolve(ctx context.Context, fsys fs.FS, dir *Dir, version uint64, r Resolution, o options) error {
	what := fmt.Sprintf("resolving version %d in %s", version, s.cfg.Table)
	rows, err := s.history(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(a Applied) bool { return a.Version == version })
	table, where := s.historyTable(), s.q("version")
	switch {
	case i < 0 && r == ResolveApplied:
		return s.markPending(ctx, fsys, dir, rows, version, o)
	case i < 0:
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is not in the history table %s", version, s.cfg.Table)}
	case r == ResolveAdopted || r == ResolveForgotten:
		if rows[i].Dirty {
			return &Error{Code: "history", Reason: fmt.Sprintf("version %d is dirty; end that first, with -resume, -undone or -applied", version)}
		}
		if r == ResolveAdopted {
			return s.adopt(ctx, fsys, dir, rows, rows[i], o)
		}
		return s.forget(ctx, dir, rows, rows[i])
	case !rows[i].Dirty:
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is not dirty", version)}
	}
	row := rows[i]
	if r == ResolveUndone {
		_, err := s.conn.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = %s", table, where, s.ph(1)), int64(version))
		return wrap(ctx, what, err)
	}
	j := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == version })
	if j < 0 {
		return &ChecksumError{Version: version, Path: dir.PathOf(version, row.Name), Applied: row.Sum}
	}
	f := dir.Files[j]
	if f.Sum != row.Sum {
		return &ChecksumError{Version: version, Path: f.Path, Applied: row.Sum, Now: f.Sum}
	}
	if r == ResolveStatements {
		if o.statements < 0 || o.statements > len(f.Statements) {
			return &Error{Code: "resolution", Pos: f.Path, Reason: fmt.Sprintf("%d statements: the file has %d", o.statements, len(f.Statements))}
		}
		q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", table, s.q("statements"), s.ph(1), where, s.ph(2))
		_, err = s.conn.ExecContext(ctx, q, o.statements, int64(version))
		return wrap(ctx, what, err)
	}
	after := slices.Clone(rows)
	after[i].Dirty = false
	forced, err := s.checkClaim(ctx, fsys, dir, claim{
		after: after,
		not:   fmt.Sprintf("version %d is not marked applied", version),
		done:  fmt.Sprintf("version %d is marked applied", version),
		ways:  []string{"finish its statements by hand and resolve again", "record how many ran with -statements k and run migrate apply -resume"},
	}, o)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("UPDATE %s SET %s = %s, %s = %s, %s = %s WHERE %s = %s", table,
		s.q("dirty"), s.ph(1), s.q("statements"), s.ph(2), s.q("error"), s.ph(3), where, s.ph(4))
	_, err = s.conn.ExecContext(ctx, q, false, len(f.Statements), forced, int64(version))
	return wrap(ctx, what, err)
}

// markPending is ResolveApplied of a version not in the history: its file is recorded
// as applied with no statements, as none ran. A pending file below it is refused:
// the history is linear. So is any while a version is dirty: -applied there meant
// that version, and would otherwise skip a whole file in silence.
func (s *session) markPending(ctx context.Context, fsys fs.FS, dir *Dir, rows []Applied, version uint64, o options) error {
	j := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == version })
	if j < 0 {
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is not in the history table %s, and the directory has no file for it", version, s.cfg.Table)}
	}
	for _, f := range dir.Files[:j] {
		if !slices.ContainsFunc(rows, func(a Applied) bool { return a.Version == f.Version }) {
			return &Error{Code: "history", Reason: fmt.Sprintf("%s is pending below version %d; apply or mark it first", f.Path, version)}
		}
	}
	if k := slices.IndexFunc(rows, func(a Applied) bool { return a.Dirty }); k >= 0 {
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is dirty; end that first, with -resume, -undone or -applied on version %d, before marking version %d applied", rows[k].Version, rows[k].Version, version)}
	}
	f := dir.Files[j]
	a := Applied{Version: version, Name: f.Name, Sum: f.Sum, AppliedAt: time.Now().UTC().Truncate(time.Microsecond)}
	forced, err := s.checkClaim(ctx, fsys, dir, claim{
		after: append(slices.Clone(rows), a),
		not:   fmt.Sprintf("version %d is not marked applied", version),
		done:  fmt.Sprintf("version %d is marked applied", version),
		ways:  []string{fmt.Sprintf("make the database hold what %s makes and resolve again", f.Path), "let migrate apply run it"},
	}, o)
	if err != nil {
		return err
	}
	a.Error = forced
	s.say(fmt.Sprintf("%s is recorded as applied, and none of it ran", f.Path))
	return wrap(ctx, fmt.Sprintf("recording version %d in %s", version, s.cfg.Table), s.record(ctx, a))
}

// adopt is ResolveAdopted of the clean row, one of rows. A forced adopt
// writes the row's Error; a checked one keeps it.
func (s *session) adopt(ctx context.Context, fsys fs.FS, dir *Dir, rows []Applied, row Applied, o options) error {
	j := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == row.Version })
	if j < 0 {
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d: the directory has no file to adopt; to take the row away use -forget", row.Version)}
	}
	f := dir.Files[j]
	forced, err := s.checkClaim(ctx, fsys, dir, claim{
		after: rows,
		not:   fmt.Sprintf("version %d is not adopted", row.Version),
		done:  fmt.Sprintf("version %d is adopted", row.Version),
		ways:  []string{fmt.Sprintf("make the database hold what %s now makes and resolve again", f.Path), "restore the file"},
	}, o)
	if err != nil {
		return err
	}
	if forced == "" {
		forced = row.Error
	}
	q := fmt.Sprintf("UPDATE %s SET %s = %s, %s = %s, %s = %s WHERE %s = %s", s.historyTable(), s.q("name"), s.ph(1), s.q("checksum"), s.ph(2), s.q("error"), s.ph(3), s.q("version"), s.ph(4))
	if _, err := s.conn.ExecContext(ctx, q, f.Name, f.Sum, forced, int64(row.Version)); err != nil {
		return wrap(ctx, fmt.Sprintf("resolving version %d in %s", row.Version, s.cfg.Table), err)
	}
	s.say(fmt.Sprintf("version %d was %s, checksum %s, and is now %s, checksum %s; none of the file ran, so the database holds what the old one did", row.Version, versionFile(row.Version, row.Name), row.Sum, f.Path, f.Sum))
	return nil
}

// forget is ResolveForgotten of the clean row. The file the row ran, there still, is
// refused: Up would run it again over what it left.
func (s *session) forget(ctx context.Context, dir *Dir, rows []Applied, row Applied) error {
	j := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == row.Version })
	if j >= 0 && dir.Files[j].Sum == row.Sum && row.Statements > 0 {
		return &Error{Code: "history", Pos: dir.Files[j].Path, Reason: fmt.Sprintf("version %d ran this file, and with its row gone the next apply would run it again; to undo it, reverse it in a new migration", row.Version)}
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE %s = %s", s.historyTable(), s.q("version"), s.ph(1))
	if _, err := s.conn.ExecContext(ctx, q, int64(row.Version)); err != nil {
		return wrap(ctx, fmt.Sprintf("resolving version %d in %s", row.Version, s.cfg.Table), err)
	}
	note := fmt.Sprintf("version %d (%s) is out of the history and nothing was undone: the database keeps whatever it ran", row.Version, versionFile(row.Version, row.Name))
	if j >= 0 {
		note += "; " + dir.Files[j].Path + " is pending"
		if slices.ContainsFunc(rows, func(a Applied) bool { return a.Version > row.Version }) {
			note += ", below versions still applied, which apply refuses until they go too"
		}
	}
	s.say(note)
	return nil
}
