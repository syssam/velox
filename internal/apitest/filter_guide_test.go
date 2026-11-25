package apitest

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

// TestGuideFilterExample compiles and runs the filter docs/guide.md shows
// for a filter that reads: the guide's Order, Member and Team are INote,
// IOwner and the owner's id; the body is the guide's. Keep the two alike.
func TestGuideFilterExample(t *testing.T) {
	userName := func(ctx context.Context) string {
		name, _ := ctx.Value(filterOwnerKey{}).(string)
		return name
	}
	db := interceptorDB(t, velox.WithFilter(func(ctx context.Context, db *velox.DB, fe velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
		if fe.Name() != "INote" || op != velox.OpSelect {
			return velox.Allow(), nil
		}
		owners, err := db.Query[fixture.IOwner]().
			Where(fixture.IOwners.Name.EQ(userName(ctx))).Limit(100).All(ctx)
		if err != nil {
			return velox.Predicate{}, err
		}
		ids := make([]any, len(owners))
		for i, o := range owners {
			ids[i] = o.ID
		}
		return velox.FieldIn("owner_id", ids...), nil
	}))
	ctx := context.WithValue(context.Background(), filterOwnerKey{}, "o1")
	count := func(ctx context.Context, db *velox.DB) int {
		t.Helper()
		n, err := db.Query[fixture.INote]().Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	outside := count(ctx, db)
	var inside int
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		inside = count(ctx, tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if outside != 1 || inside != 1 {
		t.Errorf("owner o1 sees %d notes outside a Tx and %d inside, want 1 and 1", outside, inside)
	}
}
