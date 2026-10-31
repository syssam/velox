package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/syssam/velox"
)

// Baseline reads the tables and enum types want names from db, a database
// of want's dialect, and returns them as a snapshot for a new migration
// directory, with warnings.
//
// An object the database holds as want does takes want's spelling: a
// predicate, check or default the comparison of Drift calls the same, and a
// name SQLite does not keep. Everything else keeps the database's form, so
// the next migrate diff plans what differs. A table of want that is not in
// the database is a warning and left out; the next diff creates it. A table
// the database holds that want does not name is never read. An enum type
// want names that the database has is in the snapshot with the database's
// values, even when no table found uses it, so the next diff does not
// create it again. The warnings are what Drift reports, and what a build
// from the snapshot loses: a PostgreSQL sequence's start and increment, a
// type from an extension, a PostgreSQL domain type, which it does not create.
// MySQL's AUTO_INCREMENT counter is not read, and gets no warning: a
// database built from 0001_baseline.sql starts each table's IDs at 1. A
// PostgreSQL 18 UNIQUE … WITHOUT OVERLAPS constraint is read with a note,
// and 0001_baseline.sql builds it as a plain UNIQUE constraint, without the
// overlap rule.
//
// Baseline refuses, with Code "baseline", a database that holds none of
// want's tables, a foreign key to a table outside the snapshot, and on
// PostgreSQL a nextval default whose sequence the column does not own.
// Several refusals are joined, so a caller reads one with errors.As. On an
// error the snapshot and the warnings are nil. Baseline only reads, on one
// connection of db that it closes after, never returning it to the pool.
func Baseline(ctx context.Context, db *sql.DB, want *Schema) (*Schema, []Warning, error) {
	if want == nil {
		return nil, nil, &Error{Code: "nil", Reason: "Baseline needs the schema"}
	}
	if db == nil {
		return nil, nil, &Error{Code: "nil", Reason: "Baseline needs a database"}
	}
	tables, enums := refsOf(want)
	inspected, notes, err := inspectWith(ctx, db, want.Dialect, tables, enums)
	if err != nil {
		return nil, nil, err
	}
	return adopt(inspected, notes, want)
}

// adopt is Baseline after inspecting: inspected's tables that want names,
// with the enum types they use or want names, each object in want's
// spelling where drift's comparison finds it equal, in inspected's
// otherwise; the notes on those tables as warnings; and the refusals,
// joined. On an error the snapshot and the warnings are nil.
func adopt(inspected *Schema, notes []note, want *Schema) (*Schema, []Warning, error) {
	if inspected == nil || want == nil {
		return nil, nil, &Error{Code: "nil", Reason: "Baseline needs the inspected schema and the schema"}
	}
	var missing []Warning
	found := 0
	for _, t := range want.Tables {
		if inspected.table(t.Ref()) == nil {
			missing = append(missing, Warning{Object: t.Ref().String(),
				Message: "the database has no table " + t.Ref().String() + "; the next migrate diff creates it"})
			continue
		}
		found++
	}
	if found == 0 {
		return nil, nil, &Error{Code: "baseline", Reason: "the database holds none of the schema's tables; use migrate diff to start a directory"}
	}
	c, err := compare(inspected, notes, want, true)
	if err != nil {
		return nil, nil, err
	}
	snap := c.db
	snap.Dialect = want.Dialect
	snap.sort()
	if err := refusals(snap, want); err != nil {
		return nil, nil, err
	}
	if err := snap.validate("the baseline"); err != nil {
		return nil, nil, err
	}
	warnings := slices.Concat(c.warnings, missing)
	sortWarnings(warnings)
	return snap, warnings, nil
}

// refusals is what keeps snap from being a directory's first snapshot,
// sorted by object: a foreign key to a table snap lacks, and on PostgreSQL a
// nextval default whose sequence the column does not own. One is returned
// as itself, several joined.
func refusals(snap, want *Schema) error {
	var errs []*Error
	refuse := func(object, reason string) {
		errs = append(errs, &Error{Code: "baseline", Object: object, Reason: reason})
	}
	for _, t := range snap.Tables {
		ref := t.Ref().String()
		for _, fk := range t.ForeignKeys {
			switch {
			case snap.table(fk.RefTable) != nil:
			case want.table(fk.RefTable) != nil:
				refuse(keyObject(t.Ref(), fk), "references "+fk.RefTable.String()+", which the database does not have; create the table, or drop the key first")
			default:
				refuse(keyObject(t.Ref(), fk), "references "+fk.RefTable.String()+", which the schema does not name; add the table to the schema, or drop the key first")
			}
		}
		if snap.Dialect != velox.Postgres {
			continue
		}
		for _, c := range t.Columns {
			if c.Sequence != "" || !strings.HasPrefix(c.Default, "nextval(") {
				continue
			}
			seq := c.Default
			if s, ok := nextvalSequence(c.Default); ok {
				seq = s.String()
			}
			refuse(ref+"."+c.Name, "the default uses the sequence "+seq+", which the column does not own; run ALTER SEQUENCE "+
				seq+" OWNED BY "+ref+"."+c.Name+", then baseline again")
		}
	}
	slices.SortFunc(errs, func(a, b *Error) int { return cmp.Or(cmp.Compare(a.Object, b.Object), cmp.Compare(a.Reason, b.Reason)) })
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	joined := make([]error, len(errs))
	for i, e := range errs {
		joined[i] = e
	}
	return errors.Join(joined...)
}
