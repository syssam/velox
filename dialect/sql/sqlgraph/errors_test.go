package sqlgraph

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
)

func TestRel_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rel      Rel
		expected string
	}{
		{O2O, "O2O"},
		{O2M, "O2M"},
		{M2O, "M2O"},
		{M2M, "M2M"},
		{Rel(0), "Unknown"},
		{Rel(99), "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.rel.String())
		})
	}
}

func TestConstraintError_Error(t *testing.T) {
	t.Parallel()
	err := ConstraintError{msg: "unique violation on users.email"}
	assert.Equal(t, "unique violation on users.email", err.Error())
}

func TestNotFoundError_Error(t *testing.T) {
	t.Parallel()
	err := &NotFoundError{table: "users", id: 42}
	assert.Equal(t, "record with id 42 not found in table users", err.Error())
}

func TestNotFoundError_StringID(t *testing.T) {
	t.Parallel()
	err := &NotFoundError{table: "posts", id: "abc-123"}
	assert.Contains(t, err.Error(), "abc-123")
	assert.Contains(t, err.Error(), "posts")
}

func TestIsConstraintError_Extended(t *testing.T) {
	t.Parallel()

	t.Run("ConstraintError type", func(t *testing.T) {
		t.Parallel()
		err := &ConstraintError{msg: "test"}
		assert.True(t, IsConstraintError(err))
	})

	t.Run("wrapped ConstraintError", func(t *testing.T) {
		t.Parallel()
		err := fmt.Errorf("wrap: %w", &ConstraintError{msg: "test"})
		assert.True(t, IsConstraintError(err))
	})

	t.Run("non-constraint error", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsConstraintError(errors.New("other")))
	})

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsConstraintError(nil))
	})
}

// mockSQLStateError simulates a PostgreSQL error with SQLSTATE.
type mockSQLStateError struct {
	state string
}

func (e *mockSQLStateError) Error() string    { return "pg error: " + e.state }
func (e *mockSQLStateError) SQLState() string { return e.state }

// mockErrorCoder simulates an error with Code() method (pq.Error style).
type mockErrorCoder struct {
	code string
}

func (e *mockErrorCoder) Error() string { return "coded error: " + e.code }
func (e *mockErrorCoder) Code() string  { return e.code }

// mockErrorNumberer simulates a MySQL error with Number() method.
type mockErrorNumberer struct {
	num uint16
}

func (e *mockErrorNumberer) Error() string  { return fmt.Sprintf("mysql error: %d", e.num) }
func (e *mockErrorNumberer) Number() uint16 { return e.num }

func TestIsUniqueConstraintError(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsUniqueConstraintError(nil))
	})

	t.Run("PostgreSQL SQLSTATE", func(t *testing.T) {
		t.Parallel()
		err := &mockSQLStateError{state: "23505"}
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("PostgreSQL code", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorCoder{code: "23505"}
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("MySQL error number", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorNumberer{num: 1062}
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("SQLite string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("UNIQUE constraint failed: users.email")
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("Postgres string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("pq: violates unique constraint \"users_email_key\"")
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("MySQL string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("Error 1062: Duplicate entry 'foo' for key 'email'")
		assert.True(t, IsUniqueConstraintError(err))
	})

	t.Run("non-matching", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsUniqueConstraintError(errors.New("connection refused")))
	})
}

func TestIsForeignKeyConstraintError(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsForeignKeyConstraintError(nil))
	})

	t.Run("PostgreSQL SQLSTATE", func(t *testing.T) {
		t.Parallel()
		err := &mockSQLStateError{state: "23503"}
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("PostgreSQL code", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorCoder{code: "23503"}
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("MySQL error number parent", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorNumberer{num: 1451}
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("MySQL error number child", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorNumberer{num: 1452}
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("SQLite string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("FOREIGN KEY constraint failed")
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("Postgres string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("violates foreign key constraint")
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("MySQL string fallback 1451", func(t *testing.T) {
		t.Parallel()
		err := errors.New("Error 1451: Cannot delete or update a parent row")
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("MySQL string fallback 1452", func(t *testing.T) {
		t.Parallel()
		err := errors.New("Error 1452: Cannot add or update a child row")
		assert.True(t, IsForeignKeyConstraintError(err))
	})

	t.Run("non-matching", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsForeignKeyConstraintError(errors.New("timeout")))
	})
}

func TestIsCheckConstraintError(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsCheckConstraintError(nil))
	})

	t.Run("PostgreSQL SQLSTATE", func(t *testing.T) {
		t.Parallel()
		err := &mockSQLStateError{state: "23514"}
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("PostgreSQL code", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorCoder{code: "23514"}
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("MySQL error number", func(t *testing.T) {
		t.Parallel()
		err := &mockErrorNumberer{num: 3819}
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("SQLite string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("CHECK constraint failed: users_age_check")
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("Postgres string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("violates check constraint \"positive_amount\"")
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("MySQL string fallback", func(t *testing.T) {
		t.Parallel()
		err := errors.New("Error 3819: Check constraint violation")
		assert.True(t, IsCheckConstraintError(err))
	})

	t.Run("non-matching", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsCheckConstraintError(errors.New("permission denied")))
	})
}

func TestIsConstraintError_Composite(t *testing.T) {
	t.Parallel()

	// IsConstraintError should match all three specific types
	t.Run("unique", func(t *testing.T) {
		t.Parallel()
		err := errors.New("UNIQUE constraint failed: users.email")
		assert.True(t, IsConstraintError(err))
	})

	t.Run("foreign key", func(t *testing.T) {
		t.Parallel()
		err := errors.New("FOREIGN KEY constraint failed")
		assert.True(t, IsConstraintError(err))
	})

	t.Run("check", func(t *testing.T) {
		t.Parallel()
		err := errors.New("CHECK constraint failed: positive")
		assert.True(t, IsConstraintError(err))
	})

	t.Run("non-matching", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsConstraintError(errors.New("something else")))
	})
}

func TestEdgeSpecs_GroupRel(t *testing.T) {
	t.Parallel()

	specs := EdgeSpecs{
		{Rel: O2M, Table: "posts"},
		{Rel: M2O, Table: "posts"},
		{Rel: O2M, Table: "comments"},
		{Rel: M2M, Table: "tags"},
	}
	groups := specs.GroupRel()
	assert.Len(t, groups[O2M], 2)
	assert.Len(t, groups[M2O], 1)
	assert.Len(t, groups[M2M], 1)
	assert.Len(t, groups[O2O], 0)
}

func TestEdgeSpecs_GroupTable(t *testing.T) {
	t.Parallel()

	specs := EdgeSpecs{
		{Rel: O2M, Table: "posts"},
		{Rel: M2O, Table: "posts"},
		{Rel: O2M, Table: "comments"},
	}
	groups := specs.GroupTable()
	assert.Len(t, groups["posts"], 2)
	assert.Len(t, groups["comments"], 1)
}

func TestEdgeSpecs_FilterRel(t *testing.T) {
	t.Parallel()

	specs := EdgeSpecs{
		{Rel: O2M, Table: "posts"},
		{Rel: M2O, Table: "users"},
		{Rel: O2M, Table: "comments"},
		{Rel: M2M, Table: "tags"},
	}

	o2m := specs.FilterRel(O2M)
	assert.Len(t, o2m, 2)

	m2m := specs.FilterRel(M2M)
	assert.Len(t, m2m, 1)

	o2o := specs.FilterRel(O2O)
	assert.Len(t, o2o, 0)
}

func TestNodeSpec_AddColumnOnce(t *testing.T) {
	t.Parallel()

	spec := &NodeSpec{
		Table:   "users",
		Columns: []string{"id", "name"},
	}

	// Adding a new column
	spec.AddColumnOnce("email")
	assert.Equal(t, []string{"id", "name", "email"}, spec.Columns)

	// Adding existing column should be a no-op
	spec.AddColumnOnce("name")
	assert.Equal(t, []string{"id", "name", "email"}, spec.Columns)

	// Adding another new column
	spec.AddColumnOnce("age")
	assert.Equal(t, []string{"id", "name", "email", "age"}, spec.Columns)
}

func TestEdgeTarget_FieldValues(t *testing.T) {
	t.Parallel()

	t.Run("with fields", func(t *testing.T) {
		t.Parallel()
		target := &EdgeTarget{
			Fields: []*FieldSpec{
				{Column: "weight", Type: field.TypeFloat64, Value: 1.5},
				{Column: "label", Type: field.TypeString, Value: "friend"},
			},
		}
		values := target.FieldValues()
		assert.Equal(t, []any{1.5, "friend"}, values)
	})

	t.Run("empty fields", func(t *testing.T) {
		t.Parallel()
		target := &EdgeTarget{}
		values := target.FieldValues()
		assert.Empty(t, values)
	})
}

func TestNewFieldSpec(t *testing.T) {
	t.Parallel()
	spec := NewFieldSpec("name", field.TypeString)
	assert.Equal(t, "name", spec.Column)
	assert.Equal(t, field.TypeString, spec.Type)
}

func TestNewCreateSpec(t *testing.T) {
	t.Parallel()
	id := &FieldSpec{Column: "id", Type: field.TypeInt}
	spec := NewCreateSpec("users", id)
	assert.Equal(t, "users", spec.Table)
	assert.Equal(t, id, spec.ID)
}

func TestCreateSpec_SetField(t *testing.T) {
	t.Parallel()
	spec := &CreateSpec{Table: "users"}
	spec.SetField("name", field.TypeString, "Alice")
	spec.SetField("age", field.TypeInt, 30)
	require.Len(t, spec.Fields, 2)
	assert.Equal(t, "name", spec.Fields[0].Column)
	assert.Equal(t, "Alice", spec.Fields[0].Value)
	assert.Equal(t, "age", spec.Fields[1].Column)
	assert.Equal(t, 30, spec.Fields[1].Value)
}

func TestNewDeleteSpec(t *testing.T) {
	t.Parallel()
	id := &FieldSpec{Column: "id", Type: field.TypeInt}
	spec := NewDeleteSpec("users", id)
	assert.Equal(t, "users", spec.Node.Table)
	assert.Equal(t, id, spec.Node.ID)
}

func TestNewUpdateSpec(t *testing.T) {
	t.Parallel()

	t.Run("with single ID", func(t *testing.T) {
		t.Parallel()
		id := &FieldSpec{Column: "id", Type: field.TypeInt}
		spec := NewUpdateSpec("users", []string{"id", "name"}, id)
		assert.Equal(t, "users", spec.Node.Table)
		assert.Equal(t, id, spec.Node.ID)
		assert.Equal(t, []string{"id", "name"}, spec.Node.Columns)
	})

	t.Run("with composite ID", func(t *testing.T) {
		t.Parallel()
		id1 := &FieldSpec{Column: "user_id", Type: field.TypeInt}
		id2 := &FieldSpec{Column: "group_id", Type: field.TypeInt}
		spec := NewUpdateSpec("memberships", []string{"user_id", "group_id"}, id1, id2)
		assert.Nil(t, spec.Node.ID)
		assert.Len(t, spec.Node.CompositeID, 2)
	})

	t.Run("without ID", func(t *testing.T) {
		t.Parallel()
		spec := NewUpdateSpec("settings", []string{"key", "value"})
		assert.Nil(t, spec.Node.ID)
		assert.Nil(t, spec.Node.CompositeID)
	})
}

func TestUpdateSpec_SetField(t *testing.T) {
	t.Parallel()
	spec := &UpdateSpec{Node: &NodeSpec{Table: "users"}}
	spec.SetField("name", field.TypeString, "Bob")
	require.Len(t, spec.Fields.Set, 1)
	assert.Equal(t, "name", spec.Fields.Set[0].Column)
}

func TestUpdateSpec_AddField(t *testing.T) {
	t.Parallel()
	spec := &UpdateSpec{Node: &NodeSpec{Table: "users"}}
	spec.AddField("age", field.TypeInt, 1)
	require.Len(t, spec.Fields.Add, 1)
	assert.Equal(t, "age", spec.Fields.Add[0].Column)
}

func TestUpdateSpec_ClearField(t *testing.T) {
	t.Parallel()
	spec := &UpdateSpec{Node: &NodeSpec{Table: "users"}}
	spec.ClearField("bio", field.TypeString)
	require.Len(t, spec.Fields.Clear, 1)
	assert.Equal(t, "bio", spec.Fields.Clear[0].Column)
}

func TestUpdateSpec_AddModifier(t *testing.T) {
	t.Parallel()
	spec := &UpdateSpec{Node: &NodeSpec{Table: "users"}}
	spec.AddModifier(func(_ *sql.UpdateBuilder) {})
	require.Len(t, spec.Modifiers, 1)
}

func TestUpdateSpec_AddModifiers(t *testing.T) {
	t.Parallel()
	spec := &UpdateSpec{Node: &NodeSpec{Table: "users"}}
	spec.AddModifiers(
		func(_ *sql.UpdateBuilder) {},
		func(_ *sql.UpdateBuilder) {},
	)
	assert.Len(t, spec.Modifiers, 2)
}

func TestNewQuerySpec(t *testing.T) {
	t.Parallel()
	id := &FieldSpec{Column: "id", Type: field.TypeInt}
	spec := NewQuerySpec("users", []string{"id", "name", "email"}, id)
	assert.Equal(t, "users", spec.Node.Table)
	assert.Equal(t, id, spec.Node.ID)
	assert.Equal(t, []string{"id", "name", "email"}, spec.Node.Columns)
}

func TestStep_Methods(t *testing.T) {
	t.Parallel()

	t.Run("FromEdgeOwner M2O", func(t *testing.T) {
		t.Parallel()
		s := NewStep(Edge(M2O, false, "pets", "owner_id"))
		assert.True(t, s.FromEdgeOwner())
		assert.False(t, s.ToEdgeOwner())
		assert.False(t, s.ThroughEdgeTable())
	})

	t.Run("FromEdgeOwner O2O inverse", func(t *testing.T) {
		t.Parallel()
		s := NewStep(Edge(O2O, true, "cards", "user_id"))
		assert.True(t, s.FromEdgeOwner())
		assert.False(t, s.ToEdgeOwner())
	})

	t.Run("ToEdgeOwner O2M", func(t *testing.T) {
		t.Parallel()
		s := NewStep(Edge(O2M, false, "pets", "owner_id"))
		assert.True(t, s.ToEdgeOwner())
		assert.False(t, s.FromEdgeOwner())
	})

	t.Run("ToEdgeOwner O2O non-inverse", func(t *testing.T) {
		t.Parallel()
		s := NewStep(Edge(O2O, false, "cards", "user_id"))
		assert.True(t, s.ToEdgeOwner())
		assert.False(t, s.FromEdgeOwner())
	})

	t.Run("ThroughEdgeTable M2M", func(t *testing.T) {
		t.Parallel()
		s := NewStep(Edge(M2M, false, "user_groups", "user_id", "group_id"))
		assert.True(t, s.ThroughEdgeTable())
		assert.False(t, s.FromEdgeOwner())
		assert.False(t, s.ToEdgeOwner())
	})
}

func TestSchema_MustAddE(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		g := &Schema{
			Nodes: []*Node{{Type: "user"}, {Type: "pet"}},
		}
		assert.NotPanics(t, func() {
			g.MustAddE("pets", &EdgeSpec{Rel: O2M}, "user", "pet")
		})
	})

	t.Run("panics on missing type", func(t *testing.T) {
		t.Parallel()
		g := &Schema{
			Nodes: []*Node{{Type: "user"}},
		}
		assert.Panics(t, func() {
			g.MustAddE("pets", &EdgeSpec{Rel: O2M}, "user", "nonexistent")
		})
	})
}

func TestSchema_EvalP_MissingNode(t *testing.T) {
	t.Parallel()
	g := &Schema{
		Nodes: []*Node{{Type: "user"}},
	}
	selector := sql.Select("*").From(sql.Table("users"))
	err := g.EvalP("nonexistent", nil, selector)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestWrappedFunc(t *testing.T) {
	t.Parallel()
	expr := WrapFunc(func(_ *sql.Selector) {})
	assert.NotNil(t, expr)
	assert.Equal(t, FuncSelector, expr.Func)
	assert.Len(t, expr.Args, 1)
}
