package gqlrelay

import (
	"context"
	"encoding/json"

	"github.com/syssam/velox/runtime"
)

// MetaCollectable is a query builder that knows its own GraphQL collection
// metadata. The generated query types implement it (in query/gql_collection.go),
// which is how the collector recurses into an eager-loaded edge's query with
// the right entity's metadata without importing that entity's package.
type MetaCollectable interface {
	runtime.FieldCollectable
	// CollectMeta returns the entity's GraphQL field collection metadata.
	CollectMeta() *runtime.CollectMeta
}

// CollectFields configures q from the GraphQL selection of the field being
// resolved in ctx: it projects the columns the selection reads and
// eager-loads the edges it traverses, recursively, so a nested selection
// costs one query per edge instead of one per parent row. It is a no-op
// outside a resolver: the selection comes from gqlgen, or from the engine a
// WithSelectionSource context names.
//
// The generated (*XxxQuery).CollectFields method calls it; call that from a
// resolver that returns entities directly (a list or single node). Paginate
// collects on its own, from the connection's edges.node selection.
//
// Projection is skipped (SELECT * is kept) when the selection contains a
// field the metadata cannot map to columns — a custom resolver without a
// graphql.CollectedFor annotation might read any column.
func CollectFields(ctx context.Context, q runtime.FieldCollectable, meta *runtime.CollectMeta, satisfies ...string) error {
	fc, ok := selectedField(ctx)
	if !ok || meta == nil {
		return nil
	}
	collect(q, meta, []occurrence{{field: fc, satisfies: satisfies}})
	return nil
}

// CollectConnectionFields is CollectFields for a Relay connection field: it
// configures q from the edges { node { ... } } selection of the connection
// being resolved in ctx. The generated Paginate methods call it before
// running the page query, as Ent's do.
func CollectConnectionFields(ctx context.Context, q runtime.FieldCollectable, meta *runtime.CollectMeta) error {
	fc, ok := selectedField(ctx)
	if !ok || meta == nil {
		return nil
	}
	nodes, _ := connectionSelection(fc)
	if len(nodes) == 0 {
		// No node is read (totalCount/pageInfo only): the key is enough.
		q.GetCtx().AppendFieldOnce(q.GetIDColumn())
		return nil
	}
	collect(q, meta, occurrences(nodes, nil))
	return nil
}

// TotalCountSelected reports whether the Relay connection being resolved in
// ctx needs its total count. Generated Paginate methods skip their COUNT
// query when it returns false, as Ent's do (hasCollectedField(totalCount)).
// pageInfo never needs the count: hasNextPage and hasPreviousPage come from
// fetching one row past the page.
//
// Outside a resolver it returns true — a direct Paginate call has no
// selection to consult. A resolver of another engine is one only when its
// context carries WithSelectionSource. Inside one it reads the selection of the field
// being resolved, so a resolver that calls Paginate for a connection nested
// in its own result type (not the field it resolves) gets no count; resolve
// such a connection in its own field resolver.
func TotalCountSelected(ctx context.Context) bool {
	fc, ok := selectedField(ctx)
	if !ok {
		return true
	}
	_, use := connectionSelection(fc)
	return use.totalCount
}

// occurrence is one selection of an entity: a field (or a connection's
// node) and the type conditions its fragments are collected under.
type occurrence struct {
	field     SelectedField
	satisfies []string
}

func occurrences(fields []SelectedField, satisfies []string) []occurrence {
	out := make([]occurrence, len(fields))
	for i, f := range fields {
		out[i] = occurrence{field: f, satisfies: satisfies}
	}
	return out
}

// edgeSelection gathers every path to one edge in a selection set — aliases
// and fragments select the same edge more than once, and an interface field
// (graphql.InterfaceField) reaches the edge under another name. A query
// loads an edge once, into one child query, so every path's needs are
// merged into it: the child's projection is the union of all of them.
type edgeSelection struct {
	meta runtime.EdgeMeta
	// fields are the direct selections of the edge.
	fields []SelectedField
	// viaInterface are the selections of interface fields the edge backs.
	// Their resolver answers from the loaded edge, so it is loaded whole
	// (no per-parent limit) and projected for these selections too.
	viaInterface []occurrence
}

// collect projects q onto what the given fields select and schedules the
// edges they traverse. Each field is one occurrence of the same entity in
// the selection (an alias, or the node of a connection).
func collect(
	q runtime.FieldCollectable,
	meta *runtime.CollectMeta,
	parents []occurrence,
) {
	var (
		unknownSeen bool
		selected    = []string{q.GetIDColumn()}
		edges       []*edgeSelection
		edgeIndex   = map[string]*edgeSelection{}
	)
	// edgeFor returns the merged selection of an edge, keyed by the schema
	// edge name so every path to it lands in the same child query.
	edgeFor := func(edge runtime.EdgeMeta) *edgeSelection {
		es := edgeIndex[edge.Name]
		if es == nil {
			es = &edgeSelection{meta: edge}
			edgeIndex[edge.Name] = es
			edges = append(edges, es)
		}
		return es
	}
	for _, parent := range parents {
		for _, field := range parent.field.Fields(parent.satisfies) {
			name := field.FieldName()
			switch name {
			case "id", "__typename":
				continue
			}
			if col, ok := meta.FieldColumns[name]; ok {
				selected = append(selected, col)
				continue
			}
			if edge, ok := meta.Edges[name]; ok {
				// Columns of this table the edge needs (a foreign key it
				// owns); empty when the key lives on the other side.
				selected = append(selected, edge.FKColumns...)
				es := edgeFor(edge)
				es.fields = append(es.fields, field)
				continue
			}
			// An interface field (graphql.InterfaceField): eager-load every
			// contributing edge. When all of them own their foreign key and
			// the selection needs only __typename/id, the resolver builds
			// the node from the key, so only the key columns are selected.
			if ifm, ok := meta.InterfaceFields[name]; ok {
				for _, key := range ifm.Edges {
					selected = append(selected, meta.Edges[key].FKColumns...)
				}
				// Skip the edge loads only when the resolver can actually
				// answer from the keys; otherwise this would trade one join
				// for a query per row.
				if ifm.FastPath && coveredByID(field, ifm.Satisfies) {
					continue
				}
				// The selection is collected under every implementor's type
				// condition: fields of another implementor are unknown to
				// this edge's entity and keep it unprojected, which is safe.
				for _, key := range ifm.Edges {
					es := edgeFor(meta.Edges[key])
					es.viaInterface = append(es.viaInterface, occurrence{field: field, satisfies: ifm.Satisfies})
				}
				continue
			}
			// A custom resolver whose columns were declared via
			// graphql.CollectedFor: select exactly those.
			if cols, ok := meta.CollectedFor[name]; ok {
				selected = append(selected, cols...)
				continue
			}
			// Unknown field (custom resolver, etc.) — keep SELECT *.
			unknownSeen = true
		}
	}
	for _, es := range edges {
		collectEdge(q, es)
	}
	// Only apply column projection if all fields are known.
	if !unknownSeen {
		for _, col := range selected {
			q.GetCtx().AppendFieldOnce(col)
		}
	}
}

// collectEdge eager-loads one edge for all its occurrences and recurses into
// the edge query with the union of their selections.
//
// A Relay connection edge is loaded only for the occurrences its generated
// entity method answers from the loaded edge — those without after, before,
// where or orderBy; the others resolve through Paginate on their own, which
// is always correct. The loaded rows are limited per parent (never in
// total) to first+1 when every loaded occurrence asks for first and none
// asks for totalCount; otherwise the whole edge is loaded, since the entity
// method pages the loaded slice in memory and counts it for totalCount.
//
// An edge an interface field reaches is always loaded whole — its resolver
// reads every loaded row — and projected for the union of the direct and
// the interface selections.
func collectEdge(q runtime.FieldCollectable, es *edgeSelection) {
	if es.meta.Unique || !es.meta.Relay {
		child := q.WithEdgeLoad(es.meta.Name)
		collectChild(child, append(occurrences(es.fields, nil), es.viaInterface...))
		return
	}
	var (
		nodes     []SelectedField
		load      = len(es.viaInterface) > 0
		unlimited = load
		limit     int
	)
	if !es.meta.PagesLoaded {
		// The entity method would query the edge per row regardless; only
		// an interface field reads the loaded edge.
		if !load {
			return
		}
		child := q.WithEdgeLoad(es.meta.Name)
		collectChild(child, es.viaInterface)
		return
	}
	for _, field := range es.fields {
		args := field.Arguments()
		if args["after"] != nil || args["before"] != nil || args["where"] != nil || args["orderBy"] != nil {
			continue
		}
		occNodes, occ := connectionSelection(field)
		if !occ.rows {
			continue
		}
		load = true
		nodes = append(nodes, occNodes...)
		first, hasFirst := gqlToInt(args["first"])
		if occ.totalCount || !hasFirst || first < 0 {
			unlimited = true
			continue
		}
		limit = max(limit, first+1)
	}
	if !load {
		return
	}
	var opts []runtime.LoadOption
	if !unlimited {
		opts = append(opts, runtime.Limit(limit))
	}
	child := q.WithEdgeLoad(es.meta.Name, opts...)
	if child == nil {
		return
	}
	if len(nodes) == 0 && len(es.viaInterface) == 0 {
		child.GetCtx().AppendFieldOnce(child.GetIDColumn())
		return
	}
	collectChild(child, append(occurrences(nodes, nil), es.viaInterface...))
}

// collectChild recurses into an eager-loaded edge query when it carries its
// own metadata; otherwise the edge is loaded unprojected.
func collectChild(child runtime.FieldCollectable, fields []occurrence) {
	mc, ok := child.(MetaCollectable)
	if !ok {
		return
	}
	if meta := mc.CollectMeta(); meta != nil {
		collect(mc, meta, fields)
	}
}

// connectionUse reports what a connection selection reads.
type connectionUse struct {
	// rows is true when anything needs the connection's rows: its edges,
	// its page info or its total count.
	rows bool
	// totalCount is true when the total count is selected.
	totalCount bool
}

// connectionSelection returns the node selections of a connection field
// (edges { node { ... } }, one per occurrence) and what else it reads.
func connectionSelection(conn SelectedField) ([]SelectedField, connectionUse) {
	var (
		nodes []SelectedField
		use   connectionUse
	)
	for _, f := range conn.Fields(nil) {
		switch f.FieldName() {
		case "edges":
			use.rows = true
			for _, n := range f.Fields(nil) {
				if n.FieldName() == "node" {
					nodes = append(nodes, n)
				}
			}
		case "pageInfo":
			use.rows = true
		case "totalCount":
			use.rows = true
			use.totalCount = true
		}
	}
	return nodes, use
}

// gqlToInt converts a GraphQL argument value to int.
// AST-parsed IntValue yields int64, while gqlgen resolvers may pass int.
// JSON-parsed numbers arrive as float64, or as json.Number from an engine
// that keeps their precision.
func gqlToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		if n > int64(maxInt) || n < int64(minInt) {
			return 0, false
		}
		return int(n), true
	case int32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return gqlToInt(i)
	case float64:
		// JSON-parsed integers arrive as float64; accept only whole numbers in int range.
		if n != float64(int64(n)) {
			return 0, false
		}
		i := int64(n)
		if i > int64(maxInt) || i < int64(minInt) {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// maxInt and minInt are the platform-specific int bounds.
const (
	maxInt = int(^uint(0) >> 1)
	minInt = -maxInt - 1
)
