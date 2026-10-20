package velox

import (
	"sync"
	"testing"
)

func TestCacheRendersLikeRender(t *testing.T) {
	var c renderCache
	for range 2 { // the second pass is served from the cache
		for _, rc := range renderCases() {
			for d := range rc.sql {
				want, wantArgs := render(d, rc.st())
				got, args := c.render(d, rc.st())
				if got != want || !argsEqual(args, wantArgs) {
					t.Errorf("%s, %v:\n got %s %v\nwant %s %v", rc.name, d, got, args, want, wantArgs)
				}
			}
		}
	}
	if c.size() == 0 {
		t.Error("nothing was cached")
	}
}

func TestCacheKeepsShapesApart(t *testing.T) {
	var c renderCache
	one, _ := c.render(Postgres, stmt(hID.In(1)))
	two, _ := c.render(Postgres, stmt(hID.In(1, 2)))
	if one == two {
		t.Error("In lists of different lengths share SQL")
	}
	some := stmt()
	some.Columns = []int{0}
	a, _ := c.render(Postgres, some)
	b, _ := c.render(Postgres, stmt())
	if a == b {
		t.Error("a column subset shares SQL with every column")
	}
	m, _ := c.render(MySQL, stmt())
	if m == b {
		t.Error("dialects share SQL")
	}
	other := *invInfo
	other.Table = "archive"
	st := stmt()
	st.Entity = &other
	if o, _ := c.render(Postgres, st); o == b {
		t.Error("entities share SQL")
	}
}

// TestCacheKeepsNilApartFromAValue: EQ(nil) renders IS NULL and EQ(1) renders
// "= $1"; the two must not share a cached shape.
func TestCacheKeepsNilApartFromAValue(t *testing.T) {
	var c renderCache
	nilSt := func() *Statement { return stmt(Pred[inv]{node{op: opEQ, col: 1, v: nil}}) }
	valSt := func() *Statement { return stmt(hOrg.EQ(1)) }
	for range 2 {
		n, nargs := c.render(Postgres, nilSt())
		v, vargs := c.render(Postgres, valSt())
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
	var c renderCache
	want := `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "number" IN ($2, $3)`
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				v := int64(g*1000 + i)
				text, args := c.render(Postgres, stmt(hOrg.EQ(v), hNumber.In("a", "b")))
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
	defer func(n int) { maxShapes = n }(maxShapes)
	maxShapes = 3
	var c renderCache
	for n := 1; n <= 10; n++ {
		vs := make([]int64, n)
		want, _ := render(Postgres, stmt(hID.In(vs...)))
		if got, _ := c.render(Postgres, stmt(hID.In(vs...))); got != want {
			t.Fatalf("n=%d: %s", n, got)
		}
	}
	held := 0
	for _, shapes := range c.m {
		held += len(shapes)
	}
	if c.size() != 3 || held != 3 {
		t.Errorf("cache holds %d shapes (counted %d), the limit is 3", held, c.size())
	}
}

func TestCacheKeepsWriteShapesApart(t *testing.T) {
	var c renderCache
	one, _ := c.render(Postgres, ins([]int{1}, [][]any{{int64(1)}}))
	two, _ := c.render(Postgres, ins([]int{1}, [][]any{{int64(1)}, {int64(2)}}))
	up := ins([]int{1}, [][]any{{int64(1)}})
	up.Conflict = &ConflictClause{Columns: []int{1}, Update: []int{1}}
	keep := ins([]int{1}, [][]any{{int64(1)}})
	keep.Conflict = &ConflictClause{Columns: []int{1}, DoNothing: true}
	upSQL, _ := c.render(Postgres, up)
	keepSQL, _ := c.render(Postgres, keep)
	set := &Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.0}}}
	add := &Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignAdd, Value: 1.0}}}
	setSQL, _ := c.render(Postgres, set)
	addSQL, _ := c.render(Postgres, add)
	ret := ins([]int{1}, [][]any{{int64(1)}})
	ret.Returning = []int{0}
	retSQL, _ := c.render(Postgres, ret)
	for _, p := range [][2]string{{one, two}, {upSQL, keepSQL}, {setSQL, addSQL}, {one, retSQL}} {
		if p[0] == p[1] {
			t.Errorf("two shapes share SQL: %s", p[0])
		}
	}
}

// TestCacheBoundedInBytes is Review Focus 3: In lists of many lengths cannot
// grow the cache past its byte bound, and statements past it still render
// right.
func TestCacheBoundedInBytes(t *testing.T) {
	defer func(n int) { maxCacheBytes = n }(maxCacheBytes)
	maxCacheBytes = 2000
	var c renderCache
	for n := 1; n <= 200; n++ {
		vs := make([]any, n)
		for i := range vs {
			vs[i] = int64(i)
		}
		st := &Statement{Op: OpSelect, Entity: invInfo, Where: []Predicate{{node{op: opIn, col: 0, v: vs}}}}
		got, args := c.render(Postgres, st)
		want, wantArgs := render(Postgres, st)
		if got != want || len(args) != len(wantArgs) {
			t.Fatalf("n=%d: cached render differs", n)
		}
	}
	if c.bytes > maxCacheBytes {
		t.Errorf("cache holds %d bytes, over its bound %d", c.bytes, maxCacheBytes)
	}
}

func TestCacheKeysMoreShapes(t *testing.T) {
	var c renderCache
	base := func() *Statement { return &Statement{Op: OpSelect, Entity: invInfo} }
	variants := []func(*Statement){
		func(st *Statement) {},
		func(st *Statement) { st.Limit = 1 },
		func(st *Statement) { st.Offset = 1 },
		func(st *Statement) { st.OrderBy = []OrderTerm{{Column: 0}, {Column: 1}} },
		func(st *Statement) { st.OrderBy = []OrderTerm{{Column: 1}, {Column: 0}} },
		func(st *Statement) {
			st.Where = []Predicate{{node{op: opAnd, kids: []node{{op: opEQ, col: 0, v: 1}, {op: opOr, kids: []node{{op: opEQ, col: 1, v: 1}}}}}}}
		},
		func(st *Statement) {
			st.Where = []Predicate{{node{op: opAnd, kids: []node{{op: opOr, kids: []node{{op: opEQ, col: 0, v: 1}}}, {op: opEQ, col: 1, v: 1}}}}}
		},
	}
	seen := map[string]int{}
	for i, v := range variants {
		st := base()
		v(st)
		got, _ := c.render(Postgres, st)
		if j, dup := seen[got]; dup {
			t.Errorf("variants %d and %d share SQL %q", j, i, got)
		}
		seen[got] = i
		if again, _ := c.render(Postgres, st); again != got {
			t.Errorf("variant %d: the cache answered %q for %q", i, again, got)
		}
	}
}

// TestCacheKeysTheUpsertCondition: an upsert's condition is part of its shape.
func TestCacheKeysTheUpsertCondition(t *testing.T) {
	var c renderCache
	seen := map[string]int{}
	for i, where := range [][]Predicate{
		nil,
		{ColumnEQ(1, int64(1))},
		{ColumnEQ(1, int64(1)), ColumnEQ(2, "x")},
		{ColumnEQ(2, "x")},
	} {
		st := ins([]int{1}, [][]any{{int64(1)}})
		st.Conflict = &ConflictClause{Columns: []int{1}, Update: []int{1}, Where: where}
		got, args := c.render(Postgres, st)
		if j, dup := seen[got]; dup {
			t.Errorf("conditions %d and %d share SQL %q", j, i, got)
		}
		seen[got] = i
		want, wantArgs := render(Postgres, st)
		if again, againArgs := c.render(Postgres, st); again != want || got != want || len(args) != len(wantArgs) || len(againArgs) != len(wantArgs) {
			t.Errorf("condition %d: the cache answered %q, render %q", i, again, want)
		}
	}
}

func TestFingerprintFormat(t *testing.T) {
	// SHA-256 of the empty string starts e3b0c44298fc1c14.
	if got := fingerprint(""); got != "e3b0c44298fc1c14" {
		t.Errorf("fingerprint(\"\") = %s", got)
	}
}

// TestCacheStoresTheFingerprint: a hit returns the fingerprint stored at the
// miss, and a statement past the cache's bound gets the same one.
func TestCacheStoresTheFingerprint(t *testing.T) {
	var c renderCache
	miss, _, _ := c.get(Postgres, stmt(hOrg.EQ(7)))
	hit, _, _ := c.get(Postgres, stmt(hOrg.EQ(8)))
	text, _ := render(Postgres, stmt(hOrg.EQ(7)))
	if miss != hit || miss.text != text || miss.fp != fingerprint(text) {
		t.Errorf("miss %+v, hit %+v, want text %s", miss, hit, text)
	}
	defer func(n int) { maxShapes = n }(maxShapes)
	maxShapes = 0
	var full renderCache
	if over, _, _ := full.get(Postgres, stmt(hOrg.EQ(9))); over != miss || full.size() != 0 {
		t.Errorf("past the bound: %+v, %d shapes held", over, full.size())
	}
}
