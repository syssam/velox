package codegen

import (
	"strconv"
	"strings"
	"testing"
)

// A FieldInSelect names an entity of the package by its name in the schema or
// by its table, as velox's EntityTable finds it; its conditions name that
// entity's columns and relations.
func TestCheckFieldInSelectAccepts(t *testing.T) {
	c := newCheckEnv(t)
	c.accepts(t, map[string]string{
		"by name":    read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("title", "x"))`),
		"by table":   read("Comment", `velox.FieldInSelect("doc_id", "docs", "id", velox.FieldEQ("title", "x"))`),
		"a relation": read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.HasEdgeWith("owner", velox.FieldEQ("name", "x")))`),
		"several":    read("Comment", `velox.And(velox.FieldInSelect("doc_id", "Doc", "id"), velox.FieldInSelect("doc_id", "Doc", "id"), velox.FieldInSelect("org_id", "users", "id"))`),
		// An integer column with a text key is compared as text (ADR 0091).
		"text key":          read("Comment", `velox.FieldInSelect("doc_id", "User", "name")`),
		"unsigned text key": read("User", `velox.FieldInSelect("flags", "Doc", "title")`),
		// A text column with an integer key too (ADR 0157).
		"text column": read("Doc", `velox.FieldInSelect("title", "Comment", "doc_id")`),
		"raw":         read("Doc", `velox.FieldInSelectRaw("history", "title", "Comment", "doc_id", velox.FieldEQ("doc_id", 1))`),
	})
}

// A FieldInSelect is refused at its line when the entity or a column is not
// there, the two columns are of different types (other than an integer and
// text), or a condition names a
// column the target lacks.
func TestCheckFieldInSelectRefusals(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"entity", read("Comment", `velox.FieldInSelect("doc_id", "Docc", "id")`), `FieldInSelect`, `"Docc" is neither a schema nor a table of this package (Comment.Policy)`},
		{"condition by table", read("Comment", `velox.FieldInSelect("doc_id", "docs", "id", velox.FieldEQ("name", "x"))`), `FieldInSelect`, `Doc, the subquery of Comment's FieldInSelect, has no column "name"`},
		{"own column", read("Comment", `velox.FieldInSelect("docc_id", "Doc", "id")`), `FieldInSelect`, `Comment has no column "docc_id"; did you mean "doc_id"? (Comment.Policy)`},
		{"key", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "ids")`), `FieldInSelect`, `Doc has no column "ids"; did you mean "id"? (Comment.Policy)`},
		{"types", read("User", `velox.FieldInSelect("score", "Comment", "doc_id")`), `FieldInSelect`, `column "score" of User is a float32 and column "doc_id" of Comment is an int64: a subquery compares columns of one type, or an integer and a text column`},
		{"raw key", read("Comment", `velox.FieldInSelectRaw("history", "doc_id", "Doc", "ids")`), `FieldInSelectRaw`, `Doc has no column "ids"; did you mean "id"? (Comment.Policy)`},
		{"raw condition", read("Comment", `velox.FieldInSelectRaw("history", "doc_id", "Doc", "id", velox.FieldEQ("name", "x"))`), `FieldInSelectRaw`, `Doc, the subquery of Comment's FieldInSelect, has no column "name" (Comment.Policy)`},
		{"a float and text", read("User", `velox.FieldInSelect("score", "Doc", "title")`), `FieldInSelect`, `a subquery compares columns of one type`},
		{"condition", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("name", "x"))`), `FieldInSelect`, `Doc, the subquery of Comment's FieldInSelect, has no column "name" (Comment.Policy)`},
		{"condition value", read("Comment", `velox.FieldInSelect("doc_id", "Doc", "id", velox.FieldEQ("title", 1))`), `FieldInSelect`, `column "title" of Doc, the subquery of Comment's FieldInSelect, is a string, not an int`},
	})
}

// An entity or a relation named by a value velox-gen cannot read, as one
// from configuration is, is a warning and not a refusal: velox finds it in
// the package's EntityTable when the statement runs. Its conditions, whose
// entity is not known, are not checked. A column or key that is not a
// constant is not read, and the call is accepted.
func TestCheckWarnsUnreadNames(t *testing.T) {
	c := newCheckEnv(t)
	decls := "var entity, rel = \"Doc\", \"doc\"\n\n" + read("Comment",
		`velox.And(velox.FieldInSelect("doc_id", entity, "id", velox.FieldEQ("nope", 1)), velox.HasEdgeWith(rel, velox.FieldEQ("nope", 1)))`)
	warnings, err := c.run(t, decls)
	if err != nil {
		t.Fatal(err)
	}
	line := "behaviour.go:" + strconv.Itoa(lineOf(t, decls, "velox.And(")) + ": "
	want := []string{
		line + `velox.FieldInSelect("doc_id", entity, "id", velox.FieldEQ("nope", 1)) names its entity with a value velox-gen cannot read; velox checks it when the statement runs`,
		line + `velox.HasEdgeWith(rel, velox.FieldEQ("nope", 1)) names its relation with a value velox-gen cannot read; velox checks it when the statement runs`,
	}
	if len(warnings) != len(want) {
		t.Fatalf("warnings %q", warnings)
	}
	for i, w := range want {
		if !strings.HasSuffix(warnings[i], w) {
			t.Errorf("warning %q\nwant suffix %q", warnings[i], w)
		}
	}
	if _, err := c.run(t, "var col = \"doc_id\"\n\n"+read("Comment", `velox.FieldInSelect(col, "Doc", "id")`)); err != nil {
		t.Errorf("a column velox-gen cannot read: %v", err)
	}
}
