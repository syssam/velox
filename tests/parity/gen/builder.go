// Package gen builds referentially-valid parity programs from a raw byte
// stream. It is the generation engine for Stage B: Build consumes bytes to make
// deterministic choices, emitting an op.Program in which every cross-entity
// reference points to an existing earlier handle of the correct kind, so any
// divergence the harness finds is a real bug rather than a validity artifact.
//
// The package imports only op (no ORMs), keeping the architecture guard green.
package gen

import (
	"fmt"

	"velox.test/parity/op"
)

// maxOps bounds the size of a generated program so a single fuzz input can't
// blow up into an enormous, slow-to-execute program.
const maxOps = 64

// Valid parameter domains. Generated params are drawn ONLY from these so the
// reference oracle and both ORMs never disagree on a validity artifact (e.g.
// ordering by an un-annotated field, or an out-of-range view_count).
var (
	roles     = []string{"user", "admin", "guest"}
	statuses  = []string{"draft", "published"}
	labelPool = []string{"a", "b", "c", "d", "e"}
	// orderFields is restricted to "view_count" — the only OrderField the
	// schema annotates. Ordering by un-annotated fields yields a harmless
	// ReferenceSuspect, which we avoid by construction.
	orderFields = []string{"view_count"}
)

// cursor reads bounded choices from a byte stream. When the stream is exhausted
// it returns zero forever, so a short stream yields a short program and Build
// never blocks or panics.
type cursor struct {
	data []byte
	pos  int
}

// next returns a non-negative value in [0, mod). When mod <= 0 it returns 0.
// Out of bytes => 0 (deterministic termination signal for the build loop).
func (c *cursor) next(mod int) int {
	if mod <= 0 {
		return 0
	}
	if c.pos >= len(c.data) {
		return 0
	}
	b := int(c.data[c.pos])
	c.pos++
	return b % mod
}

// more reports whether any bytes remain to consume.
func (c *cursor) more() bool { return c.pos < len(c.data) }

// state tracks the handles created so far, per kind. A handle is the program
// index of the op that created the entity. Deleted posts remain in posts:
// referencing a deleted post is allowed (it exercises not-found, which all
// three executors agree on).
type state struct {
	authors  []int
	posts    []int
	tags     []int
	comments []int
}

// opKind enumerates the generable op kinds for selection.
type opKind int

const (
	kCreateAuthor opKind = iota
	kCreatePost
	kCreateComment
	kCreateTag
	kAddTagToPost
	kSetPostLabels
	kAppendPostLabels
	kUpdatePostViewCount
	kDeletePost
	kQueryPostsByStatus
	kCountPosts
	kSumViewCount
	kLoadAuthorPosts
	kPaginatePosts
)

// satisfiable returns the op kinds whose preconditions hold in the current
// state, in a fixed order (so selection is deterministic given the byte
// stream). The precondition table:
//
//	CreateAuthor, CreateTag, QueryPostsByStatus, CountPosts, SumViewCount,
//	  PaginatePosts          -> always
//	CreatePost               -> >=1 author
//	CreateComment            -> >=1 post AND >=1 author
//	AddTagToPost             -> >=1 post AND >=1 tag
//	SetPostLabels, AppendPostLabels, UpdatePostViewCount, DeletePost
//	                         -> >=1 post (deleted handles allowed)
//	LoadAuthorPosts          -> >=1 author
func (s *state) satisfiable() []opKind {
	out := []opKind{
		kCreateAuthor, kCreateTag,
		kQueryPostsByStatus, kCountPosts, kSumViewCount, kPaginatePosts,
	}
	if len(s.authors) > 0 {
		out = append(out, kCreatePost, kLoadAuthorPosts)
	}
	if len(s.posts) > 0 && len(s.authors) > 0 {
		out = append(out, kCreateComment)
	}
	if len(s.posts) > 0 && len(s.tags) > 0 {
		out = append(out, kAddTagToPost)
	}
	if len(s.posts) > 0 {
		out = append(out,
			kSetPostLabels, kAppendPostLabels, kUpdatePostViewCount, kDeletePost)
	}
	return out
}

// Build deterministically consumes data to emit a referentially-valid
// op.Program. It is a pure function of its input bytes.
func Build(data []byte) op.Program {
	c := &cursor{data: data}
	st := &state{}
	var prog op.Program

	for len(prog) < maxOps && c.more() {
		choices := st.satisfiable()
		// satisfiable always returns >=6 kinds, so len(choices) > 0.
		kind := choices[c.next(len(choices))]
		idx := len(prog) // this op's creation handle
		o := buildOp(c, st, kind, idx)
		prog = append(prog, o)
	}
	return prog
}

// buildOp constructs one op of the given kind, drawing params from valid
// domains and refs from existing handles, and updates state for create ops.
func buildOp(c *cursor, st *state, kind opKind, idx int) op.Op {
	switch kind {
	case kCreateAuthor:
		st.authors = append(st.authors, idx)
		return op.CreateAuthor{
			Name:   name(c, "A"),
			Age:    c.next(120),
			Role:   pick(c, roles),
			Bio:    optString(c, "bio"),
			Labels: labels(c),
		}
	case kCreatePost:
		st.posts = append(st.posts, idx)
		return op.CreatePost{
			Title:     name(c, "P"),
			Status:    pick(c, statuses),
			ViewCount: c.next(1001), // [0, 1000]
			Labels:    labels(c),
			AuthorRef: ref(c, st.authors),
		}
	case kCreateComment:
		st.comments = append(st.comments, idx)
		return op.CreateComment{
			Content:   name(c, "C"),
			Labels:    labels(c),
			PostRef:   ref(c, st.posts),
			AuthorRef: ref(c, st.authors),
		}
	case kCreateTag:
		st.tags = append(st.tags, idx)
		return op.CreateTag{Name: name(c, "T")}
	case kAddTagToPost:
		return op.AddTagToPost{PostRef: ref(c, st.posts), TagRef: ref(c, st.tags)}
	case kSetPostLabels:
		return op.SetPostLabels{PostRef: ref(c, st.posts), Labels: labels(c)}
	case kAppendPostLabels:
		return op.AppendPostLabels{PostRef: ref(c, st.posts), Labels: labels(c)}
	case kUpdatePostViewCount:
		return op.UpdatePostViewCount{PostRef: ref(c, st.posts), ViewCount: c.next(1001)}
	case kDeletePost:
		return op.DeletePost{PostRef: ref(c, st.posts)}
	case kQueryPostsByStatus:
		return op.QueryPostsByStatus{Status: pick(c, statuses)}
	case kCountPosts:
		return op.CountPosts{}
	case kSumViewCount:
		return op.SumViewCount{}
	case kLoadAuthorPosts:
		return op.LoadAuthorPosts{AuthorRef: ref(c, st.authors)}
	case kPaginatePosts:
		return paginateOp(c, st)
	default:
		panic(fmt.Sprintf("gen: unhandled op kind %d", kind))
	}
}

// paginateOp builds a PaginatePosts op. It picks at most one direction (never
// First and Last together), draws the limit from [0, 20], optionally attaches a
// cursor chosen from existing post handles, and optionally orders by
// view_count.
func paginateOp(c *cursor, st *state) op.Op {
	var p op.PaginatePosts
	// Choose direction: 0 => first, 1 => last. (Always pick one; the limit can
	// still be 0, which both ORMs accept.)
	if c.next(2) == 0 {
		first := c.next(21) // [0, 20]
		p.First = &first
		if len(st.posts) > 0 && c.next(2) == 0 {
			after := ref(c, st.posts)
			p.AfterRef = &after
		}
	} else {
		last := c.next(21)
		p.Last = &last
		if len(st.posts) > 0 && c.next(2) == 0 {
			before := ref(c, st.posts)
			p.BeforeRef = &before
		}
	}
	if c.next(2) == 0 {
		p.OrderBy = []op.OrderTerm{{Field: pick(c, orderFields), Desc: c.next(2) == 1}}
	}
	return p
}

// pick chooses an element of a non-empty domain slice.
func pick(c *cursor, domain []string) string {
	return domain[c.next(len(domain))]
}

// ref chooses an existing handle from a non-empty slice. Callers must only call
// ref when the slice is non-empty (guaranteed by satisfiable's preconditions).
func ref(c *cursor, handles []int) int {
	return handles[c.next(len(handles))]
}

// name returns a short deterministic name with a prefix and a small suffix.
func name(c *cursor, prefix string) string {
	return fmt.Sprintf("%s%d", prefix, c.next(100))
}

// optString returns a *string about half the time, nil otherwise.
func optString(c *cursor, s string) *string {
	if c.next(2) == 0 {
		return nil
	}
	v := fmt.Sprintf("%s%d", s, c.next(100))
	return &v
}

// labels returns 0..3 labels drawn from the label pool. The list may be empty.
func labels(c *cursor) []string {
	n := c.next(4) // 0..3
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = pick(c, labelPool)
	}
	return out
}

// Validate replays the precondition table over an emitted program and returns a
// non-nil error if any op references a handle that does not point to an earlier
// Create of the correct kind, or whose dependency was not yet created. It is the
// executable invariant the 2000-iteration random test enforces, and an internal
// sanity check on Build's output.
//
// Deletion is intentionally NOT tracked here: referencing a deleted post is
// referentially valid (it exercises not-found, which all three executors agree
// on). Validate only checks that the handle was created as a post at all.
func Validate(prog op.Program) error {
	authors := map[int]bool{}
	posts := map[int]bool{}
	tags := map[int]bool{}

	for i, o := range prog {
		switch v := o.(type) {
		case op.CreateAuthor:
			authors[i] = true
		case op.CreateTag:
			tags[i] = true
		case op.CreatePost:
			if !authors[v.AuthorRef] {
				return fmt.Errorf("op %d CreatePost: AuthorRef %d is not an existing author handle", i, v.AuthorRef)
			}
			posts[i] = true
		case op.CreateComment:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d CreateComment: PostRef %d is not an existing post handle", i, v.PostRef)
			}
			if !authors[v.AuthorRef] {
				return fmt.Errorf("op %d CreateComment: AuthorRef %d is not an existing author handle", i, v.AuthorRef)
			}
		case op.AddTagToPost:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d AddTagToPost: PostRef %d is not an existing post handle", i, v.PostRef)
			}
			if !tags[v.TagRef] {
				return fmt.Errorf("op %d AddTagToPost: TagRef %d is not an existing tag handle", i, v.TagRef)
			}
		case op.SetPostLabels:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d SetPostLabels: PostRef %d is not an existing post handle", i, v.PostRef)
			}
		case op.AppendPostLabels:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d AppendPostLabels: PostRef %d is not an existing post handle", i, v.PostRef)
			}
		case op.UpdatePostViewCount:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d UpdatePostViewCount: PostRef %d is not an existing post handle", i, v.PostRef)
			}
		case op.DeletePost:
			if !posts[v.PostRef] {
				return fmt.Errorf("op %d DeletePost: PostRef %d is not an existing post handle", i, v.PostRef)
			}
		case op.LoadAuthorPosts:
			if !authors[v.AuthorRef] {
				return fmt.Errorf("op %d LoadAuthorPosts: AuthorRef %d is not an existing author handle", i, v.AuthorRef)
			}
		case op.PaginatePosts:
			if err := validatePaginate(i, v, posts); err != nil {
				return err
			}
		case op.QueryPostsByStatus, op.CountPosts, op.SumViewCount:
			// Always satisfiable; no refs.
		default:
			return fmt.Errorf("op %d: Validate saw unknown op type %T", i, o)
		}
	}
	return nil
}

// validatePaginate checks a PaginatePosts op's invariants: never First and Last
// together, non-negative limits, and any cursor ref points to an existing post
// handle. OrderBy fields must be in the generated domain.
func validatePaginate(i int, p op.PaginatePosts, posts map[int]bool) error {
	if p.First != nil && p.Last != nil {
		return fmt.Errorf("op %d PaginatePosts: First and Last both set", i)
	}
	if p.First != nil && *p.First < 0 {
		return fmt.Errorf("op %d PaginatePosts: negative First %d", i, *p.First)
	}
	if p.Last != nil && *p.Last < 0 {
		return fmt.Errorf("op %d PaginatePosts: negative Last %d", i, *p.Last)
	}
	if p.AfterRef != nil && !posts[*p.AfterRef] {
		return fmt.Errorf("op %d PaginatePosts: AfterRef %d is not an existing post handle", i, *p.AfterRef)
	}
	if p.BeforeRef != nil && !posts[*p.BeforeRef] {
		return fmt.Errorf("op %d PaginatePosts: BeforeRef %d is not an existing post handle", i, *p.BeforeRef)
	}
	for _, term := range p.OrderBy {
		ok := false
		for _, f := range orderFields {
			if term.Field == f {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("op %d PaginatePosts: OrderBy field %q outside generated domain", i, term.Field)
		}
	}
	return nil
}
