package codegen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type TrProject struct{ schema.Schema }

func (TrProject) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Tenant().StorageKey("project_org")}
}
func (TrProject) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("tasks", TrTask.Type),
		edge.To("notes", TrNote.Type),
		edge.To("logs", TrLog.Type),
		edge.To("labels", TrLabel.Type),
		edge.To("globals", TrGlobal.Type),
	}
}

type TrTask struct{ schema.Schema }

func (TrTask) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Tenant(), field.Int64("project_id"), field.Int64("parent_id").Optional().Nillable(), field.Int64("global_id")}
}
func (TrTask) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("project", TrProject.Type).Ref("tasks").Unique().Required().Field("project_id"),
		edge.To("children", TrTask.Type).From("parent").Unique().Field("parent_id"),
		edge.To("global", TrGlobal.Type).Unique().Required().Field("global_id"),
	}
}

// TrNote has its key only through the O2M edge of its target, which adds the column.
type TrNote struct{ schema.Schema }

func (TrNote) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Tenant()} }

// TrLog has no tenant, so its key to a tenant-scoped row is not checked.
type TrLog struct{ schema.Schema }

func (TrLog) Fields() []schema.Field { return []schema.Field{field.Int64("project_id")} }
func (TrLog) Edges() []schema.Edge {
	return []schema.Edge{edge.From("project", TrProject.Type).Ref("logs").Unique().Required().Field("project_id")}
}

type TrLabel struct{ schema.Schema }

func (TrLabel) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Tenant()} }
func (TrLabel) Edges() []schema.Edge {
	return []schema.Edge{edge.From("projects", TrProject.Type).Ref("labels")}
}

type TrGlobal struct{ schema.Schema }

func (TrGlobal) Edges() []schema.Edge {
	return []schema.Edge{edge.From("projects", TrProject.Type).Ref("globals")}
}

type (
	trCents int64
	TrKinds struct{ schema.Schema }
)

func (TrKinds) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("a"), field.String("b"), field.Enum("c").Values("x"), field.Number[trCents]("d"), field.Uint8("e"),
		field.Time("g"),
	}
}

// TestTenantKind: the kind a TenantRef states of each type a tenant field may have.
func TestTenantKind(t *testing.T) {
	e := mustBuild(t, TrKinds{}).Entities[0]
	for name, want := range map[string]string{"a": "int64", "b": "string", "c": "string", "d": "int64", "e": "uint8", "g": "struct"} {
		if got := tenantKind(e.Fields[fieldIndex(e, name)]); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// TestLowerTenantRefs: a tenant-scoped entity's foreign key to a tenant-scoped target,
// from either side of the edge, and each tenant-scoped side of a join entity. Nothing
// for a target without a tenant, or an entity without one that is no join entity. The
// Descriptor holds them as strings, naming no other entity.
func TestLowerTenantRefs(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, TrProject{}, TrTask{}, TrNote{}, TrLog{}, TrLabel{}, TrGlobal{})
	project := OutTenantRef{Table: "tr_projects", Key: "id", Tenant: "project_org"}
	at := func(col int, r OutTenantRef) OutTenantRef {
		r.Column = col
		return r
	}
	for name, want := range map[string][]OutTenantRef{
		"TrProject":       nil,
		"TrLog":           nil,
		"TrGlobal":        nil,
		"TrLabel":         nil,
		"TrTask":          {at(2, project), {Column: 3, Table: "tr_tasks", Key: "id", Tenant: "org_id"}},
		"TrNote":          {at(2, project)},
		"TrProjectLabel":  {at(0, project), {Column: 1, Table: "tr_labels", Key: "id", Tenant: "org_id"}},
		"TrProjectGlobal": {at(0, project)},
	} {
		e := o.entity(name)
		if e == nil {
			t.Fatalf("no entity %s, of %d", name, len(o.Entities))
		}
		if !reflect.DeepEqual(e.TenantRefs, want) {
			t.Errorf("%s: %+v, want %+v", name, e.TenantRefs, want)
		}
	}
	files, err := Generate(mustBuild(t, TrProject{}, TrTask{}, TrNote{}, TrLog{}, TrLabel{}, TrGlobal{}), Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name, "tr_task_velox.go") {
			s := string(f.Data)
			if !strings.Contains(s, `{Column: 2, Table: "tr_projects", Key: "id", Tenant: "project_org", TenantKind: "int64"},`) {
				t.Errorf("the Descriptor does not hold the reference:\n%s", s)
			}
			return
		}
	}
	t.Error("no file for TrTask")
}
