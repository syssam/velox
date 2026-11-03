package gqlimpl

import (
	"context"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Mutation is a mutation input being turned into a write: a Create or an
// Update. The helpers that take one record their first error in it and the
// statements they need, which Create.Insert and Update.Exec run. Only
// *Create and *Update implement it.
//
// A helper records an error of two kinds. A decode error (Key, Keys,
// DecodeJSON) is a malformed or mistyped ID, a list over MaxPage or a JSON
// value of the wrong shape; any other error (NotZero, NotNull, Cleared,
// ClearEdge, Disjoint) is an input with no single meaning. Insert and Exec
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

// Create is a create input of E being inserted: x, with the input's fields
// set on it, and the statements of its edges. A create input's generated
// method Create makes one with NewCreate, sets each field the input gives on
// x, adds its edges with one helper call each, and returns Insert:
//
//	func (in *CreatePostInput) Create(ctx context.Context, x *Post) (*Post, error) {
//		c, x := gqlimpl.NewCreate(ctx, "CreatePostInput", "gen.Post", x, postInputGQL, Posts.Title)
//		x.Title = in.Title
//		if in.Views != nil {
//			x.Views = *in.Views
//			c.Given(Posts.Views)
//		}
//		x.AuthorID = gqlimpl.Key[int64](c, "authorID", in.AuthorID, "User")
//		gqlimpl.Exists(c, "authorID", "User", 1, Users.ID.EQ(x.AuthorID))
//		tagKeys := gqlimpl.Keys[int64](c, "tagIDs", in.TagIDs, "Label")
//		gqlimpl.Join(c, "tagIDs", "Label", tagKeys, Tags.ID.In, func(x *Post, k int64) PostTag { return PostTag{PostID: x.ID, TagID: k} }, PostTags.PostID, PostTags.TagID)
//		return c.Insert()
//	}
type Create[E any, PE velox.Entity[E]] struct {
	s    inputState
	x    *E
	cols []velox.Column[E] // the columns the input sets, validated before any statement
}

func (c *Create[E, PE]) state() *inputState { return &c.s }

// NewCreate returns the Create of x from the create input of the GraphQL type
// input ("CreatePostInput"), and x, a new E when x is nil. entity is E as
// velox names it in a *velox.ValidationError ("gen.Post"), and name gives
// the input's field that sets an entity field, by the field's Go name, ""
// for one the input does not set: a validator's error on such a field
// becomes BAD_USER_INPUT naming it (WriteError). required are the columns of
// the input's required fields, whose validators run before any statement;
// Given adds those of the optional fields the input gives.
func NewCreate[E any, PE velox.Entity[E]](ctx context.Context, input, entity string, x *E, name func(goField string) string, required ...velox.Column[E]) (*Create[E, PE], *E) {
	if x == nil {
		x = new(E)
	}
	typ := strings.TrimSuffix(strings.TrimPrefix(input, "Create"), "Input")
	return &Create[E, PE]{s: inputState{ctx: ctx, input: input, typ: typ, entity: entity, name: name}, x: x, cols: required}, x
}

// Given adds col, the column of an optional field the input gives, to those
// validated before any statement.
func (c *Create[E, PE]) Given[_ velox.Lazy[E]](col velox.Column[E]) { c.cols = append(c.cols, col) }

// Insert inserts the row, and writes its edges, and returns x as written.
//
// It fails, before any statement, with the first error a helper recorded
// (see Mutation), and then with a validator's error on a column the input
// sets (velox.Validate, through WriteError). Then, atomically on the DB
// veloxgql.DBFromContext reads from ctx (Atomic), it runs each check the
// helpers registered before the insert, in their order (the IDs of the edges
// whose key is on the row, and of the many-to-many edges, must name rows the
// caller may read: Exists and Join), inserts x under the policies, and then
// writes each edge whose key is on the target's rows (Repoint) and each
// join row (Join), in their order. An error of any of them rolls the whole
// create back. x is changed even when Insert fails: its key, the keys of its
// edges and whatever a hook sets on it.
func (c *Create[E, PE]) Insert[_ velox.Lazy[E]]() (*E, error) {
	if err := c.s.failed(); err != nil {
		return nil, err
	}
	if err := velox.Validate[E, PE](c.x, c.cols...); err != nil {
		return nil, c.s.writeError(err)
	}
	err := Atomic(c.s.ctx, func(ctx context.Context, db *velox.DB) error {
		for _, f := range c.s.before {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		if err := db.Insert[E, PE](c.x).Exec(ctx); err != nil {
			return c.s.writeError(err)
		}
		for _, f := range c.s.after {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.x, nil
}

// Update is an update input of E being written: the setters of its fields
// and of the keys on the row, and the statements of its edges. An update
// input's generated method Update makes one with NewUpdate, adds a setter of
// each field the input gives with Set, its edges with one helper call each,
// and returns Exec:
//
//	func (in *UpdatePostInput) Update(ctx context.Context, id string, set ...velox.Setter[Post]) (*Post, error) {
//		u, key := gqlimpl.NewUpdate[Post, int64](ctx, "UpdatePostInput", "gen.Post", id, postInputGQL)
//		if in.Title != nil {
//			u.Set(Posts.Title.Set(*in.Title))
//		}
//		if gqlimpl.Cleared(u, "publishedAt", in.PublishedAt != nil, "clearPublishedAt", in.ClearPublishedAt) {
//			u.Set(Posts.PublishedAt.SetNull())
//		}
//		return u.Exec(Posts.ID.EQ(key), set...)
//	}
type Update[E any, PE velox.Entity[E]] struct {
	s    inputState
	set  []velox.Setter[E] // the setters of the input's fields
	keys []velox.Setter[E] // the setters of the keys on the row
}

func (u *Update[E, PE]) state() *inputState { return &u.s }

// NewUpdate returns the Update of the row whose ID is id from the update
// input of the GraphQL type input ("UpdatePostInput"), and the row's key, of
// the Go type K, decoded from id; entity and name are NewCreate's. A
// malformed id, or an ID of another type, is a decode error, the first: its
// text names "id".
func NewUpdate[E, K any, PE velox.Entity[E]](ctx context.Context, input, entity, id string, name func(goField string) string) (*Update[E, PE], K) {
	u := newUpdate[E, PE](ctx, input, entity, name)
	k, err := DecodeID[K]("id", id, u.s.typ)
	u.s.decodeFail(err)
	return u, k
}

// NewUpdateAs is NewUpdate of a key whose Go type V is a named type DecodeID
// does not take: id is decoded as a K and converted by conv.
func NewUpdateAs[E, K, V any, PE velox.Entity[E]](ctx context.Context, input, entity, id string, name func(goField string) string, conv func(K) V) (*Update[E, PE], V) {
	u := newUpdate[E, PE](ctx, input, entity, name)
	v, err := DecodeIDAs("id", id, u.s.typ, conv)
	u.s.decodeFail(err)
	return u, v
}

func newUpdate[E any, PE velox.Entity[E]](ctx context.Context, input, entity string, name func(goField string) string) *Update[E, PE] {
	typ := strings.TrimSuffix(strings.TrimPrefix(input, "Update"), "Input")
	return &Update[E, PE]{s: inputState{ctx: ctx, input: input, typ: typ, entity: entity, name: name}}
}

// Set adds s, the setter of a field the input gives, to the update. Its
// validator runs before any statement.
func (u *Update[E, PE]) Set[_ velox.Lazy[E]](s velox.Setter[E]) { u.set = append(u.set, s) }

// SetKey adds s, the setter of the key of an edge on the row, to the update,
// after the setters of the fields and the application's: Exists checks the
// ID it sets.
func (u *Update[E, PE]) SetKey[_ velox.Lazy[E]](s velox.Setter[E]) { u.keys = append(u.keys, s) }

// Exec updates the row row selects, writes its edges, and returns the row as
// written. set are the application's own setters, which run after the
// input's, are validated with them, and count as setting something.
//
// It fails, before any statement, with the first error a helper recorded
// (see Mutation), and then with a validator's error on what the input's
// setters and set assign (velox.ValidateSet, through WriteError). Then,
// atomically on the DB veloxgql.DBFromContext reads from ctx (Atomic):
//   - each check registered before the row's write, in order: the IDs the
//     keys on the row are set to must name rows the caller may read (Exists);
//   - the UPDATE of the row with every setter, under the Write policy: a row
//     it does not change is NOT_FOUND "<input>: no <Type> has this ID". When
//     there is nothing to set, or the driver does not report the rows it
//     changed (-1), LockRow checks the row instead under the same scope, and
//     locks it where the dialect can: no edge of a row the caller may read but
//     not change is written;
//   - each edge, in order (Children, Child, Through);
//   - the row, read back without the Read policy, as the update wrote it: an
//     update that moves the row out of the caller's Read view succeeds.
//
// An error of any of them rolls the whole update back.
func (u *Update[E, PE]) Exec[_ velox.Lazy[E]](row velox.Pred[E], set ...velox.Setter[E]) (*E, error) {
	if err := u.s.failed(); err != nil {
		return nil, err
	}
	all := append(slices.Clip(u.set), set...)
	if err := velox.ValidateSet[E, PE](all...); err != nil {
		return nil, u.s.writeError(err)
	}
	var out *E
	err := Atomic(u.s.ctx, func(ctx context.Context, db *velox.DB) error {
		for _, f := range u.s.before {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		all := append(slices.Clip(all), u.keys...)
		counted := false
		if len(all) > 0 {
			n, err := db.Update[E, PE]().Set(all...).Where(row).Exec(ctx)
			if err != nil {
				return u.s.writeError(err)
			}
			if n == 0 {
				return NotFound(u.s.input, u.s.typ)
			}
			counted = n > 0
		}
		if !counted {
			if err := LockRow(ctx, db, db.Query[E, PE]().Where(row), u.s.input, u.s.typ); err != nil {
				return err
			}
		}
		for _, f := range u.s.after {
			if err := f(ctx, db); err != nil {
				return err
			}
		}
		x, err := db.Query[E, PE]().Where(row).One(velox.WithoutPolicy(ctx, "graphql: the row this update wrote"))
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
// there is no null.
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

// Disjoint refuses a key in both a and b, the decoded IDs of the input's
// fields add and remove: the input has no single meaning. The client gets
// BAD_USER_INPUT "<input>: an ID is in both <add> and <remove>", which echoes
// no ID.
func Disjoint[K comparable](m Mutation, add, remove string, a, b []K) {
	s := m.state()
	s.fail(disjoint(s.input, add, remove, a, b))
}

// Exists checks, in the write's transaction and before the row's write, that
// p selects n rows of T under the caller's Read policy: the n distinct IDs
// the input's field gives name rows the caller may read (CheckExists).
// Otherwise the write fails with NOT_FOUND naming the field and typ, T's
// GraphQL type. For n 0 nothing runs.
func Exists[T any, PT velox.Entity[T]](m Mutation, field, typ string, n int, p velox.Pred[T]) {
	s := m.state()
	if n == 0 {
		return
	}
	at := s.at(field)
	s.before = append(s.before, func(ctx context.Context, db *velox.DB) error {
		return CheckExists(ctx, db.Query[T, PT]().Where(p), n, at, typ)
	})
}

// Join links a created row to keys, the distinct keys the input's field
// gives of a many-to-many edge to T, of the GraphQL type typ: before the
// insert, Exists of in(keys...) checks them; after it, one INSERT writes the
// join row row makes of x and each key, ON CONFLICT (conflict) DO NOTHING,
// so a join row a hook or the other side wrote already is kept. With no keys
// nothing runs.
func Join[E any, PE velox.Entity[E], T, J any, K comparable, PT velox.Entity[T], PJ velox.Entity[J]](c *Create[E, PE], field, typ string, keys []K, in func(...K) velox.Pred[T], row func(x *E, k K) J, conflict ...velox.Column[J]) {
	if len(keys) == 0 {
		return
	}
	Exists[T, PT](c, field, typ, len(keys), in(keys...))
	c.s.after = append(c.s.after, func(ctx context.Context, db *velox.DB) error {
		rows := make([]J, len(keys))
		for i, k := range keys {
			rows[i] = row(c.x, k)
		}
		return db.InsertAll[J, PJ](rows).OnConflict(conflict...).DoNothing().Exec(ctx)
	})
}

// Repoint links a created row to the n distinct children ids selects, of the
// GraphQL type typ, whose foreign key fk (a nullable column handle of T) is
// on the children's rows: after the insert, one UPDATE sets fk to own(x)
// on those of them whose key is NULL. Only a child no owner holds is
// re-pointed, as in ent: one another owner holds is not taken from it. The
// UPDATE's row count checks the IDs, as the target's Read and Write policies
// leave out a row the caller may not change, and Repointed tells a child the
// caller may read that another owner holds from one it may not change. For n
// 0 nothing runs.
func Repoint[E any, PE velox.Entity[E], T any, H ~struct{ I int32 }, V any, PT velox.Entity[T]](c *Create[E, PE], field, typ string, n int, ids velox.Pred[T], fk H, own func(x *E) V) {
	if n == 0 {
		return
	}
	at, owner := c.s.at(field), c.s.typ
	c.s.after = append(c.s.after, func(ctx context.Context, db *velox.DB) error {
		h, v := velox.NullCol[T, V](fk), own(c.x)
		got, err := db.Update[T, PT]().Set(h.Set(v)).Where(ids, h.IsNull()).Exec(ctx)
		if err != nil {
			return err
		}
		return Repointed(ctx, got, n, db.Query[T, PT]().Where(ids, h.NotNull(), h.NEQ(v)), at, typ, owner)
	})
}

// Children writes an update's one-to-many edge to T, of the GraphQL type
// typ, whose foreign key fk (a nullable column handle of T) is on the
// children's rows and holds own for the updated row; pk is T's key column.
// After the row's write, in this order: clearAll unlinks every child the
// caller may change (an UPDATE that sets fk to NULL); remove, the distinct
// keys of removeField, unlinks those of them it holds; add, the keys of
// addField, links those no owner holds. Whether each key to add is linked
// is counted after the UPDATE, under the Read policy, not from the UPDATE's
// own row count: MySQL counts only the rows it changed unless the DSN has
// clientFoundRows=true, and a child already linked here that the Write
// policy hides is a link that exists. A child another owner holds, or that
// the caller may not change, fails as Repoint's does.
func Children[T any, E any, PE velox.Entity[E], K comparable, HP, HF ~struct{ I int32 }, V any, PT velox.Entity[T]](u *Update[E, PE], addField, typ string, clearAll bool, remove, add []K, pk HP, fk HF, own V) {
	if !clearAll && len(remove) == 0 && len(add) == 0 {
		return
	}
	at, owner := u.s.at(addField), u.s.typ
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		h, id := velox.NullCol[T, V](fk), velox.Col[T, K](pk)
		if clearAll {
			if _, err := db.Update[T, PT]().Set(h.SetNull()).Where(h.EQ(own)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(remove) > 0 {
			if _, err := db.Update[T, PT]().Set(h.SetNull()).Where(id.In(remove...), h.EQ(own)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(add) > 0 {
			return relink[T, PT](ctx, db, id.In(add...), len(add), h, own, at, typ, owner)
		}
		return nil
	})
}

// Child writes an update's one-to-one edge to T, of the GraphQL type typ,
// whose foreign key fk (a nullable column handle of T) is on the child's
// row and holds own for the updated row; pk is T's key column. After the
// row's write: clearAll unlinks the child the row holds; a non-nil k, the key
// the input's field gives, re-points the edge to that child.
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
func Child[T any, E any, PE velox.Entity[E], K comparable, HP, HF ~struct{ I int32 }, V any, PT velox.Entity[T]](u *Update[E, PE], field, typ string, clearAll bool, k *K, pk HP, fk HF, own V) {
	if !clearAll && k == nil {
		return
	}
	at, owner := u.s.at(field), u.s.typ
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		h, id := velox.NullCol[T, V](fk), velox.Col[T, K](pk)
		if clearAll {
			if _, err := db.Update[T, PT]().Set(h.SetNull()).Where(h.EQ(own)).Exec(ctx); err != nil {
				return err
			}
		}
		if k == nil {
			return nil
		}
		if err := CheckExists(ctx, db.Query[T, PT]().Where(id.EQ(*k)), 1, at, typ); err != nil {
			return err
		}
		if _, err := db.Update[T, PT]().Set(h.SetNull()).Where(h.EQ(own), id.NEQ(*k)).Exec(ctx); err != nil {
			return err
		}
		if err := StillLinked(ctx, db.Query[T, PT]().Where(h.EQ(own), id.NEQ(*k)), at, typ, owner); err != nil {
			return err
		}
		return relink[T, PT](ctx, db, id.EQ(*k), 1, h, own, at, typ, owner)
	})
}

// relink links the n distinct children ids selects whose key h is NULL to
// own, and checks, by a count after the UPDATE, that each of them holds own.
func relink[T any, PT velox.Entity[T], V any](ctx context.Context, db *velox.DB, ids velox.Pred[T], n int, h velox.NullCol[T, V], own V, at, typ, owner string) error {
	if _, err := db.Update[T, PT]().Set(h.Set(own)).Where(ids, h.IsNull()).Exec(ctx); err != nil {
		return err
	}
	got, err := db.Query[T, PT]().Where(ids, h.EQ(own)).Count(ctx)
	if err != nil {
		return err
	}
	return Repointed(ctx, got, n, db.Query[T, PT]().Where(ids, h.NotNull(), h.NEQ(own)), at, typ, owner)
}

// Through writes an update's many-to-many edge to T, of the GraphQL type
// typ, through the join entity J, whose column jo holds the updated row's
// key own and jt the target's; pk is T's key column and key reads it from a
// row, other reads jt from a join row, and row makes the join row of a
// target key. After the row's write, in this order:
//   - clearAll deletes the row's join rows, read the MaxPage of
//     veloxgql.LimitsFromContext(ctx) at a time in jo, jt order (UnlinkAll);
//   - remove, the distinct keys of an input field, deletes the join rows of
//     those of them;
//   - add, the distinct keys of addField, are checked under T's Read policy
//     (CheckExists, NOT_FOUND naming addField), then inserted in one INSERT
//     ON CONFLICT (jo, jt) DO NOTHING, so a link that exists is kept.
//
// Join rows are scoped by no policy of their own: clearAll and remove delete
// only those to a target the caller may read, under T's Read policy
// (Readable), as add checks its targets. A link to a target hidden from the
// caller stays.
func Through[T any, E any, PE velox.Entity[E], J any, K comparable, V any, HP, HO, HT ~struct{ I int32 }, PT velox.Entity[T], PJ velox.Entity[J]](u *Update[E, PE], addField, typ string, clearAll bool, remove, add []K, pk HP, key func(*T) K, jo HO, own V, jt HT, other func(*J) K, row func(k K) J) {
	if !clearAll && len(remove) == 0 && len(add) == 0 {
		return
	}
	at := u.s.at(addField)
	u.s.after = append(u.s.after, func(ctx context.Context, db *velox.DB) error {
		id, o, t := velox.Col[T, K](pk), velox.Col[J, V](jo), velox.Col[J, K](jt)
		unlink := func(ks []K) error {
			vs, err := Readable(ctx, db.Query[T, PT]().Where(id.In(ks...)).Columns(id), len(ks), key)
			if err != nil || len(vs) == 0 {
				return err
			}
			_, err = db.Delete[J, PJ]().Where(o.EQ(own), t.In(vs...)).Exec(ctx)
			return err
		}
		if clearAll {
			if err := UnlinkAll(ctx, db.Query[J, PJ]().Where(o.EQ(own)).OrderBy(o.Asc(), t.Asc()), other, unlink); err != nil {
				return err
			}
		}
		if len(remove) > 0 {
			if err := unlink(remove); err != nil {
				return err
			}
		}
		if len(add) == 0 {
			return nil
		}
		if err := CheckExists(ctx, db.Query[T, PT]().Where(id.In(add...)), len(add), at, typ); err != nil {
			return err
		}
		rows := make([]J, len(add))
		for i, k := range add {
			rows[i] = row(k)
		}
		return db.InsertAll[J, PJ](rows).OnConflict(o, t).DoNothing().Exec(ctx)
	})
}
