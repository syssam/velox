package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestGeneratedEntities runs the library on the test model's entities, on
// the Descriptor path in the shape velox-gen will generate.
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
		velox.CheckRelation(testmodel.Samples.Children),
		velox.CheckRelation(testmodel.Samples.Parent),
		velox.CheckRelation(testmodel.Tickets.Invoice),
		velox.CheckRelation(testmodel.Tickets.Children),
		velox.CheckRelation(testmodel.Tickets.Parent),
	} {
		if err != nil {
			t.Error(err)
		}
	}
	S := testmodel.Samples
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	text, args := sqlOK(t)(db.Query[testmodel.Sample]().
		Where(S.Status.HasPrefix("a"), S.Ratio.IsNull(), S.Price.GT(100), S.MaybeWhen.NotNull()).
		AllowUnbounded().
		SQL(ctx))
	want := `SELECT "id", "name", "status", "prev", "price", "ratio", "flag", "maybe_flag", "when", "maybe_when", "blob", "parent_id", "score" ` +
		`FROM "samples" WHERE "status" LIKE $1 ESCAPE '!' AND "ratio" IS NULL AND "price" > $2 AND "maybe_when" IS NOT NULL`
	if text != want || !reflect.DeepEqual(args, []any{"a%", testmodel.Cents(100)}) {
		t.Errorf("\n got %s %#v\nwant %s", text, args, want)
	}
}

// A bytes field prints as its length, so that logging an entity with a blob
// does not write the blob.
func TestGeneratedStringPrintsBlobLength(t *testing.T) {
	s := testmodel.Invoice{ID: 7, Data: make([]byte, 1<<20)}.String()
	if !strings.Contains(s, "Data=<1048576 bytes>") || len(s) > 300 {
		t.Errorf("String = %.200q (%d bytes)", s, len(s))
	}
}
