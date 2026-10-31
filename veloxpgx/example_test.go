package veloxpgx_test

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox/veloxpgx"
)

// Open needs a reachable database, so this example is compiled and not run.
func ExampleOpen() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://localhost/app")
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	db := veloxpgx.Open(pool)
	_ = db // db.Query[gen.User]().All(ctx), and the rest of velox's API
}
