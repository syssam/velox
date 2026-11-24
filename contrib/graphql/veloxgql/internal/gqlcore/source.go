package gqlcore

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/syssam/velox"
)

// A SelectionSource tells the runtime about the field resolving in ctx.
// An adapter registers one for its engine (Register), which finds the field
// from the engine's own context values, with no per-request setup. An
// operation's state may carry another (Install), which then wins.
type SelectionSource interface {
	// Field is the field resolving in ctx; ok is false outside a field
	// (an application's own code, a field the engine gives no context, a
	// context of another engine). A registered source sets the field's
	// Table; for an installed one FieldOf sets ctx's (WithTable).
	Field(ctx context.Context) (f Field, ok bool)
	// Wait is called around a wait on another field's batch and returns
	// the function that ends it.
	Wait(ctx context.Context) (done func())
}

// Field is one field occurrence of the operation.
type Field struct {
	Object string // the parent GraphQL type, "Post"
	Name   string // the field's name, not its alias, "tags"
	// Type is the named type the field returns, its list and non-null
	// wrappers taken off: "PostConnection", "Tag".
	Type string
	// Key is comparable and distinct per occurrence (two aliases are two
	// keys); every row of a list shares it. Batches and selections are
	// memoised by it. A nil Key batches nothing: the row reads alone.
	Key any
	// Sel is the field's sub-selection; nil when the source cannot read
	// it, and the caller reads every column.
	Sel Selection
	// Table is the table of the operation (or the response) the field
	// resolves in, where its rows group and its edges batch; nil batches
	// nothing.
	Table *Table
}

// Selection is a sub-selection, its fragments folded.
type Selection interface {
	// On is the fields selected on objects of the type typ, with each
	// one's sub-selection; ok is false when the source cannot tell (a
	// fragment on an interface it has no schema for).
	On(typ string) (fields []SelectedField, ok bool)
}

// SelectedField is a field a Selection selects: its name, not its alias,
// and its own sub-selection.
type SelectedField struct {
	Name string
	Sel  Selection // nil for a leaf
}

// state is what an adapter or veloxgql.Service puts in a context: the DB,
// the limits and the selection source, as one value. A mutation's
// transaction replaces the DB and keeps the rest. A nil src leaves the
// field to the registered sources.
type state struct {
	db *velox.DB
	// limits is the adapter's *veloxgql.Limits, which this package cannot
	// name: veloxgql reads it back.
	limits any
	src    SelectionSource
}

// A Reader is a SelectionSource an adapter registers for its engine.
type Reader interface {
	SelectionSource
	// In reports whether ctx is in a field of the reader's engine, without
	// allocating.
	In(ctx context.Context) bool
}

// registered are the readers the adapters registered, in the order of
// their Register calls; registerMu orders the calls.
var (
	registerMu sync.Mutex
	registered atomic.Pointer[[]Reader]
)

// Register adds r to the readers that find the field of a context whose
// state has no source of its own: an adapter's package registers its
// engine's reader in its init, so that a server needs no per-request setup.
// A reader's Field must return ok false, and In false, for a context of
// another engine. It panics on a nil r.
func Register(r Reader) {
	if r == nil {
		panic("veloxgql: Register of a nil Reader")
	}
	registerMu.Lock()
	defer registerMu.Unlock()
	var rs []Reader
	if p := registered.Load(); p != nil {
		rs = slices.Clone(*p)
	}
	rs = append(rs, r)
	registered.Store(&rs)
}

// stateKey is the context key of the *state.
type stateKey struct{}

// Install returns ctx with an operation's state: db, limits (a
// *veloxgql.Limits, which LimitsOf returns) and src, a selection source that
// replaces the registered ones. Only the adapters and veloxgql.Service call
// it. It panics on a nil src: InstallDB is the state without one.
func Install(ctx context.Context, db *velox.DB, limits any, src SelectionSource) context.Context {
	if src == nil {
		panic("veloxgql: Install needs a SelectionSource; InstallDB takes none")
	}
	return context.WithValue(ctx, stateKey{}, &state{db: db, limits: limits, src: src})
}

// InstallDB returns ctx with a state of db and limits (a *veloxgql.Limits)
// and no selection source: the field of a context under it is the
// registered sources'.
func InstallDB(ctx context.Context, db *velox.DB, limits any) context.Context {
	return context.WithValue(ctx, stateKey{}, &state{db: db, limits: limits})
}

// WithDB returns ctx with its state's DB replaced by db, the limits and the
// source kept: a mutation's transaction. ctx must hold a state.
func WithDB(ctx context.Context, db *velox.DB) context.Context {
	s := *stateOf(ctx)
	s.db = db
	return context.WithValue(ctx, stateKey{}, &s)
}

// stateOf is ctx's state, or nil.
func stateOf(ctx context.Context) *state {
	s, _ := ctx.Value(stateKey{}).(*state)
	return s
}

// DB returns the DB of ctx's state; nil without one.
func DB(ctx context.Context) *velox.DB {
	if s := stateOf(ctx); s != nil {
		return s.db
	}
	return nil
}

// LimitsOf returns the limits of ctx's state, as Install was given them;
// nil without one.
func LimitsOf(ctx context.Context) any {
	if s := stateOf(ctx); s != nil {
		return s.limits
	}
	return nil
}

// Source returns the selection source of ctx's state; nil without one, and
// for a state with none (InstallDB).
func Source(ctx context.Context) SelectionSource {
	if s := stateOf(ctx); s != nil {
		return s.src
	}
	return nil
}

// FieldOf returns the field of ctx, with its Table: the field the source of
// ctx's state tells, with ctx's table (WithTable), or, when ctx has no
// state or one with no source, the first registered source's that knows a
// field in ctx, with the table that source keeps. ok is false outside a
// field.
func FieldOf(ctx context.Context) (Field, bool) {
	_, f, ok := lookup(ctx)
	return f, ok
}

// lookup is FieldOf, with the source that told it.
func lookup(ctx context.Context) (SelectionSource, Field, bool) {
	if s := stateOf(ctx); s != nil && s.src != nil {
		f, ok := s.src.Field(ctx)
		if ok {
			f.Table = TableFromContext(ctx)
		}
		return s.src, f, ok
	}
	if p := registered.Load(); p != nil {
		for _, src := range *p {
			if f, ok := src.Field(ctx); ok {
				return src, f, true
			}
		}
	}
	return nil, Field{}, false
}

// SourceOf returns the source that tells the field of ctx (FieldOf): its
// state's, or the registered one whose engine ctx is in; nil when there is
// none. It asks no source for the field, which may allocate.
func SourceOf(ctx context.Context) SelectionSource {
	if s := stateOf(ctx); s != nil && s.src != nil {
		return s.src
	}
	if p := registered.Load(); p != nil {
		for _, r := range *p {
			if r.In(ctx) {
				return r
			}
		}
	}
	return nil
}

// wait is the Wait of the source of ctx's field. gqlgraph uses a batch only
// in a context whose field a source knows (FieldOf).
func wait(ctx context.Context) func() {
	src := SourceOf(ctx)
	if src == nil {
		return noWait
	}
	return src.Wait(ctx)
}

// noWait is wait's done function outside a field.
func noWait() {}

// Selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns: through edges and node for
// a connection field, the field's own selection otherwise, with fragments
// whose type is typ, and without __typename. ok is false outside a field,
// and when the field returns neither typ nor a connection to it. The result
// is computed once per field occurrence (Field.Key) and type per table.
//
// ok is false too where the source cannot see what the objects' resolvers
// read, so that the caller reads every column:
//   - a field of the connection other than edges, pageInfo, totalCount and
//     __typename, or of an edge other than node, cursor and __typename (an
//     application's own, which may resolve from the nodes);
//   - a selection the source cannot read (Field.Sel nil, or On's ok false),
//     as gqlgen's without the server's schema and a fragment on an
//     interface or a union.
//
// The result is shared: the caller must not change it.
func Selected(ctx context.Context, typ string) (fields []string, ok bool) {
	f, ok := FieldOf(ctx)
	if !ok || f.Key == nil || f.Sel == nil {
		return nil, false
	}
	return f.Table.Selected(f.Key, typ, func() ([]string, bool) {
		return computeSelected(f, typ)
	})
}

// computeSelected is Selected's result for f, computed.
func computeSelected(f Field, typ string) ([]string, bool) {
	var objs []Selection // the selections on the objects of typ
	switch f.Type {
	case typ:
		objs = []Selection{f.Sel}
	case typ + "Connection":
		conn, ok := on(f.Sel, typ+"Connection")
		if !ok {
			return nil, false
		}
		for _, e := range conn {
			switch e.Name {
			case "pageInfo", "totalCount", "__typename":
			case "edges":
				edges, ok := on(e.Sel, typ+"Edge")
				if !ok {
					return nil, false
				}
				for _, n := range edges {
					switch n.Name {
					case "cursor", "__typename":
					case "node":
						objs = append(objs, n.Sel)
					default:
						return nil, false
					}
				}
			default:
				return nil, false
			}
		}
	default:
		return nil, false
	}
	fields := []string{}
	for _, s := range objs {
		cs, ok := on(s, typ)
		if !ok {
			return nil, false
		}
		for _, c := range cs {
			if c.Name != "__typename" && !slices.Contains(fields, c.Name) {
				fields = append(fields, c.Name)
			}
		}
	}
	return fields, true
}

// on is s.On(typ); a nil s, a leaf's, selects nothing.
func on(s Selection, typ string) ([]SelectedField, bool) {
	if s == nil {
		return nil, true
	}
	return s.On(typ)
}
