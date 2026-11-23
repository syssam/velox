package apitest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

type filterOwnerKey struct{}

// ownerScope is the shape of a row scope that reads to make its condition,
// as a team lookup does: the notes of the owner named in the context, whose
// key the filter reads through read's DB. The fixture's DB is SQLite with a
// pool of one connection.
func ownerScope(read func(ctx context.Context) *velox.DB, errs *[]error) velox.Filter {
	return func(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
		name, _ := ctx.Value(filterOwnerKey{}).(string)
		if entity != "INote" || op != velox.OpSelect || name == "" {
			return velox.Allow(), nil
		}
		owners, err := read(ctx).Query[fixture.IOwner]().Where(fixture.IOwners.Name.EQ(name)).Limit(10).All(ctx)
		if err != nil {
			*errs = append(*errs, err)
			return velox.None(), nil // a lookup that fails narrows, as nextapp's do
		}
		ids := make([]any, len(owners))
		for i, o := range owners {
			ids[i] = o.ID
		}
		return velox.FieldIn("owner_id", ids...), nil
	}
}

// TestFilterReadsThroughTheStatementsDB: a filter that reads through
// FilterDB gives a statement the same rows inside a Tx and outside it, on
// SQLite's pool of one connection. The same filter reading on the DB it
// captured fails inside the Tx with ErrOutsideTx, and under WithoutTx it
// would wait for the connection the Tx holds, which is refused: the
// statement then sees fewer rows inside the Tx than outside.
func TestFilterReadsThroughTheStatementsDB(t *testing.T) {
	var errs []error
	db := interceptorDB(t, velox.WithFilter(ownerScope(velox.FilterDB, &errs)))
	o2 := context.WithValue(context.Background(), filterOwnerKey{}, "o2")
	count := func(ctx context.Context, db *velox.DB) int64 {
		t.Helper()
		n, err := db.Query[fixture.INote]().Count(ctx)
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
	if outside != 1 || inside != 1 || len(errs) != 0 {
		t.Errorf("FilterDB: %d notes outside a Tx, %d inside, want 1 and 1; lookups failed: %v", outside, inside, errs)
	}

	for _, withoutTx := range []bool{false, true} {
		var errs []error
		var db *velox.DB
		f := ownerScope(func(context.Context) *velox.DB { return db }, &errs)
		if withoutTx {
			inner := f
			f = func(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
				return inner(velox.WithoutTx(ctx), entity, op)
			}
		}
		db = interceptorDB(t, velox.WithFilter(f))
		outside := count(o2, db)
		var inside int64
		if err := db.Tx(o2, func(ctx context.Context, tx *velox.DB) error {
			inside = count(ctx, tx)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if outside != 1 || inside != 0 || len(errs) != 1 || !withoutTx && !errors.Is(errs[0], velox.ErrOutsideTx) {
			t.Errorf("captured DB, WithoutTx %v: %d notes outside a Tx, %d inside; lookups failed: %v", withoutTx, outside, inside, errs)
		}
	}
}

// TestFilterDBIsTheStatementsDB: FilterDB gives the filter the DB the
// statement runs on, the transaction's inside Tx, and nil under a context no
// filter was given.
func TestFilterDBIsTheStatementsDB(t *testing.T) {
	var got []*velox.DB
	db := interceptorDB(t, velox.WithFilter(func(ctx context.Context, entity string, _ velox.Op) (velox.Predicate, error) {
		if entity == "INote" {
			got = append(got, velox.FilterDB(ctx))
			if s := fmt.Sprint(ctx); !strings.HasSuffix(s, ".WithFilterDB") {
				t.Errorf("the filter's context is named %q", s)
			}
		}
		return velox.Allow(), nil
	}))
	ctx := context.Background()
	if velox.FilterDB(ctx) != nil {
		t.Error("FilterDB outside a filter is not nil")
	}
	if _, err := db.Query[fixture.INote]().Count(ctx); err != nil {
		t.Fatal(err)
	}
	var txDB *velox.DB
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		txDB = tx
		_, err := tx.Query[fixture.INote]().Count(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != db || got[1] != txDB {
		t.Errorf("FilterDB gave %v; want the DB %p, then the Tx %p", got, db, txDB)
	}
}

// TestFilterReadsWithoutItself: a filter's own reads are not narrowed by
// it, nor by any filter whose reads made them, so a filter that reads its
// own entity ends; they are narrowed by the DB's other filters, the
// interceptors and the policy.
//
// newest narrows INote to the newest note its read sees; noC hides the note
// "c" and asks whether owner 1 has a tagged note, an EXISTS through the join
// entity IJoin and on INote. The interceptor hides note 2 and the policy
// note 3. The statement calls newest, whose read calls noC (newest is
// skipped), whose EXISTS calls neither: owner 1 has note 1 tagged, noC hides
// note 4, and newest reads note 1. The statement then calls noC, whose
// EXISTS calls newest (noC is skipped), whose read calls neither: it reads
// note 4, so the EXISTS on notes of id 4 finds none of owner 1's.
func TestFilterReadsWithoutItself(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	var seen []int64
	var tagged []bool
	newest := func(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
		if entity != "INote" {
			return velox.Allow(), nil
		}
		ns, err := velox.FilterDB(ctx).Query[fixture.INote]().OrderBy(fixture.INotes.ID.Desc()).Limit(1).All(ctx)
		if err != nil {
			return velox.Predicate{}, err
		}
		mu.Lock()
		defer mu.Unlock()
		calls["newest"]++
		if len(ns) == 0 {
			return velox.None(), nil
		}
		seen = append(seen, ns[0].ID)
		return velox.FieldEQ("id", ns[0].ID), nil
	}
	noC := func(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
		if entity != "INote" {
			return velox.Allow(), nil
		}
		ok, err := velox.FilterDB(ctx).Query[fixture.IOwner]().Where(fixture.IOwners.ID.EQ(1), fixture.IOwners.Tagged.Has()).Exists(ctx)
		if err != nil {
			return velox.Predicate{}, err
		}
		mu.Lock()
		defer mu.Unlock()
		calls["noC"]++
		tagged = append(tagged, ok)
		return velox.FieldNEQ("body", "c"), nil
	}
	db := interceptorDB(t, velox.WithFilter(newest), velox.WithFilter(noC))
	ns, err := db.Query[fixture.INote]().AllowUnbounded().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "notes", noteIDs(ns), 1)
	if !slices.Equal(seen, []int64{1, 4}) || !slices.Equal(tagged, []bool{true, false}) {
		t.Errorf("newest saw %v, want [1 4]; noC's EXISTS %v, want [true false]", seen, tagged)
	}
	if calls["newest"] != 2 || calls["noC"] != 2 {
		t.Errorf("calls %v, want 2 of each", calls)
	}
}
