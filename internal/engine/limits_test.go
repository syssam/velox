package engine_test

import (
	"cmp"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestParameterLimit(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(3))
	_, err := db.Query[testmodel.Invoice]().Where(I.ID.In(1, 2, 3, 4)).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "the statement has 5 parameters; postgres allows 3") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

// TestDryRunSplitInsertAllSQL: an InsertAll too large for one statement
// returns the first statement from SQL(ctx), and sends nothing.
func TestDryRunSplitInsertAllSQL(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(18))
	rows := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	sql, args, err := db.InsertAll(rows).SQL(testmodel.Org.With(context.Background(), 1))
	if err != nil {
		t.Fatal(err)
	}
	const want = `INSERT INTO "invoices" ("created_at", "updated_at", "org_id", "number", "total", "paid", "note", "data") VALUES ($1, $2, $3, $4, $5, $6, $7, $8), ($9, $10, $11, $12, $13, $14, $15, $16) RETURNING "id"`
	if sql != want {
		t.Errorf("SQL\n got %s\nwant %s", sql, want)
	}
	// paid_at is nil in every row, so it is left out and holds no argument
	// (ADR 0093); the rows per statement still count it (PerStatementOf).
	// RETURNING reads the generated keys back.
	if len(args) != 16 {
		t.Fatalf("%d args, want 16: %v", len(args), args)
	}
	// The values are typed (a nil []byte); compare their print.
	if got := fmt.Sprint(args[2:8]); got != "[1 A 0 false  []]" {
		t.Errorf("row 1: %s", got)
	}
	if got := fmt.Sprint(args[10:16]); got != "[1 B 0 false  []]" {
		t.Errorf("row 2: %s", got)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestDryRunInsertAllTakesNoTransaction: an InsertAll that would run in a
// transaction of its own (more rows than one statement holds, or a
// conditioned upsert of several rows) fails under a dry run with ErrDryRun
// naming the entity, before any BEGIN.
func TestDryRunInsertAllTakesNoTransaction(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(1)}}}
	dry, db := dryContext(t, e)
	before := len(e.Log)

	restore := setMaxParams(db, 2) // one row per statement
	err := db.InsertAll([]fixture.Dnote{{Owner: "a"}, {Owner: "b"}, {Owner: "c"}}).Exec(dry)
	restore()
	if !errors.Is(err, velox.ErrDryRun) || !strings.HasPrefix(err.Error(), "velox: fixture.Dnote: ") {
		t.Errorf("large InsertAll: %v", err)
	}

	// pnote has a Read and a Write policy, so its upsert's update branch is
	// conditioned.
	err = db.InsertAll([]fixture.Pnote{{ID: 1, Owner: "a"}, {ID: 2, Owner: "b"}}).OnConflict(fixture.Pn.ID).Update(fixture.Pn.Owner).Exec(dry)
	if !errors.Is(err, velox.ErrDryRun) || !strings.HasPrefix(err.Error(), "velox: fixture.Pnote: ") {
		t.Errorf("conditioned upsert: %v", err)
	}
	if len(e.Log) != before {
		t.Errorf("sent %q", e.Log[before:])
	}
}

// TestEachReChunksOnParameterOverflow: as TestLoadReChunksOnParameterOverflow,
// for LoadEach and CountEach.
func TestEachReChunksOnParameterOverflow(t *testing.T) {
	pctx := ovReading(80)
	owners := make([]*ovOwner, 50)
	for i := range owners {
		owners[i] = &ovOwner{ID: int64(i + 1)}
	}
	t.Run("LoadEach", func(t *testing.T) {
		notes := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{1000 + o, o, int64(0)})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{notes(1, 20), notes(21, 40), notes(41, 50)}, params(100))
		got, err := velox.LoadEach(pctx, db.Query[ovNote](), ovOwners.Notes, owners)
		if err != nil {
			t.Fatal(err)
		}
		for i, ns := range got {
			if len(ns) != 1 || ns[0].ID != 1000+owners[i].ID {
				t.Errorf("owner %d: %v", owners[i].ID, ns)
			}
		}
		checkSent(t, e, s, []int{100, 100, 90})
	})
	t.Run("CountEach", func(t *testing.T) {
		counts := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{o, o})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{counts(1, 20), counts(21, 40), counts(41, 50)}, params(100))
		got, err := velox.CountEach(pctx, db.Query[ovNote](), ovOwners.Notes, owners)
		if err != nil {
			t.Fatal(err)
		}
		for i, n := range got {
			if n != owners[i].ID {
				t.Errorf("owner %d: %d", owners[i].ID, n)
			}
		}
		checkSent(t, e, s, []int{100, 100, 90})
	})
}

// TestLoadsSkipAReplicaThatFailed: once one of a query's With loads has
// fallen over, its later loads go straight to the primary.
func TestLoadsSkipAReplicaThatFailed(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked, params(64+2))
		s.r0.Results = [][][]any{{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
		s.r0.Errs = []error{nil, driver.ErrBadConn} // the query reads; the first load fails
		s.p.Results = [][][]any{{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}, {itemRow(12, 3, "C", 1)}}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if c, _ := invs[2].Edges.Items.Get(); len(c) != 1 || c[0].SKU != "C" {
			t.Fatalf("invoice 3's items %v", c)
		}
		s.check(t, hooked, [3]int{2, 2, 0}, onR0, fellOver, fellOver)
	})
}

// TestInsertAllBatchesInOneTransaction is Review Focus 3.
func TestInsertAllBatchesInOneTransaction(t *testing.T) {
	rows := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(18))
	if err := db.InsertAll(rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 4 || e.Log[0] != "BEGIN" || e.Log[3] != "COMMIT" || strings.Count(e.Log[1], "(") != 3 || strings.Count(e.Log[2], "(") != 2 {
		t.Errorf("%q", e.Log)
	}
	secondBatch := errors.New("second batch failed")
	failing := &fakeconn.Exec{Errs: []error{nil, secondBatch}} // BEGIN consumes nothing; the first INSERT succeeds
	db = openFake(velox.Postgres, failing, params(18))
	if err := db.InsertAll(rows).Exec(ctx); !errors.Is(err, secondBatch) || failing.Log[len(failing.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, failing.Log)
	}
}

func TestInsertAllAnExactlyFullBatchIsOneStatement(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(18))
	if err := db.InsertAll([]testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 1 || strings.Count(e.Log[0], "(") != 3 {
		t.Errorf("%q", e.Log)
	}
}

func TestInsertAllInsideATransactionUsesASavepoint(t *testing.T) {
	rows := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(18))
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return tx.InsertAll(rows).Exec(ctx) }); err != nil {
		t.Fatal(err)
	}
	l := e.Log
	if len(l) != 6 || l[0] != "BEGIN" || l[1] != "SAVEPOINT velox_1" || !strings.HasPrefix(l[2], "INSERT") || !strings.HasPrefix(l[3], "INSERT") || l[4] != "RELEASE SAVEPOINT velox_1" || l[5] != "COMMIT" {
		t.Errorf("%q", l)
	}

	boom := errors.New("second batch failed")
	failing := &fakeconn.Exec{Errs: []error{nil, nil, boom}} // SAVEPOINT, first INSERT, second INSERT
	db = openFake(velox.Postgres, failing, params(18))
	var inner error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		inner = tx.InsertAll(rows).Exec(ctx)
		return nil // the caller swallows the error: the savepoint must already have undone batch one
	})
	if err != nil || !errors.Is(inner, boom) {
		t.Fatalf("%v, %v", err, inner)
	}
	want := []string{"BEGIN", "SAVEPOINT velox_1"}
	if l := failing.Log; len(l) != 7 || !reflect.DeepEqual(l[:2], want) || l[4] != "ROLLBACK TO SAVEPOINT velox_1" || l[5] != "RELEASE SAVEPOINT velox_1" || l[6] != "COMMIT" {
		t.Errorf("%q", failing.Log)
	}
}

// TestWithChunksUnderTheParameterLimit is Review Focus 4.
func TestWithChunksUnderTheParameterLimit(t *testing.T) {
	// 64 parameters of headroom for hooks' conditions, one for the filter, two keys per chunk.
	e := &fakeconn.Exec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(11, 1, "B", 1), itemRow(10, 1, "A", 1), itemRow(12, 2, "C", 1)},
		{itemRow(13, 3, "D", 1)},
	}}
	db := openFake(velox.Postgres, e, params(64+3))
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0))).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 3 || !strings.Contains(e.Log[1], "IN ($1, $2)") || !strings.Contains(e.Log[2], "IN ($1)") {
		t.Fatalf("%q", e.Log)
	}
	var got []string
	for _, inv := range invs {
		items, _ := inv.Edges.Items.Get()
		for _, it := range items {
			got = append(got, it.SKU)
		}
	}
	if !reflect.DeepEqual(got, []string{"B", "A", "C", "D"}) {
		t.Errorf("%v: every row once, in the order the database returned within each owner", got)
	}
}

func TestLoadReChunksOnParameterOverflow(t *testing.T) {
	// KeysPer leaves 36 keys: with the policy's 80 values, 116 parameters,
	// 16 too many, so the chunks hold 20 keys.
	pctx := ovReading(80)
	owners := func(t *testing.T, db *velox.DB, r velox.Rel[ovOwner, ovNote]) []ovOwner {
		t.Helper()
		os, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(r).Limit(50).All(pctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(os) != 50 {
			t.Fatalf("%d owners", len(os))
		}
		return os
	}
	ids := func(t *testing.T, o ovOwner, m velox.Many[ovNote]) []int64 {
		t.Helper()
		ns, ok := m.Get()
		if !ok {
			t.Fatalf("owner %d: not loaded", o.ID)
		}
		out := []int64{}
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}
	t.Run("one-to-many", func(t *testing.T) {
		// Owner o's note is 1000+o.
		notes := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{1000 + o, o, int64(0)})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), notes(1, 20), notes(21, 40), notes(41, 50)}, params(100))
		for _, o := range owners(t, db, ovOwners.Notes) {
			if got := ids(t, o, o.Notes); !reflect.DeepEqual(got, []int64{1000 + o.ID}) {
				t.Errorf("owner %d: notes %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 100, 100, 90}) // the owners' LIMIT, then the notes'
		// Without hooks, exec's check splits the chunk the same way.
		e = &fakeconn.Exec{Results: [][][]any{ovOwnerRows(50), notes(1, 20), notes(21, 40), notes(41, 50)}}
		for _, o := range owners(t, openPolicied(velox.Postgres, e, params(100)), ovOwners.Notes) {
			if got := ids(t, o, o.Notes); !reflect.DeepEqual(got, []int64{1000 + o.ID}) {
				t.Errorf("without hooks: owner %d: notes %v", o.ID, got)
			}
		}
		if e.Calls != 4 {
			t.Errorf("without hooks: %d statements sent, want 4", e.Calls)
		}
	})
	t.Run("many-to-many", func(t *testing.T) {
		// Owner o picks notes 10o+1 and 10o+2, ranked 1 and 2: two keys per
		// owner, so 10 owners per chunk. The fake returns each owner's in
		// the order OrderBy(Rank.Desc()) gives.
		links := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{o, 10*o + 1}, []any{o, 10*o + 2})
			}
			return rows
		}
		targets := func(lo int64) [][]any {
			var rows [][]any
			for o := lo; o < lo+10; o++ {
				rows = append(rows, []any{10*o + 2, int64(0), int64(2)}, []any{10*o + 1, int64(0), int64(1)})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), links(1, 36), links(37, 50), targets(1), targets(11), targets(21), targets(31), targets(41)}, params(100))
		for _, o := range owners(t, db, ovOwners.Picks.OrderBy(ovNotes.Rank.Desc())) {
			if got, want := ids(t, o, o.Picks), []int64{10*o.ID + 2, 10*o.ID + 1}; !reflect.DeepEqual(got, want) {
				t.Errorf("owner %d: picks %v, want %v", o.ID, got, want)
			}
		}
		// The join rows have no policy: 36 keys, then 14.
		checkSent(t, e, s, []int{1, 36, 14, 100, 100, 100, 100, 100})
	})
	t.Run("many-to-many with LimitEach", func(t *testing.T) {
		// The limit is a parameter too: 117, 17 too many, 19 keys a chunk.
		picks := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{10*o + 1, int64(0), int64(1), o})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), picks(1, 19), picks(20, 38), picks(39, 50)}, params(100))
		for _, o := range owners(t, db, ovOwners.Picks.LimitEach(1)) {
			if got := ids(t, o, o.Picks); !reflect.DeepEqual(got, []int64{10*o.ID + 1}) {
				t.Errorf("owner %d: picks %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 100, 100, 93})
	})
	// picksOf are owner o's n picks, notes 100o+1 to 100o+n: its join rows
	// (links) and its targets (notes).
	links := func(o, n int64) [][]any {
		var rows [][]any
		for i := int64(1); i <= n; i++ {
			rows = append(rows, []any{o, 100*o + i})
		}
		return rows
	}
	notes := func(o, n int64) [][]any {
		var rows [][]any
		for i := int64(1); i <= n; i++ {
			rows = append(rows, []any{100*o + i, int64(0), int64(0)})
		}
		return rows
	}
	t.Run("many-to-many, a later chunk overflows", func(t *testing.T) {
		// The policy's 70 values leave 30 keys. Owners 1, 2 and 3 have 20,
		// 20 and 16 picks: owner 1's chunk is read; owners 2 and 3's, 36
		// keys, overflows and is not sent. The chunks are planned again from
		// owner 2 only, 30 keys a chunk: owner 1's is not read again.
		join := slices.Concat(links(1, 20), links(2, 20), links(3, 16))
		db, e, s := ovRun([][][]any{ovOwnerRows(3), join, notes(1, 20), notes(2, 20), notes(3, 16)}, params(100))
		os, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Picks).Limit(3).All(ovReading(70))
		if err != nil {
			t.Fatal(err)
		}
		for i, o := range os {
			if got := ids(t, o, o.Picks); len(got) != []int{20, 20, 16}[i] || got[0] != 100*o.ID+1 {
				t.Errorf("owner %d: picks %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 3, 90, 90, 86})
		for i := range e.ArgLog {
			for j := range i {
				if reflect.DeepEqual(e.ArgLog[i], e.ArgLog[j]) {
					t.Errorf("statements %d and %d were sent with the same arguments %v", j, i, e.ArgLog[i])
				}
			}
		}
	})
	t.Run("many-to-many, an owner too many for the smaller chunks", func(t *testing.T) {
		// Owner 1's 32 picks overflow by 2 with the policy's 70 values:
		// 30 keys a chunk cannot hold them, and the error says why.
		db, e, s := ovRun([][][]any{ovOwnerRows(1), links(1, 32)}, params(100))
		_, err := db.Query[ovOwner]().With(ovOwners.Picks).Limit(1).All(ovReading(70))
		const want = "velox: engine_test.ovOwner: relation Picks: an owner has 32 related rows, more than one statement can load in order: velox: engine_test.ovNote: the statement has 102 parameters; postgres allows 100"
		if err == nil || err.Error() != want {
			t.Errorf("err = %v,\nwant %s", err, want)
		}
		checkSent(t, e, s, []int{1, 1})
	})
	t.Run("many-to-many, one key cannot fit", func(t *testing.T) {
		// One pick an owner: the chunks shrink to one key, which still
		// overflows, and the load fails with the parameter error.
		join := slices.Concat(links(1, 1), links(2, 1))
		db, e, s := ovRun([][][]any{ovOwnerRows(2), join}, params(100))
		_, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Picks).Limit(2).All(ovReading(100))
		if err == nil || !strings.HasPrefix(err.Error(), "velox: engine_test.ovOwner: loading Picks: ") || !strings.Contains(err.Error(), "the statement has 101 parameters; postgres allows 100") {
			t.Errorf("%v", err)
		}
		checkSent(t, e, s, []int{1, 2})
	})
	t.Run("one key cannot fit", func(t *testing.T) {
		db, e, s := ovRun([][][]any{ovOwnerRows(50)}, params(100))
		_, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Notes).Limit(50).All(ovReading(100))
		if err == nil || !strings.Contains(err.Error(), "the statement has 101 parameters; postgres allows 100") {
			t.Errorf("%v", err)
		}
		if e.Calls != 1 || len(s.args) != 1 {
			t.Errorf("%d statements sent, %d seen by the Around hooks; want the owners' only", e.Calls, len(s.args))
		}
	})
}

// TestOfOverTheParameterLimit: each owner is a parameter, and a statement
// over the dialect's limit is refused before it is sent.
func TestOfOverTheParameterLimit(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, params(3))
	invs := []testmodel.Invoice{{ID: 1}, {ID: 2}, {ID: 3}}
	_, err := db.Query[testmodel.Item]().Where(I.Items.Of(&invs[0], &invs[1], &invs[2])).Limit(10).All(ctx)
	if err == nil || err.Error() != "velox: testmodel.Item: the statement has 4 parameters; postgres allows 3" || e.Calls != 0 {
		t.Errorf("%v, %d sent", err, e.Calls)
	}
}

// TestFailedWritePinsTheSession: a write that reached the database pins its
// session even when it failed, as its outcome is unknown: an insert the
// primary failed, an update whose connection reset, an insert whose RETURNING
// row did not scan. A write refused before it was sent does not pin.
func TestFailedWritePinsTheSession(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := seeded(hooked)
		sctx := velox.WithSession(ctx)
		s.p.Err = errors.New("boom")
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := s.db.Insert(&inv).Exec(sctx); err == nil {
			t.Fatal("the insert should fail")
		}
		s.p.Err = nil
		if u := velox.PinnedUntil(sctx); !u.After(time.Now()) {
			t.Errorf("PinnedUntil %v after a failed insert, want a time to come", u)
		}
		readOne(t, sctx, s.db)
		s.check(t, hooked, [3]int{2, 0, 0}, onPrimary, onPrimary)

		sctx = velox.WithSession(ctx)
		s.p.Errs = []error{errors.New("connection reset")}
		if _, err := s.db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(sctx); err == nil {
			t.Fatal("the update should fail")
		}
		if u := velox.PinnedUntil(sctx); !u.After(time.Now()) {
			t.Errorf("PinnedUntil %v after a failed update, want a time to come", u)
		}

		sctx = velox.WithSession(ctx)
		s.p.Results = [][][]any{{{int64(42), int64(43)}}} // two values for one key
		if err := s.db.Insert(&testmodel.Invoice{OrgID: 1, Number: "N"}).Exec(sctx); err == nil {
			t.Fatal("the insert's scan should fail")
		}
		if u := velox.PinnedUntil(sctx); !u.After(time.Now()) {
			t.Errorf("PinnedUntil %v after a failed scan, want a time to come", u)
		}

		sctx = velox.WithSession(ctx)
		restore := setMaxParams(s.db, 1)
		_, err := s.db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(sctx)
		restore()
		if err == nil {
			t.Fatal("the update over the parameter limit should fail")
		}
		if u := velox.PinnedUntil(sctx); !u.IsZero() {
			t.Errorf("PinnedUntil %v after an update refused before sending, want zero", u)
		}
		if _, err := s.db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Exec(sctx); !errors.Is(err, velox.ErrNoWhere) {
			t.Fatalf("an update without Where: %v, want ErrNoWhere", err)
		}
		if u := velox.PinnedUntil(sctx); !u.IsZero() {
			t.Errorf("PinnedUntil %v after ErrNoWhere, want zero", u)
		}
	})
}

// TestThroughLoadChunksByOwner: when the targets need several statements,
// each holds whole owners, so that every owner's targets come from one query
// in the order asked; an owner with more targets than one statement holds is
// refused.
func TestThroughLoadChunksByOwner(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}, {int64(3), int64(1), "c"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(3)}}, // books 1 and 2
		{{int64(3), int64(3)}},                                 // book 3
		{{int64(2), int64(7), "b"}, {int64(1), int64(7), "a"}}, // tags 1 and 2, for book 1
		{{int64(3), int64(7), "c"}, {int64(2), int64(7), "b"}}, // tags 2 and 3, for books 2 and 3
	}}
	db := openFake(velox.Postgres, e, params(66))
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags.OrderBy(fixture.DTags.Name.Desc())).Limit(3).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"b", "a"}, {"c", "b"}, {"c"}}
	for i, b := range bs {
		if got := tagNames(t, b.Tags); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("book %d: tags %v, want %v", b.ID, got, want[i])
		}
	}
	if len(e.Log) != 5 || !reflect.DeepEqual(e.Args, []any{int64(2), int64(3)}) {
		t.Errorf("queries %q, last args %v", e.Log, e.Args)
	}
	if want := `SELECT "id", "org_id", "name" FROM "tags" WHERE "id" IN ($1, $2) ORDER BY "name" DESC`; e.Log[3] != want {
		t.Errorf("target SQL %s", e.Log[3])
	}

	e = &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(3)}},
	}}
	_, err = openFake(velox.Postgres, e, params(66)).Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(1).All(ctx)
	if want := "velox: fixture.DBook: relation tags: an owner has 3 related rows, more than one statement can load in order"; err == nil || err.Error() != want {
		t.Errorf("got %v", err)
	}
	if len(e.Log) != 2 {
		t.Errorf("queries %q", e.Log)
	}
}

// TestStatementOutsideItsTx is Review Focus 1: under a context marked as a
// transaction's, a statement on any DB but the transaction's and its
// savepoints' (db, another Open's, another transaction's) fails with
// ErrOutsideTx before it is sent; the transaction's own DB, its savepoints,
// the TxAround hooks' work, a goroutine on the outer context and WithoutTx
// run. Each case runs on a DB without hooks (plain) and on one with an Around
// hook (run).
func TestStatementOutsideItsTx(t *testing.T) {
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	outer := ctx
	for _, c := range []struct {
		name    string
		fn      func(ctx context.Context, db, tx *velox.DB) error // ctx is fn's
		outside bool
		msg     string // the error's text when outside; the statement's by default
	}{
		{"the transaction with its ctx", func(ctx context.Context, _, tx *velox.DB) error { return read(ctx, tx) }, false, ""},
		{"the transaction with a ctx derived from its", func(ctx context.Context, _, tx *velox.DB) error {
			return read(velox.WithTags(ctx, "k", "v"), tx)
		}, false, ""},
		{"the transaction with the outer ctx", func(_ context.Context, _, tx *velox.DB) error { return read(outer, tx) }, false, ""},
		{"a savepoint with its ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error { return read(ctx, sp) })
		}, false, ""},
		{"a savepoint with the transaction's ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(outer, func(_ context.Context, sp *velox.DB) error { return read(ctx, sp) })
		}, false, ""},
		{"the transaction with a savepoint's ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(ctx, func(ctx context.Context, _ *velox.DB) error { return read(ctx, tx) })
		}, false, ""},
		{"a goroutine on db with the outer ctx", func(_ context.Context, db, _ *velox.DB) error {
			errc := make(chan error)
			go func() { errc <- read(outer, db) }()
			return <-errc
		}, false, ""},
		{"db with the transaction's ctx", func(ctx context.Context, db, _ *velox.DB) error { return read(ctx, db) }, true, ""},
		{"db with a ctx derived from the transaction's", func(ctx context.Context, db, _ *velox.DB) error {
			return read(velox.WithTags(ctx, "k", "v"), db)
		}, true, ""},
		{"db with a savepoint's ctx", func(ctx context.Context, db, tx *velox.DB) error {
			return tx.Tx(outer, func(ctx context.Context, _ *velox.DB) error { return read(ctx, db) })
		}, true, ""},
		{"a write on db", func(ctx context.Context, db, _ *velox.DB) error { return touch(ctx, db) }, true, ""},
		{"a dry run on db", func(ctx context.Context, db, _ *velox.DB) error {
			_, _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).SQL(ctx)
			return err
		}, true, ""},
		{"a transaction on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return read(ctx, tx) })
		}, true, "velox: transaction: statement outside its transaction"},
		{"an InsertAll in batches on db", func(ctx context.Context, db, _ *velox.DB) error {
			defer setMaxParams(db, 18)()
			return db.InsertAll([]testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}).Exec(ctx)
		}, true, ""},
		{"an Iter on db", func(ctx context.Context, db, _ *velox.DB) error {
			for _, err := range db.Query[testmodel.Invoice]().Limit(1).Iter(ctx) {
				return err
			}
			return nil
		}, true, ""},
		{"an Insert on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Insert(&testmodel.Invoice{OrgID: 1, Number: "A"}).Exec(ctx)
		}, true, ""},
		{"a Save on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Save(&testmodel.Invoice{ID: 1, OrgID: 1, Number: "A"}, I.Number).Exec(ctx)
		}, true, ""},
		{"an upsert on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Insert(&testmodel.Invoice{OrgID: 1, Number: "A"}).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx)
		}, true, ""},
		{"a DB of another Open", func(ctx context.Context, _, _ *velox.DB) error {
			return read(ctx, openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}))
		}, true, ""},
		{"another transaction's DB", func(ctx context.Context, db, _ *velox.DB) error {
			var err error
			_ = db.Tx(outer, func(_ context.Context, other *velox.DB) error {
				err = read(ctx, other)
				return nil
			})
			return err
		}, true, ""},
		{"a savepoint of another transaction", func(ctx context.Context, db, _ *velox.DB) error {
			var err error
			_ = db.Tx(outer, func(_ context.Context, other *velox.DB) error {
				err = other.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
				return nil
			})
			return err
		}, true, "velox: transaction: statement outside its transaction"},
		{"another transaction's DB with its ctx", func(_ context.Context, db, _ *velox.DB) error {
			return db.Tx(outer, func(ctx context.Context, other *velox.DB) error { return read(ctx, other) })
		}, false, ""},
		{"db with WithoutTx", func(ctx context.Context, db, _ *velox.DB) error {
			return read(velox.WithoutTx(ctx), db)
		}, false, ""},
		{"the transaction with WithoutTx", func(ctx context.Context, _, tx *velox.DB) error {
			return read(velox.WithoutTx(ctx), tx)
		}, false, ""},
		{"a transaction on db with WithoutTx", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Tx(velox.WithoutTx(ctx), func(ctx context.Context, other *velox.DB) error { return read(ctx, other) })
		}, false, ""},
		{"a transaction on db with the outer ctx", func(_ context.Context, db, _ *velox.DB) error {
			return db.Tx(outer, func(ctx context.Context, tx *velox.DB) error { return read(ctx, tx) })
		}, false, ""},
	} {
		for _, hooked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hooked=%v", c.name, hooked), func(t *testing.T) {
				var opts []velox.Option
				if hooked {
					opts = append(opts, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
						return next(ctx)
					}))
				}
				e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
				db := openFake(velox.Postgres, e, opts...)
				var got error
				if err := db.Tx(outer, func(ctx context.Context, tx *velox.DB) error {
					got = c.fn(ctx, db, tx)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				sent := 0 // what the refused statement would have sent: a query, an insert
				for _, s := range e.Log {
					if strings.Contains(s, "invoices") {
						sent++
					}
				}
				switch {
				case !c.outside && got != nil:
					t.Errorf("failed: %v", got)
				case c.outside && !errors.Is(got, velox.ErrOutsideTx):
					t.Errorf("got %v, want ErrOutsideTx", got)
				case c.outside && got.Error() != cmp.Or(c.msg, "velox: testmodel.Invoice: statement outside its transaction"):
					t.Errorf("the error says %q", got)
				case c.outside && sent != 0:
					t.Errorf("sent %q", e.Log)
				}
			})
		}
	}
}
