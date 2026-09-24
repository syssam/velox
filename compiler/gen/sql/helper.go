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
	case edge.O2M():
		// An O2M edge marked inverse is the edge-schema edge generated for a
		// Through() (User.memberships): the key still lives on the join
		// table, so it is O2M with Inverse set, as Ent emits. Mapping it to
		// M2O told sqlgraph the key was on the owner's table, and every
		// Add/Remove/Clear built SQL against a column that does not exist.
		return jen.Qual(sqlGraphPkg, "O2M"), jen.Lit(table), colsLit, edge.IsInverse(), false
	case edge.M2O():
		return jen.Qual(sqlGraphPkg, "M2O"), jen.Lit(table), colsLit, true, false
	case edge.O2O():
		return jen.Qual(sqlGraphPkg, "O2O"), jen.Lit(table), colsLit, edge.IsInverse(), edge.Bidi
	}
	return nil, nil, nil, false, false
}

// edgeSchemaDefaults returns the statement that fills an M2M edge spec's
// join-row defaults from the edge schema's registered builder, or nil when
// the edge has no Through() entity with defaults. The adding entity cannot
// import the join entity's package, so the defaults come from a registry
// (runtime.RegisterEdgeSchemaDefaults, emitted by genEntityRuntimeRegistration).
func edgeSchemaDefaults(edge *gen.Edge, edgeVar string) jen.Code {
	if !edge.M2M() || edge.Through == nil || !edge.Through.HasDefault() {
		return nil
	}
	return jen.Id(edgeVar).Dot("Target").Dot("Fields").Op("=").Qual(runtimePkg, "EdgeSchemaDefaults").Call(jen.Lit(edge.Through.Table()))
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

// genSchemaHooksLocal emits a local hook slice that merges the builder's
// runtime hooks with the schema-level Hooks array declared in the entity's
// leaf package.
//
// The runtime slice is re-sliced with a full slice expression (cap == len)
// before appending. `<recv>.hooks` aliases the client's shared
// *entity.HookStore slice, so a plain `append(<recv>.hooks, Hooks[:]...)`
// writes the schema hook into that store's spare capacity — silently
// overwriting a hook registered by a later Use(). The hook then stops
// running and whatever field it stamped goes missing from the statement.
// Ent applies the same clamp in Client.Hooks().
func genSchemaHooksLocal(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, recv, local string) {
	grp.Id(local).Op(":=").Id(recv).Dot("hooks")
	if t.NumHooks() == 0 {
		return
	}
	grp.Id(local).Op("=").Append(
		jen.Id(local).Index(jen.Empty(), jen.Len(jen.Id(local)), jen.Len(jen.Id(local))),
		jen.Qual(h.LeafPkgPath(t), "Hooks").Index(jen.Op(":")).Op("..."),
	)
}

// Field validation — one predicate and one emitter shared by the leaf package
// declarations (package.go), the runtime init (runtime.go) and the create and
// update check() methods (create.go, update.go). Keep every site on these
// helpers: a site that disagrees either declares a validator nothing assigns
// (nil func, panic on Save) or assigns one nothing calls (dead validation).
//
// Validators are generated unconditionally, as Ent does. They were once gated
// on the opt-in FeatureValidator, which left every NotEmpty/MaxLen/Range and
// every enum check on a default project silently unenforced.

// hasGeneratedValidator reports whether the leaf package declares a
// <Field>Validator for fd: the field has schema validators (a variable the
// runtime init assigns), or it is an enum (a function checking the value
// against the declared set; see genEnumValidatorFunc).
func hasGeneratedValidator(fd *gen.Field) bool {
	return hasRuntimeValidator(fd) || fd.IsEnum()
}

// hasRuntimeValidator reports whether fd's <Field>Validator is a package
// variable assigned by the runtime init from the schema descriptor. Enum
// validators are generated functions and need no assignment.
func hasRuntimeValidator(fd *gen.Field) bool {
	return fd.Validators > 0
}

// fieldNeedsValidation reports whether a builder's check() must validate fd:
// either through the generated <Field>Validator, or through the Validate()
// method of a custom Go type that implements it.
func fieldNeedsValidation(fd *gen.Field) bool {
	return hasGeneratedValidator(fd) || (fd.HasGoType() && fd.Type != nil && fd.Type.Validator())
}

// validationErrorValue renders `&runtime.ValidationError{...}` for a field of t.
func validationErrorValue(t *gen.Type, name, errVal jen.Code) jen.Code {
	return jen.Op("&").Qual(runtimePkg, "ValidationError").Values(jen.Dict{
		jen.Id("Name"):   name,
		jen.Id("Err"):    errVal,
		jen.Id("Entity"): jen.Lit(t.Name),
		jen.Id("Field"):  name,
	})
}

// genFieldValidatorCheck emits, inside a check() body, the validation of fd's
// value when the mutation sets it:
//
//	if v, ok := <recv>.mutation.<Field>(); ok {
//		if err := <leaf>.<Field>Validator(v); err != nil {
//			return &runtime.ValidationError{...}
//		}
//	}
//
// Callers select the fields with fieldNeedsValidation.
func genFieldValidatorCheck(grp *jen.Group, entityPkg string, t *gen.Type, fd *gen.Field, recv string) {
	grp.If(
		jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id(recv).Dot("mutation").Dot(fd.MutationGet()).Call(),
		jen.Id("ok"),
	).BlockFunc(func(blk *jen.Group) {
		var validationCall *jen.Statement
		if hasGeneratedValidator(fd) {
			// The validator takes the basic type; convert a custom GoType
			// the way Ent does ($f.BasicType "v"): string(v), int64(v),
			// v.String(), []byte(v), ... Enums and JSON pass v unchanged.
			validationCall = jen.Qual(entityPkg, fd.Validator()).Call(jen.Id(fd.BasicType("v")))
		} else {
			validationCall = jen.Id("v").Dot("Validate").Call()
		}
		blk.If(jen.Id("err").Op(":=").Add(validationCall), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(validationErrorValue(t,
				jen.Lit(fd.Name),
				jen.Qual("fmt", "Errorf").Call(jen.Lit("validator failed for field \""+t.Name+"."+fd.Name+"\": %w"), jen.Id("err")),
			)),
		)
	})
}
