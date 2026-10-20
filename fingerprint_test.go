package velox_test

import (
	"context"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// lastStatement returns an Around hook that copies each statement it sees
// into *st.
func lastStatement(st *velox.Statement) velox.Option {
	return velox.WithAround(func(ctx context.Context, s *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		*st = *s
		return err
	})
}

// TestFingerprintGolden pins the fingerprint for one statement on
// each dialect: a change to rendering or to the hash shows here, and the
// release notes must say so.
func TestFingerprintGolden(t *testing.T) {
	for d, want := range map[velox.Dialect]struct{ sql, fp string }{
		velox.Postgres: {`SELECT "id" FROM "invoices" WHERE "number" = $1 LIMIT $2`, "da95560fb97b3ada"},
		velox.MySQL:    {"SELECT `id` FROM `invoices` WHERE `number` = ? LIMIT ?", "9ca18764b9e4f21a"},
		velox.SQLite:   {`SELECT "id" FROM "invoices" WHERE "number" = ? LIMIT ?`, "df6b3bf7e65701b1"},
	} {
		var st velox.Statement
		db := velox.OpenFake(d, &velox.FakeExec{}, lastStatement(&st))
		for range 2 { // rendered, then cached
			if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
				t.Fatal(err)
			}
			if st.SQL != want.sql || st.Fingerprint != want.fp {
				t.Errorf("%v: %s %s, want %s %s", d, st.SQL, st.Fingerprint, want.sql, want.fp)
			}
		}
	}
}

// TestFingerprintIsTheShapes is Review Focus 4: every value of the arguments,
// and a Rewrite hook's copy of the entity (rendered without the cache), give
// the shape's one fingerprint; another shape gets another.
func TestFingerprintIsTheShapes(t *testing.T) {
	var st velox.Statement
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, lastStatement(&st))
	fp := func(q velox.Query[testmodel.Invoice]) string {
		t.Helper()
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		return st.Fingerprint
	}
	x := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(1))
	if y := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("y")).Limit(7)); y != x {
		t.Errorf("other values: %s, want %s", y, x)
	}
	if other := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("x"), I.Paid.EQ(true)).Limit(1)); other == x {
		t.Error("two shapes share a fingerprint")
	}
	copied := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, lastStatement(&st),
		velox.WithRewrite(func(_ context.Context, s *velox.Statement) error {
			info := *s.Entity
			s.Entity = &info
			return nil
		}))
	if _, err := copied.Query[testmodel.Invoice]().Where(I.Number.EQ("z")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if st.Fingerprint != x {
		t.Errorf("uncached render: %s, want %s", st.Fingerprint, x)
	}
}
