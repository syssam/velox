package runner

import (
	"reflect"
	"sort"

	"velox.test/parity/op"
)

// Coverage records which op kinds (and, cheaply, which JSON / edge surfaces) a
// set of parity programs exercises. It turns "the curated suite covers the known
// bug classes" from an assumption into a measured, assertable fact: a coverage
// test fails if any op kind is never exercised, forcing the suite to grow when a
// new op is added rather than silently leaving it untested.
type Coverage struct {
	ops    map[string]int // op kind name -> times seen
	fields map[string]int // touched JSON/label fields -> times seen
	edges  map[string]int // exercised edge relations -> times seen
}

// allOpKinds is the canonical set of op concrete-type names. It is built from a
// zero value of every op type, so adding a new op type here is a one-line,
// compile-checked change; MissingOpKinds compares the seen set against this. If
// a new op.Op is introduced and not added here, the coverage assertion cannot
// "see" it — keep this list in sync with op/op.go (one entry per isOp type).
var allOpKinds = opKindNames([]op.Op{
	op.CreateAuthor{},
	op.CreatePost{},
	op.CreateComment{},
	op.CreateTag{},
	op.UpsertTag{},
	op.BulkCreateTags{},
	op.AddTagToPost{},
	op.RemoveTagFromPost{},
	op.SetPostLabels{},
	op.AppendPostLabels{},
	op.UpdatePostViewCount{},
	op.SetAuthorBio{},
	op.BulkAddViewCountByStatus{},
	op.DeletePost{},
	op.BulkDeletePostsByStatus{},
	op.QueryPostsByStatus{},
	op.CountPosts{},
	op.CountPostTags{},
	op.CountAuthorsWithBio{},
	op.SumViewCount{},
	op.SumTagUsage{},
	op.LoadAuthorPosts{},
	op.PaginatePosts{},
})

// opKindNames returns the concrete type names of a slice of ops, in input order.
func opKindNames(ops []op.Op) []string {
	names := make([]string, len(ops))
	for i, o := range ops {
		names[i] = opKindName(o)
	}
	return names
}

// opKindName returns the concrete type name of an op (e.g. "CreatePost").
func opKindName(o op.Op) string {
	return reflect.TypeOf(o).Name()
}

// CoverProgramSet walks every op of every program and returns the aggregate
// coverage. It is the entry point a coverage test uses to assert the curated
// suite exercises every op kind.
func CoverProgramSet(progs []op.Program) Coverage {
	c := Coverage{
		ops:    map[string]int{},
		fields: map[string]int{},
		edges:  map[string]int{},
	}
	for _, prog := range progs {
		for _, o := range prog {
			c.record(o)
		}
	}
	return c
}

// record tallies one op into the op-kind set plus the cheap field/edge surfaces
// it touches (JSON label writes and edge relations), so coverage can report not
// just "which ops" but "which JSON and edge paths" the suite reaches.
func (c Coverage) record(o op.Op) {
	c.ops[opKindName(o)]++
	switch o.(type) {
	case op.SetPostLabels:
		c.fields["post.labels"]++
	case op.AppendPostLabels:
		c.fields["post.labels"]++
	case op.AddTagToPost:
		c.edges["post.tags"]++
	case op.LoadAuthorPosts:
		c.edges["author.posts"]++
	case op.CreateComment:
		c.edges["post.comments"]++
		c.edges["author.comments"]++
	}
}

// MissingOpKinds returns the op kinds in allOpKinds that no program in the set
// exercised, sorted for stable output. Empty means full op-kind coverage.
func (c Coverage) MissingOpKinds() []string {
	var missing []string
	for _, name := range allOpKinds {
		if c.ops[name] == 0 {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// SeenOpKinds returns the distinct op kinds exercised, sorted.
func (c Coverage) SeenOpKinds() []string {
	seen := make([]string, 0, len(c.ops))
	for name := range c.ops {
		seen = append(seen, name)
	}
	sort.Strings(seen)
	return seen
}

// Fields returns the distinct JSON/label field surfaces exercised, sorted.
func (c Coverage) Fields() []string {
	return sortedKeys(c.fields)
}

// Edges returns the distinct edge relations exercised, sorted.
func (c Coverage) Edges() []string {
	return sortedKeys(c.edges)
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
