package integration_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
)

// TestConcurrentMigrate_SharedTables pins that two clients migrating at the
// same time do not race on the generated migrate.Tables values. Those are
// package-level, so every client in the process shares them, and migration
// rewrites foreign-key symbols and primary-key markers on them while the
// planning phase reads them back. Each client builds its own migration
// engine, so the engine's own mutex does not serialize this.
//
// Run with -race. Without the shared guard in dialect/sql/schema this
// reports a data race between setupTables and realm — which is how it was
// found: parallel subtests in e2e_context_cancel_test.go each called
// Schema.Create and tripped the detector only when the full suite ran.
func TestConcurrentMigrate_SharedTables(t *testing.T) {
	const clients = 8
	var wg sync.WaitGroup
	errs := make([]error, clients)
	for i := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A private in-memory database per client: they must not
			// serialize on the database, only (if at all) on the shared
			// table values this test is about.
			dsn := fmt.Sprintf("file:concurrentmigrate%d?mode=memory&_pragma=foreign_keys(1)", i)
			client, err := integration.Open("sqlite", dsn)
			if err != nil {
				errs[i] = err
				return
			}
			defer client.Close()
			for range 5 {
				if err := client.Schema.Create(context.Background()); err != nil {
					errs[i] = err
					return
				}
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		require.NoErrorf(t, err, "client %d failed to migrate concurrently", i)
	}
}
