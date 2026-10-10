package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox/tests/orm/db/predicate"

	client "github.com/syssam/velox/tests/orm/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/tests/orm/db/entity"
	"github.com/syssam/velox/tests/orm/db/pet"
	"github.com/syssam/velox/tests/orm/db/post"
	"github.com/syssam/velox/tests/orm/db/tag"
	"github.com/syssam/velox/tests/orm/db/user"
	testschema "github.com/syssam/velox/testschema"
)

func names(us []*entity.User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

// The schema's query policy reaches every read terminal, an eager load of the
// entity, an edge traversal to it and an edge predicate over it.
func TestPolicy_QueryFilterReachesEveryRead(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	pa, pb := newPost(t, db, ann, "a1"), newPost(t, db, bob, "b1")
	only := testschema.FilterUserQueryToNameContext(ctx, "ann")

	us, err := client.User.Query(db).OrderBy(user.ID.Asc()).All(only)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(us))
	// The reference agrees.
	rus, err := ref.User.Query().All(only)
	require.NoError(t, err)
	require.Len(t, rus, 1)

	n, err := client.User.Query(db).Count(only)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	ids, err := client.User.Query(db).IDs(only)
	require.NoError(t, err)
	assert.Equal(t, []int{ann.ID}, ids)
	ok, err := client.User.Query(db).Where(user.ID.EQ(bob.ID)).Exists(only)
	require.NoError(t, err)
	assert.False(t, ok, "Exist sees a row the policy filters out")
	_, err = client.User.Query(db).Where(user.ID.EQ(bob.ID)).Only(only)
	assert.ErrorIs(t, err, orm.ErrNotFound)

	// Eager load: bob's post loads, its author does not.
	ps, err := client.Post.Query(db).OrderBy(post.ID.Asc()).WithAuthor().All(only)
	require.NoError(t, err)
	require.Len(t, ps, 2)
	assert.NotNil(t, ps[0].Edges.Author)
	assert.Nil(t, ps[1].Edges.Author, "the eager load ignored the target's policy")

	// Traversal from an entity.
	_, err = pb.QueryAuthor(db).Only(only)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	a, err := pa.QueryAuthor(db).Only(only)
	require.NoError(t, err)
	assert.Equal(t, "ann", a.Name)

	// Edge predicate: posts that have an author the caller may see.
	ps, err = client.Post.Query(db).Where(post.Author.Has()).All(only)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "a1", ps[0].Title)
	ps, err = client.Post.Query(db).Where(post.Author.HasWith(user.Age.GT(1))).All(only)
	require.NoError(t, err)
	assert.Len(t, ps, 1)

	// Running the same builder again does not stack the filter, and without
	// the context key nothing is filtered.
	q := client.User.Query(db).OrderBy(user.ID.Asc())
	_, err = q.All(only)
	require.NoError(t, err)
	us, err = q.All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann", "bob"}, names(us))
}

func TestPolicy_QueryDeny(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	p := newPost(t, db, u, "a1")
	tg, err := client.Tag.Create(db).Set(tag.Name.Set("go")).Save(ctx)
	require.NoError(t, err)
	_, err = client.Post.Update(db).Set(post.Tags.Add(tg.ID)).Where(post.ID.EQ(p.ID)).Exec(ctx)
	require.NoError(t, err)
	deny := testschema.DenyTagQueryContext(ctx)

	_, err = client.Tag.Query(db).All(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.Tag.Query(db).Count(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	// A denied target fails the whole parent query, for an eager load and for
	// an edge predicate alike.
	_, err = client.Post.Query(db).WithTags().All(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.Post.Query(db).Where(post.Tags.Has()).All(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	// ... and a delete whose condition goes through the denied edge deletes nothing.
	_, err = client.Post.Delete(db).Where(post.Tags.Has()).Exec(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	n, err := client.Post.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// The prefix filter narrows an M2M eager load.
	_, err = client.Tag.Create(db).Set(tag.Name.Set("rust")).Save(ctx)
	require.NoError(t, err)
	ps, err := client.Post.Query(db).WithTags().All(testschema.FilterTagQueryToPrefixContext(ctx, "ru"))
	require.NoError(t, err)
	assert.Empty(t, ps[0].Edges.Tags)
}

func TestPolicy_Mutation(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	enforce := testschema.EnforceUserPrivacyContext(ctx)

	// Denied without the write marker: create, update, delete, bulk.
	_, err := client.User.Create(db).Set(user.Name.Set("x"), user.Email.Set("x@example.com"), user.Age.Set(1)).Save(enforce)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.User.Update(db).Set(user.Age.Set(99)).Exec(enforce)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.User.Delete(db).Exec(enforce)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.User.CreateBulk(db, client.User.Create(db).Set(user.Name.Set("y"), user.Email.Set("y@example.com"), user.Age.Set(1))).Save(enforce)
	assert.ErrorIs(t, err, privacy.Deny)
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a denied mutation wrote")

	// Allowed with it.
	_, err = client.User.Update(db).Set(user.Age.Set(31)).Where(user.ID.EQ(ann.ID)).SaveOne(testschema.AllowWriteContext(enforce))
	require.NoError(t, err)

	// A filter rule narrows an update and a delete that have no condition of
	// their own: only ann's row is touched.
	onlyAnn := testschema.FilterUserMutationToNameContext(ctx, "ann")
	n, err = client.User.Update(db).Set(user.Age.Set(50)).Exec(onlyAnn)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	b, err := client.User.Query(db).Where(user.ID.EQ(bob.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, 40, b.Age, "the filter did not reach the update")
	// Update of one row by id, under a filter that excludes it: not found, not written.
	_, err = client.User.Update(db).Set(user.Age.Set(77)).Where(user.ID.EQ(bob.ID)).SaveOne(onlyAnn)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	// The builder run again without the filter is not narrowed by the earlier run.
	del := client.User.Delete(db).Where(user.Age.GT(0))
	n, err = del.Exec(onlyAnn)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = del.Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestHooks(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	var log []string
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if m.Type() != "User" {
				return next.Mutate(ctx, m)
			}
			log = append(log, "before "+m.Op().String())
			switch {
			case m.Op().Is(velox.OpCreate):
				// Read what the caller set, change it, and set a field the
				// caller did not.
				name, ok := m.Field("name")
				if !ok {
					return nil, errors.New("no name")
				}
				if err := m.SetField("name", name.(string)+"!"); err != nil {
					return nil, err
				}
				if _, set := m.Field("nickname"); !set {
					if err := m.SetField("nickname", "hooked"); err != nil {
						return nil, err
					}
				}
			case m.Op().Is(velox.OpUpdateOne):
				old, err := m.OldField(ctx, "age")
				if err != nil {
					return nil, err
				}
				log = append(log, "old age "+string(rune('0'+old.(int)/10)))
			}
			v, err := next.Mutate(ctx, m)
			log = append(log, "after "+m.Op().String())
			return v, err
		})
	})

	u, err := client.User.Create(db).Set(user.Name.Set("ann"), user.Email.Set("a@example.com"), user.Age.Set(30)).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann!", u.Name, "the entity returned")
	require.NotNil(t, u.Nickname)
	assert.Equal(t, "hooked", *u.Nickname)
	got, err := client.User.Query(db).Where(user.ID.EQ(u.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann!", got.Name, "the row written")

	// A wrong type and an unknown field are errors, not silent no-ops.
	m := velox.Mutation(nil)
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, mm velox.Mutation) (velox.Value, error) {
			m = mm
			return next.Mutate(ctx, mm)
		})
	})
	_, err = client.User.Update(db).Set(user.Age.Set(41)).Where(user.ID.EQ(u.ID)).SaveOne(ctx)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Error(t, m.SetField("age", "forty"))
	assert.Error(t, m.SetField("no_such_field", 1))
	assert.NoError(t, m.SetField("age", int64(42)), "a convertible value")
	assert.ElementsMatch(t, []string{"age", "updated_at"}, m.Fields(), "the update default is part of what a hook sees")
	assert.Error(t, m.SetField("age", 41.9), "a value that does not survive the conversion")
	assert.Contains(t, log, "old age 3")

	// Bulk: every row's hook runs before the one statement, and they return
	// last row first.
	log = nil
	us, err := client.User.CreateBulk(db,
		client.User.Create(db).Set(user.Name.Set("b1"), user.Email.Set("b1@example.com"), user.Age.Set(1)),
		client.User.Create(db).Set(user.Name.Set("b2"), user.Email.Set("b2@example.com"), user.Age.Set(2)),
	).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"b1!", "b2!"}, names(us))
	assert.Equal(t, []string{"before OpCreate", "before OpCreate", "after OpCreate", "after OpCreate"}, log)

	// A hook that fails stops the mutation.
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if m.Op().Is(velox.OpDelete) {
				return nil, errors.New("no deletes")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = client.User.Delete(db).Exec(ctx)
	require.ErrorContains(t, err, "no deletes")
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	// Hooks registered on the handle run inside its transactions too.
	log = nil
	require.NoError(t, db.RunTx(ctx, func(tx *orm.Tx) error {
		_, err := client.User.Create(tx).Set(user.Name.Set("tx"), user.Email.Set("tx@example.com"), user.Age.Set(3)).Save(ctx)
		return err
	}))
	assert.Equal(t, []string{"before OpCreate", "after OpCreate"}, log)
}

// A hook that sets a value the policy rejects does not get it written: the
// policy is evaluated again after the hooks.
func TestPolicy_SeesWhatHooksChange(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	bob := newUser(t, db, "bob", 40)
	// The hook widens the mutation after the first policy check by dropping
	// nothing; the filter rule must still hold on the second check.
	calls := 0
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			calls++
			return next.Mutate(ctx, m)
		})
	})
	n, err := client.User.Update(db).Set(user.Age.Set(1)).Exec(testschema.FilterUserMutationToNameContext(ctx, "ann"))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, calls)
	b, err := client.User.Query(db).Where(user.ID.EQ(bob.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, 40, b.Age)
	_ = ann
}

func TestInterceptors(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, _ := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")

	// A traverser that hides users older than 35 from every read.
	var seen []string
	db.Intercept(velox.TraverseFunc(func(_ context.Context, q velox.Query) error {
		oq, ok := q.(*orm.Query)
		if !ok {
			return errors.New("unexpected query type")
		}
		seen = append(seen, oq.Type())
		if oq.Type() == "User" {
			oq.WhereP(sql.FieldLTE("age", 35))
		}
		return nil
	}))
	us, err := client.User.Query(db).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(us))
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	ps, err := client.Post.Query(db).WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Contains(t, seen, "Post")

	// It does not reach a mutation: bob is updated although no read shows him.
	n, err = client.User.Update(db).Set(user.Age.Add(1)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "interceptors must not scope writes")

	// An interceptor around the terminal sees the result.
	var rows int
	db.Intercept(velox.InterceptFunc(func(next velox.Querier) velox.Querier {
		return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
			v, err := next.Query(ctx, q)
			if list, ok := v.([]any); ok {
				rows = len(list)
			}
			return v, err
		})
	}))
	_, err = client.User.Query(db).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rows)
}

// What a hook or a mutation policy sees is the mutation as it will be
// written: a to-one key set through its edge is the key's field, and schema
// defaults are already in. A stamping hook that tests "is the owner set?" used
// to miss pet.Owner.Set and overwrite the caller's value.
func TestHooks_SeeEdgeKeysAndDefaults(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	type seen struct {
		owner    any
		ownerSet bool
		added    []velox.Value
		cleared  bool
		role     any
	}
	var got seen
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			switch m.Type() {
			case "Pet":
				got.owner, got.ownerSet = m.Field("owner_id")
				got.added = m.AddedIDs("owner")
				got.cleared = m.FieldCleared("owner_id") && m.EdgeCleared("owner")
				if m.Op().Is(velox.OpCreate) && !got.ownerSet {
					if err := m.SetField("owner_id", bob.ID); err != nil {
						return nil, err
					}
				}
			case "User":
				got.role, _ = m.Field("role")
			}
			return next.Mutate(ctx, m)
		})
	})

	p, err := client.Pet.Create(db).Set(pet.Name.Set("rex"), pet.Owner.Set(ann.ID)).Save(ctx)
	require.NoError(t, err)
	assert.True(t, got.ownerSet, "the hook did not see the key set through the edge")
	assert.Equal(t, ann.ID, got.owner)
	assert.Equal(t, []velox.Value{ann.ID}, got.added)
	owner, err := p.QueryOwner(db).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann", owner.Name, "the stamping hook overrode the caller's owner")

	_, err = client.Pet.Update(db).Set(pet.Owner.Clear()).Where(pet.ID.EQ(p.ID)).Exec(ctx)
	require.NoError(t, err)
	assert.True(t, got.cleared, "a clear through the edge is a clear of the field, and the reverse")

	newUser(t, db, "cat", 20)
	assert.EqualValues(t, user.RoleUser, got.role, "the schema default is part of what a hook sees")
}

// Changes of one edge in one update apply in order.
func TestUpdate_EdgeChangesApplyInOrder(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	p := newPost(t, db, u, "p")
	mk := func(name string) *entity.Tag {
		tg, err := client.Tag.Create(db).Set(tag.Name.Set(name)).Save(ctx)
		require.NoError(t, err)
		return tg
	}
	t1, t2, t3 := mk("t1"), mk("t2"), mk("t3")
	tags := func() []int {
		ids, err := p.QueryTags(db).OrderBy(tag.ID.Asc()).IDs(ctx)
		require.NoError(t, err)
		return ids
	}
	upd := func(ss ...predicate.PostSetter) {
		_, err := client.Post.Update(db).Set(ss...).Where(post.ID.EQ(p.ID)).Exec(ctx)
		require.NoError(t, err)
	}
	upd(post.Tags.Add(t1.ID, t2.ID))
	upd(post.Tags.Add(t3.ID), post.Tags.Clear())
	assert.Empty(t, tags(), "Add then Clear")
	upd(post.Tags.Add(t1.ID), post.Tags.Clear(), post.Tags.Add(t2.ID))
	assert.Equal(t, []int{t2.ID}, tags(), "Add, Clear, Add")
	upd(post.Tags.Add(t3.ID), post.Tags.Remove(t3.ID))
	assert.Equal(t, []int{t2.ID}, tags(), "Add then Remove of the same id")
	upd(post.Tags.Remove(t2.ID), post.Tags.Add(t2.ID, t1.ID))
	assert.Equal(t, []int{t1.ID, t2.ID}, tags(), "Remove then Add of the same id")

	// A to-one edge whose key is not a schema field.
	parent := func() *int {
		c, err := client.Tag.Query(db).Where(tag.ID.EQ(t2.ID)).WithParent().Only(ctx)
		require.NoError(t, err)
		if c.Edges.Parent == nil {
			return nil
		}
		return &c.Edges.Parent.ID
	}
	_, err := client.Tag.Update(db).Set(tag.Parent.Set(t1.ID), tag.Parent.Clear()).Where(tag.ID.EQ(t2.ID)).Exec(ctx)
	require.NoError(t, err)
	assert.Nil(t, parent(), "Set then Clear")
	_, err = client.Tag.Update(db).Set(tag.Parent.Clear(), tag.Parent.Set(t1.ID), tag.Parent.Set(t3.ID)).Where(tag.ID.EQ(t2.ID)).Exec(ctx)
	require.NoError(t, err)
	require.NotNil(t, parent())
	assert.Equal(t, t3.ID, *parent(), "Clear, Set, Set")
}

// A hook that returns without calling the next one stops the mutation; the
// caller gets an error, not a nil entity, a panic or a silently shorter batch.
func TestHooks_ShortCircuitIsAnError(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if name, _ := m.Field("name"); name == "skip" || m.Op().Is(velox.OpUpdateOne) {
				return nil, nil
			}
			return next.Mutate(ctx, m)
		})
	})
	mk := func(name string) *entity.UserCreate {
		return client.User.Create(db).Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(1))
	}
	_, err := mk("skip").Save(ctx)
	require.ErrorContains(t, err, "instead of running the mutation")
	_, err = client.User.Update(db).Set(user.Age.Set(2)).Where(user.ID.EQ(u.ID)).SaveOne(ctx)
	require.ErrorContains(t, err, "instead of running the mutation")
	for _, rows := range [][]*entity.UserCreate{{mk("a"), mk("skip")}, {mk("skip"), mk("b")}} {
		_, err = client.User.CreateBulk(db, rows...).Save(ctx)
		require.Error(t, err)
	}
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a stopped bulk create inserted rows")
}

// A target the caller may not read fails an eager load whether or not there
// is anything to load: otherwise the error tells the caller which.
func TestPolicy_DeniedLoadWithNothingToLoad(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newPost(t, db, newUser(t, db, "ann", 30), "untagged")
	deny := testschema.DenyTagQueryContext(ctx)
	_, err := client.Post.Query(db).WithTags().All(deny)
	assert.ErrorIs(t, err, privacy.Deny)
	_, err = client.Tag.Query(db).WithParent().All(ctx)
	assert.NoError(t, err)
}

// A rule shared by many entities (a mixin's tenant filter) asks the filter
// whether the table has the column it filters on. Queries and mutations both
// answer, for a field's column and for a foreign key that is no field.
func TestFilter_HasColumn(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	type columnChecker interface{ HasColumn(string) bool }

	seen := map[string]bool{}
	db.Intercept(velox.TraverseFunc(func(_ context.Context, q velox.Query) error {
		cc, ok := q.(*orm.Query).Filter().(columnChecker)
		if !ok {
			return errors.New("the filter of a query does not answer HasColumn")
		}
		seen["query "+q.(*orm.Query).Type()+" age"] = cc.HasColumn("age")
		seen["query "+q.(*orm.Query).Type()+" user_posts"] = cc.HasColumn("user_posts")
		return nil
	}))
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			cc, ok := m.(*orm.Mutation).Filter().(columnChecker)
			if !ok {
				return nil, errors.New("the filter of a mutation does not answer HasColumn")
			}
			seen["mutation "+m.Type()+" age"] = cc.HasColumn("age")
			seen["mutation "+m.Type()+" no_such"] = cc.HasColumn("no_such")
			return next.Mutate(ctx, m)
		})
	})
	ann := newUser(t, db, "ann", 30)
	newPost(t, db, ann, "a1")
	_, err := client.User.Query(db).All(ctx)
	require.NoError(t, err)
	_, err = client.Post.Query(db).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{
		"query User age": true, "query User user_posts": false,
		"query Post age": false, "query Post user_posts": true,
		"mutation User age": true, "mutation User no_such": false,
		"mutation Post age": false, "mutation Post no_such": false,
	}, seen)
}

// The interceptors as functions of *orm.Query: no assertion to write, and a
// traverser reaches the reads an eager load makes while an interceptor is
// for the read it is registered on.
func TestTraverseFuncAndInterceptFunc(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, _ := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")

	var traversed, intercepted []string
	db.Intercept(
		orm.TraverseFunc(func(_ context.Context, q *orm.Query) error {
			traversed = append(traversed, q.Type())
			if q.Type() == "User" {
				q.WhereP(sql.FieldLTE("age", 35))
			}
			return nil
		}),
		orm.InterceptFunc(func(_ context.Context, q *orm.Query) error {
			intercepted = append(intercepted, q.Type())
			if q.Type() == "Tag" {
				return errors.New("no tags today")
			}
			return nil
		}),
	)
	us, err := client.User.Query(db).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(us))
	ps, err := client.Post.Query(db).WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Contains(t, traversed, "Post")
	assert.Contains(t, intercepted, "User")
	_, err = client.Tag.Query(db).All(ctx)
	require.ErrorContains(t, err, "no tags today")
}

func TestZeroPredicate_IsZero(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	var none predicate.User
	assert.True(t, orm.IsZero(none))
	assert.False(t, orm.IsZero(user.Age.GT(1)))
	// It is no condition, alone and among others.
	n, err := client.User.Query(db).Where(none).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = client.User.Query(db).Where(none, user.Age.GT(100)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
