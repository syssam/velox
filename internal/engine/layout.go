package engine

import (
	"reflect"
	"sync"
)

// layouts holds each entity's EntityInfo, keyed by the entity's reflect.Type.
// buildDescriptor stores it once the columns are known, before Load binds
// the entity's policy, hooks and interceptors. So a column found by name
// inside that binding does not wait on the build in progress. Only the
// columns are read from it, and they do not change once stored.
var layouts sync.Map

// unlinked holds, as keys, the reflect.Types of the entities whose last
// build panicked with a NotLinkedError before their layout was stored.
var unlinked sync.Map

// publishLayout stores info as entity t's layout. A later build of t, a
// retry after a NotLinkedError, replaces it with columns of the same shape.
func publishLayout(t reflect.Type, info *EntityInfo) {
	layouts.Store(t, info)
	unlinked.Delete(t)
}
