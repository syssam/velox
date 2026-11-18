package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
)

// QueryField() with no name is the entity's plural in lower camel case, as
// entgql names it, and a named one keeps its name.
func TestQueryFieldDefaultName(t *testing.T) {
	user := entity("User", field("name", "string"))
	user.Annotations = ann(t, graphql.QueryField())
	goal := entity("PerformanceGoal", field("title", "string"))
	goal.Annotations = ann(t, graphql.QueryField(), graphql.RelayConnection())
	cat := entity("Category", field("label", "string"))
	cat.Annotations = ann(t, graphql.QueryField("allCategories"))
	s, _ := load(t, model(user, goal, cat))
	var got []string
	for _, f := range s.Types["Query"].Fields {
		if !strings.HasPrefix(f.Name, "__") {
			got = append(got, f.Name)
		}
	}
	slices.Sort(got)
	if want := []string{"allCategories", "performanceGoals", "users"}; !slices.Equal(got, want) {
		t.Errorf("Query fields %v, want %v", got, want)
	}
}

// A default name that another entity's QueryField already took is refused, as
// a named one is.
func TestQueryFieldDefaultCollides(t *testing.T) {
	user := entity("User", field("name", "string"))
	user.Annotations = ann(t, graphql.QueryField())
	other := entity("Person", field("name", "string"))
	other.Annotations = ann(t, graphql.QueryField("users"))
	_, err := Render(model(user, other))
	if err == nil || !strings.Contains(err.Error(), "both declare the Query field users") {
		t.Errorf("got %v", err)
	}
}
