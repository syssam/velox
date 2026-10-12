package orm

import (
	"fmt"
)

// This file is what a request-driven layer (GraphQL, or a REST handler that
// takes a filter object) needs on top of queries: predicates and order terms
// made from input values. Nothing here imports a GraphQL engine.

// opInvalid is a predicate that could not be built; Val is the error the
// query or mutation reports.
const opInvalid Op = 254

// Invalid returns a predicate that makes the query or mutation it is given
// to fail with err: how a condition that could not be built from its input
// reports that, where the builder's chain has no place for an error.
func Invalid(err error) P { return P{Op: opInvalid, Val: err} }

// OrderTerm returns the order term of an order input: columns maps the
// values of the entity's OrderField enum to columns, field is the value the
// request gave ("" orders by the id).
func OrderTerm(m *Meta, columns map[string]string, field string, desc bool) O {
	o := O{Desc: desc}
	switch col, ok := columns[field]; {
	case field == "":
		if id := m.id(); id != nil {
			o.Col = id.Column
		}
	case ok:
		o.Col = col
		if f := m.Field(col); f != nil && f.Sensitive {
			o.Col, o.err = "", fmt.Errorf("velox: %s cannot be ordered by %q: the field is sensitive", m.Name, field)
		}
	default:
		// Not one of the fields the entity is ordered by. The name is not
		// tried as a column: that would order by, and put into the cursors
		// of a page, a column the schema did not offer.
		o.err = fmt.Errorf("velox: %s cannot be ordered by %q", m.Name, field)
	}
	return o
}
