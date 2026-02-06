package sqlgraph

import (
	"strconv"
	"testing"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/querylanguage"
	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraph_AddE(t *testing.T) {
	g := &Schema{
		Nodes: []*Node{{Type: "user"}, {Type: "pet"}},
	}
	err := g.AddE("pets", &EdgeSpec{Rel: O2M}, "user", "pet")
	assert.NoError(t, err)
	err = g.AddE("owner", &EdgeSpec{Rel: O2M}, "pet", "user")
	assert.NoError(t, err)
	err = g.AddE("groups", &EdgeSpec{Rel: M2M}, "pet", "groups")
	assert.Error(t, err)
}

func TestGraph_EvalP(t *testing.T) {
	g := &Schema{
		Nodes: []*Node{
			{
				Type: "user",
				NodeSpec: NodeSpec{
					Table: "users",
					ID:    &FieldSpec{Column: "uid"},
				},
				Fields: map[string]*FieldSpec{
					"name": {Column: "name", Type: field.TypeString},
					"last": {Column: "last", Type: field.TypeString},
				},
			},
			{
				Type: "pet",
				NodeSpec: NodeSpec{
					Table: "pets",
					ID:    &FieldSpec{Column: "pid"},
				},
				Fields: map[string]*FieldSpec{
					"name": {Column: "name", Type: field.TypeString},
				},
			},
			{
				Type: "group",
				NodeSpec: NodeSpec{
					Table: "groups",
					ID:    &FieldSpec{Column: "gid"},
				},
				Fields: map[string]*FieldSpec{
					"name": {Column: "name", Type: field.TypeString},
				},
			},
		},
	}
	err := g.AddE("pets", &EdgeSpec{Rel: O2M, Table: "pets", Columns: []string{"owner_id"}}, "user", "pet")
	require.NoError(t, err)
	err = g.AddE("owner", &EdgeSpec{Rel: M2O, Inverse: true, Table: "pets", Columns: []string{"owner_id"}}, "pet", "user")
	require.NoError(t, err)
	err = g.AddE("groups", &EdgeSpec{Rel: M2M, Table: "user_groups", Columns: []string{"user_id", "group_id"}}, "user", "group")
	require.NoError(t, err)
	err = g.AddE("users", &EdgeSpec{Rel: M2M, Inverse: true, Table: "user_groups", Columns: []string{"user_id", "group_id"}}, "group", "user")
	require.NoError(t, err)

	tests := []struct {
		s         *sql.Selector
		p         querylanguage.P
		wantQuery string
		wantArgs  []any
		wantErr   bool
	}{
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.FieldHasPrefix("name", "a"),
			wantQuery: `SELECT * FROM "users" WHERE "users"."name" LIKE $1`,
			wantArgs:  []any{"a%"},
		},
		{
			s: sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).
				Where(sql.EQ("age", 1)),
			p:         querylanguage.FieldHasPrefix("name", "a"),
			wantQuery: `SELECT * FROM "users" WHERE "age" = $1 AND "users"."name" LIKE $2`,
			wantArgs:  []any{1, "a%"},
		},
		{
			s: sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).
				Where(sql.EQ("age", 1)),
			p:         querylanguage.FieldHasPrefix("name", "a"),
			wantQuery: `SELECT * FROM "users" WHERE "age" = $1 AND "users"."name" LIKE $2`,
			wantArgs:  []any{1, "a%"},
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.EQ(querylanguage.F("name"), querylanguage.F("last")),
			wantQuery: `SELECT * FROM "users" WHERE "users"."name" = "users"."last"`,
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.EQ(querylanguage.F("name"), querylanguage.F("last")),
			wantQuery: `SELECT * FROM "users" WHERE "users"."name" = "users"."last"`,
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.And(querylanguage.FieldNil("name"), querylanguage.FieldNotNil("last")),
			wantQuery: `SELECT * FROM "users" WHERE "users"."name" IS NULL AND "users"."last" IS NOT NULL`,
		},
		{
			s: sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).
				Where(sql.EQ("foo", "bar")),
			p:         querylanguage.Or(querylanguage.FieldEQ("name", "foo"), querylanguage.FieldEQ("name", "baz")),
			wantQuery: `SELECT * FROM "users" WHERE "foo" = $1 AND ("users"."name" = $2 OR "users"."name" = $3)`,
			wantArgs:  []any{"bar", "foo", "baz"},
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.HasEdge("pets"),
			wantQuery: `SELECT * FROM "users" WHERE EXISTS (SELECT "pets"."owner_id" FROM "pets" WHERE "users"."uid" = "pets"."owner_id")`,
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.HasEdge("groups"),
			wantQuery: `SELECT * FROM "users" WHERE "users"."uid" IN (SELECT "user_groups"."user_id" FROM "user_groups")`,
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")),
			p:         querylanguage.HasEdgeWith("pets", querylanguage.Or(querylanguage.FieldEQ("name", "pedro"), querylanguage.FieldEQ("name", "xabi"))),
			wantQuery: `SELECT * FROM "users" WHERE EXISTS (SELECT "pets"."owner_id" FROM "pets" WHERE "users"."uid" = "pets"."owner_id" AND ("pets"."name" = $1 OR "pets"."name" = $2))`,
			wantArgs:  []any{"pedro", "xabi"},
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).Where(sql.EQ("active", true)),
			p:         querylanguage.HasEdgeWith("groups", querylanguage.Or(querylanguage.FieldEQ("name", "GitHub"), querylanguage.FieldEQ("name", "GitLab"))),
			wantQuery: `SELECT * FROM "users" WHERE "active" AND "users"."uid" IN (SELECT "user_groups"."user_id" FROM "user_groups" JOIN "groups" AS "t1" ON "user_groups"."group_id" = "t1"."gid" WHERE "t1"."name" = $1 OR "t1"."name" = $2)`,
			wantArgs:  []any{"GitHub", "GitLab"},
		},
		{
			s:         sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).Where(sql.EQ("active", true)),
			p:         querylanguage.And(querylanguage.HasEdge("pets"), querylanguage.HasEdge("groups"), querylanguage.EQ(querylanguage.F("name"), querylanguage.F("uid"))),
			wantQuery: `SELECT * FROM "users" WHERE "active" AND (EXISTS (SELECT "pets"."owner_id" FROM "pets" WHERE "users"."uid" = "pets"."owner_id") AND "users"."uid" IN (SELECT "user_groups"."user_id" FROM "user_groups") AND "users"."name" = "users"."uid")`,
		},
		{
			s: sql.Dialect(dialect.Postgres).Select().From(sql.Table("users")).Where(sql.EQ("active", true)),
			p: querylanguage.HasEdgeWith("pets", querylanguage.FieldEQ("name", "pedro"), WrapFunc(func(s *sql.Selector) {
				s.Where(sql.EQ("owner_id", 10))
			})),
			wantQuery: `SELECT * FROM "users" WHERE "active" AND EXISTS (SELECT "pets"."owner_id" FROM "pets" WHERE ("users"."uid" = "pets"."owner_id" AND "pets"."name" = $1) AND "owner_id" = $2)`,
			wantArgs:  []any{"pedro", 10},
		},
	}
	for i, tt := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			err = g.EvalP("user", tt.p, tt.s)
			require.Equal(t, tt.wantErr, err != nil, err)
			query, args := tt.s.Query()
			require.Equal(t, tt.wantQuery, query)
			require.Equal(t, tt.wantArgs, args)
		})
	}
}
