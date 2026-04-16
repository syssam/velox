package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	entsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"

	_ "modernc.org/sqlite"
)

// openSQLite opens an in-memory SQLite database with foreign keys enabled.
func openSQLite(t *testing.T) (*sql.DB, dialect.Driver) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	drv := entsql.OpenDB(dialect.SQLite, db)
	return db, drv
}

// newSQLiteMigrate creates an Atlas migration engine for SQLite.
func newSQLiteMigrate(t *testing.T, drv dialect.Driver, opts ...MigrateOption) *Atlas {
	t.Helper()
	m, err := NewMigrate(drv, opts...)
	require.NoError(t, err)
	return m
}

// queryPragma returns the result of a PRAGMA query as a slice of maps.
func queryPragma(t *testing.T, db *sql.DB, pragma string) []map[string]string {
	t.Helper()
	rows, err := db.Query(pragma)
	require.NoError(t, err)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var results []map[string]string
	for rows.Next() {
		values := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		row := make(map[string]string, len(cols))
		for i, col := range cols {
			row[col] = values[i].String
		}
		results = append(results, row)
	}
	require.NoError(t, rows.Err())
	return results
}

// tableExists checks if a table exists in the SQLite database.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

func TestSQLiteIntegration_CreateSingleTable(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	users := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
			{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
			{Name: "age", Type: field.TypeInt},
			{Name: "active", Type: field.TypeBool},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, users))
	assert.True(t, tableExists(t, db, "users"))

	// Verify columns via PRAGMA.
	cols := queryPragma(t, db, "PRAGMA table_info(users)")
	require.Len(t, cols, 5)

	colByName := make(map[string]map[string]string)
	for _, c := range cols {
		colByName[c["name"]] = c
	}

	// Verify column types (SQLite returns uppercase type names).
	assert.True(t, strings.EqualFold("integer", colByName["id"]["type"]))
	assert.True(t, strings.EqualFold("text", colByName["name"]["type"]))
	assert.True(t, strings.EqualFold("text", colByName["email"]["type"]))
	assert.True(t, strings.EqualFold("integer", colByName["age"]["type"]))
	assert.True(t, strings.EqualFold("bool", colByName["active"]["type"]))

	// Verify NOT NULL (notnull=1 means NOT NULL).
	assert.Equal(t, "1", colByName["id"]["notnull"])
	assert.Equal(t, "1", colByName["name"]["notnull"])

	// Verify primary key.
	assert.Equal(t, "1", colByName["id"]["pk"])
}

func TestSQLiteIntegration_CreateWithForeignKeys(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	users := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	posts := &Table{
		Name: "posts",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "title", Type: field.TypeString, Size: 255},
			{Name: "user_posts", Type: field.TypeInt64},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     "posts_users_posts",
				Columns:    []*Column{{Name: "user_posts", Type: field.TypeInt64}},
				RefTable:   users,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "CASCADE",
			},
		},
	}

	require.NoError(t, m.Create(ctx, users, posts))
	assert.True(t, tableExists(t, db, "users"))
	assert.True(t, tableExists(t, db, "posts"))

	// Verify foreign key.
	fks := queryPragma(t, db, "PRAGMA foreign_key_list(posts)")
	require.NotEmpty(t, fks, "posts should have foreign keys")
	assert.Equal(t, "users", fks[0]["table"])
	assert.Equal(t, "id", fks[0]["to"])
	assert.Equal(t, "user_posts", fks[0]["from"])

	// Verify CASCADE behavior: insert user, insert post, delete user.
	_, err := db.Exec("INSERT INTO users (id, name) VALUES (1, 'Alice')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO posts (id, title, user_posts) VALUES (1, 'Hello', 1)")
	require.NoError(t, err)

	// Delete user should cascade to posts.
	_, err = db.Exec("DELETE FROM users WHERE id = 1")
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&count))
	assert.Equal(t, 0, count, "posts should be cascade-deleted when user is deleted")
}

func TestSQLiteIntegration_CreateWithIndexes(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	users := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
			{Name: "role", Type: field.TypeEnum},
			{Name: "created_at", Type: field.TypeTime},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		Indexes: []*Index{
			{
				Name:    "users_role_created_at",
				Columns: []*Column{{Name: "role"}, {Name: "created_at"}},
				columns: []string{"role", "created_at"},
			},
		},
	}

	require.NoError(t, m.Create(ctx, users))

	// Verify indexes via PRAGMA.
	indexes := queryPragma(t, db, "PRAGMA index_list(users)")
	require.NotEmpty(t, indexes)

	// Find our composite index.
	var found bool
	for _, idx := range indexes {
		if idx["name"] == "users_role_created_at" {
			found = true
			assert.Equal(t, "0", idx["unique"], "composite index should not be unique")
			break
		}
	}
	assert.True(t, found, "composite index users_role_created_at should exist")

	// Find the unique index on email.
	var foundUnique bool
	for _, idx := range indexes {
		if idx["unique"] == "1" {
			// Check if this index covers the email column.
			idxInfo := queryPragma(t, db, "PRAGMA index_info("+idx["name"]+")")
			for _, col := range idxInfo {
				if col["name"] == "email" {
					foundUnique = true
					break
				}
			}
		}
	}
	assert.True(t, foundUnique, "unique index on email should exist")
}

func TestSQLiteIntegration_SchemaEvolution(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv, WithDropColumn(true), WithDropIndex(true))
	ctx := context.Background()

	// Step 1: Create initial table.
	v1 := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}
	require.NoError(t, m.Create(ctx, v1))

	// Insert data.
	_, err := db.Exec("INSERT INTO users (id, name) VALUES (1, 'Alice')")
	require.NoError(t, err)

	// Step 2: Evolve schema - add a column.
	v2 := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
			{Name: "email", Type: field.TypeString, Size: 255, Nullable: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	// Need new migrate instance since Atlas caches state.
	m2 := newSQLiteMigrate(t, drv, WithDropColumn(true), WithDropIndex(true))
	require.NoError(t, m2.Create(ctx, v2))

	// Verify new column exists.
	cols := queryPragma(t, db, "PRAGMA table_info(users)")
	colNames := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = c["name"]
	}
	assert.Contains(t, colNames, "email", "email column should be added")

	// Verify existing data is preserved.
	var name string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 1").Scan(&name))
	assert.Equal(t, "Alice", name)
}

func TestSQLiteIntegration_AllFieldTypes(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "all_types",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "bool_col", Type: field.TypeBool},
			{Name: "int_col", Type: field.TypeInt},
			{Name: "int8_col", Type: field.TypeInt8},
			{Name: "int16_col", Type: field.TypeInt16},
			{Name: "int32_col", Type: field.TypeInt32},
			{Name: "int64_col", Type: field.TypeInt64},
			{Name: "uint_col", Type: field.TypeUint},
			{Name: "uint8_col", Type: field.TypeUint8},
			{Name: "uint16_col", Type: field.TypeUint16},
			{Name: "uint32_col", Type: field.TypeUint32},
			{Name: "uint64_col", Type: field.TypeUint64},
			{Name: "float32_col", Type: field.TypeFloat32},
			{Name: "float64_col", Type: field.TypeFloat64},
			{Name: "string_col", Type: field.TypeString, Size: 255},
			{Name: "text_col", Type: field.TypeString, Size: 1 << 20},
			{Name: "bytes_col", Type: field.TypeBytes},
			{Name: "time_col", Type: field.TypeTime},
			{Name: "json_col", Type: field.TypeJSON},
			{Name: "uuid_col", Type: field.TypeUUID},
			{Name: "enum_col", Type: field.TypeEnum},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, table))
	assert.True(t, tableExists(t, db, "all_types"))

	// Verify all columns were created.
	cols := queryPragma(t, db, "PRAGMA table_info(all_types)")
	assert.Len(t, cols, 21, "should have 21 columns for all field types")
}

func TestSQLiteIntegration_NullableColumns(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "nullable_test",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "required_name", Type: field.TypeString, Size: 255},
			{Name: "optional_bio", Type: field.TypeString, Size: 255, Nullable: true},
			{Name: "optional_age", Type: field.TypeInt, Nullable: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, table))

	// Insert with NULL optional fields.
	_, err := db.Exec("INSERT INTO nullable_test (id, required_name) VALUES (1, 'Alice')")
	require.NoError(t, err)

	// Verify NULL values.
	var bio sql.NullString
	var age sql.NullInt64
	require.NoError(t, db.QueryRow(
		"SELECT optional_bio, optional_age FROM nullable_test WHERE id = 1",
	).Scan(&bio, &age))
	assert.False(t, bio.Valid, "optional_bio should be NULL")
	assert.False(t, age.Valid, "optional_age should be NULL")

	// Insert with non-NULL values.
	_, err = db.Exec("INSERT INTO nullable_test (id, required_name, optional_bio, optional_age) VALUES (2, 'Bob', 'Developer', 30)")
	require.NoError(t, err)

	require.NoError(t, db.QueryRow(
		"SELECT optional_bio, optional_age FROM nullable_test WHERE id = 2",
	).Scan(&bio, &age))
	assert.True(t, bio.Valid)
	assert.Equal(t, "Developer", bio.String)
	assert.True(t, age.Valid)
	assert.Equal(t, int64(30), age.Int64)
}

func TestSQLiteIntegration_DefaultValues(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "defaults_test",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "status", Type: field.TypeString, Size: 50, Default: "active"},
			{Name: "count", Type: field.TypeInt, Default: 0},
			{Name: "enabled", Type: field.TypeBool, Default: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, table))

	// Insert without specifying defaults.
	_, err := db.Exec("INSERT INTO defaults_test (id) VALUES (1)")
	require.NoError(t, err)

	var status string
	var count int
	var enabled bool
	require.NoError(t, db.QueryRow(
		"SELECT status, count, enabled FROM defaults_test WHERE id = 1",
	).Scan(&status, &count, &enabled))
	assert.Equal(t, "active", status)
	assert.Equal(t, 0, count)
	assert.Equal(t, true, enabled)
}

func TestSQLiteIntegration_UniqueConstraint(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "unique_test",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, table))

	// Insert first row.
	_, err := db.Exec("INSERT INTO unique_test (id, email) VALUES (1, 'alice@example.com')")
	require.NoError(t, err)

	// Insert duplicate email should fail.
	_, err = db.Exec("INSERT INTO unique_test (id, email) VALUES (2, 'alice@example.com')")
	assert.Error(t, err, "duplicate unique value should fail")
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
}

func TestSQLiteIntegration_ForeignKeyEnforcement(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	users := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	posts := &Table{
		Name: "posts",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "title", Type: field.TypeString, Size: 255},
			{Name: "user_posts", Type: field.TypeInt64},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     "posts_users_posts",
				Columns:    []*Column{{Name: "user_posts", Type: field.TypeInt64}},
				RefTable:   users,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "SET NULL",
			},
		},
	}

	require.NoError(t, m.Create(ctx, users, posts))

	// Insert a post referencing non-existent user should fail.
	_, err := db.Exec("INSERT INTO posts (id, title, user_posts) VALUES (1, 'Orphan', 999)")
	assert.Error(t, err, "foreign key constraint should prevent orphan rows")
}

func TestSQLiteIntegration_MultipleTablesWithRelationships(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	users := &Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	groups := &Table{
		Name: "groups",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	// M2M join table.
	memberships := &Table{
		Name: "memberships",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "user_id", Type: field.TypeInt64},
			{Name: "group_id", Type: field.TypeInt64},
			{Name: "role", Type: field.TypeEnum},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     "memberships_users_user",
				Columns:    []*Column{{Name: "user_id", Type: field.TypeInt64}},
				RefTable:   users,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "CASCADE",
			},
			{
				Symbol:     "memberships_groups_group",
				Columns:    []*Column{{Name: "group_id", Type: field.TypeInt64}},
				RefTable:   groups,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "CASCADE",
			},
		},
		Indexes: []*Index{
			{
				Name:    "memberships_user_id_group_id",
				Unique:  true,
				Columns: []*Column{{Name: "user_id"}, {Name: "group_id"}},
				columns: []string{"user_id", "group_id"},
			},
		},
	}

	require.NoError(t, m.Create(ctx, users, groups, memberships))
	assert.True(t, tableExists(t, db, "users"))
	assert.True(t, tableExists(t, db, "groups"))
	assert.True(t, tableExists(t, db, "memberships"))

	// Insert data and verify relationships.
	_, err := db.Exec("INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Bob')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO groups (id, name) VALUES (1, 'Engineering'), (2, 'Marketing')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO memberships (user_id, group_id, role) VALUES (1, 1, 'admin'), (1, 2, 'member'), (2, 1, 'member')")
	require.NoError(t, err)

	// Query M2M: users in Engineering group.
	rows, err := db.Query(`
		SELECT u.name FROM users u
		JOIN memberships m ON m.user_id = u.id
		WHERE m.group_id = 1
		ORDER BY u.name
	`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"Alice", "Bob"}, names)

	// Verify unique index prevents duplicate membership.
	_, err = db.Exec("INSERT INTO memberships (user_id, group_id, role) VALUES (1, 1, 'owner')")
	assert.Error(t, err, "unique index should prevent duplicate user-group membership")

	// Verify cascade: delete user cascades to memberships.
	_, err = db.Exec("DELETE FROM users WHERE id = 1")
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM memberships WHERE user_id = 1").Scan(&count))
	assert.Equal(t, 0, count, "memberships for deleted user should be cascade-deleted")
}

func TestSQLiteIntegration_CRUDOperations(t *testing.T) {
	db, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "products",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
			{Name: "price", Type: field.TypeFloat64},
			{Name: "in_stock", Type: field.TypeBool},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	require.NoError(t, m.Create(ctx, table))

	// CREATE
	result, err := db.Exec("INSERT INTO products (name, price, in_stock) VALUES ('Widget', 9.99, 1)")
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	// READ
	var name string
	var price float64
	var inStock bool
	require.NoError(t, db.QueryRow("SELECT name, price, in_stock FROM products WHERE id = ?", id).Scan(&name, &price, &inStock))
	assert.Equal(t, "Widget", name)
	assert.InDelta(t, 9.99, price, 0.001)
	assert.True(t, inStock)

	// UPDATE
	_, err = db.Exec("UPDATE products SET price = 12.99, in_stock = 0 WHERE id = ?", id)
	require.NoError(t, err)

	require.NoError(t, db.QueryRow("SELECT price, in_stock FROM products WHERE id = ?", id).Scan(&price, &inStock))
	assert.InDelta(t, 12.99, price, 0.001)
	assert.False(t, inStock)

	// DELETE
	_, err = db.Exec("DELETE FROM products WHERE id = ?", id)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM products").Scan(&count))
	assert.Equal(t, 0, count)
}

func TestSQLiteIntegration_IdempotentCreate(t *testing.T) {
	_, drv := openSQLite(t)
	m := newSQLiteMigrate(t, drv)
	ctx := context.Background()

	table := &Table{
		Name: "idempotent_test",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	// First create.
	require.NoError(t, m.Create(ctx, table))

	// Second create should be idempotent (no changes needed).
	m2 := newSQLiteMigrate(t, drv)
	require.NoError(t, m2.Create(ctx, table))
}
