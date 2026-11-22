package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
)

// TestScanOnRealDatabases: Scan reads a grouped report into a struct on
// every dialect: a sum, a count into an int, the MIN of a time (which SQLite
// hands over as text) into a time.Time and the MAX of one into a pointer, nil
// for NULL; an enum, a nullable enum and a time column. The invoices are
// TestSelect's, the paid ones paid at t0.
func TestScanOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		type report struct {
			OrgID    int64
			Sum      float64 `velox:"total"`
			N        int
			First    time.Time  `velox:"first"`
			LastPaid *time.Time `velox:"last_paid"`
		}
		q := func() velox.Query[inv] {
			return tg.db.Query[inv]().
				Select(I.OrgID, I.Total.Sum().As("total"), velox.CountAll[inv]().As("n"), I.PaidAt.Min().As("first"), I.PaidAt.Max().As("last_paid")).
				GroupBy(I.OrgID).OrderBy(I.OrgID.Asc()).Limit(10)
		}
		// The setup writes its times past velox, in the driver's own text on
		// SQLite; velox writes them in the form it reads back from an
		// aggregate.
		if _, err := tg.db.Update[inv]().Set(I.PaidAt.Set(t0)).Where(I.Paid.EQ(true)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := velox.Scan[report](ctx, q())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].OrgID != 1 || got[0].Sum != 100 || got[0].N != 4 || !got[0].First.Equal(t0) ||
			got[0].LastPaid == nil || !got[0].LastPaid.Equal(t0) || got[1].OrgID != 2 || got[1].Sum != 50 || got[1].N != 1 {
			t.Errorf("report: %+v", got)
		}
		type last struct {
			OrgID    int64
			LastPaid *time.Time `velox:"last_paid"`
		}
		unpaid, err := velox.Scan[last](ctx, tg.db.Query[inv]().Where(I.Paid.EQ(false)).
			Select(I.OrgID, I.PaidAt.Max().As("last_paid")).GroupBy(I.OrgID).Limit(10))
		if err != nil || len(unpaid) != 1 || unpaid[0].LastPaid != nil {
			t.Errorf("unpaid: %+v %v", unpaid, err)
		}
		total, err := velox.ScanOne[float64](ctx, tg.db.Query[inv]().Select(I.Total.Sum()))
		if err != nil || total != 150 {
			t.Errorf("ScanOne: %v %v", total, err)
		}
		if _, err := velox.ScanOne[float64](ctx, tg.db.Query[inv]().Select(I.Total)); !errors.Is(err, velox.ErrNotSingular) {
			t.Errorf("ScanOne of five rows: %v", err)
		}

		// An enum, a nullable enum and a time column.
		tg.exec(t, "DROP TABLE IF EXISTS samples")
		tg.exec(t, sampleDDL(tg.d))
		t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS samples") })
		seedSample(t, tg, 5, nil)
		S := testmodel.Samples
		type sample struct {
			Status kind.Status
			Prev   *kind.Status
			When   time.Time
		}
		samples, err := velox.Scan[sample](ctx, tg.db.Query[testmodel.Sample]().Select(S.Status, S.Prev, S.When).Limit(5))
		if err != nil || len(samples) != 1 || samples[0].Status != "new" || samples[0].Prev != nil || !samples[0].When.Equal(t0) {
			t.Errorf("samples: %+v %v", samples, err)
		}
	})
}

// TestScanJSONOnRealDatabases: a JSON column is decoded into its field, a
// NULL one into a nil pointer, and a JSON path's Term read as its kind.
func TestScanJSONOnRealDatabases(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonParitySetup(t, tg)
		type doc struct {
			ID   int64
			Tags []string
			Nums *[]int
			Name *string `velox:"name"`
			Len  *int64  `velox:"len"`
			N    float64 `velox:"n"`
		}
		got, err := velox.Scan[doc](ctx, tg.db.Query[jrec]().
			Select(JR.ID, JR.Tags, JR.Nums, JR.Attrs.Text("name").Term().As("name"), JR.Attrs.Len("list").Term().As("len"), JR.Attrs.Number("n").Term().As("n")).
			OrderBy(JR.ID.Asc()).Limit(5))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("got %+v", got)
		}
		a, b, c := got[0], got[1], got[2]
		if len(a.Tags) != 2 || a.Nums == nil || len(*a.Nums) != 2 || a.Name == nil || *a.Name != "alpha*Beta" || a.Len == nil || *a.Len != 3 || a.N != 5 {
			t.Errorf("first: %+v", a)
		}
		if b.Tags == nil || len(b.Tags) != 0 || b.Nums != nil || b.Len == nil || *b.Len != 0 {
			t.Errorf("second: %+v", b)
		}
		if c.Len != nil || c.N != 10.5 || c.Nums == nil || len(*c.Nums) != 0 {
			t.Errorf("third: %+v", c)
		}
		sum, err := velox.ScanOne[float64](ctx, tg.db.Query[jrec]().Select(JR.Attrs.Number("n").Term().Sum()))
		if err != nil || sum != 17.5 {
			t.Errorf("sum of a path: %v %v", sum, err)
		}
	})
}
