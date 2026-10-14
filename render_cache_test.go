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
