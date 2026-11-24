package integration

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
)

// TestFieldInSelectRaw runs ADR 0157's case on every dialect: owners' names,
// text, hold ids of notes and docs, integers. FieldInSelectRaw matches a
// name whose row INote's interceptor (note 2) or Read policy (note 3) hides,
// or that is soft-deleted (doc 1) or in another org (doc 3); FieldInSelect
// does not. An Around hook sees the reason, and an UPDATE and a DELETE take
// the condition.
func TestFieldInSelectRaw(t *testing.T) {
	var mu sync.Mutex
	var reasons []string
	around := velox.WithAround(func(c context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		reasons = append(reasons, st.RawReasons()...)
		mu.Unlock()
		return next(c)
	})
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		softTables(t, tg)
		tg.exec(t, "UPDATE owners SET name = ? WHERE id = ?", "3", 1)
		tg.exec(t, "UPDATE owners SET name = ? WHERE id = ?", "2", 2)
		tg.exec(t, "INSERT INTO owners (id, name, pinned_id) VALUES (?, ?, ?)", 3, "1", nil)
		in1 := testmodel.Org.With(context.Background(), int64(1))
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(fixture.SDocs.ID.EQ(1)).Exec(in1); err != nil || n != 1 {
			t.Fatalf("soft delete: %d, %v", n, err)
		}
		O := fixture.IOwners
		owners := func(c context.Context, p velox.Predicate) []int64 {
			t.Helper()
			os, err := tg.pdb.Query[fixture.IOwner]().Where(velox.Pred[fixture.IOwner](p)).OrderBy(O.ID.Asc()).AllowUnbounded().All(c)
			if err != nil {
				t.Fatal(err)
			}
			return ownerIDs(os)
		}
		const why = "a deleted row keeps its history"
		for _, c := range []struct {
			entity      string
			plain, want []int64
		}{
			{"INote", []int64{3}, []int64{1, 2, 3}},
			{"SDoc", []int64{2}, []int64{1, 2, 3}},
		} {
			wantIDs(t, owners(in1, velox.FieldInSelect("name", c.entity, "id")), c.plain)
			mu.Lock()
			reasons = nil
			mu.Unlock()
			wantIDs(t, owners(in1, velox.FieldInSelectRaw(why, "name", c.entity, "id")), c.want)
			mu.Lock()
			if !slices.Equal(reasons, []string{why}) {
				t.Errorf("%s: RawReasons %q", c.entity, reasons)
			}
			mu.Unlock()
		}
		// In an Or and a Not, with a condition of its own.
		hidden := velox.FieldInSelectRaw(why, "name", "INote", "id", velox.FieldEQ("hidden", true))
		wantIDs(t, owners(in1, velox.Or(velox.FieldEQ("id", int64(3)), velox.Not(hidden))), []int64{1, 3})

		upd, err := tg.pdb.Update[fixture.IOwner]().Set(O.PinnedID.Set(4)).Where(velox.Pred[fixture.IOwner](hidden)).Exec(in1)
		if err != nil || upd != 1 {
			t.Errorf("update: %d, %v", upd, err)
		}
		del, err := tg.pdb.Delete[fixture.IOwner]().Where(velox.Pred[fixture.IOwner](hidden)).Exec(in1)
		if err != nil || del != 1 {
			t.Errorf("delete: %d, %v", del, err)
		}
		wantIDs(t, owners(in1, velox.Allow()), []int64{1, 3})
	}, around)
}

// TestFieldInSelectTextColumnForms: a text column meets an integer key as
// the key's text would (ADR 0161). An owner's name matches a note's id
// exactly when it is the id as strconv.FormatInt writes it: "01", " 1",
// "+1", "1.0", "-0", "" and numbers past int64 match nothing, and no text
// fails the statement, an UPDATE's and a DELETE's included (MySQL's strict
// mode turns a bad cast there into an error). On SQLite the key's index
// serves the subquery.
func TestFieldInSelectTextColumnForms(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		notes := []int64{1, 3, 4, 0, -5, math.MaxInt64, math.MinInt64} // note 2 is hidden by INote's interceptor
		tg.exec(t, "INSERT INTO notes (id, owner_id, body, hidden) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)",
			int64(0), 1, "zero", false, int64(-5), 1, "neg", false, int64(math.MaxInt64), 1, "max", false, int64(math.MinInt64), 1, "min", false)
		names := []string{
			"1", "01", " 1", "1 ", "+1", "1.0", "1e0", "1\n", "", "abc", "-0", "0", "-5", "-05", "2", "3",
			"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
			"18446744073709551615", "99999999999999999999",
		}
		var want []int64
		for i, n := range names {
			id := int64(100 + i)
			tg.exec(t, "INSERT INTO owners (id, name, pinned_id) VALUES (?, ?, ?)", id, n, nil)
			for _, k := range notes {
				if n == strconv.FormatInt(k, 10) {
					want = append(want, id)
				}
			}
		}
		bg := context.Background()
		O := fixture.IOwners
		for _, p := range []velox.Predicate{
			velox.FieldInSelect("name", "INote", "id"),
			velox.FieldInSelectRaw("forms", "name", "INote", "id", velox.FieldEQ("hidden", false)),
		} {
			q := tg.db.Query[fixture.IOwner]().Where(O.ID.GTE(100), velox.Pred[fixture.IOwner](p)).OrderBy(O.ID.Asc()).AllowUnbounded()
			os, err := q.All(bg)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ownerIDs(os), want)
			if tg.d == velox.SQLite {
				text, args, err := q.SQL(bg)
				if err != nil {
					t.Fatal(err)
				}
				if plan := sqlitePlan(t, tg, text, args); !strings.Contains(plan, "SEARCH vx1 ") || !strings.Contains(plan, "(id=?)") {
					t.Errorf("the key's index does not serve the subquery:\n%s", plan)
				}
			}
			n, err := tg.db.Update[fixture.IOwner]().Set(O.PinnedID.Set(1)).Where(O.ID.GTE(100), velox.Pred[fixture.IOwner](p)).Exec(bg)
			if err != nil || n != int64(len(want)) {
				t.Errorf("update: %d, %v; want %d", n, err, len(want))
			}
		}
		n, err := tg.db.Delete[fixture.IOwner]().Where(O.ID.GTE(100), velox.Pred[fixture.IOwner](velox.Not(velox.FieldInSelect("name", "INote", "id")))).Exec(bg)
		if err != nil || n != int64(len(names)-len(want)) {
			t.Errorf("delete: %d, %v; want %d", n, err, len(names)-len(want))
		}
	})
}

// sqlitePlan returns SQLite's query plan of text, one step a line.
func sqlitePlan(t *testing.T, tg target, text string, args []any) string {
	t.Helper()
	rows, err := tg.direct.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+text, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		b.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
