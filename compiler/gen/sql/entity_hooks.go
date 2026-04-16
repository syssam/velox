package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genEntityHooks generates entity/hooks.go with HookStore and InterceptorStore structs.
// These are shared via pointer between the root client and all entity clients,
// giving direct field access (e.g., c.hookStore.User) with zero indirection.
func genEntityHooks(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())

	graph := h.Graph()

	f.Comment("HookStore holds mutation hooks for all entities.")
	f.Comment("Shared via pointer between root client and all entity clients.")
	f.Comment("")
	f.Comment("Concurrency contract: all Use() calls must complete before")
	f.Comment("concurrent query/mutation execution begins. HookStore is")
	f.Comment("intended for application startup (e.g., in main or init),")
	f.Comment("not for runtime registration. No synchronization is provided")
	f.Comment("on the hook slices — this matches Ent's design.")
	f.Type().Id("HookStore").StructFunc(func(g *jen.Group) {
		for _, node := range graph.Nodes {
			g.Id(node.Name).Index().Qual(veloxCorePkg, "Hook")
		}
	})

	// AppendAll appends hooks to every entity's slice in one call.
	// Used by Client.Use() to avoid O(N) generated append statements in client.go.
	f.Comment("AppendAll appends the given hooks to every entity's hook slice.")
	f.Func().Params(jen.Id("s").Op("*").Id("HookStore")).Id("AppendAll").Params(
		jen.Id("hooks").Op("...").Qual(veloxCorePkg, "Hook"),
	).BlockFunc(func(grp *jen.Group) {
		for _, node := range graph.Nodes {
			grp.Id("s").Dot(node.Name).Op("=").Append(
				jen.Id("s").Dot(node.Name),
				jen.Id("hooks").Op("..."),
			)
		}
	})

	f.Comment("InterceptorStore holds query interceptors for all entities.")
	f.Comment("Shared via pointer between root client and all entity clients.")
	f.Comment("")
	f.Comment("Concurrency contract: all Intercept() calls must complete before")
	f.Comment("concurrent query execution begins. InterceptorStore is intended")
	f.Comment("for application startup (e.g., in main or init), not for")
	f.Comment("runtime registration. No synchronization is provided on the")
	f.Comment("interceptor slices — this matches Ent's design.")
	f.Type().Id("InterceptorStore").StructFunc(func(g *jen.Group) {
		for _, node := range graph.Nodes {
			g.Id(node.Name).Index().Qual(veloxCorePkg, "Interceptor")
		}
	})

	// AppendAll appends interceptors to every entity's slice in one call.
	// Used by Client.Intercept() to avoid O(N) generated append statements in client.go.
	f.Comment("AppendAll appends the given interceptors to every entity's interceptor slice.")
	f.Func().Params(jen.Id("s").Op("*").Id("InterceptorStore")).Id("AppendAll").Params(
		jen.Id("interceptors").Op("...").Qual(veloxCorePkg, "Interceptor"),
	).BlockFunc(func(grp *jen.Group) {
		for _, node := range graph.Nodes {
			grp.Id("s").Dot(node.Name).Op("=").Append(
				jen.Id("s").Dot(node.Name),
				jen.Id("interceptors").Op("..."),
			)
		}
	})

	return f
}
