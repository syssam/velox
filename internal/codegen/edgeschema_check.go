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

// EdgeSchemaWarnings returns a warning for each edge schema neither of whose
// two key edges declares OnDelete: its keys have no action, as ent's do, so
// deleting a row that a link names fails while the link exists. A plain join
// table's keys cascade. Each warning is "<pos>: schema X: reason", as an
// Error prints.
func EdgeSchemaWarnings(g *Graph) []string {
	var out []string
	for _, e := range g.Entities {
		if !e.EdgeSchema {
			continue
		}
		keys := linkKeys(g, e)
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
