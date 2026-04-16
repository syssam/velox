package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterExtension(t *testing.T) {
	const name = "test-ext-register"
	factory := func() Extension { return DefaultExtension{} }

	RegisterExtension(name, factory)
	t.Cleanup(func() {
		extensionsMu.Lock()
		delete(extensions, name)
		extensionsMu.Unlock()
	})

	names := ListExtensions()
	assert.Contains(t, names, name)

	// Must be sorted.
	for i := 1; i < len(names); i++ {
		assert.LessOrEqual(t, names[i-1], names[i], "ListExtensions should return sorted names")
	}
}

func TestGetExtensionFactory(t *testing.T) {
	const name = "test-ext-get"

	// Miss before registration.
	_, ok := GetExtensionFactory(name)
	assert.False(t, ok)

	var called bool
	factory := func() Extension {
		called = true
		return DefaultExtension{}
	}
	RegisterExtension(name, factory)
	t.Cleanup(func() {
		extensionsMu.Lock()
		delete(extensions, name)
		extensionsMu.Unlock()
	})

	f, ok := GetExtensionFactory(name)
	require.True(t, ok)
	require.NotNil(t, f)

	// Invoke the factory to verify it is the one we registered.
	_ = f()
	assert.True(t, called)
}
