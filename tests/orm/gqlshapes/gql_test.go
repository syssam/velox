package gqlshapes

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/orm/gqlshapes/types"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/asset"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/entity"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/migrate"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/org"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/person"
	"github.com/syssam/velox/tests/orm/gqlshapes/velox/predicate"

	_ "modernc.org/sqlite"
)

var (
	ctx = context.Background()
	seq atomic.Int64
)

// world is a database with an org, two people and four assets: one owned by
// the org, one by ann, one by both and one by nobody. bob made the first.
type world struct {
	conn                           *velox.DB
	acme                           *entity.Org
	ann, bob                       *entity.Person
	ofOrg, ofAnn, ofBoth, ofNobody *entity.Asset
	// ctx is a request's: it carries the handle, which is what an edge is
	// resolved on.
	ctx context.Context
}

func open(t *testing.T) *world {
	t.Helper()
	conn, err := velox.Open("sqlite", fmt.Sprintf("file:gqlshapes%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", seq.Add(1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, migrate.Create(ctx, conn))
	w := &world{conn: conn, ctx: velox.WithExecutor(ctx, conn)}
	w.acme, err = velox.Org.Create(conn).Set(org.Name.Set("acme")).Save(ctx)
	require.NoError(t, err)
	newPerson := func(name string) *entity.Person {
		p, err := velox.Person.Create(conn).Set(person.Name.Set(name)).Save(ctx)
		require.NoError(t, err)
		return p
	}
	w.ann, w.bob = newPerson("ann"), newPerson("bob")
	newAsset := func(label string, set ...predicate.AssetSetter) *entity.Asset {
		a, err := velox.Asset.Create(conn).Set(asset.Label.Set(label)).Set(set...).Save(ctx)
		require.NoError(t, err)
		return a
	}
	w.ofOrg = newAsset("of the org", asset.Org.Set(w.acme.ID), asset.Maker.Set(w.bob.ID))
	w.ofAnn = newAsset("of ann", asset.Person.Set(w.ann.ID))
	w.ofBoth = newAsset("of both", asset.Org.Set(w.acme.ID), asset.Person.Set(w.ann.ID))
	w.ofNobody = newAsset("of nobody")
	return w
}

// A field of several edges is the neighbor on the first edge that has one,
// as its own type, and nil where none has: nil as the interface, which a
// nil *Org returned as it is would not be.
func TestFieldOfSeveralEdges(t *testing.T) {
	w := open(t)

	got, err := w.ofOrg.Owner(w.ctx)
	require.NoError(t, err)
	o, ok := got.(*entity.Org)
	require.True(t, ok, "the owner of the org's asset is a %T", got)
	assert.Equal(t, w.acme.ID, o.ID)

	got, err = w.ofAnn.Owner(w.ctx)
	require.NoError(t, err)
	p, ok := got.(*entity.Person)
	require.True(t, ok, "the owner of ann's asset is a %T", got)
	assert.Equal(t, w.ann.ID, p.ID)

	// The edges are asked in the order of the schema.
	got, err = w.ofBoth.Owner(w.ctx)
	require.NoError(t, err)
	assert.IsType(t, &entity.Org{}, got)

	got, err = w.ofNobody.Owner(w.ctx)
	require.NoError(t, err)
	assert.True(t, got == nil, "an asset nobody owns has the owner %#v", got)
}

// What the query loaded is not queried again: with no handle in the context
// a loaded edge resolves, an edge that was not loaded cannot.
func TestFieldOfSeveralEdgesUsesWhatIsLoaded(t *testing.T) {
	w := open(t)
	all, err := velox.Asset.Query(w.conn).WithOrg().WithPerson().WithMaker().OrderBy(asset.ID.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 4)

	got, err := all[0].Owner(ctx)
	require.NoError(t, err)
	assert.IsType(t, &entity.Org{}, got)
	got, err = all[1].Owner(ctx) // the org edge is loaded and empty, the person edge holds ann
	require.NoError(t, err)
	assert.IsType(t, &entity.Person{}, got)
	got, err = all[3].Owner(ctx)
	require.NoError(t, err)
	assert.True(t, got == nil)

	_, err = w.ofOrg.Owner(ctx)
	assert.ErrorIs(t, err, velox.ErrNoExecutor)
}

// An edge under another name resolves as the edge does.
func TestEdgeUnderAnotherName(t *testing.T) {
	w := open(t)

	creator, err := w.ofOrg.Creator(w.ctx)
	require.NoError(t, err)
	maker, err := w.ofOrg.Maker(w.ctx)
	require.NoError(t, err)
	require.NotNil(t, creator)
	assert.Equal(t, w.bob.ID, creator.ID)
	assert.Equal(t, maker.ID, creator.ID)

	creator, err = w.ofAnn.Creator(w.ctx)
	require.NoError(t, err)
	assert.Nil(t, creator)

	// A list: what both owners give their assets as.
	holdings, err := w.acme.Holdings(w.ctx)
	require.NoError(t, err)
	assert.Len(t, holdings, 2)
	holdings, err = w.bob.Holdings(w.ctx)
	require.NoError(t, err)
	assert.Empty(t, holdings)
}

// The entities are of the interface the schema defines and of the union the
// application declares, by the methods an engine looks for; an entity that
// is of neither does not compile as one (compile-time, below).
func TestInterfaceAndUnionMembers(t *testing.T) {
	owners := []entity.Owner{&entity.Org{}, &entity.Person{}}
	assert.Len(t, owners, 2)
	type searchResult interface{ IsSearchResult() }
	results := []searchResult{&entity.Org{}, &entity.Person{}}
	assert.Len(t, results, 2)
	_, isOwner := any(&entity.Asset{}).(entity.Owner)
	assert.False(t, isOwner)
	_, isResult := any(&entity.Asset{}).(searchResult)
	assert.False(t, isResult)
	// Every one is a node of the schema.
	nodes := []velox.Noder{&entity.Org{}, &entity.Person{}, &entity.Asset{}}
	assert.Len(t, nodes, 3)
}

// A nillable bytes field is read through an accessor that gives the bytes,
// or nil: the field itself is a pointer.
func TestNillableBytesAccessor(t *testing.T) {
	w := open(t)
	assert.Nil(t, w.ofOrg.Thumbnail)
	assert.Nil(t, w.ofOrg.ThumbnailOrNil())

	a, err := velox.Asset.Create(w.conn).Set(asset.Label.Set("with a thumbnail"), asset.Thumbnail.Set([]byte{1, 2, 3})).Save(ctx)
	require.NoError(t, err)
	a, err = velox.Asset.Get(ctx, w.conn, a.ID)
	require.NoError(t, err)
	require.NotNil(t, a.Thumbnail)
	assert.Equal(t, []byte{1, 2, 3}, a.ThumbnailOrNil())
}

// The scalar of a JSON field's Go type: the type under the scalar's name in
// the root package, and what writes and reads its value.
func TestJSONScalar(t *testing.T) {
	w := open(t)
	specs := types.Specs{Weight: 1.5, Tags: []string{"fragile"}}
	a, err := velox.Asset.Create(w.conn).Set(asset.Label.Set("with specs"), asset.Specs.Set(specs)).Save(ctx)
	require.NoError(t, err)
	a, err = velox.Asset.Get(ctx, w.conn, a.ID)
	require.NoError(t, err)

	// velox.Specs is the type of the field: one passes for the other.
	holds := func(s velox.Specs) types.Specs { return s }
	out, err := velox.MarshalSpecs(holds(a.Specs))
	require.NoError(t, err)
	assert.JSONEq(t, `{"weight_kg":1.5,"tags":["fragile"]}`, string(out))

	// An engine hands the value over as JSON decodes it.
	in, err := velox.UnmarshalSpecs(map[string]any{"weight_kg": 1.5, "tags": []any{"fragile"}})
	require.NoError(t, err)
	assert.Equal(t, specs, in)
	in, err = velox.UnmarshalSpecs(out)
	require.NoError(t, err)
	assert.Equal(t, specs, in)
	_, err = velox.UnmarshalSpecs(7)
	assert.Error(t, err)

	// A field that may hold nothing.
	out, err = velox.MarshalSpecsPtr(nil)
	require.NoError(t, err)
	assert.Equal(t, "null", string(out))
	ptr, err := velox.UnmarshalSpecsPtr(nil)
	require.NoError(t, err)
	assert.Nil(t, ptr)
	ptr, err = velox.UnmarshalSpecsPtr(map[string]any{"weight_kg": 2.0})
	require.NoError(t, err)
	require.NotNil(t, ptr)
	assert.Equal(t, 2.0, ptr.Weight)
}
