package gqlrelay

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Whatever this package imports is linked into every server velox generates
// code for, so it imports no GraphQL engine: gqlgen comes in through
// gqlgenrelay, only when the generated code asks for it.
func TestImportsNoEngine(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	require.NoError(t, err)
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "99designs/gqlgen") {
			t.Errorf("gqlrelay imports %s", dep)
		}
	}
}

// A polymorphic edge whose selection is only __typename and id is answered
// from the foreign key; under any engine, not only gqlgen.
func TestSelectionCoveredByID(t *testing.T) {
	sel := func(names ...string) *treeField {
		f := &treeField{name: "item"}
		for _, n := range names {
			f.children = append(f.children, &treeField{name: n})
		}
		return f
	}
	assert.True(t, SelectionCoveredByID(treeContext(sel("__typename", "id")), "Item", "Todo"))
	assert.False(t, SelectionCoveredByID(treeContext(sel("id", "title")), "Item", "Todo"))
	// A field inside a fragment of an implementor counts too.
	withFragment := sel("id")
	withFragment.byType = map[string][]*treeField{"Todo": {{name: "title"}}}
	assert.False(t, SelectionCoveredByID(treeContext(withFragment), "Item", "Todo"))

	withoutDefault(t)
	assert.False(t, SelectionCoveredByID(context.Background(), "Item"), "no selection to read: the caller must query")
}

// Collection with no source at all projects and loads nothing. That is what
// code generated before gqlgenrelay existed does under gqlgen, so it says
// so, once; an engine source that merely has no field here is not that.
func TestNoSelectionSourceWarnsOnce(t *testing.T) {
	withoutDefault(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	warnNoSource = sync.Once{}

	outside := WithSelectionSource(context.Background(), func(context.Context) (SelectedField, bool) { return nil, false })
	_, ok := selectedField(outside)
	assert.False(t, ok)
	assert.Empty(t, buf.String(), "an installed source with no field here is not a missing source")

	for range 3 {
		_, ok = selectedField(context.Background())
		assert.False(t, ok)
	}
	assert.Equal(t, 1, strings.Count(buf.String(), "no selection source"), buf.String())
}

// withoutDefault removes the default source for one test; the test binary
// links gqlgenrelay, which registers one.
func withoutDefault(t *testing.T) {
	t.Helper()
	prev := defaultSource.Swap(nil)
	t.Cleanup(func() { defaultSource.Store(prev) })
}
