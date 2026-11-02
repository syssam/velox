package veloxotel_test

import (
	"database/sql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/veloxotel"
)

// Options returns the velox options that trace a DB and record its metrics,
// through the global providers unless Config names others.
func ExampleOptions() {
	var pool *sql.DB // opened by the application
	db := velox.Open(velox.Postgres, pool, veloxotel.Options(veloxotel.Config{
		App:         "shop",
		RecordScope: true,
	})...)
	_ = db
}
