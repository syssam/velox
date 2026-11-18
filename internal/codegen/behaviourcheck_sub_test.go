package codegen

import (
	"slices"
	"testing"
)

// A FieldInSelect names an entity, linked to the one whose behaviour holds it,
// and its conditions name that entity's columns, and its relations, which are
// linked on it.
func TestCheckFieldInSelectLinks(t *testing.T) {
	c := newCheckEnv(t)
	linked, warnings, err := c.run(t, read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("title", "x"))`))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("refused: %v %q", err, warnings)
	}
	if got := linked["Comment"]; !slices.Equal(got, []string{"@Doc"}) {
		t.Errorf("Comment links %v, want [@Doc]", got)
	}

	// A relation named inside the subquery is the target's, so it is linked on Doc.
	linked, _, err = c.run(t, read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.HasEdgeWith("owner", velox.FieldEQ("name", "x")))`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(linked["Comment"], []string{"@Doc"}) || !slices.Equal(linked["Doc"], []string{"owner"}) {
		t.Errorf("links %v, want Comment [@Doc] and Doc [owner]", linked)
	}

	// Two subqueries, and the same one twice, link each entity once.
	linked, _, err = c.run(t, read("Comment", `velox.And(velox.FieldInSelect("doc_id", "Doc", "id"), velox.FieldInSelect("doc_id", "Doc", "id"), velox.FieldInSelect("org_id", "User", "id"))`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(linked["Comment"], []string{"@Doc", "@User"}) && !slices.Equal(linked["Comment"], []string{"@User", "@Doc"}) {
		t.Errorf("Comment links %v", linked["Comment"])
	}
}

// A FieldInSelect is refused at its line when the entity or a column is not
// there, the two columns are of different types, or a condition names a
// column the target lacks.
func TestCheckFieldInSelectRefusals(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"entity", read("Comment", `velox.FieldInSelect("doc_id", "Docc", "id")`), `FieldInSelect`, `"Docc" is not a schema of this package (Comment.Policy)`},
		{"own column", read("Comment", `velox.FieldInSelect("docc_id", "Doc", "id")`), `FieldInSelect`, `Comment has no column "docc_id" (Comment.Policy)`},
		{"key", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "ids")`), `FieldInSelect`, `Doc has no column "ids" (Comment.Policy)`},
		{"types", read("Comment", `velox.FieldInSelect("doc_id", "User", "name")`), `FieldInSelect`, `a subquery compares columns of one type`},
		{"condition", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("name", "x"))`), `FieldInSelect`, `Doc, the subquery of Comment's FieldInSelect, has no column "name" (Comment.Policy)`},
		{"condition value", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("title", 1))`), `FieldInSelect`, `column "title" of Doc, the subquery of Comment's FieldInSelect, is a string, not an int`},
		{"entity not constant", "var entity = \"Doc\"\n\n" + read("Comment", `velox.FieldInSelect("doc_id", entity, "id")`), `FieldInSelect`, `names its entity with a value velox-gen cannot read`},
	})
}

// A column or key that is not a constant is not read, but the entity is still
// linked, as the statement checks the rest when it runs.
func TestCheckFieldInSelectUnreadColumns(t *testing.T) {
	c := newCheckEnv(t)
	linked, _, err := c.run(t, "var col = \"doc_id\"\n\n"+read("Comment", `velox.FieldInSelect(col, "Doc", "id")`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(linked["Comment"], []string{"@Doc"}) {
		t.Errorf("Comment links %v, want [@Doc]", linked["Comment"])
	}
}
