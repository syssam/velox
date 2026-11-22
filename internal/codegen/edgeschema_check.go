package codegen

import (
	"errors"
	"fmt"
	"slices"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// throughRead is the end of rule 70's reasons: why an edge schema cannot
// narrow the rows a read sees.
const throughRead = "Has and With through its many-to-many relation read its rows through the relation's two sides and would not apply it; put the condition in the two sides' policies"

// edgeSchemaBehaviour checks rule 70: an edge schema cannot narrow the rows
// a read sees, by a Read or Query policy, an interceptor or soft delete, its
// own or a mixin's. Has and With read its rows through its relation's two
// sides, so they would disagree with a query of its own. The engine refuses
// the relation when it resolves as well, for a Policy velox-gen cannot read.
func edgeSchemaBehaviour(g *Graph, bound map[string]Bound, mixins map[string][]MixinBound) error {
	var errs []error
	fail := func(e *Entity, pos, format string, args ...any) {
		errs = append(errs, &Error{Pos: pos, Schema: e.Name, Reason: fmt.Sprintf(format, args...)})
	}
	for _, e := range g.Entities {
		if !e.EdgeSchema {
			continue
		}
		soft := slices.ContainsFunc(e.Mixins, func(m loadjson.Mixin) bool {
			return m.PkgPath == softDeletePkg && m.Name == softDeleteMixin
		})
		if soft {
			fail(e, "", "an edge schema cannot list softdelete.Mixin: Remove would keep the link's row, which Has and With through its many-to-many relation would still read; delete links for good")
		}
		b := bound[e.Name]
		if b.ReadConds != "" {
			fail(e, b.ReadConds, "an edge schema's Policy cannot set Read or Query: %s", throughRead)
		}
		if b.Interceptors {
			fail(e, "", "an edge schema cannot have Interceptors: %s", throughRead)
		}
		for _, mb := range mixins[e.Name] {
			if soft && mb.PkgPath == softDeletePkg {
				continue
			}
			if mb.Reads != "" {
				fail(e, mb.Reads, "mixin %s's Policy sets Read or Query, which an edge schema cannot have: %s", mb.Name, throughRead)
			}
			if _, ok := mb.Methods["Interceptors"]; ok {
				fail(e, "", "mixin %s has Interceptors, which an edge schema cannot have: %s", mb.Name, throughRead)
			}
		}
	}
	return errors.Join(errs...)
}

// mysqlDrops is the end of the warning of an edge schema's other unique
// key: what Add does on MySQL.
const mysqlDrops = "on MySQL, Add's ON DUPLICATE KEY UPDATE applies to every unique key, so a new link whose row collides with another only on this key is dropped without an error: drop the key, or insert the edge schema's rows yourself"

// EdgeSchemaWarnings returns a warning for each edge schema neither of whose
// two key edges declares OnDelete: its keys have no action, as ent's do, so
// deleting a row that a link names fails while the link exists. A plain join
// table's keys cascade. It also warns of each unique field and unique index
// of an edge schema other than the one over its pair of keys, which MySQL's
// Add treats as the pair's. Each warning is "<pos>: schema X: reason", as an
// Error prints.
func EdgeSchemaWarnings(g *Graph) []string {
	var out []string
	for _, e := range g.Entities {
		if !e.EdgeSchema {
			continue
		}
		keys := linkKeys(g, e)
		out = append(out, otherUniques(e, keys)...)
		if len(keys) != 2 || keys[0].Rel.OnDelete != "" || keys[1].Rel.OnDelete != "" {
			continue
		}
		sides := keys[0].Target
		if keys[1].Target != sides {
			sides += " or " + keys[1].Target
		}
		out = append(out, (&Error{Pos: keys[0].Pos, Schema: e.Name, Reason: fmt.Sprintf(
			"deleting %s rows fails while %s rows name them; declare OnDelete(edge.Cascade) on edges %q and %q to delete their links with them",
			sides, e.Name, keys[0].Name, keys[1].Name)}).Error())
	}
	return out
}

// otherUniques are the warnings of the unique fields and indexes of the edge
// schema es, whose key edges are keys, but the ID and the index over the
// pair of keys. An edge schema with a column Add cannot fill gets none:
// its relations' Add is refused before any statement.
func otherUniques(es *Entity, keys []*Edge) []string {
	var pair []string
	for _, k := range keys {
		if i := fieldIndex(es, k.Field); i >= 0 {
			pair = append(pair, es.Fields[i].Column)
		}
	}
	if len(pair) == 2 && len(unfilled(es, columnIndex(es, pair[0]), columnIndex(es, pair[1]))) > 0 {
		return nil // Add is refused before any statement: it drops nothing
	}
	var out []string
	for _, f := range es.Fields[1:] {
		if f.Unique {
			out = append(out, (&Error{Pos: f.Pos, Schema: es.Name, Where: fieldAt(f.Name), Reason: "a unique field of an edge schema: " + mysqlDrops}).Error())
		}
	}
	for i, ix := range es.Indexes {
		if !ix.Unique || len(pair) == 2 && len(ix.Columns) == 2 && slices.Contains(ix.Columns, pair[0]) && slices.Contains(ix.Columns, pair[1]) && pair[0] != pair[1] {
			continue
		}
		out = append(out, (&Error{Pos: ix.Pos, Schema: es.Name, Where: indexAt(i), Reason: "a unique index of an edge schema other than its pair of keys': " + mysqlDrops}).Error())
	}
	return out
}

// linkKeys are the edges of the edge schema es that bind the columns of its
// links, in the order of the columns of the assoc edge of its relation.
func linkKeys(g *Graph, es *Entity) []*Edge {
	for _, o := range g.Entities {
		for _, ed := range o.Edges {
			if ed.Rel.EdgeSchema != es.Name || ed.Inverse {
				continue
			}
			var keys []*Edge
			for _, col := range ed.Rel.Columns {
				i := slices.IndexFunc(es.Edges, func(k *Edge) bool {
					f := fieldIndex(es, k.Field)
					return k.Rel.Kind == "M2O" && f >= 0 && es.Fields[f].Column == col
				})
				if i >= 0 {
					keys = append(keys, es.Edges[i])
				}
			}
			return keys
		}
	}
	return nil
}
