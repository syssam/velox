package velox_test

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/actor"
)

// filterEntity runs a query of E on db and returns the entity names its
// filter was given.
func filterEntity[E any, PE velox.Entity[E]](ctx context.Context, db *velox.DB, seen *[]string) []string {
	*seen = nil
	_, _ = db.Query[E, PE]().Limit(1).All(ctx) // no table: only the filter's call counts
	return slices.Compact(*seen)
}

// TestTypeConstantIsFiltersEntity: gen.TypeX is the Name of the entity a
// velox.Filter receives for a statement on X, a join entity's too.
func TestTypeConstantIsFiltersEntity(t *testing.T) {
	var seen []string
	db, err := velox.Connect("sqlite", "file::memory:", velox.WithFilter(
		func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
			seen = append(seen, fe.Name())
			return velox.Allow(), nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := actor.WithUser(testmodel.Org.With(context.Background(), 1), "u1")
	for _, c := range []struct {
		want string
		got  []string
	}{
		{testmodel.TypeEvent, filterEntity[testmodel.Event](ctx, db, &seen)},
		{testmodel.TypeInvoice, filterEntity[testmodel.Invoice](ctx, db, &seen)},
		{testmodel.TypeItem, filterEntity[testmodel.Item](ctx, db, &seen)},
		{testmodel.TypeLedger, filterEntity[testmodel.Ledger](ctx, db, &seen)},
		{testmodel.TypeLine, filterEntity[testmodel.Line](ctx, db, &seen)},
		{testmodel.TypePaper, filterEntity[testmodel.Paper](ctx, db, &seen)},
		{testmodel.TypePaperTopic, filterEntity[testmodel.PaperTopic](ctx, db, &seen)},
		{testmodel.TypePlayer, filterEntity[testmodel.Player](ctx, db, &seen)},
		{testmodel.TypeRemark, filterEntity[testmodel.Remark](ctx, db, &seen)},
		{testmodel.TypeRoster, filterEntity[testmodel.Roster](ctx, db, &seen)},
		{testmodel.TypeSample, filterEntity[testmodel.Sample](ctx, db, &seen)},
		{testmodel.TypeTeam, filterEntity[testmodel.Team](ctx, db, &seen)},
		{testmodel.TypeTicket, filterEntity[testmodel.Ticket](ctx, db, &seen)},
		{testmodel.TypeTopic, filterEntity[testmodel.Topic](ctx, db, &seen)},
	} {
		if len(c.got) != 1 || c.got[0] != c.want {
			t.Errorf("filter of a query of %s got entity %q", c.want, c.got)
		}
	}
}
