package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Budget's period_type and BudgetPeriod's type both make enum type
// BudgetPeriodType, with the same values in another order.
type Budget struct{ schema.Schema }

func (Budget) Fields() []schema.Field {
	return []schema.Field{field.Enum("period_type").Values("annual", "quarterly", "monthly")}
}

type BudgetPeriod struct{ schema.Schema }

func (BudgetPeriod) Fields() []schema.Field {
	return []schema.Field{field.Enum("type").Values("monthly", "quarterly", "annual")}
}

// seOrder's line_status and seOrderLine's status both make enum type
// seOrderLineStatus, with other values.
type seOrder struct{ schema.Schema }

func (seOrder) Fields() []schema.Field {
	return []schema.Field{field.Enum("line_status").Values("open", "closed")}
}

type seOrderLine struct{ schema.Schema }

func (seOrderLine) Fields() []schema.Field {
	return []schema.Field{field.Enum("status").Values("open", "shipped")}
}

// Two fields whose enum types get one name and hold the same values share
// one type, in the order of the schema first by name; with other values the
// names collide.
func TestSharedEnumType(t *testing.T) {
	o := mustLower(t, Config{}, Budget{}, BudgetPeriod{})
	var types []string
	for _, en := range o.Enums {
		types = append(types, en.Type)
	}
	if len(o.Enums) != 1 || o.Enums[0].Type != "BudgetPeriodType" || strings.Join(o.Enums[0].Values, " ") != "annual quarterly monthly" {
		t.Fatalf("enums %v: %+v", types, o.Enums)
	}
	for _, e := range o.Entities {
		for _, f := range e.Fields {
			if f.Name == "period_type" || f.Name == "type" {
				if f.ElemType != "BudgetPeriodType" {
					t.Errorf("%s.%s is %s", e.Name, f.Name, f.ElemType)
				}
			}
		}
	}
	_, err := lower(t, Config{}, seOrder{}, seOrderLine{})
	if err == nil || !strings.Contains(err.Error(), "enum type seOrderLineStatus collides with enum type seOrderLineStatus") {
		t.Errorf("other values: %v", err)
	}
}
