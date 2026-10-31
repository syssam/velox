package sdl

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql"
)

// order is an entity's order: input XOrder and enum XOrderField.
type order struct {
	values []string // the XOrderField values, in the model's field order
	fields []int    // the index in the entity's Fields of each value's field
	wheres []string // the field of each value, for errors
}

// orderOf computes o's order from its fields' OrderField annotations: none
// when no field has one, or when the entity skips its order.
func (g *graph) orderOf(o *object) error {
	e := o.ent
	var ord order
	by := map[string]string{} // value → the field
	for i, f := range e.Fields {
		a := o.fanns[i]
		if a.OrderField == "" || a.Skip&graphql.SkipOrderField != 0 || o.ann.Skip&graphql.SkipOrderField != 0 {
			continue
		}
		where := "schema " + e.Name + ", field " + f.Name
		if !validEnumValue(a.OrderField) {
			return fmt.Errorf("%s: graphql.OrderField(%q) is not a GraphQL enum value", where, a.OrderField)
		}
		if prev, ok := by[a.OrderField]; ok {
			return fmt.Errorf("%s and %s both have graphql.OrderField(%q)", prev, where, a.OrderField)
		}
		by[a.OrderField] = where
		ord.values = append(ord.values, a.OrderField)
		ord.fields = append(ord.fields, i)
		ord.wheres = append(ord.wheres, where)
	}
	if len(ord.values) == 0 {
		return nil
	}
	where := "schema " + e.Name
	for _, n := range []string{o.name + "Order", o.name + "OrderField"} {
		if err := g.types.add(n, where); err != nil {
			return err
		}
	}
	o.order = &ord
	return nil
}

// orderArg is the orderBy argument of a connection to t's nodes, "" when t
// has no order.
func orderArg(t *object) string {
	switch {
	case t.order == nil:
		return ""
	case t.ann.MultiOrder:
		return ", orderBy: [" + t.name + "Order!]"
	default:
		return ", orderBy: " + t.name + "Order"
	}
}

func (w *writer) order(o *object) {
	w.line()
	w.line(`"""Ordering options for connections to `, o.name, `."""`)
	w.line("input ", o.name, "Order {")
	w.line(`  """The direction of the order."""`)
	w.line("  direction: OrderDirection! = ASC")
	w.line(`  """The field to order by."""`)
	w.line("  field: ", o.name, "OrderField!")
	w.line("}")
	w.line()
	w.line(`"""The fields connections to `, o.name, ` can be ordered by."""`)
	w.line("enum ", o.name, "OrderField {")
	for i, v := range o.order.values {
		w.at = o.order.wheres[i]
		w.line("  ", v)
	}
	w.at = ""
	w.line("}")
}
