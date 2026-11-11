package gogen

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

// TestRenderEnumsIsRendersEnums: for each entity with a velox-gen enum,
// RenderEnums writes the file Render writes, with only its enum marshalers,
// which are Render's to the byte. Post's status, renamed published → LIVE,
// is among them.
func TestRenderEnumsIsRendersEnums(t *testing.T) {
	m := modeltest.Model(t)
	full := render(t, m)
	files, err := RenderEnums(m, outPath, "gen")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
		_, body, ok := strings.Cut(f.Content, "GeneratedCodeIsVersion")
		if !ok {
			t.Fatalf("%s carries no version guard:\n%s", f.Path, f.Content)
		}
		_, body, _ = strings.Cut(body, "\n")
		if !strings.Contains(full[f.Path], body) {
			t.Errorf("%s: the enum marshalers are not Render's:\n%s", f.Path, f.Content)
		}
		for line := range strings.SplitSeq(body, "\n") {
			if strings.HasPrefix(line, "func ") && !strings.Contains(line, ") MarshalGQL(") && !strings.Contains(line, ") UnmarshalGQL(") {
				t.Errorf("%s declares more than enum marshalers: %s", f.Path, line)
			}
		}
	}
	if want := []string{"group_gql.go", "post_gql.go", "user_gql.go"}; !slices.Equal(paths, want) {
		t.Errorf("files %v, want %v", paths, want)
	}
	post := files[slices.Index(paths, "post_gql.go")].Content
	for _, want := range []string{"case PostStatusPublished:\n\t\t_, _ = io.WriteString(w, `\"LIVE\"`)", "case \"IN_REVIEW\":\n\t\t*e = PostStatusInReview"} {
		if !strings.Contains(post, want) {
			t.Errorf("post_gql.go has no %q:\n%s", want, post)
		}
	}
}

// TestRenderEnumsWithoutTheSchemaChecks: RenderEnums writes the marshalers
// of a model Render refuses, here a field with no GraphQL type, since the
// application writes its own SDL.
func TestRenderEnumsWithoutTheSchemaChecks(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion,
		Entities: []*plugin.Entity{{
			Name: "Task", Label: "task", Handles: "Tasks", PrimaryKey: []int{0},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "string"},
				{Name: "status", GoName: "Status", Type: "enum", Enum: "TaskStatus", EnumValues: []string{"open", "in_progress"}},
				{Name: "x", GoName: "X", Type: "other"},
			},
		}},
		Enums: []*plugin.Enum{{Type: "TaskStatus", Values: []string{"open", "in_progress"}, Consts: []string{"TaskStatusOpen", "TaskStatusInProgress"}}},
	}
	if _, err := Render(m, outPath, "gen"); err == nil {
		t.Fatal("Render takes a field with no GraphQL type")
	}
	files, err := RenderEnums(m, outPath, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "task_gql.go" ||
		!strings.Contains(files[0].Content, "case TaskStatusInProgress:\n\t\t_, _ = io.WriteString(w, `\"IN_PROGRESS\"`)") ||
		!strings.Contains(files[0].Content, "case \"OPEN\":\n\t\t*e = TaskStatusOpen") {
		t.Errorf("%+v", files)
	}
	if _, err := RenderEnums(m, outPath, ""); err == nil {
		t.Error("RenderEnums takes no generated package")
	}
	m.Entities[0].Label = ""
	if _, err := RenderEnums(m, outPath, "gen"); err == nil || !strings.Contains(err.Error(), "no label") {
		t.Errorf("no label: %v", err)
	}
}
