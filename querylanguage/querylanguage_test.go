package querylanguage_test

import (
	"strconv"
	"testing"

	"github.com/syssam/velox/querylanguage"

	"github.com/stretchr/testify/assert"
)

func TestPString(t *testing.T) {
	tests := []struct {
		P querylanguage.P
		S string
	}{
		{
			P: querylanguage.And(
				querylanguage.FieldEQ("name", "a8m"),
				querylanguage.FieldIn("org", "fb", "ent"),
			),
			S: `name == "a8m" && org in ["fb","ent"]`,
		},
		{
			P: querylanguage.Or(
				querylanguage.Not(querylanguage.FieldEQ("name", "mashraki")),
				querylanguage.FieldIn("org", "fb", "ent"),
			),
			S: `!(name == "mashraki") || org in ["fb","ent"]`,
		},
		{
			P: querylanguage.HasEdgeWith(
				"groups",
				querylanguage.HasEdgeWith(
					"admins",
					querylanguage.Not(querylanguage.FieldEQ("name", "a8m")),
				),
			),
			S: `has_edge(groups, has_edge(admins, !(name == "a8m")))`,
		},
		{
			P: querylanguage.And(
				querylanguage.FieldGT("age", 30),
				querylanguage.FieldContains("workplace", "fb"),
			),
			S: `age > 30 && contains(workplace, "fb")`,
		},
		{
			P: querylanguage.Not(querylanguage.FieldLT("score", 32.23)),
			S: `!(score < 32.23)`,
		},
		{
			P: querylanguage.And(
				querylanguage.FieldNil("active"),
				querylanguage.FieldNotNil("name"),
			),
			S: `active == nil && name != nil`,
		},
		{
			P: querylanguage.Or(
				querylanguage.FieldNotIn("id", 1, 2, 3),
				querylanguage.FieldHasSuffix("name", "admin"),
			),
			S: `id not in [1,2,3] || has_suffix(name, "admin")`,
		},
		{
			P: querylanguage.EQ(querylanguage.F("current"), querylanguage.F("total")).Negate(),
			S: `!(current == total)`,
		},
	}
	for i := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			s := tests[i].P.String()
			assert.Equal(t, tests[i].S, s)
		})
	}
}

func TestFieldPredicates(t *testing.T) {
	tests := []struct {
		name string
		P    querylanguage.P
		S    string
	}{
		{
			name: "FieldNEQ",
			P:    querylanguage.FieldNEQ("status", "active"),
			S:    `status != "active"`,
		},
		{
			name: "FieldGTE",
			P:    querylanguage.FieldGTE("age", 18),
			S:    `age >= 18`,
		},
		{
			name: "FieldLTE",
			P:    querylanguage.FieldLTE("price", 100),
			S:    `price <= 100`,
		},
		{
			name: "FieldContainsFold",
			P:    querylanguage.FieldContainsFold("name", "john"),
			S:    `contains_fold(name, "john")`,
		},
		{
			name: "FieldEqualFold",
			P:    querylanguage.FieldEqualFold("email", "TEST@EXAMPLE.COM"),
			S:    `equal_fold(email, "TEST@EXAMPLE.COM")`,
		},
		{
			name: "FieldHasPrefix",
			P:    querylanguage.FieldHasPrefix("path", "/api/"),
			S:    `has_prefix(path, "/api/")`,
		},
		{
			name: "HasEdge",
			P:    querylanguage.HasEdge("owner"),
			S:    `has_edge(owner)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.S, tt.P.String())
		})
	}
}

func TestNaryExpressions(t *testing.T) {
	// Test n-ary And with more than 2 predicates
	p := querylanguage.And(
		querylanguage.FieldEQ("a", 1),
		querylanguage.FieldEQ("b", 2),
		querylanguage.FieldEQ("c", 3),
	)
	assert.Equal(t, `(a == 1 && b == 2 && c == 3)`, p.String())

	// Test n-ary Or with more than 2 predicates
	p = querylanguage.Or(
		querylanguage.FieldEQ("x", 1),
		querylanguage.FieldEQ("y", 2),
		querylanguage.FieldEQ("z", 3),
	)
	assert.Equal(t, `(x == 1 || y == 2 || z == 3)`, p.String())
}

func TestComparisonOperations(t *testing.T) {
	tests := []struct {
		name string
		P    querylanguage.P
		S    string
	}{
		{
			name: "NEQ",
			P:    querylanguage.NEQ(querylanguage.F("a"), querylanguage.F("b")),
			S:    `a != b`,
		},
		{
			name: "GT",
			P:    querylanguage.GT(querylanguage.F("x"), querylanguage.F("y")),
			S:    `x > y`,
		},
		{
			name: "GTE",
			P:    querylanguage.GTE(querylanguage.F("x"), querylanguage.F("y")),
			S:    `x >= y`,
		},
		{
			name: "LT",
			P:    querylanguage.LT(querylanguage.F("x"), querylanguage.F("y")),
			S:    `x < y`,
		},
		{
			name: "LTE",
			P:    querylanguage.LTE(querylanguage.F("x"), querylanguage.F("y")),
			S:    `x <= y`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.S, tt.P.String())
		})
	}
}

func TestNegate(t *testing.T) {
	// Test BinaryExpr.Negate
	p := querylanguage.FieldEQ("name", "test")
	assert.Equal(t, `!(name == "test")`, p.Negate().String())

	// Test UnaryExpr.Negate (double negation)
	p2 := querylanguage.Not(querylanguage.FieldEQ("name", "test"))
	assert.Equal(t, `!(!(name == "test"))`, p2.Negate().String())

	// Test NaryExpr.Negate
	p3 := querylanguage.And(
		querylanguage.FieldEQ("a", 1),
		querylanguage.FieldEQ("b", 2),
		querylanguage.FieldEQ("c", 3),
	)
	assert.Equal(t, `!((a == 1 && b == 2 && c == 3))`, p3.Negate().String())

	// Test CallExpr.Negate
	p4 := querylanguage.HasEdge("owner")
	assert.Equal(t, `!(has_edge(owner))`, p4.Negate().String())
}

func TestOpValid(t *testing.T) {
	tests := []struct {
		name  string
		op    querylanguage.Op
		valid bool
	}{
		{"OpAnd", querylanguage.OpAnd, true},
		{"OpOr", querylanguage.OpOr, true},
		{"OpNot", querylanguage.OpNot, true},
		{"OpEQ", querylanguage.OpEQ, true},
		{"OpNEQ", querylanguage.OpNEQ, true},
		{"OpGT", querylanguage.OpGT, true},
		{"OpGTE", querylanguage.OpGTE, true},
		{"OpLT", querylanguage.OpLT, true},
		{"OpLTE", querylanguage.OpLTE, true},
		{"OpIn", querylanguage.OpIn, true},
		{"OpNotIn", querylanguage.OpNotIn, true},
		{"negative", querylanguage.Op(-1), false},
		{"too_large", querylanguage.Op(100), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.op.Valid())
		})
	}
}

func TestOpString(t *testing.T) {
	assert.Equal(t, "==", querylanguage.OpEQ.String())
	assert.Equal(t, "!=", querylanguage.OpNEQ.String())
	assert.Equal(t, ">", querylanguage.OpGT.String())
	assert.Equal(t, ">=", querylanguage.OpGTE.String())
	assert.Equal(t, "<", querylanguage.OpLT.String())
	assert.Equal(t, "<=", querylanguage.OpLTE.String())
	assert.Equal(t, "in", querylanguage.OpIn.String())
	assert.Equal(t, "not in", querylanguage.OpNotIn.String())
	assert.Equal(t, "&&", querylanguage.OpAnd.String())
	assert.Equal(t, "||", querylanguage.OpOr.String())
	assert.Equal(t, "!", querylanguage.OpNot.String())
	assert.Equal(t, "<invalid>", querylanguage.Op(-1).String())
	assert.Equal(t, "<invalid>", querylanguage.Op(100).String())
}

func TestGenericInNotIn(t *testing.T) {
	tests := []struct {
		name string
		P    querylanguage.P
		S    string
	}{
		{
			name: "In_with_values",
			P:    querylanguage.In(querylanguage.F("status"), &querylanguage.Value{V: "active"}, &querylanguage.Value{V: "pending"}),
			S:    `status in ["active","pending"]`,
		},
		{
			name: "NotIn_with_values",
			P:    querylanguage.NotIn(querylanguage.F("role"), &querylanguage.Value{V: "admin"}, &querylanguage.Value{V: "root"}),
			S:    `role not in ["admin","root"]`,
		},
		{
			name: "In_with_ints",
			P:    querylanguage.In(querylanguage.F("x"), &querylanguage.Value{V: 1}, &querylanguage.Value{V: 2}),
			S:    `x in [1,2]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.S, tt.P.String())
		})
	}
}

func TestValueStringEdgeCases(t *testing.T) {
	// nil Value
	var v *querylanguage.Value
	assert.Equal(t, "nil", v.String())

	// Value with non-JSON-marshalable content (channel)
	ch := make(chan int)
	val := &querylanguage.Value{V: ch}
	// Should fall back to fmt.Sprint
	assert.Contains(t, val.String(), "0x")
}

func TestEdgeString(t *testing.T) {
	e := &querylanguage.Edge{Name: "posts"}
	assert.Equal(t, "posts", e.String())
}

func TestFieldString(t *testing.T) {
	f := &querylanguage.Field{Name: "email"}
	assert.Equal(t, "email", f.String())
}
