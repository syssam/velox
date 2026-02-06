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
	entityPkg := h.EntityPkgPath(t)

	// Filter struct - uses pointer to predicates slice so modifications affect the original query/mutation
	f.Commentf("%s provides a generic filtering capability at runtime for %s.", filterName, t.Name)
	f.Type().Id(filterName).Struct(
		jen.Id("config"),
		jen.Id("predicates").Op("*").Index().Add(h.PredicateType(t)),
	)

	// WhereP adds predicates using raw sql.Selector functions
	// This implements the privacy.Filter interface
	f.Comment("WhereP appends storage-level predicates to the filter.")
	f.Comment("Using this method, users can use type-assertion to e.g., compose dynamic filters.")
	f.Comment("")
	f.Comment("Example usage with entity predicates:")
	f.Commentf("    f.WhereP(%s.WorkspaceIDField.EQ(workspaceID))", t.Package())
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("WhereP").Params(
		jen.Id("ps").Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
	).Block(
		jen.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Op("*").Id("f").Dot("predicates").Op("=").Append(
				jen.Op("*").Id("f").Dot("predicates"),
				jen.Add(h.PredicateType(t)).Call(jen.Id("p")),
			),
		),
	)

	// Where adds type-safe predicates
	f.Comment("Where appends type-safe predicates to the filter.")
	f.Comment("")
	f.Comment("Example usage:")
	f.Commentf("    f.Where(%s.StatusEQ(\"active\"))", t.Package())
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Block(
		jen.Op("*").Id("f").Dot("predicates").Op("=").Append(jen.Op("*").Id("f").Dot("predicates"), jen.Id("ps").Op("...")),
	)

	// HasColumn checks if the entity has a specific column
	// This enables dynamic filtering based on column existence
	f.Comment("HasColumn reports whether the entity has the given column name.")
	f.Comment("This is useful for writing generic privacy rules that apply to multiple entities.")
	f.Func().Params(jen.Id("f").Op("*").Id(filterName)).Id("HasColumn").Params(
		jen.Id("column").String(),
	).Bool().Block(
		jen.Return(
			jen.Qual("slices", "Contains").Call(
				jen.Qual(entityPkg, "Columns"),
				jen.Id("column"),
			),
		),
	)

	return f
}
