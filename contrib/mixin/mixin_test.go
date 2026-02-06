package mixin_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateTimeMixin tests the CreateTime mixin.
func TestCreateTimeMixin(t *testing.T) {
	m := mixin.CreateTime{}

	t.Run("has_one_field", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Equal(t, "created_at", desc.Name)
	})

	t.Run("field_is_immutable", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable)
	})

	t.Run("has_default", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.NotNil(t, desc.Default)
	})

	t.Run("no_update_default", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Nil(t, desc.UpdateDefault)
	})
}

// TestUpdateTimeMixin tests the UpdateTime mixin.
func TestUpdateTimeMixin(t *testing.T) {
	m := mixin.UpdateTime{}

	t.Run("has_one_field", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Equal(t, "updated_at", desc.Name)
	})

	t.Run("has_default", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.NotNil(t, desc.Default)
	})

	t.Run("has_update_default", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.NotNil(t, desc.UpdateDefault)
	})

	t.Run("not_immutable", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.False(t, desc.Immutable)
	})
}

// TestTimeMixin tests the composed Time mixin.
func TestTimeMixin(t *testing.T) {
	tests := []struct {
		name     string
		validate func(t *testing.T, fields []velox.Field)
	}{
		{
			name: "has_two_fields",
			validate: func(t *testing.T, fields []velox.Field) {
				require.Len(t, fields, 2)
			},
		},
		{
			name: "first_field_is_created_at",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.Equal(t, "created_at", fields[0].Descriptor().Name)
			},
		},
		{
			name: "second_field_is_updated_at",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.Equal(t, "updated_at", fields[1].Descriptor().Name)
			},
		},
		{
			name: "create_time_is_immutable",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.True(t, fields[0].Descriptor().Immutable)
			},
		},
		{
			name: "update_time_is_not_immutable",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.False(t, fields[1].Descriptor().Immutable)
			},
		},
		{
			name: "both_have_defaults",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.NotNil(t, fields[0].Descriptor().Default)
				assert.NotNil(t, fields[1].Descriptor().Default)
			},
		},
		{
			name: "only_update_time_has_update_default",
			validate: func(t *testing.T, fields []velox.Field) {
				assert.Nil(t, fields[0].Descriptor().UpdateDefault)
				assert.NotNil(t, fields[1].Descriptor().UpdateDefault)
			},
		},
	}

	m := mixin.Time{}
	fields := m.Fields()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(t, fields)
		})
	}
}

// TestIDMixin tests the ID mixin.
func TestIDMixin(t *testing.T) {
	m := mixin.ID{}

	t.Run("has_one_field", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Equal(t, "id", desc.Name)
	})

	t.Run("field_is_immutable", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable)
	})

	t.Run("has_default", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.NotNil(t, desc.Default)
	})
}

// TestSoftDeleteMixin tests the SoftDelete mixin.
func TestSoftDeleteMixin(t *testing.T) {
	m := mixin.SoftDelete{}

	t.Run("has_one_field", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Equal(t, "deleted_at", desc.Name)
	})

	t.Run("field_is_optional", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.True(t, desc.Optional)
	})

	t.Run("field_is_nillable", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.True(t, desc.Nillable)
	})
}

// TestTenantIDMixin tests the TenantID mixin.
func TestTenantIDMixin(t *testing.T) {
	m := mixin.TenantID{}

	t.Run("has_one_field", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.Equal(t, "tenant_id", desc.Name)
	})

	t.Run("field_is_immutable", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable)
	})

	t.Run("has_validator", func(t *testing.T) {
		fields := m.Fields()
		desc := fields[0].Descriptor()
		assert.NotEmpty(t, desc.Validators, "tenant_id should have NotEmpty validator")
	})
}

// TestTimeSoftDeleteMixin tests the TimeSoftDelete mixin.
func TestTimeSoftDeleteMixin(t *testing.T) {
	m := mixin.TimeSoftDelete{}

	t.Run("has_three_fields", func(t *testing.T) {
		fields := m.Fields()
		require.Len(t, fields, 3)
	})

	t.Run("field_names", func(t *testing.T) {
		fields := m.Fields()
		assert.Equal(t, "created_at", fields[0].Descriptor().Name)
		assert.Equal(t, "updated_at", fields[1].Descriptor().Name)
		assert.Equal(t, "deleted_at", fields[2].Descriptor().Name)
	})
}

// TestMixinComposition tests composing custom mixins with contrib mixins.
func TestMixinComposition(t *testing.T) {
	t.Run("custom_mixin_with_time", func(t *testing.T) {
		// Custom mixin that embeds Time
		type CustomMixin struct {
			mixin.Time
		}

		m := CustomMixin{}
		fields := m.Fields()
		require.Len(t, fields, 2)
		assert.Equal(t, "created_at", fields[0].Descriptor().Name)
		assert.Equal(t, "updated_at", fields[1].Descriptor().Name)
	})
}

// TestMixinImplementsInterface tests that all mixins implement velox.Mixin.
func TestMixinImplementsInterface(t *testing.T) {
	t.Run("CreateTime", func(_ *testing.T) {
		var _ velox.Mixin = mixin.CreateTime{}
		var _ velox.Mixin = &mixin.CreateTime{}
	})

	t.Run("UpdateTime", func(_ *testing.T) {
		var _ velox.Mixin = mixin.UpdateTime{}
		var _ velox.Mixin = &mixin.UpdateTime{}
	})

	t.Run("Time", func(_ *testing.T) {
		var _ velox.Mixin = mixin.Time{}
		var _ velox.Mixin = &mixin.Time{}
	})

	t.Run("ID", func(_ *testing.T) {
		var _ velox.Mixin = mixin.ID{}
		var _ velox.Mixin = &mixin.ID{}
	})

	t.Run("SoftDelete", func(_ *testing.T) {
		var _ velox.Mixin = mixin.SoftDelete{}
		var _ velox.Mixin = &mixin.SoftDelete{}
	})

	t.Run("TenantID", func(_ *testing.T) {
		var _ velox.Mixin = mixin.TenantID{}
		var _ velox.Mixin = &mixin.TenantID{}
	})

	t.Run("TimeSoftDelete", func(_ *testing.T) {
		var _ velox.Mixin = mixin.TimeSoftDelete{}
		var _ velox.Mixin = &mixin.TimeSoftDelete{}
	})
}

// BenchmarkMixin benchmarks mixin operations.
func BenchmarkMixin(b *testing.B) {
	b.Run("Time.Fields", func(b *testing.B) {
		m := mixin.Time{}
		for i := 0; i < b.N; i++ {
			_ = m.Fields()
		}
	})

	b.Run("ID.Fields", func(b *testing.B) {
		m := mixin.ID{}
		for i := 0; i < b.N; i++ {
			_ = m.Fields()
		}
	})

	b.Run("TimeSoftDelete.Fields", func(b *testing.B) {
		m := mixin.TimeSoftDelete{}
		for i := 0; i < b.N; i++ {
			_ = m.Fields()
		}
	})
}
