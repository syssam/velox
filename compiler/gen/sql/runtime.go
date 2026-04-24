package sql

import (
	"strconv"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genRuntimeEntityInit generates the runtime initialization for a single entity.
// It follows Ent's template logic for handling mixins and field positions.
// Order: Mixin → Policies → Hooks → Interceptors → Fields
func genRuntimeEntityInit(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, schemaPkg string) {
	entityPkg := h.EntityPkgPath(t)
	pkg := t.Package() // lowercase package name (e.g., "abtestevent")

	// Check if entity has defaults, update defaults, validators, or value scanners
	validatorsEnabled, _ := h.Graph().FeatureEnabled(gen.FeatureValidator.Name)
	hasRuntimeFields := t.HasDefault() || t.HasUpdateDefault() || (validatorsEnabled && t.HasValidators())

	// Skip if no runtime code needed (no mixins and no runtime fields)
	if !hasRuntimeFields && !t.RuntimeMixin() {
		return
	}

	// 1. Load mixin if entity has mixins with runtime code
	if t.RuntimeMixin() {
		grp.Id(pkg+"Mixin").Op(":=").Qual(schemaPkg, t.Name).Values().Dot("Mixin").Call()
	}

	// 2. Generate policies initialization (creates Hooks[0] for policy evaluation)
	genRuntimePolicies(h, grp, t, schemaPkg, entityPkg, pkg)

	// 3. Generate hooks initialization
	genRuntimeHooks(h, grp, t, schemaPkg, entityPkg, pkg)

	// 4. Generate interceptors initialization
	genRuntimeInterceptors(h, grp, t, schemaPkg, entityPkg, pkg)

	// 5. Generate fields initialization (defaults, validators)
	if hasRuntimeFields {
		genRuntimeFields(h, grp, t, schemaPkg, entityPkg, pkg)
	}
}

// genRuntimePolicies generates the policies initialization for an entity.
// This sets the package-level RuntimePolicy variable that the entity client
// reads at construction time. Privacy evaluation is done explicitly by the
// entity client (not via Hooks[0]/Interceptors[0] positional slots).
func genRuntimePolicies(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, schemaPkg, entityPkg, pkg string) {
	policyPositions := t.PolicyPositions()
	if len(policyPositions) == 0 {
		return
	}

	// Create policy from mixins and schema
	mixedInPolicies := t.MixedInPolicies()
	args := make([]jen.Code, 0, len(mixedInPolicies)+1)
	for _, idx := range mixedInPolicies {
		args = append(args, jen.Id(pkg+"Mixin").Index(jen.Lit(idx)))
	}
	args = append(args, jen.Qual(schemaPkg, t.Name).Values())

	grp.Qual(entityPkg, "Policy").Op("=").Qual("github.com/syssam/velox/privacy", "NewPolicies").Call(args...)

	// Set RuntimePolicy to the same policy value. The entity client reads
	// this at construction time and stores it as a typed field.
	grp.Qual(entityPkg, "RuntimePolicy").Op("=").Qual(entityPkg, "Policy")
	// Register the policy in the runtime registry so cross-package edge
	// queries can look it up by entity name and wire it onto freshly-built
	// target queries (e.g. entity.User.QueryPosts() wires the Post policy).
	grp.Qual(runtimePkg, "RegisterEntityPolicy").Call(jen.Lit(t.Name), jen.Qual(entityPkg, "RuntimePolicy"))
}

// genRuntimeFields generates the fields initialization for an entity.
// Follows template order: mixin fields → entity fields → field processing
func genRuntimeFields(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, schemaPkg, entityPkg, pkg string) {
	// Load mixin fields if needed
	mixedInFieldIndices := t.MixedInFields()
	for _, mixinIdx := range mixedInFieldIndices {
		grp.Id(pkg + "MixinFields" + itoa(mixinIdx)).Op(":=").Id(pkg + "Mixin").Index(jen.Lit(mixinIdx)).Dot("Fields").Call()
		grp.Id("_").Op("=").Id(pkg + "MixinFields" + itoa(mixinIdx))
	}

	// Get fields to process (including ID if user-defined)
	// Template: $fields := $n.Fields; if $n.HasOneFieldID && $n.ID.UserDefined { $fields = append($fields, $n.ID) }
	fields := t.Fields
	if t.HasOneFieldID() && t.ID != nil && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}

	// Load entity fields if there are any
	// Template: {{ $pkg }}Fields := {{ $schema }}.{{ $n.Name }}{}.Fields()
	if len(fields) > 0 {
		grp.Id(pkg+"Fields").Op(":=").Qual(schemaPkg, t.Name).Values().Dot("Fields").Call()
		grp.Id("_").Op("=").Id(pkg + "Fields")
	}

	// Check if validators feature is enabled
	validatorsEnabled, _ := h.Graph().FeatureEnabled(gen.FeatureValidator.Name)

	// Process each field (including edge fields - they can have validators too)
	for _, field := range fields {
		// Enum defaults are wired here (same as non-enum defaults) when FeatureAutoDefault
		// is enabled. The original Ent skip was for template-era enum handling that is no
		// longer applicable with Jennifer codegen.
		hasDefault := field.Default
		hasUpdateDefault := field.UpdateDefault
		hasValidators := validatorsEnabled && (field.Validators > 0 || field.IsEnum())
		hasValueScanner := field.HasValueScanner()

		// Skip if no runtime code needed for this field
		if !hasDefault && !hasUpdateDefault && !hasValidators && !hasValueScanner {
			continue
		}

		fieldVar := pkg + "Desc" + field.StructField()

		// The descriptor is needed for defaults, updateDefaults, valueScanner, or
		// non-enum validators. Enum-only validators are generated inline without
		// referencing the descriptor.
		needsDescriptor := hasDefault || hasUpdateDefault || hasValueScanner || (hasValidators && field.Validators > 0)

		if needsDescriptor {
			// Generate descriptor assignment based on field position
			grp.Commentf("// %s is the schema descriptor for %s field.", fieldVar, field.Name)
			if field.Position != nil && field.Position.MixedIn {
				// Field comes from mixin
				grp.Id(fieldVar).Op(":=").Id(pkg + "MixinFields" + itoa(field.Position.MixinIndex)).
					Index(jen.Lit(field.Position.Index)).Dot("Descriptor").Call()
			} else {
				// Field comes from entity
				idx := 0
				if field.Position != nil {
					idx = field.Position.Index
				}
				grp.Id(fieldVar).Op(":=").Id(pkg + "Fields").Index(jen.Lit(idx)).Dot("Descriptor").Call()
			}
		}

		// Generate default value initialization
		if hasDefault {
			genRuntimeDefault(h, grp, t, field, fieldVar, entityPkg, pkg)
		}

		// Generate update default value initialization
		if hasUpdateDefault {
			genRuntimeUpdateDefault(h, grp, t, field, fieldVar, entityPkg, pkg)
		}

		// Generate ValueScanner initialization
		// Template: {{ $pkg }}.ValueScanner.{{ $f.StructField }} = {{ $desc }}.ValueScanner.(field.TypeValueScanner[{{ $f.Type }}])
		if hasValueScanner {
			grp.Qual(entityPkg, "ValueScanner").Dot(field.StructField()).Op("=").
				Id(fieldVar).Dot("ValueScanner").Assert(
				jen.Qual("github.com/syssam/velox/schema/field", "TypeValueScanner").
					Types(h.GoType(field)),
			)
		}

		// Generate validator initialization
		if hasValidators {
			genRuntimeValidator(h, grp, t, field, fieldVar, entityPkg, pkg)
		}
	}
}

// genRuntimeHooks generates the hooks initialization for an entity.
func genRuntimeHooks(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, schemaPkg, entityPkg, pkg string) {
	hookPositions := t.HookPositions()
	if len(hookPositions) == 0 {
		return
	}

	// Load hooks from mixins
	mixedInHooks := t.MixedInHooks()
	for _, mixinIdx := range mixedInHooks {
		grp.Id(pkg + "MixinHooks" + itoa(mixinIdx)).Op(":=").Id(pkg + "Mixin").Index(jen.Lit(mixinIdx)).Dot("Hooks").Call()
	}

	// Check if there are hooks defined directly in the schema (not from mixins)
	hasSchemaHooks := false
	for _, p := range hookPositions {
		if !p.MixedIn {
			hasSchemaHooks = true
			break
		}
	}

	if hasSchemaHooks {
		grp.Id(pkg+"Hooks").Op(":=").Qual(schemaPkg, t.Name).Values().Dot("Hooks").Call()
	}

	// Assign hooks to the entity's Hooks slice.
	// Privacy no longer reserves Hooks[0] — hooks start at index 0.
	for i, p := range hookPositions {
		if p.MixedIn {
			grp.Qual(entityPkg, "Hooks").Index(jen.Lit(i)).Op("=").
				Id(pkg + "MixinHooks" + itoa(p.MixinIndex)).Index(jen.Lit(p.Index))
		} else {
			grp.Qual(entityPkg, "Hooks").Index(jen.Lit(i)).Op("=").
				Id(pkg + "Hooks").Index(jen.Lit(p.Index))
		}
	}
}

// genRuntimeInterceptors generates the interceptors initialization for an entity.
func genRuntimeInterceptors(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, schemaPkg, entityPkg, pkg string) {
	interceptorPositions := t.InterceptorPositions()
	if len(interceptorPositions) == 0 {
		return
	}

	// Load interceptors from mixins
	mixedInInterceptors := t.MixedInInterceptors()
	for _, mixinIdx := range mixedInInterceptors {
		grp.Id(pkg + "MixinInters" + itoa(mixinIdx)).Op(":=").Id(pkg + "Mixin").Index(jen.Lit(mixinIdx)).Dot("Interceptors").Call()
	}

	// Check if there are interceptors defined directly in the schema (not from mixins)
	hasSchemaInterceptors := false
	for _, p := range interceptorPositions {
		if !p.MixedIn {
			hasSchemaInterceptors = true
			break
		}
	}

	if hasSchemaInterceptors {
		grp.Id(pkg+"Inters").Op(":=").Qual(schemaPkg, t.Name).Values().Dot("Interceptors").Call()
	}

	// Assign interceptors to the entity's Interceptors slice
	for i, p := range interceptorPositions {
		if p.MixedIn {
			grp.Qual(entityPkg, "Interceptors").Index(jen.Lit(i)).Op("=").
				Id(pkg + "MixinInters" + itoa(p.MixinIndex)).Index(jen.Lit(p.Index))
		} else {
			grp.Qual(entityPkg, "Interceptors").Index(jen.Lit(i)).Op("=").
				Id(pkg + "Inters").Index(jen.Lit(p.Index))
		}
	}
}

// itoa converts int to string
func itoa(i int) string {
	return strconv.Itoa(i)
}

// genRuntimeValidator generates the validator initialization for a field.
func genRuntimeValidator(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, fieldVar, entityPkg, pkg string) {
	structField := field.StructField()
	validatorVar := structField + "Validator"

	// Get the Go type for the validator function
	validatorType := getValidatorType(h, field)

	if field.IsEnum() && field.Validators == 0 {
		// Enum fields get an auto-generated IsValid() validator when FeatureValidator
		// is enabled, even without explicit schema validators.
		grp.Commentf("// %s.%s is a validator for the %q field. It is called by the builders before save.",
			pkg, validatorVar, field.Name)
		grp.Qual(entityPkg, validatorVar).Op("=").Func().Params(
			jen.Id("v").Id(field.SubpackageEnumTypeName()),
		).Error().Block(
			jen.If(jen.Op("!").Id("v").Dot("IsValid").Call()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(
					jen.Lit("invalid enum value for "+field.Name+": %v"),
					jen.Id("v"),
				)),
			),
			jen.Return(jen.Nil()),
		)
	} else if field.Validators == 1 {
		// Single validator - direct assignment
		grp.Commentf("// %s.%s is a validator for the %q field. It is called by the builders before save.",
			pkg, validatorVar, field.Name)
		grp.Qual(entityPkg, validatorVar).Op("=").
			Id(fieldVar).Dot("Validators").Index(jen.Lit(0)).
			Assert(jen.Func().Params(validatorType).Error())
	} else if field.Validators > 1 {
		// Multiple validators - create combined function using auto-sized array
		grp.Commentf("// %s.%s is a validator for the %q field. It is called by the builders before save.",
			pkg, validatorVar, field.Name)
		grp.Qual(entityPkg, validatorVar).Op("=").Func().Params().Func().Params(validatorType).Error().Block(
			jen.Id("validators").Op(":=").Id(fieldVar).Dot("Validators"),
			// Use [...]func for auto-sized array like Ent does
			// Use CustomFunc with Multi:true to get each validator on its own line
			jen.Id("fns").Op(":=").Index(jen.Op("...")).Func().Params(validatorType).Error().CustomFunc(jen.Options{
				Open:      "{",
				Close:     "}",
				Separator: ",",
				Multi:     true,
			}, func(vals *jen.Group) {
				for i := 0; i < field.Validators; i++ {
					vals.Id("validators").Index(jen.Lit(i)).Assert(jen.Func().Params(validatorType).Error())
				}
			}),
			jen.Return(jen.Func().Params(jen.Id(field.BuilderField()).Add(validatorType)).Error().Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("fn")).Op(":=").Range().Id("fns")).Block(
					jen.If(jen.Id("err").Op(":=").Id("fn").Call(jen.Id(field.BuilderField())), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Id("err")),
					),
				),
				jen.Return(jen.Nil()),
			)),
		).Call()
	}
}

// genRuntimeDefault generates the default value initialization for a field.
// Follows template exactly:
//
//	{{- $defaultType := print $f.Type.Type }}{{ if $f.DefaultFunc }}{{ $defaultType = print "func() " $f.Type }}{{ end }}
//	{{- if and $f.HasGoType (not (hasPrefix $defaultType "func")) }}
//	    {{- if or $f.IsJSON $f.IsOther }}
//	        {{ $default }} = {{ $desc }}.Default.({{ $f.Type }})
//	    {{- else }}
//	        {{ $default }} = {{ $f.Type }}({{ $desc }}.Default.({{ $defaultType }}))
//	    {{- end }}
//	{{- else }}
//	    {{ $default }} = {{ $desc }}.Default.({{ $defaultType }})
//	{{- end }}
func genRuntimeDefault(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, fieldVar, entityPkg, pkg string) {
	structField := field.StructField()
	defaultVar := "Default" + structField

	grp.Commentf("// %s.%s holds the default value on creation for the %s field.",
		pkg, defaultVar, field.Name)

	if field.DefaultFunc() {
		// Default is a function - assert to function type: func() {Type}
		grp.Qual(entityPkg, defaultVar).Op("=").Id(fieldVar).Dot("Default").Assert(
			jen.Func().Params().Add(h.GoType(field)),
		)
	} else if field.HasGoType() {
		// HasGoType and NOT a function
		if field.IsJSON() || field.IsOther() {
			// JSON or Other: direct assertion to full type
			grp.Qual(entityPkg, defaultVar).Op("=").Id(fieldVar).Dot("Default").Assert(h.GoType(field))
		} else {
			// Custom type with underlying basic type: type conversion
			// e.g., MyType(desc.Default.(string))
			grp.Qual(entityPkg, defaultVar).Op("=").Add(h.GoType(field)).Call(
				jen.Id(fieldVar).Dot("Default").Assert(getBaseType(field)),
			)
		}
	} else if field.IsEnum() {
		// Enum type: descriptor stores the underlying string, convert to enum type.
		// e.g., DefaultStatus = Status(desc.Default.(string))
		grp.Qual(entityPkg, defaultVar).Op("=").Add(h.GoType(field)).Call(
			jen.Id(fieldVar).Dot("Default").Assert(jen.String()),
		)
	} else {
		// Standard type: direct assertion
		grp.Qual(entityPkg, defaultVar).Op("=").Id(fieldVar).Dot("Default").Assert(h.GoType(field))
	}
}

// genRuntimeUpdateDefault generates the update default value initialization for a field.
func genRuntimeUpdateDefault(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, fieldVar, entityPkg, pkg string) {
	structField := field.StructField()
	updateDefaultVar := "Update" + "Default" + structField

	grp.Commentf("// %s.%s holds the default value on update for the %s field.",
		pkg, updateDefaultVar, field.Name)

	// Update default is always a function
	grp.Qual(entityPkg, updateDefaultVar).Op("=").Id(fieldVar).Dot("UpdateDefault").Assert(
		jen.Func().Params().Add(h.GoType(field)),
	)
}

// baseTypeCode returns the Jennifer code for a field's scalar base type.
// Maps field.Type.Type string names to their corresponding jen.Code.
func baseTypeCode(field *gen.Field) jen.Code {
	if field.Type == nil {
		return jen.Any()
	}
	switch field.Type.Type.String() {
	case "string", "enum":
		return jen.String()
	case "int":
		return jen.Int()
	case "int8":
		return jen.Int8()
	case "int16":
		return jen.Int16()
	case "int32":
		return jen.Int32()
	case "int64":
		return jen.Int64()
	case "uint":
		return jen.Uint()
	case "uint8":
		return jen.Uint8()
	case "uint16":
		return jen.Uint16()
	case "uint32":
		return jen.Uint32()
	case "uint64":
		return jen.Uint64()
	case "float32":
		return jen.Float32()
	case "float64":
		return jen.Float64()
	case "bool":
		return jen.Bool()
	default:
		return jen.Any()
	}
}

// getBaseType returns the Jennifer code for a field's base type (Type.Type).
// This is used for type assertions when there's a custom Go type.
func getBaseType(field *gen.Field) jen.Code {
	return baseTypeCode(field)
}

// getValidatorType returns the Jennifer code for a validator's parameter type.
// For JSON fields, uses the full Go type; for others, uses the scalar base type.
func getValidatorType(h gen.GeneratorHelper, field *gen.Field) jen.Code {
	if field.Type == nil {
		return jen.Any()
	}
	// For JSON fields, use the full type (e.g., map[string]any)
	// instead of the base type (json)
	if field.IsJSON() {
		return h.GoType(field)
	}
	return baseTypeCode(field)
}

// genPredicatePackage generates the predicate/predicate.go file.
func genPredicatePackage(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("predicate")
	graph := h.Graph()

	f.Comment("Package predicate contains type definitions for all predicates.")

	// Predicate type for each entity
	for _, t := range graph.Nodes {
		f.Commentf("%s is the predicate function for %s builders.", t.Name, t.Name)
		f.Type().Id(t.Name).Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))
	}

	return f
}
