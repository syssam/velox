package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// interceptorDDL is the schema of the fixture's fixture.IOwner, fixture.INote and IJoin.
var interceptorDDL = []string{
	`CREATE TABLE "owners" ("id" integer PRIMARY KEY, "name" text NOT NULL, "pinned_id" integer)`,
	`CREATE TABLE "notes" ("id" integer PRIMARY KEY, "owner_id" integer NOT NULL, "body" text NOT NULL UNIQUE, "hidden" boolean NOT NULL)`,
	`CREATE TABLE "owner_notes" ("owner_id" integer NOT NULL, "note_id" integer NOT NULL, PRIMARY KEY ("owner_id", "note_id"))`,
}

// interceptorDB opens a SQLite database with the fixture's tables and rows:
// owners 1 (pinned note 2) and 2 (pinned note 1); notes 1 "a" and 2 "b",
// hidden, and 3 "secret", which the Read policy hides, of owner 1, and 4 "c"
// of owner 2; and the links (1, 1), (1, 2) and (2, 2). The default context
// sees notes 1 and 4.
func interceptorDB(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "interceptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range append(interceptorDDL,
		`INSERT INTO "owners" VALUES (1, 'o1', 2), (2, 'o2', 1)`,
		`INSERT INTO "notes" VALUES (1, 1, 'a', false), (2, 1, 'b', true), (3, 1, 'secret', false), (4, 2, 'c', false)`,
		`INSERT INTO "owner_notes" VALUES (1, 1), (1, 2), (2, 2)`,
	) {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw)
}

func noteIDs(ns []fixture.INote) []int64 {
	out := []int64{}
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

func ownerIDs(os []fixture.IOwner) []int64 {
	out := []int64{}
	for _, o := range os {
		out = append(out, o.ID)
	}
	return out
}

func wantIDs(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if want == nil {
		want = []int64{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

// TestInterceptorFiltersEveryRead: Query, One, Count, Exists, Iter, the
// aggregates and a grouped aggregate see only the rows the interceptor and
// the Read policy let through.
func TestInterceptorFiltersEveryRead(t *testing.T) {
	db := interceptorDB(t)
	ctx := context.Background()
	q := db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded()
	all, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "All", noteIDs(all), 1, 4)
	if _, err := q.Where(fixture.INotes.ID.EQ(2)).One(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("One of the hidden note: %v", err)
	}
	if n, err := q.Count(ctx); err != nil || n != 2 {
		t.Errorf("Count: %d, %v", n, err)
	}
	if ok, err := q.Where(fixture.INotes.ID.EQ(2)).Exists(ctx); err != nil || ok {
		t.Errorf("Exists of the hidden note: %v, %v", ok, err)
	}
	var iterated []int64
	for n, err := range q.Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		iterated = append(iterated, n.ID)
	}
	wantIDs(t, "Iter", iterated, 1, 4)
	if sum, err := velox.Sum(ctx, q, fixture.INotes.ID); err != nil || sum != 5 {
		t.Errorf("Sum: %d, %v", sum, err)
	}
	if m, ok, err := velox.Max(ctx, q, fixture.INotes.ID); err != nil || !ok || m != 4 {
		t.Errorf("Max: %d, %v, %v", m, ok, err)
	}
	groups, err := velox.CountBy(ctx, q, fixture.INotes.OwnerID.Key())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Value != 1 || groups[1].Value != 1 {
		t.Errorf("CountBy: %v", groups)
	}
}

// TestInterceptorReachesRelations: a With load, to-one, to-many and through
// a join entity, Has and HasWith, a Join's ON, LoadEach and CountEach see
// the target's rows as a query on it does.
func TestInterceptorReachesRelations(t *testing.T) {
	db := interceptorDB(t)
	ctx := context.Background()
	owners, err := db.Query[fixture.IOwner]().With(fixture.IOwners.Notes.OrderBy(fixture.INotes.ID.Asc())).With(fixture.IOwners.Pinned).With(fixture.IOwners.Tagged).
		OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	notes0, _ := owners[0].Notes.Get()
	notes1, _ := owners[1].Notes.Get()
	wantIDs(t, "owner 1's notes", noteIDs(notes0), 1)
	wantIDs(t, "owner 2's notes", noteIDs(notes1), 4)
	if p, ok := owners[0].Pinned.Get(); !ok || p != nil {
		t.Errorf("owner 1's pinned note, hidden: %v %v", p, ok)
	}
	if p, ok := owners[1].Pinned.Get(); !ok || p == nil || p.ID != 1 {
		t.Errorf("owner 2's pinned note: %v %v", p, ok)
	}
	tagged0, _ := owners[0].Tagged.Get()
	tagged1, _ := owners[1].Tagged.Get()
	wantIDs(t, "owner 1's tagged notes", noteIDs(tagged0), 1)
	wantIDs(t, "owner 2's tagged notes", noteIDs(tagged1))

	where := func(p velox.Pred[fixture.IOwner]) []int64 {
		t.Helper()
		os, err := db.Query[fixture.IOwner]().Where(p).OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ownerIDs(os)
	}
	wantIDs(t, "Notes.Has", where(fixture.IOwners.Notes.Has()), 1, 2)
	wantIDs(t, "Notes.HasWith(b)", where(fixture.IOwners.Notes.HasWith(fixture.INotes.Body.EQ("b"))))
	wantIDs(t, "Tagged.Has", where(fixture.IOwners.Tagged.Has()), 1)

	joined, err := db.Query[fixture.IOwner]().Join(fixture.IOwners.Pinned).OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Join(Pinned)", ownerIDs(joined), 2)
	sqlText, _, err := db.Query[fixture.IOwner]().LeftJoin(fixture.IOwners.Pinned).AllowUnbounded().SQL(ctx)
	if err != nil || !strings.Contains(sqlText, `"hidden" = `) {
		t.Errorf("LeftJoin's ON lacks the interceptor: %s, %v", sqlText, err)
	}

	ptrs := []*fixture.IOwner{&owners[0], &owners[1]}
	counts, err := velox.CountEach(ctx, db.Query[fixture.INote](), fixture.IOwners.Notes, ptrs)
	if err != nil || !slices.Equal(counts, []int64{1, 1}) {
		t.Errorf("CountEach: %v, %v", counts, err)
	}
	each, err := velox.LoadEach(ctx, db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()), fixture.IOwners.Notes, ptrs)
	if err != nil || len(each) != 2 || len(each[0]) != 1 || each[0][0].ID != 1 {
		t.Errorf("LoadEach: %v, %v", each, err)
	}
}

// TestInterceptorReachesWrites: Update, Delete and Save change no row the
// interceptor hides, and an upsert's update branch carries its condition.
func TestInterceptorReachesWrites(t *testing.T) {
	db := interceptorDB(t)
	ctx := context.Background()
	if n, err := db.Update[fixture.INote]().Set(fixture.INotes.OwnerID.Set(9)).All().Exec(ctx); err != nil || n != 2 {
		t.Errorf("Update all: %d, %v; want 2", n, err)
	}
	if n, err := db.Delete[fixture.INote]().Where(fixture.INotes.ID.In(1, 2)).Exec(ctx); err != nil || n != 1 {
		t.Errorf("Delete 1 and 2: %d, %v; want 1", n, err)
	}
	hidden := fixture.INote{ID: 2, OwnerID: 1, Body: "b2", Hidden: true}
	if err := db.Save(&hidden, fixture.INotes.Body).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("Save of the hidden note: %v", err)
	}
	skip := fixture.WithFilterMode(ctx, fixture.FilterSkip)
	if err := db.Save(&hidden, fixture.INotes.Body).Exec(skip); err != nil {
		t.Errorf("Save of the hidden note under the skip: %v", err)
	}

	e := &fakeconn.Exec{}
	pg := openFake(velox.Postgres, e)
	q, _, err := pg.Insert(&fixture.INote{ID: 2, Body: "b"}).OnConflict(fixture.INotes.Body).Update(fixture.INotes.OwnerID).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(q, `ON CONFLICT`), strings.Index(q, `"notes"."hidden" = `); i < 0 || j < i {
		t.Errorf("the upsert's update branch lacks the interceptor: %s", q)
	}
}

// TestInterceptorUpsertRefusedOnMySQL: MySQL's ON DUPLICATE KEY UPDATE takes
// no condition, so an upsert that updates an entity with an interceptor is
// refused, even under WithoutPolicy; DoNothing is not.
func TestInterceptorUpsertRefusedOnMySQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.MySQL, e)
	ctx := context.Background()
	err := db.Insert(&fixture.INote{ID: 2, Body: "b"}).OnConflict(fixture.INotes.Body).Update(fixture.INotes.OwnerID).Exec(ctx)
	if !errors.Is(err, velox.ErrPolicyUpsert) || !strings.Contains(err.Error(), "or an interceptor") {
		t.Errorf("upsert on MySQL: %v", err)
	}
	if err := db.Insert(&fixture.INote{ID: 2, Body: "b"}).OnConflict(fixture.INotes.Body).DoNothing().Exec(ctx); err != nil {
		t.Errorf("DoNothing on MySQL: %v", err)
	}
}

// TestWithoutPolicyKeepsInterceptors: WithoutPolicy lifts the Read policy,
// and the interceptor still hides its rows; its own skip shows them.
func TestWithoutPolicyKeepsInterceptors(t *testing.T) {
	db := interceptorDB(t)
	q := db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded()
	lifted := db.WithoutPolicy("test").Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded()
	ns, err := lifted.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "WithoutPolicy", noteIDs(ns), 1, 3, 4)
	if ns, err = q.All(fixture.WithFilterMode(context.Background(), fixture.FilterSkip)); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "the skip", noteIDs(ns), 1, 2, 4)
	if ns, err = lifted.All(fixture.WithFilterMode(context.Background(), fixture.FilterSkip)); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "both", noteIDs(ns), 1, 2, 3, 4)
}

// TestInterceptorRunsBeforePolicy: the WHERE holds the caller's conditions,
// then the interceptor's, then the Read policy's, then the Write policy's.
func TestInterceptorRunsBeforePolicy(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.Postgres, e)
	if _, err := db.Update[fixture.INote]().Set(fixture.INotes.OwnerID.Set(1)).Where(fixture.INotes.ID.EQ(7)).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `UPDATE "notes" SET "owner_id" = $1 WHERE "id" = $2 AND "hidden" = $3 AND "body" <> $4 AND "body" <> $5`
	if e.SQL != want {
		t.Errorf("SQL\n%s\nwant\n%s", e.SQL, want)
	}
	q, _, err := db.Query[fixture.INote]().Where(fixture.INotes.ID.EQ(7)).Limit(1).SQL(context.Background())
	if err != nil || !strings.Contains(q, `WHERE "id" = $1 AND "hidden" = $2 AND "body" <> $3`) {
		t.Errorf("SELECT: %s, %v", q, err)
	}
}

// TestInterceptorZeroPredicateFailsClosed: a Filter that returns the zero Predicate
// fails the statement, naming the interceptor, and sends nothing.
func TestInterceptorZeroPredicateFailsClosed(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	_, err := db.Query[fixture.INote]().Limit(1).All(fixture.WithFilterMode(context.Background(), fixture.FilterZero))
	if err == nil || !strings.Contains(err.Error(), "interceptor [0] returned the zero Predicate; return velox.Allow() for no restriction") || errors.Is(err, velox.ErrDenied) {
		t.Errorf("zero Predicate: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestInterceptorErrorIsNotADenial: a Filter's error fails the statement,
// and errors.Is reaches it, but it is not ErrDenied, nor a *PolicyError.
func TestInterceptorErrorIsNotADenial(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	_, err := db.Query[fixture.INote]().Limit(1).All(fixture.WithFilterMode(context.Background(), fixture.FilterFail))
	var pe *velox.PolicyError
	if !errors.Is(err, fixture.ErrFilter) || errors.Is(err, velox.ErrDenied) || errors.As(err, &pe) {
		t.Errorf("Filter's error: %v", err)
	}
	// Inside an EXISTS, the error names the path.
	_, err = db.Query[fixture.IOwner]().Where(fixture.IOwners.Notes.Has()).Limit(1).All(fixture.WithFilterMode(context.Background(), fixture.FilterFail))
	if !errors.Is(err, fixture.ErrFilter) || !strings.Contains(err.Error(), "in the EXISTS on fixture.INote") {
		t.Errorf("Filter's error in an EXISTS: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestInterceptorRawSQLUnfiltered: Raw and Exec are the caller's SQL, which
// velox does not filter.
func TestInterceptorRawSQLUnfiltered(t *testing.T) {
	db := interceptorDB(t)
	ctx := context.Background()
	ns, err := db.Raw[fixture.INote]("test", `SELECT "id", "owner_id", "body", "hidden" FROM "notes" ORDER BY "id"`).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Raw", noteIDs(ns), 1, 2, 3, 4)
	if n, err := db.Exec(ctx, "test", `UPDATE "notes" SET "owner_id" = 5`); err != nil || n != 4 {
		t.Errorf("Exec: %d, %v", n, err)
	}
}

// TestInterceptorRenderCacheKeepsContextsApart: the same query under a
// context the interceptor filters and one it does not renders two
// statements, each with its own text.
func TestInterceptorRenderCacheKeepsContextsApart(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	q := db.Query[fixture.INote]().Where(fixture.INotes.ID.GT(0)).Limit(3)
	lifted := context.Background()
	var got []string
	for _, ctx := range []context.Context{lifted, fixture.WithFilterMode(lifted, fixture.FilterSkip), lifted} {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		got = append(got, e.SQL)
	}
	if !strings.Contains(got[0], `"hidden" = `) || strings.Contains(got[1], `"hidden" = `) || got[2] != got[0] {
		t.Errorf("statements %q", got)
	}
}
