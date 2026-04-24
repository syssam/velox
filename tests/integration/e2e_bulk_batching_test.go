package integration_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	sqldialect "github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/user"

	_ "modernc.org/sqlite"
)

// countingDriver wraps a dialect.Driver and records the SQL statements
// it sees. Used by TestCreateBulk_BatchingUnderHooks_IsOneStatement to
// verify that bulk creates emit ONE multi-row INSERT regardless of
// whether hooks are registered — the central claim of the mutator
// chain rewrite.
type countingDriver struct {
	dialect.Driver
	mu      sync.Mutex
	inserts []string
}

func (d *countingDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.recordIfInsert(query)
	return d.Driver.Exec(ctx, query, args, v)
}

func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.recordIfInsert(query)
	return d.Driver.Query(ctx, query, args, v)
}

func (d *countingDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &countingTx{Tx: tx, parent: d}, nil
}

func (d *countingDriver) recordIfInsert(query string) {
	// Only count INSERTs into the target table, not schema-create or
	// unrelated writes.
	if !strings.HasPrefix(strings.TrimSpace(query), "INSERT") {
		return
	}
	d.mu.Lock()
	d.inserts = append(d.inserts, query)
	d.mu.Unlock()
}

func (d *countingDriver) insertCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.inserts)
}

// countingTx makes sure statements run through a Tx() are also
// attributed to the counting driver.
type countingTx struct {
	dialect.Tx
	parent *countingDriver
}

func (t *countingTx) Exec(ctx context.Context, query string, args, v any) error {
	t.parent.recordIfInsert(query)
	return t.Tx.Exec(ctx, query, args, v)
}

func (t *countingTx) Query(ctx context.Context, query string, args, v any) error {
	t.parent.recordIfInsert(query)
	return t.Tx.Query(ctx, query, args, v)
}

// openCountingClient returns a client backed by a countingDriver so
// callers can introspect the exact SQL statements velox emits during
// a bulk create.
func openCountingClient(t *testing.T) (*integration.Client, *countingDriver) {
	t.Helper()
	raw, err := sqldialect.Open(dialect.SQLite, "file::memory:?cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	cd := &countingDriver{Driver: raw}
	client := integration.NewClient(integration.Driver(cd))
	t.Cleanup(func() { client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	// Reset the counter AFTER schema create so we don't include its
	// statements in the insert count.
	cd.mu.Lock()
	cd.inserts = nil
	cd.mu.Unlock()
	return client, cd
}

// TestCreateBulk_BatchingUnderHooks_IsOneStatement is the structural
// proof that the mutator chain rewrite does what it claims: bulk
// create of N rows emits ONE multi-row INSERT statement, and that
// property holds whether or not hooks are registered. This is the
// load-bearing correctness check for the performance claim, because
// the old hook-fallback design emitted N separate INSERTs whenever
// a hook was present.
func TestCreateBulk_BatchingUnderHooks_IsOneStatement(t *testing.T) {
	const n = 5

	cases := []struct {
		name         string
		registerHook bool
	}{
		{"without hook", false},
		{"with hook", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, cd := openCountingClient(t)
			ctx := context.Background()

			if tc.registerHook {
				client.User.Use(func(next integration.Mutator) integration.Mutator {
					return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
						return next.Mutate(ctx, m)
					})
				})
			}

			names := make([]string, n)
			for i := range names {
				names[i] = "u" + string(rune('0'+i))
			}
			users, err := client.User.MapCreateBulk(names, func(c *userclient.UserCreate, i int) {
				c.SetName(names[i]).
					SetEmail(names[i] + "@example.com").
					SetAge(30).
					SetRole(user.RoleUser).
					SetCreatedAt(now).
					SetUpdatedAt(now)
			}).Save(ctx)
			require.NoError(t, err)
			require.Len(t, users, n)

			// THE critical assertion: one INSERT, not N.
			assert.Equal(t, 1, cd.insertCount(),
				"bulk create of %d rows with %q should emit exactly 1 INSERT statement, got %d",
				n, tc.name, cd.insertCount())
		})
	}
}
