package sdl

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// order is an entity's order: input XOrder and enum XOrderField.
type order struct {
	values  []string         // the XOrderField values, in the model's field order, then the edges'
	fields  []int            // the index of each value's field, in the entity's Fields, or its edge target's
	edges   []int            // the index in the entity's Edges of the edge the field is the target's by, plus one; 0 for the entity's own
	targets []*plugin.Entity // the target entity of each value's edge; nil for the entity's own
	wheres  []string         // the field of each value, for errors
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
		ord.edges = append(ord.edges, 0)
		ord.targets = append(ord.targets, nil)
		ord.wheres = append(ord.wheres, where)
	}
	if o.ann.Skip&graphql.SkipOrderField == 0 {
		if err := g.orderVia(o, &ord, by); err != nil {
			return err
		}
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
		if k := o.order.edges[i] - 1; k >= 0 {
			// The connection joins the edge with an inner join (0031), so a client is told
			// which nodes such an order leaves out, of the page and of totalCount.
			edge, _ := o.edgeName(k) // checked as the type's fields were made
			w.line(`  """Orders through `, edge, `: leaves out each `, o.name, ` without one, or whose `, edge, ` the reader may not see."""`)
		}
		w.line("  ", v)
	}
	w.at = ""
	w.line("}")
}

// orderVia adds to ord the values that order by a column of a to-one edge's
// target (graphql.OrderVia). A connection reads it with a JOIN, and a cursor
// holds the column's value, so everything checkField asks of an OrderField is
// asked here of the target's field, and of the edge, which a directive can guard.
func (g *graph) orderVia(o *object, ord *order, by map[string]string) error {
	e := o.ent
	for i, ed := range e.Edges {
		ea := o.eanns[i]
		if len(ea.OrderVia) == 0 {
			continue
		}
		ew := "schema " + e.Name + ", edge " + ed.Name
		t := g.byName[ed.Target]
		switch {
		case !ed.Unique || ed.Join != "":
			return fmt.Errorf("%s: graphql.OrderVia on a to-many edge: a join would read an owner once for each row, and a cursor needs one value", ew)
		case ea.Skip&graphql.SkipType != 0:
			return fmt.Errorf("%s: graphql.OrderVia on an edge graphql.Skip leaves out of the type: its target's values would be in the cursors", ew)
		case t == nil:
			return fmt.Errorf("%s: graphql.OrderVia on an edge to %s, which has no GraphQL type", ew, ed.Target)
		case appDirective(ea) != "":
			return fmt.Errorf("%s: graphql.OrderVia on an edge with the directive @%s: the order would reveal its target's values, which no directive guards; remove the directive or the OrderVia", ew, appDirective(ea))
		}
		for _, v := range ea.OrderVia {
			where := ew + ", graphql.OrderVia(" + fmt.Sprintf("%q, %q", v.Field, v.Value) + ")"
			fi := -1
			for k, f := range t.ent.Fields {
				if f.Name == v.Field {
					fi = k
				}
			}
			if fi < 0 {
				return fmt.Errorf("%s: %s has no field %s", where, t.ent.Name, v.Field)
			}
			if err := viaCheck(t.ent.Fields[fi], t.fanns[fi], where); err != nil {
				return err
			}
			if !validEnumValue(v.Value) {
				return fmt.Errorf("%s: %q is not a GraphQL enum value", where, v.Value)
			}
			if prev, ok := by[v.Value]; ok {
				return fmt.Errorf("%s and %s both order by %q", prev, where, v.Value)
			}
			by[v.Value] = where
			ord.values = append(ord.values, v.Value)
			ord.fields = append(ord.fields, fi)
			ord.edges = append(ord.edges, i+1)
			ord.targets = append(ord.targets, t.ent)
			ord.wheres = append(ord.wheres, where)
		}
	}
	return nil
}

// viaCheck refuses a field of an edge's target that a cursor cannot hold or a
// connection must not order by, as checkField does for an OrderField. A
// nullable field orders as an OrderField's does: NULL after every value.
func viaCheck(f *plugin.Field, a graphql.Annotation, where string) error {
	switch {
	case f.Sensitive:
		return fmt.Errorf("%s: the field is sensitive: its values would be in the cursors", where)
	case f.Type == "json":
		return fmt.Errorf("%s: the field is json, which has no order", where)
	case f.Type == "bytes":
		return fmt.Errorf("%s: the field is bytes: keyset paging cannot continue after a bytes value", where)
	case a.Skip&graphql.SkipType != 0:
		return fmt.Errorf("%s: the field is one graphql.Skip leaves out of the type: its values would be in the cursors", where)
	case !a.OrderUnguarded && appDirective(a) != "":
		return fmt.Errorf("%s: the field has the directive @%s: its values would be in the cursors and its order would reveal them, which no directive guards; add graphql.OrderUnguarded() to the field to allow it", where, appDirective(a))
	}
	return nil
}
