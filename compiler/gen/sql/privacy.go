package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genPrivacy generates the privacy package (privacy/privacy.go).
// This is part of the privacy feature.
func genPrivacy(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("privacy")
	graph := h.Graph()

	privacyPkg := "github.com/syssam/velox/privacy"
	// The generated ent package where Query/Mutation types are defined
	entPkg := graph.Package

	f.ImportName("context", "context")
	f.ImportName(privacyPkg, "privacy")
	f.ImportAlias(entPkg, "ent")

	// Re-export common privacy types from the base package
	f.Comment("Policy groups query and mutation policies.")
	f.Var().Id("_").Op("=").Qual(privacyPkg, "Policy").Values()

	f.Comment("QueryRule defines the interface deciding whether a query is allowed and optionally modify it.")
	f.Type().Id("QueryRule").Op("=").Qual(privacyPkg, "QueryRule")

	f.Comment("MutationRule defines the interface deciding whether a mutation is allowed and optionally modify it.")
	f.Type().Id("MutationRule").Op("=").Qual(privacyPkg, "MutationRule")

	f.Comment("QueryMutationRule is an interface which groups query and mutation rules.")
	f.Type().Id("QueryMutationRule").Op("=").Qual(privacyPkg, "QueryMutationRule")

	f.Comment("QueryPolicy combines multiple query rules into a single policy.")
	f.Type().Id("QueryPolicy").Op("=").Qual(privacyPkg, "QueryPolicy")

	f.Comment("MutationPolicy combines multiple mutation rules into a single policy.")
	f.Type().Id("MutationPolicy").Op("=").Qual(privacyPkg, "MutationPolicy")

	f.Comment("Policy groups query and mutation policies.")
	f.Type().Id("Policy").Op("=").Qual(privacyPkg, "Policy")

	// Re-export decision constants and helpers
	f.Var().Defs(
		jen.Comment("Allow may be returned by rules to indicate that the policy"),
		jen.Comment("evaluation should terminate with an allow decision."),
		jen.Id("Allow").Op("=").Qual(privacyPkg, "Allow"),
		jen.Comment("Deny may be returned by rules to indicate that the policy"),
		jen.Comment("evaluation should terminate with a deny decision."),
		jen.Id("Deny").Op("=").Qual(privacyPkg, "Deny"),
		jen.Comment("Skip may be returned by rules to indicate that the policy"),
		jen.Comment("evaluation should continue to the next rule."),
		jen.Id("Skip").Op("=").Qual(privacyPkg, "Skip"),
	)

	// Re-export helper functions
	f.Comment("Allowf returns a formatted wrapped Allow decision.")
	f.Var().Id("Allowf").Op("=").Qual(privacyPkg, "Allowf")

	f.Comment("Denyf returns a formatted wrapped Deny decision.")
	f.Var().Id("Denyf").Op("=").Qual(privacyPkg, "Denyf")

	f.Comment("Skipf returns a formatted wrapped Skip decision.")
	f.Var().Id("Skipf").Op("=").Qual(privacyPkg, "Skipf")

	f.Comment("AlwaysAllowRule returns a rule that returns an allow decision.")
	f.Var().Id("AlwaysAllowRule").Op("=").Qual(privacyPkg, "AlwaysAllowRule")

	f.Comment("AlwaysDenyRule returns a rule that returns a deny decision.")
	f.Var().Id("AlwaysDenyRule").Op("=").Qual(privacyPkg, "AlwaysDenyRule")

	f.Comment("ContextQueryMutationRule creates a query/mutation rule from a context eval func.")
	f.Var().Id("ContextQueryMutationRule").Op("=").Qual(privacyPkg, "ContextQueryMutationRule")

	f.Comment("OnMutationOperation evaluates the given rule only on a given mutation operation.")
	f.Var().Id("OnMutationOperation").Op("=").Qual(privacyPkg, "OnMutationOperation")

	f.Comment("DenyMutationOperationRule returns a rule denying specified mutation operation.")
	f.Var().Id("DenyMutationOperationRule").Op("=").Qual(privacyPkg, "DenyMutationOperationRule")

	f.Comment("NewPolicies creates an ent.Policy from list of mixin.Schema and ent.Schema that implement the ent.Policy interface.")
	f.Var().Id("NewPolicies").Op("=").Qual(privacyPkg, "NewPolicies")

	f.Comment("Policies combines multiple policies into a single policy.")
	f.Type().Id("Policies").Op("=").Qual(privacyPkg, "Policies")

	f.Comment("DecisionContext creates a new context from the given parent context with a policy decision attach to it.")
	f.Var().Id("DecisionContext").Op("=").Qual(privacyPkg, "DecisionContext")

	f.Comment("DecisionFromContext retrieves the policy decision from the context.")
	f.Var().Id("DecisionFromContext").Op("=").Qual(privacyPkg, "DecisionFromContext")

	// Generate per-entity rule types
	for _, n := range graph.Nodes {
		genPrivacyEntityTypes(h, f, n, entPkg)
	}

	// Re-export Filter types from core privacy package
	// This allows users to import from either the generated or core package
	f.Comment("Filter is the interface that wraps the WhereP method for filtering")
	f.Comment("nodes in queries and mutations based on predicates.")
	f.Type().Id("Filter").Op("=").Qual(privacyPkg, "Filter")

	f.Comment("Filterable is implemented by queries and mutations that support filtering.")
	f.Type().Id("Filterable").Op("=").Qual(privacyPkg, "Filterable")

	f.Comment("FilterFunc is an adapter that allows using ordinary functions as")
	f.Comment("query/mutation rules that apply predicates to filter results.")
	f.Type().Id("FilterFunc").Op("=").Qual(privacyPkg, "FilterFunc")

	return f
}

// genPrivacyEntityTypes generates entity-specific query/mutation rule types.
func genPrivacyEntityTypes(h gen.GeneratorHelper, f *jen.File, n *gen.Type, entPkg string) {
	queryType := jen.Op("*").Qual(entPkg, n.QueryName())
	mutationType := jen.Op("*").Qual(entPkg, n.MutationName())

	// QueryRuleFunc type
	queryRuleFuncName := n.Name + "QueryRuleFunc"
	f.Commentf("The %s type is an adapter to allow the use of ordinary", queryRuleFuncName)
	f.Comment("functions as a query rule.")
	f.Type().Id(queryRuleFuncName).Func().Params(
		jen.Qual("context", "Context"),
		queryType,
	).Error()

	// EvalQuery method
	f.Comment("EvalQuery returns f(ctx, q).")
	f.Func().Params(jen.Id("f").Id(queryRuleFuncName)).Id("EvalQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("q").Qual(entPkg, "Query"),
	).Error().Block(
		jen.If(
			jen.List(jen.Id("q"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(queryType),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("q"))),
		),
		jen.Return(jen.Id("Denyf").Call(jen.Lit("unexpected query type %T, expect "+n.QueryName()), jen.Id("q"))),
	)

	// MutationRuleFunc type
	mutationRuleFuncName := n.Name + "MutationRuleFunc"
	f.Commentf("The %s type is an adapter to allow the use of ordinary", mutationRuleFuncName)
	f.Comment("functions as a mutation rule.")
	f.Type().Id(mutationRuleFuncName).Func().Params(
		jen.Qual("context", "Context"),
		mutationType,
	).Error()

	// EvalMutation method
	f.Comment("EvalMutation returns f(ctx, m).")
	f.Func().Params(jen.Id("f").Id(mutationRuleFuncName)).Id("EvalMutation").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("m").Qual(entPkg, "Mutation"),
	).Error().Block(
		jen.If(
			jen.List(jen.Id("m"), jen.Id("ok")).Op(":=").Id("m").Op(".").Parens(mutationType),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("m"))),
		),
		jen.Return(jen.Id("Denyf").Call(jen.Lit("unexpected mutation type %T, expect "+n.MutationName()), jen.Id("m"))),
	)
}
