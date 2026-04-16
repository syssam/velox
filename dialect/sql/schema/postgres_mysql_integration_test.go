//go:build integration

package schema

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	entsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// uniqueTableName returns a table name that includes a timestamp suffix to avoid
// conflicts between concurrent test runs.
func uniqueTableName(base string) string {
	return fmt.Sprintf("%s_%d", base, time.Now().UnixNano())
}

// openPostgres opens a real PostgreSQL database using the VELOX_TEST_POSTGRES env var.
// Returns the *sql.DB and dialect.Driver, or skips the test if env var is not set.
func openPostgres(t *testing.T) (*sql.DB, dialect.Driver) {
	t.Helper()
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("VELOX_TEST_POSTGRES not set — skipping real Postgres test")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping(), "failed to ping Postgres")
	t.Cleanup(func() { db.Close() })
	drv := entsql.OpenDB(dialect.Postgres, db)
	return db, drv
}

// openMySQL opens a real MySQL database using the VELOX_TEST_MYSQL env var.
// Returns the *sql.DB and dialect.Driver, or skips the test if env var is not set.
func openMySQL(t *testing.T) (*sql.DB, dialect.Driver) {
	t.Helper()
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		t.Skip("VELOX_TEST_MYSQL not set — skipping real MySQL test")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping(), "failed to ping MySQL")
	t.Cleanup(func() { db.Close() })
	drv := entsql.OpenDB(dialect.MySQL, db)
	return db, drv
}

// postgresTableExists checks if a table exists in the PostgreSQL information_schema.
func postgresTableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1",
		name,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

// mysqlTableExists checks if a table exists in the MySQL information_schema.
func mysqlTableExists(t *testing.T, db *sql.DB, dbName, name string) bool {
	t.Helper()
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		dbName, name,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

// mysqlDBName extracts the database name from a MySQL DSN.
// Falls back to "velox_test" if parsing fails.
func mysqlDBName(dsn string) string {
	// DSN format: user:pass@tcp(host:port)/dbname?params
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			rest := dsn[i+1:]
			// strip query params
			for j, c := range rest {
				if c == '?' {
					return rest[:j]
				}
			}
			return rest
		}
	}
	return "velox_test"
}

// TestPostgres_CreateTable verifies that the Atlas migrate engine can create a table
// with multiple column types on a real PostgreSQL instance and that the table is
// visible via information_schema.
func TestPostgres_CreateTable(t *testing.T) {
	db, drv := openPostgres(t)
	tableName := uniqueTableName("velox_pg_create")

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	tbl := &Table{
		Name: tableName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
			{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
			{Name: "age", Type: field.TypeInt},
			{Name: "active", Type: field.TypeBool},
			{Name: "score", Type: field.TypeFloat64},
			{Name: "created_at", Type: field.TypeTime},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q`, tableName))
	})

	require.NoError(t, m.Create(ctx, tbl))
	assert.True(t, postgresTableExists(t, db, tableName), "table should exist in information_schema after creation")
}

// TestPostgres_ColumnTypes verifies that all standard Velox field types can be
// created as columns on PostgreSQL and are visible via information_schema.columns.
func TestPostgres_ColumnTypes(t *testing.T) {
	db, drv := openPostgres(t)
	tableName := uniqueTableName("velox_pg_coltypes")

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	tbl := &Table{
		Name: tableName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "bool_col", Type: field.TypeBool, Nullable: true},
			{Name: "int8_col", Type: field.TypeInt8, Nullable: true},
			{Name: "int16_col", Type: field.TypeInt16, Nullable: true},
			{Name: "int32_col", Type: field.TypeInt32, Nullable: true},
			{Name: "int64_col", Type: field.TypeInt64, Nullable: true},
			{Name: "float32_col", Type: field.TypeFloat32, Nullable: true},
			{Name: "float64_col", Type: field.TypeFloat64, Nullable: true},
			{Name: "string_col", Type: field.TypeString, Size: 255, Nullable: true},
			{Name: "bytes_col", Type: field.TypeBytes, Nullable: true},
			{Name: "time_col", Type: field.TypeTime, Nullable: true},
			{Name: "uuid_col", Type: field.TypeUUID, Nullable: true},
			{Name: "json_col", Type: field.TypeJSON, Nullable: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q`, tableName))
	})

	require.NoError(t, m.Create(ctx, tbl))

	// Verify all columns exist via information_schema.
	rows, err := db.QueryContext(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`,
		tableName,
	)
	require.NoError(t, err)
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		cols = append(cols, col)
	}
	require.NoError(t, rows.Err())

	// All columns should be present.
	assert.Contains(t, cols, "id")
	assert.Contains(t, cols, "bool_col")
	assert.Contains(t, cols, "string_col")
	assert.Contains(t, cols, "uuid_col")
	assert.Contains(t, cols, "json_col")
	assert.Contains(t, cols, "time_col")
	assert.Len(t, cols, 13, "should have all 13 columns")
}

// TestPostgres_ForeignKey verifies that foreign key constraints are enforced on
// a real PostgreSQL instance.
func TestPostgres_ForeignKey(t *testing.T) {
	db, drv := openPostgres(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	usersName := "velox_pg_fk_users_" + suffix
	postsName := "velox_pg_fk_posts_" + suffix

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	users := &Table{
		Name: usersName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	posts := &Table{
		Name: postsName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "title", Type: field.TypeString, Size: 255},
			{Name: "user_id", Type: field.TypeInt64},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     postsName + "_users_id",
				Columns:    []*Column{{Name: "user_id", Type: field.TypeInt64}},
				RefTable:   users,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "CASCADE",
			},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q`, postsName))
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q`, usersName))
	})

	require.NoError(t, m.Create(ctx, users, posts))

	assert.True(t, postgresTableExists(t, db, usersName))
	assert.True(t, postgresTableExists(t, db, postsName))

	// Insert a user and a post.
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %q (id, name) VALUES (1, 'Alice')`, usersName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %q (id, title, user_id) VALUES (1, 'Hello', 1)`, postsName))
	require.NoError(t, err)

	// Delete user — posts should cascade.
	_, err = db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %q WHERE id = 1`, usersName))
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %q`, postsName),
	).Scan(&count))
	assert.Equal(t, 0, count, "cascade delete should remove posts when user is deleted")
}

// TestMySQL_CreateTable verifies that the Atlas migrate engine can create a table
// with multiple column types on a real MySQL instance and that the table is
// visible via information_schema.
func TestMySQL_CreateTable(t *testing.T) {
	db, drv := openMySQL(t)
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	dbName := mysqlDBName(dsn)
	tableName := uniqueTableName("velox_my_create")

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	tbl := &Table{
		Name: tableName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
			{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
			{Name: "age", Type: field.TypeInt},
			{Name: "active", Type: field.TypeBool},
			{Name: "score", Type: field.TypeFloat64},
			{Name: "created_at", Type: field.TypeTime, Size: 6},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
	})

	require.NoError(t, m.Create(ctx, tbl))
	assert.True(t, mysqlTableExists(t, db, dbName, tableName), "table should exist in information_schema after creation")
}

// TestMySQL_ColumnTypes verifies that all standard Velox field types can be
// created as columns on MySQL and are visible via information_schema.columns.
func TestMySQL_ColumnTypes(t *testing.T) {
	db, drv := openMySQL(t)
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	dbName := mysqlDBName(dsn)
	tableName := uniqueTableName("velox_my_coltypes")

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	tbl := &Table{
		Name: tableName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "bool_col", Type: field.TypeBool, Nullable: true},
			{Name: "int8_col", Type: field.TypeInt8, Nullable: true},
			{Name: "int16_col", Type: field.TypeInt16, Nullable: true},
			{Name: "int32_col", Type: field.TypeInt32, Nullable: true},
			{Name: "int64_col", Type: field.TypeInt64, Nullable: true},
			{Name: "float32_col", Type: field.TypeFloat32, Nullable: true},
			{Name: "float64_col", Type: field.TypeFloat64, Nullable: true},
			{Name: "string_col", Type: field.TypeString, Size: 255, Nullable: true},
			{Name: "bytes_col", Type: field.TypeBytes, Nullable: true},
			{Name: "time_col", Type: field.TypeTime, Size: 6, Nullable: true},
			{Name: "uuid_col", Type: field.TypeUUID, Nullable: true},
			{Name: "json_col", Type: field.TypeJSON, Nullable: true},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
	})

	require.NoError(t, m.Create(ctx, tbl))

	// Verify all columns exist via information_schema.
	rows, err := db.QueryContext(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = ? AND table_name = ?
		 ORDER BY ordinal_position`,
		dbName, tableName,
	)
	require.NoError(t, err)
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		cols = append(cols, col)
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, cols, "id")
	assert.Contains(t, cols, "bool_col")
	assert.Contains(t, cols, "string_col")
	assert.Contains(t, cols, "uuid_col")
	assert.Contains(t, cols, "json_col")
	assert.Contains(t, cols, "time_col")
	assert.Len(t, cols, 13, "should have all 13 columns")
}

// TestMySQL_ForeignKey verifies that foreign key constraints are enforced on
// a real MySQL instance.
func TestMySQL_ForeignKey(t *testing.T) {
	db, drv := openMySQL(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	usersName := "velox_my_fk_users_" + suffix
	postsName := "velox_my_fk_posts_" + suffix

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	ctx := context.Background()

	users := &Table{
		Name: usersName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString, Size: 255},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
	}

	posts := &Table{
		Name: postsName,
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "title", Type: field.TypeString, Size: 255},
			{Name: "user_id", Type: field.TypeInt64},
		},
		PrimaryKey: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
		},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     postsName + "_users_id",
				Columns:    []*Column{{Name: "user_id", Type: field.TypeInt64}},
				RefTable:   users,
				RefColumns: []*Column{{Name: "id", Type: field.TypeInt64}},
				OnDelete:   "CASCADE",
			},
		},
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", postsName))
		_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", usersName))
	})

	require.NoError(t, m.Create(ctx, users, posts))

	dsn := os.Getenv("VELOX_TEST_MYSQL")
	dbName := mysqlDBName(dsn)
	assert.True(t, mysqlTableExists(t, db, dbName, usersName))
	assert.True(t, mysqlTableExists(t, db, dbName, postsName))

	// Insert a user and a post.
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO `%s` (id, name) VALUES (1, 'Alice')", usersName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO `%s` (id, title, user_id) VALUES (1, 'Hello', 1)", postsName))
	require.NoError(t, err)

	// Delete user — posts should cascade.
	_, err = db.ExecContext(ctx, fmt.Sprintf("DELETE FROM `%s` WHERE id = 1", usersName))
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM `%s`", postsName),
	).Scan(&count))
	assert.Equal(t, 0, count, "cascade delete should remove posts when user is deleted")
}
