package engine_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

func TestCacheRendersLikeRender(t *testing.T) {
	var c engine.RenderCache
	for range 2 { // the second pass is served from the cache
		for _, rc := range renderCases() {
			for d := range rc.sql {
				want, wantArgs := render(d, rc.st())
				got, args := cachedRender(&c, d, rc.st())
				if got != want || !argsEqual(args, wantArgs) {
					t.Errorf("%s, %v:\n got %s %v\nwant %s %v", rc.name, d, got, args, want, wantArgs)
				}
			}
		}
	}
	if c.Size() == 0 {
		t.Error("nothing was cached")
	}
}

func TestCacheKeepsShapesApart(t *testing.T) {
	var c engine.RenderCache
	one, _ := cachedRender(&c, velox.Postgres, stmt(hID.In(1)))
	two, _ := cachedRender(&c, velox.Postgres, stmt(hID.In(1, 2)))
	if one == two {
		t.Error("In lists of different lengths share SQL")
	}
	some := stmt()
	some.Columns = []int{0}
	a, _ := cachedRender(&c, velox.Postgres, some)
	b, _ := cachedRender(&c, velox.Postgres, stmt())
	if a == b {
		t.Error("a column subset shares SQL with every column")
	}
	m, _ := cachedRender(&c, velox.MySQL, stmt())
	if m == b {
		t.Error("dialects share SQL")
	}
	other := *invInfo
	other.Table = "archive"
	st := stmt()
	st.Entity = &other
	if o, _ := cachedRender(&c, velox.Postgres, st); o == b {
		t.Error("entities share SQL")
	}
	// Each nil, Each with a per-owner Limit and without, Each through a join,
	// and the grouped counts, direct and through a join, are six shapes, each
	// rendered as it renders uncached.
	var ec engine.RenderCache
	plain := eachItems(1, 0)
	plain.Each = nil
	shapes := []*velox.Statement{plain, eachItems(1, 2), eachItems(1, 0), eachTags(2, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)})),
		countItems(), countTags(bookTagInfo, 0, 1)}
	seen := map[string]bool{}
	for range 2 {
		for i, st := range shapes {
			want, _ := render(velox.Postgres, st)
			got, _ := cachedRender(&ec, velox.Postgres, st)
			if got != want {
				t.Errorf("shape %d: cached\n %s\nwant %s", i, got, want)
			}
			seen[got] = true
		}
	}
	if len(seen) != 6 || ec.Size() != 6 {
		t.Errorf("%d texts, %d shapes cached; want 6 of each", len(seen), ec.Size())
	}
}

// TestCacheKeepsNilApartFromAValue: EQ(nil) renders IS NULL and EQ(1) renders
// "= $1"; the two must not share a cached shape.
func TestCacheKeepsNilApartFromAValue(t *testing.T) {
	var c engine.RenderCache
	nilSt := func() *velox.Statement {
		return stmt(velox.Pred[inv](engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: nil})))
	}
	valSt := func() *velox.Statement { return stmt(hOrg.EQ(1)) }
	for range 2 {
		n, nargs := cachedRender(&c, velox.Postgres, nilSt())
		v, vargs := cachedRender(&c, velox.Postgres, valSt())
		if n != `SELECT `+pgCols+` FROM "invoices" WHERE "org_id" IS NULL` || len(nargs) != 0 {
			t.Errorf("nil: %s %v", n, nargs)
		}
		if v != `SELECT `+pgCols+` FROM "invoices" WHERE "org_id" = $1` || len(vargs) != 1 {
			t.Errorf("value: %s %v", v, vargs)
		}
	}
}

// TestCacheIsSafeForConcurrentUse is Review Focus 5; run it with -race.
func TestCacheIsSafeForConcurrentUse(t *testing.T) {
	var c engine.RenderCache
	want := `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "number" IN ($2, $3)`
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				v := int64(g*1000 + i)
				text, args := cachedRender(&c, velox.Postgres, stmt(hOrg.EQ(v), hNumber.In("a", "b")))
				if text != want || args[0] != v || args[2] != "b" {
					t.Errorf("goroutine %d: %s %v", g, text, args)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCacheStopsGrowing(t *testing.T) {
	defer func(n int) { engine.MaxShapes = n }(engine.MaxShapes)
	engine.MaxShapes = 3
	var c engine.RenderCache
	for n := 1; n <= 10; n++ {
		vs := make([]int64, n)
		want, _ := render(velox.Postgres, stmt(hID.In(vs...)))
		if got, _ := cachedRender(&c, velox.Postgres, stmt(hID.In(vs...))); got != want {
			t.Fatalf("n=%d: %s", n, got)
		}
	}
	held := 0
	for _, shapes := range c.M {
		held += len(shapes)
	}
	if c.Size() != 3 || held != 3 {
		t.Errorf("cache holds %d shapes (counted %d), the limit is 3", held, c.Size())
	}
}

func TestCacheKeepsWriteShapesApart(t *testing.T) {
	var c engine.RenderCache
	one, _ := cachedRender(&c, velox.Postgres, ins([]int{1}, [][]any{{int64(1)}}))
	two, _ := cachedRender(&c, velox.Postgres, ins([]int{1}, [][]any{{int64(1)}, {int64(2)}}))
	up := ins([]int{1}, [][]any{{int64(1)}})
	up.Conflict = &velox.ConflictClause{Columns: []int{1}, Update: []int{1}}
	keep := ins([]int{1}, [][]any{{int64(1)}})
	keep.Conflict = &velox.ConflictClause{Columns: []int{1}, DoNothing: true}
	upSQL, _ := cachedRender(&c, velox.Postgres, up)
	keepSQL, _ := cachedRender(&c, velox.Postgres, keep)
	set := &velox.Statement{Op: velox.OpUpdate, Entity: invInfo, Set: []velox.Assignment{{Column: 3, Op: velox.AssignSet, Value: 1.0}}}
	add := &velox.Statement{Op: velox.OpUpdate, Entity: invInfo, Set: []velox.Assignment{{Column: 3, Op: velox.AssignAdd, Value: 1.0}}}
	setSQL, _ := cachedRender(&c, velox.Postgres, set)
	addSQL, _ := cachedRender(&c, velox.Postgres, add)
	ret := ins([]int{1}, [][]any{{int64(1)}})
	ret.Returning = []int{0}
	retSQL, _ := cachedRender(&c, velox.Postgres, ret)
	for _, p := range [][2]string{{one, two}, {upSQL, keepSQL}, {setSQL, addSQL}, {one, retSQL}} {
		if p[0] == p[1] {
			t.Errorf("two shapes share SQL: %s", p[0])
		}
	}
}

// TestCacheKeepsWriteColumnsApart: statements that differ only in which
// columns a clause names do not share a cached shape.
func TestCacheKeepsWriteColumnsApart(t *testing.T) {
	upsert := func(key, update []int) *velox.Statement {
		st := ins([]int{1, 2, 3}, [][]any{{int64(1), "n", 1.0}})
		st.Conflict = &velox.ConflictClause{Columns: key, Update: update}
		return st
	}
	returning := func(cols []int) *velox.Statement {
		st := ins([]int{1}, [][]any{{int64(1)}})
		st.Returning = cols
		return st
	}
	set := func(col int) *velox.Statement {
		return &velox.Statement{Op: velox.OpUpdate, Entity: invInfo, Set: []velox.Assignment{{Column: col, Op: velox.AssignSet, Value: 1.0}}}
	}
	for _, c := range []struct {
		name string
		a, b *velox.Statement
	}{
		{"Conflict.Columns", upsert([]int{1}, []int{3}), upsert([]int{2}, []int{3})},
		{"Conflict.Update", upsert([]int{1}, []int{3}), upsert([]int{1}, []int{2})},
		{"Returning", returning([]int{0}), returning([]int{1})},
		{"Set column", set(3), set(2)},
	} {
		for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
			var cache engine.RenderCache
			a, _ := cachedRender(&cache, d, c.a)
			b, _ := cachedRender(&cache, d, c.b)
			if a == b {
				t.Errorf("%s, %v: two shapes share SQL: %s", c.name, d, a)
			}
			if want, _ := render(d, c.a); a != want {
				t.Errorf("%s, %v: first\n got %s\nwant %s", c.name, d, a, want)
			}
			if want, _ := render(d, c.b); b != want {
				t.Errorf("%s, %v: second\n got %s\nwant %s", c.name, d, b, want)
			}
		}
	}
}

// TestCacheBoundedInBytes is Review Focus 3: In lists of many lengths cannot
// grow the cache past its byte bound, and statements past it still render
// right.
func TestCacheBoundedInBytes(t *testing.T) {
	defer func(n int) { engine.MaxCacheBytes = n }(engine.MaxCacheBytes)
	engine.MaxCacheBytes = 2000
	var c engine.RenderCache
	for n := 1; n <= 200; n++ {
		vs := make([]any, n)
		for i := range vs {
			vs[i] = int64(i)
		}
		st := &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpIn, Col: 0, V: vs})}}
		got, args := cachedRender(&c, velox.Postgres, st)
		want, wantArgs := render(velox.Postgres, st)
		if got != want || len(args) != len(wantArgs) {
			t.Fatalf("n=%d: cached render differs", n)
		}
	}
	if c.Bytes > engine.MaxCacheBytes {
		t.Errorf("cache holds %d bytes, over its bound %d", c.Bytes, engine.MaxCacheBytes)
	}
}

// inShape is a SELECT whose In list has n values: each n is a shape of its own.
func inShape(n int) *velox.Statement {
	vs := make([]any, n)
	for i := range vs {
		vs[i] = int64(i)
	}
	return &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpIn, Col: 0, V: vs})}}
}

// TestCacheBytesCountTheFingerprint: the bytes a cache holds are the key, the
// text and the 16-byte fingerprint of each shape, and the byte bound includes
// the fingerprint: a bound one byte short of the sum leaves the last shape out.
func TestCacheBytesCountTheFingerprint(t *testing.T) {
	const shapes = 50
	var c engine.RenderCache
	for n := 1; n <= shapes; n++ {
		cachedRender(&c, velox.Postgres, inShape(n))
	}
	sum, held := 0, 0
	for _, byKey := range c.M {
		for key, e := range byKey {
			if len(e.Fp) != 16 {
				t.Errorf("a fingerprint of %d bytes: %q", len(e.Fp), e.Fp)
			}
			sum += len(key) + len(e.Text) + len(e.Fp)
			held++
		}
	}
	if held != shapes || c.Size() != shapes {
		t.Fatalf("the cache holds %d shapes (size %d), want %d", held, c.Size(), shapes)
	}
	if c.Bytes != sum {
		t.Errorf("the cache counts %d bytes, its entries sum to %d", c.Bytes, sum)
	}

	defer func(n int) { engine.MaxCacheBytes = n }(engine.MaxCacheBytes)
	engine.MaxCacheBytes = sum - 1
	var tight engine.RenderCache
	for n := 1; n <= shapes; n++ {
		cachedRender(&tight, velox.Postgres, inShape(n))
	}
	var buf [256]byte
	last := engine.ShapeKey(buf[:0], velox.Postgres, inShape(shapes))
	if _, ok := tight.M[invInfo][string(last)]; ok {
		t.Errorf("the last shape is held, with a bound of %d bytes, one under the %d they need", engine.MaxCacheBytes, sum)
	}
	if tight.Size() != shapes-1 || tight.Bytes > engine.MaxCacheBytes {
		t.Errorf("the tight cache holds %d shapes and %d bytes, want %d shapes within %d bytes", tight.Size(), tight.Bytes, shapes-1, engine.MaxCacheBytes)
	}
}

func TestCacheKeysMoreShapes(t *testing.T) {
	var c engine.RenderCache
	base := func() *velox.Statement { return &velox.Statement{Op: velox.OpSelect, Entity: invInfo} }
	variants := []func(*velox.Statement){
		func(st *velox.Statement) {},
		func(st *velox.Statement) { st.Limit = 1 },
		func(st *velox.Statement) { st.Offset = 1 },
		func(st *velox.Statement) { st.OrderBy = []velox.OrderTerm{{Column: 0}, {Column: 1}} },
		func(st *velox.Statement) { st.OrderBy = []velox.OrderTerm{{Column: 1}, {Column: 0}} },
		func(st *velox.Statement) {
			st.Where = []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpAnd, Kids: []engine.Node{{Op: engine.OpEQ, Col: 0, V: 1}, {Op: engine.OpOr, Kids: []engine.Node{{Op: engine.OpEQ, Col: 1, V: 1}}}}})}
		},
		func(st *velox.Statement) {
			st.Where = []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpAnd, Kids: []engine.Node{{Op: engine.OpOr, Kids: []engine.Node{{Op: engine.OpEQ, Col: 0, V: 1}}}, {Op: engine.OpEQ, Col: 1, V: 1}}})}
		},
	}
	seen := map[string]int{}
	for i, v := range variants {
		st := base()
		v(st)
		got, _ := cachedRender(&c, velox.Postgres, st)
		if j, dup := seen[got]; dup {
			t.Errorf("variants %d and %d share SQL %q", j, i, got)
		}
		seen[got] = i
		if again, _ := cachedRender(&c, velox.Postgres, st); again != got {
			t.Errorf("variant %d: the cache answered %q for %q", i, again, got)
		}
	}
}

// TestCacheKeysTheUpsertCondition: an upsert's condition is part of its shape.
func TestCacheKeysTheUpsertCondition(t *testing.T) {
	var c engine.RenderCache
	seen := map[string]int{}
	for i, where := range [][]velox.Predicate{
		nil,
		{velox.ColumnEQ(1, int64(1))},
		{velox.ColumnEQ(1, int64(1)), velox.ColumnEQ(2, "x")},
		{velox.ColumnEQ(2, "x")},
	} {
		st := ins([]int{1}, [][]any{{int64(1)}})
		st.Conflict = &velox.ConflictClause{Columns: []int{1}, Update: []int{1}, Where: where}
		got, args := cachedRender(&c, velox.Postgres, st)
		if j, dup := seen[got]; dup {
			t.Errorf("conditions %d and %d share SQL %q", j, i, got)
		}
		seen[got] = i
		want, wantArgs := render(velox.Postgres, st)
		if again, againArgs := cachedRender(&c, velox.Postgres, st); again != want || got != want || len(args) != len(wantArgs) || len(againArgs) != len(wantArgs) {
			t.Errorf("condition %d: the cache answered %q, render %q", i, again, want)
		}
	}
}

// TestCacheStoresTheFingerprint: a hit returns the fingerprint stored at the
// miss, and a statement past the cache's bound gets the same one.
func TestCacheStoresTheFingerprint(t *testing.T) {
	var c engine.RenderCache
	miss, _, err := c.Get(velox.Postgres, stmt(hOrg.EQ(7)))
	if err != nil {
		t.Fatal(err)
	}
	hit, _, err := c.Get(velox.Postgres, stmt(hOrg.EQ(8)))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := render(velox.Postgres, stmt(hOrg.EQ(7)))
	if miss != hit || miss.Text != text || miss.Fp != engine.Fingerprint(text) {
		t.Errorf("miss %+v, hit %+v, want text %s", miss, hit, text)
	}
	defer func(n int) { engine.MaxShapes = n }(engine.MaxShapes)
	engine.MaxShapes = 0
	var full engine.RenderCache
	over, _, err := full.Get(velox.Postgres, stmt(hOrg.EQ(9)))
	if err != nil {
		t.Fatal(err)
	}
	if over != miss || full.Size() != 0 {
		t.Errorf("past the bound: %+v, %d shapes held", over, full.Size())
	}
}

func TestExistsHasItsOwnShape(t *testing.T) {
	var c engine.RenderCache
	for range 2 { // the second pass is served from the cache
		ex := stmt(hOrg.EQ(1))
		ex.Exists, ex.Limit = true, 1
		all := stmt(hOrg.EQ(1))
		all.Limit = 1
		a, _ := cachedRender(&c, velox.Postgres, ex)
		b, _ := cachedRender(&c, velox.Postgres, all)
		if a == b || engine.Fingerprint(a) == engine.Fingerprint(b) {
			t.Errorf("Exists and Limit(1).All share a shape: %s / %s", a, b)
		}
	}
}

// TestCacheKeysTheJoin: two Of conditions through join entities differ in
// their shape when the join entity or its column differs, though their
// target column and owners agree.
func TestCacheKeysTheJoin(t *testing.T) {
	join := &velox.EntityInfo{Name: "velox.join", Table: "joins", Columns: []velox.ColumnInfo{{Name: "a"}, {Name: "b"}}}
	other := &velox.EntityInfo{Name: "velox.other", Table: "others", Columns: join.Columns}
	through := func(j *velox.EntityInfo, col int) *velox.Statement {
		return &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpThrough, Col: 0, V: j, W: col,
			Kids: []engine.Node{{Op: engine.OpIn, Col: 1, V: []any{int64(1)}}}})}}
	}
	var c engine.RenderCache
	seen := map[string]string{}
	for range 2 { // the second pass is served from the cache
		for name, st := range map[string]*velox.Statement{"joins.a": through(join, 0), "joins.b": through(join, 1), "others.a": through(other, 0)} {
			got, args := cachedRender(&c, velox.Postgres, st)
			if want, wantArgs := render(velox.Postgres, st); got != want || !argsEqual(args, wantArgs) {
				t.Errorf("%s: the cache renders %s %v, not %s %v", name, got, args, want, wantArgs)
			}
			for n, s := range seen {
				if n != name && s == got {
					t.Errorf("%s and %s share SQL %s", n, name, got)
				}
			}
			seen[name] = got
		}
	}
}

// TestAggregateShapes: an aggregate's function and column are part of its
// shape, so none shares cached SQL with another.
func TestAggregateShapes(t *testing.T) {
	var c engine.RenderCache
	agg := func(fn velox.Aggregate, col int) string {
		st := stmt()
		st.Aggregate, st.Columns = fn, []int{col}
		sql, _ := cachedRender(&c, velox.Postgres, st)
		return sql
	}
	sum, maxAgg, other := agg(velox.AggregateSum, 3), agg(velox.AggregateMax, 3), agg(velox.AggregateSum, 1)
	if sum == maxAgg || sum == other || maxAgg == other {
		t.Errorf("shapes share SQL: %s, %s, %s", sum, maxAgg, other)
	}
	if c.Size() != 3 {
		t.Errorf("cache holds %d shapes, want 3", c.Size())
	}
	if again := agg(velox.AggregateSum, 3); again != sum || c.Size() != 3 {
		t.Errorf("a hit changed: %s, %d shapes", again, c.Size())
	}
}

// TestGroupShapes: the key column is part of a grouped aggregate's shape, apart
// from the ordering beside it, so two keys never share cached SQL, nor does a
// grouped statement share with a plain one; column 0 is a key like any other.
func TestGroupShapes(t *testing.T) {
	var c engine.RenderCache
	group := func(by int, sum bool) string {
		st := stmt()
		st.GroupBy = by
		if sum {
			st.Aggregate, st.Columns = velox.AggregateSum, []int{3}
		} else {
			st.Count = true
		}
		sql, _ := cachedRender(&c, velox.Postgres, st)
		return sql
	}
	shapes := []string{group(0, false), group(1, false), group(3, false), group(0, true), group(1, true), group(3, true)}
	for i, a := range shapes {
		for _, b := range shapes[i+1:] {
			if a == b {
				t.Errorf("shapes share SQL: %s", a)
			}
		}
	}
	if c.Size() != len(shapes) {
		t.Errorf("cache holds %d shapes, want %d", c.Size(), len(shapes))
	}
	if again := group(3, true); again != shapes[5] || c.Size() != len(shapes) {
		t.Errorf("a hit changed: %s, %d shapes", again, c.Size())
	}
}

// A read and its four locked forms are five shapes, and the cache never
// returns one's text for another.
func TestLockShapes(t *testing.T) {
	var c engine.RenderCache
	forms := []struct {
		lock velox.Lock
		wait velox.LockWait
	}{{0, 0}, {velox.LockForUpdate, 0}, {velox.LockForShare, 0}, {velox.LockForUpdate, velox.LockSkipLocked}, {velox.LockForUpdate, velox.LockNoWait}}
	texts := make(map[string]bool)
	for range 2 {
		for _, f := range forms {
			st := stmt(hID.EQ(1))
			st.Limit, st.Lock, st.LockWait = 1, f.lock, f.wait
			want, _ := render(velox.MySQL, st)
			if got, _ := cachedRender(&c, velox.MySQL, st); got != want {
				t.Errorf("cached %s, want %s", got, want)
			}
			texts[want] = true
		}
	}
	if len(texts) != 5 || c.Size() != 5 {
		t.Errorf("%d texts, %d shapes, want 5", len(texts), c.Size())
	}
}

// TestInsertAllAboveCachedRows: an insert of more than MaxCachedRows rows is
// rendered without the cache, and still gets its fingerprint; one of
// MaxCachedRows rows is cached.
func TestInsertAllAboveCachedRows(t *testing.T) {
	var fps []string
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1}, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		fps = append(fps, st.Fingerprint) // set by rendering, after the Rewrite hooks
		return next(ctx)
	}))
	insert := func(n int) {
		t.Helper()
		rows := make([]hnote, n)
		for i := range rows {
			rows[i].ID = int64(i + 1)
		}
		if err := db.InsertAll(rows).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	before := cacheSize(db)
	insert(engine.MaxCachedRows + 1)
	insert(engine.MaxCachedRows + 1)
	if got := cacheSize(db); got != before {
		t.Errorf("an insert of %d rows grew the cache from %d to %d shapes", engine.MaxCachedRows+1, before, got)
	}
	insert(engine.MaxCachedRows)
	if got := cacheSize(db); got != before+1 {
		t.Errorf("an insert of %d rows left the cache at %d shapes, want %d", engine.MaxCachedRows, got, before+1)
	}
	if len(fps) != 3 {
		t.Fatalf("%d statements ran, want 3", len(fps))
	}
	for i, fp := range fps {
		if len(fp) != 16 || strings.Trim(fp, "0123456789abcdef") != "" {
			t.Errorf("statement %d: fingerprint %q", i, fp)
		}
	}
	if fps[0] != fps[1] || fps[0] == fps[2] {
		t.Errorf("fingerprints %q", fps)
	}
}
