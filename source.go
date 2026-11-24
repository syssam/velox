package velox

import (
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Source is the DB an entity was read or written through, which its edge
// reads run on. For generated code: velox-gen gives every entity one
// unexported Source field, and velox sets it on every entity a statement
// reads or writes: All, One, First, Iter, With loads, joined targets, Raw,
// Paginate, Insert, InsertAll, InsertBulk, upserts, UpdateQuery.One and
// Save. Storing it is one pointer store per row.
//
// It is the DB the statement ran on, with two exceptions. It never keeps a
// WithoutPolicy reason, so an edge read is under the entities' policies.
// And a transaction's falls back to the DB the transaction was begun on
// once the transaction has ended, committed or rolled back, so an edge read
// after commit runs outside it; it does not keep the transaction alive.
// The zero Source is none: an entity no statement made has it, and its
// edge reads fail with ErrNoSource.
//
// reflect.DeepEqual of two entities read through different DBs, and go-cmp
// without cmpopts.IgnoreUnexported, see the field; veloxtest.Equal ignores
// it.
type Source struct{ s *engine.Source }

// The engine stores an *engine.Source in the field, through its offset.
var _ [unsafe.Sizeof(Source{}) - unsafe.Sizeof((*engine.Source)(nil))]struct{} = [0]struct{}{}

// ErrNoSource fails each statement of an edge read (QueryEdge, the
// generated QueryX methods) of an entity that no statement read or wrote:
// one built by hand, by LoadedOne or LoadedMany, or decoded from JSON.
// Query such an edge on a DB: db.Query[T]().Where(r.Of(owner)).
var ErrNoSource = engine.ErrNoSource

// QueryEdge returns the query of the entities related to owner through r,
// on the DB owner was read or written through, its Source src. For
// generated code: velox-gen's QueryX methods call it.
//
//	func (m *Post) QueryAuthor() velox.Query[User] { return velox.QueryEdge(m.source, m, Posts.Author) }
//
// It is the query db.Query[T]().Where(r.Of(owner)) on that DB, read only,
// as any Query is: a write names its DB (db.Update, db.Insert, db.Delete).
// T's Read policy, a scope included, interceptors and filters apply, with
// the viewer of the context it runs under: the Source never keeps a
// WithoutPolicy reason. owner's key is read when QueryEdge is called. An
// owner with no Source gives a query each of whose statements fails with
// ErrNoSource; a nil owner fails them too.
func QueryEdge[E, T any](src Source, owner *E, r Rel[E, T]) Query[T] {
	m, err := metaOf[T]()
	if m == nil {
		m = &metaState[T]{Err: err}
	}
	ref := engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}
	return Query[T]{s: engine.EdgeQuery(src.s, ref, r.where, r.err, unsafe.Pointer(owner), m.Info), meta: m}
}
