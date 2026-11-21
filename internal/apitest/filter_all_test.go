package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// allFilterMode is what allFilter does for INote under a context.
type allFilterMode int

const (
	allHide    allFilterMode = iota // hide the note "a": the default
	allFail                         // return errAllFilter
	allZero                         // return the zero Predicate
	allUnknown                      // name a column INote does not have
	allNone                         // return NoFilter
	allNothing                      // return Not(NoFilter()): no row
)

type allFilterKey struct{}

var errAllFilter = errors.New("all filter failed")

// allFilterLog records the entities allFilter was called for, in order.
type allFilterLog struct {
	mu   sync.Mutex
	seen []string
}

func (l *allFilterLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.seen)
}

func (l *allFilterLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = nil
}

// allFilter hides the note "a" from every statement, as the context's mode
// says, and restricts no other entity.
func allFilter(log *allFilterLog) velox.Filter {
	return func(ctx context.Context, entity string) (velox.Predicate, error) {
		log.mu.Lock()
		log.seen = append(log.seen, entity)
		log.mu.Unlock()
		if entity != "INote" {
			return velox.NoFilter(), nil
		}
		m, _ := ctx.Value(allFilterKey{}).(allFilterMode)
		switch m {
		case allFail:
			return velox.Predicate{}, errAllFilter
		case allZero:
			return velox.Predicate{}, nil
		case allUnknown:
			return velox.FieldEQ("nope", 1), nil
		case allNone:
			return velox.NoFilter(), nil
		case allNothing:
			return velox.Not(velox.NoFilter()), nil
		}
		return velox.FieldNEQ("body", "a"), nil
	}
}

func allMode(m allFilterMode) context.Context {
	return context.WithValue(context.Background(), allFilterKey{}, m)
}

// TestFilterEveryRead: with the interceptor hiding note 2, the Read policy
// note 3 and WithFilter note 1, Query, One, Count, Exists, Iter, an
// aggregate, a grouped aggregate and a paged read see note 4 alone.
func TestFilterEveryRead(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	ctx := context.Background()
	q := db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded()
	all, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "All", noteIDs(all), 4)
	if _, err := q.Where(fixture.INotes.ID.EQ(1)).One(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("One of the filtered note: %v", err)
	}
	if n, err := q.Count(ctx); err != nil || n != 1 {
		t.Errorf("Count: %d, %v", n, err)
	}
	if ok, err := q.Where(fixture.INotes.ID.EQ(1)).Exists(ctx); err != nil || ok {
		t.Errorf("Exists of the filtered note: %v, %v", ok, err)
	}
	var iterated []int64
	for n, err := range q.Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		iterated = append(iterated, n.ID)
	}
	wantIDs(t, "Iter", iterated, 4)
	if sum, err := velox.Sum(ctx, q, fixture.INotes.ID); err != nil || sum != 4 {
		t.Errorf("Sum: %d, %v", sum, err)
	}
	groups, err := velox.CountBy(ctx, q, fixture.INotes.OwnerID.Key())
	if err != nil || len(groups) != 1 || groups[0].Value != 1 {
		t.Errorf("CountBy: %v, %v", groups, err)
	}
	page, err := db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "first page", noteIDs(page), 4)
	// Under NoFilter the filter restricts nothing.
	all, err = q.All(allMode(allNone))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "All under NoFilter", noteIDs(all), 1, 4)
	all, err = q.All(allMode(allNothing))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "All under Not(NoFilter())", noteIDs(all))
}

// TestFilterReachesRelations: a With load, to-one, to-many and through a
// join entity, Has and HasWith, a Join's ON, LoadEach and CountEach see
// INote's rows as a query on it does.
func TestFilterReachesRelations(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	ctx := context.Background()
	owners, err := db.Query[fixture.IOwner]().With(fixture.IOwners.Notes.OrderBy(fixture.INotes.ID.Asc())).With(fixture.IOwners.Pinned).With(fixture.IOwners.Tagged).
		OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	notes0, _ := owners[0].Notes.Get()
	notes1, _ := owners[1].Notes.Get()
	wantIDs(t, "owner 1's notes", noteIDs(notes0))
	wantIDs(t, "owner 2's notes", noteIDs(notes1), 4)
	if p, ok := owners[1].Pinned.Get(); !ok || p != nil {
		t.Errorf("owner 2's pinned note, filtered: %v %v", p, ok)
	}
	tagged0, _ := owners[0].Tagged.Get()
	wantIDs(t, "owner 1's tagged notes", noteIDs(tagged0))

	where := func(p velox.Pred[fixture.IOwner]) []int64 {
		t.Helper()
		os, err := db.Query[fixture.IOwner]().Where(p).OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ownerIDs(os)
	}
	wantIDs(t, "Notes.Has", where(fixture.IOwners.Notes.Has()), 2)
	wantIDs(t, "Notes.HasWith(a)", where(fixture.IOwners.Notes.HasWith(fixture.INotes.Body.EQ("a"))))
	wantIDs(t, "Tagged.Has", where(fixture.IOwners.Tagged.Has()))

	joined, err := db.Query[fixture.IOwner]().Join(fixture.IOwners.Pinned).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Join(Pinned)", ownerIDs(joined))

	ptrs := []*fixture.IOwner{&owners[0], &owners[1]}
	counts, err := velox.CountEach(ctx, db.Query[fixture.INote](), fixture.IOwners.Notes, ptrs)
	if err != nil || !slices.Equal(counts, []int64{0, 1}) {
		t.Errorf("CountEach: %v, %v", counts, err)
	}
	each, err := velox.LoadEach(ctx, db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()), fixture.IOwners.Notes, ptrs)
	if err != nil || len(each) != 2 || len(each[0]) != 0 || len(each[1]) != 1 || each[1][0].ID != 4 {
		t.Errorf("LoadEach: %v, %v", each, err)
	}
}

// TestFilterFailsClosedOnRelations: an error, a zero Predicate or an
// unknown column from the filter for the target fails a With load, a Has, a
// HasWith and a Join, and no row comes back; a query that does not reach
// the target does not call it for the target.
func TestFilterFailsClosedOnRelations(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	owners := func() velox.Query[fixture.IOwner] {
		return db.Query[fixture.IOwner]().OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded()
	}
	reads := map[string]velox.Query[fixture.IOwner]{
		"With(Notes)":   owners().With(fixture.IOwners.Notes),
		"With(Pinned)":  owners().With(fixture.IOwners.Pinned),
		"With(Tagged)":  owners().With(fixture.IOwners.Tagged),
		"Notes.Has":     owners().Where(fixture.IOwners.Notes.Has()),
		"Notes.HasWith": owners().Where(fixture.IOwners.Notes.HasWith(fixture.INotes.Body.EQ("c"))),
		"Join(Pinned)":  owners().Join(fixture.IOwners.Pinned),
	}
	for _, c := range []struct {
		mode allFilterMode
		want string
	}{
		{allFail, "all filter failed"},
		{allZero, "zero Predicate"},
		{allUnknown, "nope"},
	} {
		for name, q := range reads {
			got, err := q.All(allMode(c.mode))
			if err == nil || !strings.Contains(err.Error(), c.want) || got != nil {
				t.Errorf("mode %d, %s: %v rows, %v; want an error with %q", c.mode, name, len(got), err, c.want)
			}
			if c.mode == allFail && !errors.Is(err, errAllFilter) {
				t.Errorf("%s: errors.Is does not reach the filter's error: %v", name, err)
			}
			if errors.Is(err, velox.ErrDenied) {
				t.Errorf("%s: a filter's error is a denial: %v", name, err)
			}
		}
	}
	log.reset()
	if os, err := owners().All(allMode(allFail)); err != nil || len(os) != 2 {
		t.Errorf("owners alone: %d, %v", len(os), err)
	}
	if got := log.names(); !slices.Equal(got, []string{"IOwner"}) {
		t.Errorf("the filter was called for %v, want [IOwner]", got)
	}
}

// TestFilterReachesWrites: Update, Delete and Save change no row the filter
// hides.
func TestFilterReachesWrites(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	ctx := context.Background()
	if n, err := db.Update[fixture.INote]().Set(fixture.INotes.OwnerID.Set(9)).All().Exec(ctx); err != nil || n != 1 {
		t.Errorf("Update all: %d, %v; want 1", n, err)
	}
	filtered := fixture.INote{ID: 1, OwnerID: 1, Body: "a2"}
	if err := db.Save(&filtered, fixture.INotes.Body).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("Save of the filtered note: %v", err)
	}
	if n, err := db.Delete[fixture.INote]().Where(fixture.INotes.ID.In(1, 4)).Exec(ctx); err != nil || n != 1 {
		t.Errorf("Delete 1 and 4: %d, %v; want 1", n, err)
	}
	if _, err := db.Delete[fixture.INote]().Where(fixture.INotes.ID.EQ(1)).Exec(allMode(allZero)); err == nil {
		t.Error("a delete under the zero Predicate ran")
	}
}

// TestFilterNotLiftedByWithoutPolicy: WithoutPolicy shows the note the Read
// policy hides, and not the one the filter hides.
func TestFilterNotLiftedByWithoutPolicy(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log))).WithoutPolicy("test")
	all, err := db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "All without policy", noteIDs(all), 3, 4)
	if _, err := db.Query[fixture.INote]().AllowUnbounded().All(allMode(allFail)); !errors.Is(err, errAllFilter) {
		t.Errorf("the filter's error without policy: %v", err)
	}
	owners, err := db.Query[fixture.IOwner]().Where(fixture.IOwners.Notes.HasWith(fixture.INotes.Body.EQ("a"))).AllowUnbounded().All(context.Background())
	if err != nil || len(owners) != 0 {
		t.Errorf("HasWith without policy: %v, %v", ownerIDs(owners), err)
	}
}

// TestFiltersAndInOrder: two filters AND together, the first called first,
// once per statement for each entity it reads, with the statement's
// context.
func TestFiltersAndInOrder(t *testing.T) {
	var calls []string
	type key struct{}
	f := func(name, body string) velox.Filter {
		return func(ctx context.Context, entity string) (velox.Predicate, error) {
			calls = append(calls, name+":"+entity+":"+ctx.Value(key{}).(string))
			if entity != "INote" {
				return velox.NoFilter(), nil
			}
			return velox.FieldNEQ("body", body), nil
		}
	}
	db := interceptorDB(t, velox.WithFilter(f("f1", "a")), velox.WithFilter(f("f2", "c")))
	ctx := context.WithValue(context.Background(), key{}, "stmt")
	all, err := db.Query[fixture.INote]().AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "two filters", noteIDs(all))
	if want := []string{"f1:INote:stmt", "f2:INote:stmt"}; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
	calls = nil
	_, err = db.Query[fixture.IOwner]().
		Where(fixture.IOwners.Notes.Has(), fixture.IOwners.Notes.HasWith(fixture.INotes.Body.EQ("b")), fixture.IOwners.Tagged.Has()).
		AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"f1:IOwner:stmt", "f2:IOwner:stmt", "f1:INote:stmt", "f2:INote:stmt"}; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
	// A second statement calls them again: nothing is kept between
	// statements.
	calls = nil
	if _, err := db.Query[fixture.IOwner]().AllowUnbounded().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"f1:IOwner:stmt", "f2:IOwner:stmt"}; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
}

// TestFilterErrorText: the error names the entity and the filter's index.
func TestFilterErrorText(t *testing.T) {
	var log allFilterLog
	db := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	q := db.Query[fixture.INote]().AllowUnbounded()
	for mode, want := range map[allFilterMode]string{
		allFail:    "velox: fixture.INote: filter [0]: all filter failed",
		allZero:    "velox: fixture.INote: filter [0] returned the zero Predicate; return velox.NoFilter() for no restriction",
		allUnknown: `velox: fixture.INote: filter [0]'s condition: `,
	} {
		_, err := q.All(allMode(mode))
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("mode %d: %v, want %q", mode, err, want)
		}
	}
}

// TestFilterPanicsOnNil: a nil Filter panics, as a nil Rewrite does.
func TestFilterPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WithFilter(nil) did not panic")
		}
	}()
	velox.WithFilter(nil)
}

// TestFilterUpsertOnMySQL: an upsert that updates is refused on MySQL only
// when the filter restricts its entity.
func TestFilterUpsertOnMySQL(t *testing.T) {
	var log allFilterLog
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.MySQL, e, velox.WithFilter(allFilter(&log)))
	up := db.Insert(&fixture.DBook{ID: 1, AuthorID: 1, Title: "t"}).OnConflict(fixture.DBooks.ID).Update(fixture.DBooks.Title)
	if err := up.Exec(context.Background()); err != nil {
		t.Errorf("an upsert of an entity the filter leaves alone: %v", err)
	}
	ow := db.Insert(&fixture.IOwner{ID: 1, Name: "o"}).OnConflict(fixture.IOwners.ID).Update(fixture.IOwners.Name)
	if err := ow.Exec(context.Background()); err != nil {
		t.Errorf("an upsert of IOwner: %v", err)
	}
	r := velox.WithFilter(func(context.Context, string) (velox.Predicate, error) { return velox.FieldEQ("name", "o"), nil })
	rdb := openPolicied(velox.MySQL, e, r)
	err := rdb.Insert(&fixture.IOwner{ID: 1, Name: "o"}).OnConflict(fixture.IOwners.ID).Update(fixture.IOwners.Name).Exec(context.Background())
	if !errors.Is(err, velox.ErrPolicyUpsert) {
		t.Errorf("an upsert of a filtered entity on MySQL: %v", err)
	}
}

// TestHooksAllOrderAndEntity: WithHooksAll's hooks run for every entity,
// in the order given, outside the schema's hooks and WithHooks's, and
// Mutation.Entity names the entity.
func TestHooksAllOrderAndEntity(t *testing.T) {
	fixture.Trace = nil
	named := func(name string) velox.Hook {
		return func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				fixture.Trace = append(fixture.Trace, name+" "+m.Entity())
				return next.Mutate(ctx, m)
			})
		}
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1},
		velox.WithHooks[fixture.DBook](named("typed")),
		velox.WithHooksAll(named("all1"), named("all2")))
	if _, err := db.Delete[fixture.DBook]().Where(fixture.DBooks.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := "all1 DBook|all2 DBook|outer before delete|delete only|typed DBook|outer after"
	if got := strings.Join(fixture.Trace, "|"); got != want {
		t.Errorf("trace %s, want %s", got, want)
	}
	fixture.Trace = nil
	if _, err := db.Delete[fixture.IOwner]().Where(fixture.IOwners.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fixture.Trace, "|"); got != "all1 IOwner|all2 IOwner" {
		t.Errorf("trace for an entity with no hooks of its own: %s", got)
	}
	fixture.Trace = nil
	if err := db.Insert(&fixture.IOwner{ID: 3, Name: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fixture.Trace, "|"); got != "all1 IOwner|all2 IOwner" {
		t.Errorf("trace for an insert: %s", got)
	}
}

// TestHooksAllRefuse: an error from a WithHooksAll hook fails the write,
// wrapped as a hook's, and nothing is sent.
func TestHooksAllRefuse(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	refuse := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		if m.Entity() == "IOwner" {
			return fixture.ErrRefused
		}
		return nil
	}, velox.OpUpdate, velox.OpDelete)
	db := openFake(velox.Postgres, e, velox.WithHooksAll(refuse))
	_, err := db.Delete[fixture.IOwner]().Where(fixture.IOwners.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, fixture.ErrRefused) || err.Error() != "velox: fixture.IOwner: hook: refused" {
		t.Errorf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestHooksAllPanicsOnNil: a nil hook panics, as WithHooks's does.
func TestHooksAllPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WithHooksAll(nil) did not panic")
		}
	}()
	velox.WithHooksAll(nil)
}
