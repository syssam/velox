package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genFilter generates the filter file ({entity}_filter.go) for privacy filtering.
// This is only generated when the privacy feature is enabled.
//
// The generated Filter struct provides these methods:
//   - WhereP: accepts raw sql.Selector functions (implements privacy.Filter interface)
//   - Where: accepts type-safe predicates for the entity
//   - HasColumn: checks if the entity has a specific column (for dynamic filtering)
//
// Example multi-tenant filtering:
//
//	type ColumnChecker interface {
//	    HasColumn(column string) bool
//	}
//
//	func FilterWorkspaceRule(workspaceID string) privacy.QueryMutationRule {
//	    return privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
//	        cc, ok := f.(ColumnChecker)
//	        if !ok || !cc.HasColumn("workspace_id") {
//	            return privacy.Skip
//	        }
//	        f.WhereP(func(s *sql.Selector) {
//	            s.Where(sql.EQ(s.C("workspace_id"), workspaceID))
//	        })
//	        return privacy.Skip
//	    })
//	}
func genFilter(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	filterName := t.Name + "Filter"
	entityPkg := h.LeafPkgPath(t)

	// Filter struct — wraps a pointer to the query's own predicates
	// slice so modifications written through the filter land directly
	// on the live query. The slice element type is the raw
	// func(*sql.Selector) used by generated query builders; named
	// predicate types are assignable to it because their underlying
	// type is identical.
	// Filter struct — wraps a runtime.PredicateAdder (implemented by
	// the generated query builder) rather than a pointer into the
	// query's internal predicates slice. This decouples the filter
	// from the query's internal representation: the query can evolve
	// how it stores predicates without breaking filter construction,
	// and privacy rules only see the narrow AddPredicate surface.
	configField := jen.Id("config").Qual(runtimePkg, "Config")
	selectorFn := jen.Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))
	f.Commentf("%s provides a generic filtering capability at runtime for %s.", filterName, t.Name)
	f.Type().Id(filterName).Struct(
		configField,
		jen.Id("adder").Qual(runtimePkg, "PredicateAdder"),
	)

	// New{Entity}Filter — exported constructor so the generated query
	// builder (which lives in a sibling package) can build one without
	// reaching into unexported fields. Takes the PredicateAdder so
	// the caller decides where the predicates actually land.
	f.Commentf("New%s builds a %s that writes predicates through the given PredicateAdder.", filterName, filterName)
	f.Comment("Used by generated query builders to implement privacy.Filterable.")
	f.Func().Id("New"+filterName).Params(
		jen.Id("cfg").Qual(runtimePkg, "Config"),
		jen.Id("adder").Qual(runtimePkg, "PredicateAdder"),
	).Qual("github.com/syssam/velox/privacy", "Filter").Block(
		jen.Return(jen.Op("&").Id(filterName).Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
			jen.Id("adder"):  jen.Id("adder"),
		})),
	)

	// WhereP adds predicates using raw sql.Selector functions.
	// Implements the privacy.Filter interface by delegating through
	// the PredicateAdder — the filter never touches the query's
	// internal slice directly.
	f.Comment("WhereP appends storage-level predicates to the filter.")
	f.Comment("Using this method, users can use type-assertion to e.g., compose dynamic filters.")
	f.Comment("")
	f.Comment("Example usage with entity predicates:")
	f.Commentf("    f.WhereP(%s.WorkspaceIDField.EQ(workspaceID))", t.Package())
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("WhereP").Params(
		jen.Id("ps").Op("...").Add(selectorFn.Clone()),
	).Block(
		jen.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Id("f").Dot("adder").Dot("AddPredicate").Call(jen.Id("p")),
		),
	)

	// Where adds type-safe predicates. predicate.Xxx is a named alias
	// of func(*sql.Selector), so each one can be passed through the
	// PredicateAdder interface unchanged.
	f.Comment("Where appends type-safe predicates to the filter.")
	f.Comment("")
	f.Comment("Example usage:")
	f.Commentf("    f.Where(%s.StatusEQ(\"active\"))", t.Package())
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Block(
		jen.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Id("f").Dot("adder").Dot("AddPredicate").Call(jen.Id("p")),
		),
	)

	// HasColumn checks if the entity has a specific column — O(1) map lookup.
	// This enables dynamic filtering based on column existence.
	f.Comment("HasColumn reports whether the entity has the given column name.")
	f.Comment("This is useful for writing generic privacy rules that apply to multiple entities.")
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("HasColumn").Params(
		jen.Id("column").String(),
	).Bool().Block(
		jen.Return(jen.Qual(entityPkg, "ValidColumn").Call(jen.Id("column"))),
	)

	return f
}
