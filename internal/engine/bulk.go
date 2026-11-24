package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"unsafe"
)

// An InsertBulk is ent's CreateBulk: a list of single-row inserts, each
// with its own setters, sent as few multi-row INSERTs as their columns
// allow (ADR 0153). The typed and the untyped InsertBulk share this file,
// so that no entity compiles a bulk insert of its own.

// bulkRows are an InsertBulk's rows, which the typed InsertBulk keeps in
// its Setters: a field of InsertQuery's own would grow every entity's
// package (ADR 0153).
type bulkRows struct{ rows []BulkRow }

// BulkSetters is the Setters of an InsertBulk of rows.
func BulkSetters(rows []BulkRow) Setters { return Setters{bulk: &bulkRows{rows: rows}} }

// IsBulk reports whether s is an InsertBulk's (BulkSetters).
func IsBulk(s Setters) bool { return s.bulk != nil }

// BulkRow is one row of an InsertBulk: the row, the setters of the Insert
// it came from, and why that Insert cannot be one of a bulk.
type BulkRow struct {
	p   unsafe.Pointer
	set Setters
	err error  // the Insert's own error, such as SetInput's: returned as it is
	bad string // why the query is not one an InsertBulk takes
}

// BulkElem is the row of an InsertBulk that the insert of the row at p
// with the setters s gives: err is its own error, single says it was made
// by Insert (not InsertAll or InsertBulk), sameDB that it was built on the
// bulk's DB (and, untyped, Table), and upsert that it carries an upsert
// option of its own.
func BulkElem(p unsafe.Pointer, s Setters, err error, single, sameDB, upsert bool) BulkRow {
	r := BulkRow{p: p, set: s, err: err}
	switch {
	case !single:
		r.bad = "is not an Insert of one row; InsertBulk takes the queries Insert makes, not InsertAll's or InsertBulk's"
	case !sameDB:
		r.bad = "was built on another DB; build every query on the DB InsertBulk is called on"
	case upsert:
		r.bad = "carries its own upsert option; give OnConflict, Update, UpdateAll, UpdateSet, UpdateWhere, DoNothing or OnConstraint to the InsertBulk, for every row"
	case len(s.Edges) > 0:
		r.bad = "has edge setters; insert a row with edges with Insert"
	}
	return r
}

// InsertBulk is InsertBulk's Exec of the rows of in that set holds
// (BulkSetters), with the bulk's own setters, which it refuses, and cf, the
// conflict clause of every row.
func InsertBulk(ctx context.Context, db *DB, in *EntityInfo, set Setters, cf *ConflictClause) error {
	if set.bulk == nil {
		return nil
	}
	rows := set.bulk.rows
	mus, err := prepareBulk(in, rows, set, cf, false)
	if err != nil || len(rows) == 0 {
		return err
	}
	t := InfoTable(in).state()
	return bulkHooks(ctx, db, in, mus, 0, func(ctx context.Context) error {
		var res Result
		err := insertBulk(ctx, t, db, bulkGroups(t, in, rows, mus, cf), &res)
		if err == nil {
			for _, mu := range mus {
				mu.Affected = res.Affected
			}
		}
		return err
	})
}

// InsertBulkSQL is InsertBulk's SQL: the first statement Exec would send,
// the INSERT of the first row's group, or the error Exec would fail with
// before sending it. It prepares and asks the Insert policy about every
// row, as Exec does.
func InsertBulkSQL(ctx context.Context, db *DB, in *EntityInfo, set Setters, cf *ConflictClause) (string, []any, error) {
	if !IsDryRun(ctx) {
		ctx = DryRunContext(ctx)
	}
	rows := set.bulk.rows
	mus, err := prepareBulk(in, rows, set, cf, true)
	if err != nil || len(rows) == 0 {
		return "", nil, err
	}
	t := InfoTable(in).state()
	var (
		q    string
		args []any
	)
	err = bulkHooks(ctx, db, in, mus, 0, func(ctx context.Context) error {
		gs := bulkGroups(t, in, rows, mus, cf)
		var first Statement
		for i, g := range gs {
			st, err := t.firstBatch(ctx, db, g.rows, g.cf)
			if err != nil {
				return err
			}
			if i == 0 {
				first = st
			}
		}
		first.InTx = first.InTx || len(gs) > 1
		var err error
		q, args, err = db.DryRun(ctx, &first)
		for _, mu := range mus {
			mu.SQL, mu.Args = q, args
		}
		return err
	})
	if err != nil && !DryRefused(mus[0], err) {
		return "", nil, err
	}
	return q, args, nil
}

// prepareBulk checks each row as its Insert's Exec would, then fills its
// defaults and stores its setters, and returns the mutation its hooks see.
// Every row is checked before any is changed.
func prepareBulk(in *EntityInfo, rows []BulkRow, set Setters, cf *ConflictClause, dry bool) ([]*MutationState, error) {
	if len(set.Set) > 0 || len(set.Edges) > 0 || len(set.Skipped) > 0 {
		return nil, fmt.Errorf("velox: %s: InsertBulk takes no Set; give each row's setters to its Insert", in.Name)
	}
	for i, r := range rows {
		if r.err != nil {
			return nil, r.err
		}
		if r.bad != "" {
			return nil, fmt.Errorf("velox: %s: InsertBulk's query %d %s", in.Name, i, r.bad)
		}
		if _, err := CheckInsertQuery(in, r.set, nil, r.p, r.p == nil, false); err != nil {
			return nil, err
		}
	}
	mus := make([]*MutationState, len(rows))
	for i, r := range rows {
		FillDefaults(in, r.p)
		if _, err := PrepareInsert(in, r.set, []unsafe.Pointer{r.p}, cf != nil, false); err != nil {
			return nil, err
		}
		mus[i] = &MutationState{Op: OpInsert, Dry: dry, Rows: []unsafe.Pointer{r.p}}
	}
	return mus, nil
}

// bulkHooks runs in's hooks for mus[i], one row's, around those of the
// rows after it, and core inside the last: row 0's hooks are the
// outermost, as ent's CreateBulk chains its builders' hooks.
func bulkHooks(ctx context.Context, db *DB, in *EntityInfo, mus []*MutationState, i int, core func(ctx context.Context) error) error {
	if i == len(mus) || !db.HasHooks(in) {
		return core(ctx)
	}
	return Mutate(ctx, db, in, mus[i], func(ctx context.Context, _ *MutationState) error {
		return bulkHooks(ctx, db, in, mus, i+1, core)
	})
}

// bulkGroup is the rows of an InsertBulk that share one column set, and
// their conflict clause.
type bulkGroup struct {
	rows TableRows
	cf   *ConflictClause
}

// bulkGroups splits rows into groups, in the order each first appears:
// rows whose setters and hooks named the same columns, whose key the
// database assigns or not alike, and whose skipped setters leave the
// upsert's update branch the same columns. Each group is an InsertAll of
// its rows, with those columns given (SetExplicit).
func bulkGroups(t *table, in *EntityInfo, rows []BulkRow, mus []*MutationState, cf *ConflictClause) []bulkGroup {
	var gs []bulkGroup
	at := map[string]int{}
	var key []byte
	for i, r := range rows {
		given := slices.Compact(slices.Sorted(slices.Values(GivenColumns(r.set.InsertSet(in), mus[i]))))
		rcf := r.set.Upsert(cf)
		key = key[:0]
		if t.generatedKey(r.p) >= 0 {
			key = append(key, 'g')
		}
		for _, c := range given {
			key = strconv.AppendInt(append(key, ','), int64(c), 10)
		}
		if rcf != cf {
			key = append(key, '|')
			if rcf.Ignore {
				key = append(key, 'i')
			}
			for _, c := range rcf.Update {
				key = strconv.AppendInt(append(key, ','), int64(c), 10)
			}
		}
		j, ok := at[string(key)]
		if !ok {
			j = len(gs)
			at[string(key)] = j
			gs = append(gs, bulkGroup{rows: TableRows{info: in, explicit: given}, cf: rcf})
		}
		g := &gs[j]
		g.rows.ptrs = append(g.rows.ptrs, r.p)
		g.rows.n++
	}
	return gs
}

// insertBulk sends gs, each group as InsertAll sends its rows, all in one
// transaction when there is more than one group: a savepoint inside the
// caller's.
func insertBulk(ctx context.Context, t *table, db *DB, gs []bulkGroup, out *Result) error {
	if len(gs) == 1 {
		return t.insertMany(ctx, db, gs[0].rows, gs[0].cf, out)
	}
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", t.info.Name, ErrDryRun)
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", t.info.Name, ErrOutsideTx)
	}
	var n int64
	unknown := false
	err := db.Tx(ctx, func(ctx context.Context, tx *DB) error {
		n, unknown = 0, false
		for _, g := range gs {
			var res Result
			if err := t.insertMany(ctx, tx, g.rows, g.cf, &res); err != nil {
				return err
			}
			unknown = unknown || res.Affected < 0
			n += res.Affected
		}
		return nil
	})
	if err != nil {
		return err
	}
	if unknown {
		n = -1
	}
	out.Affected = n
	return nil
}
