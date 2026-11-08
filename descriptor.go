package velox

import (
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Descriptor is an entity's metadata as velox-gen writes it: literals, read on the
// entity's first use. For generated code. Its column order is the scan order.
// EntityInfo is its runtime view.
type Descriptor[E any] struct {
	Table       string
	TableSchema string // the schema (PostgreSQL) or database holding Table; "" for the default
	Columns     []ColumnDescriptor
	PrimaryKey  []int // indexes into Columns
	// Bidi marks the join entity of a self-referencing many-to-many edge that has
	// no inverse, which is bidirectional, as in ent: each link is the two rows
	// (a, b) and (b, a). Insert and InsertAll write both, Delete removes both, and
	// Update and Save are refused. Its two columns are its primary key and have
	// one Go type. Its Policy may have Insert, which sees the rows the caller
	// gave, and it may have Hooks; it cannot have Read or Write, whose conditions
	// would show or delete one row of a link. A condition a Rewrite hook adds
	// to a delete is ANDed outside the two directions' Or, so it must hold
	// for both rows of a link, or the delete splits it.
	//
	// velox adds no conflict clause: an insert of a link fails when either
	// row already exists, its mirror included, so a link with one row (a
	// half-link, from one-way data) cannot be inserted plainly either.
	// OnConflict(…).DoNothing() is the idempotent insert, and it repairs a
	// half-link by writing the missing row. ent's AddXIDs is idempotent
	// silently, as it adds DO NOTHING to the join rows of an edge without
	// extra fields (an edge schema's rows get no such clause); velox's
	// GraphQL mutations add DoNothing, so they behave as ent's do. For
	// generated code.
	Bidi         bool
	Indexes      []IndexInfo
	Edges        []EdgeDescriptor      // in the order of the generated relation handles
	FieldPtr     func(x *E, i int) any // &x.<field of column i>
	EdgeFieldPtr func(x *E, i int) any // &x.Edges.<field of edge i> in generated code: a *One[T] or *Many[T]

	// Check runs the validators of column col on its value v, and is nil
	// when no column has any. For a pointer field, v is the value the
	// pointer points to: velox never calls Check for a nil pointer, which is
	// the absence of a value, nor with a pointer.
	Check func(col int, v any) error

	// Schema is the value that declares the entity's policies, hooks and
	// interceptors, nil when it has none. velox-gen writes the entity's
	// schema, as ent's runtime names it:
	//
	//	Schema: schema.Issue{},
	//
	// velox reads it once, when it builds the metadata: for each value its
	// Mixin method returns, in order, then for Schema itself, the value's
	// Policy, Hooks and Interceptors methods, each that it has, of either
	// receiver. A mixin's configuration is the value Mixin returns. The
	// policies are ANDed, in order, and none can lift another. The hooks
	// run in order, the first outermost. The interceptors' conditions are
	// ANDed, in order. A method is called once, and the functions it
	// returns on every statement.
	//
	// An entity written by hand names itself,
	//
	//	Schema: (*E)(nil),
	//
	// and its methods run on a nil *E, so they may not read their receiver.
	// An entity whose pointer type has a Policy, Hooks or Interceptors
	// method while Schema is not a *E is refused at first use with a
	// *SchemaError. So a policy, a hook or an interceptor is never silently
	// left out.
	//
	// Schema names no E, and Load composes it in code that is not generic,
	// so an entity's behaviour instantiates nothing for it.
	//
	// A panic in Mixin or a method becomes the entity's *SchemaError, as
	// does a nil hook or a nil Filter. None may read the entity's own
	// metadata in its body, outside the functions it returns: anything that
	// calls its VeloxMeta waits on the build in progress and deadlocks. A
	// Predicate does not: it names its columns, and the statement that
	// holds it resolves them.
	Schema any
}

// Load returns the entity's Meta, building it from descriptor the first time. A
// panic in desc, or in the behaviour of the Descriptor's Schema, becomes the
// Meta's *SchemaError.
//
// It does no more than call descriptor and bind its functions to a new entity: the
// build, which uses reflect, is BuildDescriptor's, and the binding of the
// Schema's behaviour is not generic, so that Load, compiled in every
// entity's package, holds no reflect.Type and instantiates nothing for an
// entity that has no policy, hook or interceptor. A build that fails is
// kept, as one that succeeds is.
//
// Load is not inlined: inlined into each entity's VeloxMeta, with its
// closure, it took gen of 100 entities from 24.5 to 25.8 MB (ADR 0039).
//
//go:noinline
func (o *MetaOnce[E]) Load[_ Lazy[E]](descriptor func() Descriptor[E]) *Meta[E] {
	o.once.Do(func() {
		x := new(E)
		var d Descriptor[E]
		info, err := engine.BuildDescriptor(x, unsafe.Pointer(x), func() engine.DescriptorView {
			d = descriptor()
			v := engine.DescriptorView{D: &engine.DescriptorFields{Table: d.Table, TableSchema: d.TableSchema, Columns: d.Columns, PrimaryKey: d.PrimaryKey,
				Bidi: d.Bidi, Indexes: d.Indexes, Relations: d.Edges, Check: d.Check, Schema: d.Schema}}
			if a := d.FieldPtr; a != nil {
				v.FieldPtr = func(i int) any { return a(x, i) }
			}
			if a := d.EdgeFieldPtr; a != nil {
				v.EdgeFieldPtr = func(i int) any { return a(x, i) }
			}
			return v
		})
		addr := d.FieldPtr
		if err == nil && d.Schema != nil {
			err = bind(info, d.Schema)
		}
		if err != nil {
			info, addr = nil, nil // an error Meta has no scan plan
		}
		m := &Meta[E]{d: metaState[E]{Info: info, Addr: addr, Err: err}}
		if info != nil {
			m.d.NilCols = engine.NilColumns(info)
			engine.SetInfoKit(info, kitOf(info, addr))
			engine.SetInfoCheck(info, d.Check)
		}
		o.m = m
	})
	return o.m
}

// EdgeTo is a relation to entity T keyed owner.Columns[ownerCol] =
// T.Columns[targetCol]. For generated code. Its field must be a One[T] or a
// Many[T]: the metadata is refused at first use when EdgeFieldPtr returns another
// type, such as the holder of another entity, as stale or hand-edited code
// would.
//
// The relation does not name T's metadata: if it did, every program that links
// the owner would link T, and through T's own relations every entity of the
// connected model. Where the relation is used, the handle Rel[E, T] says T, and
// CheckRelation checks the target.
func EdgeTo[T any, PT Entity[T]](name string, ownerCol, targetCol int) EdgeDescriptor {
	return engine.NewEdgeDescriptor(name, ownerCol, targetCol, [2]any{engine.ElemType((*One[T])(nil)), engine.ElemType((*Many[T])(nil))})
}

// EdgeBidi is the one-to-one relation of a self-referencing edge that has no
// inverse, which is bidirectional, as in ent: owner.Columns[keyCol] holds
// the other row's T.Columns[idCol], and that row's key holds this row's. For
// generated code. T is the owner entity, idCol is its primary key, and
// keyCol is a pointer field of the key's Go type, with a unique index and no
// RequiredEdge.
//
// velox writes both rows. An Insert that sets the key sets the other row's
// key too. An Update or Save that sets it clears the key of the row that
// pointed here and sets the new partner's; one that sets it to nil clears
// the partner's. They run in a transaction velox opens, a savepoint when the
// caller is in one. The clearing of the old partner is not scoped by a
// policy, a scope included (it runs on DB.WithoutPolicy, with a reason the
// hooks see): it
// is the edge's integrity, as ent's is. The link of the new partner is
// scoped as the caller's write, so a partner it may not change cannot be
// linked.
// Delete writes only the deleted rows; the foreign key's ON DELETE acts on
// their partners, and velox-gen's default for this NULL-able key, as ent's,
// is SET NULL.
//
// A new partner that already has a partner is refused with a
// *ConstraintError of kind ConstraintUnique: clear its key first. Match it
// on Kind, not Constraint: Constraint is the database's name for the key's
// unique index when the index refuses the write, and the key column's name
// when velox's link of the partner does (a partner whose key points one way,
// or one the caller may not change).
//
// velox refuses, before anything is sent, what it could not keep both rows
// of: an Update that sets the key and does not name one row by its primary
// key (Where on the id's EQ), as velox re-points that row's partners; Add or
// Sub on the key; a row given itself as its partner, with a
// *ValidationError on the key's field; and an InsertAll or an upsert that
// sets the key. The last two are stricter than ent, which links through
// them: InsertAll reads no generated key back, so velox cannot name the rows
// to link, and an upsert's DO NOTHING or DO UPDATE leaves unknown whether the
// row that holds the key is the caller's. Insert the rows, then Save each
// key. A Rewrite hook may not change the row such an update names, the
// key's value, nor make such an insert an upsert.
func EdgeBidi[T any, PT Entity[T]](name string, keyCol, idCol int) EdgeDescriptor {
	r := EdgeTo[T, PT](name, keyCol, idCol)
	engine.SetEdgeBidi(&r, engine.ElemType((*T)(nil)))
	return r
}

// EdgeThrough is a many-to-many relation to T through the join entity J:
// owner.Columns[ownerCol] = J.Columns[joinOwner] and J.Columns[joinTarget] =
// T.Columns[targetCol]. For generated code. Its field is a Many[T]. J's rows
// are written like any entity's, with Insert and Delete.
//
// Loading it reads J's rows for the owners' keys, then T's rows for the keys
// they hold, and Has is an EXISTS on J holding an EXISTS on T. T's Read
// policy, a scope included, applies, as it does to any relation's target. J
// cannot have a Read policy of its own: it is read through its two sides,
// whose Read policies apply, and the relation is refused when it resolves. velox does not check that an endpoint is a row the
// caller's policies let it see or change: an application that writes links, or
// deletes them, checks both endpoints itself, in J's Policy (Insert, and Write for
// updates and deletes) or in a hook on J. When J is the join entity of a bidirectional edge (Descriptor.Bidi), its Policy cannot
// have Write either, since a condition would delete one row of a link: check
// deletes in a hook on J.
func EdgeThrough[J, T any, PJ Entity[J], PT Entity[T]](name string, ownerCol, joinOwner, joinTarget, targetCol int) EdgeDescriptor {
	r := EdgeTo[T, PT](name, ownerCol, targetCol)
	engine.SetEdgeThrough(&r, &engine.Through{
		Join: func() (*EntityInfo, error) {
			m := stateOf(PJ(nil).VeloxMeta())
			return m.Info, m.Err
		},
		OwnerCol:  joinOwner,
		TargetCol: joinTarget,
	})
	return r
}

// Linked is e with its target T linked: the relation then names T's
// metadata, so a program that links the owner links T. For generated code.
// velox-gen wraps the relations that an entity's behaviour names with
// velox.HasEdge or HasEdgeWith, which name no target type of their own:
//
//	velox.Linked[Doc](velox.EdgeTo[Doc]("doc", commentColDocID, docColID))
//
// A condition by the name of a relation that is not linked fails its
// statement.
func Linked[T any, PT Entity[T]](e EdgeDescriptor) EdgeDescriptor {
	engine.SetEdgeTarget(&e, func() (*EntityInfo, error) {
		m := stateOf(PT(nil).VeloxMeta())
		return m.Info, m.Err
	})
	return e
}
