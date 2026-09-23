package gqlrelay

import (
	"context"
	"encoding/json"

	"github.com/99designs/gqlgen/graphql"

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
// outside a gqlgen resolver.
//
// The generated (*XxxQuery).CollectFields method calls it; call that from a
// resolver that returns entities directly (a list or single node). Paginate
// collects on its own, from the connection's edges.node selection.
//
// Projection is skipped (SELECT * is kept) when the selection contains a
// field the metadata cannot map to columns — a custom resolver without a
// graphql.CollectedFor annotation might read any column.
func CollectFields(ctx context.Context, q runtime.FieldCollectable, meta *runtime.CollectMeta, satisfies ...string) error {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || meta == nil || !graphql.HasOperationContext(ctx) {
		return nil
	}
	collect(graphql.GetOperationContext(ctx), q, meta, []graphql.CollectedField{fc.Field}, satisfies)
	return nil
}

// CollectConnectionFields is CollectFields for a Relay connection field: it
// configures q from the edges { node { ... } } selection of the connection
// being resolved in ctx. The generated Paginate methods call it before
// running the page query, as Ent's do.
func CollectConnectionFields(ctx context.Context, q runtime.FieldCollectable, meta *runtime.CollectMeta) error {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || meta == nil || !graphql.HasOperationContext(ctx) {
		return nil
	}
	opCtx := graphql.GetOperationContext(ctx)
	nodes, _ := connectionSelection(opCtx, fc.Field)
	if len(nodes) == 0 {
		// No node is read (totalCount/pageInfo only): the key is enough.
		q.GetCtx().AppendFieldOnce(q.GetIDColumn())
		return nil
	}
	collect(opCtx, q, meta, nodes, nil)
	return nil
}

// edgeSelection gathers every occurrence of one edge in a selection set —
// aliases select the same edge more than once, and a query can load an edge
// only once, so the occurrences are merged.
type edgeSelection struct {
	meta   runtime.EdgeMeta
	fields []graphql.CollectedField
}

// collect projects q onto what the given fields select and schedules the
// edges they traverse. Each field is one occurrence of the same entity in
// the selection (an alias, or the node of a connection).
func collect(
	opCtx *graphql.OperationContext,
	q runtime.FieldCollectable,
	meta *runtime.CollectMeta,
	parents []graphql.CollectedField,
	satisfies []string,
) {
	var (
		unknownSeen bool
		selected    = []string{q.GetIDColumn()}
		edges       []*edgeSelection
		edgeIndex   = map[string]*edgeSelection{}
	)
	for _, parent := range parents {
		for _, field := range graphql.CollectFields(opCtx, parent.Selections, satisfies) {
			switch field.Name {
			case "id", "__typename":
				continue
			}
			if col, ok := meta.FieldColumns[field.Name]; ok {
				selected = append(selected, col)
				continue
			}
			if edge, ok := meta.Edges[field.Name]; ok {
				// Columns of this table the edge needs (a foreign key it
				// owns); empty when the key lives on the other side.
				selected = append(selected, edge.FKColumns...)
				es := edgeIndex[field.Name]
				if es == nil {
					es = &edgeSelection{meta: edge}
					edgeIndex[field.Name] = es
					edges = append(edges, es)
				}
				es.fields = append(es.fields, field)
				continue
			}
			// An interface field (graphql.InterfaceField): eager-load every
			// contributing edge. When all of them own their foreign key and
			// the selection needs only __typename/id, the resolver builds
			// the node from the key, so only the key columns are selected.
			if ifm, ok := meta.InterfaceFields[field.Name]; ok {
				for _, key := range ifm.Edges {
					selected = append(selected, meta.Edges[key].FKColumns...)
				}
				// Skip the edge loads only when the resolver can actually
				// answer from the keys; otherwise this would trade one join
				// for a query per row.
				if ifm.FastPath && InterfaceFieldCoveredByID(field, opCtx, ifm.Satisfies...) {
					continue
				}
				for _, key := range ifm.Edges {
					q.WithEdgeLoad(meta.Edges[key].Name)
				}
				continue
			}
			// A custom resolver whose columns were declared via
			// graphql.CollectedFor: select exactly those.
			if cols, ok := meta.CollectedFor[field.Name]; ok {
				selected = append(selected, cols...)
				continue
			}
			// Unknown field (custom resolver, etc.) — keep SELECT *.
			unknownSeen = true
		}
	}
	for _, es := range edges {
		collectEdge(opCtx, q, es)
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
func collectEdge(opCtx *graphql.OperationContext, q runtime.FieldCollectable, es *edgeSelection) {
	if es.meta.Unique || !es.meta.Relay {
		child := q.WithEdgeLoad(es.meta.Name)
		collectChild(opCtx, child, es.fields)
		return
	}
	if !es.meta.PagesLoaded {
		// The entity method would query the edge per row regardless.
		return
	}
	var (
		nodes     []graphql.CollectedField
		load      bool
		unlimited bool
		limit     int
	)
	for _, field := range es.fields {
		args := field.ArgumentMap(opCtx.Variables)
		if args["after"] != nil || args["before"] != nil || args["where"] != nil || args["orderBy"] != nil {
			continue
		}
		occNodes, occ := connectionSelection(opCtx, field)
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
	if len(nodes) == 0 {
		child.GetCtx().AppendFieldOnce(child.GetIDColumn())
		return
	}
	collectChild(opCtx, child, nodes)
}

// collectChild recurses into an eager-loaded edge query when it carries its
// own metadata; otherwise the edge is loaded unprojected.
func collectChild(opCtx *graphql.OperationContext, child runtime.FieldCollectable, fields []graphql.CollectedField) {
	mc, ok := child.(MetaCollectable)
	if !ok {
		return
	}
	if meta := mc.CollectMeta(); meta != nil {
		collect(opCtx, mc, meta, fields, nil)
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
func connectionSelection(opCtx *graphql.OperationContext, conn graphql.CollectedField) ([]graphql.CollectedField, connectionUse) {
	var (
		nodes []graphql.CollectedField
		use   connectionUse
	)
	for _, f := range graphql.CollectFields(opCtx, conn.Selections, nil) {
		switch f.Name {
		case "edges":
			use.rows = true
			for _, n := range graphql.CollectFields(opCtx, f.Selections, nil) {
				if n.Name == "node" {
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
// JSON-parsed numbers may arrive as float64.
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
