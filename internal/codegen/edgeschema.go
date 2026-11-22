package codegen

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/plugin"
)

// edgeSchemas resolves the many-to-many relations an edge names an edge
// schema for with Through (step 3b), as ent's edgeSchemas does: the edge
// schema's table holds the relation, by the fields its two Required to-one
// edges bind; each edge that declares Through gains a to-many edge to the
// edge schema; and the edge schema's pair of keys is unique. It checks rules
// 63 to 69.
func (b *builder) edgeSchemas() {
	var assocs []*edgeState // one edge per relation: its assoc edge
	for _, n := range b.nodes {
		for _, s := range n.edges {
			switch {
			case s.le.Through == nil || s.ed.Rel.Kind == "":
				continue // no Through, or unresolved: an error is recorded
			case s.ed.Rel.Kind != "M2M":
				b.fail(63, n.ent.Name, edgeLoc(s.ed), "Through is allowed only on a many-to-many edge; this edge is %s", s.ed.Rel.Kind)
				continue
			case s.ed.Bidi:
				b.fail(69, n.ent.Name, edgeLoc(s.ed), "Through on a bidirectional edge (an M2M to its own schema with no inverse) is not supported; give it an inverse edge, or drop Through")
				continue
			}
			a := s
			if s.le.Inverse {
				a = s.pair
			}
			if !slices.Contains(assocs, a) {
				assocs = append(assocs, a)
			}
		}
	}
	claimed := map[*node]*edgeState{}
	for _, a := range assocs {
		b.edgeSchema(a, claimed)
	}
}

// edgeSchema resolves the relation of the assoc edge a through the edge
// schema its Through, or its inverse's, names. claimed holds the edge that
// first named each edge schema.
func (b *builder) edgeSchema(a *edgeState, claimed map[*node]*edgeState) {
	p := a.pair
	decl := a // the edge whose Through names the edge schema
	if a.le.Through == nil {
		decl = p
	}
	es := b.throughNode(decl)
	if es == nil {
		return
	}
	if p != nil && decl == a && p.le.Through != nil && (p.le.Through.Type != a.le.Through.Type || p.le.Through.TypePkg != a.le.Through.TypePkg) {
		b.fail(66, p.owner.ent.Name, edgeLoc(p.ed), "%s is already the edge schema of %s.%s", es.ent.Name, a.owner.ent.Name, a.ed.Name)
		return
	}
	if first := claimed[es]; first != nil {
		b.fail(66, decl.owner.ent.Name, edgeLoc(decl.ed), "%s is already the edge schema of %s.%s", es.ent.Name, first.owner.ent.Name, first.ed.Name)
		return
	}
	claimed[es] = decl
	keyed := false
	for _, s := range []*edgeState{a, p} {
		if s != nil && (hasKey(s.le) || len(s.le.Symbols) > 0) {
			b.fail(68, s.owner.ent.Name, edgeLoc(s.ed), "Through takes its table and columns from %s; remove StorageKey (edge.Table, edge.Columns, edge.Symbols)", es.ent.Name)
			keyed = true
		}
	}
	if keyed {
		return
	}
	if c := a.ed.Rel.Columns; c[0] == c[1] {
		return // rule 35
	}
	// The relation's default columns, the assoc edge's owner's and then its
	// target's, name the edge schema's fields.
	sides := [2]*node{a.owner, a.target}
	var keys [2]*edgeState
	var cols [2]string
	for i, col := range a.ed.Rel.Columns {
		k := keyEdge(es, sides[i], col, sides[0] == sides[1])
		if k == nil {
			b.fail(67, decl.owner.ent.Name, edgeLoc(decl.ed), "%s", noKeyEdge(es, sides[i], col))
			return
		}
		if why := badKeyEdge(es, k); why != "" {
			b.fail(67, decl.owner.ent.Name, edgeLoc(decl.ed), "%s", why)
			return
		}
		keys[i], cols[i] = k, k.le.Field
		if f := es.declared(k.le.Field); f != nil {
			cols[i] = f.Column
		}
	}
	es.ent.EdgeSchema, es.link, es.linkPos = true, cols[:], decl.ed.Pos
	a.ed.Rel.Table, a.ed.Rel.Columns, a.ed.Rel.EdgeSchema = es.ent.Table, []string{cols[0], cols[1]}, es.ent.Name
	if p != nil {
		p.ed.Rel.Table, p.ed.Rel.Columns, p.ed.Rel.EdgeSchema = es.ent.Table, []string{cols[1], cols[0]}, es.ent.Name
	}
	// Only an edge that declares Through gains a through edge, as in ent.
	if a.le.Through != nil {
		b.throughEdge(a, es, keys[0], cols[0])
	}
	if p != nil && p.le.Through != nil {
		b.throughEdge(p, es, keys[1], cols[1])
	}
}

// noKeyEdge is rule 67's reason when the edge schema es has no edge to side
// that binds the field named col. It suggests the edge to declare, or, when
// es has an edge of that name already, the field to bind on it.
func noKeyEdge(es, side *node, col string) string {
	reason := fmt.Sprintf("edge schema %s has no edge to %s bound to field %q; ", es.ent.Name, side.ent.Name, col)
	name := cmp.Or(strings.TrimSuffix(col, "_id"), side.ent.Label)
	if es.edge(name) != nil {
		return reason + fmt.Sprintf("bind field %q on edge %q", col, name)
	}
	return reason + fmt.Sprintf("declare edge.To(%q, %s.Type).Unique().Required().Field(%q)", name, side.ent.Name, col)
}

// badKeyEdge is rule 67's reason when k, the key edge of the edge schema es,
// cannot hold one end of a link: it must be Required, and many-to-one, as a
// row of its side has many links. It is "" when k can.
func badKeyEdge(es *node, k *edgeState) string {
	at := ""
	if k.ed.Pos != "" {
		at = " (" + k.ed.Pos + ")"
	}
	switch {
	case !k.le.Required:
		return fmt.Sprintf("edge %q of %s%s must be Required: a link has both ends", k.ed.Name, es.ent.Name, at)
	case k.ed.Rel.Kind == "O2O" && k.pair != nil:
		return fmt.Sprintf("edge %q of %s%s must be many-to-one: a %s has many links; remove Unique from %s.%s",
			k.ed.Name, es.ent.Name, at, k.target.ent.Name, k.pair.owner.ent.Name, k.pair.ed.Name)
	}
	return ""
}

// throughNode returns the node of the edge schema that the Through of s
// names, or reports rule 64 and returns nil.
func (b *builder) throughNode(s *edgeState) *node {
	th := s.le.Through
	var es *node
	if th.TypePkg == b.pkg {
		es = b.node(th.Type)
	}
	switch es {
	case nil:
		t := th.Type
		if th.TypePkg != b.pkg && th.TypePkg != "" {
			t = path.Base(th.TypePkg) + "." + t
		}
		b.fail(64, s.owner.ent.Name, edgeLoc(s.ed), "Through names %s, which is not a schema of this package", t)
	case s.owner:
		b.fail(64, s.owner.ent.Name, edgeLoc(s.ed), "an edge cannot go through its own schema")
		es = nil
	case s.target:
		b.fail(64, s.owner.ent.Name, edgeLoc(s.ed), "an edge cannot go through its target's schema")
		es = nil
	}
	return es
}

// keyEdge returns the edge of the edge schema es to side that binds the
// field named col; failing that, unless the relation is to its own schema,
// the one edge of es to side that binds a field. It returns nil when none
// decides.
func keyEdge(es, side *node, col string, self bool) *edgeState {
	for _, s := range es.edges {
		if s.target == side && s.le.Field == col {
			return s
		}
	}
	if self {
		return nil // both edges point to side: only a field's name tells them apart
	}
	var found *edgeState
	for _, s := range es.edges {
		if s.target == side && s.le.Field != "" {
			if found != nil {
				return nil
			}
			found = s
		}
	}
	return found
}

// throughEdge adds to the owner of s, an edge that declares Through, the
// to-many edge to the edge schema es that its Through names, paired with
// key, the edge of es that binds col, the owner's column (rule 65).
func (b *builder) throughEdge(s *edgeState, es *node, key *edgeState, col string) {
	n, th := s.owner, s.le.Through
	if th.Name == "" {
		b.fail(65, n.ent.Name, edgeLoc(s.ed), "Through needs a name")
		return
	}
	goName := plugin.GoName(th.Name)
	for _, ed := range n.ent.Edges {
		if ed.Name == th.Name {
			b.fail(65, n.ent.Name, edgeLoc(s.ed), "Through's name %q is taken by edge %q of %s", th.Name, ed.Name, n.ent.Name)
			return
		}
	}
	for _, f := range n.ent.Fields {
		if f.Name == th.Name {
			b.fail(65, n.ent.Name, edgeLoc(s.ed), "Through's name %q is taken by field %q of %s", th.Name, f.Name, n.ent.Name)
			return
		}
	}
	for _, ed := range n.ent.Edges {
		if ed.GoName == goName {
			b.fail(65, n.ent.Name, edgeLoc(s.ed), "Through's name %q has the Go name %s of edge %q of %s", th.Name, goName, ed.Name, n.ent.Name)
			return
		}
	}
	for _, f := range n.ent.Fields {
		if f.GoName == goName {
			b.fail(65, n.ent.Name, edgeLoc(s.ed), "Through's name %q has the Go name %s of field %q of %s", th.Name, goName, f.Name, n.ent.Name)
			return
		}
	}
	le := &loadjson.Edge{Name: th.Name, Type: es.ent.Name, TypePkg: b.pkg, Inverse: true, Ref: key.ed.Name, Pos: s.le.Pos}
	ed := &Edge{
		Name: th.Name, GoName: goName, Target: es.ent.Name, Inverse: true, Ref: key.ed.Name, Field: key.le.Field,
		Rel: Rel{Kind: "O2M", Table: es.ent.Table, Columns: []string{col}}, Pos: s.ed.Pos,
	}
	// Its pair is key, one way: key keeps the inverse it may declare, and
	// gives the edge its action on delete.
	n.ent.Edges = append(n.ent.Edges, ed)
	n.edges = append(n.edges, &edgeState{ed: ed, le: le, owner: n, target: es, pair: key})
	s.ed.Through = th.Name
}

// linkIndex adds to the edge schema n the unique index of its pair of keys,
// as ent does, unless it declares one: unique, over every row, on the two
// columns in either order. Add's ON CONFLICT names it.
//
// A declared index over every row on the two columns in their order that is
// not unique is refused (rule 67): it would stand where the pair's index
// goes, and its name is the one the pair's index would take.
func (b *builder) linkIndex(n *node) {
	e, link := n.ent, n.link
	for i, ix := range e.Indexes {
		if b.refused[ix] || ix.Where != "" || len(ix.Columns) != 2 {
			continue
		}
		same := slices.Equal(ix.Columns, link)
		switch {
		case ix.Unique && (same || ix.Columns[0] == link[1] && ix.Columns[1] == link[0]):
			return
		case same:
			b.fail(67, e.Name, indexLoc(i, ix), "over (%s) must be Unique: Through's links are unique pairs", strings.Join(link, ", "))
			b.refused[ix] = true
			return
		}
	}
	e.Indexes = append(e.Indexes, &Index{Name: indexName(e.Name, link), Derived: true, Columns: slices.Clone(link), Unique: true, Pos: n.linkPos})
}
