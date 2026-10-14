package velox

// One holds a to-one relation, filled by With. Its zero value means not
// loaded. The value is held as any so that an entity's type does not reach
// the types of the entities it relates to: every package that uses an
// instantiated generic type compiles its type descriptor and everything the
// descriptor reaches (design §3 rule 2).
type One[T any] struct {
	v      any // *T
	loaded bool
}

// Get returns the related entity (nil when there is none) and whether the
// relation was loaded.
func (o One[T]) Get[_ Lazy[T]]() (*T, bool) {
	p, _ := o.v.(*T)
	return p, o.loaded
}

// Many holds a to-many relation, filled by With. Its zero value means not
// loaded; the value is held as any for the reason given on One.
type Many[T any] struct {
	v      any // []T
	loaded bool
}

// Get returns the related entities and whether the relation was loaded.
func (m Many[T]) Get[_ Lazy[T]]() ([]T, bool) {
	s, _ := m.v.([]T)
	return s, m.loaded
}
