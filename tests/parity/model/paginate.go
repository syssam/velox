package model

import (
	"sort"

	"velox.test/parity/op"
)

// paginate implements Relay cursor pagination as plain in-memory list slicing,
// so it is obviously correct. Cursors are creation handles, not opaque bytes.
//
// Algorithm (Relay spec, on a totally-ordered slice):
//  0. Validate: reject first+last together and negative first/last with
//     ErrValidation (both velox and ent do this before touching data).
//  1. Sort a copy of the live posts by the OrderBy terms, each followed by a
//     final total-order tiebreak on handle in the PRIMARY term's direction.
//     Default order (empty OrderBy) = handle ascending. The total order removes
//     tie ambiguity.
//  2. after: drop all elements up to and including the element whose handle ==
//     *AfterRef. before: drop all elements from the element whose handle ==
//     *BeforeRef onward.
//  3. HasPrev/HasNext baseline = cursor PRESENCE (HasPrev when AfterRef != nil,
//     HasNext when BeforeRef != nil), unconditional on the handle being found —
//     cursor pagination is predicate-based, matching both ORMs.
//  4. first: if len > *First, keep the first *First and set HasNext. last: if
//     len > *Last, keep the LAST *Last and set HasPrev.
//  5. Rows are the survivors in display (sorted) order, each with its id Ref
//     and fields. StartHandle/EndHandle are the first/last surviving handle.
//
// The backward case falls out naturally: BeforeRef trims the right, Last keeps
// the tail of what remains — no separate direction-reversal logic, so the
// before-cursor mirror is correct by construction.
func paginate(posts []*post, p op.PaginatePosts) Result {
	// Step 0: validation. Both velox and ent reject first+last together and
	// negative first/last with a validation error (velox: gqlrelay
	// ValidateFirstLast; ent: errInvalidPagination). The oracle mirrors that so
	// it never disagrees with either ORM on bad input.
	if p.First != nil && p.Last != nil {
		return Result{Err: ErrValidation}
	}
	if p.First != nil && *p.First < 0 {
		return Result{Err: ErrValidation}
	}
	if p.Last != nil && *p.Last < 0 {
		return Result{Err: ErrValidation}
	}

	sorted := make([]*post, len(posts))
	copy(sorted, posts)
	sortPosts(sorted, p.OrderBy)

	hasPrev := false
	hasNext := false

	// Step 2 + 3: cursor trimming. Both ORMs set HasPreviousPage = (after !=
	// nil) and HasNextPage = (before != nil) UNCONDITIONALLY — cursor
	// pagination is predicate-based, so the flag reflects cursor PRESENCE, not
	// whether the cursor handle exists in the data. The trim still happens when
	// the handle is found.
	if p.AfterRef != nil {
		hasPrev = true
		if i := indexOfHandle(sorted, *p.AfterRef); i >= 0 {
			// Dropped elements 0..i (the cursor element and everything before
			// it).
			sorted = sorted[i+1:]
		}
	}
	if p.BeforeRef != nil {
		hasNext = true
		if i := indexOfHandle(sorted, *p.BeforeRef); i >= 0 {
			// Dropped elements i..end (the cursor element and everything after
			// it).
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
	return Result{Rows: rows, Page: page, Err: ErrOK}
}

// sortPosts sorts in place by the order terms, with a final handle tiebreak so
// the ordering is always total. The tiebreak follows the PRIMARY order term's
// direction: both ORMs append the id term with the first order term's direction
// (velox: idDir = directions[0]; ent: applyOrder emits the id term in the same
// direction). An empty/default OrderBy ties handle ascending.
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
		// Total-order tiebreak: handle in the primary order term's direction.
		desc := len(terms) > 0 && terms[0].Desc
		if desc {
			return a.handle > b.handle
		}
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
