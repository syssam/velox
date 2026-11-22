package bench

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"
)

// The where and page benchmarks read E4 (items) and E5 (kids, under items
// through the edge next), which the other benchmarks never touch. seedPages
// seeds them apart from seed, so that the other benchmarks' setup is
// unchanged. Every ORM seeds the same rows, by the rules below.
const (
	nItems   = 400 // E4 rows: item i has count i
	nKids    = 600 // E5 rows
	pageSize = 20
	pageNum  = 5 // PageOffset reads page 5: OFFSET 80
)

// itemName is item i's name: x, y or z by i%3, then i, as x000.
func itemName(i int) string { return fmt.Sprintf("%c%03d", "xyz"[i%3], i) }

// itemLive reports whether item i has status live; the others are draft.
func itemLive(i int) bool { return i%4 == 0 }

// kidName is kid j's name.
func kidName(j int) string { return fmt.Sprintf("k%03d", j) }

// kidParent is the item that kid j is under: items 0..299 have two kids each.
func kidParent(j int) int { return j % 300 }

// kidActive reports whether kid j is active: then its item is j%10 = 0 too.
func kidActive(j int) bool { return j%10 == 0 }

// kidCount is kid j's count, which pages order by: 50 values, 12 kids each.
func kidCount(j int) int { return j % 50 }

// The filters every ORM writes the same way.
const (
	wherePrefix = "x" // name starts with x
	whereFrom   = 100 // count >= 100
	whereTo     = 380 // count < 380
)

// setupPages is setup, then seedPages, analyzed again.
func setupPages(tb testing.TB) *app {
	tb.Helper()
	a := setup(tb)
	if err := a.seedPages(context.Background()); err != nil {
		tb.Fatal(err)
	}
	analyze(tb, a.sqlDB)
	return a
}

// wantWhereAnd is the items WhereAnd returns: live, named x…, count in
// [100, 380), by count, the first 20.
func (a *app) wantWhereAnd() []int64 {
	var ids []int64
	for i := range nItems {
		if itemLive(i) && itemName(i)[:1] == wherePrefix && i >= whereFrom && i < whereTo && len(ids) < pageSize {
			ids = append(ids, a.items[i])
		}
	}
	return ids
}

// wantWhereHasEdge is the items WhereHasEdge returns: those with an active
// kid, by id, the first 20.
func (a *app) wantWhereHasEdge() []int64 {
	var ids []int64
	for j := range nKids {
		if kidActive(j) && !slices.Contains(ids, a.items[kidParent(j)]) {
			ids = append(ids, a.items[kidParent(j)])
		}
	}
	slices.Sort(ids)
	return ids[:pageSize]
}

// kidsInOrder is every kid's id, ordered by (count, id).
func (a *app) kidsInOrder() []int64 {
	js := make([]int, nKids)
	for j := range js {
		js[j] = j
	}
	slices.SortFunc(js, func(x, y int) int {
		return cmp.Or(cmp.Compare(kidCount(x), kidCount(y)), cmp.Compare(a.kids[x], a.kids[y]))
	})
	ids := make([]int64, nKids)
	for i, j := range js {
		ids[i] = a.kids[j]
	}
	return ids
}

// wantPage is the kids of page 5, which both PageOffset and PageKeyset return.
func (a *app) wantPage() []int64 {
	from := (pageNum - 1) * pageSize
	return a.kidsInOrder()[from : from+pageSize]
}

// cursorKid is the kid that PageKeyset continues after: the last of page 4.
func (a *app) cursorKid() int64 {
	return a.kidsInOrder()[(pageNum-1)*pageSize-1]
}

// checkIDs fails t unless got is want: the same count, and the same ids in
// the same order.
func checkIDs(t *testing.T, op string, got []int64, err error, want []int64) {
	t.Helper()
	switch {
	case err != nil:
		t.Errorf("%s: %v", op, err)
	case len(want) != pageSize:
		t.Errorf("%s: the seed gives %d rows, want %d", op, len(want), pageSize)
	case len(got) != len(want):
		t.Errorf("%s: %d rows, want %d", op, len(got), len(want))
	case got[0] != want[0] || got[len(got)-1] != want[len(want)-1]:
		t.Errorf("%s: ids %d..%d, want %d..%d", op, got[0], got[len(got)-1], want[0], want[len(want)-1])
	case !slices.Equal(got, want):
		t.Errorf("%s: ids %v, want %v", op, got, want)
	}
}

// guardPages checks the where and page operations, after seedPages.
func guardPages(t *testing.T, a *app) {
	ctx := context.Background()
	if err := a.seedPages(ctx); err != nil {
		t.Fatal(err)
	}
	analyze(t, a.sqlDB)
	if len(a.items) != nItems || len(a.kids) != nKids {
		t.Fatalf("seedPages: %d items, %d kids; want %d, %d", len(a.items), len(a.kids), nItems, nKids)
	}
	ids, err := a.whereAnd(ctx)
	checkIDs(t, "whereAnd", ids, err, a.wantWhereAnd())
	ids, err = a.whereHasEdge(ctx)
	checkIDs(t, "whereHasEdge", ids, err, a.wantWhereHasEdge())
	ids, err = a.pageOffset(ctx)
	checkIDs(t, "pageOffset", ids, err, a.wantPage())
	if got, want := a.cursor(), a.cursorKid(); got != want {
		t.Errorf("the keyset cursor is kid %d, want %d", got, want)
	}
	ids, err = a.pageKeyset(ctx)
	checkIDs(t, "pageKeyset", ids, err, a.wantPage())
}
