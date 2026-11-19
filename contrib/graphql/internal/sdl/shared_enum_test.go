package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// sharedModel is a model where velox-gen gives Budget's period_type and
// BudgetPeriod's type one Go enum type, BudgetPeriodType, in Budget's order.
func sharedModel() *plugin.Model {
	pt := &plugin.Field{Name: "period_type", GoName: "PeriodType", Type: "enum", Enum: "BudgetPeriodType", EnumValues: []string{"annual", "quarterly", "monthly"}}
	ty := &plugin.Field{Name: "type", GoName: "Type", Type: "enum", Enum: "BudgetPeriodType", EnumValues: []string{"monthly", "quarterly", "annual"}}
	m := model(entity("Budget", pt), entity("BudgetPeriod", ty))
	m.Enums = []*plugin.Enum{{Type: "BudgetPeriodType", Values: []string{"annual", "quarterly", "monthly"}, Consts: []string{"BudgetPeriodTypeAnnual", "BudgetPeriodTypeQuarterly", "BudgetPeriodTypeMonthly"}}}
	return m
}

// Two fields of one velox-gen enum type are one GraphQL enum, declared once
// in the Go type's order; the second field's enum is Shared, so its
// marshalers are not written twice.
func TestSharedEnumType(t *testing.T) {
	s, sdl := load(t, sharedModel())
	if typeOf(s, "Budget", "periodType") != "BudgetPeriodType!" || typeOf(s, "BudgetPeriod", "type") != "BudgetPeriodType!" {
		t.Errorf("field types:\n%s", sdl)
	}
	if strings.Count(sdl, "enum BudgetPeriodType") != 1 || !strings.Contains(sdl, "enum BudgetPeriodType {\n  ANNUAL\n  QUARTERLY\n  MONTHLY\n}") {
		t.Errorf("enum:\n%s", sdl)
	}
	surf, err := Build(sharedModel())
	if err != nil {
		t.Fatal(err)
	}
	var shared []bool
	for _, ty := range surf.Types {
		for _, en := range ty.Enums {
			shared = append(shared, en.Shared)
			if strings.Join(en.Values, " ") != "ANNUAL QUARTERLY MONTHLY" {
				t.Errorf("%s: values %v", ty.Name, en.Values)
			}
		}
	}
	if len(shared) != 2 || shared[0] || !shared[1] {
		t.Errorf("shared %v", shared)
	}
	ees, err := Enums(sharedModel())
	if err != nil || len(ees) != 2 || ees[0].Enums[0].Shared || !ees[1].Enums[0].Shared {
		t.Errorf("Enums: %+v, %v", ees, err)
	}

	// graphql.EnumValues naming a value otherwise on one field is refused.
	m := sharedModel()
	m.Entities[1].Fields[1].Annotations = ann(t, graphql.EnumValues(map[string]string{"monthly": "MONTH"}))
	if _, err := Render(m); err == nil || !strings.Contains(err.Error(), "its enum type BudgetPeriodType is also that of") {
		t.Errorf("renamed value: %v", err)
	}
}
