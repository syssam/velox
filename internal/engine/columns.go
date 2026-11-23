package engine

import "sync"

// built holds the metadata velox has built, by entity name (EntityName),
// for HasColumn. Two packages may each have an entity of one name, so a
// name holds a list.
var built struct {
	mu     sync.RWMutex
	byName map[string][]*EntityInfo
}

// registerInfo records in, whose metadata has just been built.
func registerInfo(in *EntityInfo) {
	name := EntityName(in)
	built.mu.Lock()
	defer built.mu.Unlock()
	if built.byName == nil {
		built.byName = map[string][]*EntityInfo{}
	}
	built.byName[name] = append(built.byName[name], in)
}

// HasColumn reports whether an entity named entity, as a Filter is given
// it, whose metadata velox has built, has a column named column. When two
// packages each have an entity of that name, one having it is enough.
func HasColumn(entity, column string) bool {
	built.mu.RLock()
	defer built.mu.RUnlock()
	for _, in := range built.byName[entity] {
		if _, ok := ColumnNamed(in, column); ok {
			return true
		}
	}
	return false
}
