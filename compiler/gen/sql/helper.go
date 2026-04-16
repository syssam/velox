package sql

import (
	"unicode"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// veloxCorePkg is the import path for the velox core package.
// Used for types like velox.Hook and velox.Interceptor in generated code.
const veloxCorePkg = "github.com/syssam/velox"

// runtimePkg is the import path for the velox runtime package.
// Entity sub-packages and root generators reference shared types (Hook, Interceptor,
// Op, Value, AggregateFunc) from this package.
const runtimePkg = "github.com/syssam/velox/runtime"

// genConvertPredicates generates the code that converts typed predicates
// to []func(*sql.Selector). Uses PredicatesFuncs() public method to work
// across package boundaries (root wrapper accessing entity sub-package mutation).
func genConvertPredicates(grp *jen.Group, recv, _ string) {
	grp.Id("ps").Op(":=").Id(recv).Dot("mutation").Dot("PredicatesFuncs").Call()
}

// idFieldTypeVar returns the variable name for the IDFieldType of a type.
// For example, "User" -> "userIDFieldType".
func idFieldTypeVar(t *gen.Type) string {
	return lowerFirst(t.Name) + "IDFieldType"
}

// fieldTypesVar returns the variable name for the FieldTypes map of a type.
// For example, "User" -> "userFieldTypes".
func fieldTypesVar(t *gen.Type) string {
	return lowerFirst(t.Name) + "FieldTypes"
}

// genIDFieldTypeAndFieldTypesVars generates package-level variables for IDFieldType and FieldTypes.
// These replace the old TypeInfo.IDFieldType and TypeInfo.FieldTypes fields.
func genIDFieldTypeAndFieldTypesVars(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	// IDFieldType constant.
	f.Commentf("%s is the field.Type for %s's primary key column.", idFieldTypeVar(t), t.Name)
	f.Var().Id(idFieldTypeVar(t)).Op("=").Qual(h.FieldPkg(), h.FieldTypeConstant(t.ID))

	// FieldTypes map.
	f.Commentf("%s maps column names to field.Type for %s.", fieldTypesVar(t), t.Name)
	f.Var().Id(fieldTypesVar(t)).Op("=").Map(jen.String()).Qual(h.FieldPkg(), "Type").ValuesFunc(func(d *jen.Group) {
		if t.ID != nil {
			d.Lit(t.ID.StorageKey()).Op(":").Qual(h.FieldPkg(), h.FieldTypeConstant(t.ID))
		}
		for _, fd := range t.Fields {
			d.Lit(fd.StorageKey()).Op(":").Qual(h.FieldPkg(), h.FieldTypeConstant(fd))
		}
	})
}

// edgeSpecBase returns the (Rel, Table, Columns, Inverse, Bidi) Jennifer
// expressions for a sqlgraph.EdgeSpec literal. All values are emitted as
// literals (table/column strings, sqlgraph.Rel constants) so generated entity
// sub-packages do not need to import target entity sub-packages for edge
// processing — matching Velox's "zero cross-entity imports" rule.
//
// Returns (nil, nil, nil, false, false) if the edge relation type is unknown.
func edgeSpecBase(edge *gen.Edge, sqlGraphPkg string) (rel jen.Code, tableExpr jen.Code, columnsExpr jen.Code, inverse bool, bidi bool) {
	table := edge.Rel.Table
	cols := edge.Rel.Columns
	colStrs := make([]jen.Code, 0, len(cols))
	for _, c := range cols {
		colStrs = append(colStrs, jen.Lit(c))
	}
	colsLit := jen.Index().String().Values(colStrs...)
	switch {
	case edge.M2M():
		return jen.Qual(sqlGraphPkg, "M2M"), jen.Lit(table), colsLit, edge.IsInverse(), edge.Bidi
	case edge.O2M() && !edge.IsInverse():
		return jen.Qual(sqlGraphPkg, "O2M"), jen.Lit(table), colsLit, false, false
	case edge.M2O() || (edge.O2M() && edge.IsInverse()):
		return jen.Qual(sqlGraphPkg, "M2O"), jen.Lit(table), colsLit, true, false
	case edge.O2O():
		return jen.Qual(sqlGraphPkg, "O2O"), jen.Lit(table), colsLit, edge.IsInverse(), edge.Bidi
	}
	return nil, nil, nil, false, false
}

// lowerFirst returns s with its first rune lowered (used for unexported function names).
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// dialectPkg returns the import path for the dialect package.
func dialectPkg() string {
	return "github.com/syssam/velox/dialect"
}

// schemaPkg returns the import path for the schema field package.
func schemaPkg() string {
	return "github.com/syssam/velox/schema/field"
}

// assertSetInterStore returns Jennifer code for the inline interface type-assertion
// pattern that wires the shared *entity.InterceptorStore pointer onto a query:
//
//	<queryVar>.(interface{ SetInterStore(*entity.InterceptorStore) }).SetInterStore(<storeExpr>)
//
// This avoids importing the concrete query type's package from the caller side.
func assertSetInterStore(queryVar string, entityPkg string, storeExpr jen.Code) *jen.Statement {
	return jen.Id(queryVar).Op(".").Parens(
		jen.Interface(
			jen.Id("SetInterStore").Params(jen.Op("*").Qual(entityPkg, "InterceptorStore")),
		),
	).Dot("SetInterStore").Call(storeExpr)
}

// assertSetPolicy returns Jennifer code for the inline interface type-assertion
// pattern that wires a velox.Policy onto a query. The assertion uses a
// two-result form (", ok") so queries without a SetPolicy method (entities
// without privacy policies) don't panic — they simply skip the wiring.
//
//	if _sp, _ok := <queryVar>.(interface{ SetPolicy(velox.Policy) }); _ok {
//	    _sp.SetPolicy(<policyExpr>)
//	}
//
// This keeps the caller side entity-agnostic: a single generated
// `client.User.Query()` code path works whether or not the User entity
// has a privacy policy.
func assertSetPolicy(queryVar string, veloxPkg string, policyExpr jen.Code) *jen.Statement {
	return jen.If(
		jen.List(jen.Id("_sp"), jen.Id("_ok")).Op(":=").Id(queryVar).Op(".").Parens(
			jen.Interface(
				jen.Id("SetPolicy").Params(jen.Qual(veloxPkg, "Policy")),
			),
		),
		jen.Id("_ok"),
	).Block(
		jen.Id("_sp").Dot("SetPolicy").Call(policyExpr),
	)
}

// assertSetPath returns Jennifer code for the inline interface type-assertion
// pattern that sets the graph traversal path on an edge query:
//
//	<queryVar>.(interface{ SetPath(func(context.Context) (*sql.Selector, error)) }).SetPath(...)
//
// This avoids importing the concrete query type's package from the caller side.
func assertSetPath(queryVar string, sqlPkg string, pathClosure jen.Code) *jen.Statement {
	return jen.Id(queryVar).Op(".").Parens(
		jen.Interface(
			jen.Id("SetPath").Params(
				jen.Func().Params(
					jen.Qual("context", "Context"),
				).Params(
					jen.Op("*").Qual(sqlPkg, "Selector"),
					jen.Error(),
				),
			),
		),
	).Dot("SetPath").Call(pathClosure)
}
