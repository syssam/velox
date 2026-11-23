package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// noneFor is a filter that hides every row of entity.
func noneFor(entity string) velox.Filter {
	return func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		if fe.Name() == entity {
			return velox.None(), nil
		}
		return velox.Allow(), nil
	}
}

// TestNoneRenders: None is 1 = 0 on every dialect.
func TestNoneRenders(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		e := &fakeconn.Exec{}
		db := openFake(d, e, velox.WithFilter(noneFor("DBook")))
		if _, err := db.Query[fixture.DBook]().Limit(5).All(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 1 || !strings.HasSuffix(e.Log[0], " WHERE 1 = 0 LIMIT 5") {
			t.Errorf("%v: %q", d, e.Log)
		}
	}
}

// TestNoneHidesEveryRow: under a filter that returns None, a read returns no
// row, and an update or a delete changes none.
func TestNoneHidesEveryRow(t *testing.T) {
	db := interceptorDB(t, velox.WithFilter(noneFor("IOwner")))
	ctx := context.Background()
	if n, err := db.Query[fixture.IOwner]().Count(ctx); err != nil || n != 0 {
		t.Errorf("Count: %d, %v", n, err)
	}
	if n, err := db.Update[fixture.IOwner]().Set(fixture.IOwners.Name.Set("x")).Where(fixture.IOwners.ID.EQ(1)).Exec(ctx); err != nil || n != 0 {
		t.Errorf("Update: %d, %v", n, err)
	}
	if n, err := db.Delete[fixture.IOwner]().Where(fixture.IOwners.ID.EQ(1)).Exec(ctx); err != nil || n != 0 {
		t.Errorf("Delete: %d, %v", n, err)
	}
}
