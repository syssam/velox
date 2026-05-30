package parity_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// TestCuratedSuite runs a table of curated parity programs through the three-way
// driver (reference ⟷ velox ⟷ ent) across every configured backend
// (SQLite always; Postgres / MySQL when their VELOX_TEST_* DSN is set). Each
// (backend, program) is a sub-test; backends without a DSN are skipped so a
// no-DB machine stays green. Each case declares its expectation:
//
//   - expectAllPass: all three executors must agree on every op. This is the
//     default and the strongest assertion.
//   - expectVeloxCorrect: velox must match the reference on every op (zero
//     VeloxBugs, no ReferenceSuspect), but ent is allowed to diverge. Used ONLY
//     for the JSON-array-append case. The `labels` column is declared json in
//     both schemas; on SQLite ent emits JSON_INSERT(labels, '$[#]', ?) which
//     SQLite rejects ("malformed JSON") on the blob-stored JSON value, while
//     velox emits CAST(labels AS TEXT) + json_each, which succeeds — a real
//     EntDivergent the harness SURFACES rather than silences. On Postgres /
//     MySQL ent's append (jsonb `||` / JSON_ARRAY_APPEND) typically succeeds, so
//     the same case becomes all-Pass. The assertion therefore pins only that
//     velox is correct and tolerates EITHER all-Pass OR EntDivergent — itself a
//     documented finding: Ent's JSON-append defect is SQLite-specific.
func TestCuratedSuite(t *testing.T) {
	for _, backend := range []runner.Backend{runner.SQLite, runner.Postgres, runner.MySQL} {
		if !runner.HasBackend(backend) {
			t.Run(backend.String(), func(t *testing.T) { t.Skipf("%s not configured", backend) })
			continue
		}
		t.Run(backend.String(), func(t *testing.T) {
			for _, tc := range curatedPrograms() {
				t.Run(tc.name, func(t *testing.T) {
					rep := runner.RunParity(t, backend, tc.prog)
					tc.assert(t, rep)
				})
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

// assert applies the case's per-backend expectation to one report. The
// expectVeloxCorrect tolerance is backend-independent: velox must never be the
// outlier; ent may diverge OR agree (the SQLite-specific JSON-append defect
// shows up only on SQLite, so the same case is all-Pass on PG/MySQL).
func (tc progCase) assert(t *testing.T, rep runner.Report) {
	t.Helper()
	switch tc.expect {
	case expectAllPass:
		require.True(t, rep.AllPass(), "%s: expected all-pass, got:\n%s", tc.name, rep)
	case expectVeloxCorrect:
		require.Zero(t, rep.CountVeloxBugs(), "%s: velox diverged from reference (VeloxBug):\n%s", tc.name, rep)
		require.Zero(t, rep.CountReferenceSuspect(), "%s: reference suspect (velox AND ent disagree):\n%s", tc.name, rep)
		// EntDivergent is tolerated but NOT required — Ent's JSON-append defect
		// is SQLite-specific, so this case is all-Pass on Postgres / MySQL.
	}
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
			// Migration FK-action guard — the behavioral migration-DDL diff.
			// Comment.post carries ON DELETE CASCADE in both the velox and ent
			// parity schemas. Deleting a post that HAS a comment must succeed by
			// cascading the delete on all three executors. If velox's generated
			// migration emitted the wrong ON DELETE action (e.g. NoAction — the
			// schema.CASCADE bug class reported downstream), velox's DeletePost
			// would FK-fail while ent and the reference model succeed, surfacing
			// as a VeloxBug verdict. This catches migration-DDL divergence through
			// observable behavior — no schema introspection required.
			name: "migration_fk_cascade_on_delete",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},                 // 0
				op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0}, // 1
				op.CreateComment{Content: "c", PostRef: 1, AuthorRef: 0}, // 2
				op.DeletePost{PostRef: 1},                                // 3 — cascade, must not FK-fail
				op.CountPosts{},                                          // 4 — 0 after delete
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
			// Regression for a velox JSON-append bug the generative leg (Stage B)
			// surfaced: appending an EMPTY label list used to marshal the nil
			// slice to JSON "null" and bind it, so json_each('null') injected a
			// spurious empty element. The fix skips the modifier when the value
			// marshals to "null". Appending [] here must be a no-op, leaving the
			// labels unchanged and the later non-empty append clean.
			name: "json_append_empty_is_noop",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "P", Status: "draft", ViewCount: 1, AuthorRef: 0, Labels: []string{"e"}},
				op.AppendPostLabels{PostRef: 1, Labels: nil},                     // no-op
				op.AppendPostLabels{PostRef: 1, Labels: []string{"c", "b", "e"}}, // -> [e c b e]
				op.QueryPostsByStatus{Status: "draft"},
			},
			expect: expectVeloxCorrect,
		},
		{
			// Regression for the partner velox bug: SetLabels(nil) stores the JSON
			// scalar null in the column, and the SQLite append expression's
			// COALESCE did NOT normalize a json-null column (only SQL NULL), so a
			// later append injected a leading empty element. The fix maps a
			// json_type of 'null' to '[]' before json_each. SET-empty then append
			// must yield exactly the appended values.
			name: "json_set_empty_then_append",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "P", Status: "draft", ViewCount: 1, AuthorRef: 0, Labels: []string{"x"}},
				op.SetPostLabels{PostRef: 1, Labels: nil},                        // column -> json null
				op.AppendPostLabels{PostRef: 1, Labels: []string{"a", "a", "a"}}, // -> [a a a]
				op.QueryPostsByStatus{Status: "draft"},
			},
			expect: expectVeloxCorrect,
		},
		{
			// Comment + tag + M2M attach: exercises CreateComment, CreateTag, and
			// AddTagToPost (the edge-write ops not covered by the CRUD/pagination
			// cases), then reads the post back so the three executors agree on the
			// post's surviving state after the edge writes. Pinned for coverage by
			// coverage_test.go::TestCoverage_AllOpKindsExercised.
			name: "comment_tag_attach",
			prog: op.Program{
				op.CreateAuthor{Name: "A", Role: "user"},
				op.CreatePost{Title: "P", Status: "published", ViewCount: 7, AuthorRef: 0},
				op.CreateTag{Name: "go"},
				op.AddTagToPost{PostRef: 1, TagRef: 2},
				op.CreateComment{Content: "nice", PostRef: 1, AuthorRef: 0},
				op.QueryPostsByStatus{Status: "published"},
				op.CountPosts{},
			},
			expect: expectAllPass,
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
