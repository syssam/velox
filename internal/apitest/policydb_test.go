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

// ownerO2 looks up the owner "o2" on db, as a team lookup does. A lookup
// that fails is recorded in errs, and the caller narrows to nothing, as
// nextapp's lookups do.
func ownerO2(ctx context.Context, db *velox.DB, errs *[]error) (int64, bool) {
	os, err := db.Query[fixture.IOwner]().Where(fixture.IOwners.Name.EQ("o2")).Limit(1).All(ctx)
	if err != nil {
		*errs = append(*errs, err)
		return 0, false
	}
	return os[0].ID, len(os) == 1
}

// ownerCond is the condition of the notes of owner "o2", read as ownerO2.
func ownerCond(ctx context.Context, db *velox.DB, errs *[]error) velox.Predicate {
	id, ok := ownerO2(ctx, db, errs)
	if !ok {
		return velox.None()
	}
	return velox.FieldEQ("owner_id", id)
}

// TestPolicyReadsThroughItsDB: a Read policy, a privacy Query rule and an
// interceptor's Filter that read through the DB they are given (the
// policy's and the Filter's db, the rule's q.DB()) give a statement the
// same rows inside a Tx and outside it, on SQLite's pool of one
// connection, with no package-level DB and no TxFrom. Each reading on a
// DB it captured fails inside the Tx with ErrOutsideTx and narrows to
// nothing.
func TestPolicyReadsThroughItsDB(t *testing.T) {
	resetPNote(t)
	o2 := context.Background()
	var db *velox.DB
	for _, given := range []bool{true, false} {
		for _, kind := range []string{"Read", "Query", "Interceptor"} {
			name := map[bool]string{true: "given", false: "captured"}[given] + "/" + kind
			t.Run(name, func(t *testing.T) {
				var errs []error
				pick := func(stmt *velox.DB) *velox.DB {
					if given {
						return stmt
					}
					return db
				}
				fixture.PNoteRead, fixture.PNoteQuery, fixture.PNoteFilter = nil, nil, nil
				switch kind {
				case "Read":
					fixture.PNoteRead = func(ctx context.Context, stmt *velox.DB) (velox.Predicate, error) {
						return ownerCond(ctx, pick(stmt), &errs), nil
					}
				case "Query":
					fixture.PNoteQuery = func(ctx context.Context, q privacy.Query) error {
						q.Filter().Where(ownerCond(ctx, pick(q.DB()), &errs))
						return privacy.Skip
					}
				case "Interceptor":
					fixture.PNoteFilter = func(ctx context.Context, stmt *velox.DB) (velox.Predicate, error) {
						return ownerCond(ctx, pick(stmt), &errs), nil
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
				if given {
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

// TestPolicyGetsTheStatementsDB: every policy function and rule, and an
// interceptor's Filter, is given the DB its statement runs on: the
// transaction's inside Tx, never nil.
func TestPolicyGetsTheStatementsDB(t *testing.T) {
	resetPNote(t)
	got := map[string]*velox.DB{}
	fixture.PNoteRead = func(_ context.Context, db *velox.DB) (velox.Predicate, error) {
		got["Read"] = db
		return velox.Allow(), nil
	}
	fixture.PNoteWrite = func(_ context.Context, db *velox.DB) (velox.Predicate, error) {
		got["Write"] = db
		return velox.Allow(), nil
	}
	fixture.PNoteInsert = func(_ context.Context, db *velox.DB, _ velox.Row) error {
		got["Insert"] = db
		return nil
	}
	fixture.PNoteQuery = func(_ context.Context, q privacy.Query) error {
		got["Query"] = q.DB()
		return privacy.Skip
	}
	fixture.PNoteFilter = func(_ context.Context, db *velox.DB) (velox.Predicate, error) {
		got["Interceptor"] = db
		return velox.Allow(), nil
	}
	db := interceptorDB(t)
	run := func(ctx context.Context, db *velox.DB, body string) {
		t.Helper()
		clear(got)
		if _, err := db.Query[fixture.PNote]().Count(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Update[fixture.PNote]().Where(fixture.PNotes.ID.EQ(1)).Set(fixture.PNotes.Body.Set("changed")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Insert(&fixture.PNote{OwnerID: 1, Body: body}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"Read", "Write", "Insert", "Query", "Interceptor"} {
			if got[k] != db {
				t.Errorf("%s was given %p, want the statement's DB %p", k, got[k], db)
			}
		}
	}
	run(context.Background(), db, "outside")
	if err := db.Tx(context.Background(), func(ctx context.Context, tx *velox.DB) error {
		run(ctx, tx, "inside")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestInsertPolicyReadsInTx: an Insert policy that reads through its db
// judges a row inside a Tx as outside it.
func TestInsertPolicyReadsInTx(t *testing.T) {
	resetPNote(t)
	ctx := context.Background()
	db := interceptorDB(t)
	var errs []error
	fixture.PNoteInsert = func(ctx context.Context, db *velox.DB, r velox.Row) error {
		id, ok := ownerO2(ctx, db, &errs)
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
	fixture.PNoteRead = func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		note("Read", ".Policy", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteQuery = func(ctx context.Context, _ privacy.Query) error {
		note("Query", ".Policy", ctx)
		return privacy.Skip
	}
	fixture.PNoteFilter = func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		note("Interceptor", ".Interceptor", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteWrite = func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		note("Write", ".Policy", ctx)
		return velox.Allow(), nil
	}
	fixture.PNoteInsert = func(ctx context.Context, _ *velox.DB, _ velox.Row) error {
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
	fixture.PNoteFilter = func(context.Context, *velox.DB) (velox.Predicate, error) {
		return velox.FieldEQ("hidden", false), nil
	}
	fixture.PNoteRead = func(ctx context.Context, db *velox.DB) (velox.Predicate, error) {
		calls++
		if calls > 10 {
			return velox.Predicate{}, errors.New("the policy called itself without end")
		}
		ns, err := db.Query[fixture.PNote]().OrderBy(fixture.PNotes.ID.Desc()).Limit(10).All(ctx)
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
