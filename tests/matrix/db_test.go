package matrix

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	gen "example.com/matrix/velox"

	_ "modernc.org/sqlite"
)

var dbSeq atomic.Int64

// openClient returns a client over a fresh in-memory SQLite database with the
// schema created. Every call gets its own database, so tests do not depend
// on each other or on -count.
func openClient(t *testing.T) *gen.Client {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", name, dbSeq.Add(1))
	client, err := gen.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	return client
}

// must passes v through and panics on a non-nil error: must(q.Only(ctx)).
// The Querier interfaces have no OnlyX/AllX, and a generic helper cannot
// take both t and a (value, error) pair.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
