package graphql

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWhereInputGeneratedPackageName asserts that the generator writes
// WhereInput files into a package literally named "filter", not the
// legacy "gqlfilter". This pin-test catches regressions in future refactors
// that might flip the name back.
func TestWhereInputGeneratedPackageName(t *testing.T) {
	g := newTestGeneratorWithConfig(Config{
		RelayConnection: true,
		WhereInputs:     true,
		ORMPackage:      "example/ent",
	}, mockGraph().Nodes...)

	file := g.genWhereInputShared()
	require.NotNil(t, file)

	var buf bytes.Buffer
	require.NoError(t, file.Render(&buf))

	got := buf.String()
	require.Contains(t, got, "package filter\n",
		"WhereInput shared file must declare `package filter`, not gqlfilter")
	require.NotContains(t, got, "package gqlfilter",
		"legacy package name must be gone")
}
