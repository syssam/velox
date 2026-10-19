package velox_test

import (
	"reflect"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestGeneratedEntities runs the library on the test model's entities, on
// the Desc path in the shape velox-gen will generate.
func TestGeneratedEntities(t *testing.T) {
	for _, err := range []error{
		velox.CheckEntity[testmodel.Invoice](),
		velox.CheckEntity[testmodel.Sample](),
		velox.CheckEntity[testmodel.Line](),
		velox.CheckEntity[testmodel.Item](),
		velox.CheckEntity[testmodel.Ticket](),
		// A relation of generated code names its target where it is used, so
		// CheckEntity leaves it: CheckRelation resolves each.
		velox.CheckRelation(testmodel.Invoices.Items),
		velox.CheckRelation(testmodel.Items.Invoice),
		velox.CheckRelation(testmodel.Lines.Sample),
		velox.CheckRelation(testmodel.Samples.Lines),
		velox.CheckRelation(testmodel.Samples.Parent),
		velox.CheckRelation(testmodel.Tickets.Invoice),
		velox.CheckRelation(testmodel.Tickets.Parent),
	} {
		if err != nil {
			t.Error(err)
		}
	}
	S := testmodel.Samples
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	text, args := sqlOK(t)(db.Query[testmodel.Sample]().
		Where(S.Status.HasPrefix("a"), S.Ratio.IsNull(), S.Price.GT(100), S.MaybeWhen.NotNull()).
		Unbounded().
		SQL(ctx))
	want := `SELECT "id", "name", "status", "prev", "price", "ratio", "flag", "maybe_flag", "when", "maybe_when", "blob", "parent_id", "score" ` +
		`FROM "samples" WHERE "status" LIKE $1 ESCAPE '\' AND "ratio" IS NULL AND "price" > $2 AND "maybe_when" IS NOT NULL`
	if text != want || !reflect.DeepEqual(args, []any{"a%", testmodel.Cents(100)}) {
		t.Errorf("\n got %s %#v\nwant %s", text, args, want)
	}
}
