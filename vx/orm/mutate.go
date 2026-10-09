package orm

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
	"github.com/syssam/velox/schema/field"
)

// MutCore is the mutation of any entity. Generated per-entity create, update
// and delete types embed one.
type MutCore struct {
	M *Meta
	// Ex is what the mutation runs on: the database handle or transaction
	// it was built from.
	Ex    Executor
	Sets  []S
	Preds []P
}

func wrapErr(err error) error {
	if err == nil || !sqlgraph.IsConstraintError(err) {
		return err
	}
	c := &ConstraintError{Err: err, Constraint: constraintOf(err)}
	switch {
	case sqlgraph.IsUniqueConstraintError(err):
		c.Kind = ConstraintUnique
	case sqlgraph.IsForeignKeyConstraintError(err):
		c.Kind = ConstraintForeignKey
	case sqlgraph.IsCheckConstraintError(err):
		c.Kind = ConstraintCheck
	}
	return c
}

// validate runs the field's validator and enum check on v.
func validate(m *Meta, f *FieldMeta, v any) error {
	if v == nil {
		return nil
	}
	if len(f.Enums) > 0 {
		if s := fmt.Sprint(v); !slices.Contains(f.Enums, s) {
			return &ValidationError{Entity: m.Name, Field: f.Name, Err: fmt.Errorf("invalid enum value %q", s)}
		}
	}
	if f.Validate != nil {
		if err := f.Validate(v); err != nil {
			return &ValidationError{Entity: m.Name, Field: f.Name, Err: err}
		}
	}
	return nil
}

// edgeSpec renders an edge change for the SQL layer.
func edgeSpec(e *EdgeMeta, ids []any) *sqlgraph.EdgeSpec {
	spec := &sqlgraph.EdgeSpec{
		Rel:     sqlgraphRel(e.Rel),
		Inverse: e.Inverse,
		Table:   e.Table,
		Columns: e.Columns,
		Bidi:    e.Bidi,
		Target:  &sqlgraph.EdgeTarget{IDSpec: sqlgraph.NewFieldSpec(e.Target.id().Column, e.Target.id().Type)},
	}
	for _, id := range ids {
		spec.Target.Nodes = append(spec.Target.Nodes, id)
	}
	// A join table with an entity of its own has columns besides the two
	// keys; a join row gets their defaults like any other row of that entity.
	if e.Through != nil && len(ids) > 0 {
		for _, f := range e.Through.Fields {
			if f.Default != nil && f != e.Through.id() {
				spec.Target.Fields = append(spec.Target.Fields, &sqlgraph.FieldSpec{Column: f.Column, Type: f.Type, Value: bytesValue(f, fresh(f, f.Default()))})
			}
		}
	}
	return spec
}

// fresh returns a default value the new row can own: a slice or map default
// is one value in the schema, and every row would otherwise share it.
func fresh(f *FieldMeta, v any) any {
	if f.Type != field.TypeJSON && f.Type != field.TypeBytes {
		return v
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		c := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		reflect.Copy(c, rv)
		return c.Interface()
	case reflect.Map:
		if rv.IsNil() {
			return v
		}
		c := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		for it := rv.MapRange(); it.Next(); {
			c.SetMapIndex(it.Key(), it.Value())
		}
		return c.Interface()
	}
	return v
}

// invalid returns the error of the first setter that could not be built.
func invalid(sets []S) error {
	for i := range sets {
		if sets[i].Kind == setInvalid {
			return sets[i].Val.(error) //nolint:errcheck // set by Fill
		}
	}
	return nil
}

// createSpec validates the setters of one row and renders its INSERT. It
// returns the column values written, for building the entity.
//
// Setters apply in order and the last one for a column wins, whether it names
// the column (post.AuthorID.To) or the to-one edge stored in it
// (post.Author.Set).
func createSpec(m *Meta, sets []S) (*sqlgraph.CreateSpec, map[string]any, error) {
	if m.id() == nil {
		return nil, nil, fmt.Errorf("orm: %s has no single-column id", m.Name)
	}
	if err := invalid(sets); err != nil {
		return nil, nil, err
	}
	vals := make(map[string]any, len(sets))
	var null map[string]bool
	var edges []S
	// dropEdge forgets an earlier setter of the to-one edge stored in column.
	dropEdge := func(column string) {
		edges = slices.DeleteFunc(edges, func(s S) bool { return s.Edge.fkOnOwner() && s.Edge.Columns[0] == column })
	}
	for _, s := range sets {
		switch s.Kind {
		case 0:
			// A setter that sets nothing (SetNillable of a nil pointer).
		case SetValue:
			if m.Field(s.Col) == nil {
				return nil, nil, fmt.Errorf("orm: creating %s: unknown column %q", m.Name, s.Col)
			}
			vals[s.Col] = s.Val
			delete(null, s.Col)
			dropEdge(s.Col)
		case SetNull:
			if m.Field(s.Col) == nil {
				return nil, nil, fmt.Errorf("orm: creating %s: unknown column %q", m.Name, s.Col)
			}
			delete(vals, s.Col)
			if null == nil {
				null = make(map[string]bool)
			}
			null[s.Col] = true
			dropEdge(s.Col)
		case EdgeAdd:
			// Adding no neighbors is nothing to do, not an empty INSERT.
			if len(s.IDs) == 0 {
				continue
			}
			e := s.Edge
			if e.Rel == M2O || e.Rel == O2O {
				// A to-one edge holds one neighbor: a second Set replaces the first.
				edges = slices.DeleteFunc(edges, func(o S) bool { return o.Edge == e })
			}
			if e.fkOnOwner() {
				col := e.Columns[0]
				delete(vals, col)
				delete(null, col)
				if m.Field(col) != nil {
					// The key is a schema field: write it as one.
					vals[col] = s.IDs[0]
					continue
				}
			}
			edges = append(edges, s)
		case SetKeep:
			return nil, nil, fmt.Errorf("orm: creating %s: column %q is kept, which only an update can do: a new row has nothing to keep", m.Name, s.Col)
		default:
			return nil, nil, fmt.Errorf("orm: creating %s: setter kind %d is not valid on create", m.Name, s.Kind)
		}
	}
	spec := sqlgraph.NewCreateSpec(m.Table, sqlgraph.NewFieldSpec(m.id().Column, m.id().Type))
	for _, f := range m.Fields {
		v, ok := vals[f.Column]
		if !ok && null[f.Column] {
			if !f.Optional || f.NotNull {
				return nil, nil, &ValidationError{Entity: m.Name, Field: f.Name, Err: errors.New("the field cannot be NULL")}
			}
			// An explicit NULL, so that a column DEFAULT does not fill it. A
			// JSON column is left out instead: the SQL layer would encode a
			// nil value as the JSON text null.
			if f.Type != field.TypeJSON {
				spec.SetField(f.Column, f.Type, nil)
			}
			continue
		}
		if !ok && f.Default != nil {
			v, ok = fresh(f, f.Default()), true
			vals[f.Column] = v
		}
		if !ok {
			switch {
			case f == m.id() && f.Optional:
				// The database assigns the id.
			case f.Optional && f.NotNull && zeroFilled(f.Type):
				// An optional field whose column is NOT NULL: an unset one is
				// stored as the zero value, which is not validated because
				// the caller did not provide it.
				spec.SetField(f.Column, f.Type, m.cols[m.col(f.Column)].zero)
			case f.Optional:
			case writtenByEdge(edges, f.Column):
				// A required column may be written by an edge setter.
			default:
				return nil, nil, &ValidationError{Entity: m.Name, Field: f.Name, Err: errors.New("missing required field")}
			}
			continue
		}
		if err := validate(m, f, v); err != nil {
			return nil, nil, err
		}
		v = bytesValue(f, v)
		vals[f.Column] = v // what is stored, so that the entity returned equals the row
		if rv := reflect.ValueOf(v); f.Type == field.TypeBytes && rv.Kind() == reflect.Slice && rv.IsNil() {
			// NULL in a nullable column: the entity's field stays nil too.
			delete(vals, f.Column)
		}
		if f == m.id() {
			spec.ID.Value = v
			continue
		}
		spec.SetField(f.Column, f.Type, v)
	}
	// A required to-one edge whose key is not a schema field has no field to
	// be found missing by the loop above: the edge is.
	for _, e := range m.Edges {
		if e.Required && e.fkOnOwner() && m.Field(e.Columns[0]) == nil &&
			!slices.ContainsFunc(edges, func(s S) bool { return s.Edge == e }) {
			return nil, nil, &ValidationError{Entity: m.Name, Field: e.Name, Err: errors.New("missing required edge")}
		}
	}
	for _, s := range edges {
		spec.Edges = append(spec.Edges, edgeSpec(s.Edge, s.IDs))
		if s.Edge.fkOnOwner() {
			vals[s.Edge.Columns[0]] = s.IDs[0]
		}
	}
	return spec, vals, nil
}

// bytesValue returns v as it is stored.
//
// A nil byte slice would be sent as NULL; in a column that does not accept
// NULL it is the empty value.
//
// A time is written as stored (see stored), so that the entity a create
// returns, a cursor made of it and an optimistic-lock guard on it equal what
// is read back.
func bytesValue(f *FieldMeta, v any) any {
	switch t := v.(type) {
	case time.Time:
		return stored(t)
	case *time.Time:
		if t != nil {
			at := stored(*t)
			return &at
		}
	}
	if f.Type != field.TypeBytes || !f.NotNull {
		return v
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	return v
}

// stored returns t as a database keeps it: without its monotonic clock
// reading, and to the microsecond.
//
// A driver that stores times as text (SQLite) writes time.Time's String
// form, which for the result of time.Now() ends in "m=+0.0123": a value that
// nothing read back can equal. Postgres keeps microseconds, so a time with
// nanoseconds is another value once it has been stored.
func stored(t time.Time) time.Time { return t.Round(0).Truncate(time.Microsecond) }

// zeroFilled reports whether an unset optional column of type t is written as
// its zero value. An enum is not: the empty string is not one of its values,
// and the schema must give it a default.
func zeroFilled(t field.Type) bool { return t.IsStandardType() || t == field.TypeOther }

func writtenByEdge(edges []S, column string) bool {
	for _, s := range edges {
		if s.Edge.fkOnOwner() && s.Edge.Columns[0] == column {
			return true
		}
	}
	return false
}

// built returns the entity of a row just inserted.
func built(m *Meta, spec *sqlgraph.CreateSpec, vals map[string]any) (any, error) {
	e := m.New()
	for col, v := range vals {
		if col != m.id().Column {
			if err := m.assign(e, col, v); err != nil {
				return nil, err
			}
		}
	}
	if spec.ID.Value != nil {
		if err := assignScanned(m.Ptr(e, m.col(m.id().Column)), spec.ID.Value); err != nil {
			return nil, fmt.Errorf("orm: reading the id of the new %s: %w", m.Name, err)
		}
	}
	return e, nil
}

// assignScanned stores a driver value, or an id the caller set, into the
// address of a field.
func assignScanned(dest, v any) error {
	switch d := dest.(type) {
	case *int:
		if n, ok := v.(int64); ok {
			*d = int(n)
			return nil
		}
	case *int64:
		if n, ok := v.(int64); ok {
			*d = n
			return nil
		}
	case *string:
		if s, ok := v.(string); ok {
			*d = s
			return nil
		}
	}
	// A value of the field's own type (a uuid.UUID the caller or a default
	// produced) is stored as it is, before any Scanner is asked to parse it.
	if dv, vv := reflect.ValueOf(dest), reflect.ValueOf(v); dv.Kind() == reflect.Pointer && vv.IsValid() && vv.Type().AssignableTo(dv.Type().Elem()) {
		dv.Elem().Set(vv)
		return nil
	}
	if d, ok := dest.(stdsql.Scanner); ok {
		return d.Scan(v)
	}
	// Any other pairing goes through Go's conversion rules.
	return convertAssign(dest, v)
}

// Insert creates one row and returns its entity.
//
//go:noinline
func (m *MutCore) Insert(ctx context.Context) (any, error) {
	c, err := connOf(m.Ex, m.M)
	if err != nil {
		return nil, err
	}
	v, err := m.mutate(ctx, c, velox.OpCreate, func(ctx context.Context, m *MutCore) (any, error) {
		return m.insert(ctx, c)
	})
	return m.M.entity(v, err)
}

func (m *MutCore) insert(ctx context.Context, c *conn) (any, error) {
	spec, vals, err := createSpec(m.M, m.Sets)
	if err != nil {
		return nil, err
	}
	if err := sqlgraph.CreateNode(ctx, c.atomic(), spec); err != nil {
		return nil, wrapErr(err)
	}
	return built(m.M, spec, vals)
}

// InsertBulk creates one row per setter list with a single statement and
// returns the entities in the same order.
//
// With hooks or a policy every row is a mutation of its own: each row's hooks
// wrap the next row's, and the innermost runs the one statement. So all hooks
// see their row before the insert, and return, last row first, after it.
//
//go:noinline
func InsertBulk(ctx context.Context, ex Executor, m *Meta, rows [][]S) ([]any, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	if err := m.ready(); err != nil {
		return nil, err
	}
	c, err := connOf(ex, m)
	if err != nil {
		return nil, err
	}
	if m.Policy == nil && len(c.hooks()) == 0 && len(m.Hooks) == 0 {
		return insertBulk(ctx, c, m, rows)
	}
	final := make([][]S, len(rows))
	var out []any
	var run func(ctx context.Context, i int) (any, error)
	run = func(ctx context.Context, i int) (any, error) {
		row := &MutCore{M: m, Ex: ex, Sets: rows[i]}
		return row.mutate(ctx, c, velox.OpCreate, func(ctx context.Context, mc *MutCore) (any, error) {
			final[i] = mc.Sets
			if i+1 < len(rows) {
				if _, err := run(ctx, i+1); err != nil {
					return nil, err
				}
			} else {
				var err error
				if out, err = insertBulk(ctx, c, m, final); err != nil {
					return nil, err
				}
			}
			if i >= len(out) {
				return nil, fmt.Errorf("orm: a hook of %s ended the bulk create before row %d", m.Name, i)
			}
			return out[i], nil
		})
	}
	if _, err := run(ctx, 0); err != nil {
		return nil, err
	}
	// A hook that returns without calling the next one stops every row after
	// it, its own included: that is an error, not an empty result.
	if len(out) != len(rows) {
		return nil, fmt.Errorf("orm: a hook of %s returned without running the bulk create", m.Name)
	}
	return out, nil
}

// entity checks that what a hook chain returned for a create or an update of
// one row is the entity. A hook that returns without calling the next one
// yields something else.
func (m *Meta) entity(v any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	m.prepare()
	if v == nil || reflect.TypeOf(v) != m.typ {
		return nil, fmt.Errorf("orm: a hook of %s returned %T instead of running the mutation", m.Name, v)
	}
	return v, nil
}

func insertBulk(ctx context.Context, c *conn, m *Meta, rows [][]S) ([]any, error) {
	batch := &sqlgraph.BatchCreateSpec{Nodes: make([]*sqlgraph.CreateSpec, len(rows))}
	vals := make([]map[string]any, len(rows))
	for i, sets := range rows {
		spec, v, err := createSpec(m, sets)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		batch.Nodes[i], vals[i] = spec, v
	}
	// A statement without columns is INSERT ... DEFAULT VALUES, which inserts
	// one row however many were asked for; such rows go one at a time.
	if !slices.ContainsFunc(batch.Nodes, func(n *sqlgraph.CreateSpec) bool { return len(n.Fields) > 0 || n.ID.Value != nil }) {
		for _, spec := range batch.Nodes {
			if err := sqlgraph.CreateNode(ctx, c.atomic(), spec); err != nil {
				return nil, wrapErr(err)
			}
		}
	} else if err := sqlgraph.BatchCreate(ctx, c.atomic(), batch); err != nil {
		return nil, wrapErr(err)
	}
	out := make([]any, len(rows))
	for i, spec := range batch.Nodes {
		e, err := built(m, spec, vals[i])
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

// conditions returns the error of using the zero predicate in a mutation. In
// a query it is "no condition", which is what code that builds a predicate
// only under some circumstance wants. In an update or delete it would turn a
// forgotten assignment into a statement over every row, so it is refused: a
// mutation of every row is written without Where.
func (m *MutCore) conditions() error {
	var bad func(ps []P) bool
	bad = func(ps []P) bool {
		for i := range ps {
			if ps[i].Op == 0 || (ps[i].Op == OpAnd || ps[i].Op == OpOr || ps[i].Op == OpNot || ps[i].Op == OpHasEdge) && bad(ps[i].Subs) {
				return true
			}
		}
		return false
	}
	if bad(m.Preds) {
		return fmt.Errorf("orm: a condition of the %s mutation is the zero predicate; to change every row, leave Where out", m.M.Name)
	}
	return nil
}

// updateSpec validates the setters and renders the UPDATE.
func (m *MutCore) updateSpec() (*sqlgraph.UpdateSpec, error) {
	meta := m.M
	if err := m.conditions(); err != nil {
		return nil, err
	}
	var id []*sqlgraph.FieldSpec
	if meta.id() != nil {
		id = append(id, sqlgraph.NewFieldSpec(meta.id().Column, meta.id().Type))
	}
	spec := sqlgraph.NewUpdateSpec(meta.Table, meta.Columns, id...)
	if err := invalid(m.Sets); err != nil {
		return nil, err
	}
	touched := make(map[string]bool, len(m.Sets))
	sets, err := m.orderedSets()
	if err != nil {
		return nil, err
	}
	for _, s := range sets {
		switch s.Kind {
		case SetKeep:
			if meta.Field(s.Col) == nil {
				return nil, fmt.Errorf("orm: updating %s: unknown column %q", meta.Name, s.Col)
			}
			touched[s.Col] = true
		case SetValue, SetNull, SetAdd:
			f := meta.Field(s.Col)
			if f == nil {
				return nil, fmt.Errorf("orm: updating %s: unknown column %q", meta.Name, s.Col)
			}
			if f == meta.id() {
				return nil, &ValidationError{Entity: meta.Name, Field: f.Name, Err: errors.New("the id cannot be changed")}
			}
			if f.Immutable {
				return nil, &ValidationError{Entity: meta.Name, Field: f.Name, Err: errors.New("field is immutable")}
			}
			touched[s.Col] = true
			switch s.Kind {
			case SetValue:
				if err := validate(meta, f, s.Val); err != nil {
					return nil, err
				}
				spec.SetField(f.Column, f.Type, bytesValue(f, s.Val))
			case SetAdd:
				spec.AddField(f.Column, f.Type, s.Val)
			default:
				if f.NotNull || !f.Optional {
					return nil, &ValidationError{Entity: meta.Name, Field: f.Name, Err: errors.New("the field cannot be NULL")}
				}
				spec.ClearField(f.Column, f.Type)
			}
		}
		switch s.Kind {
		case EdgeAdd:
			if len(s.IDs) > 0 {
				spec.Edges.Add = append(spec.Edges.Add, edgeSpec(s.Edge, s.IDs))
			}
		case EdgeRemove:
			// An edge spec without ids clears the whole edge; removing no
			// neighbors must not.
			if len(s.IDs) > 0 {
				spec.Edges.Clear = append(spec.Edges.Clear, edgeSpec(s.Edge, s.IDs))
			}
		case EdgeClear:
			spec.Edges.Clear = append(spec.Edges.Clear, edgeSpec(s.Edge, nil))
		}
	}
	for _, f := range meta.Fields {
		if f.UpdateDefault != nil && !touched[f.Column] {
			spec.SetField(f.Column, f.Type, bytesValue(f, f.UpdateDefault()))
		}
	}
	if len(m.Preds) > 0 {
		preds := make([]func(*sql.Selector), len(m.Preds))
		for i := range m.Preds {
			preds[i] = compile(&m.Preds[i])
		}
		spec.Predicate = func(s *sql.Selector) {
			for _, p := range preds {
				p(s)
			}
		}
	}
	return spec, nil
}

// orderedSets returns the setters of an update as the SQL layer can apply
// them with the meaning "in order":
//
//   - a change of a to-one edge whose key is a schema field is a write of that
//     field, and only the last write of a column is kept;
//   - the changes of every other edge are folded: Clear forgets what came
//     before it, Add and Remove of the same id cancel, and a to-one edge keeps
//     its last neighbor. The SQL layer always clears before it adds, so what
//     is handed to it must already be the net change.
func (m *MutCore) orderedSets() ([]S, error) {
	meta := m.M
	type change struct {
		e           *EdgeMeta
		cleared     bool
		add, remove []any
	}
	var changes []*change
	of := func(e *EdgeMeta) *change {
		for _, c := range changes {
			if c.e == e {
				return c
			}
		}
		c := &change{e: e}
		changes = append(changes, c)
		return c
	}
	without := func(ids, drop []any) []any {
		return slices.DeleteFunc(ids, func(id any) bool {
			return slices.ContainsFunc(drop, func(d any) bool { return key(d) == key(id) })
		})
	}
	out := make([]S, 0, len(m.Sets))
	for _, s := range keyWrites(meta, m.Sets) {
		if s.Edge == nil {
			if s.Kind == SetValue || s.Kind == SetNull || s.Kind == SetKeep {
				out = slices.DeleteFunc(out, func(o S) bool { return o.Col == s.Col })
			}
			out = append(out, s)
			continue
		}
		e := s.Edge
		if e.Immutable {
			return nil, &ValidationError{Entity: meta.Name, Field: e.Name, Err: errors.New("edge is immutable")}
		}
		c := of(e)
		switch s.Kind {
		case EdgeClear:
			c.cleared, c.add, c.remove = true, nil, nil
		case EdgeAdd:
			c.remove = without(c.remove, s.IDs)
			c.add = append(without(c.add, s.IDs), s.IDs...)
			if e.Rel == M2O || e.Rel == O2O {
				c.add = c.add[len(c.add)-1:]
			}
		case EdgeRemove:
			c.add = without(c.add, s.IDs)
			if !c.cleared {
				c.remove = append(without(c.remove, s.IDs), s.IDs...)
			}
		}
	}
	for _, c := range changes {
		toOne := c.e.Rel == M2O || c.e.Rel == O2O
		switch {
		case c.cleared && len(c.add) > 0 && toOne:
			// Setting a to-one edge replaces its neighbor; a clear before
			// it would write the same column twice.
		case c.cleared:
			if c.e.Required && len(c.add) == 0 {
				return nil, &ValidationError{Entity: meta.Name, Field: c.e.Name, Err: errors.New("a required edge cannot be cleared")}
			}
			out = append(out, S{Kind: EdgeClear, Edge: c.e})
		case len(c.remove) > 0:
			out = append(out, S{Kind: EdgeRemove, Edge: c.e, IDs: c.remove})
		}
		if len(c.add) > 0 {
			out = append(out, S{Kind: EdgeAdd, Edge: c.e, IDs: c.add})
		}
	}
	return out, nil
}

// Update updates every row the predicates match and returns how many.
//
//go:noinline
func (m *MutCore) Update(ctx context.Context) (int, error) {
	c, err := connOf(m.Ex, m.M)
	if err != nil {
		return 0, err
	}
	v, err := m.mutate(ctx, c, velox.OpUpdate, func(ctx context.Context, m *MutCore) (any, error) {
		return m.update(ctx, c)
	})
	n, _ := v.(int)
	return n, err
}

func (m *MutCore) update(ctx context.Context, c *conn) (int, error) {
	spec, err := m.updateSpec()
	if err != nil {
		return 0, err
	}
	if len(spec.Fields.Set)+len(spec.Fields.Add)+len(spec.Fields.Clear)+len(spec.Edges.Add)+len(spec.Edges.Clear) == 0 {
		// Nothing to write (an empty patch, a Keep and no more): no
		// statement runs, and the count is of the rows the conditions
		// match, as an UPDATE of them would have reported. Zero would say
		// that no row matched, which orm.One turns into ErrNotFound.
		matched, cerr := (&QueryCore{M: m.M, Preds: m.Preds}).count(ctx, c)
		return matched, wrapErr(cerr)
	}
	n, err := sqlgraph.UpdateNodes(ctx, c.atomic(), spec)
	return n, wrapErr(err)
}

// UpdateOne updates the single row the predicates match and returns its
// entity: ErrNotFound for no row, ErrNotSingular for more than one, in which
// case nothing is written.
//
//go:noinline
func (m *MutCore) UpdateOne(ctx context.Context) (any, error) {
	c, err := connOf(m.Ex, m.M)
	if err != nil {
		return nil, err
	}
	v, err := m.mutate(ctx, c, velox.OpUpdateOne, func(ctx context.Context, m *MutCore) (any, error) {
		return m.updateOne(ctx, c)
	})
	return m.M.entity(v, err)
}

// updateOne finds, updates and reads back the row. Its reads are the
// mutation's own, under the mutation's conditions: no query policy or
// interceptor applies to them.
func (m *MutCore) updateOne(ctx context.Context, c *conn) (any, error) {
	meta := m.M
	if meta.id() == nil {
		return nil, fmt.Errorf("orm: %s has no single-column id", meta.Name)
	}
	// The usual case names the row by its id, possibly with further
	// conditions (a policy's tenant filter, an optimistic-lock guard). The
	// SQL layer then updates WHERE id = ? AND <the rest> in one statement,
	// reports a row that is missing or fails a condition as not found, and
	// reads the row back.
	for i := range m.Preds {
		if p := &m.Preds[i]; p.Op == OpEQ && p.Col == meta.id().Column {
			rest := append(append([]P(nil), m.Preds[:i]...), m.Preds[i+1:]...)
			return (&MutCore{M: meta, Sets: m.Sets, Preds: rest}).updateByID(ctx, c, p.Val)
		}
	}
	probe := &QueryCore{M: meta, Preds: m.Preds, Lim: 2}
	ids, err := probe.ids(ctx, c)
	if err != nil {
		return nil, err
	}
	switch len(ids) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, meta.Name)
	case 1:
	default:
		return nil, fmt.Errorf("%w: %s", ErrNotSingular, meta.Name)
	}
	// The conditions go with the id: a row another transaction changed
	// between the probe and here is then not updated, and not reported as if
	// it had been.
	return m.updateByID(ctx, c, ids[0])
}

// updateByID updates the row with the given id, if it also satisfies the
// mutation's conditions, and returns its entity; ErrNotFound otherwise.
func (m *MutCore) updateByID(ctx context.Context, c *conn, id any) (any, error) {
	meta := m.M
	spec, err := m.updateSpec()
	if err != nil {
		return nil, err
	}
	spec.Node.ID.Value = id
	e := meta.New()
	var slots []slot
	spec.ScanValues = func(columns []string) ([]any, error) {
		dest := make([]any, len(columns))
		slots = make([]slot, len(columns))
		for i, col := range columns {
			if at := meta.col(col); at >= 0 {
				dest[i] = meta.dest(e, at, &slots[i])
			} else {
				dest[i] = discard{}
			}
		}
		return dest, nil
	}
	spec.Assign = func([]string, []any) error { return nil }
	// UpdateNode always asks for a transaction (it writes, then reads the row
	// back). Only a change to edges makes it more than one write.
	drv := c.drv
	if len(spec.Edges.Add)+len(spec.Edges.Clear) > 0 {
		drv = c.atomic()
	}
	if err := sqlgraph.UpdateNode(ctx, drv, spec); err != nil {
		var nf *sqlgraph.NotFoundError
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, meta.Name)
		}
		return nil, wrapErr(err)
	}
	return e, nil
}

// Delete deletes every row the predicates match and returns how many.
//
//go:noinline
func (m *MutCore) Delete(ctx context.Context) (int, error) {
	c, err := connOf(m.Ex, m.M)
	if err != nil {
		return 0, err
	}
	v, err := m.mutate(ctx, c, velox.OpDelete, func(ctx context.Context, m *MutCore) (any, error) {
		return m.delete(ctx, c)
	})
	n, _ := v.(int)
	return n, err
}

func (m *MutCore) delete(ctx context.Context, c *conn) (int, error) {
	meta := m.M
	if err := m.conditions(); err != nil {
		return 0, err
	}
	var id *sqlgraph.FieldSpec
	if meta.id() != nil {
		id = sqlgraph.NewFieldSpec(meta.id().Column, meta.id().Type)
	}
	spec := sqlgraph.NewDeleteSpec(meta.Table, id)
	if len(m.Preds) > 0 {
		preds := make([]func(*sql.Selector), len(m.Preds))
		for i := range m.Preds {
			preds[i] = compile(&m.Preds[i])
		}
		spec.Predicate = func(s *sql.Selector) {
			for _, p := range preds {
				p(s)
			}
		}
	}
	n, err := sqlgraph.DeleteNodes(ctx, c.drv, spec)
	return n, wrapErr(err)
}
