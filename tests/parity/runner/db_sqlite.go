package runner

import (
	"time"

	// modernc.org/sqlite registers the pure-Go "sqlite" database/sql driver
	// used by both the velox and ent executors. It lives here (an ORM-free
	// file) so the driver registration happens exactly once for the package and
	// the executor files (run_velox.go / run_ent.go) only deal with the ORM
	// clients, not driver wiring.
	_ "modernc.org/sqlite"
)

// parityEpoch is the deterministic base time injected into every Create/Update.
// Each op at program index i gets parityEpoch + i seconds for its created_at /
// updated_at, so created_at order == insertion order == the reference's handle
// order, making ORDER BY created_at deterministic and identical across all
// three executors. The VALUES are excluded from row comparison by the driver;
// only their ordering effect matters. It is ORM-free, so it lives here.
var parityEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// sqliteDriverName is the database/sql driver registered by modernc.org/sqlite.
// It is "sqlite" (NOT "sqlite3" — that is the CGO mattn driver velox does not
// use). Both executors open through this name.
const sqliteDriverName = "sqlite"

// sqliteMemoryDSN returns an in-memory SQLite DSN with foreign keys enabled.
// Each call to NewVeloxSQLite / NewEntSQLite opens its own connection to a
// private ":memory:" database, so programs never bleed into one another
// (per-program isolation). The _pragma form is the modernc.org/sqlite syntax
// the rest of the repo uses.
func sqliteMemoryDSN() string {
	return ":memory:?_pragma=foreign_keys(1)"
}
