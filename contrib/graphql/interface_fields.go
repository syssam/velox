package graphql

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/syssam/velox/compiler/gen"
)

// interfaceFieldGroup is one GraphQL field produced by InterfaceField
// annotations on a type's edges.
type interfaceFieldGroup struct {
	// FieldName is the GraphQL field name (the annotation value).
	FieldName string
	// InterfaceName is the GraphQL interface the field is typed as. Empty
	// for a rename (single edge), where the field is typed as the target.
	InterfaceName string
	// Edges are the contributing edges, in declaration order.
	Edges []*gen.Edge
	// IsRename is true when a single edge carries the name.
	IsRename bool
}

// unique reports whether every contributing edge is to-one.
func (ifc *interfaceFieldGroup) unique() bool {
	for _, e := range ifc.Edges {
		if !e.Unique {
			return false
		}
	}
	return true
}

// allOwnFK reports whether every contributing edge stores its foreign key
// on this type. Then the populated edge — and the target's id — is known
// from the row alone, so a selection needing only __typename and id can be
// answered without querying the target table.
func (ifc *interfaceFieldGroup) allOwnFK() bool {
	for _, e := range ifc.Edges {
		if !e.OwnFK() {
			return false
		}
	}
	return len(ifc.Edges) > 0
}

// interfaceFieldGroups returns the interface fields of t, in the order the
// first contributing edge of each appears, considering only edges that are
// exposed in GraphQL. Returns an error for a group whose targets share no
// interface, or a name that collides with a field or edge of the type.
func (g *Generator) interfaceFieldGroups(t *gen.Type) ([]*interfaceFieldGroup, error) {
	groups := map[string]*interfaceFieldGroup{}
	var order []string
	for _, e := range g.filterEdges(t.Edges, SkipType) {
		name := g.getEdgeAnnotation(e).GetInterfaceField()
		if name == "" {
			continue
		}
		if groups[name] == nil {
			groups[name] = &interfaceFieldGroup{FieldName: name}
			order = append(order, name)
		}
		groups[name].Edges = append(groups[name].Edges, e)
	}
	if len(order) == 0 {
		return nil, nil
	}
	taken := map[string]bool{"id": true}
	for _, f := range g.filterFields(t.Fields, SkipType) {
		taken[g.graphqlFieldName(f)] = true
	}
	for _, e := range g.filterEdges(t.Edges, SkipType) {
		taken[camel(e.Name)] = true
	}
	// The annotation value also becomes a Go method on the entity struct
	// (pascal case), so it must not collide with a member velox already
	// emits there — otherwise the entity package fails to compile with an
	// error nowhere near the schema.
	reservedGo := map[string]bool{
		"ID": true, "Edges": true, "Config": true, "SetConfig": true,
		"String": true, "Unwrap": true, "Value": true, "AssignValues": true,
	}
	for _, f := range t.Fields {
		reservedGo[pascal(f.Name)] = true
	}
	for _, e := range t.Edges {
		reservedGo[pascal(e.Name)] = true
	}
	out := make([]*interfaceFieldGroup, 0, len(order))
	for _, name := range order {
		ifc := groups[name]
		if taken[name] {
			return nil, fmt.Errorf("graphql: %s: interface field %q collides with a field or edge of the same name", t.Name, name)
		}
		if reservedGo[pascal(name)] {
			return nil, fmt.Errorf("graphql: %s: interface field %q would generate the method %s, which already exists on the entity; choose another name",
				t.Name, name, pascal(name))
		}
		if len(ifc.Edges) == 1 {
			ifc.IsRename = true
		} else {
			iface, err := g.commonInterface(ifc.Edges)
			if err != nil {
				return nil, fmt.Errorf("graphql: %s: interface field %q: %w", t.Name, name, err)
			}
			ifc.InterfaceName = iface
		}
		out = append(out, ifc)
	}
	return out, nil
}

// commonInterface returns the GraphQL interface, other than Node, that the
// target types of all edges declare via Implements. With several candidates
// the lexically first is used, so the result is deterministic.
func (g *Generator) commonInterface(edges []*gen.Edge) (string, error) {
	if len(edges) == 0 {
		return "", fmt.Errorf("no edges")
	}
	candidates := map[string]bool{}
	for _, iface := range g.getTypeAnnotation(edges[0].Type).GetImplements() {
		candidates[iface] = true
	}
	for _, e := range edges[1:] {
		has := map[string]bool{}
		for _, iface := range g.getTypeAnnotation(e.Type).GetImplements() {
			has[iface] = true
		}
		for iface := range candidates {
			if !has[iface] {
				delete(candidates, iface)
			}
		}
	}
	delete(candidates, "Node")
	if len(candidates) == 0 {
		names := make([]string, 0, len(edges))
		for _, e := range edges {
			names = append(names, e.Type.Name)
		}
		return "", fmt.Errorf("target types %v share no GraphQL interface other than Node; declare one with graphql.Implements on each", names)
	}
	sorted := make([]string, 0, len(candidates))
	for iface := range candidates {
		sorted = append(sorted, iface)
	}
	sort.Strings(sorted)
	return sorted[0], nil
}

// hasFKFastPath reports whether the generated resolver for this interface
// field can answer a __typename/id-only selection from the foreign keys on
// t, without loading the target. That needs a polymorphic group (a rename
// resolves through the ordinary edge method), every edge to-one and owning
// its key, and a nullable key field for each — the same conditions
// genPolymorphicUniqueMethod emits the switch under. Both call this so the
// generated code and the collection metadata cannot disagree.
func (g *Generator) hasFKFastPath(t *gen.Type, ifc *interfaceFieldGroup) bool {
	if ifc.IsRename || !ifc.unique() || !ifc.allOwnFK() {
		return false
	}
	for _, e := range ifc.Edges {
		if fkPointerField(t, e) == "" {
			return false
		}
	}
	return true
}

// generatedInterface describes a GraphQL interface velox emits itself: one
// that InterfaceField renames make every implementor share.
type generatedInterface struct {
	Name string
	// Implementors are the GraphQL type names of the concrete nodes.
	Implementors []string
	// ImplementorTypes are the concrete nodes, for Go code generation.
	ImplementorTypes []*gen.Type
	// Fields are the argument-less fields every implementor exposes with the
	// same name and type, as "name: Type" SDL lines, sorted by name.
	Fields []string
	// Connection is true when some node exposes the interface through a
	// to-many polymorphic field, so <Name>Connection/<Name>Edge are needed.
	Connection bool
}

// generatedInterfaces returns the interfaces velox must define. An interface
// is generated only when every non-view, non-skipped implementor shares at
// least one InterfaceField rename — that is the signal the interface exists
// to unify those nodes; interfaces declared solely via Implements (for
// example a NamedNode the application defines in its own .graphql) are left
// to the application. The result is sorted by name.
func (g *Generator) generatedInterfaces() ([]*generatedInterface, error) {
	implementors := map[string][]*gen.Type{}
	renames := map[*gen.Type]map[string]bool{}
	connection := map[string]bool{}
	for _, n := range g.graph.Nodes {
		ann := g.getTypeAnnotation(n)
		if n.IsView() || ann.Skip.Is(SkipType) {
			continue
		}
		groups, err := g.interfaceFieldGroups(n)
		if err != nil {
			return nil, err
		}
		renames[n] = map[string]bool{}
		for _, ifc := range groups {
			if ifc.IsRename {
				renames[n][ifc.FieldName] = true
			} else if !ifc.unique() {
				connection[ifc.InterfaceName] = true
			}
		}
		for _, iface := range ann.GetImplements() {
			if iface != "Node" {
				implementors[iface] = append(implementors[iface], n)
			}
		}
	}
	var out []*generatedInterface
	for iface, impls := range implementors {
		shared := map[string]bool{}
		for name := range renames[impls[0]] {
			shared[name] = true
		}
		for _, impl := range impls[1:] {
			for name := range shared {
				if !renames[impl][name] {
					delete(shared, name)
				}
			}
		}
		if len(shared) == 0 {
			continue
		}
		fields, err := g.sharedInterfaceFields(impls)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			continue
		}
		gi := &generatedInterface{Name: iface, Fields: fields, Connection: connection[iface]}
		sort.Slice(impls, func(i, j int) bool { return impls[i].Name < impls[j].Name })
		for _, impl := range impls {
			gi.Implementors = append(gi.Implementors, g.graphqlTypeName(impl))
			gi.ImplementorTypes = append(gi.ImplementorTypes, impl)
		}
		out = append(out, gi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// sdlFieldSig is the GraphQL signature of one field of an object type.
type sdlFieldSig struct {
	Name    string
	Type    string // including nullability, e.g. "Category" or "[Tag!]!"
	HasArgs bool
}

// entityFieldSignatures lists the fields genEntityType exposes on t, in the
// same order, as name/type signatures. Interface fields are included.
func (g *Generator) entityFieldSignatures(t *gen.Type) ([]sdlFieldSig, error) {
	sigs := []sdlFieldSig{{Name: "id", Type: "ID!"}}
	for _, f := range g.filterFields(t.Fields, SkipType) {
		if g.isEdgeFKField(t, f) && g.shouldSkipEdgeFKField(t, f) {
			continue
		}
		typ := g.graphqlFieldType(t, f)
		if !f.Optional && !f.Nillable {
			typ += "!"
		}
		sigs = append(sigs, sdlFieldSig{Name: g.graphqlFieldName(f), Type: typ})
	}
	for _, e := range g.filterEdges(t.Edges, SkipType) {
		sigs = append(sigs, g.edgeFieldSignature(e))
	}
	groups, err := g.interfaceFieldGroups(t)
	if err != nil {
		return nil, err
	}
	for _, ifc := range groups {
		sigs = append(sigs, g.interfaceFieldSignature(ifc))
	}
	return sigs, nil
}

// edgeFieldSignature mirrors genEdgeField's type choice for an edge.
func (g *Generator) edgeFieldSignature(e *gen.Edge) sdlFieldSig {
	target := g.graphqlTypeName(e.Type)
	switch {
	case e.Unique && !e.Optional:
		return sdlFieldSig{Name: camel(e.Name), Type: target + "!"}
	case e.Unique:
		return sdlFieldSig{Name: camel(e.Name), Type: target}
	case g.config.RelayConnection && g.hasRelayConnection(e.Type):
		return sdlFieldSig{Name: camel(e.Name), Type: target + "Connection!", HasArgs: true}
	default:
		return sdlFieldSig{Name: camel(e.Name), Type: "[" + target + "!]!"}
	}
}

// interfaceFieldSignature returns the SDL signature of an interface field:
// rename → the target (list for a to-many edge); polymorphic to-one → the
// interface; polymorphic to-many → the interface connection with pagination
// arguments (no orderBy/where: neither is well-defined across members).
func (g *Generator) interfaceFieldSignature(ifc *interfaceFieldGroup) sdlFieldSig {
	if ifc.IsRename {
		e := ifc.Edges[0]
		target := g.graphqlTypeName(e.Type)
		if e.Unique {
			return sdlFieldSig{Name: ifc.FieldName, Type: target}
		}
		return sdlFieldSig{Name: ifc.FieldName, Type: "[" + target + "!]!"}
	}
	if ifc.unique() {
		return sdlFieldSig{Name: ifc.FieldName, Type: ifc.InterfaceName}
	}
	return sdlFieldSig{Name: ifc.FieldName, Type: ifc.InterfaceName + "Connection!", HasArgs: true}
}

// sharedInterfaceFields intersects the implementors' argument-less fields:
// a field is shared when every implementor exposes it with the same name
// and type. Returned as sorted "name: Type" lines.
func (g *Generator) sharedInterfaceFields(impls []*gen.Type) ([]string, error) {
	first, err := g.entityFieldSignatures(impls[0])
	if err != nil {
		return nil, err
	}
	shared := map[string]string{}
	for _, s := range first {
		if !s.HasArgs {
			shared[s.Name] = s.Type
		}
	}
	for _, impl := range impls[1:] {
		sigs, err := g.entityFieldSignatures(impl)
		if err != nil {
			return nil, err
		}
		have := map[string]sdlFieldSig{}
		for _, s := range sigs {
			have[s.Name] = s
		}
		for name, typ := range shared {
			s, ok := have[name]
			if !ok || s.HasArgs || s.Type != typ {
				delete(shared, name)
			}
		}
	}
	names := make([]string, 0, len(shared))
	for name := range shared {
		names = append(names, name)
	}
	slices.Sort(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+shared[name])
	}
	return lines, nil
}

// interfaceFieldSDL renders the SDL lines for t's interface fields.
func (g *Generator) interfaceFieldSDL(t *gen.Type) (string, error) {
	groups, err := g.interfaceFieldGroups(t)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, ifc := range groups {
		sig := g.interfaceFieldSignature(ifc)
		if sig.HasArgs {
			fmt.Fprintf(&b, "  %s%s: %s\n", sig.Name, g.genConnectionArgs(ifc.InterfaceName, "", false, false), sig.Type)
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", sig.Name, sig.Type)
	}
	return b.String(), nil
}

// genInterfaceDefsSchema renders the interfaces velox generates (see
// generatedInterfaces) and, for those exposed through a to-many polymorphic
// field, their Connection/Edge types. Each interface is bound with
// @goModel to the Go interface emitted in the entity package so gqlgen
// resolves concrete types through the marker methods.
func (g *Generator) genInterfaceDefsSchema() (string, error) {
	ifaces, err := g.generatedInterfaces()
	if err != nil {
		return "", err
	}
	if len(ifaces) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	for _, gi := range ifaces {
		buf.WriteString(sdlDescription(gi.Name+" is implemented by "+strings.Join(gi.Implementors, ", ")+".", ""))
		if g.config.ORMPackage != "" {
			fmt.Fprintf(&buf, "interface %s @goModel(model: \"%s/entity.%s\") {\n", gi.Name, g.config.ORMPackage, gi.Name)
		} else {
			fmt.Fprintf(&buf, "interface %s {\n", gi.Name)
		}
		for _, f := range gi.Fields {
			fmt.Fprintf(&buf, "  %s\n", f)
		}
		buf.WriteString("}\n")
		if gi.Connection && g.config.RelayConnection {
			g.writeConnectionEdgeTypes(&buf, gi.Name)
		}
	}
	return buf.String(), nil
}

// validateInterfaceFields runs the InterfaceField checks up front so a bad
// schema fails generation with the reason instead of producing SDL that
// gqlgen rejects later.
func (g *Generator) validateInterfaceFields() error {
	for _, n := range g.graph.Nodes {
		if _, err := g.interfaceFieldGroups(n); err != nil {
			return err
		}
	}
	ifaces, err := g.generatedInterfaces()
	if err != nil {
		return err
	}
	generated := make(map[string]bool, len(ifaces))
	for _, gi := range ifaces {
		generated[gi.Name] = true
	}
	// A to-many group renders as <Interface>Connection, and velox emits that
	// type only alongside an interface it generates itself. Over an
	// application-declared interface the SDL would reference a type nothing
	// defines, and gqlgen would fail with a message that does not mention
	// the annotation.
	for _, n := range g.graph.Nodes {
		groups, err := g.interfaceFieldGroups(n)
		if err != nil {
			return err
		}
		for _, ifc := range groups {
			if ifc.IsRename || ifc.unique() || generated[ifc.InterfaceName] {
				continue
			}
			return fmt.Errorf(
				"graphql: %s: interface field %q groups to-many edges under %[3]q, which velox does not define: it would emit %[3]sConnection with no %[3]s type. "+
					"Give every implementor of %[3]q a shared graphql.InterfaceField rename so velox generates the interface, or group to-one edges instead",
				n.Name, ifc.FieldName, ifc.InterfaceName)
		}
	}
	return nil
}
