package sdl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// TestEnums: Enums gives velox-gen's enum fields their GraphQL values and
// leaves out what is not in GraphQL or brings its own marshalers.
func TestEnums(t *testing.T) {
	ann := func(s string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"graphql": json.RawMessage(s)}
	}
	enum := func(name, typ string, a map[string]json.RawMessage) *plugin.Field {
		return &plugin.Field{Name: name, Type: "enum", Enum: typ, EnumValues: []string{"open", "in_progress"}, Annotations: a}
	}
	m := &plugin.Model{
		Entities: []*plugin.Entity{
			{Name: "Task", Fields: []*plugin.Field{
				{Name: "id", Type: "string"},
				enum("status", "TaskStatus", nil),
				enum("kind", "TaskKind", ann(`{"enum_values":{"open":"PUBLIC"}}`)),
				enum("raw", "TaskRaw", ann(fmt.Sprintf(`{"skip":%d}`, 2))), // SkipEnumField
				enum("gone", "TaskGone", ann(`{"skip":63}`)),               // SkipAll
				enum("own", "levels.Level", nil),                           // the application's type
			}},
			{Name: "Hidden", Annotations: ann(`{"skip":1}`), Fields: []*plugin.Field{enum("status", "HiddenStatus", nil)}},
			{Name: "TaskTag", Join: true, Fields: []*plugin.Field{enum("status", "TaskTagStatus", nil)}},
			{Name: "Plain", Fields: []*plugin.Field{{Name: "id", Type: "string"}}},
		},
	}
	for _, typ := range []string{"TaskStatus", "TaskKind", "TaskRaw", "TaskGone", "HiddenStatus", "TaskTagStatus"} {
		m.Enums = append(m.Enums, &plugin.Enum{Type: typ})
	}
	got, err := Enums(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Entity.Name != "Task" {
		t.Fatalf("%+v, want Task alone", got)
	}
	want := fmt.Sprint([]Enum{
		{Name: "TaskStatus", Field: 1, Values: []string{"OPEN", "IN_PROGRESS"}},
		{Name: "TaskKind", Field: 2, Values: []string{"PUBLIC", "IN_PROGRESS"}},
		{Field: 3},
	})
	if s := fmt.Sprint(got[0].Enums); s != want {
		t.Errorf("enums %s, want %s", s, want)
	}

	for name, c := range map[string]struct {
		a    map[string]json.RawMessage
		want string
	}{
		"rename":     {ann(`{"enum_values":{"closed":"X"}}`), `graphql.EnumValues names "closed"`},
		"collision":  {ann(`{"enum_values":{"open":"IN_PROGRESS"}}`), "are both the GraphQL enum value IN_PROGRESS"},
		"annotation": {ann(`{"skip":"x"}`), "the graphql annotation"},
	} {
		t.Run(name, func(t *testing.T) {
			m := &plugin.Model{Entities: []*plugin.Entity{{Name: "Task", Fields: []*plugin.Field{enum("status", "TaskStatus", c.a)}}}, Enums: []*plugin.Enum{{Type: "TaskStatus"}}}
			if _, err := Enums(m); err == nil || !strings.Contains(err.Error(), "schema Task, field status: ") || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v, want %q", err, c.want)
			}
		})
	}
	bad := &plugin.Model{Entities: []*plugin.Entity{{Name: "Task", Annotations: ann(`{"skip":"x"}`)}}}
	if _, err := Enums(bad); err == nil || !strings.Contains(err.Error(), "schema Task: the graphql annotation") {
		t.Errorf("entity annotation: %v", err)
	}
}

// TestEnumsRenameUpperStored: stored values in upper case, renamed to other
// GraphQL values by graphql.EnumValues, keyed by the stored value.
func TestEnumsRenameUpperStored(t *testing.T) {
	m := &plugin.Model{
		Entities: []*plugin.Entity{{Name: "Task", Fields: []*plugin.Field{{
			Name: "status", Type: "enum", Enum: "TaskStatus", EnumValues: []string{"IN_PROGRESS", "COMPLETED"},
			Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"enum_values":{"IN_PROGRESS":"inProgress","COMPLETED":"completed"}}`)},
		}}}},
		Enums: []*plugin.Enum{{Type: "TaskStatus"}},
	}
	got, err := Enums(m)
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got[0].Enums[0].Values); s != "[inProgress completed]" {
		t.Errorf("values %s, want [inProgress completed]", s)
	}
}
