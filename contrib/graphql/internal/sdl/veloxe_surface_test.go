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

// veloxeApp is what a veloxe application declares beside velox.graphql: the
// scalars veloxe's SDL left to it, and its directives.
const veloxeApp = "scalar JSON\nscalar UUID\nscalar Uint64\ndirective @sensitive on FIELD_DEFINITION | INPUT_FIELD_DEFINITION\n"

// loadVeloxe renders m under cfg, Veloxe set, and loads the SDL beside the
// application's files: veloxeApp and others.
func loadVeloxe(t *testing.T, m *plugin.Model, cfg Config, others ...string) (*ast.Schema, string) {
	t.Helper()
	cfg.Veloxe = true
	sdl, err := RenderConfig(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Check(sdl, append([]string{veloxeApp}, others...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, sdl)
	}
	return s, sdl
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

// orderThing is an entity with a field of each type veloxe's order enum
// listed or left out, and a foreign key.
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

// Under compat=veloxe the order enum lists every field veloxe's did: the key
// first, as its OrderField asks, then each string that is not Text, time,
// int, int64, bool, enum and float in the schema's order, named by its
// OrderField or its name upper-cased; a foreign key it declares too. An
// OrderField on a field it leaves out says nothing, and an application's
// directive does not guard the order.
func TestVeloxeOrder(t *testing.T) {
	m := orderThing(t)
	s, sdl := loadVeloxe(t, m, Config{}, "scalar Decimal\n")
	want := []string{"ID", "NAME", "CREATED_AT", "COUNT", "BIG", "ACTIVE", "STATUS", "RATIO", "TAG", "CUSTOMER_ID"}
	if got := enumValuesOf(s, "ThingOrderField"); !slices.Equal(got, want) {
		t.Errorf("ThingOrderField: %v, want %v\n%s", got, want, sdl)
	}
	// The order and its directive: the field keeps its @sensitive.
	if !strings.Contains(sdl, "  tagged: String! @sensitive\n") {
		t.Errorf("tagged lost its directive:\n%s", sdl)
	}
	// velox's order is the annotated fields alone, and refuses an OrderField
	// it cannot order by.
	m.Entities[0].Fields[10].Annotations = nil // data
	m.Entities[0].Fields[11].Annotations = ann(t, graphql.Type("Decimal"))
	m.Entities[0].Fields[14].Annotations = ann(t, graphql.OrderField("TAG"), graphql.OrderUnguarded(), sensitiveDir())
	sdl, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sdl, "enum ThingOrderField {\n  ID\n  TAG\n}") {
		t.Errorf("velox's order changed:\n%s", sdl)
	}

	// Skip(SkipOrderField) on the schema leaves the order out.
	m = orderThing(t)
	m.Entities[0].Annotations = ann(t, graphql.Skip(graphql.SkipOrderField))
	s, _ = loadVeloxe(t, m, Config{}, "scalar Decimal\n")
	if s.Types["ThingOrderField"] != nil || s.Types["ThingOrder"] != nil {
		t.Error("an order on a schema that skips it")
	}
}

// veloxeOrderName upper-snakes a name as veloxe's toSnakeCase did.
func TestVeloxeOrderName(t *testing.T) {
	for in, want := range map[string]string{"created_at": "CREATED_AT", "createdAt": "CREATED_AT", "name": "NAME", "URL": "U_R_L"} {
		if got := veloxeOrderName(in); got != want {
			t.Errorf("veloxeOrderName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Under compat=veloxe every type has a root Query field: a connection, or
// with QueryField and without RelayConnection a list, whose type then has
// no connection, edge or order types, and whose to-many edges are lists.
// The connection's edges and node are nullable.
func TestVeloxeQueryAndConnections(t *testing.T) {
	ws := entity("Workspace", field("name", "string"))
	ws.Annotations = ann(t, graphql.QueryField())
	member := entity("Member", field("name", "string"))
	member.Annotations = ann(t, graphql.QueryField("allMembers"), graphql.RelayConnection())
	person := entity("Person", field("name", "string"))
	ws.Edges = []*plugin.Edge{edge("members", plugin.O2M, "Member")}
	member.Edges = []*plugin.Edge{edge("workspaces", plugin.M2M, "Workspace")}
	m := model(ws, member, person)
	s, sdl := loadVeloxe(t, m, Config{})
	var got []string
	for _, f := range s.Types["Query"].Fields {
		if !strings.HasPrefix(f.Name, "__") {
			got = append(got, f.Name+": "+f.Type.String())
		}
	}
	want := []string{"node: Node", "nodes: [Node]!", "allMembers: MemberConnection!", "people: PersonConnection!", "workspaces: [Workspace!]!"}
	if !slices.Equal(got, want) {
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
	if got := enumValuesOf(s, "PersonOrderField"); !slices.Equal(got, []string{"NAME"}) {
		t.Errorf("PersonOrderField: %v", got)
	}

	// velox's: only QueryFields, connections throughout, non-null edges.
	s, _ = load(t, m)
	if typeOf(s, "Query", "people") != "" || typeOf(s, "Query", "workspaces") != "WorkspaceConnection!" || typeOf(s, "WorkspaceConnection", "edges") != "[WorkspaceEdge!]!" {
		t.Error("velox's Query or connections changed")
	}

	// An explicit connection to a type without one is refused.
	member.Edges[0].Annotations = ann(t, graphql.Connection(true))
	if _, err := RenderConfig(m, Config{Veloxe: true}); err == nil || !strings.Contains(err.Error(), "which has no connection") {
		t.Errorf("connection to a list type: %v", err)
	}
}

// Under compat=veloxe an Optional field is nullable in the type, as veloxe
// had it, and a Required unique edge is non-null; the inputs keep velox's.
func TestVeloxeNullability(t *testing.T) {
	nick := field("nick", "string")
	nick.Optional = true
	ck := field("customer_id", "int64")
	order := entity("Order", nick, ck, field("ref_id", "int64"))
	order.Fields[3].Optional, order.Fields[3].Nullable, order.Fields[3].Pointer = true, true, true
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn, cust.Required = "customer_id", "Order", true
	ref := edge("ref", plugin.M2O, "Customer")
	ref.Field, ref.FieldOn = "ref_id", "Order"
	order.Edges = []*plugin.Edge{cust, ref}
	order.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	m := model(order, entity("Customer"))
	s, sdl := loadVeloxe(t, m, Config{})
	for _, c := range [][3]string{
		{"Order", "nick", "String"}, {"Order", "customerId", "ID!"}, {"Order", "refId", "ID"},
		{"Order", "customer", "Customer!"}, {"Order", "ref", "Customer"},
		{"CreateOrderInput", "nick", "String"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %s, want %s\n%s", c[0], c[1], got, c[2], sdl)
		}
	}
	s, _ = load(t, m)
	if typeOf(s, "Order", "nick") != "String!" || typeOf(s, "Order", "customer") != "Customer" {
		t.Error("velox's nullability changed")
	}
}

// Under compat=veloxe velox.graphql declares the scalars veloxe's did:
// Cursor and Time, and Bytes when a field is of type bytes. The application
// declares JSON, UUID and Uint64, as it did.
func TestVeloxeScalars(t *testing.T) {
	m := model(entity("Thing", field("data", "json")))
	_, sdl := loadVeloxe(t, m, Config{})
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
	// Without the application's declarations it does not load.
	plain, err := RenderConfig(m, Config{Veloxe: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(plain); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("JSON undeclared: %v", err)
	}
	_, sdl = loadVeloxe(t, model(entity("Thing", field("raw", "bytes"))), Config{})
	if !strings.Contains(sdl, "scalar Bytes\n") {
		t.Errorf("no Bytes for a bytes field:\n%s", sdl)
	}
}

// Under compat=veloxe the where inputs follow veloxe's defaults: the key and
// an Other field filter by equality alone, an explicit WhereOps wins over
// WhereInput's defaults, and no directive is on an input's field.
func TestVeloxeWhere(t *testing.T) {
	name := field("name", "string")
	name.Annotations = ann(t, graphql.WhereInput(), graphql.WhereOps(graphql.OpEQ), sensitiveDir())
	amount := field("amount", "other")
	amount.ElemType, amount.PkgPath = "decimal.Decimal", "github.com/shopspring/decimal"
	amount.Annotations = ann(t, graphql.Type("Decimal"), graphql.WhereInput())
	e := entity("Thing", name, amount)
	e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	m := model(e)
	s, sdl := loadVeloxe(t, m, Config{}, "scalar Decimal\n")
	got := strings.Join(fieldsOf(s, "ThingWhereInput"), ", ")
	want := "not: ThingWhereInput, and: [ThingWhereInput!], or: [ThingWhereInput!], id: ID, idNEQ: ID, idIn: [ID!], idNotIn: [ID!], name: String, amount: Decimal, amountNEQ: Decimal, amountIn: [Decimal!], amountNotIn: [Decimal!]"
	if got != want {
		t.Errorf("ThingWhereInput:\n got %s\nwant %s", got, want)
	}
	if strings.Count(sdl, "@sensitive") != 1 || !strings.Contains(sdl, "  name: String! @sensitive\n") {
		t.Errorf("a directive is on an input, or not on the type:\n%s", sdl)
	}
	// velox's: the defaults and the explicit ops add up, and the directive
	// guards the inputs.
	s, _ = load(t, m, "scalar Decimal\ndirective @sensitive on FIELD_DEFINITION | INPUT_FIELD_DEFINITION\n")
	for _, f := range []string{"idGT", "nameContains", "amountGT"} {
		if s.Types["ThingWhereInput"].Fields.ForName(f) == nil {
			t.Errorf("velox's where input lacks %s", f)
		}
	}
}

// With Config.WhereWhitelist a field other than the key has predicates only
// with WhereInput, whatever its WhereOps, as nextapp's codegen hook made
// them under veloxe.
func TestWhereWhitelist(t *testing.T) {
	tags := field("tags", "json")
	tags.ElemType, tags.Annotations = "[]string", ann(t, graphql.WhereOps(graphql.OpsJSONArray))
	name := field("name", "string")
	name.Annotations = ann(t, graphql.WhereInput())
	code := field("code", "string")
	code.Annotations = ann(t, graphql.WhereOps())
	m := model(entity("Thing", tags, name, code))
	for _, veloxe := range []bool{false, true} {
		sdl, err := RenderConfig(m, Config{WhereWhitelist: true, Veloxe: veloxe})
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"tagsHas", "  code: String\n", "codeNEQ"} {
			if strings.Contains(sdl, bad) {
				t.Errorf("veloxe %v: %s is filtered:\n%s", veloxe, bad, sdl)
			}
		}
		for _, want := range []string{"  id: ID\n", "  nameNEQ: String\n"} {
			if !strings.Contains(sdl, want) {
				t.Errorf("veloxe %v: lacks %q:\n%s", veloxe, want, sdl)
			}
		}
	}
	if sdl, err := Render(m); err != nil || !strings.Contains(sdl, "tagsHas") || !strings.Contains(sdl, "codeNEQ") {
		t.Errorf("without the whitelist: %v\n%s", err, sdl)
	}
}

// Under compat=veloxe what veloxe's generator ignored is ignored, where velox
// refuses it.
func TestVeloxeIgnores(t *testing.T) {
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
	keyDir := func() *plugin.Model {
		ck := field("customer_id", "int64")
		ck.Annotations = ann(t, sensitiveDir())
		e := entity("Order", ck)
		ed := edge("customer", plugin.M2O, "Customer")
		ed.Field, ed.FieldOn = "customer_id", "Order"
		e.Edges = []*plugin.Edge{ed}
		return model(e, entity("Customer"))
	}
	for name, c := range map[string]struct {
		m    *plugin.Model
		want string // velox's refusal
	}{
		"resolvers on a skipped schema": {func() *plugin.Model {
			e := entity("Thing", field("name", "string"))
			e.Annotations = ann(t, graphql.Skip(graphql.SkipAll), graphql.Resolvers(graphql.Map("x", "String")))
			return model(e, entity("Other"))
		}(), "graphql.Resolvers on a schema whose type graphql.Skip leaves out"},
		"where ops on a sensitive field":     {model(entity("Thing", field("name", "string"), secret(graphql.WhereOps()))), "graphql.WhereOps on a sensitive field"},
		"where input on a sensitive field":   {model(entity("Thing", field("name", "string"), secret(graphql.WhereInput()))), "graphql.WhereOps on a sensitive field"},
		"order field on a sensitive field":   {model(entity("Thing", field("name", "string"), secret(graphql.OrderField("SECRET")))), "graphql.OrderField on a sensitive field"},
		"where ops on a json struct":         {model(entity("Thing", settings(graphql.Type("JSON"), graphql.WhereOps()))), "which has no predicates"},
		"order field on a json field":        {model(entity("Thing", settings(graphql.Type("JSON"), graphql.OrderField("SETTINGS")))), "graphql.OrderField on a json field"},
		"a directive on a shown foreign key": {keyDir(), "on a foreign key"},
		"where ops on a field skipped by type": {model(entity("Thing", field("name", "string"), func() *plugin.Field {
			f := field("x", "string")
			f.Annotations = ann(t, graphql.Skip(graphql.SkipType), graphql.WhereOps())
			return f
		}())), "graphql.WhereOps on a field graphql.Skip leaves out of the type"},
	} {
		if _, err := Render(c.m); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: velox: %v, want %q", name, err, c.want)
		}
		sdl, err := RenderConfig(c.m, Config{Veloxe: true})
		if err != nil {
			t.Errorf("%s: compat=veloxe: %v", name, err)
			continue
		}
		if strings.Contains(sdl, "secretNEQ") || strings.Contains(sdl, "SECRET") || strings.Contains(sdl, "settingsNEQ") {
			t.Errorf("%s: compat=veloxe kept it:\n%s", name, sdl)
		}
	}
	sdl, err := RenderConfig(keyDir(), Config{Veloxe: true})
	if err != nil || !strings.Contains(sdl, "  customerId: ID! @sensitive\n") {
		t.Errorf("the foreign key's directive: %v\n%s", err, sdl)
	}
}

// Under compat=veloxe a json field of a named Go type is that type's name,
// and a slice of one a list of it, as veloxe's generator named them; the
// application declares the type. An input takes JSON for it, as veloxe's did.
func TestVeloxeJSONTypes(t *testing.T) {
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
	s, sdl := loadVeloxe(t, m, Config{}, app)
	for _, c := range [][3]string{
		{"Workspace", "crmSettings", "WorkspaceCRMSettings"},
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
		t.Error("velox's JSON changed")
	}
}

// veloxeJSONType derives the names veloxe's generator did.
func TestVeloxeJSONType(t *testing.T) {
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
		if got := veloxeJSONType(f); got != c.want {
			t.Errorf("veloxeJSONType(%s) = %q, want %q", c.goType, got, c.want)
		}
	}
}
