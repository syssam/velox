package model

import (
	"sort"

	"velox.test/parity/compare"
	"velox.test/parity/op"
)

// paginate implements Relay cursor pagination as plain in-memory list slicing,
// so it is obviously correct. Cursors are creation handles, not opaque bytes.
//
// Algorithm (Relay spec, on a totally-ordered slice):
//  1. Sort a copy of the live posts by the OrderBy terms, each followed by a
//     final total-order tiebreak on handle ascending. Default order (empty
//     OrderBy) = handle ascending. The total order removes tie ambiguity.
//  2. after: drop all elements up to and including the element whose handle ==
//     *AfterRef. before: drop all elements from the element whose handle ==
//     *BeforeRef onward.
//  3. HasPrev/HasNext baseline = whether the cursor step dropped elements on
//     the left/right.
//  4. first: if len > *First, keep the first *First and set HasNext. last: if
//     len > *Last, keep the LAST *Last and set HasPrev.
//  5. Rows are the survivors in display (sorted) order, each with its id Ref
//     and fields. StartHandle/EndHandle are the first/last surviving handle.
//
// The backward case falls out naturally: BeforeRef trims the right, Last keeps
// the tail of what remains — no separate direction-reversal logic, so the
// before-cursor mirror is correct by construction.
func paginate(posts []*post, p op.PaginatePosts) Result {
	sorted := make([]*post, len(posts))
	copy(sorted, posts)
	sortPosts(sorted, p.OrderBy)

	hasPrev := false
	hasNext := false

	// Step 2 + 3: cursor trimming, tracking whether anything was dropped.
	if p.AfterRef != nil {
		if i := indexOfHandle(sorted, *p.AfterRef); i >= 0 {
			// Dropped elements 0..i (the cursor element and everything before
			// it), so there are previous pages.
			hasPrev = true
			sorted = sorted[i+1:]
		}
	}
	if p.BeforeRef != nil {
		if i := indexOfHandle(sorted, *p.BeforeRef); i >= 0 {
			// Dropped elements i..end (the cursor element and everything after
			// it), so there are next pages.
			hasNext = true
			sorted = sorted[:i]
		}
	}

	// Step 4: first/last window trimming.
	if p.First != nil && len(sorted) > *p.First {
		sorted = sorted[:*p.First]
		hasNext = true
	}
	if p.Last != nil && len(sorted) > *p.Last {
		sorted = sorted[len(sorted)-*p.Last:]
		hasPrev = true
	}

	// Step 5: project survivors into rows and page info.
	rows := make([]Row, len(sorted))
	for i, post := range sorted {
		rows[i] = postRow(post)
	}
	page := &PageInfo{HasNext: hasNext, HasPrev: hasPrev}
	if len(sorted) > 0 {
		start := sorted[0].handle
		end := sorted[len(sorted)-1].handle
		page.StartHandle = &start
		page.EndHandle = &end
	}
	return Result{Rows: rows, Page: page, Err: compare.ErrOK}
}

// sortPosts sorts in place by the order terms, with a final handle-ascending
// tiebreak so the ordering is always total.
func sortPosts(posts []*post, terms []op.OrderTerm) {
	sort.SliceStable(posts, func(i, j int) bool {
		a, b := posts[i], posts[j]
		for _, term := range terms {
			c := comparePostField(a, b, term.Field)
			if c == 0 {
				continue
			}
			if term.Desc {
				return c > 0
			}
			return c < 0
		}
		// Total-order tiebreak: handle ascending.
		return a.handle < b.handle
	})
}

// comparePostField returns -1/0/1 comparing a vs b on the named column.
func comparePostField(a, b *post, field string) int {
	switch field {
	case "view_count":
		return cmpInt(a.viewCount, b.viewCount)
	case "created_at":
		return cmpInt(a.createdAt, b.createdAt)
	case "updated_at":
		return cmpInt(a.updatedAt, b.updatedAt)
	case "title":
		return cmpString(a.title, b.title)
	case "status":
		return cmpString(a.status, b.status)
	case "id", "":
		return cmpInt(a.handle, b.handle)
	default:
		panic("model: paginate on unknown order field " + field)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// indexOfHandle returns the index of the post with the given handle, or -1.
func indexOfHandle(posts []*post, handle int) int {
	for i, p := range posts {
		if p.handle == handle {
			return i
		}
	}
	return -1
}
