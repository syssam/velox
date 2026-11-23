package apitest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/privacy"
)

// resetPNote clears PNote's behaviour functions when the test ends.
func resetPNote(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		fixture.PNoteRead, fixture.PNoteWrite, fixture.PNoteInsert = nil, nil, nil
		fixture.PNoteQuery, fixture.PNoteFilter = nil, nil
	})
}

// joined is the DB a policy that captured db reads through: the
// transaction ctx belongs to, joined with TxFrom, or db outside one, as
// ent's rules use ent.TxFromContext.
func joined(ctx context.Context, db *velox.DB) *velox.DB {
	if tx, ok := velox.TxFrom(ctx, db); ok {
		return tx
	}
	return db
}

// ownerO2 looks up the owner "o2" through the DB read returns for ctx, as
// a team lookup does. A lookup that fails is recorded in errs, and the
// caller narrows to nothing, as nextapp's lookups do.
func ownerO2(ctx context.Context, read func(context.Context) *velox.DB, errs *[]error) (int64, bool) {
	os, err := read(ctx).Query[fixture.IOwner]().Where(fixture.IOwners.Name.EQ("o2")).Limit(1).All(ctx)
	if err != nil {
		*errs = append(*errs, err)
		return 0, false
	}
	return os[0].ID, len(os) == 1
}

// ownerCond is the condition of the notes of owner "o2", read as ownerO2.
func ownerCond(ctx context.Context, read func(context.Context) *velox.DB, errs *[]error) velox.Predicate {
	id, ok := ownerO2(ctx, read, errs)
	if !ok {
		return velox.None()
	}
	return velox.FieldEQ("owner_id", id)
}

// TestPolicyReadsThroughTxFrom: a Read policy, a privacy Query rule and an
// interceptor's Filter that read through the transaction TxFrom joins give
// a statement the same rows inside a Tx and outside it, on SQLite's pool
// of one connection. Each reading on the DB it captured, without TxFrom,
// fails inside the Tx with ErrOutsideTx and narrows to nothing.
func TestPolicyReadsThroughTxFrom(t *testing.T) {
	resetPNote(t)
	o2 := context.Background()
	var db *velox.DB
	for _, read := range []struct {
		name string
		db   func(context.Context) *velox.DB
	}{
		{"TxFrom", func(ctx context.Context) *velox.DB { return joined(ctx, db) }},
		{"captured", func(context.Context) *velox.DB { return db }},
	} {
		for _, kind := range []string{"Read", "Query", "Interceptor"} {
			t.Run(read.name+"/"+kind, func(t *testing.T) {
				var errs []error
				fixture.PNoteRead, fixture.PNoteQuery, fixture.PNoteFilter = nil, nil, nil
				switch kind {
				case "Read":
					fixture.PNoteRead = func(ctx context.Context) (velox.Predicate, error) {
						return ownerCond(ctx, read.db, &errs), nil
					}
				case "Query":
					fixture.PNoteQuery = func(ctx context.Context, f privacy.Filter) error {
						f.Where(ownerCond(ctx, read.db, &errs))
						return privacy.Skip
					}
				case "Interceptor":
					fixture.PNoteFilter = func(ctx context.Context) (velox.Predicate, error) {
						return ownerCond(ctx, read.db, &errs), nil
					}
				}
				db = interceptorDB(t)
				count := func(ctx context.Context, db *velox.DB) int64 {
					t.Helper()
					n, err := db.Query[fixture.PNote]().Count(ctx)
					if err != nil {
						t.Fatal(err)
					}
					return n
				}
				outside := count(o2, db)
				var inside int64
				if err := db.Tx(o2, func(ctx context.Context, tx *velox.DB) error {
					inside = count(ctx, tx)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if read.name == "TxFrom" {
					if outside != 1 || inside != 1 || len(errs) != 0 {
						t.Errorf("%d notes outside a Tx, %d inside, want 1 and 1; lookups failed: %v", outside, inside, errs)
					}
					return
				}
				if outside != 1 || inside != 0 || len(errs) != 1 || !errors.Is(errs[0], velox.ErrOutsideTx) {
					t.Errorf("%d notes outside a Tx, %d inside, want 1 and 0; lookups failed: %v, want ErrOutsideTx", outside, inside, errs)
				}
			})
		}
	}
}

// TestInsertPolicyReadsInTx: an Insert policy that reads through TxFrom
// judges a row inside a Tx as outside it.
func TestInsertPolicyReadsInTx(t *testing.T) {
	resetPNote(t)
	ctx := context.Background()
	db := interceptorDB(t)
	var errs []error
	fixture.PNoteInsert = func(ctx context.Context, r velox.Row) error {
		id, ok := ownerO2(ctx, func(ctx context.Context) *velox.DB { return joined(ctx, db) }, &errs)
		if !ok || r.Value("owner_id") != id {
			return velox.Deny("not o2's")
		}
		return nil
	}
	if err := db.Insert(&fixture.PNote{ID: 10, OwnerID: 2, Body: "out"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Insert(&fixture.PNote{ID: 11, OwnerID: 1, Body: "o1's"}).Exec(ctx); !errors.Is(err, velox.ErrDenied) {
			return fmt.Errorf("owner 1's note: %v, want ErrDenied", err)
		}
		return tx.Insert(&fixture.PNote{ID: 12, OwnerID: 2, Body: "in"}).Exec(ctx)
	}); err != nil || len(errs) != 0 {
		t.Errorf("inserts in a Tx: %v; lookups failed: %v", err, errs)
	}
}

// TestBehaviourContextNames: a policy function and a privacy rule are
// given a context named .Policy, an interceptor one named .Interceptor.
func TestBehaviourContextNames(t *testing.T) {
	resetPNote(t)
	seen := map[string]bool{}
	note := func(kind, suffix string, ctx context.Context) {
		seen[kind] = true
		if s := fmt.Sprint(ctx); !strings.HasSuffix(s, suffix) {
			t.Errorf("%s's context is named %q, want the suffix %s", kind, s, suffix)
		}
	}
	fixture.PNoteRead = func(ctx context.Context) (velox.Predicate, error) {
		note("Read", ".Policy", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteQuery = func(ctx context.Context, _ privacy.Filter) error {
		note("Query", ".Policy", ctx)
		return privacy.Skip
	}
	fixture.PNoteFilter = func(ctx context.Context) (velox.Predicate, error) {
		note("Interceptor", ".Interceptor", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteWrite = func(ctx context.Context) (velox.Predicate, error) {
		note("Write", ".Policy", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteInsert = func(ctx context.Context, _ velox.Row) error {
		note("Insert", ".Policy", ctx)
		return nil
	}
	db := interceptorDB(t)
	ctx := context.Background()
	if _, err := db.Query[fixture.PNote]().Count(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[fixture.PNote]().Where(fixture.PNotes.ID.EQ(1)).Set(fixture.PNotes.Body.Set("a2")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&fixture.PNote{OwnerID: 1, Body: "n"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Errorf("called: %v, want all five", seen)
	}
}

// TestPolicyReadsWithoutItself: a Read policy that reads its own entity
// ends, its read not narrowed by it, inside a Tx and outside; the
// interceptor still narrows that read. Before, each read called the
// policy again without end.
func TestPolicyReadsWithoutItself(t *testing.T) {
	resetPNote(t)
	db := interceptorDB(t)
	calls := 0
	var seen []int64
	fixture.PNoteFilter = func(context.Context) (velox.Predicate, error) {
		return velox.FieldEQ("hidden", false), nil
	}
	fixture.PNoteRead = func(ctx context.Context) (velox.Predicate, error) {
		calls++
		if calls > 10 {
			return velox.Predicate{}, errors.New("the policy called itself without end")
		}
		ns, err := joined(ctx, db).Query[fixture.PNote]().OrderBy(fixture.PNotes.ID.Desc()).Limit(10).All(ctx)
		if err != nil {
			return velox.Predicate{}, err
		}
		for _, n := range ns {
			seen = append(seen, n.ID)
		}
		return velox.FieldNEQ("id", ns[0].ID), nil // all but the newest
	}
	read := func(ctx context.Context, db *velox.DB) {
		t.Helper()
		calls, seen = 0, nil
		ns, err := db.Query[fixture.PNote]().OrderBy(fixture.PNotes.ID.Asc()).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(ns))
		for i, n := range ns {
			ids[i] = n.ID
		}
		wantIDs(t, "notes", ids, 1, 3)
		wantIDs(t, "the policy's read", seen, 4, 3, 1)
		if calls != 1 {
			t.Errorf("the policy ran %d times, want 1", calls)
		}
	}
	read(context.Background(), db)
	if err := db.Tx(context.Background(), func(ctx context.Context, tx *velox.DB) error {
		read(ctx, tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
