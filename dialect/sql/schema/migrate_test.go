package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"
	"ariga.io/atlas/sql/sqltool"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrate_SchemaName(t *testing.T) {
	db, mk, err := sqlmock.New()
	require.NoError(t, err)
	mk.ExpectQuery(escape("SHOW server_version_num")).
		WillReturnRows(sqlmock.NewRows([]string{"server_version_num"}).AddRow("130000"))
	mk.ExpectQuery(escape("SELECT current_setting('server_version_num'), current_setting('default_table_access_method', true), current_setting('crdb_version', true)")).
		WillReturnRows(sqlmock.NewRows([]string{"current_setting", "current_setting", "current_setting"}).AddRow("130000", "heap", ""))
	mk.ExpectQuery("SELECT nspname AS schema_name,.+").
		WithArgs("public"). // Schema "public" param is used.
		WillReturnRows(sqlmock.NewRows([]string{"schema_name", "comment"}).AddRow("public", "default schema"))
	mk.ExpectQuery("SELECT t3.oid, t1.table_schema,.+").
		WillReturnRows(sqlmock.NewRows([]string{}))
	m, err := NewMigrate(sql.OpenDB("postgres", db), WithSchemaName("public"), WithDiffHook(func(_ Differ) Differ {
		return DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return nil, nil // Noop.
		})
	}))
	require.NoError(t, err)
	require.NoError(t, m.Create(context.Background()))
	require.NoError(t, mk.ExpectationsWereMet())

	// Without schema name the CURRENT_SCHEMA is used.
	mk.ExpectQuery(escape("SHOW server_version_num")).
		WillReturnRows(sqlmock.NewRows([]string{"server_version_num"}).AddRow("130000"))
	mk.ExpectQuery(escape("SELECT current_setting('server_version_num'), current_setting('default_table_access_method', true), current_setting('crdb_version', true)")).
		WillReturnRows(sqlmock.NewRows([]string{"current_setting", "current_setting", "current_setting"}).AddRow("130000", "heap", ""))
	mk.ExpectQuery("SELECT nspname AS schema_name,.+CURRENT_SCHEMA().+").
		WillReturnRows(sqlmock.NewRows([]string{"schema_name", "comment"}).AddRow("public", "default schema"))
	mk.ExpectQuery("SELECT t3.oid, t1.table_schema,.+").
		WillReturnRows(sqlmock.NewRows([]string{}))
	m, err = NewMigrate(sql.OpenDB("postgres", db), WithDiffHook(func(_ Differ) Differ {
		return DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return nil, nil // Noop.
		})
	}))
	require.NoError(t, err)
	require.NoError(t, m.Create(context.Background()))
}

func escape(query string) string {
	rows := strings.Split(query, "\n")
	for i := range rows {
		rows[i] = strings.TrimPrefix(rows[i], " ")
	}
	query = strings.Join(rows, " ")
	return strings.TrimSpace(regexp.QuoteMeta(query)) + "$"
}

func TestMigrate_Formatter(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)

	// If no formatter is given it will be set according to the given migration directory implementation.
	var m *Atlas
	for _, tt := range []struct {
		dir migrate.Dir
		fmt migrate.Formatter
	}{
		{&migrate.LocalDir{}, sqltool.GolangMigrateFormatter},
		{&sqltool.GolangMigrateDir{}, sqltool.GolangMigrateFormatter},
		{&sqltool.GooseDir{}, sqltool.GooseFormatter},
		{&sqltool.DBMateDir{}, sqltool.DBMateFormatter},
		{&sqltool.FlywayDir{}, sqltool.FlywayFormatter},
		{&sqltool.LiquibaseDir{}, sqltool.LiquibaseFormatter},
		{struct{ migrate.Dir }{}, sqltool.GolangMigrateFormatter}, // default one if migration dir is unknown
	} {
		m, err = NewMigrate(sql.OpenDB("", db), WithDir(tt.dir))
		require.NoError(t, err)
		require.Equal(t, tt.fmt, m.fmt)
	}

	// If a formatter is given, it is not overridden.
	m, err = NewMigrate(sql.OpenDB("", db), WithDir(&migrate.LocalDir{}), WithFormatter(migrate.DefaultFormatter))
	require.NoError(t, err)
	require.Equal(t, migrate.DefaultFormatter, m.fmt)
}

func TestMigrate_DiffJoinTableAllocationBC(t *testing.T) {
	// Due to a bug in previous versions, if the universal ID option was enabled and the schema did contain an M2M
	// relation, the join table would have had an entry for the join table in the types table. This test ensures,
	// that the PK range allocated for the join table stays in place, since it's removal would break existing projects
	// due to shifted ranges.

	db, err := sql.Open(dialect.SQLite, "file:test?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)

	// Mock an existing database with an allocation for a join table.
	for _, stmt := range []string{
		"CREATE TABLE `groups` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL);",
		"CREATE INDEX `short` ON `groups` (`id`);",
		"CREATE INDEX `long____________________________1cb2e7e47a309191385af4ad320875b1` ON `groups` (`id`);",
		"CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL);",
		"INSERT INTO sqlite_sequence (name, seq) VALUES (\"users\", 4294967296);",
		"CREATE TABLE `user_groups` (`user_id` integer NOT NULL, `group_id` integer NOT NULL, PRIMARY KEY (`user_id`, `group_id`), CONSTRAINT `user_groups_user_id` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE, CONSTRAINT `user_groups_group_id` FOREIGN KEY (`group_id`) REFERENCES `groups` (`id`) ON DELETE CASCADE);",
		"INSERT INTO sqlite_sequence (name, seq) VALUES (\"user_groups\", 8589934592);",
		"CREATE TABLE `ent_types` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `type` text NOT NULL);",
		"CREATE UNIQUE INDEX `ent_types_type_key` ON `ent_types` (`type`);",
		"INSERT INTO `ent_types` (`type`) VALUES ('groups'), ('users'), ('user_groups');",
		"INSERT INTO `groups` (`name`) VALUES ('seniors'), ('juniors')",
		"INSERT INTO `users` (`name`) VALUES ('masseelch'), ('a8m'), ('rotemtam')",
		"INSERT INTO `user_groups` (`user_id`, `group_id`) VALUES (4294967297, 1), (4294967298, 1), (4294967299, 2)",
	} {
		_, err = db.ExecContext(context.Background(), stmt)
		require.NoError(t, err)
	}

	// Expect to have no changes when migration runs with fix.
	m, err := NewMigrate(db, WithGlobalUniqueID(true), WithDiffHook(func(next Differ) Differ {
		return DiffFunc(func(current, desired *schema.Schema) ([]schema.Change, error) {
			changes, err := next.Diff(current, desired)
			if err != nil {
				return nil, err
			}
			require.Len(t, changes, 0)
			return changes, nil
		})
	}))
	require.NoError(t, err)
	require.NoError(t, m.Create(context.Background(), tables...))

	// Expect to have no changes to the allocation when the join table is dropped.
	m, err = NewMigrate(db, WithGlobalUniqueID(true))
	require.NoError(t, err)
	require.NoError(t, m.Create(context.Background(), groupsTable, usersTable))

	rows, err := db.QueryContext(context.Background(), "SELECT `type` from `ent_types` ORDER BY `id` ASC")
	require.NoError(t, err)
	var types []string
	for rows.Next() {
		var typ string
		require.NoError(t, rows.Scan(&typ))
		types = append(types, typ)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"groups", "users", "user_groups"}, types)
}

var (
	groupsColumns = []*Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
		{Name: "name", Type: field.TypeString},
	}
	groupsTable = &Table{
		Name:       "groups",
		Columns:    groupsColumns,
		PrimaryKey: []*Column{groupsColumns[0]},
		Indexes: []*Index{
			{
				Name:    "short",
				Columns: []*Column{groupsColumns[0]}},
			{
				Name:    "long_" + strings.Repeat("_", 60),
				Columns: []*Column{groupsColumns[0]},
			},
		},
	}
	usersColumns = []*Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
		{Name: "name", Type: field.TypeString},
	}
	usersTable = &Table{
		Name:       "users",
		Columns:    usersColumns,
		PrimaryKey: []*Column{usersColumns[0]},
	}
	userGroupsColumns = []*Column{
		{Name: "user_id", Type: field.TypeInt},
		{Name: "group_id", Type: field.TypeInt},
	}
	userGroupsTable = &Table{
		Name:       "user_groups",
		Columns:    userGroupsColumns,
		PrimaryKey: []*Column{userGroupsColumns[0], userGroupsColumns[1]},
		ForeignKeys: []*ForeignKey{
			{
				Symbol:     "user_groups_user_id",
				Columns:    []*Column{userGroupsColumns[0]},
				RefColumns: []*Column{usersColumns[0]},
				OnDelete:   Cascade,
			},
			{
				Symbol:     "user_groups_group_id",
				Columns:    []*Column{userGroupsColumns[1]},
				RefColumns: []*Column{groupsColumns[0]},
				OnDelete:   Cascade,
			},
		},
	}
	tables = []*Table{
		groupsTable,
		usersTable,
		userGroupsTable,
	}
	petColumns = []*Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
	}
	petsTable = &Table{
		Name:       "pets",
		Columns:    petColumns,
		PrimaryKey: petColumns,
	}
)

func init() {
	userGroupsTable.ForeignKeys[0].RefTable = usersTable
	userGroupsTable.ForeignKeys[1].RefTable = groupsTable
}

func TestMigrate_Diff(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open(dialect.SQLite, "file:test?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)

	p := t.TempDir()
	d, err := migrate.NewLocalDir(p)
	require.NoError(t, err)

	m, err := NewMigrate(db, WithDir(d))
	require.NoError(t, err)
	require.NoError(t, m.Diff(ctx, &Table{Name: "users"}))
	v := time.Now().UTC().Format("20060102150405")
	requireFileEqual(t, filepath.Join(p, v+"_changes.up.sql"), "-- create \"users\" table\nCREATE TABLE `users` ();\n")
	requireFileEqual(t, filepath.Join(p, v+"_changes.down.sql"), "-- reverse: create \"users\" table\nDROP TABLE `users`;\n")
	require.FileExists(t, filepath.Join(p, migrate.HashFileName))

	// Test integrity file.
	p = t.TempDir()
	d, err = migrate.NewLocalDir(p)
	require.NoError(t, err)
	m, err = NewMigrate(db, WithDir(d))
	require.NoError(t, err)
	require.NoError(t, m.Diff(ctx, &Table{Name: "users"}))
	requireFileEqual(t, filepath.Join(p, v+"_changes.up.sql"), "-- create \"users\" table\nCREATE TABLE `users` ();\n")
	requireFileEqual(t, filepath.Join(p, v+"_changes.down.sql"), "-- reverse: create \"users\" table\nDROP TABLE `users`;\n")
	require.FileExists(t, filepath.Join(p, migrate.HashFileName))
	require.NoError(t, d.WriteFile("tmp.sql", nil))
	require.ErrorIs(t, m.Diff(ctx, &Table{Name: "users"}), migrate.ErrChecksumMismatch)

	p = t.TempDir()
	d, err = migrate.NewLocalDir(p)
	require.NoError(t, err)
	f, err := migrate.NewTemplateFormatter(
		template.Must(template.New("").Parse("{{ .Name }}.sql")),
		template.Must(template.New("").Parse(
			`{{ range .Changes }}{{ printf "%s;\n" .Cmd }}{{ end }}`,
		)),
	)
	require.NoError(t, err)

	// Join tables (mapping between user and group) will not result in an entry to the types table.
	m, err = NewMigrate(db, WithFormatter(f), WithDir(d), WithGlobalUniqueID(true))
	require.NoError(t, err)
	require.NoError(t, m.Diff(ctx, tables...))
	changesSQL := strings.Join([]string{
		"CREATE TABLE `groups` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL);",
		"CREATE INDEX `short` ON `groups` (`id`);",
		"CREATE INDEX `long____________________________1cb2e7e47a309191385af4ad320875b1` ON `groups` (`id`);",
		"CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL);",
		fmt.Sprintf("INSERT INTO sqlite_sequence (name, seq) VALUES (\"users\", %d);", 1<<32),
		"CREATE TABLE `user_groups` (`user_id` integer NOT NULL, `group_id` integer NOT NULL, PRIMARY KEY (`user_id`, `group_id`), CONSTRAINT `user_groups_user_id` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE, CONSTRAINT `user_groups_group_id` FOREIGN KEY (`group_id`) REFERENCES `groups` (`id`) ON DELETE CASCADE);",
		"CREATE TABLE `ent_types` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `type` text NOT NULL);",
		"CREATE UNIQUE INDEX `ent_types_type_key` ON `ent_types` (`type`);",
		"INSERT INTO `ent_types` (`type`) VALUES ('groups'), ('users');",
		"",
	}, "\n")
	requireFileEqual(t, filepath.Join(p, "changes.sql"), changesSQL)

	// Skipping table creation should write only the ent_type insertion.
	m, err = NewMigrate(db, WithFormatter(f), WithDir(d), WithGlobalUniqueID(true), WithDiffOptions(schema.DiffSkipChanges(&schema.AddTable{})))
	require.NoError(t, err)
	require.NoError(t, m.Diff(ctx, tables...))
	requireFileEqual(t, filepath.Join(p, "changes.sql"), "INSERT INTO `ent_types` (`type`) VALUES ('groups'), ('users');\n")

	// Enable indentations.
	m, err = NewMigrate(db, WithFormatter(f), WithDir(d), WithGlobalUniqueID(true), WithIndent("  "))
	require.NoError(t, err)
	// Adding another node will result in a new entry to the TypeTable (without actually creating it).
	_, err = db.ExecContext(ctx, changesSQL, nil, nil)
	require.NoError(t, err)
	require.NoError(t, m.NamedDiff(ctx, "changes_2", petsTable))
	requireFileEqual(t,
		filepath.Join(p, "changes_2.sql"), strings.Join([]string{
			"CREATE TABLE `pets` (\n  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT\n);",
			fmt.Sprintf("INSERT INTO sqlite_sequence (name, seq) VALUES (\"pets\", %d);", 2<<32),
			"INSERT INTO `ent_types` (`type`) VALUES ('pets');", "",
		}, "\n"))

	// Checksum will be updated as well.
	require.NoError(t, migrate.Validate(d))

	require.NoError(t, m.NamedDiff(ctx, "no_changes"), "should not error if WithErrNoPlan is not set")
	// Enable WithErrNoPlan.
	m, err = NewMigrate(db, WithFormatter(f), WithDir(d), WithGlobalUniqueID(true), WithErrNoPlan(true))
	require.NoError(t, err)
	err = m.NamedDiff(ctx, "no_changes")
	require.ErrorIs(t, err, migrate.ErrNoPlan)
}

func requireFileEqual(t *testing.T, name, contents string) {
	t.Helper()
	c, err := os.ReadFile(name)
	require.NoError(t, err)
	require.Equal(t, contents, string(c))
}

func TestMigrateWithoutForeignKeys(t *testing.T) {
	tbl := &schema.Table{
		Name: "tbl",
		Columns: []*schema.Column{
			{Name: "id", Type: &schema.ColumnType{Type: &schema.IntegerType{T: "bigint"}}},
		},
	}
	fk := &schema.ForeignKey{
		Symbol:     "fk",
		Table:      tbl,
		Columns:    tbl.Columns[1:],
		RefTable:   tbl,
		RefColumns: tbl.Columns[:1],
		OnUpdate:   schema.NoAction,
		OnDelete:   schema.Cascade,
	}
	tbl.ForeignKeys = append(tbl.ForeignKeys, fk)
	t.Run("AddTable", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.AddTable{
					T: tbl,
				},
			}, nil
		})
		df, err := withoutForeignKeys(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		require.Len(t, df, 1)
		actual, ok := df[0].(*schema.AddTable)
		require.True(t, ok)
		require.Nil(t, actual.T.ForeignKeys)
	})
	t.Run("ModifyTable", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.ModifyTable{
					T: tbl,
					Changes: []schema.Change{
						&schema.AddIndex{
							I: &schema.Index{
								Name: "id_key",
								Parts: []*schema.IndexPart{
									{C: tbl.Columns[0]},
								},
							},
						},
						&schema.DropForeignKey{
							F: fk,
						},
						&schema.AddForeignKey{
							F: fk,
						},
						&schema.ModifyForeignKey{
							From:   fk,
							To:     fk,
							Change: schema.ChangeRefColumn,
						},
						&schema.AddColumn{
							C: &schema.Column{Name: "name", Type: &schema.ColumnType{Type: &schema.StringType{T: "varchar(255)"}}},
						},
					},
				},
			}, nil
		})
		df, err := withoutForeignKeys(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		require.Len(t, df, 1)
		actual, ok := df[0].(*schema.ModifyTable)
		require.True(t, ok)
		require.Len(t, actual.Changes, 2)
		addIndex, ok := actual.Changes[0].(*schema.AddIndex)
		require.True(t, ok)
		require.EqualValues(t, "id_key", addIndex.I.Name)
		addColumn, ok := actual.Changes[1].(*schema.AddColumn)
		require.True(t, ok)
		require.EqualValues(t, "name", addColumn.C.Name)
	})
}

func TestAtlas_StateReader(t *testing.T) {
	db, err := sql.Open(dialect.SQLite, "file:test?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	m, err := NewMigrate(db)
	require.NoError(t, err)
	realm, err := m.StateReader(&Table{
		Name: "users",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt64, Increment: true},
			{Name: "name", Type: field.TypeString},
			{Name: "active", Type: field.TypeBool},
		},
		Annotation: &sqlschema.Annotation{
			IncrementStart: func(i int) *int { return &i }(100),
		},
	}).ReadState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, realm)
	require.Len(t, realm.Schemas, 1)
	require.Len(t, realm.Schemas[0].Tables, 1)
	require.Equal(t, "users", realm.Schemas[0].Tables[0].Name)
	require.Equal(t, []schema.Attr{&sqlite.AutoIncrement{Seq: 100}}, realm.Schemas[0].Tables[0].Attrs)
	require.Equal(t,
		realm.Schemas[0].Tables[0].Columns,
		[]*schema.Column{
			schema.NewIntColumn("id", "integer").
				AddAttrs(&sqlite.AutoIncrement{}),
			schema.NewStringColumn("name", "text"),
			schema.NewBoolColumn("active", "bool"),
		},
	)
}

func TestAtlas_ParallelCreate(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(10)
	for i := range 10 {
		db, err := sql.Open(dialect.SQLite, fmt.Sprintf("file:test-%d?mode=memory&_pragma=foreign_keys(1)", i))
		require.NoError(t, err)
		m, err := NewMigrate(db)
		require.NoError(t, err)
		go func() {
			defer wg.Done()
			require.NoError(t, m.Create(context.Background(), petsTable))
			require.NoError(t, db.Close())
		}()
	}
	wg.Wait()
}

// TestMigrateOptions tests the various MigrateOption functions.
func TestMigrateOptions(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)

	t.Run("WithDropColumn", func(t *testing.T) {
		m, err := NewMigrate(sql.OpenDB("", db), WithDropColumn(true))
		require.NoError(t, err)
		require.True(t, m.dropColumns)

		m, err = NewMigrate(sql.OpenDB("", db), WithDropColumn(false))
		require.NoError(t, err)
		require.False(t, m.dropColumns)
	})

	t.Run("WithDropIndex", func(t *testing.T) {
		m, err := NewMigrate(sql.OpenDB("", db), WithDropIndex(true))
		require.NoError(t, err)
		require.True(t, m.dropIndexes)

		m, err = NewMigrate(sql.OpenDB("", db), WithDropIndex(false))
		require.NoError(t, err)
		require.False(t, m.dropIndexes)
	})

	t.Run("WithForeignKeys", func(t *testing.T) {
		// Default is true
		m, err := NewMigrate(sql.OpenDB("", db))
		require.NoError(t, err)
		require.True(t, m.withForeignKeys)

		m, err = NewMigrate(sql.OpenDB("", db), WithForeignKeys(false))
		require.NoError(t, err)
		require.False(t, m.withForeignKeys)

		m, err = NewMigrate(sql.OpenDB("", db), WithForeignKeys(true))
		require.NoError(t, err)
		require.True(t, m.withForeignKeys)
	})

	t.Run("WithHooks", func(t *testing.T) {
		hook1 := func(next Creator) Creator { return next }
		hook2 := func(next Creator) Creator { return next }

		m, err := NewMigrate(sql.OpenDB("", db), WithHooks(hook1, hook2))
		require.NoError(t, err)
		require.Len(t, m.hooks, 2)

		// Adding more hooks
		m, err = NewMigrate(sql.OpenDB("", db), WithHooks(hook1), WithHooks(hook2))
		require.NoError(t, err)
		require.Len(t, m.hooks, 2)
	})

	t.Run("WithSkipChanges", func(t *testing.T) {
		m, err := NewMigrate(sql.OpenDB("", db), WithSkipChanges(DropColumn|DropIndex))
		require.NoError(t, err)
		require.Equal(t, DropColumn|DropIndex, m.skip)

		m, err = NewMigrate(sql.OpenDB("", db), WithSkipChanges(NoChange))
		require.NoError(t, err)
		require.Equal(t, NoChange, m.skip)
	})

	t.Run("WithApplyHook", func(t *testing.T) {
		hook := func(next Applier) Applier { return next }

		m, err := NewMigrate(sql.OpenDB("", db), WithApplyHook(hook))
		require.NoError(t, err)
		require.Len(t, m.applyHook, 1)

		m, err = NewMigrate(sql.OpenDB("", db), WithApplyHook(hook, hook))
		require.NoError(t, err)
		require.Len(t, m.applyHook, 2)
	})

	t.Run("WithDialect", func(t *testing.T) {
		// WithDialect is primarily for NewMigrateURL where the dialect can differ from URL scheme.
		// With NewMigrate, the driver's dialect takes precedence, but we can still verify
		// the option sets the value correctly before init overwrites it.
		// Here we test that the option function works by checking the field directly.
		a := &Atlas{}
		WithDialect("tidb")(a)
		require.Equal(t, "tidb", a.dialect)

		WithDialect("cockroachdb")(a)
		require.Equal(t, "cockroachdb", a.dialect)
	})

	t.Run("WithMigrationMode", func(t *testing.T) {
		// ModeReplay requires WithDir, so we provide one for testing
		p := t.TempDir()
		d, err := migrate.NewLocalDir(p)
		require.NoError(t, err)

		m, err := NewMigrate(sql.OpenDB("", db), WithMigrationMode(ModeReplay), WithDir(d))
		require.NoError(t, err)
		require.Equal(t, Mode(ModeReplay), m.mode)

		m, err = NewMigrate(sql.OpenDB("", db), WithMigrationMode(ModeInspect))
		require.NoError(t, err)
		require.Equal(t, Mode(ModeInspect), m.mode)
	})
}

// TestChangeKind tests the ChangeKind type and its Is method.
func TestChangeKind(t *testing.T) {
	tests := []struct {
		name     string
		k        ChangeKind
		c        ChangeKind
		expected bool
	}{
		{"NoChange is NoChange", NoChange, NoChange, true},
		{"AddSchema is AddSchema", AddSchema, AddSchema, true},
		{"ModifySchema is ModifySchema", ModifySchema, ModifySchema, true},
		{"DropSchema is DropSchema", DropSchema, DropSchema, true},
		{"AddTable is AddTable", AddTable, AddTable, true},
		{"ModifyTable is ModifyTable", ModifyTable, ModifyTable, true},
		{"DropTable is DropTable", DropTable, DropTable, true},
		{"AddColumn is AddColumn", AddColumn, AddColumn, true},
		{"ModifyColumn is ModifyColumn", ModifyColumn, ModifyColumn, true},
		{"DropColumn is DropColumn", DropColumn, DropColumn, true},
		{"AddIndex is AddIndex", AddIndex, AddIndex, true},
		{"ModifyIndex is ModifyIndex", ModifyIndex, ModifyIndex, true},
		{"DropIndex is DropIndex", DropIndex, DropIndex, true},
		{"AddForeignKey is AddForeignKey", AddForeignKey, AddForeignKey, true},
		{"ModifyForeignKey is ModifyForeignKey", ModifyForeignKey, ModifyForeignKey, true},
		{"DropForeignKey is DropForeignKey", DropForeignKey, DropForeignKey, true},
		{"AddCheck is AddCheck", AddCheck, AddCheck, true},
		{"ModifyCheck is ModifyCheck", ModifyCheck, ModifyCheck, true},
		{"DropCheck is DropCheck", DropCheck, DropCheck, true},
		{"combined flags contain DropColumn", DropColumn | DropIndex, DropColumn, true},
		{"combined flags contain DropIndex", DropColumn | DropIndex, DropIndex, true},
		{"combined flags do not contain AddColumn", DropColumn | DropIndex, AddColumn, false},
		{"DropColumn is not DropIndex", DropColumn, DropIndex, false},
		{"NoChange is not AddTable", NoChange, AddTable, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.k.Is(tt.c)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestNewTypesTable tests the NewTypesTable function.
func TestNewTypesTable(t *testing.T) {
	table := NewTypesTable()

	require.Equal(t, TypeTable, table.Name)
	require.Len(t, table.Columns, 2)

	// Check primary key column
	idCol := table.Columns[0]
	require.Equal(t, "id", idCol.Name)
	require.Equal(t, field.TypeUint, idCol.Type)
	require.True(t, idCol.Increment)

	// Check type column
	typeCol := table.Columns[1]
	require.Equal(t, "type", typeCol.Name)
	require.Equal(t, field.TypeString, typeCol.Type)
	require.True(t, typeCol.Unique)

	// Check primary key
	require.Len(t, table.PrimaryKey, 1)
	require.Equal(t, idCol, table.PrimaryKey[0])
}

// TestCreateFunc tests the CreateFunc adapter.
func TestCreateFunc(t *testing.T) {
	called := false
	var receivedTables []*Table

	f := CreateFunc(func(_ context.Context, tables ...*Table) error {
		called = true
		receivedTables = tables
		return nil
	})

	ctx := context.Background()
	err := f.Create(ctx, groupsTable, usersTable)

	require.NoError(t, err)
	require.True(t, called)
	require.Len(t, receivedTables, 2)
	require.Equal(t, groupsTable, receivedTables[0])
	require.Equal(t, usersTable, receivedTables[1])
}

// TestCreateFuncWithError tests the CreateFunc adapter error handling.
func TestCreateFuncWithError(t *testing.T) {
	expectedErr := fmt.Errorf("create failed")
	f := CreateFunc(func(_ context.Context, _ ...*Table) error {
		return expectedErr
	})

	ctx := context.Background()
	err := f.Create(ctx, groupsTable)

	require.Error(t, err)
	require.Equal(t, expectedErr, err)
}

// TestIndexOf tests the indexOf helper function.
func TestIndexOf(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		target   string
		expected int
	}{
		{"found at start", []string{"a", "b", "c"}, "a", 0},
		{"found in middle", []string{"a", "b", "c"}, "b", 1},
		{"found at end", []string{"a", "b", "c"}, "c", 2},
		{"not found", []string{"a", "b", "c"}, "d", -1},
		{"empty slice", []string{}, "a", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexOf(tt.slice, tt.target)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestTableMethods tests various Table methods.
func TestTableMethods(t *testing.T) {
	t.Run("NewView", func(t *testing.T) {
		v := NewView("my_view")
		require.Equal(t, "my_view", v.Name)
		require.True(t, v.View)
	})

	t.Run("SetComment", func(t *testing.T) {
		tbl := NewTable("test")
		result := tbl.SetComment("This is a test table")
		require.Equal(t, "This is a test table", tbl.Comment)
		require.Equal(t, tbl, result) // Returns self for chaining
	})

	t.Run("SetSchema", func(t *testing.T) {
		tbl := NewTable("test")
		result := tbl.SetSchema("public")
		require.Equal(t, "public", tbl.Schema)
		require.Equal(t, tbl, result) // Returns self for chaining
	})

	t.Run("SetPos", func(t *testing.T) {
		tbl := NewTable("test")
		result := tbl.SetPos("schema/user.go:15")
		require.Equal(t, "schema/user.go:15", tbl.Pos)
		require.Equal(t, tbl, result) // Returns self for chaining
	})

	t.Run("HasColumn", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "id", Type: field.TypeInt}).
			AddColumn(&Column{Name: "name", Type: field.TypeString})

		require.True(t, tbl.HasColumn("id"))
		require.True(t, tbl.HasColumn("name"))
		require.False(t, tbl.HasColumn("email"))
	})

	t.Run("Column", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "id", Type: field.TypeInt}).
			AddColumn(&Column{Name: "name", Type: field.TypeString})

		col, ok := tbl.Column("id")
		require.True(t, ok)
		require.Equal(t, "id", col.Name)

		col, ok = tbl.Column("name")
		require.True(t, ok)
		require.Equal(t, "name", col.Name)

		col, ok = tbl.Column("email")
		require.False(t, ok)
		require.Nil(t, col)
	})

	t.Run("Column_DirectlyAdded", func(t *testing.T) {
		// Test the case where columns are added directly to Columns slice
		tbl := NewTable("test")
		tbl.Columns = append(tbl.Columns, &Column{Name: "direct_col", Type: field.TypeInt})

		col, ok := tbl.Column("direct_col")
		require.True(t, ok)
		require.Equal(t, "direct_col", col.Name)
	})

	t.Run("AddIndex", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "id", Type: field.TypeInt}).
			AddColumn(&Column{Name: "name", Type: field.TypeString}).
			AddIndex("idx_name", false, []string{"name"})

		require.Len(t, tbl.Indexes, 1)
		require.Equal(t, "idx_name", tbl.Indexes[0].Name)
		require.False(t, tbl.Indexes[0].Unique)
	})

	t.Run("AddIndex_Unique", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "email", Type: field.TypeString}).
			AddIndex("idx_email", true, []string{"email"})

		require.Len(t, tbl.Indexes, 1)
		require.Equal(t, "idx_email", tbl.Indexes[0].Name)
		require.True(t, tbl.Indexes[0].Unique)
	})

	t.Run("Index", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "name", Type: field.TypeString}).
			AddIndex("idx_name", false, []string{"name"})

		idx, ok := tbl.Index("idx_name")
		require.True(t, ok)
		require.Equal(t, "idx_name", idx.Name)

		idx, ok = tbl.Index("nonexistent")
		require.False(t, ok)
		require.Nil(t, idx)
	})
}

// TestColumnMethods tests various Column methods.
func TestColumnMethods(t *testing.T) {
	t.Run("UniqueKey", func(t *testing.T) {
		col := &Column{Name: "email", Key: UniqueKey}
		require.True(t, col.UniqueKey())

		col2 := &Column{Name: "name", Key: ""}
		require.False(t, col2.UniqueKey())
	})

	t.Run("IntType", func(t *testing.T) {
		tests := []struct {
			typ      field.Type
			expected bool
		}{
			{field.TypeInt8, true},
			{field.TypeInt16, true},
			{field.TypeInt32, true},
			{field.TypeInt64, true},
			{field.TypeInt, true},
			{field.TypeUint8, false},
			{field.TypeString, false},
			{field.TypeFloat64, false},
		}
		for _, tt := range tests {
			col := Column{Type: tt.typ}
			require.Equal(t, tt.expected, col.IntType(), "IntType() for %v", tt.typ)
		}
	})

	t.Run("UintType", func(t *testing.T) {
		tests := []struct {
			typ      field.Type
			expected bool
		}{
			{field.TypeUint8, true},
			{field.TypeUint16, true},
			{field.TypeUint32, true},
			{field.TypeUint64, true},
			{field.TypeUint, true},
			{field.TypeInt8, false},
			{field.TypeString, false},
		}
		for _, tt := range tests {
			col := Column{Type: tt.typ}
			require.Equal(t, tt.expected, col.UintType(), "UintType() for %v", tt.typ)
		}
	})

	t.Run("FloatType", func(t *testing.T) {
		tests := []struct {
			typ      field.Type
			expected bool
		}{
			{field.TypeFloat32, true},
			{field.TypeFloat64, true},
			{field.TypeInt64, false},
			{field.TypeString, false},
		}
		for _, tt := range tests {
			col := Column{Type: tt.typ}
			require.Equal(t, tt.expected, col.FloatType(), "FloatType() for %v", tt.typ)
		}
	})

	t.Run("ScanDefault", func(t *testing.T) {
		// Test NULL
		col := &Column{Name: "test", Type: field.TypeInt}
		require.NoError(t, col.ScanDefault("NULL"))
		require.Nil(t, col.Default)

		// Test int
		col = &Column{Name: "count", Type: field.TypeInt64}
		require.NoError(t, col.ScanDefault("42"))
		require.Equal(t, int64(42), col.Default)

		// Test uint
		col = &Column{Name: "age", Type: field.TypeUint64}
		require.NoError(t, col.ScanDefault("25"))
		require.Equal(t, uint64(25), col.Default)

		// Test float
		col = &Column{Name: "price", Type: field.TypeFloat64}
		require.NoError(t, col.ScanDefault("19.99"))
		require.Equal(t, 19.99, col.Default)

		// Test bool
		col = &Column{Name: "active", Type: field.TypeBool}
		require.NoError(t, col.ScanDefault("true"))
		require.Equal(t, true, col.Default)

		// Test string
		col = &Column{Name: "name", Type: field.TypeString}
		require.NoError(t, col.ScanDefault("hello"))
		require.Equal(t, "hello", col.Default)

		// Test enum
		col = &Column{Name: "status", Type: field.TypeEnum}
		require.NoError(t, col.ScanDefault("active"))
		require.Equal(t, "active", col.Default)

		// Test JSON
		col = &Column{Name: "data", Type: field.TypeJSON}
		require.NoError(t, col.ScanDefault("{\"key\":\"value\"}"))
		require.Equal(t, "{\"key\":\"value\"}", col.Default)

		// Test bytes
		col = &Column{Name: "blob", Type: field.TypeBytes}
		require.NoError(t, col.ScanDefault("binary"))
		require.Equal(t, []byte("binary"), col.Default)

		// Test UUID without function
		col = &Column{Name: "id", Type: field.TypeUUID}
		require.NoError(t, col.ScanDefault("550e8400-e29b-41d4-a716-446655440000"))
		require.Equal(t, "550e8400-e29b-41d4-a716-446655440000", col.Default)

		// Test UUID with function (should skip)
		col = &Column{Name: "id", Type: field.TypeUUID}
		require.NoError(t, col.ScanDefault("gen_random_uuid()"))
		require.Nil(t, col.Default)
	})

	t.Run("ScanDefault_Errors", func(t *testing.T) {
		// Test invalid int
		col := &Column{Name: "count", Type: field.TypeInt64}
		require.Error(t, col.ScanDefault("not_a_number"))

		// Test invalid uint
		col = &Column{Name: "age", Type: field.TypeUint64}
		require.Error(t, col.ScanDefault("not_a_number"))

		// Test invalid float
		col = &Column{Name: "price", Type: field.TypeFloat64}
		require.Error(t, col.ScanDefault("not_a_number"))

		// Test unsupported type
		col = &Column{Name: "other", Type: field.TypeOther}
		require.Error(t, col.ScanDefault("something"))
	})

	t.Run("ConvertibleTo", func(t *testing.T) {
		// Same type
		c1 := &Column{Type: field.TypeInt64}
		c2 := &Column{Type: field.TypeInt64}
		require.True(t, c1.ConvertibleTo(c2))

		// Same type with size constraint - smaller to larger OK
		c1 = &Column{Type: field.TypeString, Size: 100}
		c2 = &Column{Type: field.TypeString, Size: 200}
		require.True(t, c1.ConvertibleTo(c2))

		// Same type with size constraint - larger to smaller NOT OK
		c1 = &Column{Type: field.TypeString, Size: 200}
		c2 = &Column{Type: field.TypeString, Size: 100}
		require.False(t, c1.ConvertibleTo(c2))

		// Int to larger int
		c1 = &Column{Type: field.TypeInt8}
		c2 = &Column{Type: field.TypeInt64}
		require.True(t, c1.ConvertibleTo(c2))

		// String to enum
		c1 = &Column{Type: field.TypeString}
		c2 = &Column{Type: field.TypeEnum}
		require.True(t, c1.ConvertibleTo(c2))

		// Enum to string
		c1 = &Column{Type: field.TypeEnum}
		c2 = &Column{Type: field.TypeString}
		require.True(t, c1.ConvertibleTo(c2))

		// Int to string
		c1 = &Column{Type: field.TypeInt64}
		c2 = &Column{Type: field.TypeString}
		require.True(t, c1.ConvertibleTo(c2))

		// Float32 to Float64
		c1 = &Column{Type: field.TypeFloat32}
		c2 = &Column{Type: field.TypeFloat64}
		require.True(t, c1.ConvertibleTo(c2))

		// String to int - NOT OK
		c1 = &Column{Type: field.TypeString}
		c2 = &Column{Type: field.TypeInt64}
		require.False(t, c1.ConvertibleTo(c2))
	})

	t.Run("supportDefault", func(t *testing.T) {
		// String with small size supports default
		col := Column{Type: field.TypeString, Size: 100}
		require.True(t, col.supportDefault())

		// String with large size (text) does not support default
		col = Column{Type: field.TypeString, Size: 1 << 16}
		require.False(t, col.supportDefault())

		// Enum supports default
		col = Column{Type: field.TypeEnum, Size: 100}
		require.True(t, col.supportDefault())

		// Bool supports default
		col = Column{Type: field.TypeBool}
		require.True(t, col.supportDefault())

		// Time supports default
		col = Column{Type: field.TypeTime}
		require.True(t, col.supportDefault())

		// UUID supports default
		col = Column{Type: field.TypeUUID}
		require.True(t, col.supportDefault())

		// Numeric types support default
		col = Column{Type: field.TypeInt64}
		require.True(t, col.supportDefault())

		col = Column{Type: field.TypeFloat64}
		require.True(t, col.supportDefault())
	})

	t.Run("scanTypeOr", func(t *testing.T) {
		// Without typ set
		col := &Column{Name: "test"}
		require.Equal(t, "default", col.scanTypeOr("default"))

		// With typ set
		col = &Column{Name: "test", typ: "BIGINT"}
		require.Equal(t, "bigint", col.scanTypeOr("default"))
	})
}

// TestReferenceOptionConstName tests the ConstName method of ReferenceOption.
func TestReferenceOptionConstName(t *testing.T) {
	tests := []struct {
		opt      ReferenceOption
		expected string
	}{
		{NoAction, "NoAction"},
		{Restrict, "Restrict"},
		{Cascade, "Cascade"},
		{SetNull, "SetNull"},
		{SetDefault, "SetDefault"},
	}

	for _, tt := range tests {
		t.Run(string(tt.opt), func(t *testing.T) {
			require.Equal(t, tt.expected, tt.opt.ConstName())
		})
	}
}

// TestNoResultMethods tests the noResult type methods.
func TestNoResultMethods(t *testing.T) {
	res := noResult{}

	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.Equal(t, int64(0), id)

	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(0), affected)
}

// TestNoRowsMethods tests the noRows type methods.
func TestNoRowsMethods(t *testing.T) {
	rows := &noRows{cols: []string{"id", "name"}}

	// First Next returns true
	require.True(t, rows.Next())

	// Second Next returns false
	require.False(t, rows.Next())

	// Columns
	cols, err := rows.Columns()
	require.NoError(t, err)
	require.Equal(t, []string{"id", "name"}, cols)

	// Close and Err
	require.NoError(t, rows.Close())
	require.NoError(t, rows.Err())

	// Scan
	require.NoError(t, rows.Scan())
}

// TestFilterChanges_AddForeignKey tests that filterChanges correctly handles AddForeignKey.
// This is a regression test for a bug where AddForeignKey was incorrectly mapped to AddIndex.
func TestFilterChanges_AddForeignKey(t *testing.T) {
	tbl := &schema.Table{
		Name: "posts",
		Columns: []*schema.Column{
			{Name: "id", Type: &schema.ColumnType{Type: &schema.IntegerType{T: "bigint"}}},
			{Name: "user_id", Type: &schema.ColumnType{Type: &schema.IntegerType{T: "bigint"}}},
		},
	}
	fk := &schema.ForeignKey{
		Symbol:     "posts_user_id_fkey",
		Table:      tbl,
		Columns:    tbl.Columns[1:],
		RefTable:   tbl,
		RefColumns: tbl.Columns[:1],
		OnDelete:   schema.Cascade,
	}

	t.Run("SkipAddForeignKey", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.AddTable{T: tbl},
				&schema.AddForeignKey{F: fk},
			}, nil
		})

		// Apply filterChanges with AddForeignKey skip
		filtered := filterChanges(AddForeignKey)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)

		// Should have filtered out AddForeignKey, leaving only AddTable
		require.Len(t, df, 1)
		_, ok := df[0].(*schema.AddTable)
		require.True(t, ok, "expected AddTable to remain after filtering AddForeignKey")
	})

	t.Run("SkipAddIndex_DoesNotAffectAddForeignKey", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.AddForeignKey{F: fk},
				&schema.AddIndex{I: &schema.Index{Name: "idx_user_id"}},
			}, nil
		})

		// Apply filterChanges with AddIndex skip (not AddForeignKey)
		filtered := filterChanges(AddIndex)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)

		// Should have filtered out AddIndex, but kept AddForeignKey
		require.Len(t, df, 1)
		_, ok := df[0].(*schema.AddForeignKey)
		require.True(t, ok, "expected AddForeignKey to remain when only AddIndex is skipped")
	})

	t.Run("SkipBothAddIndexAndAddForeignKey", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.AddTable{T: tbl},
				&schema.AddForeignKey{F: fk},
				&schema.AddIndex{I: &schema.Index{Name: "idx_user_id"}},
			}, nil
		})

		// Apply filterChanges with both AddIndex and AddForeignKey skip
		filtered := filterChanges(AddIndex | AddForeignKey)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)

		// Should have filtered out both AddIndex and AddForeignKey, leaving only AddTable
		require.Len(t, df, 1)
		_, ok := df[0].(*schema.AddTable)
		require.True(t, ok, "expected only AddTable to remain")
	})

	t.Run("ModifyTable_FilterForeignKeyChanges", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.ModifyTable{
					T: tbl,
					Changes: []schema.Change{
						&schema.AddColumn{C: &schema.Column{Name: "status"}},
						&schema.AddForeignKey{F: fk},
						&schema.DropForeignKey{F: fk},
						&schema.ModifyForeignKey{From: fk, To: fk},
						&schema.AddIndex{I: &schema.Index{Name: "idx_status"}},
					},
				},
			}, nil
		})

		// Skip all FK-related changes
		filtered := filterChanges(AddForeignKey | DropForeignKey | ModifyForeignKey)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)

		require.Len(t, df, 1)
		mt, ok := df[0].(*schema.ModifyTable)
		require.True(t, ok)

		// Should have 2 remaining changes: AddColumn and AddIndex
		require.Len(t, mt.Changes, 2)
		_, ok = mt.Changes[0].(*schema.AddColumn)
		require.True(t, ok, "first change should be AddColumn")
		_, ok = mt.Changes[1].(*schema.AddIndex)
		require.True(t, ok, "second change should be AddIndex")
	})
}
