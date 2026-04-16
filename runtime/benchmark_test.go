package runtime

import (
	"context"
	stdsql "database/sql"
	"testing"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// CloneSlice Benchmarks
// =============================================================================

func BenchmarkCloneSlice_Empty(b *testing.B) {
	var s []string
	for b.Loop() {
		_ = CloneSlice(s)
	}
}

func BenchmarkCloneSlice_Small(b *testing.B) {
	s := []string{"id", "name", "email"}
	for b.Loop() {
		_ = CloneSlice(s)
	}
}

func BenchmarkCloneSlice_Large(b *testing.B) {
	s := make([]string, 50)
	for i := range s {
		s[i] = "column_placeholder"
	}
	for b.Loop() {
		_ = CloneSlice(s)
	}
}

func BenchmarkCloneSlice_Funcs(b *testing.B) {
	s := make([]func(*sql.Selector), 5)
	for i := range s {
		s[i] = func(*sql.Selector) {}
	}
	for b.Loop() {
		_ = CloneSlice(s)
	}
}

// =============================================================================
// QueryContext Benchmarks
// =============================================================================

func BenchmarkQueryContext_Clone_Minimal(b *testing.B) {
	ctx := &QueryContext{Type: "User"}
	for b.Loop() {
		_ = ctx.Clone()
	}
}

func BenchmarkQueryContext_Clone_Full(b *testing.B) {
	unique := true
	limit := 10
	offset := 20
	ctx := &QueryContext{
		Type:   "User",
		Fields: []string{"id", "name", "email", "age", "created_at"},
		Unique: &unique,
		Limit:  &limit,
		Offset: &offset,
	}
	for b.Loop() {
		_ = ctx.Clone()
	}
}

func BenchmarkQueryContext_AppendFieldOnce_Cold(b *testing.B) {
	for b.Loop() {
		ctx := &QueryContext{Type: "User", Fields: []string{"id", "name"}}
		ctx.AppendFieldOnce("email")
	}
}

func BenchmarkQueryContext_AppendFieldOnce_Warm(b *testing.B) {
	ctx := &QueryContext{Type: "User", Fields: []string{"id", "name"}}
	ctx.AppendFieldOnce("warm_up") // build fieldsSeen
	b.ResetTimer()
	for b.Loop() {
		ctx.AppendFieldOnce("email")
	}
}

func BenchmarkQueryContext_AppendFieldOnce_Duplicate(b *testing.B) {
	ctx := &QueryContext{Type: "User", Fields: []string{"id", "name", "email"}}
	ctx.AppendFieldOnce("warm_up")
	b.ResetTimer()
	for b.Loop() {
		ctx.AppendFieldOnce("name") // already present
	}
}

// =============================================================================
// QueryBase Benchmarks
// =============================================================================

func BenchmarkQueryBase_Clone_Minimal(b *testing.B) {
	qb := NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
	for b.Loop() {
		_ = qb.Clone()
	}
}

func BenchmarkQueryBase_Clone_Full(b *testing.B) {
	qb := NewQueryBase(nil, "users",
		[]string{"id", "name", "email", "age", "created_at"},
		"id",
		[]string{"group_id", "org_id"},
		"User",
	)
	qb.Where(func(*sql.Selector) {})
	qb.Where(func(*sql.Selector) {})
	qb.AddOrder(func(*sql.Selector) {})
	qb.AddModifier(func(*sql.Selector) {})
	qb.Ctx.Fields = []string{"id", "name", "email"}
	unique := true
	qb.Ctx.Unique = &unique
	limit := 25
	qb.Ctx.Limit = &limit
	for b.Loop() {
		_ = qb.Clone()
	}
}

// =============================================================================
// BuildSelectorFrom Benchmarks
// =============================================================================

func BenchmarkBuildSelectorFrom_Simple(b *testing.B) {
	drv := newTestDB(b)
	qb := NewQueryBase(drv, "users", []string{"id", "name", "email"}, "id", nil, "User")
	ctx := context.Background()
	for b.Loop() {
		_, _ = BuildSelectorFrom(ctx, qb)
	}
}

func BenchmarkBuildSelectorFrom_WithPredicates(b *testing.B) {
	drv := newTestDB(b)
	qb := NewQueryBase(drv, "users", []string{"id", "name", "email"}, "id", nil, "User")
	qb.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "alice")) })
	qb.Where(func(s *sql.Selector) { s.Where(sql.GT(s.C("age"), 18)) })
	ctx := context.Background()
	for b.Loop() {
		_, _ = BuildSelectorFrom(ctx, qb)
	}
}

func BenchmarkBuildSelectorFrom_WithFieldProjection(b *testing.B) {
	drv := newTestDB(b)
	qb := NewQueryBase(drv, "users",
		[]string{"id", "name", "email", "age", "created_at", "updated_at"},
		"id", nil, "User")
	qb.Ctx.Fields = []string{"name", "email"}
	ctx := context.Background()
	for b.Loop() {
		_, _ = BuildSelectorFrom(ctx, qb)
	}
}

func BenchmarkBuildSelectorFrom_Full(b *testing.B) {
	drv := newTestDB(b)
	qb := NewQueryBase(drv, "users",
		[]string{"id", "name", "email", "age", "created_at"},
		"id",
		[]string{"group_id"},
		"User",
	)
	qb.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "alice")) })
	qb.AddOrder(func(s *sql.Selector) { s.OrderBy(s.C("name")) })
	qb.Ctx.Fields = []string{"name", "email"}
	unique := true
	qb.Ctx.Unique = &unique
	limit := 25
	qb.Ctx.Limit = &limit
	offset := 50
	qb.Ctx.Offset = &offset
	qb.WithFKs = true
	ctx := context.Background()
	for b.Loop() {
		_, _ = BuildSelectorFrom(ctx, qb)
	}
}

// =============================================================================
// MakeQuerySpec Benchmarks
// =============================================================================

func BenchmarkMakeQuerySpec_Simple(b *testing.B) {
	qb := NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", nil, "User")
	for b.Loop() {
		_ = MakeQuerySpec(qb, field.TypeInt)
	}
}

func BenchmarkMakeQuerySpec_Full(b *testing.B) {
	qb := NewQueryBase(nil, "users",
		[]string{"id", "name", "email", "age", "created_at"},
		"id",
		[]string{"group_id"},
		"User",
	)
	qb.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "alice")) })
	qb.AddOrder(func(s *sql.Selector) { s.OrderBy(s.C("name")) })
	qb.Ctx.Fields = []string{"name", "email"}
	unique := true
	qb.Ctx.Unique = &unique
	limit := 25
	qb.Ctx.Limit = &limit
	offset := 50
	qb.Ctx.Offset = &offset
	qb.WithFKs = true
	for b.Loop() {
		_ = MakeQuerySpec(qb, field.TypeInt)
	}
}

// =============================================================================
// Registry Benchmarks
// =============================================================================

func BenchmarkRegistryLookup_Mutator(b *testing.B) {
	RegisterMutator("BenchEntity", func(_ context.Context, _ Config, _ any) (any, error) {
		return nil, nil
	})
	b.ResetTimer()
	for b.Loop() {
		_ = FindMutator("BenchEntity")
	}
}

func BenchmarkRegistryLookup_TypeInfo(b *testing.B) {
	RegisterTypeInfo("bench_entities", &RegisteredTypeInfo{
		Table:   "bench_entities",
		Columns: []string{"id", "name"},
	})
	b.ResetTimer()
	for b.Loop() {
		_ = FindRegisteredType("bench_entities")
	}
}

func BenchmarkRegistryLookup_ValidColumn(b *testing.B) {
	RegisterColumns("bench_col_table", func(col string) bool {
		switch col {
		case "id", "name", "email", "age":
			return true
		default:
			return false
		}
	})
	b.ResetTimer()
	for b.Loop() {
		_ = ValidColumn("bench_col_table", "name")
	}
}

func BenchmarkRegistryLookup_Miss(b *testing.B) {
	for b.Loop() {
		_ = FindMutator("NonExistentEntity")
	}
}

// =============================================================================
// ScanAll Benchmark (real SQLite)
// =============================================================================

func BenchmarkScanAll_10Rows(b *testing.B) {
	benchmarkScanAll(b, 10)
}

func BenchmarkScanAll_100Rows(b *testing.B) {
	benchmarkScanAll(b, 100)
}

func BenchmarkScanAll_1000Rows(b *testing.B) {
	benchmarkScanAll(b, 1000)
}

func benchmarkScanAll(b *testing.B, n int) {
	b.Helper()
	drv := newTestDB(b)
	ctx := context.Background()

	// Seed rows.
	for i := range n {
		var res stdsql.Result
		err := drv.Exec(ctx, "INSERT INTO users (name, age) VALUES (?, ?)", []any{"user", i}, &res)
		if err != nil {
			b.Fatal(err)
		}
	}

	meta := testTypeInfo()
	qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
	sc := meta.ScanConfig()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := QueryAllSC(ctx, qb, sc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// IDScanValues / ExtractID Benchmarks
// =============================================================================

func BenchmarkIDScanValues_Int(b *testing.B) {
	for b.Loop() {
		_ = IDScanValues(field.TypeInt)
	}
}

func BenchmarkIDScanValues_UUID(b *testing.B) {
	for b.Loop() {
		_ = IDScanValues(field.TypeUUID)
	}
}

func BenchmarkExtractID_Int(b *testing.B) {
	v := &stdsql.NullInt64{Int64: 42, Valid: true}
	for b.Loop() {
		_, _ = ExtractID(v, field.TypeInt)
	}
}

func BenchmarkExtractID_UUID(b *testing.B) {
	v := &stdsql.NullString{String: "550e8400-e29b-41d4-a716-446655440000", Valid: true}
	for b.Loop() {
		_, _ = ExtractID(v, field.TypeUUID)
	}
}

// =============================================================================
// LoadConfig Benchmarks
// =============================================================================

func BenchmarkLoadConfig_Apply(b *testing.B) {
	n := 10
	opts := []LoadOption{
		Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("active"), true)) }),
		Limit(10),
		func(c *LoadConfig) { c.Offset = &n },
		OrderBy(func(s *sql.Selector) { s.OrderBy(s.C("name")) }),
		Select("id", "name"),
		WithEdge("posts", Limit(5)),
	}
	for b.Loop() {
		cfg := &LoadConfig{}
		for _, o := range opts {
			o(cfg)
		}
	}
}
