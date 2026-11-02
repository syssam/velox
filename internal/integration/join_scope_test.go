package integration

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
)

// A join entity as velox-gen writes one: jsProjectLabel between two tenant-scoped
// entities, jsProjectKind between a tenant-scoped one and jsKind, which has no tenant,
// and jsFriend, the bidirectional edge of a tenant-scoped entity.

type jsProject struct {
	ID     int64
	OrgID  int64
	Labels velox.Many[jsLabel]
	Kinds  velox.Many[jsKind]
}

var JsProjects = struct {
	ID     velox.NumberCol[jsProject, int64]
	Labels velox.Rel[jsProject, jsLabel]
	Kinds  velox.Rel[jsProject, jsKind]
}{velox.NumberCol[jsProject, int64]{I: 0}, velox.Rel[jsProject, jsLabel]{I: 0}, velox.Rel[jsProject, jsKind]{I: 1}}

var jsProjectMeta velox.MetaOnce[jsProject]

func (*jsProject) VeloxMeta() *velox.Meta[jsProject] {
	return jsProjectMeta.Load(func() velox.Descriptor[jsProject] {
		return velox.Descriptor[jsProject]{
			Table:      "js_projects",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
			PrimaryKey: []int{0},
			Tenant:     1,
			Edges: []velox.EdgeDescriptor{
				velox.EdgeThrough[jsProjectLabel, jsLabel]("Labels", 0, 0, 1, 0),
				velox.EdgeThrough[jsProjectKind, jsKind]("Kinds", 0, 0, 1, 0),
			},
			FieldPtr: func(x *jsProject, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.OrgID
			},
			EdgeFieldPtr: func(x *jsProject, i int) any {
				if i == 0 {
					return &x.Labels
				}
				return &x.Kinds
			},
		}
	})
}

type jsLabel struct {
	ID    int64
	OrgID int64
}

var jsLabelMeta velox.MetaOnce[jsLabel]

func (*jsLabel) VeloxMeta() *velox.Meta[jsLabel] {
	return jsLabelMeta.Load(func() velox.Descriptor[jsLabel] {
		return velox.Descriptor[jsLabel]{
			Table:      "js_labels",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
			PrimaryKey: []int{0},
			Tenant:     1,
			FieldPtr: func(x *jsLabel, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.OrgID
			},
		}
	})
}

// jsKind has no tenant: every tenant may read it, and only a jsProjectKind says whose.
type jsKind struct {
	ID   int64
	Name string
}

var JsKinds = struct {
	ID velox.NumberCol[jsKind, int64]
}{velox.NumberCol[jsKind, int64]{I: 0}}

var jsKindMeta velox.MetaOnce[jsKind]

func (*jsKind) VeloxMeta() *velox.Meta[jsKind] {
	return jsKindMeta.Load(func() velox.Descriptor[jsKind] {
		return velox.Descriptor[jsKind]{
			Table:      "js_kinds",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			FieldPtr: func(x *jsKind, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Name
			},
		}
	})
}

type jsProjectLabel struct {
	ProjectID int64
	LabelID   int64
}

var JsLinks = struct {
	ProjectID velox.NumberCol[jsProjectLabel, int64]
	LabelID   velox.NumberCol[jsProjectLabel, int64]
}{velox.NumberCol[jsProjectLabel, int64]{I: 0}, velox.NumberCol[jsProjectLabel, int64]{I: 1}}

var jsProjectLabelMeta velox.MetaOnce[jsProjectLabel]

func (*jsProjectLabel) VeloxMeta() *velox.Meta[jsProjectLabel] {
	return jsProjectLabelMeta.Load(func() velox.Descriptor[jsProjectLabel] {
		return velox.Descriptor[jsProjectLabel]{
			Table:      "js_project_labels",
			Columns:    []velox.ColumnDescriptor{{Field: "ProjectID", Name: "project_id"}, {Field: "LabelID", Name: "label_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			TenantRefs: []velox.TenantRef{
				{Column: 0, Table: "js_projects", Key: "id", Tenant: "org_id", TenantKind: "int64"},
				{Column: 1, Table: "js_labels", Key: "id", Tenant: "org_id", TenantKind: "int64"},
			},
			FieldPtr: func(x *jsProjectLabel, i int) any {
				if i == 0 {
					return &x.ProjectID
				}
				return &x.LabelID
			},
		}
	})
}

type jsProjectKind struct {
	ProjectID int64
	KindID    int64
}

var jsPK = struct {
	ProjectID velox.NumberCol[jsProjectKind, int64]
	KindID    velox.NumberCol[jsProjectKind, int64]
}{velox.NumberCol[jsProjectKind, int64]{I: 0}, velox.NumberCol[jsProjectKind, int64]{I: 1}}

var jsProjectKindMeta velox.MetaOnce[jsProjectKind]

func (*jsProjectKind) VeloxMeta() *velox.Meta[jsProjectKind] {
	return jsProjectKindMeta.Load(func() velox.Descriptor[jsProjectKind] {
		return velox.Descriptor[jsProjectKind]{
			Table:      "js_project_kinds",
			Columns:    []velox.ColumnDescriptor{{Field: "ProjectID", Name: "project_id"}, {Field: "KindID", Name: "kind_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			TenantRefs: []velox.TenantRef{{Column: 0, Table: "js_projects", Key: "id", Tenant: "org_id", TenantKind: "int64"}},
			FieldPtr: func(x *jsProjectKind, i int) any {
				if i == 0 {
					return &x.ProjectID
				}
				return &x.KindID
			},
		}
	})
}

type jsFriend struct {
	A int64
	B int64
}

var JsFriends = struct {
	A velox.NumberCol[jsFriend, int64]
}{velox.NumberCol[jsFriend, int64]{I: 0}}

var jsFriendMeta velox.MetaOnce[jsFriend]

func (*jsFriend) VeloxMeta() *velox.Meta[jsFriend] {
	return jsFriendMeta.Load(func() velox.Descriptor[jsFriend] {
		return velox.Descriptor[jsFriend]{
			Table:      "js_friends",
			Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Bidi:       true,
			TenantRefs: []velox.TenantRef{
				{Column: 0, Table: "js_projects", Key: "id", Tenant: "org_id", TenantKind: "int64"},
				{Column: 1, Table: "js_projects", Key: "id", Tenant: "org_id", TenantKind: "int64"},
			},
			FieldPtr: func(x *jsFriend, i int) any {
				if i == 0 {
					return &x.A
				}
				return &x.B
			},
		}
	})
}

// jsSetup makes projects and labels 1 and 3 of tenant 1, 2 of tenant 2. Their links are
// (1,1), (3,3) and (2,2), and (1,2), which crosses tenants, as written before the check
// or under WithAllTenants; each project is linked to its own kind, and 1 and 3 are friends,
// and 2 and 4 of tenant 2.
func jsSetup(t *testing.T, tg target) {
	t.Helper()
	for _, s := range []string{"js_friends", "js_project_kinds", "js_project_labels", "js_kinds", "js_labels", "js_projects"} {
		tg.exec(t, "DROP TABLE IF EXISTS "+s)
	}
	tg.exec(t, "CREATE TABLE js_projects (id BIGINT PRIMARY KEY, org_id BIGINT NOT NULL)")
	tg.exec(t, "CREATE TABLE js_labels (id BIGINT PRIMARY KEY, org_id BIGINT NOT NULL)")
	tg.exec(t, "CREATE TABLE js_kinds (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL)")
	tg.exec(t, "CREATE TABLE js_project_labels (project_id BIGINT NOT NULL, label_id BIGINT NOT NULL, PRIMARY KEY (project_id, label_id))")
	tg.exec(t, "CREATE TABLE js_project_kinds (project_id BIGINT NOT NULL, kind_id BIGINT NOT NULL, PRIMARY KEY (project_id, kind_id))")
	tg.exec(t, "CREATE TABLE js_friends (a BIGINT NOT NULL, b BIGINT NOT NULL, PRIMARY KEY (a, b))")
	for _, r := range [][2]int64{{1, 1}, {2, 2}, {3, 1}, {4, 2}} {
		tg.exec(t, "INSERT INTO js_projects (id, org_id) VALUES (?, ?)", r[0], r[1])
	}
	for _, r := range [][2]int64{{1, 1}, {2, 2}, {3, 1}} {
		tg.exec(t, "INSERT INTO js_labels (id, org_id) VALUES (?, ?)", r[0], r[1])
	}
	for _, r := range [][2]int64{{1, 1}, {2, 2}, {3, 3}, {1, 2}} {
		tg.exec(t, "INSERT INTO js_project_labels (project_id, label_id) VALUES (?, ?)", r[0], r[1])
	}
	for _, id := range []int64{1, 2} {
		tg.exec(t, "INSERT INTO js_kinds (id, name) VALUES (?, ?)", id, "k")
		tg.exec(t, "INSERT INTO js_project_kinds (project_id, kind_id) VALUES (?, ?)", id, id)
	}
	for _, r := range [][2]int64{{1, 3}, {3, 1}, {2, 4}, {4, 2}} {
		tg.exec(t, "INSERT INTO js_friends (a, b) VALUES (?, ?)", r[0], r[1])
	}
}

func jsLinksOf(t *testing.T, tg target) [][2]int64 {
	t.Helper()
	got, err := tg.db.Query[jsProjectLabel]().OrderBy(JsLinks.ProjectID.Asc(), JsLinks.LabelID.Asc()).Unbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][2]int64, len(got))
	for i, l := range got {
		out[i] = [2]int64{l.ProjectID, l.LabelID}
	}
	return out
}

// jsChecks are the scenarios of a join entity's boundary.
func jsChecks(t *testing.T, tg target) {
	bg := context.Background()
	t1, t2 := velox.WithTenant(bg, int64(1)), velox.WithTenant(bg, int64(2))
	byLink := []velox.Order[jsProjectLabel]{JsLinks.ProjectID.Asc(), JsLinks.LabelID.Asc()}
	all := [][2]int64{{1, 1}, {1, 2}, {2, 2}, {3, 3}}

	t.Run("reads", func(t *testing.T) {
		q := tg.db.Query[jsProjectLabel]().OrderBy(byLink...).Unbounded()
		ids := func(xs []jsProjectLabel) [][2]int64 {
			out := make([][2]int64, len(xs))
			for i, l := range xs {
				out[i] = [2]int64{l.ProjectID, l.LabelID}
			}
			return out
		}
		for _, c := range []struct {
			name string
			ctx  context.Context
			want [][2]int64
		}{{"tenant 1", t1, [][2]int64{{1, 1}, {3, 3}}}, {"tenant 2", t2, [][2]int64{{2, 2}}}, {"all", ctx, all}} {
			got, err := q.All(c.ctx)
			if err != nil || !reflect.DeepEqual(ids(got), c.want) {
				t.Errorf("%s: All: %v, %v, want %v", c.name, ids(got), err, c.want)
			}
			var it [][2]int64
			for l, err := range q.Iter(c.ctx) {
				if err != nil {
					t.Fatalf("%s: Iter: %v", c.name, err)
				}
				it = append(it, [2]int64{l.ProjectID, l.LabelID})
			}
			if !reflect.DeepEqual(it, c.want) {
				t.Errorf("%s: Iter: %v, want %v", c.name, it, c.want)
			}
			if n, err := tg.db.Query[jsProjectLabel]().Count(c.ctx); err != nil || n != int64(len(c.want)) {
				t.Errorf("%s: Count: %d, %v", c.name, n, err)
			}
			var sum int64
			for _, r := range c.want {
				sum += r[1]
			}
			if v, err := velox.Sum(c.ctx, tg.db.Query[jsProjectLabel](), JsLinks.LabelID); err != nil || v != sum {
				t.Errorf("%s: Sum: %d, %v, want %d", c.name, v, err, sum)
			}
			groups, err := velox.CountBy(c.ctx, tg.db.Query[jsProjectLabel]().Unbounded(), JsLinks.ProjectID.Key())
			if err != nil {
				t.Errorf("%s: CountBy: %v", c.name, err)
			}
			var n int64
			for _, g := range groups {
				n += g.Value
			}
			if n != int64(len(c.want)) {
				t.Errorf("%s: CountBy: %v", c.name, groups)
			}
		}
		if ok, err := tg.db.Query[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(2)).Exists(t1); err != nil || ok {
			t.Errorf("Exists of tenant 2's link under tenant 1: %v, %v", ok, err)
		}
		if ok, err := tg.db.Query[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(2)).Exists(t2); err != nil || !ok {
			t.Errorf("Exists of its own: %v, %v", ok, err)
		}
		for name, err := range map[string]error{
			"All":    first(q.All(bg)),
			"Count":  first(tg.db.Query[jsProjectLabel]().Count(bg)),
			"Exists": first(tg.db.Query[jsProjectLabel]().Exists(bg)),
			"Sum":    first(velox.Sum(bg, tg.db.Query[jsProjectLabel](), JsLinks.LabelID)),
		} {
			if !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("no tenant: %s: %v", name, err)
			}
		}
		for _, err := range q.Iter(bg) {
			if !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("no tenant: Iter: %v", err)
			}
			break
		}
	})

	t.Run("writes", func(t *testing.T) {
		if n, err := tg.db.Delete[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(2)).Exec(t1); err != nil || n != 0 {
			t.Errorf("delete of another tenant's link: %d, %v", n, err)
		}
		if _, err := tg.db.Delete[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(2)).Exec(bg); !errors.Is(err, velox.ErrNoTenant) {
			t.Errorf("delete with no tenant: %v", err)
		}
		if n, err := tg.db.Update[jsProjectLabel]().Set(JsLinks.ProjectID.Set(1), JsLinks.LabelID.Set(1)).Where(JsLinks.ProjectID.EQ(2)).Exec(t1); err != nil || n != 0 {
			t.Errorf("update of another tenant's link: %d, %v", n, err)
		}
		if _, err := tg.db.Update[jsProjectLabel]().Set(JsLinks.ProjectID.Set(1)).Where(JsLinks.ProjectID.EQ(2)).Exec(bg); !errors.Is(err, velox.ErrNoTenant) {
			t.Errorf("update with no tenant: %v", err)
		}
		if got := jsLinksOf(t, tg); !reflect.DeepEqual(got, all) {
			t.Errorf("after the refusals: %v", got)
		}
		// The link that crosses tenants is hidden from both, and stays.
		if n, err := tg.db.Delete[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(1)).Exec(t1); err != nil || n != 1 {
			t.Errorf("delete of its own: %d, %v", n, err)
		}
		if n, err := tg.db.Update[jsProjectLabel]().Set(JsLinks.LabelID.Set(1)).Where(JsLinks.ProjectID.EQ(3)).Exec(t1); err != nil || n != 1 {
			t.Errorf("update of its own: %d, %v", n, err)
		}
		if got, want := jsLinksOf(t, tg), [][2]int64{{1, 2}, {2, 2}, {3, 1}}; !reflect.DeepEqual(got, want) {
			t.Errorf("after them: %v, want %v", got, want)
		}
		if n, err := tg.db.Delete[jsProjectLabel]().All().Exec(t2); err != nil || n != 1 {
			t.Errorf("All deletes the tenant's: %d, %v", n, err)
		}
		if n, err := tg.db.Delete[jsProjectLabel]().Where(JsLinks.ProjectID.EQ(1)).Exec(ctx); err != nil || n != 1 {
			t.Errorf("WithAllTenants reaches the one that crosses: %d, %v", n, err)
		}
		if err := tg.db.Insert(&jsProjectLabel{1, 1}).Exec(t1); err != nil {
			t.Errorf("link: %v", err)
		}
		if err := tg.db.InsertAll([]jsProjectLabel{{1, 1}}).OnConflict(JsLinks.ProjectID, JsLinks.LabelID).DoNothing().Exec(t1); err != nil {
			t.Errorf("link again: %v", err)
		}
	})

	t.Run("a bidirectional link", func(t *testing.T) {
		if n, err := tg.db.Delete[jsFriend]().Where(JsFriends.A.EQ(2)).Exec(t1); err != nil || n != 0 {
			t.Errorf("another tenant's: %d, %v", n, err)
		}
		if n, err := tg.db.Delete[jsFriend]().Where(JsFriends.A.EQ(1)).Exec(t1); err != nil || n != 2 {
			t.Errorf("both rows of its own: %d, %v", n, err)
		}
		if n, err := tg.db.Query[jsFriend]().Count(ctx); err != nil || n != 2 {
			t.Errorf("left: %d, %v", n, err)
		}
	})
}

func first[T any](_ T, err error) error { return err }

// TestJoinRowsAreScoped is the finding: a join row had no tenant boundary. Under tenant
// 1, or with no tenant, a delete removed tenant 2's link, an update moved it, and a query
// read it. They now reach the rows both of whose sides are the tenant's, and fail with
// ErrNoTenant with none; WithAllTenants is the deliberate way across.
func TestJoinRowsAreScoped(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		each(t, func(t *testing.T, tg target) {
			jsSetup(t, tg)
			jsChecks(t, tg)
		})
	})
	t.Run("through the hooks' path", func(t *testing.T) {
		each(t, func(t *testing.T, tg target) {
			jsSetup(t, tg)
			jsChecks(t, tg)
		}, velox.WithRewrite(func(context.Context, *velox.Statement) error { return nil }))
	})
}

// TestJoinLoadsPayNothing: With and Has read join rows under what already scopes them:
// the owners were read under the tenant, and the targets are. So With's read of the
// join rows carries no condition of its own, and neither does its LimitEach. Without a
// tenant the load fails as a statement on either side does.
func TestJoinLoadsPayNothing(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	log := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		sent = append(sent, st.SQL)
		mu.Unlock()
		return next(ctx)
	})
	each(t, func(t *testing.T, tg target) {
		jsSetup(t, tg)
		bg := context.Background()
		t1 := velox.WithTenant(bg, int64(1))
		sent = nil
		ps, err := tg.db.Query[jsProject]().Where(JsProjects.ID.EQ(1)).With(JsProjects.Labels).With(JsProjects.Kinds).Limit(5).All(t1)
		if err != nil || len(ps) != 1 {
			t.Fatalf("%v, %v", ps, err)
		}
		labels, _ := ps[0].Labels.Get()
		kinds, _ := ps[0].Kinds.Get()
		// The link to tenant 2's label is not loaded, by its target's scope.
		if len(labels) != 1 || labels[0].ID != 1 || len(kinds) != 1 || kinds[0].ID != 1 {
			t.Errorf("%+v %+v", labels, kinds)
		}
		if i := slices.IndexFunc(sent, func(s string) bool { return strings.Contains(s, "IN (SELECT") }); i >= 0 {
			t.Errorf("a load paid: %s", sent[i])
		}
		ps, err = tg.db.Query[jsProject]().Where(JsProjects.ID.EQ(1)).With(JsProjects.Kinds.LimitEach(5)).Limit(5).All(t1)
		if err != nil {
			t.Fatal(err)
		}
		if kinds, _ := ps[0].Kinds.Get(); len(kinds) != 1 {
			t.Errorf("LimitEach: %+v", kinds)
		}
		if i := slices.IndexFunc(sent, func(s string) bool { return strings.Contains(s, "IN (SELECT") }); i >= 0 {
			t.Errorf("a load paid: %s", sent[i])
		}
		has, err := tg.db.Query[jsProject]().Where(JsProjects.Labels.Has()).Count(velox.WithTenant(bg, int64(2)))
		if err != nil || has != 1 {
			t.Errorf("Has: %d, %v", has, err)
		}
	}, log)
}

// TestJoinOwnersGivenAreScoped: Of, LoadEach and CountEach read join rows for owners the
// caller names. To a target with no tenant nothing else stands between tenant 1 and the
// links of tenant 2's project.
func TestJoinOwnersGivenAreScoped(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		jsSetup(t, tg)
		bg := context.Background()
		t1 := velox.WithTenant(bg, int64(1))
		theirs, mine := &jsProject{ID: 2}, &jsProject{ID: 1}
		q := tg.db.Query[jsKind]().OrderBy(JsKinds.ID.Asc()).Unbounded()
		t.Run("Of", func(t *testing.T) {
			if got, err := q.Where(JsProjects.Kinds.Of(theirs)).All(t1); err != nil || len(got) != 0 {
				t.Errorf("another tenant's: %+v, %v", got, err)
			}
			if got, err := q.Where(JsProjects.Kinds.Of(mine)).All(t1); err != nil || len(got) != 1 {
				t.Errorf("its own: %+v, %v", got, err)
			}
			if got, err := q.Where(velox.Not(JsKinds.ID.EQ(99)), JsProjects.Kinds.Of(theirs)).All(t1); err != nil || len(got) != 0 {
				t.Errorf("with another condition: %+v, %v", got, err)
			}
			if got, err := q.Where(velox.And(JsKinds.ID.EQ(2), JsProjects.Kinds.Of(theirs))).All(t1); err != nil || len(got) != 0 {
				t.Errorf("inside And: %+v, %v", got, err)
			}
			if _, err := q.Where(JsProjects.Kinds.Of(theirs)).All(bg); !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("no tenant: %v", err)
			}
			if got, err := q.Where(JsProjects.Kinds.Of(theirs)).All(ctx); err != nil || len(got) != 1 {
				t.Errorf("WithAllTenants: %+v, %v", got, err)
			}
		})
		t.Run("Of inside an EXISTS of a scoped statement", func(t *testing.T) {
			// Project 1 is linked to kind 2, tenant 2's project's. Its count would tell
			// tenant 1 that the two share it.
			if err := tg.db.Insert(&jsProjectKind{1, 2}).Exec(t1); err != nil {
				t.Fatal(err)
			}
			n, err := tg.db.Query[jsProject]().Where(JsProjects.Kinds.HasWhere(JsProjects.Kinds.Of(theirs))).Count(t1)
			if err != nil || n != 0 {
				t.Errorf("%d, %v", n, err)
			}
			if _, err := tg.db.Delete[jsProjectKind]().Where(jsPK.ProjectID.EQ(1), jsPK.KindID.EQ(2)).Exec(t1); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("LoadEach", func(t *testing.T) {
			got, err := velox.LoadEach(t1, q, JsProjects.Kinds, []*jsProject{theirs, mine})
			if err != nil || len(got[0]) != 0 || len(got[1]) != 1 {
				t.Errorf("%+v, %v", got, err)
			}
			got, err = velox.LoadEach(t1, q, JsProjects.Kinds.LimitEach(1), []*jsProject{theirs, mine})
			if err != nil || len(got[0]) != 0 || len(got[1]) != 1 {
				t.Errorf("LimitEach: %+v, %v", got, err)
			}
			if _, err := velox.LoadEach(bg, q, JsProjects.Kinds, []*jsProject{theirs}); !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("no tenant: %v", err)
			}
			if got, err := velox.LoadEach(ctx, q, JsProjects.Kinds, []*jsProject{theirs}); err != nil || len(got[0]) != 1 {
				t.Errorf("WithAllTenants: %+v, %v", got, err)
			}
		})
		t.Run("CountEach", func(t *testing.T) {
			n, err := velox.CountEach(t1, tg.db.Query[jsKind](), JsProjects.Kinds, []*jsProject{theirs, mine})
			if err != nil || n[0] != 0 || n[1] != 1 {
				t.Errorf("%v, %v", n, err)
			}
		})
	})
}
