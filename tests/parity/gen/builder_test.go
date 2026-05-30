package gen_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/gen"
	"velox.test/parity/op"
)

func TestBuild_EmptyInputIsEmptyOrTrivial(t *testing.T) {
	prog := gen.Build(nil)
	assert.LessOrEqual(t, len(prog), 1, "no bytes => at most a trivial program")
}

func TestBuild_IsReferentiallyValid(t *testing.T) {
	// Across many random byte streams, every emitted program must be
	// referentially valid: every ref points to an existing earlier handle of
	// the correct kind, and the first Create of a dependent kind is preceded
	// by its dependency.
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		buf := make([]byte, r.Intn(80))
		r.Read(buf)
		prog := gen.Build(buf)
		require.NoError(t, gen.Validate(prog),
			"iter %d produced an invalid program:\n%s", iter, op.Format(prog))
	}
}

func TestBuild_Deterministic(t *testing.T) {
	buf := []byte{3, 7, 1, 9, 4, 2, 8, 5, 6, 0, 11, 13}
	a := op.Format(gen.Build(buf))
	b := op.Format(gen.Build(buf))
	assert.Equal(t, a, b, "Build must be a pure function of its input bytes")
}

// TestBuild_TagNamesUnique pins the generator-side fix for the first finding
// surfaced by the generative leg: Tag.name is UNIQUE in both schemas, so a
// program that creates two tags with the same name fails the constraint in
// BOTH ORMs while the oracle (which does not model the constraint) reports ok —
// a spurious ReferenceSuspect. Build must never emit a duplicate tag name.
func TestBuild_TagNamesUnique(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		buf := make([]byte, r.Intn(120))
		r.Read(buf)
		prog := gen.Build(buf)
		seen := map[string]bool{}
		for _, o := range prog {
			ct, ok := o.(op.CreateTag)
			if !ok {
				continue
			}
			require.Falsef(t, seen[ct.Name],
				"iter %d emitted duplicate tag name %q:\n%s", iter, ct.Name, op.Format(prog))
			seen[ct.Name] = true
		}
	}
}

// TestValidate_RejectsDuplicateTagName pins that Validate itself rejects a
// hand-built program with a duplicate tag name, so the uniqueness invariant is
// executable and not merely a property of the current Build implementation.
func TestValidate_RejectsDuplicateTagName(t *testing.T) {
	prog := op.Program{
		op.CreateTag{Name: "dup"},
		op.CreateTag{Name: "dup"},
	}
	assert.Error(t, gen.Validate(prog), "duplicate tag name must be rejected")
}

// TestBuild_NoCommentOrCursorOnDeletedPost pins the generator-side fix for the
// second finding class: a CreateComment FK parent and a pagination cursor anchor
// must reference a LIVE (undeleted) post. A deleted post is physically gone, so
// the comment INSERT FK-violates and the pagination cursor has no anchor row in
// BOTH ORMs, while the oracle reports a different outcome — a spurious
// ReferenceSuspect. The mutation ops (Set/Append/Update/Delete) and AddTagToPost
// may still reference deleted posts (they hit the agreed not-found path).
func TestBuild_NoCommentOrCursorOnDeletedPost(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for iter := 0; iter < 4000; iter++ {
		buf := make([]byte, r.Intn(160))
		r.Read(buf)
		prog := gen.Build(buf)
		deleted := map[int]bool{}
		for i, o := range prog {
			switch v := o.(type) {
			case op.DeletePost:
				deleted[v.PostRef] = true
			case op.CreateComment:
				require.Falsef(t, deleted[v.PostRef],
					"iter %d op %d CreateComment FK parent post %d is deleted:\n%s",
					iter, i, v.PostRef, op.Format(prog))
			case op.PaginatePosts:
				if v.AfterRef != nil {
					require.Falsef(t, deleted[*v.AfterRef],
						"iter %d op %d PaginatePosts AfterRef anchor post %d is deleted:\n%s",
						iter, i, *v.AfterRef, op.Format(prog))
				}
				if v.BeforeRef != nil {
					require.Falsef(t, deleted[*v.BeforeRef],
						"iter %d op %d PaginatePosts BeforeRef anchor post %d is deleted:\n%s",
						iter, i, *v.BeforeRef, op.Format(prog))
				}
			}
		}
	}
}

// TestValidate_RejectsCommentOnDeletedPost pins that Validate rejects a
// hand-built program whose CreateComment FK parent has been deleted.
func TestValidate_RejectsCommentOnDeletedPost(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0},
		op.DeletePost{PostRef: 1},
		op.CreateComment{Content: "c", PostRef: 1, AuthorRef: 0},
	}
	assert.Error(t, gen.Validate(prog), "comment on a deleted post must be rejected")
}

// TestValidate_RejectsCursorOnDeletedPost pins that Validate rejects a
// pagination cursor anchored on a deleted post.
func TestValidate_RejectsCursorOnDeletedPost(t *testing.T) {
	after := 1
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0},
		op.DeletePost{PostRef: 1},
		op.PaginatePosts{First: intp(2), AfterRef: &after},
	}
	assert.Error(t, gen.Validate(prog), "pagination cursor on a deleted post must be rejected")
}

// TestValidate_AllowsMutationOnDeletedPost pins the asymmetry: the post-mutation
// ops and AddTagToPost MAY reference a deleted post (agreed not-found path), so
// Validate must NOT reject them.
func TestValidate_AllowsMutationOnDeletedPost(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0},
		op.CreateTag{Name: "t"},
		op.DeletePost{PostRef: 1},
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9}, // not-found, agreed
		op.SetPostLabels{PostRef: 1, Labels: []string{"x"}},
		op.AppendPostLabels{PostRef: 1, Labels: []string{"y"}},
		op.DeletePost{PostRef: 1},        // not-found, agreed
		op.AddTagToPost{PostRef: 1, TagRef: 2}, // not-found, agreed
	}
	assert.NoError(t, gen.Validate(prog), "mutation/tag ops on a deleted post are valid (not-found path)")
}

// TestBuild_NoDeleteOfLiveCommentedPost pins the generator-side fix for the
// third finding class: the comments->posts FK is OnDelete NoAction (RESTRICT),
// so hard-deleting a LIVE post that still has a comment fails in both ORMs while
// the oracle soft-deletes happily. Build must never emit a DeletePost targeting
// a live post that has received a comment. (Posts with only tags delete fine —
// post_tags is OnDelete Cascade — so this rule keys on comments alone.)
func TestBuild_NoDeleteOfLiveCommentedPost(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for iter := 0; iter < 4000; iter++ {
		buf := make([]byte, r.Intn(200))
		r.Read(buf)
		prog := gen.Build(buf)
		deleted := map[int]bool{}
		hasComment := map[int]bool{}
		for i, o := range prog {
			switch v := o.(type) {
			case op.CreateComment:
				hasComment[v.PostRef] = true
			case op.DeletePost:
				live := !deleted[v.PostRef]
				require.Falsef(t, live && hasComment[v.PostRef],
					"iter %d op %d DeletePost targets live commented post %d (RESTRICT FK):\n%s",
					iter, i, v.PostRef, op.Format(prog))
				deleted[v.PostRef] = true
			}
		}
	}
}

// TestValidate_RejectsDeleteOfLiveCommentedPost pins that Validate rejects a
// hand-built program that deletes a live post which has a comment.
func TestValidate_RejectsDeleteOfLiveCommentedPost(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0},
		op.CreateComment{Content: "c", PostRef: 1, AuthorRef: 0},
		op.DeletePost{PostRef: 1}, // RESTRICT FK: blocked
	}
	assert.Error(t, gen.Validate(prog), "delete of a live commented post must be rejected")
}

func intp(i int) *int { return &i }
