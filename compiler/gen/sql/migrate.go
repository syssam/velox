// Package sql provides SQL dialect code generation.
package sql

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"
)

// genMigrate generates the migrate package with schema definitions and migration support.
// This creates two files:
//   - migrate/schema.go: Table and column definitions
//   - migrate/migrate.go: Schema type with Create() method
func genMigrate(h gen.GeneratorHelper) (gen.MigrateFiles, error) {
	schemaFile, err := genMigrateSchema(h)
	if err != nil {
		return gen.MigrateFiles{}, err
	}
	return gen.MigrateFiles{
		Schema:  schemaFile,
		Migrate: genMigrateMigrate(h),
	}, nil
}

// migrateSchemaPkg is the import path of the migration schema package
// referenced throughout the generated migrate/schema.go. (Not to be confused
// with helper.go's schemaPkg(), which returns the schema/field package.)
const migrateSchemaPkg = "github.com/syssam/velox/dialect/sql/schema"

// genMigrateSchema generates migrate/schema.go with table definitions.
//
// It renders the tables Graph.Tables builds and builds none of its own. A
// second builder here drifted from Graph.Tables for years: a one-way O2M
// edge lost its foreign-key column, an optional edge field became NOT NULL
// with ON DELETE SET NULL, O2O edge fields lost UNIQUE, composite-key edge
// schemas panicked at init, views were created as tables, and Through on
// the inverse edge emitted the join table twice. Change migrations in
// Graph.Tables.
func genMigrateSchema(h gen.GeneratorHelper) (*jen.File, error) {
	f := h.NewFile("migrate")
	schemaPkg := migrateSchemaPkg
	fieldPkg := "github.com/syssam/velox/schema/field"
	f.ImportName(schemaPkg, "schema")
	f.ImportName(fieldPkg, "field")

	graph := h.Graph()
	tables, err := graph.Tables()
	if err != nil {
		return nil, fmt.Errorf("velox/gen: migrate schema: %w", err)
	}

	// Entity tables keep the node's name (UserTable/UserColumns), join
	// tables their table name in PascalCase (UserGroupsTable).
	nodeVar := make(map[string]string, len(graph.Nodes))
	for _, t := range graph.Nodes {
		if !t.IsView() {
			nodeVar[t.Table()] = pascal(t.Name)
		}
	}
	varOf := make(map[*schema.Table]string, len(tables))
	for _, t := range tables {
		name, ok := nodeVar[t.Name]
		if !ok {
			parts := strings.Split(t.Name, "_")
			for i, p := range parts {
				parts[i] = pascal(p)
			}
			name = strings.Join(parts, "")
		}
		varOf[t] = name
	}
	// colRef renders a reference to column c of table t by its position in
	// the table's columns slice, so every table shares one *Column value.
	colRef := func(t *schema.Table, c *schema.Column) (jen.Code, error) {
		for i, tc := range t.Columns {
			if tc == c {
				return jen.Id(varOf[t] + "Columns").Index(jen.Lit(i)), nil
			}
		}
		return nil, fmt.Errorf("velox/gen: migrate schema: column %q is not a column of table %q", c.Name, t.Name)
	}
	colRefs := func(t *schema.Table, cs []*schema.Column) (jen.Code, error) {
		refs := make([]jen.Code, 0, len(cs))
		for _, c := range cs {
			r, err := colRef(t, c)
			if err != nil {
				return nil, err
			}
			refs = append(refs, r)
		}
		return jen.Index().Op("*").Qual(schemaPkg, "Column").Values(refs...), nil
	}

	for _, t := range tables {
		columnsVar, tableVar := varOf[t]+"Columns", varOf[t]+"Table"
		f.Comment("// " + columnsVar + " holds the columns for the \"" + t.Name + "\" table.")
		// Elements are bare composite literals ({...}, not &schema.Column{...})
		// so the output is already gofmt -s simplified — otherwise the regen
		// script's format pass and the generator ping-pong the file forever.
		f.Var().Id(columnsVar).Op("=").Index().Op("*").Qual(schemaPkg, "Column").ValuesFunc(func(g *jen.Group) {
			for _, c := range t.Columns {
				g.Values(genColumnDict(c, fieldPkg))
			}
		})
		f.Line()

		pk, err := colRefs(t, t.PrimaryKey)
		if err != nil {
			return nil, err
		}
		tableDict := jen.Dict{
			jen.Id("Name"):       jen.Lit(t.Name),
			jen.Id("Columns"):    jen.Id(columnsVar),
			jen.Id("PrimaryKey"): pk,
		}
		if len(t.ForeignKeys) > 0 {
			fks := make([]jen.Code, 0, len(t.ForeignKeys))
			for _, fk := range t.ForeignKeys {
				cols, err := colRefs(t, fk.Columns)
				if err != nil {
					return nil, err
				}
				refCols, err := colRefs(fk.RefTable, fk.RefColumns)
				if err != nil {
					return nil, err
				}
				d := jen.Dict{
					jen.Id("Symbol"):     jen.Lit(fk.Symbol),
					jen.Id("Columns"):    cols,
					jen.Id("RefColumns"): refCols,
				}
				if fk.OnDelete != "" {
					d[jen.Id("OnDelete")] = referenceOption(fk.OnDelete)
				}
				if fk.OnUpdate != "" {
					d[jen.Id("OnUpdate")] = referenceOption(fk.OnUpdate)
				}
				fks = append(fks, jen.Values(d))
			}
			tableDict[jen.Id("ForeignKeys")] = jen.Index().Op("*").Qual(schemaPkg, "ForeignKey").Values(fks...)
		}
		if len(t.Indexes) > 0 {
			const sqlschemaPkg = "github.com/syssam/velox/dialect/sqlschema"
			idxs := make([]jen.Code, 0, len(t.Indexes))
			for _, idx := range t.Indexes {
				cols, err := colRefs(t, idx.Columns)
				if err != nil {
					return nil, err
				}
				d := jen.Dict{
					jen.Id("Name"):    jen.Lit(idx.Name),
					jen.Id("Unique"):  jen.Lit(idx.Unique),
					jen.Id("Columns"): cols,
				}
				if ant := idx.Annotation; ant != nil {
					if antDict := genIndexAnnotationDict(ant); len(antDict) > 0 {
						d[jen.Id("Annotation")] = jen.Op("&").Qual(sqlschemaPkg, "IndexAnnotation").Values(antDict)
					}
				}
				idxs = append(idxs, jen.Values(d))
			}
			tableDict[jen.Id("Indexes")] = jen.Index().Op("*").Qual(schemaPkg, "Index").Values(idxs...)
		}
		if t.Comment != "" {
			tableDict[jen.Id("Comment")] = jen.Lit(t.Comment)
		}
		if t.Schema != "" {
			tableDict[jen.Id("Schema")] = jen.Lit(t.Schema)
		}
		if ant := t.Annotation; ant != nil {
			if antDict := genTableAnnotationDict(ant); len(antDict) > 0 {
				const sqlschemaPkg = "github.com/syssam/velox/dialect/sqlschema"
				tableDict[jen.Id("Annotation")] = jen.Op("&").Qual(sqlschemaPkg, "Annotation").Values(antDict)
			}
		}
		f.Comment("// " + tableVar + " holds the schema information for the \"" + t.Name + "\" table.")
		f.Var().Id(tableVar).Op("=").Op("&").Qual(schemaPkg, "Table").Values(tableDict)
		f.Line()
	}

	f.Comment("// Tables holds all the tables in the schema.")
	f.Var().Id("Tables").Op("=").Index().Op("*").Qual(schemaPkg, "Table").ValuesFunc(func(g *jen.Group) {
		for _, t := range tables {
			g.Id(varOf[t] + "Table")
		}
	})
	f.Line()

	// RefTable is set in init: a table referencing itself, or two tables
	// referencing each other, would be an initialization cycle as literals.
	f.Func().Id("init").Params().BlockFunc(func(g *jen.Group) {
		for _, t := range tables {
			for i, fk := range t.ForeignKeys {
				g.Id(varOf[t] + "Table").Dot("ForeignKeys").Index(jen.Lit(i)).Dot("RefTable").Op("=").Id(varOf[fk.RefTable] + "Table")
			}
		}
	})
	return f, nil
}

// referenceOption renders a referential action as its Go constant
// (schema.SetNull). The SQL literal ("SET NULL") is not a Go identifier.
func referenceOption(o schema.ReferenceOption) jen.Code {
	return jen.Qual(migrateSchemaPkg, o.ConstName())
}

// genMigrateMigrate generates migrate/migrate.go with the Schema type.
func genMigrateMigrate(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("migrate")
	f.HeaderComment("Code generated by velox. DO NOT EDIT.")

	// Imports
	dialectPkg := "github.com/syssam/velox/dialect"
	schemaPkg := "github.com/syssam/velox/dialect/sql/schema"
	f.ImportName("context", "context")
	f.ImportName("fmt", "fmt")
	f.ImportName("io", "io")
	f.ImportName(dialectPkg, "dialect")
	f.ImportName(schemaPkg, "schema")

	// Export schema options
	f.Var().DefsFunc(func(g *jen.Group) {
		g.Comment("// WithGlobalUniqueID sets the universal ids options to the migration.")
		g.Id("WithGlobalUniqueID").Op("=").Qual(schemaPkg, "WithGlobalUniqueID")
		g.Comment("// WithDropColumn sets the drop column option to the migration.")
		g.Id("WithDropColumn").Op("=").Qual(schemaPkg, "WithDropColumn")
		g.Comment("// WithDropIndex sets the drop index option to the migration.")
		g.Id("WithDropIndex").Op("=").Qual(schemaPkg, "WithDropIndex")
		g.Comment("// WithForeignKeys enables creating foreign-key in schema DDL.")
		g.Id("WithForeignKeys").Op("=").Qual(schemaPkg, "WithForeignKeys")
	})
	f.Line()

	// Schema struct
	f.Comment("// Schema is the API for creating, migrating and dropping a schema.")
	f.Type().Id("Schema").Struct(
		jen.Id("drv").Qual(dialectPkg, "Driver"),
	)
	f.Line()

	// NewSchema constructor
	f.Comment("// NewSchema creates a new schema client.")
	f.Func().Id("NewSchema").Params(
		jen.Id("drv").Qual(dialectPkg, "Driver"),
	).Op("*").Id("Schema").Block(
		jen.Return(jen.Op("&").Id("Schema").Values(jen.Dict{
			jen.Id("drv"): jen.Id("drv"),
		})),
	)
	f.Line()

	// Create method
	f.Comment("// Create creates all schema resources.")
	f.Func().Params(
		jen.Id("s").Op("*").Id("Schema"),
	).Id("Create").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("opts").Op("...").Qual(schemaPkg, "MigrateOption"),
	).Error().Block(
		jen.Return(jen.Id("Create").Call(
			jen.Id("ctx"),
			jen.Id("s"),
			jen.Id("Tables"),
			jen.Id("opts").Op("..."),
		)),
	)
	f.Line()

	// Package-level Create function
	f.Comment("// Create creates all table resources using the given schema driver.")
	f.Func().Id("Create").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("s").Op("*").Id("Schema"),
		jen.Id("tables").Index().Op("*").Qual(schemaPkg, "Table"),
		jen.Id("opts").Op("...").Qual(schemaPkg, "MigrateOption"),
	).Error().Block(
		jen.List(jen.Id("migrate"), jen.Id("err")).Op(":=").Qual(schemaPkg, "NewMigrate").Call(
			jen.Id("s").Dot("drv"),
			jen.Id("opts").Op("..."),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("velox/migrate: %w"), jen.Id("err"))),
		),
		jen.Return(jen.Id("migrate").Dot("Create").Call(jen.Id("ctx"), jen.Id("tables").Op("..."))),
	)
	f.Line()

	// WriteTo method
	f.Comment("// WriteTo writes the schema changes to w instead of running them against the database.")
	f.Func().Params(
		jen.Id("s").Op("*").Id("Schema"),
	).Id("WriteTo").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("w").Qual("io", "Writer"),
		jen.Id("opts").Op("...").Qual(schemaPkg, "MigrateOption"),
	).Error().Block(
		jen.Return(jen.Id("Create").Call(
			jen.Id("ctx"),
			jen.Op("&").Id("Schema").Values(jen.Dict{
				jen.Id("drv"): jen.Op("&").Qual(schemaPkg, "WriteDriver").Values(jen.Dict{
					jen.Id("Writer"): jen.Id("w"),
					jen.Id("Driver"): jen.Id("s").Dot("drv"),
				}),
			}),
			jen.Id("Tables"),
			jen.Id("opts").Op("..."),
		)),
	)

	return f
}

// genColumnDict generates a jen.Dict for a schema.Column.
func genColumnDict(col *schema.Column, fieldPkg string) jen.Dict {
	dict := jen.Dict{
		jen.Id("Name"): jen.Lit(col.Name),
		jen.Id("Type"): fieldTypeCode(col.Type, fieldPkg),
	}
	if col.Unique {
		dict[jen.Id("Unique")] = jen.True()
	}
	if col.Increment {
		dict[jen.Id("Increment")] = jen.True()
	}
	if col.Nullable {
		dict[jen.Id("Nullable")] = jen.True()
	}
	if col.Size != 0 {
		dict[jen.Id("Size")] = jen.Lit(col.Size)
	}
	if len(col.Enums) > 0 {
		dict[jen.Id("Enums")] = jen.Index().String().ValuesFunc(func(g *jen.Group) {
			for _, e := range col.Enums {
				g.Lit(e)
			}
		})
	}
	if col.Default != nil {
		switch v := col.Default.(type) {
		case string:
			dict[jen.Id("Default")] = jen.Lit(v)
		case int, int64, float64, bool:
			dict[jen.Id("Default")] = jen.Lit(v)
		case schema.Expr:
			dict[jen.Id("Default")] = jen.Qual("github.com/syssam/velox/dialect/sql/schema", "Expr").Call(jen.Lit(string(v)))
		case map[string]schema.Expr:
			// Dialect-specific SQL expression defaults (sqlschema.DefaultExprs).
			exprKeys := make([]string, 0, len(v))
			for k := range v {
				exprKeys = append(exprKeys, k)
			}
			sort.Strings(exprKeys)
			exprElems := make([]jen.Code, 0, len(exprKeys))
			for _, k := range exprKeys {
				exprElems = append(exprElems, jen.Lit(k).Op(":").Qual("github.com/syssam/velox/dialect/sql/schema", "Expr").Call(jen.Lit(string(v[k]))))
			}
			dict[jen.Id("Default")] = jen.Map(jen.String()).Qual("github.com/syssam/velox/dialect/sql/schema", "Expr").Values(exprElems...)
		}
	}
	if col.Collation != "" {
		dict[jen.Id("Collation")] = jen.Lit(col.Collation)
	}
	if col.Attr != "" {
		dict[jen.Id("Attr")] = jen.Lit(col.Attr)
	}
	if col.Comment != "" {
		dict[jen.Id("Comment")] = jen.Lit(col.Comment)
	}
	// Include SchemaType for TypeOther fields (like decimal) that need dialect-specific types.
	// Iterate in sorted order for deterministic generated output.
	if len(col.SchemaType) > 0 {
		stKeys := make([]string, 0, len(col.SchemaType))
		for k := range col.SchemaType {
			stKeys = append(stKeys, k)
		}
		sort.Strings(stKeys)
		stElems := make([]jen.Code, 0, len(stKeys))
		for _, k := range stKeys {
			stElems = append(stElems, jen.Lit(k).Op(":").Lit(col.SchemaType[k]))
		}
		dict[jen.Id("SchemaType")] = jen.Map(jen.String()).String().Values(stElems...)
	}
	return dict
}

// genIndexAnnotationDict builds a jen.Dict for the non-zero fields of an IndexAnnotation.
// Returns an empty dict when the annotation carries no meaningful data.
func genIndexAnnotationDict(ant *sqlschema.IndexAnnotation) jen.Dict {
	d := jen.Dict{}
	if ant.Where != "" {
		d[jen.Id("Where")] = jen.Lit(ant.Where)
	}
	if ant.Type != "" {
		d[jen.Id("Type")] = jen.Lit(ant.Type)
	}
	if ant.StorageParams != "" {
		d[jen.Id("StorageParams")] = jen.Lit(ant.StorageParams)
	}
	if ant.Desc {
		d[jen.Id("Desc")] = jen.Lit(true)
	}
	if ant.OpClass != "" {
		d[jen.Id("OpClass")] = jen.Lit(ant.OpClass)
	}
	if ant.Prefix > 0 {
		d[jen.Id("Prefix")] = jen.Lit(ant.Prefix)
	}
	if len(ant.Types) > 0 {
		keys := make([]string, 0, len(ant.Types))
		for k := range ant.Types {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mapElems := make([]jen.Code, 0, len(keys))
		for _, k := range keys {
			mapElems = append(mapElems, jen.Lit(k).Op(":").Lit(ant.Types[k]))
		}
		d[jen.Id("Types")] = jen.Map(jen.String()).String().Values(mapElems...)
	}
	if len(ant.DescColumns) > 0 {
		keys := make([]string, 0, len(ant.DescColumns))
		for k := range ant.DescColumns {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mapElems := make([]jen.Code, 0, len(keys))
		for _, k := range keys {
			mapElems = append(mapElems, jen.Lit(k).Op(":").Lit(ant.DescColumns[k]))
		}
		d[jen.Id("DescColumns")] = jen.Map(jen.String()).Bool().Values(mapElems...)
	}
	if len(ant.OpClassColumns) > 0 {
		keys := make([]string, 0, len(ant.OpClassColumns))
		for k := range ant.OpClassColumns {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mapElems := make([]jen.Code, 0, len(keys))
		for _, k := range keys {
			mapElems = append(mapElems, jen.Lit(k).Op(":").Lit(ant.OpClassColumns[k]))
		}
		d[jen.Id("OpClassColumns")] = jen.Map(jen.String()).String().Values(mapElems...)
	}
	if len(ant.PrefixColumns) > 0 {
		keys := make([]string, 0, len(ant.PrefixColumns))
		for k := range ant.PrefixColumns {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mapElems := make([]jen.Code, 0, len(keys))
		for _, k := range keys {
			mapElems = append(mapElems, jen.Lit(k).Op(":").Lit(ant.PrefixColumns[k]))
		}
		d[jen.Id("PrefixColumns")] = jen.Map(jen.String()).Uint().Values(mapElems...)
	}
	if len(ant.IncludeColumns) > 0 {
		cols := make([]jen.Code, len(ant.IncludeColumns))
		for i, c := range ant.IncludeColumns {
			cols[i] = jen.Lit(c)
		}
		d[jen.Id("IncludeColumns")] = jen.Index().String().Values(cols...)
	}
	return d
}

// fieldTypeCode returns the Jennifer code for a field type constant.
func fieldTypeCode(ft field.Type, fieldPkg string) jen.Code {
	switch ft {
	case field.TypeBool:
		return jen.Qual(fieldPkg, "TypeBool")
	case field.TypeTime:
		return jen.Qual(fieldPkg, "TypeTime")
	case field.TypeJSON:
		return jen.Qual(fieldPkg, "TypeJSON")
	case field.TypeUUID:
		return jen.Qual(fieldPkg, "TypeUUID")
	case field.TypeBytes:
		return jen.Qual(fieldPkg, "TypeBytes")
	case field.TypeEnum:
		return jen.Qual(fieldPkg, "TypeEnum")
	case field.TypeString:
		return jen.Qual(fieldPkg, "TypeString")
	case field.TypeOther:
		return jen.Qual(fieldPkg, "TypeOther")
	case field.TypeInt:
		return jen.Qual(fieldPkg, "TypeInt")
	case field.TypeInt8:
		return jen.Qual(fieldPkg, "TypeInt8")
	case field.TypeInt16:
		return jen.Qual(fieldPkg, "TypeInt16")
	case field.TypeInt32:
		return jen.Qual(fieldPkg, "TypeInt32")
	case field.TypeInt64:
		return jen.Qual(fieldPkg, "TypeInt64")
	case field.TypeUint:
		return jen.Qual(fieldPkg, "TypeUint")
	case field.TypeUint8:
		return jen.Qual(fieldPkg, "TypeUint8")
	case field.TypeUint16:
		return jen.Qual(fieldPkg, "TypeUint16")
	case field.TypeUint32:
		return jen.Qual(fieldPkg, "TypeUint32")
	case field.TypeUint64:
		return jen.Qual(fieldPkg, "TypeUint64")
	case field.TypeFloat32:
		return jen.Qual(fieldPkg, "TypeFloat32")
	case field.TypeFloat64:
		return jen.Qual(fieldPkg, "TypeFloat64")
	default:
		return jen.Qual(fieldPkg, "TypeString")
	}
}

// pascal converts a string to PascalCase.
func pascal(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// fkSymbolForEdge returns the FK constraint symbol for O2O/O2M/M2O edges,
// matching the runtime fkSymbol() in graph_tables.go and Ent (entc/gen/graph.go).
//

// genTableAnnotationDict builds a jen.Dict for the non-zero annotation fields that
// Atlas needs at the table level: CHECK constraints, charset, collation, options (RISK 7).
// Schema is handled separately as a direct Table.Schema field.
func genTableAnnotationDict(ant *sqlschema.Annotation) jen.Dict {
	d := jen.Dict{}
	if ant.Check != "" {
		d[jen.Id("Check")] = jen.Lit(ant.Check)
	}
	if len(ant.Checks) > 0 {
		keys := make([]string, 0, len(ant.Checks))
		for k := range ant.Checks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		elems := make([]jen.Code, 0, len(keys))
		for _, k := range keys {
			elems = append(elems, jen.Lit(k).Op(":").Lit(ant.Checks[k]))
		}
		d[jen.Id("Checks")] = jen.Map(jen.String()).String().Values(elems...)
	}
	if ant.Charset != "" {
		d[jen.Id("Charset")] = jen.Lit(ant.Charset)
	}
	if ant.Collation != "" {
		d[jen.Id("Collation")] = jen.Lit(ant.Collation)
	}
	if ant.Options != "" {
		d[jen.Id("Options")] = jen.Lit(ant.Options)
	}
	return d
}
