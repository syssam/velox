package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/syssam/velox2"
)

// A Resolution is how a human ended a dirty version.
type Resolution uint8

const (
	ResolveApplied Resolution = iota + 1 // a human finished the version
	ResolveUndone                        // a human reverted it; the row is deleted
)

// Resolve ends the dirty version of db's history, under the migration lock.
// ResolveApplied marks it applied, with every statement of its file, which
// must be in dir with the row's checksum (else a *ChecksumError);
// ResolveUndone deletes its row, so the file may be edited and applied
// again, and reads only dir's velox.json, so an edit not yet hashed into
// velox.sum does not stop it. Resolve reads the history itself, not against
// dir's other files, so an edited file does not stop it either. A version
// that is not dirty, or not in the history, is an *Error of Code "history";
// a Resolution other than the two, one of Code "resolution". Like Up, it
// uses one connection of db, its session in UTC, and closes it after. Of
// the Options, it takes WithLockWait and WithLogger.
func Resolve(ctx context.Context, db *sql.DB, dir fs.FS, version uint64, r Resolution, opts ...Option) error {
	if r != ResolveApplied && r != ResolveUndone {
		return &Error{Code: "resolution", Reason: fmt.Sprintf("no resolution %d; use ResolveApplied or ResolveUndone", r)}
	}
	o := optionsOf(opts)
	var d *Dir
	var cfg Config
	if r == ResolveApplied {
		var err error
		if d, err = ReadDir(dir); err != nil {
			return err
		}
		cfg = d.Config
	} else {
		raw, err := fs.ReadFile(dir, "velox.json")
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return &Error{Code: "config", Pos: "velox.json", Reason: "the file is missing"}
		case err != nil:
			return &Error{Code: "config", Pos: "velox.json", Reason: "the file cannot be read: " + err.Error(), cause: err}
		}
		if cfg, err = parseConfig(raw); err != nil {
			return err
		}
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
	what := fmt.Sprintf("resolving version %d in %s", version, s.cfg.Table)
	if s.d == velox.SQLite {
		if err := s.begin(ctx); err != nil {
			return wrap(ctx, what, err)
		}
	}
	err = s.resolve(ctx, d, version, r)
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
	if o.logger != nil {
		o.logger.InfoContext(ctx, "migrate: resolved", "version", version, "applied", r == ResolveApplied)
	}
	return nil
}

// resolve is Resolve's read and write of the history.
func (s *session) resolve(ctx context.Context, dir *Dir, version uint64, r Resolution) error {
	what := fmt.Sprintf("resolving version %d in %s", version, s.cfg.Table)
	rows, _, err := s.history(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(a Applied) bool { return a.Version == version })
	switch {
	case i < 0:
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is not in the history table %s", version, s.cfg.Table)}
	case !rows[i].Dirty:
		return &Error{Code: "history", Reason: fmt.Sprintf("version %d is not dirty", version)}
	}
	row := rows[i]
	table, where := s.q(s.cfg.Table), s.q("version")
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
	q := fmt.Sprintf("UPDATE %s SET %s = %s, %s = %s, %s = %s WHERE %s = %s", table,
		s.q("dirty"), s.ph(1), s.q("statements"), s.ph(2), s.q("error"), s.ph(3), where, s.ph(4))
	_, err = s.conn.ExecContext(ctx, q, false, len(f.Statements), "", int64(version))
	return wrap(ctx, what, err)
}
