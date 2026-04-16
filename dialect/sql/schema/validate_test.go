package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ValidationError
		expected string
	}{
		{
			name:     "with column",
			err:      &ValidationError{Table: "users", Column: "email", Message: "column will be dropped"},
			expected: "users.email: column will be dropped",
		},
		{
			name:     "without column",
			err:      &ValidationError{Table: "users", Message: "table will be dropped"},
			expected: "users: table will be dropped",
		},
		{
			name:     "breaking with column",
			err:      &ValidationError{Table: "orders", Column: "status", Message: "type changed", Breaking: true},
			expected: "orders.status: type changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestValidationResult_HasErrors(t *testing.T) {
	tests := []struct {
		name     string
		result   *ValidationResult
		expected bool
	}{
		{
			name:     "no errors",
			result:   &ValidationResult{},
			expected: false,
		},
		{
			name: "has errors",
			result: &ValidationResult{
				Errors: []*ValidationError{{Table: "t", Message: "err"}},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasErrors())
		})
	}
}

func TestValidationResult_HasWarnings(t *testing.T) {
	tests := []struct {
		name     string
		result   *ValidationResult
		expected bool
	}{
		{
			name:     "no warnings",
			result:   &ValidationResult{},
			expected: false,
		},
		{
			name: "has warnings",
			result: &ValidationResult{
				Warnings: []*ValidationError{{Table: "t", Message: "warn"}},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasWarnings())
		})
	}
}

func TestValidationResult_HasBreakingChanges(t *testing.T) {
	tests := []struct {
		name     string
		result   *ValidationResult
		expected bool
	}{
		{
			name:     "empty result",
			result:   &ValidationResult{},
			expected: false,
		},
		{
			name: "non-breaking error",
			result: &ValidationResult{
				Errors: []*ValidationError{{Table: "t", Message: "err", Breaking: false}},
			},
			expected: false,
		},
		{
			name: "breaking error",
			result: &ValidationResult{
				Errors: []*ValidationError{{Table: "t", Message: "err", Breaking: true}},
			},
			expected: true,
		},
		{
			name: "breaking warning",
			result: &ValidationResult{
				Warnings: []*ValidationError{{Table: "t", Message: "warn", Breaking: true}},
			},
			expected: true,
		},
		{
			name: "non-breaking warning",
			result: &ValidationResult{
				Warnings: []*ValidationError{{Table: "t", Message: "warn", Breaking: false}},
			},
			expected: false,
		},
		{
			name: "mixed breaking and non-breaking",
			result: &ValidationResult{
				Errors:   []*ValidationError{{Table: "t", Message: "err", Breaking: false}},
				Warnings: []*ValidationError{{Table: "t", Message: "warn", Breaking: true}},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasBreakingChanges())
		})
	}
}

func TestValidationResult_String(t *testing.T) {
	tests := []struct {
		name     string
		result   *ValidationResult
		contains []string
	}{
		{
			name:     "no issues",
			result:   &ValidationResult{},
			contains: []string{"No issues found"},
		},
		{
			name: "errors only",
			result: &ValidationResult{
				Errors: []*ValidationError{
					{Table: "users", Column: "email", Message: "column will be dropped", Breaking: true},
				},
			},
			contains: []string{"Errors:", "users.email: column will be dropped", "[BREAKING]"},
		},
		{
			name: "warnings only",
			result: &ValidationResult{
				Warnings: []*ValidationError{
					{Table: "users", Column: "bio", Message: "size reduced"},
				},
			},
			contains: []string{"Warnings:", "users.bio: size reduced"},
		},
		{
			name: "errors and warnings",
			result: &ValidationResult{
				Errors: []*ValidationError{
					{Table: "users", Message: "table will be dropped", Breaking: true},
				},
				Warnings: []*ValidationError{
					{Table: "posts", Column: "title", Message: "type changed"},
				},
			},
			contains: []string{"Errors:", "Warnings:", "users: table will be dropped", "posts.title: type changed", "[BREAKING]"},
		},
		{
			name: "non-breaking error omits BREAKING tag",
			result: &ValidationResult{
				Errors: []*ValidationError{
					{Table: "users", Message: "something", Breaking: false},
				},
			},
			contains: []string{"Errors:", "users: something"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.result.String()
			for _, c := range tt.contains {
				assert.Contains(t, s, c)
			}
		})
	}

	// Verify non-breaking error does NOT contain [BREAKING]
	t.Run("non-breaking error no BREAKING tag", func(t *testing.T) {
		r := &ValidationResult{
			Errors: []*ValidationError{
				{Table: "t", Message: "msg", Breaking: false},
			},
		}
		assert.NotContains(t, r.String(), "[BREAKING]")
	})
}

func TestValidateOptions(t *testing.T) {
	t.Run("AllowDropColumn", func(t *testing.T) {
		cfg := &validateConfig{}
		AllowDropColumn()(cfg)
		assert.True(t, cfg.allowDropColumn)
	})
	t.Run("AllowDropTable", func(t *testing.T) {
		cfg := &validateConfig{}
		AllowDropTable()(cfg)
		assert.True(t, cfg.allowDropTable)
	})
	t.Run("AllowDropIndex", func(t *testing.T) {
		cfg := &validateConfig{}
		AllowDropIndex()(cfg)
		assert.True(t, cfg.allowDropIndex)
	})
	t.Run("AllowNullToNotNull", func(t *testing.T) {
		cfg := &validateConfig{}
		AllowNullToNotNull()(cfg)
		assert.True(t, cfg.allowNullToNotNull)
	})
}

// Helper to create a simple table with columns for testing.
func newTestTable(name string, cols ...*Column) *Table {
	t := &Table{
		Name:    name,
		columns: make(map[string]*Column),
	}
	for _, c := range cols {
		t.AddColumn(c)
	}
	return t
}

func TestValidateDiff(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		tbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "name", Type: field.TypeString, Size: 255},
		)
		result := ValidateDiff([]*Table{tbl}, []*Table{tbl})
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("new table no validation needed", func(t *testing.T) {
		current := []*Table{}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("dropped table error", func(t *testing.T) {
		current := []*Table{
			newTestTable("users", &Column{Name: "id", Type: field.TypeInt64}),
		}
		desired := []*Table{}
		result := ValidateDiff(current, desired)
		require.True(t, result.HasErrors())
		assert.True(t, result.HasBreakingChanges())
		assert.Equal(t, 1, len(result.Errors))
		assert.Equal(t, "users", result.Errors[0].Table)
		assert.Contains(t, result.Errors[0].Message, "table will be dropped")
		assert.True(t, result.Errors[0].Breaking)
	})

	t.Run("dropped table allowed", func(t *testing.T) {
		current := []*Table{
			newTestTable("users", &Column{Name: "id", Type: field.TypeInt64}),
		}
		desired := []*Table{}
		result := ValidateDiff(current, desired, AllowDropTable())
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Equal(t, 1, len(result.Warnings))
		assert.Contains(t, result.Warnings[0].Message, "table will be dropped")
		assert.True(t, result.Warnings[0].Breaking)
	})

	t.Run("dropped column error", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		result := ValidateDiff(current, desired)
		require.True(t, result.HasErrors())
		assert.True(t, result.HasBreakingChanges())
		assert.Equal(t, "users", result.Errors[0].Table)
		assert.Equal(t, "email", result.Errors[0].Column)
		assert.Contains(t, result.Errors[0].Message, "column will be dropped")
	})

	t.Run("dropped column allowed", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		result := ValidateDiff(current, desired, AllowDropColumn())
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "column will be dropped")
	})

	t.Run("new NOT NULL column without default warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString, Nullable: false, Default: nil},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "NOT NULL column without default")
	})

	t.Run("new NOT NULL column with default no warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString, Nullable: false, Default: ""},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("new nullable column no warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "bio", Type: field.TypeString, Nullable: true},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("column type change warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "age", Type: field.TypeInt},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "age", Type: field.TypeString},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "column type changing")
	})

	t.Run("nullable to NOT NULL error", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "bio", Type: field.TypeString, Nullable: true},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "bio", Type: field.TypeString, Nullable: false},
			),
		}
		result := ValidateDiff(current, desired)
		require.True(t, result.HasErrors())
		assert.True(t, result.HasBreakingChanges())
		assert.Contains(t, result.Errors[0].Message, "NULL to NOT NULL")
	})

	t.Run("nullable to NOT NULL allowed", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "bio", Type: field.TypeString, Nullable: true},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "bio", Type: field.TypeString, Nullable: false},
			),
		}
		result := ValidateDiff(current, desired, AllowNullToNotNull())
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "NULL to NOT NULL")
		assert.True(t, result.Warnings[0].Breaking)
	})

	t.Run("size reduction warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "name", Type: field.TypeString, Size: 255},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "name", Type: field.TypeString, Size: 100},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "size reducing from 255 to 100")
	})

	t.Run("size increase no warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "name", Type: field.TypeString, Size: 100},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "name", Type: field.TypeString, Size: 255},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("unique constraint added warning", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString, Unique: false},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "email", Type: field.TypeString, Unique: true},
			),
		}
		result := ValidateDiff(current, desired)
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "UNIQUE constraint")
	})

	t.Run("dropped index error", func(t *testing.T) {
		currentTbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "email", Type: field.TypeString},
		)
		currentTbl.Indexes = []*Index{
			{Name: "idx_users_email", Columns: []*Column{{Name: "email"}}},
		}
		desiredTbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "email", Type: field.TypeString},
		)
		result := ValidateDiff([]*Table{currentTbl}, []*Table{desiredTbl})
		require.True(t, result.HasErrors())
		assert.Contains(t, result.Errors[0].Message, `index "idx_users_email" will be dropped`)
	})

	t.Run("dropped index allowed", func(t *testing.T) {
		currentTbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "email", Type: field.TypeString},
		)
		currentTbl.Indexes = []*Index{
			{Name: "idx_users_email", Columns: []*Column{{Name: "email"}}},
		}
		desiredTbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "email", Type: field.TypeString},
		)
		result := ValidateDiff([]*Table{currentTbl}, []*Table{desiredTbl}, AllowDropIndex())
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, `index "idx_users_email" will be dropped`)
	})

	t.Run("multiple changes combined", func(t *testing.T) {
		current := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "name", Type: field.TypeString, Size: 255},
				&Column{Name: "legacy", Type: field.TypeString},
			),
		}
		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "name", Type: field.TypeString, Size: 100},
				// legacy column dropped
				// new NOT NULL column without default
				&Column{Name: "email", Type: field.TypeString, Nullable: false},
			),
		}
		result := ValidateDiff(current, desired)
		// Should have: dropped column error + size reduction warning + new NOT NULL warning
		assert.True(t, result.HasErrors())
		assert.True(t, result.HasWarnings())
		// At least 1 error (dropped column)
		require.GreaterOrEqual(t, len(result.Errors), 1)
		// At least 2 warnings (size reduction + new NOT NULL without default)
		require.GreaterOrEqual(t, len(result.Warnings), 2)
	})

	t.Run("all options combined", func(t *testing.T) {
		current := []*Table{
			newTestTable("old_table", &Column{Name: "id", Type: field.TypeInt64}),
		}
		currentTbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "dropped_col", Type: field.TypeString},
			&Column{Name: "nullable_col", Type: field.TypeString, Nullable: true},
		)
		currentTbl.Indexes = []*Index{
			{Name: "idx_old", Columns: []*Column{{Name: "id"}}},
		}
		current = append(current, currentTbl)

		desired := []*Table{
			newTestTable("users",
				&Column{Name: "id", Type: field.TypeInt64},
				&Column{Name: "nullable_col", Type: field.TypeString, Nullable: false},
			),
		}

		result := ValidateDiff(current, desired,
			AllowDropTable(),
			AllowDropColumn(),
			AllowDropIndex(),
			AllowNullToNotNull(),
		)
		// All issues should be warnings, not errors
		assert.False(t, result.HasErrors())
		assert.True(t, result.HasWarnings())
	})
}

func TestValidateTable(t *testing.T) {
	t.Run("valid table", func(t *testing.T) {
		tbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
			&Column{Name: "name", Type: field.TypeString},
		)
		tbl.PrimaryKey = []*Column{tbl.Columns[0]}
		tbl.Indexes = []*Index{
			{Name: "idx_name", Columns: []*Column{tbl.Columns[1]}},
		}
		result := ValidateTable(tbl)
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("no primary key warning", func(t *testing.T) {
		tbl := newTestTable("users",
			&Column{Name: "id", Type: field.TypeInt64},
		)
		// No primary key set
		result := ValidateTable(tbl)
		assert.False(t, result.HasErrors())
		require.True(t, result.HasWarnings())
		assert.Contains(t, result.Warnings[0].Message, "no primary key")
	})

	t.Run("duplicate column names error", func(t *testing.T) {
		tbl := &Table{
			Name:    "users",
			columns: make(map[string]*Column),
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt64},
				{Name: "id", Type: field.TypeInt64},
			},
			PrimaryKey: []*Column{{Name: "id"}},
		}
		result := ValidateTable(tbl)
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Column == "id" && e.Message == "duplicate column name" {
				found = true
				break
			}
		}
		assert.True(t, found, "expected duplicate column name error for 'id'")
	})

	t.Run("duplicate index names error", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		tbl := newTestTable("users", idCol)
		tbl.PrimaryKey = []*Column{idCol}
		tbl.Indexes = []*Index{
			{Name: "idx_id", Columns: []*Column{idCol}},
			{Name: "idx_id", Columns: []*Column{idCol}},
		}
		result := ValidateTable(tbl)
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Message == "duplicate index name: idx_id" {
				found = true
				break
			}
		}
		assert.True(t, found, "expected duplicate index name error")
	})

	t.Run("index references non-existent column error", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		tbl := newTestTable("users", idCol)
		tbl.PrimaryKey = []*Column{idCol}
		tbl.Indexes = []*Index{
			{Name: "idx_missing", Columns: []*Column{{Name: "nonexistent"}}},
		}
		result := ValidateTable(tbl)
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Table == "users" && e.Message == `index "idx_missing" references non-existent column "nonexistent"` {
				found = true
				break
			}
		}
		assert.True(t, found, "expected index references non-existent column error")
	})

	t.Run("index with nil column skipped", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		tbl := newTestTable("users", idCol)
		tbl.PrimaryKey = []*Column{idCol}
		tbl.Indexes = []*Index{
			{Name: "idx_nil", Columns: []*Column{nil}},
		}
		result := ValidateTable(tbl)
		// nil column should be skipped, no error about non-existent column
		assert.False(t, result.HasErrors())
	})

	t.Run("foreign key references non-existent column error", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		tbl := newTestTable("posts", idCol)
		tbl.PrimaryKey = []*Column{idCol}
		tbl.ForeignKeys = []*ForeignKey{
			{
				Symbol:   "fk_posts_user",
				Columns:  []*Column{{Name: "user_id"}}, // user_id does not exist in posts
				RefTable: &Table{Name: "users"},
			},
		}
		result := ValidateTable(tbl)
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Table == "posts" && e.Message == `foreign key references non-existent column "user_id"` {
				found = true
				break
			}
		}
		assert.True(t, found, "expected FK references non-existent column error")
	})

	t.Run("valid foreign key column exists", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		userIDCol := &Column{Name: "user_id", Type: field.TypeInt64}
		tbl := newTestTable("posts", idCol, userIDCol)
		tbl.PrimaryKey = []*Column{idCol}
		tbl.ForeignKeys = []*ForeignKey{
			{
				Symbol:   "fk_posts_user",
				Columns:  []*Column{userIDCol},
				RefTable: &Table{Name: "users"},
			},
		}
		result := ValidateTable(tbl)
		// No errors about FK columns (may have warning about no PK for ref table, but that's ValidateSchema)
		fkErr := false
		for _, e := range result.Errors {
			if e.Message == `foreign key references non-existent column "user_id"` {
				fkErr = true
			}
		}
		assert.False(t, fkErr)
	})
}

func TestValidateSchema(t *testing.T) {
	t.Run("valid schema", func(t *testing.T) {
		usersIDCol := &Column{Name: "id", Type: field.TypeInt64}
		usersTbl := newTestTable("users", usersIDCol)
		usersTbl.PrimaryKey = []*Column{usersIDCol}

		postsIDCol := &Column{Name: "id", Type: field.TypeInt64}
		postsUserIDCol := &Column{Name: "user_id", Type: field.TypeInt64}
		postsTbl := newTestTable("posts", postsIDCol, postsUserIDCol)
		postsTbl.PrimaryKey = []*Column{postsIDCol}
		postsTbl.ForeignKeys = []*ForeignKey{
			{
				Symbol:     "fk_posts_user",
				Columns:    []*Column{postsUserIDCol},
				RefTable:   usersTbl,
				RefColumns: []*Column{usersIDCol},
			},
		}

		result := ValidateSchema([]*Table{usersTbl, postsTbl})
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})

	t.Run("duplicate table names error", func(t *testing.T) {
		idCol1 := &Column{Name: "id", Type: field.TypeInt64}
		tbl1 := newTestTable("users", idCol1)
		tbl1.PrimaryKey = []*Column{idCol1}

		idCol2 := &Column{Name: "id", Type: field.TypeInt64}
		tbl2 := newTestTable("users", idCol2)
		tbl2.PrimaryKey = []*Column{idCol2}

		result := ValidateSchema([]*Table{tbl1, tbl2})
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Table == "users" && e.Message == "duplicate table name" {
				found = true
				break
			}
		}
		assert.True(t, found, "expected duplicate table name error")
	})

	t.Run("foreign key references non-existent table error", func(t *testing.T) {
		idCol := &Column{Name: "id", Type: field.TypeInt64}
		userIDCol := &Column{Name: "user_id", Type: field.TypeInt64}
		postsTbl := newTestTable("posts", idCol, userIDCol)
		postsTbl.PrimaryKey = []*Column{idCol}
		postsTbl.ForeignKeys = []*ForeignKey{
			{
				Symbol:   "fk_posts_user",
				Columns:  []*Column{userIDCol},
				RefTable: &Table{Name: "nonexistent_table"},
			},
		}

		result := ValidateSchema([]*Table{postsTbl})
		require.True(t, result.HasErrors())
		found := false
		for _, e := range result.Errors {
			if e.Table == "posts" && e.Message == `foreign key references non-existent table "nonexistent_table"` {
				found = true
				break
			}
		}
		assert.True(t, found, "expected FK references non-existent table error")
	})

	t.Run("schema validates individual tables", func(t *testing.T) {
		// Table with no PK and duplicate columns should produce warnings/errors
		tbl := &Table{
			Name:    "bad_table",
			columns: make(map[string]*Column),
			Columns: []*Column{
				{Name: "col", Type: field.TypeString},
				{Name: "col", Type: field.TypeString},
			},
		}
		result := ValidateSchema([]*Table{tbl})
		require.True(t, result.HasErrors())
		// Should have duplicate column error from ValidateTable
		dupFound := false
		for _, e := range result.Errors {
			if e.Column == "col" && e.Message == "duplicate column name" {
				dupFound = true
				break
			}
		}
		assert.True(t, dupFound, "expected duplicate column error from individual table validation")
		// Should have no primary key warning from ValidateTable
		pkFound := false
		for _, w := range result.Warnings {
			if w.Message == "table has no primary key" {
				pkFound = true
				break
			}
		}
		assert.True(t, pkFound, "expected no primary key warning from individual table validation")
	})

	t.Run("empty schema is valid", func(t *testing.T) {
		result := ValidateSchema([]*Table{})
		assert.False(t, result.HasErrors())
		assert.False(t, result.HasWarnings())
	})
}
