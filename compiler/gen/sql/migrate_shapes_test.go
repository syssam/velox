package sql

import (
	"context"
	stdsql "database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect"
	entsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/schema/field"
)

// The migrate generator once built tables itself, beside Graph.Tables, and
// the two drifted. These are the schema shapes it got wrong; it now renders
// Graph.Tables, and these pin both the rendered output and — by migrating
// Graph.Tables on SQLite — that the tables work.

func intInfo() *field.TypeInfo { return &field.TypeInfo{Type: field.TypeInt} }

// columnsLiteral returns the `var <name>Columns = ...` literal.
func columnsLiteral(code, name string) string {
	i := strings.Index(code, "var "+name+"Columns = ")
	if i < 0 {
		return ""
	}
	rest := code[i:]
	if j := strings.Index(rest, "\n\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

var (
	// A one-way O2M edge: no inverse, the FK lives on pets.
	oneWayO2M = []*load.Schema{
		{Name: "User", Edges: []*load.Edge{{Name: "pets", Type: "Pet"}}},
		{Name: "Pet"},
	}
	// An O2O edge bound to an optional edge field.
	optionalO2OEdgeField = []*load.Schema{
		{Name: "User", Edges: []*load.Edge{{Name: "card", Type: "Card", Unique: true}}},
		{
			Name:   "Card",
			Fields: []*load.Field{{Name: "owner_id", Info: intInfo(), Optional: true}},
			Edges:  []*load.Edge{{Name: "owner", Type: "User", RefName: "card", Inverse: true, Unique: true, Field: "owner_id"}},
		},
	}
	// An edge schema keyed by its two edge fields.
	compositeKeyEdgeSchema = []*load.Schema{
		{Name: "User", Edges: []*load.Edge{{Name: "groups", Type: "Group", Through: &struct{ N, T string }{"memberships", "Membership"}}}},
		{Name: "Group", Edges: []*load.Edge{{Name: "users", Type: "User", RefName: "groups", Inverse: true}}},
		{
			Name:        "Membership",
			Annotations: map[string]any{"Fields": map[string]any{"ID": []string{"user_id", "group_id"}}},
			Fields:      []*load.Field{{Name: "user_id", Info: intInfo()}, {Name: "group_id", Info: intInfo()}},
			Edges: []*load.Edge{
				{Name: "user", Type: "User", Unique: true, Required: true, Field: "user_id"},
				{Name: "group", Type: "Group", Unique: true, Required: true, Field: "group_id"},
			},
		},
	}
	// Through declared only on the inverse edge.
	throughOnInverse = []*load.Schema{
		{Name: "User", Edges: []*load.Edge{{Name: "groups", Type: "Group"}}},
		{Name: "Group", Edges: []*load.Edge{{
			Name: "users", Type: "User", RefName: "groups", Inverse: true,
			Through: &struct{ N, T string }{"memberships", "Membership"},
		}}},
		{
			Name:   "Membership",
			Fields: []*load.Field{{Name: "user_id", Info: intInfo()}, {Name: "group_id", Info: intInfo()}},
			Edges: []*load.Edge{
				{Name: "user", Type: "User", Unique: true, Required: true, Field: "user_id"},
				{Name: "group", Type: "Group", Unique: true, Required: true, Field: "group_id"},
			},
		},
	}
)

func TestMigrateSchema_OneWayO2MHasItsForeignKeyColumn(t *testing.T) {
	t.Parallel()
	code := migrateSchemaFor(t, oneWayO2M...)
	require.Contains(t, columnsLiteral(code, "Pet"), `"user_pets"`)
	require.Contains(t, code, "PetTable.ForeignKeys[0].RefTable = UserTable")
}

func TestMigrateSchema_OptionalO2OEdgeFieldIsNullableAndUnique(t *testing.T) {
	t.Parallel()
	cols := columnsLiteral(migrateSchemaFor(t, optionalO2OEdgeField...), "Card")
	require.Contains(t, cols, "Nullable: true", "ON DELETE SET NULL needs a nullable column")
	require.Contains(t, cols, "Unique:   true", "an O2O foreign key is unique")
}

func TestMigrateSchema_CompositeKeyEdgeSchema(t *testing.T) {
	t.Parallel()
	code := migrateSchemaFor(t, compositeKeyEdgeSchema...)
	n := strings.Count(columnsLiteral(code, "Membership"), "Name:")
	require.Equal(t, 2, n)
	for _, m := range regexp.MustCompile(`MembershipColumns\[(\d+)\]`).FindAllStringSubmatch(code, -1) {
		require.Less(t, m[1], "2", "%s indexes past the %d columns", m[0], n)
	}
	require.Contains(t, code, "PrimaryKey: []*schema.Column{MembershipColumns[0], MembershipColumns[1]}")
}

func TestMigrateSchema_ViewsAreNotTables(t *testing.T) {
	t.Parallel()
	code := migrateSchemaFor(t,
		&load.Schema{Name: "User"},
		&load.Schema{Name: "UserStat", View: true, Fields: []*load.Field{{Name: "n", Info: intInfo()}}},
	)
	require.NotContains(t, code, "user_stats")
}

func TestMigrateSchema_ThroughOnInverseEmitsOneTable(t *testing.T) {
	t.Parallel()
	code := migrateSchemaFor(t, throughOnInverse...)
	require.Len(t, regexp.MustCompile(`Name:\s+"memberships"`).FindAllString(code, -1), 1)
}

// TestMigrateSchema_ShapesMigrateAndWork migrates what the generated schema
// renders — Graph.Tables — on SQLite and exercises the rows the old output
// broke: an owner-less card and deleting a card's owner (NOT NULL column
// with ON DELETE SET NULL), and a duplicate membership (composite key).
func TestMigrateSchema_ShapesMigrateAndWork(t *testing.T) {
	t.Parallel()
	migrate := func(t *testing.T, schemas ...*load.Schema) *stdsql.DB {
		t.Helper()
		g, err := gen.NewGraph(&gen.Config{Package: "example.com/app/velox"}, schemas...)
		require.NoError(t, err)
		tables, err := g.Tables()
		require.NoError(t, err)
		db, err := stdsql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		m, err := schema.NewMigrate(entsql.OpenDB(dialect.SQLite, db))
		require.NoError(t, err)
		require.NoError(t, m.Create(context.Background(), tables...))
		return db
	}
	exec := func(t *testing.T, db *stdsql.DB, q string) {
		t.Helper()
		_, err := db.Exec(q)
		require.NoError(t, err, q)
	}

	t.Run("one-way O2M", func(t *testing.T) {
		db := migrate(t, oneWayO2M...)
		exec(t, db, `INSERT INTO users (id) VALUES (1)`)
		exec(t, db, `INSERT INTO pets (id, user_pets) VALUES (1, 1)`)
	})
	t.Run("optional O2O edge field", func(t *testing.T) {
		db := migrate(t, optionalO2OEdgeField...)
		exec(t, db, `INSERT INTO users (id) VALUES (1)`)
		exec(t, db, `INSERT INTO cards (id) VALUES (10)`)
		exec(t, db, `INSERT INTO cards (id, owner_id) VALUES (11, 1)`)
		_, err := db.Exec(`INSERT INTO cards (id, owner_id) VALUES (12, 1)`)
		require.Error(t, err, "one card per owner")
		exec(t, db, `DELETE FROM users WHERE id = 1`)
		var owner stdsql.NullInt64
		require.NoError(t, db.QueryRow(`SELECT owner_id FROM cards WHERE id = 11`).Scan(&owner))
		require.False(t, owner.Valid, "ON DELETE SET NULL")
	})
	t.Run("composite key edge schema", func(t *testing.T) {
		db := migrate(t, compositeKeyEdgeSchema...)
		exec(t, db, `INSERT INTO users (id) VALUES (1)`)
		exec(t, db, `INSERT INTO groups (id) VALUES (1)`)
		exec(t, db, `INSERT INTO memberships (user_id, group_id) VALUES (1, 1)`)
		_, err := db.Exec(`INSERT INTO memberships (user_id, group_id) VALUES (1, 1)`)
		require.Error(t, err, "the composite primary key rejects a duplicate")
	})
	t.Run("through on inverse", func(t *testing.T) {
		db := migrate(t, throughOnInverse...)
		exec(t, db, `INSERT INTO users (id) VALUES (1)`)
		exec(t, db, `INSERT INTO groups (id) VALUES (1)`)
		exec(t, db, `INSERT INTO memberships (user_id, group_id) VALUES (1, 1)`)
	})
}

// TestMigrateSchema_ForeignKeySymbols pins the constraint names, which match
// Ent: {owner}_{ref}_{assoc edge}. A bidirectional pair names the FK after
// the assoc edge (Post.comments), not the M2O edge that owns the column
// (Comment.post) — a drift once shipped here; a standalone M2O after itself.
func TestMigrateSchema_ForeignKeySymbols(t *testing.T) {
	t.Parallel()
	code := migrateSchemaFor(t,
		&load.Schema{Name: "Post", Edges: []*load.Edge{{Name: "comments", Type: "Comment"}}},
		&load.Schema{Name: "Comment", Edges: []*load.Edge{
			{Name: "post", Type: "Post", RefName: "comments", Inverse: true, Unique: true},
			{Name: "author", Type: "User", Unique: true},
		}},
		&load.Schema{Name: "User"},
	)
	require.Contains(t, code, `"comments_posts_comments"`)
	require.NotContains(t, code, `"comments_posts_post"`)
	require.Contains(t, code, `"comments_users_author"`)
}
