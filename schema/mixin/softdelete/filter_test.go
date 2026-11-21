package softdelete_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// TestFilterSeesSoftDeleteAsDelete: a filter that restricts only deletes
// applies to a soft delete, which reaches it as OpDelete, not OpUpdate.
func TestFilterSeesSoftDeleteAsDelete(t *testing.T) {
	var ops []velox.Op
	db, raw, _ := open(t, velox.WithFilter(func(_ context.Context, entity string, op velox.Op) (velox.Predicate, error) {
		if entity != "SDoc" {
			return velox.NoFilter(), nil
		}
		ops = append(ops, op)
		if op != velox.OpDelete {
			return velox.NoFilter(), nil
		}
		return velox.FieldEQ("owner_id", 2), nil // deletes reach owner 2's docs only
	}))
	ctx := org(1)
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil || n != 0 {
		t.Errorf("soft delete of owner 1's doc: %d, %v; want 0", n, err)
	}
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(3)).Exec(ctx); err != nil || n != 1 {
		t.Errorf("soft delete of owner 2's doc: %d, %v; want 1", n, err)
	}
	wantIDs(t, "deleted", deleted(t, raw), 3)
	if !slices.Equal(ops, []velox.Op{velox.OpDelete, velox.OpDelete}) {
		t.Errorf("filter ops %v, want [delete delete]", ops)
	}
	// A plain update still reaches the filter as OpUpdate.
	ops = nil
	if _, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.Title.Set("z")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ops, []velox.Op{velox.OpUpdate}) {
		t.Errorf("update's filter ops %v, want [update]", ops)
	}
}

// TestRestoreUnderReadsOnlyFilter: a filter that narrows reads only does
// not stop a soft delete, nor the restore, an update, that follows it.
func TestRestoreUnderReadsOnlyFilter(t *testing.T) {
	db, raw, _ := open(t, velox.WithFilter(func(_ context.Context, entity string, op velox.Op) (velox.Predicate, error) {
		if entity != "SDoc" || op != velox.OpSelect {
			return velox.NoFilter(), nil
		}
		return velox.FieldNEQ("title", "hidden"), nil
	}))
	ctx := org(1)
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil || n != 1 {
		t.Fatalf("soft delete: %d, %v; want 1", n, err)
	}
	if n, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx)); err != nil || n != 1 {
		t.Errorf("restore: %d, %v; want 1", n, err)
	}
	wantIDs(t, "deleted", deleted(t, raw))
}

// TestHooksAllSeeDeleteThenUpdate pins what a WithHooksAll hook sees of a
// soft delete: the delete, then the mixin's update inside it, which assigns
// deleted_at a time. A restore assigns it nil, and a plain update not at
// all.
func TestHooksAllSeeDeleteThenUpdate(t *testing.T) {
	var seen []string
	all := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			v, ok := m.Value(softdelete.Column)
			seen = append(seen, fmt.Sprintf("%v %s soft=%t", m.Op(), m.Entity(), ok && v != nil))
			return next.Mutate(ctx, m)
		})
	}
	db, _, _ := open(t, velox.WithHooksAll(all))
	ctx := org(1)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf("%v SDoc soft=false", velox.OpDelete),
		fmt.Sprintf("%v SDoc soft=true", velox.OpUpdate),
		fmt.Sprintf("%v SDoc soft=false", velox.OpUpdate),
	}
	if !slices.Equal(seen, want) {
		t.Errorf("hooks saw\n %q\nwant\n %q", seen, want)
	}
}
