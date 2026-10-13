// Package nostitch is a program that uses generated entities without linking
// their schemainit package: it declares the clients of its entities itself and
// never imports the root package, which would link it.
package nostitch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/velox/entity"
	"github.com/syssam/velox/tests/orm/velox/migrate"
	"github.com/syssam/velox/tests/orm/velox/predicate"
	"github.com/syssam/velox/tests/orm/velox/tag"
	"github.com/syssam/velox/tests/orm/velox/user"

	_ "modernc.org/sqlite"
)

// The clients of the entities, declared here as the root package declares
// them: what a program has that leaves the root package out.
var (
	tags  = orm.ClientOf[*entity.Tag, predicate.Tag, int]{Meta: &entity.TagMeta}
	users = orm.ClientOf[*entity.User, predicate.User, int]{Meta: &entity.UserMeta}
)

// Without schemainit an entity has no policy, hooks, validators or defaults.
// Running it anyway would answer every caller, so every query and mutation of
// an entity whose schema has any of those is an error that names the import.
func TestEntityWithoutSchemainitRefusesToRun(t *testing.T) {
	ctx := context.Background()
	drv, err := velsql.Open(dialect.SQLite, "file:nostitch?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })
	db := velox.OpenDB(dialect.SQLite, drv.DB())
	require.NoError(t, migrate.Create(ctx, db))

	const want = `import _ "github.com/syssam/velox/tests/orm/velox/schemainit"`
	_, err = tags.Query(db).All(ctx)
	require.ErrorContains(t, err, want)
	_, err = tags.Query(db).Count(ctx)
	require.ErrorContains(t, err, want)
	_, err = tags.Create(db).Set(tag.Name.Set("")).Save(ctx)
	require.ErrorContains(t, err, want)
	_, err = users.Delete(db).Exec(ctx)
	require.ErrorContains(t, err, want)
	_, err = users.Update(db).Set(user.Age.Set(1)).Exec(ctx)
	require.ErrorContains(t, err, want)
	_, err = users.CreateBulk(db, users.Create(db).Set(user.Name.Set("a"), user.Email.Set("a@example.com"), user.Age.Set(1))).Save(ctx)
	require.ErrorContains(t, err, want)
}
