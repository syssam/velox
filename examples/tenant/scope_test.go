package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tenant/gen"
	"github.com/syssam/velox/examples/tenant/schema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/scope"
	"github.com/syssam/velox/scope/scopetest"
)

// openScoped returns a database with the example's tables.
func openScoped(t *testing.T) *velox.DB {
	t.Helper()
	db, err := velox.Connect("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sub, err := fsSub()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(t.Context(), velox.Pool(db), sub); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestScopeKeyZeroIsNoValue(t *testing.T) {
	ctx := schema.OrgKey.With(t.Context(), 0)
	if v, ok := schema.OrgKey.From(ctx); ok {
		t.Errorf("the zero value is a scope value: %v", v)
	}
	if _, err := openScoped(t).Query[gen.Project]().Count(ctx); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("a zero scope value: err = %v, want ErrNoScope", err)
	}
}

// A scope on one key never serves another key of the same type.
func TestScopeKeysAreDistinct(t *testing.T) {
	other := scope.NewKey[int64]("other")
	ctx := other.With(t.Context(), 1)
	if _, err := openScoped(t).Query[gen.Project]().Count(ctx); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("another key's value served: err = %v", err)
	}
}

func TestScopeIsolatesEveryStatement(t *testing.T) {
	db := openScoped(t)
	a, b := schema.OrgKey.With(t.Context(), 1), schema.OrgKey.With(t.Context(), 2)
	p := gen.Project{Name: "x", Status: "open"}
	if err := db.Insert(&p).Exec(a); err != nil {
		t.Fatal(err)
	}
	P := gen.Projects
	if _, err := db.Query[gen.Project]().Where(P.ID.EQ(p.ID)).One(b); !velox.IsNotFound(err) {
		t.Errorf("read across scopes: err = %v", err)
	}
	if n, err := db.Update[gen.Project]().Set(P.Status.Set("done")).Where(P.ID.EQ(p.ID)).Exec(b); err != nil || n != 0 {
		t.Errorf("update across scopes: n=%d err=%v", n, err)
	}
	if n, err := db.Delete[gen.Project]().Where(P.ID.EQ(p.ID)).Exec(b); err != nil || n != 0 {
		t.Errorf("delete across scopes: n=%d err=%v", n, err)
	}
	if got, err := db.Query[gen.Project]().Where(P.ID.EQ(p.ID)).One(a); err != nil || got.Status != "open" {
		t.Errorf("the owner's row changed: %+v, %v", got, err)
	}
	if n, err := db.Delete[gen.Project]().Where(P.ID.EQ(p.ID)).Exec(a); err != nil || n != 1 {
		t.Errorf("the owner's delete: n=%d err=%v", n, err)
	}
}

// scopetest checks InOrg on Project as it would on any entity that lists
// it: the hook fills org_id, and the policy confines every statement.
func TestInOrgIsolatesProject(t *testing.T) {
	scopetest.Isolates(t, t.Context(), openScoped(t), schema.OrgKey, "org_id", 1, 2,
		func() *gen.Project { return &gen.Project{Name: "x", Status: "open"} })
}

func TestScopeWithoutPolicyReadsAll(t *testing.T) {
	db := openScoped(t)
	for org := int64(1); org <= 2; org++ {
		if err := db.Insert(&gen.Project{Name: "n", Status: "s"}).Exec(schema.OrgKey.With(t.Context(), org)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.WithoutPolicy("test").Query[gen.Project]().Count(context.Background())
	if err != nil || n != 2 {
		t.Errorf("WithoutPolicy count = %d, %v; want 2", n, err)
	}
}
