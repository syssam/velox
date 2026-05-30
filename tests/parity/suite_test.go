package parity_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// TestCuratedSuite_SQLite runs a table of curated parity programs through the
// three-way driver (reference ⟷ velox ⟷ ent) on in-memory SQLite. Each case
// declares its expectation:
//
//   - expectAllPass: all three executors must agree on every op. This is the
//     default and the strongest assertion.
//   - expectVeloxCorrect: velox must match the reference on every op (zero
//     VeloxBugs and velox is never the outlier), but ent is allowed to diverge.
//     This is used ONLY for the JSON-array-append case. The `labels` column is
//     declared json in both schemas; the value is blob-stored because both ORMs
//     bind it as a []byte param (no column-type asymmetry between them). The real
//     differentiator is the append SQL: ent emits JSON_INSERT(labels, '$[#]', ?)
//     which SQLite rejects ("malformed JSON") on the blob-stored JSON value,
//     while velox emits CAST(labels AS TEXT) + json_each, which succeeds — a real
//     EntDivergent the harness SURFACES rather than silences. The driver's SQL
//     trace pinpoints the divergence; see README and the case comment.
func TestCuratedSuite_SQLite(t *testing.T) {
	for _, tc := range curatedPrograms() {
		t.Run(tc.name, func(t *testing.T) {
			rep := runner.RunParity(t, runner.SQLite, tc.prog)
			switch tc.expect {
			case expectAllPass:
				require.True(t, rep.AllPass(), "%s: expected all-pass, got:\n%s", tc.name, rep)
			case expectVeloxCorrect:
				// velox must be correct; ent may diverge (documented finding).
				require.Zero(t, rep.CountVeloxBugs(), "%s: velox diverged from reference (VeloxBug):\n%s", tc.name, rep)
				require.Zero(t, rep.CountReferenceSuspect(), "%s: reference suspect (velox AND ent disagree):\n%s", tc.name, rep)
				require.NotZero(t, rep.CountEntDivergent(), "%s: expected a documented ent divergence but found none — recheck the finding:\n%s", tc.name, rep)
			}
		})
	}
}

type expectation int

const (
	expectAllPass expectation = iota
	expectVeloxCorrect
)

type progCase struct {
	name   string
	prog   op.Program
	expect expectation
}

func intp(i int) *int { return &i }

// curatedPrograms returns the curated parity programs covering each bug class.
func curatedPrograms() []progCase {
	viewCountAsc := []op.OrderTerm{{Field: "view_count", Desc: false}}
	viewCountDesc := []op.OrderTerm{{Field: "view_count", Desc: true}}

	// sixPosts builds an author + N posts with ascending view_count.
	sixPosts := func(n int) op.Program {
		prog := op.Program{op.CreateAuthor{Name: "A", Role: "user"}}
		for i := 1; i <= n; i++ {
			prog = append(prog, op.CreatePost{Title: "P", Status: "draft", ViewCount: i, AuthorRef: 0})
		}
		return prog
	}

	return []progCase{
		{
			name: "crud_round_trip",
			prog: op.Program{
				op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},
				op.CreatePost{Title: "T", Status: "draft", ViewCount: 5, AuthorRef: 0},
				op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},
				op.QueryPostsByStatus{Status: "draft"},
			},
			expect: expectAllPass,
		},
		{
			// M2O author pre-population: after creating a post, a read that
			// loads the author edge must resolve the owner Ref.
			name: "m2o_author_prepopulation",
			prog: op.Program{
				op.CreateAuthor{Name: "Owner", Role: "user"},
				op.CreatePost{Title: "Owned", Status: "published", ViewCount: 1, AuthorRef: 0},
				op.QueryPostsByStatus{Status: "published"},
			},
			expect: expectAllPass,
		},
		{
			// O2M load: posts owned by a specific author.
			name: "o2m_load_author_posts",
			prog: op.Program{
				op.CreateAuthor{Name: "A0", Role: "user"},
				op.CreateAuthor{Name: "A1", Role: "user"},
				op.CreatePost{Title: "P0", Status: "draft", ViewCount: 1, AuthorRef: 0},
				op.CreatePost{Title: "P1", Status: "draft", ViewCount: 2, AuthorRef: 1},
				op.CreatePost{Title: "P2", Status: "draft", ViewCount: 3, AuthorRef: 0},
				op.LoadAuthorPosts{AuthorRef: 0},
			},
			expect: expectAllPass,
		},
		{
			// JSON replace: SetPostLabels overwrites the array. Both ORMs agree.
			name: "json_set",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "P", Status: "draft", ViewCount: 1, AuthorRef: 0, Labels: []string{"a"}},
				op.SetPostLabels{PostRef: 1, Labels: []string{"x", "y", "z"}},
				op.QueryPostsByStatus{Status: "draft"},
			},
			expect: expectAllPass,
		},
		{
			// JSON append: AppendPostLabels. velox is correct; ent's append SQL
			// — JSON_INSERT(labels, '$[#]', ?) — is rejected by SQLite as
			// malformed JSON on the blob-stored JSON value, whereas velox's
			// CAST-to-TEXT + json_each succeeds. (The labels column is declared
			// json in both; it is blob-stored because of the []byte bind param,
			// not a column-type difference.) This is a documented EntDivergent
			// surfaced by the harness — the same class as the Postgres JSON-append
			// bug A3b exercises against Postgres.
			name: "json_append",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "P", Status: "draft", ViewCount: 1, AuthorRef: 0, Labels: []string{"go"}},
				op.AppendPostLabels{PostRef: 1, Labels: []string{"orm"}},
				op.AppendPostLabels{PostRef: 1, Labels: []string{"sql", "db"}},
				op.QueryPostsByStatus{Status: "draft"},
			},
			expect: expectVeloxCorrect,
		},
		{
			name: "predicate_query_by_status",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "D0", Status: "draft", ViewCount: 1, AuthorRef: 0},
				op.CreatePost{Title: "P0", Status: "published", ViewCount: 2, AuthorRef: 0},
				op.CreatePost{Title: "D1", Status: "draft", ViewCount: 3, AuthorRef: 0},
				op.QueryPostsByStatus{Status: "draft"},
				op.QueryPostsByStatus{Status: "published"},
			},
			expect: expectAllPass,
		},
		{
			name:   "aggregate_sum_empty",
			prog:   op.Program{op.SumViewCount{}, op.CountPosts{}},
			expect: expectAllPass,
		},
		{
			name: "aggregate_sum_nonempty",
			prog: append(sixPosts(4),
				op.SumViewCount{}, // 1+2+3+4 = 10
				op.CountPosts{},
			),
			expect: expectAllPass,
		},
		{
			name: "delete_then_aggregate",
			prog: append(sixPosts(3),
				op.DeletePost{PostRef: 2}, // remove view_count 2
				op.SumViewCount{},         // 1+3 = 4
				op.CountPosts{},           // 2
				op.QueryPostsByStatus{Status: "draft"},
			),
			expect: expectAllPass,
		},
		{
			name: "not_found_update_deleted",
			prog: append(sixPosts(1),
				op.DeletePost{PostRef: 1},
				op.UpdatePostViewCount{PostRef: 1, ViewCount: 99}, // not_found
				op.DeletePost{PostRef: 1},                         // not_found
			),
			expect: expectAllPass,
		},
		{
			name: "paginate_forward_single_order",
			prog: append(sixPosts(6),
				op.PaginatePosts{First: intp(3), OrderBy: viewCountAsc},
			),
			expect: expectAllPass,
		},
		{
			name: "paginate_after_cursor",
			prog: append(sixPosts(6),
				op.PaginatePosts{First: intp(2), AfterRef: intp(2), OrderBy: viewCountAsc},
			),
			expect: expectAllPass,
		},
		{
			name: "paginate_backward_before_cursor",
			prog: append(sixPosts(6),
				op.PaginatePosts{Last: intp(2), BeforeRef: intp(5), OrderBy: viewCountAsc},
			),
			expect: expectAllPass,
		},
		{
			// The multi-order backward case: descending order + before-cursor +
			// last. This is the cursor bug class — the comparator pins
			// HasNext/HasPrev, start/end handles, and row order against the
			// reference.
			name: "paginate_multi_order_backward",
			prog: append(sixPosts(6),
				op.PaginatePosts{First: intp(4), OrderBy: viewCountDesc},
				op.PaginatePosts{Last: intp(2), BeforeRef: intp(3), OrderBy: viewCountDesc},
			),
			expect: expectAllPass,
		},
		{
			name: "paginate_validation_first_and_last",
			prog: append(sixPosts(3),
				op.PaginatePosts{First: intp(1), Last: intp(1), OrderBy: viewCountAsc}, // validation
			),
			expect: expectAllPass,
		},
	}
}
