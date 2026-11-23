// Package benchschema builds, for benchmarks of migrate.Create and
// veloxtest.Open, the SQLite tables of bench/scale's generated schemas.
package benchschema

import (
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// Tables is the SQLite tables of bench/scale's n schemas: e0s..e<n-1>s,
// each with name, amount, created_at and note, in groups of ten where each
// but a group's head refers to the one before it and a group's head to the
// head of the group it belongs to.
//
// With wide, each table also has the columns, indexes and checks of an
// application's table: 33 columns in all, up to six secondary indexes, two
// of them partial, and two checks. A port of 496 entities has on average 33
// columns and seven indexes a table.
func Tables(n int, wide bool) *migrate.Schema {
	s := &migrate.Schema{Dialect: velox.SQLite}
	parent := func(k int) int { return 10 * ((k - 1) / 2) }
	for i := range n {
		name := fmt.Sprintf("e%ds", i)
		t := migrate.Table{
			Name: name,
			Columns: []migrate.Column{
				{Name: "id", Type: "integer", Identity: true},
				{Name: "name", Type: "text"},
				{Name: "amount", Type: "real"},
				{Name: "created_at", Type: "datetime(9)"},
				{Name: "note", Type: "text", Default: "''"},
			},
			PrimaryKey: []string{"id"},
		}
		ref, col := -1, ""
		switch {
		case i%10 != 0:
			ref, col = i-1, "prev_id"
		case i > 0:
			ref, col = parent(i/10), "up_id"
		}
		if ref >= 0 {
			t.Columns = append(t.Columns, migrate.Column{Name: col, Type: "integer"})
			t.ForeignKeys = []migrate.ForeignKey{{
				Name:       fmt.Sprintf("%s_e%ds_%s", name, ref, col),
				Columns:    []string{col},
				RefTable:   migrate.TableRef{Name: fmt.Sprintf("e%ds", ref)},
				RefColumns: []string{"id"},
			}}
			t.Indexes = []migrate.Index{{Name: name + "_" + col, Columns: []string{col}}}
		}
		if wide {
			widen(&t)
		}
		s.Tables = append(s.Tables, t)
	}
	return s
}

// widen adds to t the columns, indexes and checks of an application's
// table: a workspace, audit columns, a soft delete and a status.
func widen(t *migrate.Table) {
	t.Columns = append(t.Columns,
		migrate.Column{Name: "workspace_id", Type: "integer", Default: "0"},
		migrate.Column{Name: "updated_at", Type: "datetime(9)"},
		migrate.Column{Name: "deleted_at", Type: "datetime(9)", Nullable: true},
		migrate.Column{Name: "status", Type: "text", Default: "'draft'"},
		migrate.Column{Name: "ref", Type: "text", Default: "''"},
		migrate.Column{Name: "enabled", Type: "bool", Default: "true"},
		migrate.Column{Name: "meta", Type: "json", Nullable: true},
	)
	types := []string{"integer", "text", "decimal(18,4)", "datetime(9)"}
	for k := len(t.Columns); k < 33; k++ {
		t.Columns = append(t.Columns, migrate.Column{Name: fmt.Sprintf("c%d", k), Type: types[k%4], Nullable: true})
	}
	t.Indexes = append(t.Indexes,
		migrate.Index{Name: t.Name + "_ix_created", Columns: []string{"workspace_id", "created_at"}},
		migrate.Index{Name: t.Name + "_ix_deleted", Columns: []string{"workspace_id", "deleted_at"}},
		migrate.Index{Name: t.Name + "_ix_status", Columns: []string{"workspace_id", "status", "enabled"}, Where: "deleted_at IS NULL"},
		migrate.Index{Name: t.Name + "_ix_updated", Columns: []string{"workspace_id", "updated_at"}},
		migrate.Index{Name: t.Name + "_ux_ref", Columns: []string{"workspace_id", "ref"}, Unique: true, Where: "deleted_at IS NULL"},
	)
	t.Checks = []migrate.Check{
		{Name: t.Name + "_amount", Expr: `"amount" >= 0`},
		{Name: t.Name + "_status", Expr: `"status" IN ('draft', 'active', 'closed')`},
	}
}
