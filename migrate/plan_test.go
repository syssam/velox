package migrate_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema/field"
)

func TestNewPlan(t *testing.T) {
	t.Parallel()
	for _, d := range rendered {
		from := mustTables(d, func(m *loadjson.Model) { addField(m, "User", field.String("legacy").Optional().Default("")) })
		to := mustTables(d, nil)
		p, err := migrate.NewPlan(from, to, migrate.PlanOptions{LockTimeout: 5 * time.Second})
		if b, ok := errors.AsType[*migrate.BlockedError](err); p != nil || !ok || len(b.Findings) != 1 {
			t.Fatalf("%v: %v, %v; want a *BlockedError with one finding", d, p, err)
		}
		want := "migrate: 1 destructive change refused:\n" +
			"  users.legacy: drop-column: drops column users.legacy and its data; allow with -allow drop-column=users.legacy"
		if err.Error() != want {
			t.Errorf("%v: %q\nwant %q", d, err.Error(), want)
		}

		opts := migrate.PlanOptions{LockTimeout: 5 * time.Second, Allow: []migrate.Allow{{Code: "drop-column", Target: "users.legacy"}}}
		p, err = migrate.NewPlan(from, to, opts)
		if err != nil {
			t.Fatalf("%v: %v", d, err)
		}
		cs := diff(t, from, to, migrate.DiffOptions{})
		if !reflect.DeepEqual(p.Changes, cs) {
			t.Errorf("%v: changes %v, want Diff's %v", d, lines(p.Changes), lines(cs))
		}
		if !reflect.DeepEqual(p.Findings, migrate.Lint(from, cs)) {
			t.Errorf("%v: findings %v, want Lint's", d, p.Findings)
		}
		if want := render(t, from, to, cs, migrate.RenderOptions{LockTimeout: opts.LockTimeout, Allow: opts.Allow}); !reflect.DeepEqual(p.Scripts, want) {
			t.Errorf("%v: scripts\n%s\nwant Render's\n%s", d, scriptsText(p.Scripts), scriptsText(want))
		}
	}

	// Diff's errors come through.
	_, err := migrate.NewPlan(nil, mustTables(velox.MySQL, nil), migrate.PlanOptions{})
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "nil" {
		t.Errorf("a nil from: %v", err)
	}
}

func TestBlockedError(t *testing.T) {
	t.Parallel()
	e := &migrate.BlockedError{Findings: []migrate.Finding{
		{Code: "drop-table", Target: "posts", Message: "drops table posts and its rows"},
		{Code: "set-not-null", Target: "users.bio", Pos: "user.go:12", Message: "makes column users.bio NOT NULL"},
	}}
	want := "migrate: 2 destructive changes refused:\n" +
		"  posts: drop-table: drops table posts and its rows; allow with -allow drop-table=posts\n" +
		"  user.go:12: users.bio: set-not-null: makes column users.bio NOT NULL; allow with -allow set-not-null=users.bio"
	if e.Error() != want {
		t.Errorf("%q\nwant %q", e.Error(), want)
	}
}

func TestNewPlanDoesNotMutate(t *testing.T) {
	t.Parallel()
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		f, w := migrate.Clone(from), migrate.Clone(to)
		opts := migrate.PlanOptions{Diff: c.opts, Allow: c.allow, LockTimeout: time.Second}
		if _, err := migrate.NewPlan(from, to, opts); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(from, f) || !reflect.DeepEqual(to, w) {
			t.Error("NewPlan changed its arguments")
		}
	})
}

func TestNewPlanDeterministic(t *testing.T) {
	t.Parallel()
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		opts := migrate.PlanOptions{Diff: c.opts, Allow: c.allow, LockTimeout: time.Second}
		a, err := migrate.NewPlan(from, to, opts)
		if err != nil {
			t.Fatal(err)
		}
		b, err := migrate.NewPlan(from, to, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) || scriptsText(a.Scripts) != scriptsText(b.Scripts) {
			t.Errorf("two calls differ:\n%s\n--\n%s", scriptsText(a.Scripts), scriptsText(b.Scripts))
		}
	})
}
