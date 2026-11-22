package codegen

// rule is one of the schema rules velox-gen refuses a schema for: what it
// refuses, and the test that covers it.
type rule struct {
	summary string
	test    string
}

// rules indexes the numbers b.fail reports, which order a schema's errors
// and which comments cite as "rule N". Rule 70 is edgeSchemaBehaviour's,
// which does not go through b.fail. A number that went is not reused.
// TestRuleTable checks that every b.fail number and every "rule N" in a
// comment is here, and that every test named exists.
var rules = map[int]rule{
	1:  {"a field is declared twice", "TestBuildRules"},
	2:  {"two fields, or a field and an implicit foreign key, share a column", "TestBuildRules"},
	3:  {"an edge is declared twice", "TestBuildRules"},
	4:  {"an edge's Go name is a field's", "TestBuildRules"},
	5:  {"an edge's type is not a schema of the package", "TestBuildRules"},
	6:  {"an inverse edge has no Ref", "TestBuildRules"},
	7:  {"Ref names no edge of the target", "TestBuildRules"},
	8:  {"Ref names an edge that points elsewhere", "TestBuildRules"},
	9:  {"an edge is Required in both directions", "TestBuildRules"},
	10: {"an edge binds a field that is not declared", "TestBuildRules"},
	11: {"an edge binds a field on the side without the foreign key", "TestBuildRules"},
	12: {"a bound foreign-key field is not of the target ID's type", "TestBuildRules"},
	13: {"a bound field is Optional when the edge is Required, or the reverse", "TestBuildRules"},
	18: {"an enum has no values", "TestBuildRules"},
	19: {"an enum value is given twice", "TestBuildRules"},
	20: {"an enum value has no Go name", "TestBuildRules"},
	21: {"the ID is optional, Nillable, updated by default, or an integer with a database default", "TestBuildRules"},
	22: {"the ID is of a type an ID cannot be", "TestBuildRules"},
	23: {"an index names an unknown field or edge", "TestBuildRules"},
	24: {"an index names an edge with no column in the table", "TestBuildRules"},
	25: {"an index names nothing", "TestBuildRules"},
	26: {"an Other field has no SchemaType", "TestBuildRules"},
	27: {"a join table is another schema's table", "TestBuildRules"},
	28: {"an inline From is on an edge to another schema", "TestBuildRules"},
	29: {"edge.Table is on an edge that is not many-to-many", "TestBuildRules"},
	30: {"a Go name is not an identifier, or is shared", "TestBuildRules"},
	31: {"an edge's column differs from its bound field's", "TestBuildRules"},
	32: {"storage keys are on both edges, or have the wrong number of columns", "TestBuildRules"},
	33: {"an assoc edge has a second inverse", "TestBuildRules"},
	34: {"an Immutable edge binds a mutable field", "TestBuildRules"},
	35: {"a join table's two columns share a name", "TestBuildRules"},
	37: {"an Optional enum has neither a Default nor Nillable", "TestBuildRules"},
	38: {"an optional edge binds a field that is not Nillable", "TestBuildRules"},
	41: {"a JSON field is Unique", "TestJSONIndexRefused"},
	42: {"an index covers a JSON field and is not GIN", "TestJSONIndexRefused"},
	43: {"an index repeats another", "TestDuplicateIndexes"},
	44: {"two objects share a name in one namespace", "TestBuildRules"},
	45: {"a many-to-many edge has OnDelete", "TestOnDelete"},
	46: {"OnDelete SET NULL on a foreign key that is not NULL-able", "TestOnDelete"},
	47: {"the two edges of a relation have different OnDelete", "TestOnDelete"},
	49: {"a JSON field's Go type cannot be encoded", "TestBuildRules"},
	50: {"a database name is too long", "TestBuildRules"},
	51: {"edge.Symbol or Symbols has the wrong number of names, or differs from the inverse's", "TestBuildRules"},
	52: {"enum fields of one type have different values", "TestBuildRules"},
	53: {"a bidirectional one-to-one edge is Required", "TestBuildRules"},
	54: {"an Optional field is Unique without Nillable", "TestBuildUniqueOptionalRefused"},
	55: {"two edges bind one foreign-key field", "TestBuildTwoEdgesOnOneKeyRefused"},
	56: {"a schema, field or edge name is not ASCII", "TestBuildNonASCIINamesRefused"},
	57: {"a schema has two Version fields", "TestBuildRules"},
	58: {"an index names an unknown access method", "TestIndexMethodRules"},
	59: {"a Unique index has a method other than btree", "TestIndexMethodRules"},
	60: {"sqlschema.Desc or OpClass names a column the index does not cover", "TestIndexKeysRules"},
	61: {"sqlschema.Desc is on an index other than btree", "TestIndexKeysRules"},
	62: {"an operator class is not a name, or a column has two", "TestIndexKeysRules"},
	63: {"Through is on an edge that is not many-to-many", "TestEdgeSchemaRules"},
	64: {"Through names no schema, its own or its target's", "TestEdgeSchemaRules"},
	65: {"Through has no name, or one another field or edge has", "TestEdgeSchemaRules"},
	66: {"an edge schema is used by two relations", "TestEdgeSchemaRules"},
	67: {"an edge schema lacks the key edges or the unique index Through needs", "TestEdgeSchemaNonUniqueIndex"},
	68: {"an edge through an edge schema has storage keys", "TestEdgeSchemaRules"},
	69: {"Through is on a bidirectional edge", "TestEdgeSchemaRules"},
	70: {"an edge schema narrows reads: a Read policy, interceptors or soft delete", "TestEdgeSchemaBehaviourRefused"},
}
