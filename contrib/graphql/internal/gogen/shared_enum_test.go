package gogen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// Budget's period_type and BudgetPeriod's type share velox-gen's Go enum
// type BudgetPeriodType: its marshalers are written once, for the first.
func TestSharedEnumMarshalers(t *testing.T) {
	id := func() *plugin.Field {
		return &plugin.Field{Name: "id", GoName: "ID", Column: "id", Type: "int64", AutoID: true}
	}
	pt := &plugin.Field{Name: "period_type", GoName: "PeriodType", Type: "enum", Enum: "BudgetPeriodType", EnumValues: []string{"annual", "quarterly", "monthly"}}
	ty := &plugin.Field{Name: "type", GoName: "Type", Type: "enum", Enum: "BudgetPeriodType", EnumValues: []string{"monthly", "quarterly", "annual"}}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{
		{Name: "Budget", Label: "budget", Fields: []*plugin.Field{id(), pt}, Edges: []*plugin.Edge{}, PrimaryKey: []int{0}},
		{Name: "BudgetPeriod", Label: "budget_period", Fields: []*plugin.Field{id(), ty}, Edges: []*plugin.Edge{}, PrimaryKey: []int{0}},
	}}
	m.Enums = []*plugin.Enum{{Type: "BudgetPeriodType", Values: []string{"annual", "quarterly", "monthly"}, Consts: []string{"BudgetPeriodTypeAnnual", "BudgetPeriodTypeQuarterly", "BudgetPeriodTypeMonthly"}}}
	files, err := RenderEnums(m, "example.com/app/gen", "gen")
	if err != nil {
		t.Fatal(err)
	}
	var all string
	for _, f := range files {
		all += f.Content
	}
	if n := strings.Count(all, "func (e BudgetPeriodType) MarshalGQL"); n != 1 {
		t.Fatalf("%d MarshalGQL:\n%s", n, all)
	}
	if !strings.Contains(all, "case BudgetPeriodTypeAnnual:\n\t\t_, _ = io.WriteString(w, `\"ANNUAL\"`)") {
		t.Errorf("annual's value:\n%s", all)
	}
}
