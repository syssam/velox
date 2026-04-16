package compiler

import (
	"slices"
	"sync"
)

var (
	extensionsMu sync.RWMutex
	extensions   = map[string]func() Extension{}
)

// RegisterExtension registers a factory function for an Extension under the given name.
// It is safe for concurrent use. Calling RegisterExtension with an already-registered
// name overwrites the previous entry.
func RegisterExtension(name string, factory func() Extension) {
	extensionsMu.Lock()
	defer extensionsMu.Unlock()
	extensions[name] = factory
}

// GetExtensionFactory returns the factory function registered under the given name.
// The second return value reports whether a factory was found.
func GetExtensionFactory(name string) (func() Extension, bool) {
	extensionsMu.RLock()
	defer extensionsMu.RUnlock()
	f, ok := extensions[name]
	return f, ok
}

// ListExtensions returns a sorted list of registered extension names.
func ListExtensions() []string {
	extensionsMu.RLock()
	defer extensionsMu.RUnlock()
	names := make([]string, 0, len(extensions))
	for name := range extensions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
