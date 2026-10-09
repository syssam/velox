package orm

import (
	"context"
	"fmt"
	"slices"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/privacy"
)

// Mutation is what hooks and mutation policies receive: the pending create,
// update or delete of any entity. It is one type for every entity; fields and
// edges are addressed by their schema names, through the velox.Mutation
// interface that schema hooks and privacy rules are written against.
//
// A hook that type-asserts the mutation to an interface of typed setters
// (SetWorkspaceID(int64)) does not match this type and must use SetField.
type Mutation struct {
	op velox.Op
	c  *MutCore
	ex Executor

	old       any
	oldLoaded bool
}

var (
	_ velox.Mutation     = (*Mutation)(nil)
	_ privacy.Filterable = (*Mutation)(nil)
)

// Op returns the operation.
func (m *Mutation) Op() velox.Op { return m.op }

// Type returns the name of the entity.
func (m *Mutation) Type() string { return m.c.M.Name }

// Executor returns the database or transaction the mutation runs on, for a
// hook that reads or writes other rows as part of it.
func (m *Mutation) Executor() Executor { return m.ex }

func (m *Mutation) field(name string) (*FieldMeta, error) {
	meta := m.c.M
	meta.prepare()
	if f := meta.byName[name]; f != nil {
		return f, nil
	}
	return nil, fmt.Errorf("orm: unknown %s field %q", meta.Name, name)
}

func (m *Mutation) edge(name string) (*EdgeMeta, error) {
	for _, e := range m.c.M.Edges {
		if e.Name == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("orm: unknown %s edge %q", m.c.M.Name, name)
}

// columns lists, in the order they were first set, the fields whose last
// setter is of the given kind.
func (m *Mutation) columns(kind SetKind) []string {
	var names []string
	for _, f := range m.c.M.Fields {
		if s := m.last(f.Column); s != nil && s.Kind == kind {
			names = append(names, f.Name)
		}
	}
	return names
}

// last returns the setter that decides the value of column, or nil.
func (m *Mutation) last(column string) *S {
	for i := len(m.c.Sets) - 1; i >= 0; i-- {
		if s := &m.c.Sets[i]; s.Edge == nil && s.Col == column {
			return s
		}
	}
	return nil
}

func (m *Mutation) drop(column string) {
	m.c.Sets = slices.DeleteFunc(m.c.Sets, func(s S) bool { return s.Edge == nil && s.Col == column })
}

// typed converts a value a hook passed for field f to the Go type of the field.
func (m *Mutation) typed(f *FieldMeta, v any) (any, error) { return m.c.M.convert(f, v) }

// Fields returns the fields the mutation sets.
func (m *Mutation) Fields() []string { return m.columns(SetValue) }

// Field returns the value the mutation sets the field to.
func (m *Mutation) Field(name string) (velox.Value, bool) {
	f, err := m.field(name)
	if err != nil {
		return nil, false
	}
	if s := m.last(f.Column); s != nil && s.Kind == SetValue {
		return s.Val, true
	}
	return nil, false
}

// SetField sets a field. The value must be of the field's Go type or
// convertible to it.
func (m *Mutation) SetField(name string, value velox.Value) error {
	f, err := m.field(name)
	if err != nil {
		return err
	}
	v, err := m.typed(f, value)
	if err != nil {
		return err
	}
	m.drop(f.Column)
	m.c.Sets = append(m.c.Sets, S{Kind: SetValue, Col: f.Column, Val: v})
	return nil
}

// AddedFields returns the fields the mutation increments.
func (m *Mutation) AddedFields() []string { return m.columns(SetAdd) }

// AddedField returns the amount the mutation adds to the field.
func (m *Mutation) AddedField(name string) (velox.Value, bool) {
	f, err := m.field(name)
	if err != nil {
		return nil, false
	}
	if s := m.last(f.Column); s != nil && s.Kind == SetAdd {
		return s.Val, true
	}
	return nil, false
}

// AddField adds value to a numeric field.
func (m *Mutation) AddField(name string, value velox.Value) error {
	f, err := m.field(name)
	if err != nil {
		return err
	}
	v, err := m.typed(f, value)
	if err != nil {
		return err
	}
	m.c.Sets = append(m.c.Sets, S{Kind: SetAdd, Col: f.Column, Val: v})
	return nil
}

// ClearedFields returns the fields the mutation sets to NULL.
func (m *Mutation) ClearedFields() []string { return m.columns(SetNull) }

// FieldCleared reports whether the mutation sets the field to NULL.
func (m *Mutation) FieldCleared(name string) bool {
	f, err := m.field(name)
	if err != nil {
		return false
	}
	s := m.last(f.Column)
	return s != nil && s.Kind == SetNull
}

// ClearField sets the field to NULL.
func (m *Mutation) ClearField(name string) error {
	f, err := m.field(name)
	if err != nil {
		return err
	}
	m.drop(f.Column)
	m.c.Sets = append(m.c.Sets, S{Kind: SetNull, Col: f.Column})
	return nil
}

// ResetField removes every change the mutation makes to the field.
func (m *Mutation) ResetField(name string) error {
	f, err := m.field(name)
	if err != nil {
		return err
	}
	m.drop(f.Column)
	return nil
}

// keyEdge reports how a to-one edge whose key is a schema field is changed:
// the mutation holds such a change as a write of the field.
func (m *Mutation) keyEdge(e *EdgeMeta) (*S, bool) {
	if !e.fkOnOwner() || m.c.M.Field(e.Columns[0]) == nil {
		return nil, false
	}
	return m.last(e.Columns[0]), true
}

func (m *Mutation) edges(kind SetKind) []string {
	var names []string
	for _, e := range m.c.M.Edges {
		if s, ok := m.keyEdge(e); ok {
			if s != nil && (kind == EdgeAdd && s.Kind == SetValue || kind == EdgeClear && s.Kind == SetNull) {
				names = append(names, e.Name)
			}
			continue
		}
		if slices.ContainsFunc(m.c.Sets, func(s S) bool { return s.Edge == e && s.Kind == kind }) {
			names = append(names, e.Name)
		}
	}
	return names
}

func (m *Mutation) ids(kind SetKind, name string) []velox.Value {
	e, err := m.edge(name)
	if err != nil {
		return nil
	}
	if s, ok := m.keyEdge(e); ok {
		if kind == EdgeAdd && s != nil && s.Kind == SetValue {
			return []velox.Value{s.Val}
		}
		return nil
	}
	var ids []velox.Value
	for _, s := range m.c.Sets {
		if s.Edge == e && s.Kind == kind {
			for _, id := range s.IDs {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// AddedEdges returns the edges the mutation adds neighbors to.
func (m *Mutation) AddedEdges() []string { return m.edges(EdgeAdd) }

// AddedIDs returns the ids the mutation connects through the edge.
func (m *Mutation) AddedIDs(name string) []velox.Value { return m.ids(EdgeAdd, name) }

// RemovedEdges returns the edges the mutation removes neighbors from.
func (m *Mutation) RemovedEdges() []string { return m.edges(EdgeRemove) }

// RemovedIDs returns the ids the mutation disconnects from the edge.
func (m *Mutation) RemovedIDs(name string) []velox.Value { return m.ids(EdgeRemove, name) }

// ClearedEdges returns the edges the mutation clears.
func (m *Mutation) ClearedEdges() []string { return m.edges(EdgeClear) }

// EdgeCleared reports whether the mutation clears the edge.
func (m *Mutation) EdgeCleared(name string) bool { return slices.Contains(m.edges(EdgeClear), name) }

// ClearEdge disconnects every neighbor of the edge.
func (m *Mutation) ClearEdge(name string) error {
	e, err := m.edge(name)
	if err != nil {
		return err
	}
	if _, ok := m.keyEdge(e); ok {
		m.drop(e.Columns[0])
		m.c.Sets = append(m.c.Sets, S{Kind: SetNull, Col: e.Columns[0]})
		return nil
	}
	m.c.Sets = append(m.c.Sets, S{Kind: EdgeClear, Edge: e})
	return nil
}

// ResetEdge removes every change the mutation makes to the edge.
func (m *Mutation) ResetEdge(name string) error {
	e, err := m.edge(name)
	if err != nil {
		return err
	}
	if _, ok := m.keyEdge(e); ok {
		m.drop(e.Columns[0])
		return nil
	}
	m.c.Sets = slices.DeleteFunc(m.c.Sets, func(s S) bool { return s.Edge == e })
	return nil
}

// OldField returns the value the field has in the database before the
// mutation. It is available for an update of one row (SaveOne).
func (m *Mutation) OldField(ctx context.Context, name string) (velox.Value, error) {
	if !m.op.Is(velox.OpUpdateOne) {
		return nil, fmt.Errorf("orm: OldField(%q) is only available when updating one %s", name, m.c.M.Name)
	}
	f, err := m.field(name)
	if err != nil {
		return nil, err
	}
	if !m.oldLoaded {
		// The row as the mutation's own conditions select it, without the
		// policy or interceptors of a user-facing read.
		q := &QueryCore{M: m.c.M, Preds: m.c.Preds, Lim: 2}
		rows, err := q.all(ctx, m.ex.conn())
		if err != nil {
			return nil, err
		}
		switch len(rows) {
		case 0:
			return nil, fmt.Errorf("%w: %s", ErrNotFound, m.c.M.Name)
		case 1:
			m.old, m.oldLoaded = rows[0], true
		default:
			return nil, fmt.Errorf("%w: %s", ErrNotSingular, m.c.M.Name)
		}
	}
	return m.c.M.get(m.old, f.Column), nil
}

// WhereP restricts the rows an update or delete touches. It has no effect on
// a create.
func (m *Mutation) WhereP(ps ...func(*sql.Selector)) {
	for _, p := range ps {
		m.c.Preds = append(m.c.Preds, P{Op: opRaw, raw: p})
	}
}

// Filter returns the mutation as the target of privacy filter rules.
func (m *Mutation) Filter() privacy.Filter { return m }

// HasColumn reports whether the table of the entity has the column.
func (m *Mutation) HasColumn(column string) bool { return m.c.M.col(column) >= 0 }

// mutate runs exec for the mutation, inside the policy of the entity and the
// hooks of the schema and of the database handle.
//
// The policy runs before the hooks, so that a denied request reaches none of
// them, and again after them when there are any, so that it sees what they
// changed.
func (m *MutCore) mutate(ctx context.Context, c *conn, op velox.Op, exec func(context.Context, *MutCore) (any, error)) (any, error) {
	meta := m.M
	if err := meta.ready(); err != nil {
		return nil, err
	}
	// Before the policy and the hooks: a mutation that cannot be built must
	// not reach either.
	if err := invalid(m.Sets); err != nil {
		return nil, err
	}
	hooks := c.hooks()
	if meta.Policy == nil && len(hooks) == 0 && len(meta.Hooks) == 0 {
		return exec(ctx, m)
	}
	// The policy and the hooks work on a copy: what they add belongs to this
	// execution, not to the builder, which may run again. They see the
	// mutation as it will be written: a to-one key set through its edge is a
	// write of the key's field, and defaults are already in.
	own := &MutCore{M: meta, Ex: m.Ex, Sets: withDefaults(meta, op, keyWrites(meta, m.Sets)), Preds: slices.Clone(m.Preds)}
	mut := &Mutation{op: op, c: own, ex: m.Ex}
	if meta.Policy != nil {
		if err := meta.Policy.EvalMutation(ctx, mut); err != nil {
			return nil, err
		}
	}
	// The handle's hooks outermost, then the schema's.
	all := make([]velox.Hook, 0, len(hooks)+len(meta.Hooks))
	all = append(append(all, hooks...), meta.Hooks...)
	var chain velox.Mutator = velox.MutateFunc(func(ctx context.Context, vm velox.Mutation) (velox.Value, error) {
		mm, ok := vm.(*Mutation)
		if !ok {
			return nil, fmt.Errorf("orm: a hook passed on a %T instead of the mutation it received", vm)
		}
		if len(all) > 0 && meta.Policy != nil {
			if err := meta.Policy.EvalMutation(ctx, mm); err != nil {
				return nil, err
			}
		}
		return exec(ctx, mm.c)
	})
	for i := len(all) - 1; i >= 0; i-- {
		chain = all[i](chain)
	}
	return chain.Mutate(ctx, mut)
}

// keyWrites returns the setters with every change of a to-one edge whose key
// is a schema field turned into a write of that field. The two spellings
// (post.Author.Set(id), post.AuthorID.Set(id)) are one change, and a hook or a
// policy rule that reads the field must see it whichever was used.
func keyWrites(meta *Meta, sets []S) []S {
	out := make([]S, 0, len(sets)+len(meta.Fields))
	for _, s := range sets {
		if s.Kind == 0 {
			continue // a setter that sets nothing (SetNillable of a nil pointer)
		}
		if e := s.Edge; e != nil && e.fkOnOwner() && meta.Field(e.Columns[0]) != nil {
			switch {
			case s.Kind == EdgeAdd && len(s.IDs) > 0:
				s = S{Kind: SetValue, Col: e.Columns[0], Val: s.IDs[0]}
			case s.Kind == EdgeClear:
				s = S{Kind: SetNull, Col: e.Columns[0]}
			case s.Kind == EdgeAdd:
				continue // adding nobody
			}
		}
		out = append(out, s)
	}
	return out
}

// withDefaults adds the schema defaults a create would apply, and the update
// defaults an update would, for the columns the caller left alone.
func withDefaults(meta *Meta, op velox.Op, sets []S) []S {
	touched := func(col string) bool {
		return slices.ContainsFunc(sets, func(s S) bool {
			return s.Edge == nil && s.Col == col || s.Edge != nil && s.Edge.fkOnOwner() && s.Edge.Columns[0] == col
		})
	}
	for _, f := range meta.Fields {
		switch {
		case op.Is(velox.OpCreate) && f.Default != nil && !touched(f.Column):
			sets = append(sets, S{Kind: SetValue, Col: f.Column, Val: fresh(f, f.Default())})
		case op.Is(velox.OpUpdate|velox.OpUpdateOne) && f.UpdateDefault != nil && !touched(f.Column):
			sets = append(sets, S{Kind: SetValue, Col: f.Column, Val: f.UpdateDefault()})
		}
	}
	return sets
}
