package model

import "velox.test/parity/op"

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

// tag is the in-memory tag entity, keyed by its creation handle. name carries a
// UNIQUE constraint in both schemas, so usageCount is keyed by name for upsert.
type tag struct {
	handle     int
	name       string
	usageCount int
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
	synthSeq  int                  // monotonic key source for handle-less rows (bulk)
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
		return Result{Err: ErrOK}
	case op.CreatePost:
		st.posts[idx] = &post{
			handle: idx, title: v.Title, status: v.Status, viewCount: v.ViewCount,
			labels: cloneStrings(v.Labels), authorRef: v.AuthorRef,
			createdAt: idx, updatedAt: idx,
		}
		st.postOrder = append(st.postOrder, idx)
		return Result{Err: ErrOK}
	case op.CreateComment:
		st.comments[idx] = &comment{
			handle: idx, content: v.Content, labels: cloneStrings(v.Labels),
			postRef: v.PostRef, authorRef: v.AuthorRef,
		}
		return Result{Err: ErrOK}
	case op.CreateTag:
		st.tags[idx] = &tag{handle: idx, name: v.Name}
		return Result{Err: ErrOK}
	case op.UpsertTag:
		return st.upsertTag(idx, v)
	case op.BulkCreateTags:
		return st.bulkCreateTags(v)
	case op.SumTagUsage:
		return st.sumTagUsage()
	case op.AddTagToPost:
		return st.addTagToPost(v)
	case op.RemoveTagFromPost:
		return st.removeTagFromPost(v)
	case op.CountPostTags:
		return st.countPostTags(v.PostRef)
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
	case op.SetAuthorBio:
		return st.setAuthorBio(v)
	case op.CountAuthorsWithBio:
		return st.countAuthorsWithBio()
	case op.BulkAddViewCountByStatus:
		return st.bulkAddViewCountByStatus(idx, v)
	case op.DeletePost:
		return st.deletePost(v.PostRef)
	case op.BulkDeletePostsByStatus:
		return st.bulkDeletePostsByStatus(v.Status)
	case op.QueryPostsByStatus:
		return st.queryPostsByStatus(v.Status)
	case op.CountPosts:
		n := st.livePostCount()
		return Result{Scalar: &n, Err: ErrOK}
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
		return Result{Err: ErrNotFound}
	}
	mut(p)
	p.updatedAt = idx
	return Result{Err: ErrOK}
}

// bulkAddViewCountByStatus adds Delta to every live post matching status,
// stamping updatedAt, and returns the affected-row count. Delta>=1 so matched ==
// changed (dialect-robust).
func (st *State) bulkAddViewCountByStatus(idx int, v op.BulkAddViewCountByStatus) Result {
	n := 0
	for _, p := range st.livePosts() {
		if p.status == v.Status {
			p.viewCount += v.Delta
			p.updatedAt = idx
			n++
		}
	}
	return Result{Scalar: &n, Err: ErrOK}
}

// bulkDeletePostsByStatus deletes every live post matching status (cascading
// comments, via deletePost) and returns the deleted-row count.
func (st *State) bulkDeletePostsByStatus(status string) Result {
	n := 0
	for _, p := range st.livePosts() {
		if p.status == status {
			st.deletePost(p.handle)
			n++
		}
	}
	return Result{Scalar: &n, Err: ErrOK}
}

func (st *State) deletePost(ref int) Result {
	p := st.livePost(ref)
	if p == nil {
		return Result{Err: ErrNotFound}
	}
	p.deleted = true
	// Comment.post has ON DELETE CASCADE (parity schema), so deleting a post also
	// removes its comments. Modeling the cascade keeps the oracle consistent with
	// the real databases: with the correct migration, deleting a post that has a
	// comment succeeds on all three; if velox's migration emitted the wrong FK
	// action it would FK-fail instead, surfacing as a VeloxBug.
	for handle, c := range st.comments {
		if c.postRef == ref {
			delete(st.comments, handle)
		}
	}
	return Result{Err: ErrOK}
}

// upsertTag inserts a tag with the unique name, or adds AddUsage onto the
// usage_count of the existing tag of that name. Returns the resulting
// usage_count as a Scalar — the value the real DBs' DO UPDATE SET must produce.
func (st *State) upsertTag(idx int, v op.UpsertTag) Result {
	for _, tg := range st.tags {
		if tg.name == v.Name {
			tg.usageCount += v.AddUsage
			n := tg.usageCount
			return Result{Scalar: &n, Err: ErrOK}
		}
	}
	st.tags[idx] = &tag{handle: idx, name: v.Name, usageCount: v.AddUsage}
	n := v.AddUsage
	return Result{Scalar: &n, Err: ErrOK}
}

// bulkCreateTags inserts every spec as a tag in one logical batch. Rows are
// keyed by a synthetic monotonic sequence (not a program handle) since they are
// observed only in aggregate. Tag names are unique program-wide by construction.
func (st *State) bulkCreateTags(v op.BulkCreateTags) Result {
	for _, spec := range v.Specs {
		st.synthSeq--
		st.tags[st.synthSeq] = &tag{handle: st.synthSeq, name: spec.Name, usageCount: spec.UsageCount}
	}
	return Result{Err: ErrOK}
}

// sumTagUsage returns the sum of usage_count across every tag (single, upserted,
// and bulk), mirroring SUM(usage_count) over the tags table.
func (st *State) sumTagUsage() Result {
	sum := 0
	for _, tg := range st.tags {
		sum += tg.usageCount
	}
	return Result{Scalar: &sum, Err: ErrOK}
}

func (st *State) addTagToPost(v op.AddTagToPost) Result {
	p := st.livePost(v.PostRef)
	if p == nil {
		return Result{Err: ErrNotFound}
	}
	if _, ok := st.tags[v.TagRef]; !ok {
		return Result{Err: ErrNotFound}
	}
	set := st.postTags[v.PostRef]
	if set == nil {
		set = map[int]bool{}
		st.postTags[v.PostRef] = set
	}
	set[v.TagRef] = true
	return Result{Err: ErrOK}
}

// setAuthorBio sets (Bio non-nil) or clears to NULL (Bio nil) the author's bio.
// A missing author handle yields ErrNotFound.
func (st *State) setAuthorBio(v op.SetAuthorBio) Result {
	a, ok := st.authors[v.AuthorRef]
	if !ok {
		return Result{Err: ErrNotFound}
	}
	if v.Bio == nil {
		a.bio = nil
	} else {
		b := *v.Bio
		a.bio = &b
	}
	return Result{Err: ErrOK}
}

// countAuthorsWithBio counts authors whose bio is non-NULL, mirroring
// SELECT COUNT(*) ... WHERE bio IS NOT NULL.
func (st *State) countAuthorsWithBio() Result {
	n := 0
	for _, a := range st.authors {
		if a.bio != nil {
			n++
		}
	}
	return Result{Scalar: &n, Err: ErrOK}
}

// removeTagFromPost detaches TagRef from PostRef's tag set. Detaching a tag that
// is not attached is a no-op (matching the ORMs). Missing/deleted post or
// missing tag handle yields ErrNotFound, mirroring addTagToPost.
func (st *State) removeTagFromPost(v op.RemoveTagFromPost) Result {
	p := st.livePost(v.PostRef)
	if p == nil {
		return Result{Err: ErrNotFound}
	}
	if _, ok := st.tags[v.TagRef]; !ok {
		return Result{Err: ErrNotFound}
	}
	if set := st.postTags[v.PostRef]; set != nil {
		delete(set, v.TagRef)
	}
	return Result{Err: ErrOK}
}

// countPostTags returns the number of tags attached to a live post (its M2M edge
// degree). A missing/deleted post yields ErrNotFound.
func (st *State) countPostTags(ref int) Result {
	if st.livePost(ref) == nil {
		return Result{Err: ErrNotFound}
	}
	n := len(st.postTags[ref])
	return Result{Scalar: &n, Err: ErrOK}
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
	return Result{Rows: rows, Err: ErrOK}
}

func (st *State) sumViewCount() Result {
	sum := 0
	for _, p := range st.livePosts() {
		sum += p.viewCount
	}
	return Result{Scalar: &sum, Err: ErrOK}
}

func (st *State) loadAuthorPosts(ref int) Result {
	var rows []Row
	for _, p := range st.livePosts() {
		if p.authorRef == ref {
			rows = append(rows, postRow(p))
		}
	}
	return Result{Rows: rows, Err: ErrOK}
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
