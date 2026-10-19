package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

// logic is the head of every where input.
func logic(typ string) []string {
	return []string{"not: " + typ, "and: [" + typ + "!]", "or: [" + typ + "!]"}
}

func TestWhere(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		opt := func(f *plugin.Field) *plugin.Field { f.Nullable, f.Optional = true, true; return f }
		cases := []struct {
			f    *plugin.Field
			want []string
		}{
			{field("s", "string"), []string{"s: String", "sNEQ: String", "sIn: [String!]", "sNotIn: [String!]", "sContains: String", "sHasPrefix: String", "sHasSuffix: String"}},
			{field("i", "int"), []string{"i: Int", "iNEQ: Int", "iIn: [Int!]", "iNotIn: [Int!]", "iGT: Int", "iGTE: Int", "iLT: Int", "iLTE: Int"}},
			{field("u", "uint64"), []string{"u: Uint64", "uNEQ: Uint64", "uIn: [Uint64!]", "uNotIn: [Uint64!]", "uGT: Uint64", "uGTE: Uint64", "uLT: Uint64", "uLTE: Uint64"}},
			{field("f", "float64"), []string{"f: Float", "fNEQ: Float", "fIn: [Float!]", "fNotIn: [Float!]", "fGT: Float", "fGTE: Float", "fLT: Float", "fLTE: Float"}},
			{field("t", "time"), []string{"t: Time", "tNEQ: Time", "tIn: [Time!]", "tNotIn: [Time!]", "tGT: Time", "tGTE: Time", "tLT: Time", "tLTE: Time"}},
			{field("b", "bool"), []string{"b: Boolean", "bNEQ: Boolean"}},
			{&plugin.Field{Name: "e", GoName: "E", Type: "enum", EnumValues: []string{"x"}}, []string{"e: UserE", "eNEQ: UserE", "eIn: [UserE!]", "eNotIn: [UserE!]"}},
			{field("uid", "uuid"), []string{"uid: UUID", "uidNEQ: UUID", "uidIn: [UUID!]", "uidNotIn: [UUID!]"}},
			{opt(field("o", "string")), []string{"o: String", "oNEQ: String", "oIn: [String!]", "oNotIn: [String!]", "oContains: String", "oHasPrefix: String", "oHasSuffix: String", "oIsNil: Boolean", "oNotNil: Boolean"}},
			{opt(field("ob", "bool")), []string{"ob: Boolean", "obNEQ: Boolean", "obIsNil: Boolean", "obNotNil: Boolean"}},
		}
		for _, c := range cases {
			c.f.Annotations = ann(t, graphql.WhereOps())
			s, sdl := load(t, model(entity("User", c.f)))
			want := append(logic("UserWhereInput"), c.want...)
			if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
				t.Errorf("%s (%s):\n got %v\nwant %v\n%s", c.f.Name, c.f.Type, got, want, sdl)
			}
		}
	})

	t.Run("ID", func(t *testing.T) {
		u := entity("User")
		u.Fields[0].Annotations = ann(t, graphql.WhereOps())
		s, _ := load(t, model(u))
		want := append(logic("UserWhereInput"), "id: ID", "idNEQ: ID", "idIn: [ID!]", "idNotIn: [ID!]")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v", got, want)
		}
	})

	// A foreign key is never a raw scalar: its edge names its predicates, and
	// they take IDs.
	t.Run("foreign key", func(t *testing.T) {
		aid := field("author_id", "int64")
		aid.Annotations = ann(t, graphql.WhereOps())
		eid := &plugin.Field{Name: "editor_id", GoName: "EditorID", Type: "int64", Nullable: true, Optional: true}
		eid.Annotations = ann(t, graphql.WhereOps(graphql.OpEQ, graphql.OpIsNil))
		post := entity("Post", aid, eid)
		author := edge("author", "M2O", "User")
		author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
		author.Annotations = ann(t, graphql.FieldName("writer"))
		editor := edge("editor", "M2O", "User")
		editor.Field, editor.FieldOn = "editor_id", "Post"
		post.Edges = []*plugin.Edge{author, editor}
		user := entity("User")
		posts := edge("posts", "O2M", "Post")
		posts.Field, posts.FieldOn = "author_id", "Post"
		user.Edges = []*plugin.Edge{posts}
		s, sdl := load(t, model(post, user))
		want := append(logic("PostWhereInput"), "writerID: ID", "writerIDNEQ: ID", "writerIDIn: [ID!]", "writerIDNotIn: [ID!]", "editorID: ID", "editorIDIsNil: Boolean")
		if got := fieldsOf(s, "PostWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v\n%s", got, want, sdl)
		}
		if strings.Contains(sdl, "authorID") {
			t.Errorf("the raw key is exposed:\n%s", sdl)
		}

		// With its edge out of GraphQL, the key has no name to filter by.
		author.Annotations = ann(t, graphql.Skip())
		renderErr(t, model(post, user), "schema Post, field author_id", "WhereOps", "edge")
	})

	t.Run("each op", func(t *testing.T) {
		ops := []struct {
			op   graphql.WhereOp
			want string
		}{
			{graphql.OpEQ, "n: String"}, {graphql.OpNEQ, "nNEQ: String"},
			{graphql.OpIn, "nIn: [String!]"}, {graphql.OpNotIn, "nNotIn: [String!]"},
			{graphql.OpGT, "nGT: String"}, {graphql.OpGTE, "nGTE: String"},
			{graphql.OpLT, "nLT: String"}, {graphql.OpLTE, "nLTE: String"},
			{graphql.OpContains, "nContains: String"}, {graphql.OpHasPrefix, "nHasPrefix: String"},
			{graphql.OpHasSuffix, "nHasSuffix: String"},
			{graphql.OpIsNil, "nIsNil: Boolean"}, {graphql.OpNotNil, "nNotNil: Boolean"},
		}
		for _, c := range ops {
			n := &plugin.Field{Name: "n", GoName: "N", Type: "string", Nullable: true, Optional: true}
			n.Annotations = ann(t, graphql.WhereOps(c.op))
			s, _ := load(t, model(entity("User", n)))
			want := append(logic("UserWhereInput"), c.want)
			if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
				t.Errorf("%s: got %v", c.want, got)
			}
		}
	})

	t.Run("merged and presets", func(t *testing.T) {
		// WhereOps() with WhereOps(OpGT): the defaults plus GT.
		n := field("n", "string")
		n.Annotations = ann(t, graphql.WhereOps(), graphql.WhereOps(graphql.OpGT))
		// OpsAll: every predicate the type has.
		i := field("i", "int")
		i.Annotations = ann(t, graphql.WhereOps(graphql.OpsAll))
		s, _ := load(t, model(entity("User", n, i)))
		want := append(logic("UserWhereInput"),
			"n: String", "nNEQ: String", "nIn: [String!]", "nNotIn: [String!]", "nGT: String", "nContains: String", "nHasPrefix: String", "nHasSuffix: String",
			"i: Int", "iNEQ: Int", "iIn: [Int!]", "iNotIn: [Int!]", "iGT: Int", "iGTE: Int", "iLT: Int", "iLTE: Int")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v", got, want)
		}
	})

	t.Run("FieldName", func(t *testing.T) {
		n := field("name", "string")
		n.Annotations = ann(t, graphql.FieldName("displayName"), graphql.WhereOps(graphql.OpEQ, graphql.OpIn))
		s, _ := load(t, model(entity("User", n)))
		want := append(logic("UserWhereInput"), "displayName: String", "displayNameIn: [String!]")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("WhereEdge", func(t *testing.T) {
		user := entity("User")
		posts := edge("posts", "O2M", "Post")
		posts.Annotations = ann(t, graphql.WhereEdge())
		groups := edge("groups", "M2M", "Group")
		groups.Annotations = ann(t, graphql.WhereEdge(), graphql.FieldName("memberships"))
		user.Edges = []*plugin.Edge{posts, groups, edge("profile", "O2O", "Group")}
		user.Annotations = ann(t, graphql.QueryField("users"))
		title := field("title", "string")
		title.Annotations = ann(t, graphql.WhereOps())
		post := entity("Post", title)
		post.Edges = []*plugin.Edge{edge("author", "M2O", "User")}
		s, sdl := load(t, model(entity("Group"), post, user))
		// Group has no where input: memberships gets has, not hasWith.
		want := append(logic("UserWhereInput"), "hasPosts: Boolean", "hasPostsWith: [PostWhereInput!]", "hasMemberships: Boolean")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v\n%s", got, want, sdl)
		}
		conn := []string{"after: Cursor", "first: Int", "before: Cursor", "last: Int"}
		for _, c := range []struct {
			typ, name string
			want      []string
		}{
			{"Query", "users", append(slices.Clone(conn), "where: UserWhereInput")},
			{"User", "posts", append(slices.Clone(conn), "where: PostWhereInput")},
			{"User", "memberships", conn},
		} {
			if got := argsOf(s, c.typ, c.name); !slices.Equal(got, c.want) {
				t.Errorf("%s.%s: %v, want %v", c.typ, c.name, got, c.want)
			}
		}
		if s.Types["GroupWhereInput"] != nil {
			t.Errorf("Group has a where input:\n%s", sdl)
		}
	})

	t.Run("none", func(t *testing.T) {
		n := field("name", "string")
		n.Annotations = ann(t, graphql.WhereOps())
		user := entity("User", n)
		user.Annotations = ann(t, graphql.QueryField("users"), graphql.Skip(graphql.SkipWhereInput))
		skipped := field("label", "string")
		skipped.Annotations = ann(t, graphql.WhereOps(), graphql.Skip(graphql.SkipWhereInput))
		tag := entity("Tag", skipped)
		tag.Annotations = ann(t, graphql.QueryField("tags"))
		s, sdl := load(t, model(tag, user))
		if s.Types["UserWhereInput"] != nil || s.Types["TagWhereInput"] != nil || strings.Contains(sdl, "where:") {
			t.Errorf("a where input:\n%s", sdl)
		}
	})

	t.Run("refused", func(t *testing.T) {
		i := field("i", "int")
		i.Annotations = ann(t, graphql.WhereOps(graphql.OpContains))
		renderErr(t, model(entity("User", i)), "schema User, field i", "OpContains", "int")
		i.Annotations = ann(t, graphql.WhereOps(graphql.OpIsNil))
		renderErr(t, model(entity("User", i)), "schema User, field i", "OpIsNil")
		j := field("doc", "json")
		j.Annotations = ann(t, graphql.WhereOps())
		renderErr(t, model(entity("User", j)), "schema User, field doc", "json")
	})
}
