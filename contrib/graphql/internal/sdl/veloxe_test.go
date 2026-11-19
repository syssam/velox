package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// veloxeCamel names as veloxe's generator did; the names on the right are
// in a veloxe application's API.
func TestVeloxeCamel(t *testing.T) {
	for in, want := range map[string]string{
		"customer_id":        "customerId",
		"image_url":          "imageUrl",
		"allowed_ips":        "allowedIps",
		"has_api_access":     "hasApiAccess",
		"last_ip":            "lastIp",
		"id":                 "id",
		"created_at":         "createdAt",
		"HTTPServer":         "httpServer",
		"userName":           "userName",
		"deleted_by_user_id": "deletedByUserId",
	} {
		if got := veloxeCamel(in); got != want {
			t.Errorf("veloxeCamel(%q) = %q, want %q", in, got, want)
		}
	}
}

// graphql.Type on a field is the field's GraphQL type, as veloxe took it: a
// name or a list the application declares, or the name of an enum field's
// enum, which the fields of one Go type that give it share.
func TestFieldType(t *testing.T) {
	app := "scalar Map\ntype ProductTranslate { name: String }\n"
	typed := func(name, typ, elem, pkg, gql string) *plugin.Field {
		f := field(name, typ)
		f.ElemType, f.PkgPath, f.Annotations = elem, pkg, ann(t, graphql.Type(gql))
		return f
	}
	itemType := func() *plugin.Field {
		f := typed("item_type", "enum", "st.ItemType", "example.com/st", "ItemType")
		f.Enum, f.EnumValues = "ItemType", []string{"product", "service"}
		return f
	}
	status := typed("status", "enum", "CreditNoteStatus", "", "CreditNoteEntryStatus")
	status.Enum, status.EnumValues = "CreditNoteStatus", []string{"open", "applied"}
	note := entity("CreditNote",
		typed("custom_fields", "json", "map[string]any", "", "Map"),
		typed("organization_ids", "json", "[]int64", "", "[ID!]"),
		typed("translations", "json", "[]st.ProductTranslate", "", "[ProductTranslate!]"),
		typed("user_id", "string", "string", "", "ID"),
		itemType(), status)
	note.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	bom := entity("BomItem", itemType())
	m := model(note, bom)
	m.Enums = []*plugin.Enum{{Type: "CreditNoteStatus", Values: status.EnumValues, Consts: []string{"CreditNoteStatusOpen", "CreditNoteStatusApplied"}}}
	s, sdl := load(t, m, app)
	for _, c := range [][3]string{
		{"CreditNote", "customFields", "Map!"},
		{"CreditNote", "organizationIds", "[ID!]!"},
		{"CreditNote", "translations", "[ProductTranslate!]!"},
		{"CreditNote", "userID", "ID!"},
		{"CreditNote", "itemType", "ItemType!"},
		{"BomItem", "itemType", "ItemType!"},
		{"CreditNote", "status", "CreditNoteEntryStatus!"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %s, want %s", c[0], c[1], got, c[2])
		}
	}
	if strings.Count(sdl, "enum ItemType") != 1 || s.Types["CreditNoteStatus"] != nil {
		t.Errorf("enums:\n%s", sdl)
	}
	// An input takes no object type: the field is JSON there, as veloxe had it.
	in := strings.Join(fieldsOf(s, "CreateCreditNoteInput"), "\n")
	for _, want := range []string{"customFields: Map!", "organizationIds: [ID!]!", "translations: JSON!", "itemType: ItemType!"} {
		if !strings.Contains(in, want) {
			t.Errorf("CreateCreditNoteInput lacks %q:\n%s", want, in)
		}
	}

	for name, c := range map[string]struct {
		f    *plugin.Field
		want string
	}{
		"non-null": {typed("code", "string", "string", "", "ID!"), `graphql.Type("ID!") on a field is not a GraphQL type`},
		"enum list": {func() *plugin.Field {
			f := itemType()
			f.Annotations = ann(t, graphql.Type("[ItemType!]"))
			return f
		}(), `on an enum field names its enum`},
	} {
		e := entity("Thing", c.f)
		e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
		if _, err := Render(model(e)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	key := entity("Thing")
	key.Fields[0].Annotations = ann(t, graphql.Type("Code"))
	if _, err := Render(model(key)); err == nil || !strings.Contains(err.Error(), "on the key: the key is an ID") {
		t.Errorf("key: %v", err)
	}
}

// Under Config.Veloxe, a foreign key bound to an edge is an ID in the type,
// named as veloxe named it, and its predicates are named from it.
func TestVeloxeForeignKeys(t *testing.T) {
	ck := &plugin.Field{Name: "customer_id", GoName: "CustomerID", Type: "int64", Annotations: ann(t, graphql.WhereOps())}
	order := entity("SalesOrder", ck, &plugin.Field{Name: "image_url", GoName: "ImageURL", Type: "string"})
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn, cust.OwnsKey, cust.Required = "customer_id", "SalesOrder", true, true
	order.Edges = []*plugin.Edge{cust}
	m := model(order, entity("Customer"))
	sdl, err := RenderConfig(m, Config{Veloxe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  customerId: ID!\n", "  imageUrl: String!\n", "  customerIdNEQ: ID\n", "  customerIdNotIn: [ID!]\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
	plain, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "customerId") || !strings.Contains(plain, "customerIDNEQ: ID") || !strings.Contains(plain, "imageURL") {
		t.Errorf("velox's names changed:\n%s", plain)
	}
}

// Under Config.Veloxe, WhereOps() on a field named as a key gives equality
// alone, as veloxe's defaults did; other strings keep theirs.
func TestVeloxeIDDefaults(t *testing.T) {
	ref := &plugin.Field{Name: "reference_id", GoName: "ReferenceID", Type: "string", Annotations: ann(t, graphql.WhereOps())}
	org := &plugin.Field{Name: "org_id", GoName: "OrgID", Type: "int64", Nullable: true, Annotations: ann(t, graphql.WhereOps())}
	code := &plugin.Field{Name: "code", GoName: "Code", Type: "string", Annotations: ann(t, graphql.WhereOps())}
	m := model(entity("Thing", ref, org, code))
	sdl, err := RenderConfig(m, Config{Veloxe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  referenceIdNotIn: [String!]\n", "  orgIdIsNil: Boolean\n", "  codeContains: String\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
	for _, bad := range []string{"referenceIdContains", "orgIdGT"} {
		if strings.Contains(sdl, bad) {
			t.Errorf("has %s:\n%s", bad, sdl)
		}
	}
	if plain, err := Render(m); err != nil || !strings.Contains(plain, "referenceIDContains") {
		t.Errorf("velox's defaults changed: %v\n%s", err, plain)
	}
}

// An Other field compares in SQL: WhereOps gives it equality and order,
// typed with its graphql.Type, and IsNil and NotNil when it is nullable.
func TestOtherPredicates(t *testing.T) {
	amt := &plugin.Field{Name: "conversion_value", GoName: "ConversionValue", Type: "other", ElemType: "decimal.Decimal", PkgPath: "github.com/shopspring/decimal", Nullable: true,
		Annotations: ann(t, graphql.Type("Decimal"), graphql.WhereOps(graphql.OpsComparison))}
	m := model(entity("Participant", amt))
	s, sdl := load(t, m, "scalar Decimal\n")
	if typeOf(s, "Participant", "conversionValue") != "Decimal" || strings.Contains(sdl, "IsNil") {
		t.Errorf("type:\n%s", sdl)
	}
	// veloxe's: a nullable field's predicates test for NULL too.
	sdl, err := RenderConfig(m, Config{Veloxe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  conversionValueGT: Decimal\n", "  conversionValueLTE: Decimal\n", "  conversionValueIsNil: Boolean\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
}
