// The declarations of this file name their types on purpose: "var u
// *entity.User = ..." is the assertion that the generated code gave the
// entity that type under that name.
//
//nolint:staticcheck // see above
package shapes

import (
	"context"
	"testing"

	client "github.com/syssam/velox/tests/orm/shapes/db"
	"github.com/syssam/velox/tests/orm/shapes/db/predicate"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/orm"
	nand "github.com/syssam/velox/tests/orm/shapes/db/and"
	nbulk "github.com/syssam/velox/tests/orm/shapes/db/bulk"
	nclash "github.com/syssam/velox/tests/orm/shapes/db/clash"
	ncontext "github.com/syssam/velox/tests/orm/shapes/db/context"
	mcreate "github.com/syssam/velox/tests/orm/shapes/db/create"
	ndbx "github.com/syssam/velox/tests/orm/shapes/db/dbx"
	nedge "github.com/syssam/velox/tests/orm/shapes/db/edge"
	nedges "github.com/syssam/velox/tests/orm/shapes/db/edges"
	"github.com/syssam/velox/tests/orm/shapes/db/entity"
	mentityx "github.com/syssam/velox/tests/orm/shapes/db/entityx"
	nentityx "github.com/syssam/velox/tests/orm/shapes/db/entityxx" // Entity: entityx is taken by the entity Entityx
	nerrors "github.com/syssam/velox/tests/orm/shapes/db/errors"
	nfield "github.com/syssam/velox/tests/orm/shapes/db/field"
	nhttp "github.com/syssam/velox/tests/orm/shapes/db/http"
	nid "github.com/syssam/velox/tests/orm/shapes/db/id"
	ninternalx "github.com/syssam/velox/tests/orm/shapes/db/internalx"
	njson "github.com/syssam/velox/tests/orm/shapes/db/json"
	"github.com/syssam/velox/tests/orm/shapes/db/keywords"
	nkind "github.com/syssam/velox/tests/orm/shapes/db/kind"
	nkindshape "github.com/syssam/velox/tests/orm/shapes/db/kindshape"
	nload "github.com/syssam/velox/tests/orm/shapes/db/load"
	nmath "github.com/syssam/velox/tests/orm/shapes/db/math"
	nmeta "github.com/syssam/velox/tests/orm/shapes/db/meta"
	nmigratex "github.com/syssam/velox/tests/orm/shapes/db/migratex"
	nnot "github.com/syssam/velox/tests/orm/shapes/db/not"
	nodd "github.com/syssam/velox/tests/orm/shapes/db/odd"
	nor "github.com/syssam/velox/tests/orm/shapes/db/or"
	norder "github.com/syssam/velox/tests/orm/shapes/db/order"
	normx "github.com/syssam/velox/tests/orm/shapes/db/ormx"
	npred "github.com/syssam/velox/tests/orm/shapes/db/pred"
	npredicatex "github.com/syssam/velox/tests/orm/shapes/db/predicatex"
	mqueryuser "github.com/syssam/velox/tests/orm/shapes/db/queryuser"
	nruntime "github.com/syssam/velox/tests/orm/shapes/db/runtime"
	nschemainitx "github.com/syssam/velox/tests/orm/shapes/db/schemainitx"
	nset "github.com/syssam/velox/tests/orm/shapes/db/set"
	nsql "github.com/syssam/velox/tests/orm/shapes/db/sql"
	nstrings "github.com/syssam/velox/tests/orm/shapes/db/strings"
	ntable "github.com/syssam/velox/tests/orm/shapes/db/table"
	ntime "github.com/syssam/velox/tests/orm/shapes/db/time"
	ntx "github.com/syssam/velox/tests/orm/shapes/db/tx"
	mupdate "github.com/syssam/velox/tests/orm/shapes/db/update"
	muser "github.com/syssam/velox/tests/orm/shapes/db/user"
	nuserclient "github.com/syssam/velox/tests/orm/shapes/db/userclient"
	nuserorder "github.com/syssam/velox/tests/orm/shapes/db/userorderterm"
	nuserset "github.com/syssam/velox/tests/orm/shapes/db/usersetter"
	nuuid "github.com/syssam/velox/tests/orm/shapes/db/uuid"
	"github.com/syssam/velox/tests/orm/shapes/types"
)

// Every awkwardly named entity is created, queried, updated and deleted
// through its own package: the names only have to stay out of the way. Each
// entity package is imported under an alias (n + its name) because most of
// the names shadow a package this file, or any application, also imports —
// which is the caller's problem, not the generator's.
func TestNames_EveryAwkwardEntityWorks(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	t.Run("And", func(t *testing.T) {
		use(t)
		a := must(client.And.Create(db).Set(nand.Label.Set("a")).Save(ctx))
		rows := must(client.And.CreateBulk(db, client.And.Create(db).Set(nand.Label.Set("b")), client.And.Create(db).Set(nand.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.And = must(client.And.Query(db).Where(nand.Label.EQ("a"), nand.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.And.Update(db).Set(nand.Label.Set("a2")).Where(nand.ID.EQ(a.ID), nand.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.And.Delete(db).Where(nand.Or(nand.Label.EQ("b"), nand.And(nand.Label.EQ("c"), nand.Not(nand.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.And.Query(db).OrderBy(nand.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nand.Table+"\""))
	})

	t.Run("Or", func(t *testing.T) {
		use(t)
		a := must(client.Or.Create(db).Set(nor.Label.Set("a")).Save(ctx))
		rows := must(client.Or.CreateBulk(db, client.Or.Create(db).Set(nor.Label.Set("b")), client.Or.Create(db).Set(nor.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Or = must(client.Or.Query(db).Where(nor.Label.EQ("a"), nor.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Or.Update(db).Set(nor.Label.Set("a2")).Where(nor.ID.EQ(a.ID), nor.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Or.Delete(db).Where(nor.Or(nor.Label.EQ("b"), nor.And(nor.Label.EQ("c"), nor.Not(nor.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Or.Query(db).OrderBy(nor.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nor.Table+"\""))
	})

	t.Run("Not", func(t *testing.T) {
		use(t)
		a := must(client.Not.Create(db).Set(nnot.Label.Set("a")).Save(ctx))
		rows := must(client.Not.CreateBulk(db, client.Not.Create(db).Set(nnot.Label.Set("b")), client.Not.Create(db).Set(nnot.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Not = must(client.Not.Query(db).Where(nnot.Label.EQ("a"), nnot.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Not.Update(db).Set(nnot.Label.Set("a2")).Where(nnot.ID.EQ(a.ID), nnot.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Not.Delete(db).Where(nnot.Or(nnot.Label.EQ("b"), nnot.And(nnot.Label.EQ("c"), nnot.Not(nnot.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Not.Query(db).OrderBy(nnot.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nnot.Table+"\""))
	})

	t.Run("Bulk", func(t *testing.T) {
		use(t)
		a := must(client.Bulk.Create(db).Set(nbulk.Label.Set("a")).Save(ctx))
		rows := must(client.Bulk.CreateBulk(db, client.Bulk.Create(db).Set(nbulk.Label.Set("b")), client.Bulk.Create(db).Set(nbulk.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Bulk = must(client.Bulk.Query(db).Where(nbulk.Label.EQ("a"), nbulk.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Bulk.Update(db).Set(nbulk.Label.Set("a2")).Where(nbulk.ID.EQ(a.ID), nbulk.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Bulk.Delete(db).Where(nbulk.Or(nbulk.Label.EQ("b"), nbulk.And(nbulk.Label.EQ("c"), nbulk.Not(nbulk.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Bulk.Query(db).OrderBy(nbulk.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nbulk.Table+"\""))
	})

	t.Run("Context", func(t *testing.T) {
		use(t)
		a := must(client.Context.Create(db).Set(ncontext.Label.Set("a")).Save(ctx))
		rows := must(client.Context.CreateBulk(db, client.Context.Create(db).Set(ncontext.Label.Set("b")), client.Context.Create(db).Set(ncontext.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Context = must(client.Context.Query(db).Where(ncontext.Label.EQ("a"), ncontext.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Context.Update(db).Set(ncontext.Label.Set("a2")).Where(ncontext.ID.EQ(a.ID), ncontext.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Context.Delete(db).Where(ncontext.Or(ncontext.Label.EQ("b"), ncontext.And(ncontext.Label.EQ("c"), ncontext.Not(ncontext.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Context.Query(db).OrderBy(ncontext.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+ncontext.Table+"\""))
	})

	t.Run("Edge", func(t *testing.T) {
		use(t)
		a := must(client.Edge.Create(db).Set(nedge.Label.Set("a")).Save(ctx))
		rows := must(client.Edge.CreateBulk(db, client.Edge.Create(db).Set(nedge.Label.Set("b")), client.Edge.Create(db).Set(nedge.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Edge = must(client.Edge.Query(db).Where(nedge.Label.EQ("a"), nedge.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Edge.Update(db).Set(nedge.Label.Set("a2")).Where(nedge.ID.EQ(a.ID), nedge.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Edge.Delete(db).Where(nedge.Or(nedge.Label.EQ("b"), nedge.And(nedge.Label.EQ("c"), nedge.Not(nedge.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Edge.Query(db).OrderBy(nedge.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nedge.Table+"\""))
	})

	t.Run("Edges", func(t *testing.T) {
		use(t)
		a := must(client.Edges.Create(db).Set(nedges.Label.Set("a")).Save(ctx))
		rows := must(client.Edges.CreateBulk(db, client.Edges.Create(db).Set(nedges.Label.Set("b")), client.Edges.Create(db).Set(nedges.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Edges = must(client.Edges.Query(db).Where(nedges.Label.EQ("a"), nedges.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Edges.Update(db).Set(nedges.Label.Set("a2")).Where(nedges.ID.EQ(a.ID), nedges.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Edges.Delete(db).Where(nedges.Or(nedges.Label.EQ("b"), nedges.And(nedges.Label.EQ("c"), nedges.Not(nedges.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Edges.Query(db).OrderBy(nedges.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nedges.Table+"\""))
	})

	t.Run("Errors", func(t *testing.T) {
		use(t)
		a := must(client.Errors.Create(db).Set(nerrors.Label.Set("a")).Save(ctx))
		rows := must(client.Errors.CreateBulk(db, client.Errors.Create(db).Set(nerrors.Label.Set("b")), client.Errors.Create(db).Set(nerrors.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Errors = must(client.Errors.Query(db).Where(nerrors.Label.EQ("a"), nerrors.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Errors.Update(db).Set(nerrors.Label.Set("a2")).Where(nerrors.ID.EQ(a.ID), nerrors.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Errors.Delete(db).Where(nerrors.Or(nerrors.Label.EQ("b"), nerrors.And(nerrors.Label.EQ("c"), nerrors.Not(nerrors.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Errors.Query(db).OrderBy(nerrors.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nerrors.Table+"\""))
	})

	t.Run("Field", func(t *testing.T) {
		use(t)
		a := must(client.Field.Create(db).Set(nfield.Label.Set("a")).Save(ctx))
		rows := must(client.Field.CreateBulk(db, client.Field.Create(db).Set(nfield.Label.Set("b")), client.Field.Create(db).Set(nfield.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Field = must(client.Field.Query(db).Where(nfield.Label.EQ("a"), nfield.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Field.Update(db).Set(nfield.Label.Set("a2")).Where(nfield.ID.EQ(a.ID), nfield.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Field.Delete(db).Where(nfield.Or(nfield.Label.EQ("b"), nfield.And(nfield.Label.EQ("c"), nfield.Not(nfield.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Field.Query(db).OrderBy(nfield.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nfield.Table+"\""))
	})

	t.Run("Http", func(t *testing.T) {
		use(t)
		a := must(client.Http.Create(db).Set(nhttp.Label.Set("a")).Save(ctx))
		rows := must(client.Http.CreateBulk(db, client.Http.Create(db).Set(nhttp.Label.Set("b")), client.Http.Create(db).Set(nhttp.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Http = must(client.Http.Query(db).Where(nhttp.Label.EQ("a"), nhttp.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Http.Update(db).Set(nhttp.Label.Set("a2")).Where(nhttp.ID.EQ(a.ID), nhttp.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Http.Delete(db).Where(nhttp.Or(nhttp.Label.EQ("b"), nhttp.And(nhttp.Label.EQ("c"), nhttp.Not(nhttp.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Http.Query(db).OrderBy(nhttp.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nhttp.Table+"\""))
	})

	t.Run("Id", func(t *testing.T) {
		use(t)
		a := must(client.Id.Create(db).Set(nid.Label.Set("a")).Save(ctx))
		rows := must(client.Id.CreateBulk(db, client.Id.Create(db).Set(nid.Label.Set("b")), client.Id.Create(db).Set(nid.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Id = must(client.Id.Query(db).Where(nid.Label.EQ("a"), nid.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Id.Update(db).Set(nid.Label.Set("a2")).Where(nid.ID.EQ(a.ID), nid.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Id.Delete(db).Where(nid.Or(nid.Label.EQ("b"), nid.And(nid.Label.EQ("c"), nid.Not(nid.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Id.Query(db).OrderBy(nid.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nid.Table+"\""))
	})

	t.Run("Json", func(t *testing.T) {
		use(t)
		a := must(client.Json.Create(db).Set(njson.Label.Set("a")).Save(ctx))
		rows := must(client.Json.CreateBulk(db, client.Json.Create(db).Set(njson.Label.Set("b")), client.Json.Create(db).Set(njson.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Json = must(client.Json.Query(db).Where(njson.Label.EQ("a"), njson.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Json.Update(db).Set(njson.Label.Set("a2")).Where(njson.ID.EQ(a.ID), njson.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Json.Delete(db).Where(njson.Or(njson.Label.EQ("b"), njson.And(njson.Label.EQ("c"), njson.Not(njson.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Json.Query(db).OrderBy(njson.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+njson.Table+"\""))
	})

	t.Run("Load", func(t *testing.T) {
		use(t)
		a := must(client.Load.Create(db).Set(nload.Label.Set("a")).Save(ctx))
		rows := must(client.Load.CreateBulk(db, client.Load.Create(db).Set(nload.Label.Set("b")), client.Load.Create(db).Set(nload.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Load = must(client.Load.Query(db).Where(nload.Label.EQ("a"), nload.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Load.Update(db).Set(nload.Label.Set("a2")).Where(nload.ID.EQ(a.ID), nload.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Load.Delete(db).Where(nload.Or(nload.Label.EQ("b"), nload.And(nload.Label.EQ("c"), nload.Not(nload.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Load.Query(db).OrderBy(nload.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nload.Table+"\""))
	})

	t.Run("Math", func(t *testing.T) {
		use(t)
		a := must(client.Math.Create(db).Set(nmath.Label.Set("a")).Save(ctx))
		rows := must(client.Math.CreateBulk(db, client.Math.Create(db).Set(nmath.Label.Set("b")), client.Math.Create(db).Set(nmath.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Math = must(client.Math.Query(db).Where(nmath.Label.EQ("a"), nmath.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Math.Update(db).Set(nmath.Label.Set("a2")).Where(nmath.ID.EQ(a.ID), nmath.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Math.Delete(db).Where(nmath.Or(nmath.Label.EQ("b"), nmath.And(nmath.Label.EQ("c"), nmath.Not(nmath.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Math.Query(db).OrderBy(nmath.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nmath.Table+"\""))
	})

	t.Run("Meta", func(t *testing.T) {
		use(t)
		a := must(client.Meta.Create(db).Set(nmeta.Label.Set("a")).Save(ctx))
		rows := must(client.Meta.CreateBulk(db, client.Meta.Create(db).Set(nmeta.Label.Set("b")), client.Meta.Create(db).Set(nmeta.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Meta = must(client.Meta.Query(db).Where(nmeta.Label.EQ("a"), nmeta.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Meta.Update(db).Set(nmeta.Label.Set("a2")).Where(nmeta.ID.EQ(a.ID), nmeta.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Meta.Delete(db).Where(nmeta.Or(nmeta.Label.EQ("b"), nmeta.And(nmeta.Label.EQ("c"), nmeta.Not(nmeta.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Meta.Query(db).OrderBy(nmeta.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nmeta.Table+"\""))
	})

	t.Run("Entity", func(t *testing.T) {
		use(t)
		a := must(client.Entity.Create(db).Set(nentityx.Label.Set("a")).Save(ctx))
		rows := must(client.Entity.CreateBulk(db, client.Entity.Create(db).Set(nentityx.Label.Set("b")), client.Entity.Create(db).Set(nentityx.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Entity = must(client.Entity.Query(db).Where(nentityx.Label.EQ("a"), nentityx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Entity.Update(db).Set(nentityx.Label.Set("a2")).Where(nentityx.ID.EQ(a.ID), nentityx.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Entity.Delete(db).Where(nentityx.Or(nentityx.Label.EQ("b"), nentityx.And(nentityx.Label.EQ("c"), nentityx.Not(nentityx.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Entity.Query(db).OrderBy(nentityx.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nentityx.Table+"\""))
	})

	// An entity called Internal: its package is internalx, which this test,
	// outside the generated tree, can import.
	t.Run("Internal", func(t *testing.T) {
		use(t)
		a := must(client.Internal.Create(db).Set(ninternalx.Label.Set("a")).Save(ctx))
		var typed *entity.Internal = must(client.Internal.Query(db).Where(ninternalx.Label.EQ("a"), ninternalx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+ninternalx.Table+"\""))
	})

	t.Run("Orm", func(t *testing.T) {
		use(t)
		a := must(client.Orm.Create(db).Set(normx.Label.Set("a")).Save(ctx))
		rows := must(client.Orm.CreateBulk(db, client.Orm.Create(db).Set(normx.Label.Set("b")), client.Orm.Create(db).Set(normx.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Orm = must(client.Orm.Query(db).Where(normx.Label.EQ("a"), normx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Orm.Update(db).Set(normx.Label.Set("a2")).Where(normx.ID.EQ(a.ID), normx.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Orm.Delete(db).Where(normx.Or(normx.Label.EQ("b"), normx.And(normx.Label.EQ("c"), normx.Not(normx.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Orm.Query(db).OrderBy(normx.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+normx.Table+"\""))
	})

	t.Run("Order", func(t *testing.T) {
		use(t)
		a := must(client.Order.Create(db).Set(norder.Label.Set("a")).Save(ctx))
		rows := must(client.Order.CreateBulk(db, client.Order.Create(db).Set(norder.Label.Set("b")), client.Order.Create(db).Set(norder.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Order = must(client.Order.Query(db).Where(norder.Label.EQ("a"), norder.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Order.Update(db).Set(norder.Label.Set("a2")).Where(norder.ID.EQ(a.ID), norder.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Order.Delete(db).Where(norder.Or(norder.Label.EQ("b"), norder.And(norder.Label.EQ("c"), norder.Not(norder.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Order.Query(db).OrderBy(norder.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+norder.Table+"\""))
	})

	t.Run("Pred", func(t *testing.T) {
		use(t)
		a := must(client.Pred.Create(db).Set(npred.Label.Set("a")).Save(ctx))
		rows := must(client.Pred.CreateBulk(db, client.Pred.Create(db).Set(npred.Label.Set("b")), client.Pred.Create(db).Set(npred.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Pred = must(client.Pred.Query(db).Where(npred.Label.EQ("a"), npred.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Pred.Update(db).Set(npred.Label.Set("a2")).Where(npred.ID.EQ(a.ID), npred.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Pred.Delete(db).Where(npred.Or(npred.Label.EQ("b"), npred.And(npred.Label.EQ("c"), npred.Not(npred.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Pred.Query(db).OrderBy(npred.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+npred.Table+"\""))
	})

	t.Run("Runtime", func(t *testing.T) {
		use(t)
		a := must(client.Runtime.Create(db).Set(nruntime.Label.Set("a")).Save(ctx))
		rows := must(client.Runtime.CreateBulk(db, client.Runtime.Create(db).Set(nruntime.Label.Set("b")), client.Runtime.Create(db).Set(nruntime.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Runtime = must(client.Runtime.Query(db).Where(nruntime.Label.EQ("a"), nruntime.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Runtime.Update(db).Set(nruntime.Label.Set("a2")).Where(nruntime.ID.EQ(a.ID), nruntime.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Runtime.Delete(db).Where(nruntime.Or(nruntime.Label.EQ("b"), nruntime.And(nruntime.Label.EQ("c"), nruntime.Not(nruntime.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Runtime.Query(db).OrderBy(nruntime.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nruntime.Table+"\""))
	})

	t.Run("Schemainit", func(t *testing.T) {
		use(t)
		a := must(client.Schemainit.Create(db).Set(nschemainitx.Label.Set("a")).Save(ctx))
		rows := must(client.Schemainit.CreateBulk(db, client.Schemainit.Create(db).Set(nschemainitx.Label.Set("b")), client.Schemainit.Create(db).Set(nschemainitx.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Schemainit = must(client.Schemainit.Query(db).Where(nschemainitx.Label.EQ("a"), nschemainitx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Schemainit.Update(db).Set(nschemainitx.Label.Set("a2")).Where(nschemainitx.ID.EQ(a.ID), nschemainitx.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Schemainit.Delete(db).Where(nschemainitx.Or(nschemainitx.Label.EQ("b"), nschemainitx.And(nschemainitx.Label.EQ("c"), nschemainitx.Not(nschemainitx.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Schemainit.Query(db).OrderBy(nschemainitx.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nschemainitx.Table+"\""))
	})

	t.Run("Set", func(t *testing.T) {
		use(t)
		a := must(client.Set.Create(db).Set(nset.Label.Set("a")).Save(ctx))
		rows := must(client.Set.CreateBulk(db, client.Set.Create(db).Set(nset.Label.Set("b")), client.Set.Create(db).Set(nset.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Set = must(client.Set.Query(db).Where(nset.Label.EQ("a"), nset.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Set.Update(db).Set(nset.Label.Set("a2")).Where(nset.ID.EQ(a.ID), nset.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Set.Delete(db).Where(nset.Or(nset.Label.EQ("b"), nset.And(nset.Label.EQ("c"), nset.Not(nset.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Set.Query(db).OrderBy(nset.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nset.Table+"\""))
	})

	t.Run("Sql", func(t *testing.T) {
		use(t)
		a := must(client.Sql.Create(db).Set(nsql.Label.Set("a")).Save(ctx))
		rows := must(client.Sql.CreateBulk(db, client.Sql.Create(db).Set(nsql.Label.Set("b")), client.Sql.Create(db).Set(nsql.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Sql = must(client.Sql.Query(db).Where(nsql.Label.EQ("a"), nsql.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Sql.Update(db).Set(nsql.Label.Set("a2")).Where(nsql.ID.EQ(a.ID), nsql.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Sql.Delete(db).Where(nsql.Or(nsql.Label.EQ("b"), nsql.And(nsql.Label.EQ("c"), nsql.Not(nsql.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Sql.Query(db).OrderBy(nsql.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nsql.Table+"\""))
	})

	t.Run("Strings", func(t *testing.T) {
		use(t)
		a := must(client.Strings.Create(db).Set(nstrings.Label.Set("a")).Save(ctx))
		rows := must(client.Strings.CreateBulk(db, client.Strings.Create(db).Set(nstrings.Label.Set("b")), client.Strings.Create(db).Set(nstrings.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Strings = must(client.Strings.Query(db).Where(nstrings.Label.EQ("a"), nstrings.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Strings.Update(db).Set(nstrings.Label.Set("a2")).Where(nstrings.ID.EQ(a.ID), nstrings.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Strings.Delete(db).Where(nstrings.Or(nstrings.Label.EQ("b"), nstrings.And(nstrings.Label.EQ("c"), nstrings.Not(nstrings.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Strings.Query(db).OrderBy(nstrings.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nstrings.Table+"\""))
	})

	t.Run("Table", func(t *testing.T) {
		use(t)
		a := must(client.Table.Create(db).Set(ntable.Label.Set("a")).Save(ctx))
		rows := must(client.Table.CreateBulk(db, client.Table.Create(db).Set(ntable.Label.Set("b")), client.Table.Create(db).Set(ntable.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Table = must(client.Table.Query(db).Where(ntable.Label.EQ("a"), ntable.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Table.Update(db).Set(ntable.Label.Set("a2")).Where(ntable.ID.EQ(a.ID), ntable.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Table.Delete(db).Where(ntable.Or(ntable.Label.EQ("b"), ntable.And(ntable.Label.EQ("c"), ntable.Not(ntable.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Table.Query(db).OrderBy(ntable.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+ntable.Table+"\""))
	})

	t.Run("Time", func(t *testing.T) {
		use(t)
		a := must(client.Time.Create(db).Set(ntime.Label.Set("a")).Save(ctx))
		rows := must(client.Time.CreateBulk(db, client.Time.Create(db).Set(ntime.Label.Set("b")), client.Time.Create(db).Set(ntime.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Time = must(client.Time.Query(db).Where(ntime.Label.EQ("a"), ntime.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Time.Update(db).Set(ntime.Label.Set("a2")).Where(ntime.ID.EQ(a.ID), ntime.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Time.Delete(db).Where(ntime.Or(ntime.Label.EQ("b"), ntime.And(ntime.Label.EQ("c"), ntime.Not(ntime.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Time.Query(db).OrderBy(ntime.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+ntime.Table+"\""))
	})

	t.Run("Tx", func(t *testing.T) {
		use(t)
		a := must(client.Tx.Create(db).Set(ntx.Label.Set("a")).Save(ctx))
		rows := must(client.Tx.CreateBulk(db, client.Tx.Create(db).Set(ntx.Label.Set("b")), client.Tx.Create(db).Set(ntx.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Tx = must(client.Tx.Query(db).Where(ntx.Label.EQ("a"), ntx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Tx.Update(db).Set(ntx.Label.Set("a2")).Where(ntx.ID.EQ(a.ID), ntx.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Tx.Delete(db).Where(ntx.Or(ntx.Label.EQ("b"), ntx.And(ntx.Label.EQ("c"), ntx.Not(ntx.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Tx.Query(db).OrderBy(ntx.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+ntx.Table+"\""))
	})

	t.Run("UUID", func(t *testing.T) {
		use(t)
		a := must(client.UUID.Create(db).Set(nuuid.Label.Set("a")).Save(ctx))
		rows := must(client.UUID.CreateBulk(db, client.UUID.Create(db).Set(nuuid.Label.Set("b")), client.UUID.Create(db).Set(nuuid.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.UUID = must(client.UUID.Query(db).Where(nuuid.Label.EQ("a"), nuuid.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.UUID.Update(db).Set(nuuid.Label.Set("a2")).Where(nuuid.ID.EQ(a.ID), nuuid.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.UUID.Delete(db).Where(nuuid.Or(nuuid.Label.EQ("b"), nuuid.And(nuuid.Label.EQ("c"), nuuid.Not(nuuid.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.UUID.Query(db).OrderBy(nuuid.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nuuid.Table+"\""))
	})

	t.Run("Kind", func(t *testing.T) {
		use(t)
		a := must(client.Kind.Create(db).Set(nkind.Label.Set("a")).Save(ctx))
		rows := must(client.Kind.CreateBulk(db, client.Kind.Create(db).Set(nkind.Label.Set("b")), client.Kind.Create(db).Set(nkind.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Kind = must(client.Kind.Query(db).Where(nkind.Label.EQ("a"), nkind.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Kind.Update(db).Set(nkind.Label.Set("a2")).Where(nkind.ID.EQ(a.ID), nkind.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Kind.Delete(db).Where(nkind.Or(nkind.Label.EQ("b"), nkind.And(nkind.Label.EQ("c"), nkind.Not(nkind.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Kind.Query(db).OrderBy(nkind.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nkind.Table+"\""))
	})

	t.Run("KindShape", func(t *testing.T) {
		use(t)
		a := must(client.KindShape.Create(db).Set(nkindshape.Label.Set("a")).Save(ctx))
		rows := must(client.KindShape.CreateBulk(db, client.KindShape.Create(db).Set(nkindshape.Label.Set("b")), client.KindShape.Create(db).Set(nkindshape.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.KindShape = must(client.KindShape.Query(db).Where(nkindshape.Label.EQ("a"), nkindshape.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.KindShape.Update(db).Set(nkindshape.Label.Set("a2")).Where(nkindshape.ID.EQ(a.ID), nkindshape.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.KindShape.Delete(db).Where(nkindshape.Or(nkindshape.Label.EQ("b"), nkindshape.And(nkindshape.Label.EQ("c"), nkindshape.Not(nkindshape.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.KindShape.Query(db).OrderBy(nkindshape.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+nkindshape.Table+"\""))
	})

	t.Run("Create", func(t *testing.T) {
		use(t)
		a := must(client.Create.Create(db).Set(mcreate.Label.Set("a")).Save(ctx))
		rows := must(client.Create.CreateBulk(db, client.Create.Create(db).Set(mcreate.Label.Set("b")), client.Create.Create(db).Set(mcreate.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Create = must(client.Create.Query(db).Where(mcreate.Label.EQ("a"), mcreate.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Create.Update(db).Set(mcreate.Label.Set("a2")).Where(mcreate.ID.EQ(a.ID), mcreate.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Create.Delete(db).Where(mcreate.Or(mcreate.Label.EQ("b"), mcreate.And(mcreate.Label.EQ("c"), mcreate.Not(mcreate.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Create.Query(db).OrderBy(mcreate.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+mcreate.Table+"\""))
	})

	t.Run("Update", func(t *testing.T) {
		use(t)
		a := must(client.Update.Create(db).Set(mupdate.Label.Set("a")).Save(ctx))
		rows := must(client.Update.CreateBulk(db, client.Update.Create(db).Set(mupdate.Label.Set("b")), client.Update.Create(db).Set(mupdate.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Update = must(client.Update.Query(db).Where(mupdate.Label.EQ("a"), mupdate.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Update.Update(db).Set(mupdate.Label.Set("a2")).Where(mupdate.ID.EQ(a.ID), mupdate.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Update.Delete(db).Where(mupdate.Or(mupdate.Label.EQ("b"), mupdate.And(mupdate.Label.EQ("c"), mupdate.Not(mupdate.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Update.Query(db).OrderBy(mupdate.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+mupdate.Table+"\""))
	})

	t.Run("Entityx", func(t *testing.T) {
		use(t)
		a := must(client.Entityx.Create(db).Set(mentityx.Label.Set("a")).Save(ctx))
		rows := must(client.Entityx.CreateBulk(db, client.Entityx.Create(db).Set(mentityx.Label.Set("b")), client.Entityx.Create(db).Set(mentityx.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.Entityx = must(client.Entityx.Query(db).Where(mentityx.Label.EQ("a"), mentityx.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.Entityx.Update(db).Set(mentityx.Label.Set("a2")).Where(mentityx.ID.EQ(a.ID), mentityx.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.Entityx.Delete(db).Where(mentityx.Or(mentityx.Label.EQ("b"), mentityx.And(mentityx.Label.EQ("c"), mentityx.Not(mentityx.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.Entityx.Query(db).OrderBy(mentityx.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+mentityx.Table+"\""))
	})

	t.Run("QueryUser", func(t *testing.T) {
		use(t)
		a := must(client.QueryUser.Create(db).Set(mqueryuser.Label.Set("a")).Save(ctx))
		rows := must(client.QueryUser.CreateBulk(db, client.QueryUser.Create(db).Set(mqueryuser.Label.Set("b")), client.QueryUser.Create(db).Set(mqueryuser.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.QueryUser = must(client.QueryUser.Query(db).Where(mqueryuser.Label.EQ("a"), mqueryuser.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.QueryUser.Update(db).Set(mqueryuser.Label.Set("a2")).Where(mqueryuser.ID.EQ(a.ID), mqueryuser.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.QueryUser.Delete(db).Where(mqueryuser.Or(mqueryuser.Label.EQ("b"), mqueryuser.And(mqueryuser.Label.EQ("c"), mqueryuser.Not(mqueryuser.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.QueryUser.Query(db).OrderBy(mqueryuser.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+mqueryuser.Table+"\""))
	})

	t.Run("User", func(t *testing.T) {
		use(t)
		a := must(client.User.Create(db).Set(muser.Label.Set("a")).Save(ctx))
		rows := must(client.User.CreateBulk(db, client.User.Create(db).Set(muser.Label.Set("b")), client.User.Create(db).Set(muser.Label.Set("c"))).Save(ctx))
		require.Len(t, rows, 2)
		var typed *entity.User = must(client.User.Query(db).Where(muser.Label.EQ("a"), muser.ID.EQ(a.ID)).Only(ctx))
		assert.Equal(t, "a", typed.Label)
		got := must(client.User.Update(db).Set(muser.Label.Set("a2")).Where(muser.ID.EQ(a.ID), muser.Label.NEQ("zz")).SaveOne(ctx))
		assert.Equal(t, "a2", got.Label)
		n := must(client.User.Delete(db).Where(muser.Or(muser.Label.EQ("b"), muser.And(muser.Label.EQ("c"), muser.Not(muser.ID.EQ(a.ID))))).Exec(ctx))
		assert.Equal(t, 2, n)
		assert.Equal(t, []int{a.ID}, must(client.User.Query(db).OrderBy(muser.Label.Desc()).IDs(ctx)))
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM \""+muser.Table+"\""))
	})
}

// Entities named after a package their own generated package imports, and
// an entity X with an enum field y next to an entity XY.
func TestNames_PackageAndTypeCollisions(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	// time.Time: a package named time, importing time, with a column Time.
	tm := must(client.Time.Create(db).Set(ntime.Label.Set("t")).Save(ctx))
	assert.False(t, tm.Time.IsZero(), "the default func")
	tm = must(client.Time.Update(db).Set(ntime.Time.Set(t0)).Where(ntime.Time.GT(t0)).SaveOne(ctx))
	assert.True(t, tm.Time.Equal(t0))
	assert.Equal(t, 1, must(client.Time.Query(db).Where(ntime.Time.EQ(t0)).OrderBy(ntime.Time.Desc()).Count(ctx)))

	// uuid.UUID likewise.
	u := must(client.UUID.Create(db).Set(nuuid.Label.Set("u")).Save(ctx))
	assert.NotEqual(t, uuid.Nil, u.UUID)
	u = must(client.UUID.Update(db).Set(nuuid.UUID.Set(u1)).Where(nuuid.ID.EQ(u.ID)).SaveOne(ctx))
	assert.Equal(t, u1, u.UUID)
	assert.Equal(t, "u", must(client.UUID.Query(db).Where(nuuid.UUID.In(u1, u2)).Only(ctx)).Label)

	// Kind.shape is an enum whose type would be entity.KindShape — the entity.
	k := must(client.Kind.Create(db).Set(nkind.Label.Set("k")).Save(ctx))
	assert.Equal(t, nkind.ShapeRound, k.Shape)
	var shapeType nkind.ShapeEnum = nkind.ShapeFlat
	k = must(client.Kind.Update(db).Set(nkind.Shape.Set(shapeType)).Where(nkind.ID.EQ(k.ID)).SaveOne(ctx))
	assert.Equal(t, nkind.ShapeFlat, k.Shape)
	_, err := client.Kind.Update(db).Set(nkind.Shape.Set("cube")).Where(nkind.ID.EQ(k.ID)).Exec(ctx)
	var verr *orm.ValidationError
	require.ErrorAs(t, err, &verr)
	// ...and Kind.shapes is an edge to it.
	var ks *entity.KindShape = must(client.KindShape.Create(db).Set(nkindshape.Label.Set("s1"), nkindshape.Kind.Set(k.ID)).Save(ctx))
	must(client.KindShape.Create(db).Set(nkindshape.Label.Set("s2")).Save(ctx))
	kinds := must(client.Kind.Query(db).Where(nkind.Shapes.HasWith(nkindshape.Label.EQ("s1")), nkind.Shape.EQ(nkind.ShapeFlat)).WithShapes().All(ctx))
	require.Len(t, kinds, 1)
	require.Len(t, kinds[0].Edges.Shapes, 1)
	assert.Equal(t, ks.ID, kinds[0].Edges.Shapes[0].ID)
	assert.Equal(t, "k", must(ks.QueryKind(db).Only(ctx)).Label)
	assert.Equal(t, "s1", must(k.QueryShapes(db).Only(ctx)).Label)
}

// Fields and edges named like Go keywords, SQL keywords, predeclared
// identifiers and the identifiers of the generated code itself, on a table
// named "select".
func TestNames_KeywordFieldsAndEdges(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	assert.Equal(t, "select", keywords.Table)

	a := must(client.Keywords.Create(db).Save(ctx))
	b := must(client.Keywords.Create(db).Set(
		keywords.Type.Set("type"), keywords.Func.Set("func"), keywords.Range.Set("range"), keywords.Select.Set("select"),
		keywords.From.Set("from"), keywords.Where.Set("where"), keywords.Order.Set("order"), keywords.Group.Set("group"),
		keywords.Default.Set("default"), keywords.TableField.Set("table"), keywords.Index.Set("index"), keywords.Key.Set("key"),
		keywords.Values.Set("values"), keywords.Query.Set("query"), keywords.Create.Set("create"),
		keywords.CreateBulk.Set("create_bulk"), keywords.Update.Set("update"), keywords.Delete.Set("delete"),
		keywords.Set.Set("set"), keywords.AndField.Set("and"), keywords.OrField.Set("or"), keywords.NotField.Set("not"),
		keywords.Meta.Set("meta"), keywords.Load.Set("load"), keywords.Pred.Set("pred"), keywords.Edge.Set("edge"),
		keywords.Model.Set("model"), keywords.Orm.Set("orm"), keywords.Context.Set("context"), keywords.Tx.Set("tx"),
		keywords.String.Set("string"), keywords.Int.Set(7), keywords.Error.Set("error"), keywords.Len.Set("len"),
		keywords.New.Set("new"), keywords.StringColumn.Set("string_column"), keywords.IntColumn.Set(8),
		keywords.To.Set("to"), keywords.Clear.Set("clear"), keywords.Asc.Set("asc"), keywords.Desc.Set("desc"),
		keywords.Eq.Set("eq"), keywords.In.Set("in"), keywords.Has.Set("has"),
	).Save(ctx))

	// Defaults of the first row, values of the second, read back.
	rows := must(client.Keywords.Query(db).OrderBy(keywords.ID.Asc()).All(ctx))
	require.Len(t, rows, 2)
	for name, r := range map[string]*entity.Keywords{"created": a, "queried": rows[0]} {
		assert.Equal(t, []any{"t", "f", "r", "s", "f", "w", "o", "g", "d", "t", "i", "k", "v", "q", "c", "cb", "u", "d", "s", "a", "o", "n", "m", "l", "p", "e", "m", "o", "c", "t", "s", 1, "e", "l", "n", "sc", 2, "t", (*string)(nil), "a", "d", "e", "i", "h"},
			[]any{r.Type, r.Func, r.Range, r.Select, r.From, r.Where, r.Order, r.Group, r.Default, r.Table, r.Index, r.Key, r.Values, r.Query, r.Create, r.CreateBulk, r.Update, r.Delete, r.Set, r.And, r.Or, r.Not, r.Meta, r.Load, r.Pred, r.Edge, r.Model, r.Orm, r.Context, r.Tx, r.String, r.Int, r.Error, r.Len, r.New, r.StringColumn, r.IntColumn, r.To, r.Clear, r.Asc, r.Desc, r.Eq, r.In, r.Has}, name)
	}
	for name, r := range map[string]*entity.Keywords{"created": b, "queried": rows[1]} {
		require.NotNil(t, r.Clear, name)
		assert.Equal(t, []any{"type", "func", "range", "select", "from", "where", "order", "group", "default", "table", "index", "key", "values", "query", "create", "create_bulk", "update", "delete", "set", "and", "or", "not", "meta", "load", "pred", "edge", "model", "orm", "context", "tx", "string", 7, "error", "len", "new", "string_column", 8, "to", "clear", "asc", "desc", "eq", "in", "has"},
			[]any{r.Type, r.Func, r.Range, r.Select, r.From, r.Where, r.Order, r.Group, r.Default, r.Table, r.Index, r.Key, r.Values, r.Query, r.Create, r.CreateBulk, r.Update, r.Delete, r.Set, r.And, r.Or, r.Not, r.Meta, r.Load, r.Pred, r.Edge, r.Model, r.Orm, r.Context, r.Tx, r.String, r.Int, r.Error, r.Len, r.New, r.StringColumn, r.IntColumn, r.To, *r.Clear, r.Asc, r.Desc, r.Eq, r.In, r.Has}, name)
	}

	// Predicates, order and assignments on the keyword columns.
	got := must(client.Keywords.Query(db).Where(
		keywords.Select.EQ("select"), keywords.From.NEQ("f"), keywords.Where.In("where", "x"), keywords.Order.HasPrefix("ord"),
		keywords.Group.Contains("rou"), keywords.TableField.EQ("table"), keywords.AndField.EQ("and"),
		keywords.Or(keywords.OrField.EQ("or"), keywords.NotField.EQ("zz")), keywords.Not(keywords.Default.EQ("d")),
		keywords.Int.GT(1), keywords.Clear.NotNil(), keywords.In.In("in"), keywords.Has.EQ("has"), keywords.To.EQ("to"),
	).OrderBy(keywords.Order.Desc(), keywords.Group.Asc(), keywords.Select.Asc(), keywords.Desc.Desc(), keywords.Asc.Asc()).Only(ctx))
	assert.Equal(t, b.ID, got.ID)
	got = must(client.Keywords.Update(db).Set(
		keywords.Select.Set("s2"), keywords.From.Set("f2"), keywords.Where.Set("w2"), keywords.Order.Set("o2"), keywords.Group.Set("g2"),
		keywords.TableField.Set("t2"), keywords.Default.Set("d2"), keywords.Set.Set("set2"), keywords.Int.Add(3), keywords.Clear.Clear(),
		keywords.To.Set("to2"), keywords.Query.Set("q2"), keywords.Update.Set("u2"), keywords.Delete.Set("del2"),
	).Where(keywords.Where.EQ("where"), keywords.Order.EQ("order")).SaveOne(ctx))
	assert.Equal(t, []any{"s2", "f2", "w2", "o2", "g2", "t2", "d2", "set2", 10, (*string)(nil), "to2", "q2", "u2", "del2"},
		[]any{got.Select, got.From, got.Where, got.Order, got.Group, got.Table, got.Default, got.Set, got.Int, got.Clear, got.To, got.Query, got.Update, got.Delete})
	assert.Equal(t, 1, must(client.Keywords.Delete(db).Where(keywords.Select.EQ("s2"), keywords.Group.EQ("g2")).Exec(ctx)))

	// Edges named fields, columns, loaded, with and all.
	f1 := must(client.Field.Create(db).Set(nfield.Label.Set("f1")).Save(ctx))
	f2 := must(client.Field.Create(db).Set(nfield.Label.Set("f2")).Save(ctx))
	f3 := must(client.Field.Create(db).Set(nfield.Label.Set("f3")).Save(ctx))
	k := must(client.Keywords.Create(db).Set(keywords.Fields.Add(f1.ID, f2.ID), keywords.Columns.Set(f1.ID), keywords.Loaded.Add(f3.ID), keywords.With.Add(f2.ID), keywords.All.Set(f3.ID)).Save(ctx))
	loaded := must(client.Keywords.Query(db).Where(keywords.ID.EQ(k.ID), keywords.Fields.Has(), keywords.Columns.HasWith(nfield.Label.EQ("f1")), keywords.All.Has()).
		WithFields(client.Field.Query(db).OrderBy(nfield.Label.Asc())).WithColumns().WithLoaded().WithWith().WithAll().Only(ctx))
	require.Len(t, loaded.Edges.Fields, 2)
	assert.Equal(t, "f2", loaded.Edges.Fields[1].Label)
	assert.Equal(t, "f1", loaded.Edges.Columns.Label)
	require.Len(t, loaded.Edges.Loaded, 1)
	assert.Equal(t, "f3", loaded.Edges.Loaded[0].Label)
	require.Len(t, loaded.Edges.With, 1)
	assert.Equal(t, "f2", loaded.Edges.With[0].Label)
	assert.Equal(t, "f3", loaded.Edges.All.Label)
	assert.Equal(t, 2, must(k.QueryFields(db).Count(ctx)))
	assert.Equal(t, "f1", must(k.QueryColumns(db).Only(ctx)).Label)
	assert.Equal(t, "f3", must(k.QueryLoaded(db).Only(ctx)).Label)
	assert.Equal(t, "f2", must(k.QueryWith(db).Only(ctx)).Label)
	assert.Equal(t, "f3", must(k.QueryAll(db).Only(ctx)).Label)
	must(client.Keywords.Update(db).Set(keywords.Fields.Remove(f1.ID), keywords.Columns.Clear(), keywords.With.Clear(), keywords.All.Set(f2.ID)).Where(keywords.ID.EQ(k.ID)).Exec(ctx))
	loaded = must(client.Keywords.Query(db).Where(keywords.ID.EQ(k.ID)).WithFields().WithColumns().WithWith().WithAll().Only(ctx))
	require.Len(t, loaded.Edges.Fields, 1)
	_, err := loaded.Edges.ColumnsOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Empty(t, loaded.Edges.With)
	assert.Equal(t, "f2", loaded.Edges.All.Label)
}

// Identifiers that come close to each other: JSON types naming another
// package inside a composite type, an enum next to fields named like its
// type alias and its value constant, edges named like generated identifiers.
func TestNames_Odd(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	f1 := must(client.Field.Create(db).Set(nfield.Label.Set("f1")).Save(ctx))
	f2 := must(client.Field.Create(db).Set(nfield.Label.Set("f2")).Save(ctx))
	f3 := must(client.Field.Create(db).Set(nfield.Label.Set("f3")).Save(ctx))

	a := must(client.Odd.Create(db).Save(ctx))
	assert.Equal(t, nodd.RoleUser, a.Role)
	assert.Equal(t, "rt", a.RoleType)
	assert.Equal(t, "ra", a.RoleAdmin)
	assert.Equal(t, types.TierFree, a.Tier)
	b := must(client.Odd.Create(db).Set(
		nodd.ByName.Set(map[string]types.Address{"home": {City: "H"}}), nodd.List.Set([]types.Address{{Zip: 1}, {Zip: 2}}),
		nodd.Ptrs.Set([]*types.Address{{City: "P"}, nil}), nodd.Role.Set(nodd.RoleAdminValue), nodd.RoleType.Set("x"), nodd.RoleAdmin.Set("y"),
		nodd.Tier.Set(types.TierPro),
		nodd.IDEdge.Add(f1.ID), nodd.Query.Add(f2.ID), nodd.Edges.Add(f3.ID), nodd.TableEdge.Set(f1.ID), nodd.Model.Set(f2.ID),
	).Save(ctx))
	must(client.Odd.Create(db).Save(ctx))

	rows := must(client.Odd.Query(db).OrderBy(nodd.ID.Asc()).WithID().WithQuery().WithEdges().WithTable().WithModel().All(ctx))
	require.Len(t, rows, 3)
	for _, i := range []int{0, 2} {
		assert.Nil(t, rows[i].ByName)
		assert.Nil(t, rows[i].List)
		assert.Nil(t, rows[i].Ptrs)
		assert.Empty(t, rows[i].Edges.ID)
		assert.Empty(t, rows[i].Edges.Edges)
		_, err := rows[i].Edges.TableOrErr()
		assert.ErrorIs(t, err, orm.ErrNotFound)
	}
	r := rows[1]
	assert.Equal(t, b.ID, r.ID)
	assert.Equal(t, map[string]types.Address{"home": {City: "H"}}, r.ByName)
	assert.Equal(t, []types.Address{{Zip: 1}, {Zip: 2}}, r.List)
	require.Len(t, r.Ptrs, 2)
	assert.Equal(t, "P", r.Ptrs[0].City)
	assert.Nil(t, r.Ptrs[1])
	assert.Equal(t, []any{nodd.RoleAdminValue, "x", "y", types.TierPro}, []any{r.Role, r.RoleType, r.RoleAdmin, r.Tier})
	assert.Equal(t, "f1", r.Edges.ID[0].Label)
	assert.Equal(t, "f2", r.Edges.Query[0].Label)
	assert.Equal(t, "f3", r.Edges.Edges[0].Label)
	assert.Equal(t, "f1", r.Edges.Table.Label)
	assert.Equal(t, "f2", r.Edges.Model.Label)
	assert.Equal(t, "f1", must(b.QueryID(db).Only(ctx)).Label)
	assert.Equal(t, "f2", must(b.QueryQuery(db).Only(ctx)).Label)
	assert.Equal(t, "f3", must(b.QueryEdges(db).Only(ctx)).Label)
	assert.Equal(t, "f1", must(b.QueryTable(db).Only(ctx)).Label)
	assert.Equal(t, "f2", must(b.QueryModel(db).Only(ctx)).Label)
	ids := must(client.Odd.Query(db).Where(nodd.IDEdge.Has(), nodd.Query.HasWith(nfield.Label.EQ("f2")), nodd.Edges.Has(), nodd.TableEdge.Has(), nodd.Model.Has(),
		nodd.Role.EQ(nodd.RoleAdminValue), nodd.RoleType.EQ("x"), nodd.RoleAdmin.EQ("y")).IDs(ctx))
	assert.Equal(t, []int{b.ID}, ids)
}

// Names that collide with generated identifiers are told apart by a suffix
// on the later one; the test names each of them, so a change of the scheme
// does not compile.
func TestNames_ClashesAreRenamed(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	// Enum fields named like the per-entity types of the model: the enum types
	// are in the entity package and no longer collide with anything there.
	var (
		_ nclash.OrderEnum           = nclash.OrderDesc
		_ nclash.SetEnum             = nclash.SetB
		_ nclash.QueryEnum           = nclash.QueryB
		_ nclash.CreateEnum          = nclash.CreateSingle
		_ nclash.UpdateEnum          = nclash.UpdateB
		_ nclash.LoadEnum            = nclash.LoadHigh
		_ nclash.EdgeEnum            = nclash.EdgeB
		_ nclash.MetaEnum            = nclash.MetaB
		_ nclash.PredEnum            = nclash.PredB
		_ *entity.ClashQuery         = client.Clash.Query(nil)
		_ *entity.ClashCreate        = client.Clash.Create(nil)
		_ *entity.ClashUpdate        = client.Clash.Update(nil)
		_ []predicate.ClashOrderTerm = []predicate.ClashOrderTerm{nclash.Order.Asc()}
		_ []predicate.ClashSetter    = []predicate.ClashSetter{nclash.Set.Set(nclash.SetA)}
		_ []predicate.Clash          = []predicate.Clash{nclash.Pred.EQ(nclash.PredA)}
		_ nclash.KindEnum            = nclash.KindType // the value "type" of kind keeps its name; the alias gives way
		_ nclash.AEnum               = nclash.ABC      // a = "b_c"
		_ nclash.ABEnum              = nclash.ABCValue // a_b = "c"
	)
	assert.Equal(t, "bulk", string(nclash.CreateBulk), "the value bulk of create is not the CreateBulk constructor")
	assert.Equal(t, "type", string(nclash.KindType))
	assert.Equal(t, "b_c", string(nclash.ABC))
	assert.Equal(t, "c", string(nclash.ABCValue))

	f1 := must(client.Field.Create(db).Set(nfield.Label.Set("f1")).Save(ctx))
	f2 := must(client.Field.Create(db).Set(nfield.Label.Set("f2")).Save(ctx))
	a := must(client.Clash.Create(db).Save(ctx))
	b := must(client.Clash.Create(db).Set(
		nclash.Order.Set(nclash.OrderDesc), nclash.Set.Set(nclash.SetB), nclash.Query.Set(nclash.QueryB), nclash.Create.Set(nclash.CreateBulk),
		nclash.Update.Set(nclash.UpdateB), nclash.Load.Set(nclash.LoadHigh), nclash.Edge.Set(nclash.EdgeB), nclash.Meta.Set(nclash.MetaB),
		nclash.Pred.Set(nclash.PredB), nclash.Kind.Set(nclash.KindType), nclash.A.Set(nclash.ABC), nclash.AB.Set(nclash.ABCValue),
		nclash.UserName.Set("u"), nclash.QueryOthersField.Set("q"), nclash.Price.Set(types.Decimal{Cents: 250}),
		nclash.Others.Add(f1.ID), nclash.OthersOrErrEdge.Add(f2.ID),
	).Save(ctx))
	rows := must(client.Clash.Query(db).OrderBy(nclash.ID.Asc()).WithOthers().WithOthersOrErrEdge().All(ctx))
	require.Len(t, rows, 2)
	for name, r := range map[string]*entity.Clash{"created": a, "queried": rows[0]} {
		assert.Equal(t, []any{nclash.OrderAsc, nclash.SetA, nclash.QueryA, nclash.CreateSingle, nclash.UpdateA, nclash.LoadLow, nclash.EdgeA, nclash.MetaA, nclash.PredA, nclash.KindOther, nclash.AX, nclash.ABY, "u1", "qo"},
			[]any{r.Order, r.Set, r.Query, r.Create, r.Update, r.Load, r.Edge, r.Meta, r.Pred, r.Kind, r.A, r.AB, r.UserName, r.QueryOthersField}, name)
		// A literal default of a struct type is parsed through its Scan.
		assert.Equal(t, types.Decimal{Cents: 100}, r.Price, name)
	}
	for name, r := range map[string]*entity.Clash{"created": b, "queried": rows[1]} {
		assert.Equal(t, []any{nclash.OrderDesc, nclash.SetB, nclash.QueryB, nclash.CreateBulk, nclash.UpdateB, nclash.LoadHigh, nclash.EdgeB, nclash.MetaB, nclash.PredB, nclash.KindType, nclash.ABC, nclash.ABCValue, "u", "q"},
			[]any{r.Order, r.Set, r.Query, r.Create, r.Update, r.Load, r.Edge, r.Meta, r.Pred, r.Kind, r.A, r.AB, r.UserName, r.QueryOthersField}, name)
		assert.Equal(t, types.Decimal{Cents: 250}, r.Price, name)
	}
	assert.Equal(t, "1.00", rawValue[string](t, db, "SELECT price FROM clashes WHERE id = ?", a.ID))
	assert.Equal(t, "b_c", rawValue[string](t, db, "SELECT a FROM clashes WHERE id = ?", b.ID))
	assert.Equal(t, "c", rawValue[string](t, db, "SELECT a_b FROM clashes WHERE id = ?", b.ID))

	// The edge others and the edge others_or_err, and the field query_others
	// next to the method QueryOthers.
	others, err := rows[1].Edges.OthersOrErr()
	require.NoError(t, err)
	require.Len(t, others, 1)
	assert.Equal(t, "f1", others[0].Label)
	othersOrErr, err := rows[1].Edges.OthersOrErrEdgeOrErr()
	require.NoError(t, err)
	require.Len(t, othersOrErr, 1)
	assert.Equal(t, "f2", othersOrErr[0].Label)
	assert.Equal(t, "f1", must(b.QueryOthers(db).Only(ctx)).Label)
	assert.Equal(t, "f2", must(b.QueryOthersOrErrEdge(db).Only(ctx)).Label)

	// Predicates, order, update and validation through the renamed ones.
	ids := must(client.Clash.Query(db).Where(nclash.Order.EQ(nclash.OrderDesc), nclash.Create.In(nclash.CreateBulk), nclash.Kind.NEQ(nclash.KindOther),
		nclash.AB.EQ(nclash.ABCValue), nclash.QueryOthersField.EQ("q"), nclash.OthersOrErrEdge.Has()).OrderBy(nclash.Order.Desc(), nclash.Query.Asc()).IDs(ctx))
	assert.Equal(t, []int{b.ID}, ids)
	got := must(client.Clash.Update(db).Set(nclash.Kind.Set(nclash.KindOther), nclash.A.Set(nclash.AX), nclash.OthersOrErrEdge.Clear(), nclash.Set.Set(nclash.SetA)).Where(nclash.ID.EQ(b.ID)).SaveOne(ctx))
	assert.Equal(t, nclash.KindOther, got.Kind)
	assert.Equal(t, nclash.SetA, got.Set)
	assert.Zero(t, must(b.QueryOthersOrErrEdge(db).Count(ctx)))
	_, err = client.Clash.Update(db).Set(nclash.AB.Set("b_c")).Where(nclash.ID.EQ(b.ID)).Exec(ctx)
	var verr *orm.ValidationError
	require.ErrorAs(t, err, &verr, "b_c is a value of a, not of a_b")
	assert.Equal(t, "a_b", verr.Field)

	// Entity and Entityx, QueryUser and User: an entity each.
	var (
		_ *entity.Entity    = must(client.Entity.Create(db).Set(nentityx.Label.Set("entity")).Save(ctx))
		_ *entity.Entityx   = must(client.Entityx.Create(db).Set(mentityx.Label.Set("entityx"), mentityx.Other.Set("o2")).Save(ctx))
		_ *entity.QueryUser = must(client.QueryUser.Create(db).Set(mqueryuser.Label.Set("qu")).Save(ctx))
		_ *entity.User      = must(client.User.Create(db).Set(muser.Label.Set("u")).Save(ctx))
	)
	assert.Equal(t, "o2", must(client.Entityx.Query(db).Only(ctx)).Other)
	assert.Equal(t, "entity", must(client.Entity.Query(db).Only(ctx)).Label)

	// Entities named like the other generated packages (migrate, predicate),
	// like the root package (db) and like a type of the root package
	// (UserClient): each gets a package and a client of its own.
	var (
		_ *entity.Migrate    = must(client.Migrate.Create(db).Set(nmigratex.Label.Set("m")).Save(ctx))
		_ *entity.Predicate  = must(client.Predicate.Create(db).Set(npredicatex.Label.Set("p")).Save(ctx))
		_ *entity.Db         = must(client.Db.Create(db).Set(ndbx.Label.Set("d")).Save(ctx))
		_ *entity.UserClient = must(client.UserClient.Create(db).Set(nuserclient.Label.Set("uc")).Save(ctx))
	)
	assert.Equal(t, 1, must(client.Migrate.Query(db).Where(nmigratex.Label.EQ("m")).Count(ctx)))
	assert.Equal(t, 1, must(client.Predicate.Query(db).Where(npredicatex.Label.EQ("p")).Count(ctx)))
	assert.Equal(t, 1, must(client.Db.Query(db).Where(ndbx.Label.EQ("d")).Count(ctx)))
	assert.Equal(t, "uc", must(client.UserClient.Query(db).Where(nuserclient.Label.NEQ("")).Only(ctx)).Label)
	assert.Equal(t, "u", must(client.User.Query(db).Only(ctx)).Label)

	// Entities named like the setter and order types of another: the entity
	// keeps its name as its predicate type (predicate.UserSetter is a predicate
	// of UserSetter), and User's own setters and order terms still work.
	var (
		_ predicate.UserSetter    = nuserset.Label.EQ("x")
		_ predicate.UserOrderTerm = nuserorder.Label.EQ("x")
		_ predicate.User          = muser.Label.EQ("x")
	)
	must(client.UserSetter.Create(db).Set(nuserset.Label.Set("us")).Save(ctx))
	must(client.UserOrderTerm.Create(db).Set(nuserorder.Label.Set("uo")).Save(ctx))
	assert.Equal(t, "us", must(client.UserSetter.Query(db).Where(nuserset.Label.EQ("us")).OrderBy(nuserset.Label.Asc()).Only(ctx)).Label)
	assert.Equal(t, "uo", must(client.UserOrderTerm.Query(db).OrderBy(nuserorder.Label.Desc()).Only(ctx)).Label)
	assert.Equal(t, 1, must(client.User.Update(db).Set(muser.Label.Set("u2")).Where(muser.Label.EQ("u")).Exec(ctx)))
	assert.Equal(t, "u2", must(client.User.Query(db).OrderBy(muser.Label.Asc()).Only(ctx)).Label)
}
