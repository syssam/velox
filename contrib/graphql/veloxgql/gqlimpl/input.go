package gqlimpl

import (
	"context"
	"slices"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Mutation is a mutation input being turned into a write: a Create or an
// Update. The helpers that take one record their first error in it and the
// statements they need, which Create.Insert and Update.Exec run. Only
// *Create and *Update implement it.
//
// A helper records an error of two kinds. A decode error (Key, Keys,
// AddRemove, DecodeJSON) is a malformed or mistyped ID, a list over MaxPage or
// a JSON value of the wrong shape; any other error (NotZero, NotNull, Cleared,
// ClearEdge, an ID both to add and to remove of AddRemove) is an input with no
// single meaning. Insert and Exec
// return the first decode error, else the first other one, else the field
// validators' error, before any statement: so the order the helpers are
// called in, within each kind, is the order their errors win in.
type Mutation interface {
	state() *inputState
}

// inputState is what a Create and an Update share: the input, its errors,
// and the steps of the write's transaction.
type inputState struct {
	ctx    context.Context
	input  string                      // the input's GraphQL type: "CreatePostInput"
	typ    string                      // the entity's GraphQL type: "Post"
	entity string                      // the entity as velox names it: "gen.Post"
	name   func(goField string) string // the input's field of an entity field
	decode error                       // the first decode error
	err    error                       // the first other error
	before []step                      // the steps before the row's write
	after  []step                      // the steps after it
}

// step is a statement of a write, in its transaction.
type step func(ctx context.Context, db *velox.DB) error

// at is the input's field as errors name it: "CreatePostInput.authorID".
func (s *inputState) at(field string) string { return s.input + "." + field }

// decodeFail records err as a decode error, unless there is one.
func (s *inputState) decodeFail(err error) {
	if err != nil && s.decode == nil {
		s.decode = err
	}
}

// fail records err as an error of the input, unless there is one.
func (s *inputState) fail(err error) {
	if err != nil && s.err == nil {
		s.err = err
	}
}

// failed is the error the write returns before any statement: the first
// decode error, else the first other one.
func (s *inputState) failed() error {
	if s.decode != nil {
		return s.decode
	}
	return s.err
}

// writeError is WriteError of err, a validator's or the write's.
func (s *inputState) writeError(err error) error {
	return WriteError(err, s.input, s.entity, s.name)
}

// Create is a create input being inserted: the row x of the entity of t,
// with the input's fields set on it, and the statements of its edges. None of
// it is generic over the entity: it is compiled once, for every input. A
// create input's generated method Create makes one with NewCreate, sets each
// field the input gives on x, adds its edges with one helper call each, and
// runs Insert:
//
//	func (in *CreatePostInput) Create(ctx context.Context, x *Post) (*Post, error) {
//		if x == nil {
//			x = new(Post)
//		}
//		c := gqlimpl.NewCreate(ctx, "CreatePostInput", "Post", "gen.Post", velox.TableOf[Post](), unsafe.Pointer(x), postInputGQL, postColTitle)
//		x.Title = in.Title
//		if in.Views != nil {
//			x.Views = *in.Views
//			c.Given(postColViews)
//		}
//		x.AuthorID = gqlimpl.Key[int64](c, "authorID", in.AuthorID, "User")
//		gqlimpl.Exists(c, "authorID", "User", velox.TableOf[User](), userColID, []int64{x.AuthorID})
//		tagIDsKeys := gqlimpl.Keys[int64](c, "tagIDs", in.TagIDs, "Label")
//		gqlimpl.Join(c, "tagIDs", "Label", postTagsEdgeGQL(), tagIDsKeys)
//		if err := c.Insert(); err != nil {
//			return nil, err
//		}
//		return x, nil
//	}
//
// where postTagsEdgeGQL returns the edge's M2M, which Update's Through takes
// too.
type Create struct {
	s    inputState
	t    velox.Table
	x    unsafe.Pointer
	cols []int // the columns the input sets, validated before any statement
}

func (c *Create) state() *inputState { return &c.s }

// NewCreate returns the Create of x, a row of t's entity, from the create
// input of the GraphQL type input ("CreatePostInput"). typ is the entity's
// GraphQL type ("Post"), which a NOT_FOUND or "already linked" error names,
// entity is the entity as velox names it in a *velox.ValidationError
// ("gen.Post"), and name gives the input's field that sets an entity field,
// by the field's Go name, "" for one the input does not set: a validator's
// error on such a field becomes BAD_USER_INPUT naming it (WriteError).
// required are the columns of the input's required fields, whose validators
// run before any statement; Given adds those of the optional fields the input
// gives.
func NewCreate(ctx context.Context, input, typ, entity string, t velox.Table, x unsafe.Pointer, name func(goField string) string, required ...int) *Create {
	return &Create{s: inputState{ctx: ctx, input: input, typ: typ, entity: entity, name: name}, t: t, x: x, cols: required}
}

// Given adds col, the column of an optional field the input gives, to those
// validated before any statement.
func (c *Create) Given(col int) { c.cols = append(c.cols, col) }

// Insert inserts the row, and writes its edges.
//
// It fails, before any statement, with the first error a helper recorded
// (see Mutation), and then with a validator's error on a column the input
// sets (velox.Table.Validate, through WriteError). Then, atomically on the DB
// veloxgql.DBFromContext reads from ctx (Atomic), it runs each check the
// helpers registered before the insert, in their order (the IDs of the edges
// whose key is on the row, and of the many-to-many edges, must name rows the
// caller may read: Exists and Join), inserts x under the policies, and then
// writes each edge whose key is on the target's rows (Repoint) and each
// join row (Join), in their order. An error of any of them rolls the whole
// create back. x is changed even when Insert fails: its key, the keys of its
// edges and whatever a hook sets on it.
func (c *Create) Insert() error {
	if err := c.s.failed(); err != nil {
		return err
	}
	if err := c.t.Validate(c.x, c.cols...); err != nil {
		return c.s.writeError(err)
	}
	return Atomic(c.s.ctx, func(ctx context.Context, db *velox.DB) error {
		for _, f := range c.s.before {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		if err := db.InsertTable(c.t, c.x).Exec(ctx); err != nil {
			return c.s.writeError(err)
		}
		for _, f := range c.s.after {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		return nil
	})
}

// Update is an update input being written: the assignments of its fields
// and of the keys on the row, of the entity of a velox.Table, and the
// statements of its edges. None of it is generic over the entity. An update
// input's generated method Update makes one with NewUpdate, adds the
// assignment of each field the input gives with Set, its edges with one
// helper call each, and returns Exec's row:
//
//	func (in *UpdatePostInput) Update(ctx context.Context, id string, set ...velox.Setter[Post]) (*Post, error) {
//		u, key := gqlimpl.NewUpdate[int64](ctx, "UpdatePostInput", "Post", "gen.Post", velox.TableOf[Post](), postColID, id, postInputGQL)
//		if in.Title != nil {
//			u.Set(postColTitle, *in.Title)
//		}
//		if gqlimpl.Cleared(u, "publishedAt", in.PublishedAt != nil, "clearPublishedAt", in.ClearPublishedAt) {
//			u.SetNull(postColPublishedAt)
//		}
//		addTagIDsKeys, removeTagIDsKeys := gqlimpl.AddRemove[int64](u, "Label", "addTagIDs", in.AddTagIDs, "removeTagIDs", in.RemoveTagIDs)
//		gqlimpl.Through(u, "addTagIDs", "Label", gqlimpl.Edit{Clear: in.ClearTags, Remove: removeTagIDsKeys, Add: addTagIDsKeys}, key, postTagsEdgeGQL())
//		p, err := u.Exec(key, velox.Assignments(set))
//		return (*Post)(p), err
//	}
type Update struct {
	s    inputState
	t    velox.Table
	key  int                // the key's column
	set  []velox.Assignment // the assignments of the input's fields
	keys []velox.Assignment // the assignments of the keys on the row
}

func (u *Update) state() *inputState { return &u.s }

// NewUpdate returns the Update of the row of t's entity whose ID is id, of
// the GraphQL type typ ("Post"), whose key is the column key, from the update
// input of the GraphQL type input ("UpdatePostInput"), and the row's key, of
// the Go type K, decoded from id; entity and name are NewCreate's. A
// malformed id, or an ID of another type, is a decode error, the first: its
// text names "id".
func NewUpdate[K any](ctx context.Context, input, typ, entity string, t velox.Table, key int, id string, name func(goField string) string) (*Update, K) {
	u := newUpdate(ctx, input, typ, entity, t, key, name)
	k, err := DecodeID[K]("id", id, typ)
	u.s.decodeFail(err)
	return u, k
}

// NewUpdateAs is NewUpdate of a key whose Go type V is a named type DecodeID
// does not take: id is decoded as a K and converted by conv.
func NewUpdateAs[K, V any](ctx context.Context, input, typ, entity string, t velox.Table, key int, id string, name func(goField string) string, conv func(K) V) (*Update, V) {
	u := newUpdate(ctx, input, typ, entity, t, key, name)
	v, err := DecodeIDAs("id", id, typ, conv)
	u.s.decodeFail(err)
	return u, v
}

func newUpdate(ctx context.Context, input, typ, entity string, t velox.Table, key int, name func(goField string) string) *Update {
	return &Update{s: inputState{ctx: ctx, input: input, typ: typ, entity: entity, name: name}, t: t, key: key}
}

// assign is the assignment of v, converted as velox.Table.Convert does, to
// column col: nil is NULL. An assignment the column cannot take is an error
// of u, which the generated code never makes.
func (u *Update) assign(col int, v any) (velox.Assignment, bool) {
	if v != nil {
		c, err := u.t.Convert(col, v)
		if err != nil {
			u.s.fail(err)
			return velox.Assignment{}, false
		}
		v = c
	}
	a, err := u.t.Assign(col, v)
	u.s.fail(err)
	return a, err == nil
}

// Set adds the assignment of v to column col, a field the input gives, to
// the update. Its validator runs before any statement.
func (u *Update) Set(col int, v any) {
	if a, ok := u.assign(col, v); ok {
		u.set = append(u.set, a)
	}
}

// SetNull adds the assignment of NULL to column col, a nullable field the
// input clears.
func (u *Update) SetNull(col int) { u.Set(col, nil) }

// SetKey adds the assignment of v, or NULL for nil, to column col, the key of
// an edge on the row, after the assignments of the fields and the
// application's: Exists checks the ID it sets.
func (u *Update) SetKey(col int, v any) {
	if a, ok := u.assign(col, v); ok {
		u.keys = append(u.keys, a)
	}
}

// Exec updates the row whose key is key, writes its edges, and returns the
// row as written, a pointer to a row of the entity. set are the
// application's own assignments (velox.Assignments of its setters), which
// count as setting something. An application's assignment of a column wins:
// the input's assignment of that column, a field's or an edge's key, is
// dropped, as an UPDATE assigns a column once.
//
// It fails, before any statement, with the first error a helper recorded
// (see Mutation), then with a validator's error on what the input's
// assignments that are kept assign (velox.Table.ValidateSet, through
// WriteError), and then with one on what set assigns, as velox returns it:
// an assignment of the application's that its validator refuses is the
// server's error, not the client's, and is not named as an input field. Then,
// atomically on the DB veloxgql.DBFromContext reads from ctx (Atomic):
//   - each check registered before the row's write, in order: the IDs the
//     keys on the row are set to must name rows the caller may read (Exists);
//   - the UPDATE of the row with every assignment, under the Write policy: a
//     row it does not change is NOT_FOUND "<input>: no <Type> has this ID".
//     When there is nothing to set, or the driver does not report the rows it
//     changed (-1), LockRow checks the row instead under the same scope, and
//     locks it where the dialect can: no edge of a row the caller may read but
//     not change is written;
//   - each edge, in order (Children, Child, Through);
//   - the row, read back without the Read policy, as the update wrote it: an
//     update that moves the row out of the caller's Read view succeeds.
//
// An error of any of them rolls the whole update back.
func (u *Update) Exec(key any, set []velox.Assignment) (unsafe.Pointer, error) {
	if err := u.s.failed(); err != nil {
		return nil, err
	}
	k, err := u.t.Convert(u.key, key)
	if err != nil {
		return nil, err
	}
	row := u.t.Pred(u.key, velox.PredEQ, k)
	fields, keys := unassigned(u.set, set), unassigned(u.keys, set)
	if err := u.t.ValidateSet(fields...); err != nil {
		return nil, u.s.writeError(err)
	}
	if err := u.t.ValidateSet(set...); err != nil {
		return nil, err
	}
	all := append(append(slices.Clip(fields), set...), keys...)
	var out unsafe.Pointer
	err = Atomic(u.s.ctx, func(ctx context.Context, db *velox.DB) error {
		for _, f := range u.s.before {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		counted := false
		if len(all) > 0 {
			n, err := db.UpdateTable(u.t).Assign(all...).Where(row).Exec(ctx)
			if err != nil {
				return u.s.writeError(err)
			}
			if n == 0 {
				return NotFound(u.s.input, u.s.typ)
			}
			counted = n > 0
		}
		if !counted {
			if err := LockRow(ctx, db, db.QueryTable(u.t).Where(row), u.s.input, u.s.typ); err != nil {
				return err
			}
		}
		for _, f := range u.s.after {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		x, err := db.QueryTable(u.t).Where(row).One(velox.WithoutPolicy(ctx, "graphql: the row this update wrote"))
		if velox.IsNotFound(err) {
			return NotFound(u.s.input, u.s.typ)
		}
		if err != nil {
			return err
		}
		out = x
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// unassigned are the assignments of in but those of a column one of set
// assigns.
func unassigned(in, set []velox.Assignment) []velox.Assignment {
	if len(set) == 0 {
		return in
	}
	var out []velox.Assignment
	for _, s := range in {
		if !slices.ContainsFunc(set, func(a velox.Assignment) bool { return a.Column == s.Column }) {
			out = append(out, s)
		}
	}
	return out
}

// Key decodes id, the ID of the GraphQL type typ that the input's field gives,
// into a K (DecodeID). A failure is a decode error of m, and Key returns the
// zero K.
func Key[K any](m Mutation, field, id, typ string) K {
	s := m.state()
	k, err := DecodeID[K](s.at(field), id, typ)
	s.decodeFail(err)
	return k
}

// KeyAs is Key of a K converted to V by conv: for a key whose Go type is a
// named type DecodeID does not take.
func KeyAs[K, V any](m Mutation, field, id, typ string, conv func(K) V) V {
	s := m.state()
	v, err := DecodeIDAs(s.at(field), id, typ, conv)
	s.decodeFail(err)
	return v
}

// OptionalKey is Key of the ID id points to, and a pointer to the key; nil
// when id is nil, the field not given.
func OptionalKey[K any](m Mutation, field string, id *string, typ string) *K {
	if id == nil {
		return nil
	}
	k := Key[K](m, field, *id, typ)
	return &k
}

// OptionalKeyAs is OptionalKey of a K converted to V by conv.
func OptionalKeyAs[K, V any](m Mutation, field string, id *string, typ string, conv func(K) V) *V {
	if id == nil {
		return nil
	}
	v := KeyAs(m, field, *id, typ, conv)
	return &v
}

// Keys decodes ids, the IDs the input's field gives, into their distinct keys
// (DecodeIDs): more than the MaxPage of veloxgql.LimitsFromContext(ctx) is
// refused. A failure is a decode error of m, and Keys returns nil.
func Keys[K comparable](m Mutation, field string, ids []string, typ string) []K {
	s := m.state()
	ks, err := DecodeIDs[K](s.ctx, s.at(field), ids, typ)
	s.decodeFail(err)
	return ks
}

// KeysAs is Keys of K keys, each converted to V by conv.
func KeysAs[K comparable, V any](m Mutation, field string, ids []string, typ string, conv func(K) V) []V {
	s := m.state()
	vs, err := DecodeIDsAs(s.ctx, s.at(field), ids, typ, conv)
	s.decodeFail(err)
	return vs
}

// DecodeJSON decodes j, the JSON value the input's field gives, into a T
// (FromJSON). A failure is a decode error of m, and DecodeJSON returns the
// zero T.
func DecodeJSON[T any](m Mutation, field string, j veloxgql.JSON) T {
	s := m.state()
	t, err := FromJSON[T](s.at(field), j)
	s.decodeFail(err)
	return t
}

// NotZero refuses v, what the input's field gives a field that has a
// Default, when it is the field's zero value: an insert would store the
// Default in its place, as it does for a field left out. The client gets
// BAD_USER_INPUT.
func NotZero[V any](m Mutation, field string, v V) {
	s := m.state()
	s.fail(notZero(s.at(field), v))
}

// NotNull refuses a null the client gave the input's field, not given in Go
// (given is false), on a Nillable field that has a Default: an insert would
// store the Default in its place, as it does for a field left out, and the
// input's nil pointer cannot tell them apart. So NotNull reads the request:
// a null in any value of the input's type that the running field's
// arguments hold (see Cleared). The client gets BAD_USER_INPUT. Called
// outside a gqlgen field it finds no null and refuses nothing.
func NotNull(m Mutation, field string, given bool) {
	s := m.state()
	if given {
		return
	}
	if _, nulls := nullsIn(s.ctx, s.input, field); nulls > 0 {
		s.fail(InputErrorf("%s: invalid value: null cannot be stored on create, where it means the field's default; leave the field out, or clear it with an update", s.at(field)))
	}
}

// Cleared reports whether an update clears a nullable field: the input's
// clear field clearField is true, or the client gave field as null. The
// input's nil pointer cannot tell null from a field left out, so Cleared
// reads the request: the values of the input's GraphQL type that the
// running field's arguments hold, at any depth of input objects and lists,
// literals and variables alike. With exactly one, its null clears. With
// more than one (a list of inputs, or two arguments), which of them this
// input is cannot be told, so a null in any of them is refused with
// BAD_USER_INPUT naming clearField. With none, as outside a gqlgen field,
// there is no null. The lookup is by type, so an input Go code builds inside
// a field whose arguments hold one of its type reads that one's explicit
// nulls, and a value nested in a variable of another input type is found only
// with the server's schema, which the Extension keeps.
//
// field given with clearField true has no single meaning, and is refused
// with BAD_USER_INPUT "<input>: <field> and <clearField> together".
func Cleared(m Mutation, field string, given bool, clearField string, cleared bool) bool {
	s := m.state()
	if given && cleared {
		s.fail(InputErrorf("%s: %s and %s together", s.input, field, clearField))
		return false
	}
	if given || cleared {
		return cleared
	}
	switch found, nulls := nullsIn(s.ctx, s.input, field); {
	case nulls == 0:
		return false
	case found == 1:
		return true
	default:
		s.fail(InputErrorf("%s: invalid value: null in one of several %s values, which cannot say which one it clears; give %s: true to clear it", s.at(field), s.input, clearField))
		return false
	}
}

// ClearEdge reports whether an update clears an edge: its clearField is
// true. field, the edge's <edge>ID, given with clearField true has no single
// meaning, and is refused as Cleared refuses a field. A null the client gave
// field is refused too, with BAD_USER_INPUT naming clearField: clearing an
// edge unlinks a row, so it is said by clearField. It reads the request as
// Cleared does, and refuses a null in any value of the input's type.
func ClearEdge(m Mutation, field string, given bool, clearField string, cleared bool) bool {
	s := m.state()
	if given && cleared {
		s.fail(InputErrorf("%s: %s and %s together", s.input, field, clearField))
		return false
	}
	if !given && !cleared {
		if _, nulls := nullsIn(s.ctx, s.input, field); nulls > 0 {
			s.fail(InputErrorf("%s: invalid value: null would leave it as it is; give %s: true to clear it, or leave the field out", s.at(field), clearField))
		}
	}
	return cleared
}

// AddRemove decodes add and remove, the IDs of the GraphQL type typ that the
// input's fields addField and removeField give an edge to many, into their
// distinct keys, as Keys does each: a failure is a decode error of m, and
// AddRemove returns nil for it. A key in both refuses the input, which has no
// single meaning, with BAD_USER_INPUT "<input>: an ID is in both <addField>
// and <removeField>", which echoes no ID.
func AddRemove[K comparable](m Mutation, typ, addField string, add []string, removeField string, remove []string) (a, r []K) {
	a, r = Keys[K](m, addField, add, typ), Keys[K](m, removeField, remove, typ)
	s := m.state()
	s.fail(disjoint(s.input, addField, removeField, a, r))
	return a, r
}

// AddRemoveAs is AddRemove of K keys, each converted to V by conv.
func AddRemoveAs[K, V comparable](m Mutation, typ, addField string, add []string, removeField string, remove []string, conv func(K) V) (a, r []V) {
	a, r = KeysAs(m, addField, add, typ, conv), KeysAs(m, removeField, remove, typ, conv)
	s := m.state()
	s.fail(disjoint(s.input, addField, removeField, a, r))
	return a, r
}

// keyed is the condition on column col of t, a key, that it is one of
// keys, a slice or nil (none), each converted to the column's Go type: = for
// one key, else IN. n is the number of keys.
func keyed(t velox.Table, col int, keys any) (p velox.Predicate, n int, err error) {
	ks, err := converted(t, col, keys)
	if len(ks) == 1 {
		return t.Pred(col, velox.PredEQ, ks[0]), 1, err
	}
	return t.Pred(col, velox.PredIn, ks), len(ks), err
}

// in is keyed's condition as IN, for one key too.
func in(t velox.Table, col int, keys any) (p velox.Predicate, n int, err error) {
	ks, err := converted(t, col, keys)
	return t.Pred(col, velox.PredIn, ks), len(ks), err
}

// converted is keys, a slice or nil, each converted to the Go type of t's
// column col.
func converted(t velox.Table, col int, keys any) ([]any, error) {
	ks, _ := values(keys)
	var err error
	for i, k := range ks {
		if ks[i], err = t.Convert(col, k); err != nil {
			return nil, err
		}
	}
	return ks, nil
}

// Exists checks, in the write's transaction and before the row's write, that
// keys, the distinct keys of the IDs the input's field gives (a slice), name
// rows of target, in its key column col, that the caller may read under its
// Read policy (CheckExists). Otherwise the write fails with NOT_FOUND naming
// the field and typ, target's GraphQL type. For no keys nothing runs.
func Exists(m Mutation, field, typ string, target velox.Table, col int, keys any) {
	p, n, err := keyed(target, col, keys)
	exists(m, field, typ, target, p, n, err)
}

// exists is Exists of the condition p on n keys, or of err, the condition's.
func exists(m Mutation, field, typ string, target velox.Table, p velox.Predicate, n int, err error) {
	s := m.state()
	if err != nil {
		s.fail(err)
		return
	}
	if n == 0 {
		return
	}
	at := s.at(field)
	s.before = append(s.before, func(ctx context.Context, db *velox.DB) error {
		return CheckExists(ctx, db.QueryTable(target).Where(p), n, at, typ)
	})
}

// Edit is what an update input says of an edge to many: Clear is
// clear<Edge>, and Remove and Add are the distinct keys of remove<Edge>IDs
// and add<Edge>IDs (AddRemove), slices of the target's keys, or nil. The edge
// is cleared, then the keys to remove are unlinked, then those to add linked.
type Edit struct {
	Clear       bool
	Remove, Add any
}

// unchanged reports whether ed changes nothing.
func unchanged(ed Edit) bool {
	r, _ := values(ed.Remove)
	a, _ := values(ed.Add)
	return !ed.Clear && len(r) == 0 && len(a) == 0
}

// M2M is a many-to-many edge of Owner to Target through the join entity
// Join, by column index: OwnerKey and TargetKey are the two entities' key
// columns, and JoinOwner and JoinTarget the join entity's columns that hold
// them. Join and Through write it. Generated code makes one per edge, in a
// function that both a create input's Create and an update input's Update
// call. The edge's direction is JoinOwner's: the inverse of an edge holds its
// owner in the join entity's second column, and a bidirectional self edge's
// two join rows are velox's to write.
type M2M struct {
	Owner, Target, Join   velox.Table
	OwnerKey, TargetKey   int
	JoinOwner, JoinTarget int
}

// joinRows are the join rows of own, the owner's key, and each of keys.
func (m M2M) joinRows(own any, keys []any) (velox.TableRows, error) {
	rows := m.Join.NewRows(len(keys))
	for i, k := range keys {
		if err := m.Join.SetField(rows.At(i), m.JoinOwner, own); err != nil {
			return velox.TableRows{}, err
		}
		if err := m.Join.SetField(rows.At(i), m.JoinTarget, k); err != nil {
			return velox.TableRows{}, err
		}
	}
	return rows, nil
}

// Owned is an edge of Owner to Target whose foreign key FK, nullable, is on
// Target's rows, by column index: a one-to-many edge, or a one-to-one one
// whose key Target holds. OwnerKey and TargetKey are the two entities' key
// columns. Generated code makes one per edge, in a function that both Create
// (Repoint) and Update (Children or Child) call.
type Owned struct {
	Owner, Target       velox.Table
	OwnerKey, TargetKey int
	FK                  int
}

// Join links a created row to keys, the distinct keys the input's field gives
// of the many-to-many edge m (a slice), to the target of the GraphQL type
// typ: before the insert, Exists checks them; after it, one INSERT writes the
// join row of the created row's key and each key, ON CONFLICT (m's two
// columns) DO NOTHING, so a join row a hook or the other side wrote already
// is kept. With no keys nothing runs.
func Join(c *Create, field, typ string, m M2M, keys any) {
	ks, _ := values(keys)
	if len(ks) == 0 {
		return
	}
	p, n, err := in(m.Target, m.TargetKey, keys)
	exists(c, field, typ, m.Target, p, n, err)
	c.s.after = append(c.s.after, func(ctx context.Context, db *velox.DB) error {
		rows, err := m.joinRows(m.Owner.Value(c.x, m.OwnerKey), ks)
		if err != nil {
			return err
		}
		return db.InsertRows(m.Join, rows).OnConflict(m.JoinOwner, m.JoinTarget).DoNothing().Exec(ctx)
	})
}

// Repoint links a created row to the children keys, the distinct keys the
// input's field gives of the edge o (a slice), to the target of the GraphQL
// type typ: after the insert, one UPDATE sets o's foreign key to the created
// row's key on those of them whose key is NULL. Only a child no owner holds
// is re-pointed, as in ent: one another owner holds is not taken from it.
// The UPDATE's row count checks the IDs, as the target's Read and Write
// policies leave out a row the caller may not change, and Repointed tells a
// child the caller may read that another owner holds from one it may not
// change. With no keys nothing runs.
func Repoint(c *Create, field, typ string, o Owned, keys any) {
	ids, n, err := keyed(o.Target, o.TargetKey, keys)
	if err != nil {
		c.s.fail(err)
		return
	}
	if n == 0 {
		return
	}
	at, owner := c.s.at(field), c.s.typ
	c.s.after = append(c.s.after, func(ctx context.Context, db *velox.DB) error {
		v, err := o.Target.Convert(o.FK, o.Owner.Value(c.x, o.OwnerKey))
		if err != nil {
			return err
		}
		t, fk := o.Target, o.FK
		got, err := db.UpdateTable(t).Set(fk, v).Where(ids, t.Pred(fk, velox.PredIsNull, nil)).Exec(ctx)
		if err != nil {
			return err
		}
		return Repointed(ctx, got, n, db.QueryTable(t).Where(ids, t.Pred(fk, velox.PredNotNull, nil), t.Pred(fk, velox.PredNEQ, v)), at, typ, owner)
	})
}

// Children writes an update's edge to many o, to the target of the GraphQL
// type typ, whose foreign key the target's rows hold, and hold own for the
// updated row. After the row's write, in this order: ed.Clear unlinks every
// child the caller may change (an UPDATE that sets the key to NULL);
// ed.Remove unlinks those of its children the row holds; ed.Add, the keys of
// the input field addField, links those no owner holds. Whether each key to
// add is linked is counted after the UPDATE, under the Read policy, not from
// the UPDATE's own row count: MySQL counts only the rows it changed unless
// the DSN has clientFoundRows=true, and a child already linked here that the
// Write policy hides is a link that exists. A child another owner holds, or
// that the caller may not change, fails as Repoint's does.
func Children(u *Update, addField, typ string, ed Edit, own any, o Owned) {
	if unchanged(ed) {
		return
	}
	t, fk := o.Target, o.FK
	v, err := t.Convert(fk, own)
	if err != nil {
		u.s.fail(err)
		return
	}
	rm, nrm, err := in(t, o.TargetKey, ed.Remove)
	u.s.fail(err)
	add, nadd, err := in(t, o.TargetKey, ed.Add)
	u.s.fail(err)
	at, owner := u.s.at(addField), u.s.typ
	held := t.Pred(fk, velox.PredEQ, v)
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		if ed.Clear {
			if _, err := db.UpdateTable(t).SetNull(fk).Where(held).Exec(ctx); err != nil {
				return err
			}
		}
		if nrm > 0 {
			if _, err := db.UpdateTable(t).SetNull(fk).Where(rm, held).Exec(ctx); err != nil {
				return err
			}
		}
		if nadd > 0 {
			return relink(ctx, db, o, add, nadd, v, at, typ, owner)
		}
		return nil
	})
}

// Child writes an update's one-to-one edge o, to the target of the GraphQL
// type typ, whose foreign key the child's row holds, and holds own for the
// updated row. After the row's write: clearAll unlinks the child the row
// holds; a non-nil k, a pointer to the key the input's field gives,
// re-points the edge to that child.
//
// The new child must be one the caller may read (CheckExists), or the
// re-point is NOT_FOUND, as for an ID that names no child: otherwise
// StillLinked would tell a guess of a hidden child's ID from any other ID.
// The child the row holds now, unless it is the new one, is let go next:
// the key is unique. That UPDATE is under the caller's scope, so a child it
// leaves holding the key is one the caller may not change: StillLinked
// refuses the re-point before the link would fail on the unique key, and
// the child stays linked. It looks whatever the UPDATE's row count, which
// may be unknown (-1). Then the new child is linked as Children links one.
func Child(u *Update, field, typ string, clearAll bool, k any, own any, o Owned) {
	key, given := deref(k)
	if !clearAll && !given {
		return
	}
	t, fk := o.Target, o.FK
	v, err := t.Convert(fk, own)
	if err != nil {
		u.s.fail(err)
		return
	}
	held := t.Pred(fk, velox.PredEQ, v)
	var id, other velox.Predicate
	if given {
		c, err := t.Convert(o.TargetKey, key)
		if err != nil {
			u.s.fail(err)
			return
		}
		id, other = t.Pred(o.TargetKey, velox.PredEQ, c), t.Pred(o.TargetKey, velox.PredNEQ, c)
	}
	at, owner := u.s.at(field), u.s.typ
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		if clearAll {
			if _, err := db.UpdateTable(t).SetNull(fk).Where(held).Exec(ctx); err != nil {
				return err
			}
		}
		if !given {
			return nil
		}
		if err := CheckExists(ctx, db.QueryTable(t).Where(id), 1, at, typ); err != nil {
			return err
		}
		if _, err := db.UpdateTable(t).SetNull(fk).Where(held, other).Exec(ctx); err != nil {
			return err
		}
		if err := StillLinked(ctx, db.QueryTable(t).Where(held, other), at, typ, owner); err != nil {
			return err
		}
		return relink(ctx, db, o, id, 1, v, at, typ, owner)
	})
}

// relink links the n distinct children ids selects whose key is NULL to v,
// the owner's key as o's FK holds it, and checks, by a count after the
// UPDATE, that each of them holds v.
func relink(ctx context.Context, db *velox.DB, o Owned, ids velox.Predicate, n int, v any, at, typ, owner string) error {
	t, fk := o.Target, o.FK
	if _, err := db.UpdateTable(t).Set(fk, v).Where(ids, t.Pred(fk, velox.PredIsNull, nil)).Exec(ctx); err != nil {
		return err
	}
	got, err := db.QueryTable(t).Where(ids, t.Pred(fk, velox.PredEQ, v)).Count(ctx)
	if err != nil {
		return err
	}
	return Repointed(ctx, got, n, db.QueryTable(t).Where(ids, t.Pred(fk, velox.PredNotNull, nil), t.Pred(fk, velox.PredNEQ, v)), at, typ, owner)
}

// Through writes an update's many-to-many edge m, to the target of the
// GraphQL type typ, for the updated row, whose key is own. After the row's
// write, in this order:
//   - ed.Clear deletes the row's join rows, read the MaxPage of
//     veloxgql.LimitsFromContext(ctx) at a time in the order of m's two
//     columns (UnlinkAll);
//   - ed.Remove deletes the join rows of those keys;
//   - ed.Add, the keys of the input field addField, are checked under the
//     target's Read policy (CheckExists, NOT_FOUND naming addField), then
//     inserted in one INSERT ON CONFLICT (m's two columns) DO NOTHING, so a
//     link that exists is kept.
//
// Join rows are scoped by no policy of their own: ed.Clear and ed.Remove
// delete only those to a target the caller may read, under the target's Read
// policy (Readable), as ed.Add checks its targets. A link to a target hidden
// from the caller stays.
func Through(u *Update, addField, typ string, ed Edit, own any, m M2M) {
	if unchanged(ed) {
		return
	}
	j := m.Join
	o, err := j.Convert(m.JoinOwner, own)
	if err != nil {
		u.s.fail(err)
		return
	}
	rm, _ := values(ed.Remove)
	add, _ := values(ed.Add)
	addIDs, nadd, err := in(m.Target, m.TargetKey, ed.Add)
	u.s.fail(err)
	at := u.s.at(addField)
	mine := j.Pred(m.JoinOwner, velox.PredEQ, o)
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		unlink := func(ks []any) error {
			ids, n, err := in(m.Target, m.TargetKey, ks)
			if err != nil || n == 0 {
				return err
			}
			vs, err := Readable(ctx, db.QueryTable(m.Target).Where(ids).Columns(m.TargetKey), n, m.TargetKey)
			if err != nil || len(vs) == 0 {
				return err
			}
			for i, v := range vs {
				if vs[i], err = j.Convert(m.JoinTarget, v); err != nil {
					return err
				}
			}
			_, err = db.DeleteTable(j).Where(mine, j.Pred(m.JoinTarget, velox.PredIn, vs)).Exec(ctx)
			return err
		}
		if ed.Clear {
			links := db.QueryTable(j).Where(mine).OrderBy(velox.OrderTerm{Column: m.JoinOwner}, velox.OrderTerm{Column: m.JoinTarget})
			if err := UnlinkAll(ctx, links, m.JoinTarget, unlink); err != nil {
				return err
			}
		}
		if len(rm) > 0 {
			if err := unlink(rm); err != nil {
				return err
			}
		}
		if nadd == 0 {
			return nil
		}
		if err := CheckExists(ctx, db.QueryTable(m.Target).Where(addIDs), nadd, at, typ); err != nil {
			return err
		}
		rows, err := m.joinRows(o, add)
		if err != nil {
			return err
		}
		return db.InsertRows(j, rows).OnConflict(m.JoinOwner, m.JoinTarget).DoNothing().Exec(ctx)
	})
}
