package runner

// kind identifies an entity table so the handle registry can disambiguate db
// ids that collide across tables (author #1 and post #1 are different entities
// with the same numeric id under SQLite's per-table AUTOINCREMENT).
type kind int

const (
	kindAuthor kind = iota
	kindPost
	kindComment
	kindTag
)

// handleRegistry maps between an entity's creation handle (its op-program
// index, globally unique) and its database id (unique only within a table).
//
// handleToID is global: each op index creates at most one entity, so the index
// is a unique key. idToHandle MUST be keyed by (kind, id) because db ids repeat
// across tables — keying by id alone silently overwrote the author's mapping
// with the post's when both had id 1, yielding the wrong author Ref on read.
//
// It is ORM-free (operates on plain ints), so it lives outside run_velox.go /
// run_ent.go and is shared by both executors.
type handleRegistry struct {
	handleToID map[int]int    // op index (handle) -> db id
	idToHandle map[kindID]int // (kind, db id) -> op index (handle)
}

// kindID is the composite key disambiguating per-table db ids.
type kindID struct {
	k  kind
	id int
}

func newHandleRegistry() *handleRegistry {
	return &handleRegistry{
		handleToID: map[int]int{},
		idToHandle: map[kindID]int{},
	}
}

// record links a created entity's (kind, db id) to its creation handle.
func (r *handleRegistry) record(k kind, handle, id int) {
	r.handleToID[handle] = id
	r.idToHandle[kindID{k, id}] = handle
}

// handleForID returns the creation handle of the entity of kind k with the
// given db id, or -1 if unknown.
func (r *handleRegistry) handleForID(k kind, id int) int {
	if h, ok := r.idToHandle[kindID{k, id}]; ok {
		return h
	}
	return -1
}
