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
// index of the op that created the entity.
//
// posts retains EVERY created post handle, including deleted ones: the
// post-mutation ops (SetPostLabels, AppendPostLabels, UpdatePostViewCount,
// DeletePost) and AddTagToPost may reference a deleted post — they exercise the
// not-found path, which all three executors agree on.
//
// deleted records hard-deleted posts. Ops that require a LIVE post row — a
// CreateComment FK parent, or a pagination cursor anchor — must NOT reference a
// deleted post: the post is physically gone, so the ORMs FK-violate (comment)
// or compute a cursor with no anchor row (pagination), while the oracle, which
// does not model deletion at those sites, reports a different outcome. That
// disagreement is a generator validity artifact, not a bug, so the builder
// draws those refs from livePosts() only.
//
// The comments->posts FK is OnDelete: Cascade in both schemas (declared on the
// parent's assoc edge, matching Ent), so hard-deleting a post that still has a
// comment SUCCEEDS in both ORMs — the comment rows are cascade-deleted — and the
// oracle models the same cascade. DeletePost may therefore target ANY created
// post; doing so also exercises the migration's FK action end to end: a wrong ON
// DELETE would FK-fail in velox only, surfacing as a VeloxBug. (The post_tags M2M
// FK is likewise OnDelete: Cascade.)
type state struct {
	authors  []int
	posts    []int
	tags     []int
	comments []int
	deleted  map[int]bool
}

// markDeleted records that the post at handle h has been hard-deleted.
func (s *state) markDeleted(h int) {
	if s.deleted == nil {
		s.deleted = map[int]bool{}
	}
	s.deleted[h] = true
}

// livePosts returns the post handles that have not been deleted, in creation
// order. Used for refs that require a live row (CreateComment FK parent,
// pagination cursor anchor).
func (s *state) livePosts() []int {
	out := make([]int, 0, len(s.posts))
	for _, h := range s.posts {
		if !s.deleted[h] {
			out = append(out, h)
		}
	}
	return out
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
//	CreateComment            -> >=1 LIVE post AND >=1 author (FK parent must exist)
//	AddTagToPost             -> >=1 post AND >=1 tag (deleted handle -> not-found, agreed)
//	SetPostLabels, AppendPostLabels, UpdatePostViewCount
//	                         -> >=1 post (deleted handle -> not-found, agreed)
//	DeletePost               -> >=1 deletable post (no live-commented post: RESTRICT FK)
//	LoadAuthorPosts          -> >=1 author
func (s *state) satisfiable() []opKind {
	out := []opKind{
		kCreateAuthor, kCreateTag,
		kQueryPostsByStatus, kCountPosts, kSumViewCount, kPaginatePosts,
	}
	if len(s.authors) > 0 {
		out = append(out, kCreatePost, kLoadAuthorPosts)
	}
	// CreateComment writes an FK to its parent post, so the parent must be a
	// LIVE row — a deleted post is physically gone and the INSERT would
	// FK-violate in both ORMs while the oracle stores it happily.
	if len(s.livePosts()) > 0 && len(s.authors) > 0 {
		out = append(out, kCreateComment)
	}
	if len(s.posts) > 0 && len(s.tags) > 0 {
		out = append(out, kAddTagToPost)
	}
	if len(s.posts) > 0 {
		out = append(out, kSetPostLabels, kAppendPostLabels, kUpdatePostViewCount)
	}
	// DeletePost may target any created post: the comments->posts FK cascades, so
	// deleting a commented post succeeds (and exercises the cascade), and
	// re-deleting an already-deleted post hits the agreed not-found path.
	if len(s.posts) > 0 {
		out = append(out, kDeletePost)
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
		// PostRef is an FK parent that must point to a LIVE post (satisfiable
		// guarantees >=1 live post). A deleted post would FK-violate the INSERT.
		postRef := ref(c, st.livePosts())
		return op.CreateComment{
			Content:   name(c, "C"),
			Labels:    labels(c),
			PostRef:   postRef,
			AuthorRef: ref(c, st.authors),
		}
	case kCreateTag:
		st.tags = append(st.tags, idx)
		// Tag.name carries a UNIQUE constraint in both schemas. Derive the name
		// from the creation handle (op index), which is unique across the whole
		// program, so a CreateTag never collides — a duplicate name would fail
		// the unique constraint in BOTH ORMs while the oracle (which does not
		// model the constraint) reports ok, a spurious ReferenceSuspect. Keeping
		// names unique is a validity rule, the uniqueness cousin of referential
		// validity, enforced by Validate.
		return op.CreateTag{Name: fmt.Sprintf("T%d", idx)}
	case kAddTagToPost:
		return op.AddTagToPost{PostRef: ref(c, st.posts), TagRef: ref(c, st.tags)}
	case kSetPostLabels:
		return op.SetPostLabels{PostRef: ref(c, st.posts), Labels: labels(c)}
	case kAppendPostLabels:
		return op.AppendPostLabels{PostRef: ref(c, st.posts), Labels: labels(c)}
	case kUpdatePostViewCount:
		return op.UpdatePostViewCount{PostRef: ref(c, st.posts), ViewCount: c.next(1001)}
	case kDeletePost:
		// Any created post is a valid target: the comments->posts FK cascades, so
		// deleting a commented post succeeds (cascade-deleting its comments), and a
		// re-delete of an already-deleted post hits the agreed not-found path.
		target := ref(c, st.posts)
		st.markDeleted(target)
		return op.DeletePost{PostRef: target}
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
// cursor chosen from LIVE post handles, and optionally orders by view_count.
//
// The cursor anchor (AfterRef/BeforeRef) must be a live post: both ORMs encode
// the cursor from the anchor row's (view_count, id) and derive HasPrev/HasNext
// from rows on either side of that value. With a deleted anchor the row is gone
// and the ORMs produce different page flags than the oracle, whose
// cursor-presence rule (HasPrev when AfterRef!=nil, HasNext when BeforeRef!=nil)
// assumes the anchor resolves to a real position. Drawing from livePosts() keeps
// the cursor semantics aligned across all three.
func paginateOp(c *cursor, st *state) op.Op {
	var p op.PaginatePosts
	live := st.livePosts()
	// Choose direction: 0 => first, 1 => last. (Always pick one; the limit can
	// still be 0, which both ORMs accept.)
	if c.next(2) == 0 {
		first := c.next(21) // [0, 20]
		p.First = &first
		if len(live) > 0 && c.next(2) == 0 {
			after := ref(c, live)
			p.AfterRef = &after
		}
	} else {
		last := c.next(21)
		p.Last = &last
		if len(live) > 0 && c.next(2) == 0 {
			before := ref(c, live)
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
// Deletion handling is asymmetric, by design:
//   - The post-mutation ops (SetPostLabels, AppendPostLabels,
//     UpdatePostViewCount, DeletePost) and AddTagToPost MAY reference a deleted
//     post — they exercise the not-found path, which all three executors agree
//     on. Validate only checks the handle was created as a post.
//   - CreateComment's FK parent and a pagination cursor anchor MUST be a LIVE
//     post: a deleted post is physically gone, so the ORMs FK-violate (comment)
//     or compute a cursor with no anchor row (pagination) while the oracle
//     reports a different outcome — a generator validity artifact, not a bug.
//   - DeletePost may target any created post, including a live one that still
//     has a comment: the comments->posts FK is OnDelete: Cascade, so the delete
//     succeeds in both ORMs (cascade-deleting the comments) and the oracle models
//     the same cascade. Re-deleting an already-deleted post hits the agreed
//     not-found path.
func Validate(prog op.Program) error {
	authors := map[int]bool{}
	posts := map[int]bool{}
	deleted := map[int]bool{}
	tags := map[int]bool{}
	tagNames := map[string]bool{}
	live := func(h int) bool { return posts[h] && !deleted[h] }

	for i, o := range prog {
		switch v := o.(type) {
		case op.CreateAuthor:
			authors[i] = true
		case op.CreateTag:
			// Tag.name is UNIQUE in both schemas; a duplicate name fails in both
			// ORMs while the oracle reports ok (a spurious ReferenceSuspect), so a
			// valid program must never repeat a tag name.
			if tagNames[v.Name] {
				return fmt.Errorf("op %d CreateTag: duplicate tag name %q violates the unique constraint", i, v.Name)
			}
			tagNames[v.Name] = true
			tags[i] = true
		case op.CreatePost:
			if !authors[v.AuthorRef] {
				return fmt.Errorf("op %d CreatePost: AuthorRef %d is not an existing author handle", i, v.AuthorRef)
			}
			posts[i] = true
		case op.CreateComment:
			if !live(v.PostRef) {
				return fmt.Errorf("op %d CreateComment: PostRef %d is not a LIVE post handle (FK parent must exist)", i, v.PostRef)
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
			// Any created post may be deleted: the comments->posts FK cascades, so
			// deleting a commented post succeeds in both ORMs and the oracle.
			deleted[v.PostRef] = true
		case op.LoadAuthorPosts:
			if !authors[v.AuthorRef] {
				return fmt.Errorf("op %d LoadAuthorPosts: AuthorRef %d is not an existing author handle", i, v.AuthorRef)
			}
		case op.PaginatePosts:
			if err := validatePaginate(i, v, live); err != nil {
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
// together, non-negative limits, and any cursor anchor points to a LIVE post
// handle (live reports whether a handle is an undeleted post). OrderBy fields
// must be in the generated domain.
func validatePaginate(i int, p op.PaginatePosts, live func(int) bool) error {
	if p.First != nil && p.Last != nil {
		return fmt.Errorf("op %d PaginatePosts: First and Last both set", i)
	}
	if p.First != nil && *p.First < 0 {
		return fmt.Errorf("op %d PaginatePosts: negative First %d", i, *p.First)
	}
	if p.Last != nil && *p.Last < 0 {
		return fmt.Errorf("op %d PaginatePosts: negative Last %d", i, *p.Last)
	}
	if p.AfterRef != nil && !live(*p.AfterRef) {
		return fmt.Errorf("op %d PaginatePosts: AfterRef %d is not a LIVE post handle (cursor anchor must exist)", i, *p.AfterRef)
	}
	if p.BeforeRef != nil && !live(*p.BeforeRef) {
		return fmt.Errorf("op %d PaginatePosts: BeforeRef %d is not a LIVE post handle (cursor anchor must exist)", i, *p.BeforeRef)
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
