package migrate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// fkSchema is a schema of dialect d with a table p and a table c whose column
// pid references p, with the indexes ix on c.
func fkSchema(d velox.Dialect, ix ...migrate.Index) *migrate.Schema {
	parent := migrate.Table{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}}
	child := migrate.Table{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "pid", Type: "bigint"}, {Name: "n", Type: "bigint"}},
		PrimaryKey: []string{"id"}, Indexes: ix,
		ForeignKeys: []migrate.ForeignKey{{Name: "c_p", Columns: []string{"pid"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{"id"}}}}
	if d == velox.MySQL {
		parent.PrimaryKeyName, child.PrimaryKeyName = "PRIMARY", "PRIMARY"
	}
	return &migrate.Schema{Dialect: d, Tables: []migrate.Table{child, parent}}
}

// unindexedTargets is the targets of the plan's unindexed-fk findings.
func unindexedTargets(t *testing.T, from, to *migrate.Schema, allow ...migrate.Allow) (targets []string, plan *migrate.Plan) {
	t.Helper()
	plan, err := migrate.NewPlan(from, to, migrate.PlanOptions{Allow: allow})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Findings {
		if f.Code == "unindexed-fk" {
			if f.Class != migrate.Review {
				t.Errorf("%s is %s, want review", f.Target, f.Class)
			}
			targets = append(targets, f.Target)
		}
	}
	return targets, plan
}

// TestUnindexedForeignKey: a foreign key that no full index or primary key
// leads with is a review finding when the plan creates it or drops its last
// leading index; an index that leads with it, a primary key, or an Allow
// leaves it out; MySQL, which indexes every key itself, has none.
func TestUnindexedForeignKey(t *testing.T) {
	t.Parallel()
	leading := migrate.Index{Name: "c_pid", Columns: []string{"pid"}}
	wide := migrate.Index{Name: "c_pid_n", Columns: []string{"pid", "n"}}
	trailing := migrate.Index{Name: "c_n_pid", Columns: []string{"n", "pid"}}
	partial := migrate.Index{Name: "c_pid_some", Columns: []string{"pid"}, Where: "n > 0"}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		empty := &migrate.Schema{Dialect: d}
		for _, c := range []struct {
			name     string
			from, to *migrate.Schema
			want     []string
		}{
			{"new table", empty, fkSchema(d), []string{"c.pid"}},
			{"leading index", empty, fkSchema(d, leading), nil},
			{"wider index", empty, fkSchema(d, wide), nil},
			{"second column", empty, fkSchema(d, trailing), []string{"c.pid"}},
			{"partial index", empty, fkSchema(d, partial), []string{"c.pid"}},
			{"index dropped", fkSchema(d, leading), fkSchema(d), []string{"c.pid"}},
			{"one of two dropped", fkSchema(d, leading, wide), fkSchema(d, wide), nil},
			{"unchanged", fkSchema(d), fkSchema(d), nil},
		} {
			got, _ := unindexedTargets(t, c.from, c.to)
			if !slices.Equal(got, c.want) {
				t.Errorf("%s %s: %v, want %v", d, c.name, got, c.want)
			}
		}

		// A key added to an existing table.
		from := fkSchema(d)
		from.Tables[0].ForeignKeys = nil
		if got, _ := unindexedTargets(t, from, fkSchema(d)); !slices.Equal(got, []string{"c.pid"}) {
			t.Errorf("%s key added: %v", d, got)
		}
		// A key whose action changes is not new.
		to := fkSchema(d)
		to.Tables[0].ForeignKeys[0].OnDelete = "CASCADE"
		if got, _ := unindexedTargets(t, fkSchema(d), to); got != nil {
			t.Errorf("%s action changed: %v", d, got)
		}

		// An Allow takes the finding out of the plan and its review lines.
		for _, allow := range []migrate.Allow{{Code: "unindexed-fk", Target: "c.pid"}, {Code: "unindexed-fk"}} {
			got, plan := unindexedTargets(t, empty, fkSchema(d), allow)
			if got != nil {
				t.Errorf("%s allowed %v: %v", d, allow, got)
			}
			for _, sc := range plan.Scripts {
				for _, st := range sc.Statements {
					if strings.Contains(st.Review, "unindexed-fk") {
						t.Errorf("%s allowed %v: review line %q", d, allow, st.Review)
					}
				}
			}
		}
		if got, _ := unindexedTargets(t, empty, fkSchema(d), migrate.Allow{Code: "unindexed-fk", Target: "c.other"}); got == nil {
			t.Errorf("%s: an Allow of another column took the finding", d)
		}
	}

	// MySQL makes an index for every key, and velox-gen's tables hold it.
	if got, _ := unindexedTargets(t, &migrate.Schema{Dialect: velox.MySQL}, fkSchema(velox.MySQL)); got != nil {
		t.Errorf("mysql: %v", got)
	}
}

// TestUnindexedForeignKeyLint: Lint, which has no target schema, finds the
// keys of a table the plan creates.
func TestUnindexedForeignKeyLint(t *testing.T) {
	t.Parallel()
	empty := &migrate.Schema{Dialect: velox.Postgres}
	changes, err := migrate.Diff(empty, fkSchema(velox.Postgres), migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range migrate.Lint(empty, changes) {
		got = append(got, f.Code+"@"+f.Target)
	}
	if !slices.Contains(got, "unindexed-fk@c.pid") {
		t.Errorf("Lint: %v", got)
	}
}

// TestParseAllowUnindexedFK: -allow takes unindexed-fk, with a target or
// without; another review code is refused.
func TestParseAllowUnindexedFK(t *testing.T) {
	t.Parallel()
	got, err := migrate.ParseAllow("unindexed-fk=posts.author_id,unindexed-fk")
	if err != nil || !slices.Equal(got, []migrate.Allow{{Code: "unindexed-fk", Target: "posts.author_id"}, {Code: "unindexed-fk"}}) {
		t.Errorf("ParseAllow: %v, %v", got, err)
	}
	if _, err := migrate.ParseAllow("add-index"); err == nil || !strings.Contains(err.Error(), "unindexed-fk") {
		t.Errorf("ParseAllow(add-index): %v", err)
	}
}
