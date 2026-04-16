package gen

import (
	"fmt"

	"github.com/syssam/velox/compiler/load"
)

// resolve the type references and relations of its edges.
// It fails if one of the references is missing or invalid.
//
// Relation definitions between A and B, where A is the owner of
// the edge and B uses this edge as a back-reference:
//
//	O2O
//	 - A have a unique edge (E) to B, and B have a back-reference unique edge (E') for E.
//	 - A have a unique edge (E) to A.
//
//	O2M (The "Many" side, keeps a reference to the "One" side).
//	 - A have an edge (E) to B (not unique), and B doesn't have a back-reference edge for E.
//	 - A have an edge (E) to B (not unique), and B have a back-reference unique edge (E') for E.
//
//	M2O (The "Many" side, holds the reference to the "One" side).
//	 - A have a unique edge (E) to B, and B doesn't have a back-reference edge for E.
//	 - A have a unique edge (E) to B, and B have a back-reference non-unique edge (E') for E.
//
//	M2M
//	 - A have an edge (E) to B (not unique), and B have a back-reference non-unique edge (E') for E.
//	 - A have an edge (E) to A (not unique).
func (g *Graph) resolve(t *Type) error {
	for _, e := range t.Edges {
		switch {
		// --- Inverse edge resolution: link back-references and determine relation type ---
		case e.IsInverse():
			ref, ok := e.Type.HasAssoc(e.Inverse)
			if !ok {
				return NewEdgeError(t.Name, e.Type.Name, e.Name, fmt.Sprintf("edge %q is missing for inverse edge", e.Inverse), nil)
			}
			if !e.Optional && !ref.Optional {
				return NewEdgeError(t.Name, e.Type.Name, e.Name, fmt.Sprintf("edges cannot be required in both directions: %s.%s <-> %s.%s", t.Name, e.Name, e.Type.Name, ref.Name), nil)
			}
			if ref.Type != t {
				return NewEdgeError(t.Name, e.Type.Name, e.Name, fmt.Sprintf("mismatch type for back-ref %q", e.Inverse), nil)
			}
			e.Ref, ref.Ref = ref, e
			table := t.Table()
			// Name the foreign-key column in a format that wouldn't change even if an inverse
			// edge is dropped (or added). The format is: "<Edge-Owner>_<Edge-Name>".
			// Note: explicit FK field names (edge.Field()) are resolved later in setupFKs()
			// via setupFieldEdge(), which updates Rel.Columns after relation types are known.
			column := fmt.Sprintf("%s_%s", e.Type.Label(), snake(ref.Name))
			// --- Determine O2O/O2M/M2O/M2M based on uniqueness of both sides ---
			switch a, b := ref.Unique, e.Unique; {
			// If the relation column is in the inverse side/table. The rule is simple, if assoc is O2M,
			// then inverse is M2O and the relation is in its table.
			case a && b:
				e.Rel.Type, ref.Rel.Type = O2O, O2O
			case !a && b:
				e.Rel.Type, ref.Rel.Type = M2O, O2M

			// If the relation column is in the assoc side.
			case a && !b:
				e.Rel.Type, ref.Rel.Type = O2M, M2O
				table = e.Type.Table()

			case !a && !b:
				e.Rel.Type, ref.Rel.Type = M2M, M2M
				table = e.Type.Label() + "_" + ref.Name
				c1, c2 := ref.Owner.Label()+"_id", ref.Type.Label()+"_id"
				// If the relation is from the same type: User has Friends ([]User),
				// we give the second column a different name (the relation name).
				if c1 == c2 {
					c2 = rules.Singularize(e.Name) + "_id"
				}
				if c1 == c2 {
					c2 = e.Name + "_id"
				}
				// Both edges get their own copy of the columns to prevent
				// accidental cross-mutation through shared backing arrays.
				e.Rel.Columns = []string{c1, c2}
				ref.Rel.Columns = []string{c1, c2}
			}
			e.Rel.Table, ref.Rel.Table = table, table
			if !e.M2M() {
				e.Rel.Columns = []string{column}
				ref.Rel.Columns = []string{column}
			}
		// --- Unidirectional assoc edges: no inverse defined ---
		case !e.IsInverse() && e.Rel.Type == Unk:
			// Each case is self-contained: sets Type, Table, and Columns together.
			// For bidirectional edges, use edge.From(...).Ref(...) instead.
			column := fmt.Sprintf("%s_%s", t.Label(), snake(e.Name))
			switch {
			case !e.Unique && e.Type == t:
				e.Rel.Type = M2M
				e.Bidi = true
				e.Rel.Table = t.Label() + "_" + e.Name
				e.Rel.Columns = []string{e.Owner.Label() + "_id", rules.Singularize(e.Name) + "_id"}
			case e.Unique && e.Type == t:
				e.Rel.Type = O2O
				e.Bidi = true
				e.Rel.Table = t.Table()
				e.Rel.Columns = []string{column}
			case e.Unique:
				e.Rel.Type = M2O
				e.Rel.Table = t.Table()
				e.Rel.Columns = []string{column}
			default:
				e.Rel.Type = O2M
				e.Rel.Table = e.Type.Table()
				e.Rel.Columns = []string{column}
			}
		}
	}
	return nil
}

// edgeSchemas visits all edges in the graph and detects which schemas are used as "edge schemas".
// Note, edge schemas cannot be used by more than one association (edge.To), must define two required
// edges (+ edge-fields) to the types that go through them, and allow adding additional fields with
// optional default values.
func (g *Graph) edgeSchemas() error {
	for _, n := range g.Nodes {
		for _, e := range n.Edges {
			if e.def.Through == nil {
				continue
			}
			// --- Validate edge schema prerequisites ---
			if !e.M2M() {
				return fmt.Errorf("edge %s.%s Through(%q, %s.Type) is allowed only on M2M edges, but got: %q", n.Name, e.Name, e.def.Through.N, e.def.Through.T, e.Rel.Type)
			}
			edgeT, ok := g.typ(e.def.Through.T)
			switch {
			case !ok:
				return fmt.Errorf("edge %s.%s defined with Through(%q, %s.Type), but type %[4]s was not found", n.Name, e.Name, e.def.Through.N, e.def.Through.T)
			case edgeT == n:
				return fmt.Errorf("edge %s.%s defined with Through(%q, %s.Type), but edge cannot go through itself", n.Name, e.Name, e.def.Through.N, e.def.Through.T)
			case e.def.Through.N == "" || n.hasEdge(e.def.Through.N):
				return fmt.Errorf("edge %s.%s defined with Through(%q, %s.Type), but schema %[1]s already has an edge named %[3]s", n.Name, e.Name, e.def.Through.N, e.def.Through.T)
			case e.IsInverse():
				if edgeT.EdgeSchema.From != nil {
					return fmt.Errorf("type %s is already used as an edge-schema by other edge.From: %s.%s", edgeT.Name, edgeT.EdgeSchema.From.Name, edgeT.EdgeSchema.From.Owner.Name)
				}
				e.Through = edgeT
				edgeT.EdgeSchema.From = e
				if to, from := edgeT.EdgeSchema.To, edgeT.EdgeSchema.From; to != nil && from.Ref != to {
					return fmt.Errorf("mismatched edge.From(%q, %s.Type) and edge.To(%q, %s.Type) for edge schema %s", from.Name, from.Type.Name, to.Name, to.Type.Name, edgeT.Name)
				}
			default: // Assoc.
				if edgeT.EdgeSchema.To != nil {
					return fmt.Errorf("type %s is already used as an edge schema by other edge.To: %s.%s", edgeT.Name, edgeT.EdgeSchema.To.Name, edgeT.EdgeSchema.To.Owner.Name)
				}
				e.Through = edgeT
				edgeT.EdgeSchema.To = e
				if to, from := edgeT.EdgeSchema.To, edgeT.EdgeSchema.From; from != nil && from.Ref != to {
					return fmt.Errorf("mismatched edge.To(%q, %s.Type) and edge.From(%q, %s.Type) for edge schema %s", from.Name, from.Type.Name, to.Name, to.Type.Name, edgeT.Name)
				}
			}
			// --- Update relation tables and resolve FK columns for edge schema ---
			// Update both Assoc/To and Inverse/From
			// relation tables to the edge schema table.
			e.Rel.Table = edgeT.Table()
			if e.Ref != nil {
				e.Ref.Rel.Table = edgeT.Table()
			}
			var ref *Edge
			for i, c := range e.Rel.Columns {
				r, ok := resolveEdgeSchemaFK(edgeT, e, n, i, c)
				if !ok {
					return fmt.Errorf("missing edge-field %s.%s for edge schema used by %s.%s in Through(%q, %s.Type)", edgeT.Name, c, n.Name, e.Name, e.def.Through.N, edgeT.Name)
				}
				if r.Optional {
					return fmt.Errorf("edge-schema %s is missing a Required() attribute for its reference edge %q", edgeT.Name, e.Name)
				}
				if (!e.IsInverse() && i == 0) || (e.IsInverse() && i == 1) {
					ref = r
				}
			}
			// --- Create O2M edges from source/dest tables to join table ---
			// Edges from src/dest table are always O2M. One row to many
			// rows in the join table. Hence, a many-to-many relationship.
			n.Edges = append(n.Edges, &Edge{
				def:         &load.Edge{},
				Name:        e.def.Through.N,
				Type:        edgeT,
				Inverse:     ref.Name,
				Ref:         ref,
				Owner:       n,
				Optional:    true,
				StructTag:   structTag(e.def.Through.N, ""),
				Annotations: e.Annotations,
				Rel: Relation{
					Type:    O2M,
					fk:      ref.Rel.fk,
					Table:   ref.Rel.Table,
					Columns: ref.Rel.Columns,
				},
			})
			// --- Handle composite primary key for edge schema ---
			// Edge schema contains a composite primary key, and it was not resolved in previous iterations.
			if ant := fieldAnnotate(edgeT.Annotations); ant != nil && len(ant.ID) > 0 && len(edgeT.EdgeSchema.ID) == 0 {
				if len(e.Rel.Columns) < 2 {
					return fmt.Errorf("M2M edge %q on type %q has insufficient columns (got %d, need 2)", e.Name, n.Name, len(e.Rel.Columns))
				}
				r1, r2 := e.Rel.Columns[0], e.Rel.Columns[1]
				if len(ant.ID) != 2 || ant.ID[0] != r1 || ant.ID[1] != r2 {
					return fmt.Errorf(`edge schema primary key can only be defined on "id" or (%q, %q) in the same order`, r1, r2)
				}
				edgeT.ID = nil
				for _, f := range ant.ID {
					edgeT.EdgeSchema.ID = append(edgeT.EdgeSchema.ID, edgeT.fields[f])
				}
			}
			if edgeT.HasCompositeID() {
				continue
			}
			hasI := func() bool {
				// M2M edges require exactly 2 columns
				if len(e.Rel.Columns) < 2 {
					return false
				}
				for _, idx := range edgeT.Indexes {
					if !idx.Unique || len(idx.Columns) != 2 {
						continue
					}
					c1, c2 := idx.Columns[0], idx.Columns[1]
					r1, r2 := e.Rel.Columns[0], e.Rel.Columns[1]
					if (c1 == r1 && c2 == r2) || (c1 == r2 && c2 == r1) {
						return true
					}
				}
				return false
			}()
			if !hasI {
				if err := edgeT.AddIndex(&load.Index{Unique: true, Fields: e.Rel.Columns}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resolveEdgeSchemaFK resolves the foreign-key edge in an edge-schema that
// corresponds to the given column name. It first searches by edge-field name,
// then falls back to matching by edge-type if there's a single match.
func resolveEdgeSchemaFK(edgeT *Type, e *Edge, n *Type, colIdx int, colName string) (*Edge, bool) {
	// Search first for matching by edge-field.
	for _, fk := range edgeT.ForeignKeys {
		if fk.Field.Name == colName {
			return fk.Edge, true
		}
	}
	// In case of no match, search by edge-type. This can happen if the type (edge owner)
	// is named "T", but the edge-schema "E" names its edge field as "u_id". We consider
	// it as a match if there is only one usage of "T" in "E".
	matchOn := n
	if (colIdx == 0 && e.IsInverse()) || (colIdx == 1 && !e.IsInverse()) {
		matchOn = e.Type
	}
	var matches []*Edge
	for _, e2 := range edgeT.Edges {
		if e2.Type == matchOn && e2.Field() != nil {
			matches = append(matches, e2)
		}
	}
	if len(matches) == 1 {
		// Ensure the M2M foreign key is updated accordingly.
		e.Rel.Columns[colIdx] = matches[0].Field().Name
		return matches[0], true
	}
	return nil, false
}
