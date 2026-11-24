package sdl

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// sensitiveDecl declares the application's directive @sensitive.
const sensitiveDecl = "directive @sensitive on FIELD_DEFINITION | INPUT_FIELD_DEFINITION\n"

// wideApp is what an application declares beside velox.graphql under
// wideConfig: the scalars its apptypes= lists, and its directives.
const wideApp = "scalar JSON\nscalar UUID\nscalar Uint64\n" + sensitiveDecl

// wideConfig sets every SDL option to its value other than the default:
// schema names, lenient filters, Node, equality on fields named like keys,
// every orderable field and every type's Query field, nullable Optional
// fields, non-null Required edges, implicit keys' inputs by their column,
// named JSON types, and JSON, UUID and Uint64 left to the application.
func wideConfig() Config {
	return Config{
		Names: "schema", Filters: "lenient", Node: "on", IDOps: "equality", OrderFields: "all", QueryFields: "all",
		Optional: "nullable", RequiredEdges: "nonnull", InputKeys: "column", JSONTypes: "named",
		AppTypes: []string{"JSON", "UUID", "Uint64"},
	}
}

// loadWith renders m under cfg and loads the SDL beside the application's
// declarations decls.
func loadWith(t *testing.T, m *plugin.Model, cfg Config, decls ...string) (*ast.Schema, string) {
	t.Helper()
	sdl, err := RenderConfig(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Check(sdl, decls...)
	if err != nil {
		t.Fatalf("%v\n%s", err, sdl)
	}
	return s, sdl
}

// warningsOf renders m under cfg and returns its warnings.
func warningsOf(t *testing.T, m *plugin.Model, cfg Config) []string {
	t.Helper()
	_, s, err := RenderSurface(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s.Warnings
}

// enumValuesOf lists the values of the enum typ.
func enumValuesOf(s *ast.Schema, typ string) []string {
	def := s.Types[typ]
	if def == nil {
		return nil
	}
	var out []string
	for _, v := range def.EnumValues {
		out = append(out, v.Name)
	}
	return out
}

// sensitiveDir is the application's directive @sensitive, which audits reads.
func sensitiveDir() graphql.Annotation {
	return graphql.Directives(graphql.Directive{Name: "sensitive"})
}

// orderThing is an entity with a field of each type orderfields=all lists
// or leaves out, and a foreign key.
func orderThing(t *testing.T) *plugin.Model {
	text := field("body", "string")
	text.Size = math.MaxInt32
	status := field("status", "enum")
	status.Enum, status.ElemType, status.EnumValues = "ThingStatus", "ThingStatus", []string{"open", "done"}
	secret := field("secret", "string")
	secret.Sensitive = true
	amount := field("amount", "other")
	amount.ElemType, amount.PkgPath = "decimal.Decimal", "github.com/shopspring/decimal"
	amount.Annotations = ann(t, graphql.Type("Decimal"), graphql.OrderField("AMOUNT"))
	data := field("data", "json")
	data.ElemType, data.Annotations = "map[string]any", ann(t, graphql.OrderField("DATA"))
	hidden := field("hidden", "string")
	hidden.Annotations = ann(t, graphql.Skip(graphql.SkipOrderField))
	tagged := field("tagged", "string")
	tagged.Annotations = ann(t, graphql.OrderField("TAG"), sensitiveDir())
	e := entity("Thing",
		field("name", "string"), text, field("created_at", "time"), field("count", "int"),
		field("small", "int32"), field("big", "int64"), field("active", "bool"), status,
		field("ratio", "float64"), data, amount, secret, hidden, tagged, field("uid", "uuid"),
		field("customer_id", "int64"), field("raw", "bytes"))
	e.Fields[0].Annotations = ann(t, graphql.OrderField("ID"))
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn = "customer_id", "Thing"
	e.Edges = []*plugin.Edge{cust}
	m := model(e, entity("Customer"))
	m.Enums = []*plugin.Enum{{Type: "ThingStatus", Values: status.EnumValues, Consts: []string{"ThingStatusOpen", "ThingStatusDone"}}}
	return m
}

// annotatedOrder strips from orderThing what orderfields=annotated refuses:
// an order on a json field, an order on a decimal, and an unguarded order on
// a field with a directive.
func annotatedOrder(t *testing.T, m *plugin.Model) *plugin.Model {
	m.Entities[0].Fields[10].Annotations = nil // data
	m.Entities[0].Fields[11].Annotations = ann(t, graphql.Type("Decimal"))
	m.Entities[0].Fields[14].Annotations = ann(t, graphql.OrderField("TAG"), graphql.OrderUnguarded(), sensitiveDir())
	return m
}

// Under orderfields=all the order enum lists every orderable field: the key
// first, as its OrderField asks, then each string that is not Text, time,
// int, int64, bool, enum and float in the schema's order, named by its
// OrderField or its name upper-cased; a foreign key it declares too. An
// application's directive does not guard the order. By default the order is
// the annotated fields alone, and a directive guards it.
func TestOrderFieldsAll(t *testing.T) {
	m := orderThing(t)
	s, sdl := loadWith(t, m, Config{OrderFields: "all"}, "scalar Decimal\n"+sensitiveDecl)
	want := []string{"ID", "NAME", "CREATED_AT", "COUNT", "BIG", "ACTIVE", "STATUS", "RATIO", "TAG", "CUSTOMER_ID"}
	if got := enumValuesOf(s, "ThingOrderField"); !slices.Equal(got, want) {
		t.Errorf("ThingOrderField: %v, want %v\n%s", got, want, sdl)
	}
	if !strings.Contains(sdl, "  tagged: String! @sensitive\n") {
		t.Errorf("tagged lost its directive:\n%s", sdl)
	}

	if _, err := Render(m); err == nil || !strings.Contains(err.Error(), "graphql.OrderField on a json field") {
		t.Errorf("annotated, an order on a json field: %v", err)
	}
	m = orderThing(t)
	m.Entities[0].Fields[10].Annotations = nil
	if _, err := Render(m); err == nil || !strings.Contains(err.Error(), "add graphql.OrderUnguarded()") {
		t.Errorf("annotated, an order on a field with a directive: %v", err)
	}
	s, sdl = load(t, annotatedOrder(t, orderThing(t)), "scalar Decimal\n"+sensitiveDecl)
	if got := enumValuesOf(s, "ThingOrderField"); !slices.Equal(got, []string{"ID", "TAG"}) {
		t.Errorf("annotated: %v\n%s", got, sdl)
	}

	// Skip(SkipOrderField) on the schema leaves the order out.
	m = orderThing(t)
	m.Entities[0].Annotations = ann(t, graphql.Skip(graphql.SkipOrderField))
	s, _ = loadWith(t, m, Config{OrderFields: "all"}, "scalar Decimal\n"+sensitiveDecl)
	if s.Types["ThingOrderField"] != nil || s.Types["ThingOrder"] != nil {
		t.Error("an order on a schema that skips it")
	}
}

// Under orderfields=all an order on a field of a type the order enum does
// not list is dropped, with a warning naming the entity, the field and the
// type. The annotated order warns of none.
func TestOrderFieldsAllWarns(t *testing.T) {
	m := orderThing(t)
	m.Entities[0].Fields[2].Annotations = ann(t, graphql.OrderUnguarded()) // body, a Text
	want := []string{
		`Thing.body: graphql.OrderUnguarded() is dropped: orderfields=all orders no field of type string of unbounded size (field.Text)`,
		`Thing.data: graphql.OrderField("DATA") is dropped: orderfields=all orders no field of type json (map[string]any)`,
		`Thing.amount: graphql.OrderField("AMOUNT") is dropped: orderfields=all orders no field of type other (decimal.Decimal)`,
		unguardedOrder,
	}
	if got := warningsOf(t, m, Config{OrderFields: "all"}); !slices.Equal(got, want) {
		t.Errorf("warnings:\n got %q\nwant %q", got, want)
	}
	if got := warningsOf(t, annotatedOrder(t, orderThing(t)), Config{}); len(got) != 0 {
		t.Errorf("the annotated order warns: %q, want nothing", got)
	}
}

// unguardedOrder is the warning of orderThing's tagged field, which has
// @sensitive and an order value orderfields=all gives it.
const unguardedOrder = "Thing.tagged has @sensitive, but its order value is not guarded by it; drop the order (graphql.OrderField, or graphql.Skip(graphql.SkipOrderField) under orderfields=all), add graphql.OrderUnguarded() to accept it, or guard the field another way"

// A field with an application directive that has where predicates or an
// order value is warned of, naming the type, the field and the directive:
// neither carries the directive. OrderUnguarded silences the order's
// warning, and the output does not change.
func TestUnguardedWarns(t *testing.T) {
	const where = "Thing.tagged has @sensitive, but its where predicates are not guarded by it; drop graphql.WhereOps or graphql.WhereInput, or guard the field another way"
	const both = "Thing.tagged has @sensitive, but its where predicates and order value are not guarded by it; drop graphql.WhereOps or graphql.WhereInput and the order (graphql.OrderField, or graphql.Skip(graphql.SkipOrderField) under orderfields=all), or guard the field another way"
	tagged := func(anns ...graphql.Annotation) *plugin.Model {
		m := annotatedOrder(t, orderThing(t))
		m.Entities[0].Fields[14].Annotations = ann(t, append(anns, sensitiveDir())...)
		return m
	}
	unguarded := func(ws []string) []string {
		var out []string
		for _, w := range ws {
			if strings.Contains(w, "not guarded") {
				out = append(out, w)
			}
		}
		return out
	}
	for _, c := range []struct {
		name string
		m    *plugin.Model
		cfg  Config
		want []string
	}{
		{"no predicates, no order", tagged(), Config{}, nil},
		{"WhereOps", tagged(graphql.WhereOps(graphql.OpsEquality)), Config{}, []string{where}},
		{"WhereInput", tagged(graphql.WhereInput()), Config{}, []string{where}},
		{"WhereInput under where=whitelist", tagged(graphql.WhereInput()), Config{WhereWhitelist: true}, []string{where}},
		{"WhereOps under where=whitelist", tagged(graphql.WhereOps(graphql.OpsEquality)), Config{WhereWhitelist: true}, nil},
		{"OrderUnguarded", tagged(graphql.OrderField("TAG"), graphql.OrderUnguarded()), Config{}, nil},
		{"OrderUnguarded and WhereOps", tagged(graphql.OrderField("TAG"), graphql.OrderUnguarded(), graphql.WhereOps(graphql.OpsEquality)), Config{}, []string{where}},
		{"orderfields=all", tagged(), Config{OrderFields: "all"}, []string{unguardedOrder}},
		{"orderfields=all and WhereOps", tagged(graphql.WhereOps(graphql.OpsEquality)), Config{OrderFields: "all"}, []string{both}},
		{"orderfields=all, OrderUnguarded", tagged(graphql.OrderField("TAG"), graphql.OrderUnguarded()), Config{OrderFields: "all"}, nil},
		{"orderfields=all, SkipOrderField", tagged(graphql.Skip(graphql.SkipOrderField)), Config{OrderFields: "all"}, nil},
	} {
		if got := unguarded(warningsOf(t, c.m, c.cfg)); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// The warning leaves the output as it was.
	m := tagged(graphql.WhereOps(graphql.OpsEquality))
	if _, sdl := loadWith(t, m, Config{}, "scalar Decimal\n"+sensitiveDecl); !strings.Contains(sdl, "  taggedNEQ: String\n") || strings.Contains(sdl, "taggedNEQ: String @") {
		t.Errorf("the predicate changed:\n%s", sdl)
	}
}

// orderName upper-snakes a name.
func TestOrderName(t *testing.T) {
	for in, want := range map[string]string{"created_at": "CREATED_AT", "createdAt": "CREATED_AT", "name": "NAME", "URL": "U_R_L"} {
		if got := orderName(in); got != want {
			t.Errorf("orderName(%q) = %q, want %q", in, got, want)
		}
	}
}

// connModel is a workspace with a QueryField and no RelayConnection, a
// member with both, and a person with neither.
func connModel(t *testing.T) (*plugin.Model, *plugin.Entity) {
	ws := entity("Workspace", field("name", "string"))
	ws.Annotations = ann(t, graphql.QueryField())
	member := entity("Member", field("name", "string"))
	member.Annotations = ann(t, graphql.QueryField("allMembers"), graphql.RelayConnection())
	person := entity("Person", field("name", "string"))
	ws.Edges = []*plugin.Edge{edge("members", plugin.O2M, "Member")}
	member.Edges = []*plugin.Edge{edge("workspaces", plugin.M2M, "Workspace")}
	return model(ws, member, person), member
}

// queryFields lists Query's fields as "name: Type".
func queryFields(s *ast.Schema) []string {
	var got []string
	for _, f := range s.Types["Query"].Fields {
		if !strings.HasPrefix(f.Name, "__") {
			got = append(got, f.Name+": "+f.Type.String())
		}
	}
	return got
}

// RelayConnection is honoured, as in entgql: a type with a QueryField and
// without RelayConnection has a list in Query and no connection, edge or
// order types, and its to-many edges are lists. Only QueryFields are in
// Query. A connection's edges and node are nullable, as the Relay spec
// allows.
func TestConnectionsDefault(t *testing.T) {
	m, member := connModel(t)
	s, sdl := load(t, m)
	if got, want := queryFields(s), []string{"allMembers: MemberConnection!", "workspaces: [Workspace!]!"}; !slices.Equal(got, want) {
		t.Errorf("Query: %v, want %v", got, want)
	}
	for _, n := range []string{"WorkspaceConnection", "WorkspaceEdge", "WorkspaceOrder", "WorkspaceOrderField"} {
		if s.Types[n] != nil {
			t.Errorf("%s is declared:\n%s", n, sdl)
		}
	}
	if typeOf(s, "Member", "workspaces") != "[Workspace!]!" || typeOf(s, "Workspace", "members") != "MemberConnection!" {
		t.Errorf("edges:\n%s", sdl)
	}
	if typeOf(s, "MemberConnection", "edges") != "[MemberEdge!]" || typeOf(s, "MemberEdge", "node") != "Member" {
		t.Errorf("connection nullability:\n%s", sdl)
	}
	// An explicit connection to a type without one is refused.
	member.Edges[0].Annotations = ann(t, graphql.Connection(true))
	if _, err := Render(m); err == nil || !strings.Contains(err.Error(), "which has no connection") {
		t.Errorf("connection to a list type: %v", err)
	}
}

// Under queryfields=all every type has a root Query field, named by
// QueryField or by default.
func TestQueryFieldsAll(t *testing.T) {
	m, _ := connModel(t)
	s, _ := loadWith(t, m, Config{QueryFields: "all"})
	want := []string{"allMembers: MemberConnection!", "people: PersonConnection!", "workspaces: [Workspace!]!"}
	if got := queryFields(s); !slices.Equal(got, want) {
		t.Errorf("Query: %v, want %v", got, want)
	}
	s, _ = loadWith(t, m, wideConfig(), wideApp)
	want = append([]string{"node: Node", "nodes: [Node]!"}, want...)
	if got := queryFields(s); !slices.Equal(got, want) {
		t.Errorf("wide Query: %v, want %v", got, want)
	}
	if got := enumValuesOf(s, "PersonOrderField"); !slices.Equal(got, []string{"NAME"}) {
		t.Errorf("PersonOrderField: %v", got)
	}
}

// orderModel is an order with an Optional field, a Required foreign key
// declared as a field, an optional one, and a create input.
func orderModel(t *testing.T) *plugin.Model {
	nick := field("nick", "string")
	nick.Optional = true
	order := entity("Order", nick, field("customer_id", "int64"), field("ref_id", "int64"))
	order.Fields[3].Optional, order.Fields[3].Nullable, order.Fields[3].Pointer = true, true, true
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn, cust.Required = "customer_id", "Order", true
	ref := edge("ref", plugin.M2O, "Customer")
	ref.Field, ref.FieldOn = "ref_id", "Order"
	order.Edges = []*plugin.Edge{cust, ref}
	order.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	return model(order, entity("Customer"))
}

// A declared foreign key is in the type, as an ID beside its edge, as in
// ent; the create input names it as the type does. An Optional field is
// non-null in the type and optional in the inputs, and a Required unique
// edge is nullable. optional=nullable and requirededges=nonnull turn each.
func TestNullabilityAndKeys(t *testing.T) {
	m := orderModel(t)
	s, sdl := load(t, m)
	for _, c := range [][3]string{
		{"Order", "nick", "String!"}, {"Order", "customerID", "ID!"}, {"Order", "refID", "ID"},
		{"Order", "customer", "Customer"}, {"Order", "ref", "Customer"},
		{"CreateOrderInput", "nick", "String"}, {"CreateOrderInput", "customerID", "ID!"}, {"CreateOrderInput", "refID", "ID"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %s, want %s\n%s", c[0], c[1], got, c[2], sdl)
		}
	}
	s, sdl = loadWith(t, m, Config{Optional: "nullable", RequiredEdges: "nonnull", Names: "schema"})
	for _, c := range [][3]string{
		{"Order", "nick", "String"}, {"Order", "customerId", "ID!"}, {"Order", "customer", "Customer!"},
		{"CreateOrderInput", "nick", "String"}, {"CreateOrderInput", "customerId", "ID!"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("options: %s.%s: %s, want %s\n%s", c[0], c[1], got, c[2], sdl)
		}
	}
}

// An edge's implicit foreign key, which the type does not show, is named in
// an input by its edge, bestFriendID; inputkeys=column names it by its
// column.
func TestInputKeys(t *testing.T) {
	user := entity("User", field("name", "string"))
	key := field("user_best_friend", "int64")
	key.Implicit, key.Nullable = "user_best_friend", true
	user.Fields = append(user.Fields, key)
	bf := edge("best_friend", plugin.O2O, "User")
	bf.Field, bf.FieldOn, bf.OwnsKey = "user_best_friend", "User", true
	user.Edges = []*plugin.Edge{bf}
	user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	m := model(user)
	s, sdl := load(t, m)
	if typeOf(s, "CreateUserInput", "bestFriendID") != "ID" || typeOf(s, "User", "userBestFriend") != "" {
		t.Errorf("default:\n%s", sdl)
	}
	s, sdl = loadWith(t, m, Config{InputKeys: "column"})
	if typeOf(s, "CreateUserInput", "userBestFriend") != "ID" || typeOf(s, "CreateUserInput", "bestFriendID") != "" {
		t.Errorf("inputkeys=column:\n%s", sdl)
	}
}

// apptypes= leaves each scalar of the vocabulary it lists to the
// application, with or without entities=; velox.graphql declares the rest.
// It refuses another type velox-graphql declares.
func TestAppTypesScalars(t *testing.T) {
	m := model(entity("Thing", field("data", "json"), field("raw", "bytes")))
	cfg := Config{AppTypes: []string{"JSON", "UUID", "Uint64", "Bytes"}}
	_, sdl := loadWith(t, m, cfg, "scalar JSON\nscalar UUID\nscalar Uint64\nscalar Bytes\n")
	for _, not := range []string{"scalar JSON", "scalar UUID", "scalar Uint64", "scalar Bytes"} {
		if strings.Contains(sdl, not) {
			t.Errorf("declares %q:\n%s", not, sdl)
		}
	}
	for _, want := range []string{"scalar Time\n", "scalar Cursor\n", "type PageInfo {\n", "enum OrderDirection {\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
	plain, err := RenderConfig(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(plain); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("JSON undeclared: %v", err)
	}
	if _, sdl := load(t, m); !strings.Contains(sdl, "scalar JSON\n") || !strings.Contains(sdl, "scalar Bytes\n") {
		t.Errorf("by default velox.graphql declares its scalars:\n%s", sdl)
	}
	for _, n := range []string{"Cursor", "PageInfo", "ThingConnection", "a-b"} {
		if _, err := RenderConfig(m, Config{AppTypes: []string{n}}); err == nil {
			t.Errorf("apptypes=%s: no error", n)
		}
	}
}

// whereModel is a thing with a string field whose WhereOps gives EQ alone,
// with a directive; an Other field; a string named like a key; a nullable
// int with WhereOps(EQ); a foreign key declared as a field; and one whose
// edge GraphQL skips. Each has WhereInput.
func whereModel(t *testing.T) *plugin.Model {
	name := field("name", "string")
	name.Annotations = ann(t, graphql.WhereInput(), graphql.WhereOps(graphql.OpEQ), sensitiveDir())
	amount := field("amount", "other")
	amount.ElemType, amount.PkgPath = "decimal.Decimal", "github.com/shopspring/decimal"
	amount.Annotations = ann(t, graphql.Type("Decimal"), graphql.WhereInput())
	ref := field("ref_id", "string")
	ref.Annotations = ann(t, graphql.WhereInput())
	rank := field("rank", "int")
	rank.Nullable, rank.Pointer, rank.Annotations = true, true, ann(t, graphql.WhereOps(graphql.OpEQ))
	ck := field("customer_id", "int64")
	ck.Annotations = ann(t, graphql.WhereInput())
	ok := field("owner_id", "int64")
	ok.Annotations = ann(t, graphql.WhereInput())
	e := entity("Thing", name, amount, ref, rank, ck, ok)
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn = "customer_id", "Thing"
	owner := edge("owner", plugin.M2O, "Customer")
	owner.Field, owner.FieldOn, owner.Annotations = "owner_id", "Thing", ann(t, graphql.Skip(graphql.SkipAll))
	e.Edges = []*plugin.Edge{cust, owner}
	e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	return model(e, entity("Customer"))
}

// The where input's defaults: an explicit WhereOps is the exact set, a
// nullable field's predicates add IsNil and NotNil, every field but a
// foreign key gets its kind's operators whatever its name, a foreign key
// declared as a field is equality alone and named from the field, also when
// its edge is skipped, and no directive is on an input's field.
func TestWhereDefaults(t *testing.T) {
	s, sdl := load(t, whereModel(t), "scalar Decimal\n"+sensitiveDecl)
	got := strings.Join(fieldsOf(s, "ThingWhereInput"), ", ")
	want := "not: ThingWhereInput, and: [ThingWhereInput!], or: [ThingWhereInput!], " +
		"id: ID, idNEQ: ID, idIn: [ID!], idNotIn: [ID!], idGT: ID, idGTE: ID, idLT: ID, idLTE: ID, " +
		"name: String, " +
		"amount: Decimal, amountNEQ: Decimal, amountIn: [Decimal!], amountNotIn: [Decimal!], amountGT: Decimal, amountGTE: Decimal, amountLT: Decimal, amountLTE: Decimal, " +
		"refID: String, refIDNEQ: String, refIDIn: [String!], refIDNotIn: [String!], refIDContains: String, refIDHasPrefix: String, refIDHasSuffix: String, refIDEqualFold: String, refIDContainsFold: String, " +
		"rank: Int, rankIsNil: Boolean, rankNotNil: Boolean, " +
		"customerID: ID, customerIDNEQ: ID, customerIDIn: [ID!], customerIDNotIn: [ID!], " +
		"ownerID: ID, ownerIDNEQ: ID, ownerIDIn: [ID!], ownerIDNotIn: [ID!]"
	if got != want {
		t.Errorf("ThingWhereInput:\n got %s\nwant %s", got, want)
	}
	if strings.Count(sdl, "@sensitive") != 1 || !strings.Contains(sdl, "  name: String! @sensitive\n") {
		t.Errorf("a directive is on an input, or not on the type:\n%s", sdl)
	}
	if typeOf(s, "Thing", "ownerID") != "ID!" || typeOf(s, "Thing", "owner") != "" {
		t.Errorf("a key whose edge is skipped:\n%s", sdl)
	}
}

// Under idops=equality the key, a field named like a key and an Other field
// default to equality alone.
func TestIDOpsEquality(t *testing.T) {
	s, sdl := loadWith(t, whereModel(t), Config{IDOps: "equality"}, "scalar Decimal\n"+sensitiveDecl)
	got := strings.Join(fieldsOf(s, "ThingWhereInput"), ", ")
	for _, want := range []string{
		"id: ID, idNEQ: ID, idIn: [ID!], idNotIn: [ID!], name",
		"amount: Decimal, amountNEQ: Decimal, amountIn: [Decimal!], amountNotIn: [Decimal!], refID",
		"refID: String, refIDNEQ: String, refIDIn: [String!], refIDNotIn: [String!], rank",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ThingWhereInput lacks %q:\n%s\n%s", want, got, sdl)
		}
	}
}

// With Config.WhereWhitelist a field other than the key has predicates only
// with WhereInput, whatever its WhereOps.
func TestWhereWhitelist(t *testing.T) {
	tags := field("tags", "json")
	tags.ElemType, tags.Annotations = "[]string", ann(t, graphql.WhereOps(graphql.OpsJSONArray))
	name := field("name", "string")
	name.Annotations = ann(t, graphql.WhereInput())
	code := field("code", "string")
	code.Annotations = ann(t, graphql.WhereOps())
	m := model(entity("Thing", tags, name, code))
	for _, cfg := range []Config{{WhereWhitelist: true}, func() Config { c := wideConfig(); c.WhereWhitelist = true; return c }()} {
		sdl, err := RenderConfig(m, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"tagsHas", "  code: String\n", "codeNEQ"} {
			if strings.Contains(sdl, bad) {
				t.Errorf("%+v: %s is filtered:\n%s", cfg, bad, sdl)
			}
		}
		for _, want := range []string{"  id: ID\n", "  nameNEQ: String\n"} {
			if !strings.Contains(sdl, want) {
				t.Errorf("%+v: lacks %q:\n%s", cfg, want, sdl)
			}
		}
	}
	if sdl, err := Render(m); err != nil || !strings.Contains(sdl, "tagsHas") || !strings.Contains(sdl, "codeNEQ") {
		t.Errorf("without the whitelist: %v\n%s", err, sdl)
	}
}

// An annotation that has no effect is dropped with a warning, as ent ignores
// it; a directive on a declared foreign key, which the type shows, stays.
func TestNoEffectWarns(t *testing.T) {
	secret := func(a ...graphql.Annotation) *plugin.Field {
		f := field("secret", "string")
		f.Sensitive, f.Annotations = true, ann(t, a...)
		return f
	}
	settings := func(a ...graphql.Annotation) *plugin.Field {
		f := field("settings", "json")
		f.ElemType, f.Kind, f.Annotations = "st.Settings", "struct", ann(t, a...)
		return f
	}
	for name, c := range map[string]struct {
		m    *plugin.Model
		want string
	}{
		"resolvers on a skipped schema": {func() *plugin.Model {
			e := entity("Thing", field("name", "string"))
			e.Annotations = ann(t, graphql.Skip(graphql.SkipAll), graphql.Resolvers(graphql.Map("x", "String")))
			return model(e, entity("Other"))
		}(), "schema Thing: graphql.Resolvers has no effect on a schema whose type graphql.Skip leaves out"},
		"where ops on a sensitive field":   {model(entity("Thing", field("name", "string"), secret(graphql.WhereOps()))), "Thing.secret: graphql.WhereOps has no effect on a sensitive field"},
		"where input on a sensitive field": {model(entity("Thing", field("name", "string"), secret(graphql.WhereInput()))), "Thing.secret: graphql.WhereOps has no effect on a sensitive field"},
		"order field on a sensitive field": {model(entity("Thing", field("name", "string"), secret(graphql.OrderField("SECRET")))), `Thing.secret: graphql.OrderField("SECRET") has no effect on a sensitive field`},
		"where ops on a json struct":       {model(entity("Thing", settings(graphql.Type("JSON"), graphql.WhereOps()))), "Thing.settings: graphql.WhereOps has no effect on a json field"},
		"a directive on the key": {func() *plugin.Model {
			e := entity("Thing", field("name", "string"))
			e.Fields[0].Annotations = ann(t, sensitiveDir())
			return model(e)
		}(), "graphql.Directives with the directive @sensitive has no effect on the primary key"},
		"where ops on a field skipped by type": {model(entity("Thing", field("name", "string"), func() *plugin.Field {
			f := field("x", "string")
			f.Annotations = ann(t, graphql.Skip(graphql.SkipType), graphql.WhereOps())
			return f
		}())), "Thing.x: graphql.WhereOps has no effect on a field graphql.Skip leaves out of the type"},
	} {
		sdl, s, err := RenderSurface(c.m, Config{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.ContainsFunc(s.Warnings, func(w string) bool { return strings.Contains(w, c.want) }) {
			t.Errorf("%s: warnings %q, want %q", name, s.Warnings, c.want)
		}
		if strings.Contains(sdl, "secretNEQ") || strings.Contains(sdl, "SECRET") || strings.Contains(sdl, "settingsNEQ") || strings.Contains(sdl, "  x: String") {
			t.Errorf("%s: kept it:\n%s", name, sdl)
		}
	}
	ck := field("customer_id", "int64")
	ck.Annotations = ann(t, sensitiveDir())
	e := entity("Order", ck)
	ed := edge("customer", plugin.M2O, "Customer")
	ed.Field, ed.FieldOn = "customer_id", "Order"
	e.Edges = []*plugin.Edge{ed}
	if sdl, err := Render(model(e, entity("Customer"))); err != nil || !strings.Contains(sdl, "  customerID: ID! @sensitive\n") {
		t.Errorf("the foreign key's directive: %v\n%s", err, sdl)
	}
}

// Under jsontypes=named a json field of a named Go type is that type's
// name, and a slice of one a list of it; the application declares the type.
// An input takes JSON for it. By default each is JSON.
func TestJSONTypesNamed(t *testing.T) {
	json := func(name, goType, kind string) *plugin.Field {
		f := field(name, "json")
		f.GoType, f.ElemType, f.Kind, f.Imports = goType, goType, kind, []string{"example.com/st"}
		return f
	}
	crm := json("crm_settings", "st.WorkspaceCRMSettings", "struct")
	crm.Optional = true
	assignees := json("assignees", "[]st.TaskAssignee", "slice")
	restrict := json("restrictions", "*st.Restrictions", "ptr")
	e := entity("Workspace", crm, assignees, restrict, json("tags", "[]string", "slice"), json("meta", "map[string]any", "map"))
	e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	m := model(e)
	app := "type WorkspaceCRMSettings { a: Int }\ntype TaskAssignee { a: Int }\ntype Restrictions { a: Int }\n"
	s, sdl := loadWith(t, m, Config{JSONTypes: "named"}, app)
	for _, c := range [][3]string{
		{"Workspace", "crmSettings", "WorkspaceCRMSettings!"},
		{"Workspace", "assignees", "[TaskAssignee!]!"},
		{"Workspace", "restrictions", "Restrictions!"},
		{"Workspace", "tags", "[String!]!"},
		{"Workspace", "meta", "JSON!"},
		{"CreateWorkspaceInput", "crmSettings", "JSON"},
		{"CreateWorkspaceInput", "assignees", "JSON!"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %s, want %s\n%s", c[0], c[1], got, c[2], sdl)
		}
	}
	if strings.Contains(sdl, "type TaskAssignee") || strings.Contains(sdl, "scalar TaskAssignee") {
		t.Errorf("velox declares the application's type:\n%s", sdl)
	}
	s, _ = load(t, m)
	if typeOf(s, "Workspace", "assignees") != "JSON!" {
		t.Error("the default changed")
	}
}

// jsonType derives a named Go type's GraphQL name.
func TestJSONType(t *testing.T) {
	for _, c := range []struct{ goType, kind, want string }{
		{"st.Settings", "struct", "Settings"},
		{"*st.Settings", "ptr", "Settings"},
		{"Settings", "struct", "Settings"},
		{"[]st.Item", "slice", "[Item!]"},
		{"[]*st.Item", "slice", "[Item!]"},
		{"[]string", "slice", ""},
		{"[]int32", "slice", ""},
		{"[]time.Time", "slice", ""},
		{"[][]st.Item", "slice", ""},
		{"[]map[string]any", "slice", ""},
		{"[]struct{ A int }", "slice", ""},
		{"map[string]any", "map", ""},
		{"any", "interface", ""},
		{"st.JSON", "struct", ""},
	} {
		f := &plugin.Field{Name: "f", Type: "json", GoType: c.goType, ElemType: c.goType, Kind: c.kind}
		if got := jsonType(f); got != c.want {
			t.Errorf("jsonType(%s) = %q, want %q", c.goType, got, c.want)
		}
	}
}
