package model

import (
	"velox.test/parity/compare"
	"velox.test/parity/op"
)

// author is the in-memory author entity, keyed by its creation handle.
type author struct {
	handle int
	name   string
	age    int
	role   string
	bio    *string
	labels []string
}

// post is the in-memory post entity, keyed by its creation handle.
type post struct {
	handle    int
	title     string
	status    string
	viewCount int
	labels    []string
	authorRef int
	createdAt int // op index at creation (monotone, deterministic clock)
	updatedAt int // op index at last mutation
	deleted   bool
}

// comment is the in-memory comment entity, keyed by its creation handle.
type comment struct {
	handle    int
	content   string
	labels    []string
	postRef   int
	authorRef int
}

// tag is the in-memory tag entity, keyed by its creation handle.
type tag struct {
	handle int
	name   string
}

// State is the mutable world the interpreter walks. Entities are keyed by
// creation handle; postOrder preserves insertion order for stable iteration.
type State struct {
	authors   map[int]*author
	posts     map[int]*post
	comments  map[int]*comment
	tags      map[int]*tag
	postTags  map[int]map[int]bool // post handle -> set of tag handles
	postOrder []int                // post handles in insertion order
}

func newState() *State {
	return &State{
		authors:  map[int]*author{},
		posts:    map[int]*post{},
		comments: map[int]*comment{},
		tags:     map[int]*tag{},
		postTags: map[int]map[int]bool{},
	}
}

// Run interprets a program, producing exactly one Result per op in program
// order. The returned error is for harness-internal failures only; per-op
// outcomes (including expected errors like not-found) are encoded in
// Result.Err, never the returned error.
func Run(prog op.Program) ([]Result, error) {
	st := newState()
	results := make([]Result, len(prog))
	for i, o := range prog {
		results[i] = st.step(i, o)
	}
	return results, nil
}

// step applies one op at program index idx (the op's creation handle) and
// returns its Result.
func (st *State) step(idx int, o op.Op) Result {
	switch v := o.(type) {
	case op.CreateAuthor:
		st.authors[idx] = &author{
			handle: idx, name: v.Name, age: v.Age, role: v.Role,
			bio: v.Bio, labels: cloneStrings(v.Labels),
		}
		return Result{Err: compare.ErrOK}
	case op.CreatePost:
		st.posts[idx] = &post{
			handle: idx, title: v.Title, status: v.Status, viewCount: v.ViewCount,
			labels: cloneStrings(v.Labels), authorRef: v.AuthorRef,
			createdAt: idx, updatedAt: idx,
		}
		st.postOrder = append(st.postOrder, idx)
		return Result{Err: compare.ErrOK}
	case op.CreateComment:
		st.comments[idx] = &comment{
			handle: idx, content: v.Content, labels: cloneStrings(v.Labels),
			postRef: v.PostRef, authorRef: v.AuthorRef,
		}
		return Result{Err: compare.ErrOK}
	case op.CreateTag:
		st.tags[idx] = &tag{handle: idx, name: v.Name}
		return Result{Err: compare.ErrOK}
	case op.AddTagToPost:
		return st.addTagToPost(v)
	case op.SetPostLabels:
		return st.mutatePost(idx, v.PostRef, func(p *post) {
			p.labels = cloneStrings(v.Labels)
		})
	case op.AppendPostLabels:
		return st.mutatePost(idx, v.PostRef, func(p *post) {
			p.labels = append(p.labels, v.Labels...)
		})
	case op.UpdatePostViewCount:
		return st.mutatePost(idx, v.PostRef, func(p *post) {
			p.viewCount = v.ViewCount
		})
	case op.DeletePost:
		return st.deletePost(v.PostRef)
	case op.QueryPostsByStatus:
		return st.queryPostsByStatus(v.Status)
	case op.CountPosts:
		n := st.livePostCount()
		return Result{Scalar: &n, Err: compare.ErrOK}
	case op.SumViewCount:
		return st.sumViewCount()
	case op.LoadAuthorPosts:
		return st.loadAuthorPosts(v.AuthorRef)
	case op.PaginatePosts:
		return paginate(st.livePosts(), v)
	default:
		// Unknown op kinds are a harness authoring bug; surface loudly.
		panic("model: unhandled op type")
	}
}

// livePost returns the live (non-deleted) post at handle, or nil.
func (st *State) livePost(handle int) *post {
	p, ok := st.posts[handle]
	if !ok || p.deleted {
		return nil
	}
	return p
}

// mutatePost applies mut to the live post at ref, recording the op index as the
// updated_at clock. Missing/deleted posts yield ErrNotFound.
func (st *State) mutatePost(idx, ref int, mut func(*post)) Result {
	p := st.livePost(ref)
	if p == nil {
		return Result{Err: compare.ErrNotFound}
	}
	mut(p)
	p.updatedAt = idx
	return Result{Err: compare.ErrOK}
}

func (st *State) deletePost(ref int) Result {
	p := st.livePost(ref)
	if p == nil {
		return Result{Err: compare.ErrNotFound}
	}
	p.deleted = true
	return Result{Err: compare.ErrOK}
}

func (st *State) addTagToPost(v op.AddTagToPost) Result {
	p := st.livePost(v.PostRef)
	if p == nil {
		return Result{Err: compare.ErrNotFound}
	}
	if _, ok := st.tags[v.TagRef]; !ok {
		return Result{Err: compare.ErrNotFound}
	}
	set := st.postTags[v.PostRef]
	if set == nil {
		set = map[int]bool{}
		st.postTags[v.PostRef] = set
	}
	set[v.TagRef] = true
	return Result{Err: compare.ErrOK}
}

// livePosts returns live posts in insertion order.
func (st *State) livePosts() []*post {
	out := make([]*post, 0, len(st.postOrder))
	for _, h := range st.postOrder {
		if p := st.livePost(h); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (st *State) livePostCount() int {
	n := 0
	for _, h := range st.postOrder {
		if st.livePost(h) != nil {
			n++
		}
	}
	return n
}

func (st *State) queryPostsByStatus(status string) Result {
	var rows []Row
	for _, p := range st.livePosts() {
		if p.status == status {
			rows = append(rows, postRow(p))
		}
	}
	return Result{Rows: rows, Err: compare.ErrOK}
}

func (st *State) sumViewCount() Result {
	sum := 0
	for _, p := range st.livePosts() {
		sum += p.viewCount
	}
	return Result{Scalar: &sum, Err: compare.ErrOK}
}

func (st *State) loadAuthorPosts(ref int) Result {
	var rows []Row
	for _, p := range st.livePosts() {
		if p.authorRef == ref {
			rows = append(rows, postRow(p))
		}
	}
	return Result{Rows: rows, Err: compare.ErrOK}
}

// postRow projects a post into its normalized Row. Every Row carries "id" as a
// Ref to its own handle and "author" as a Ref to its owner. created_at/updated_at
// are the deterministic monotone-int clock (op index), not wall time.
func postRow(p *post) Row {
	return Row{
		"id":         Ref{Handle: p.handle},
		"title":      Value(p.title),
		"status":     Value(p.status),
		"view_count": Value(p.viewCount),
		"labels":     Value(cloneStrings(p.labels)),
		"created_at": Value(p.createdAt),
		"updated_at": Value(p.updatedAt),
		"author":     Ref{Handle: p.authorRef},
	}
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
