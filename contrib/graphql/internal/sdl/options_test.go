package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// schemaCamel names a schema name under names=schema.
func TestSchemaCamel(t *testing.T) {
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
		if got := schemaCamel(in); got != want {
			t.Errorf("schemaCamel(%q) = %q, want %q", in, got, want)
		}
	}
}

// graphql.Type on a field is the field's GraphQL type: a name or a list the
// application declares, or the name of an enum field's
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
	// An input takes no object type: the field is JSON there.
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

// A foreign key bound to an edge is an ID in the type, as in ent, named
// from the field by names=, and its predicates are named from it.
func TestForeignKeyNames(t *testing.T) {
	ck := &plugin.Field{Name: "customer_id", GoName: "CustomerID", Type: "int64", Annotations: ann(t, graphql.WhereOps())}
	order := entity("SalesOrder", ck, &plugin.Field{Name: "image_url", GoName: "ImageURL", Type: "string"})
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn, cust.OwnsKey, cust.Required = "customer_id", "SalesOrder", true, true
	order.Edges = []*plugin.Edge{cust}
	m := model(order, entity("Customer"))
	for cfg, wants := range map[string][]string{
		"go":     {"  customerID: ID!\n", "  imageURL: String!\n", "  customerIDNEQ: ID\n", "  customerIDNotIn: [ID!]\n"},
		"schema": {"  customerId: ID!\n", "  imageUrl: String!\n", "  customerIdNEQ: ID\n", "  customerIdNotIn: [ID!]\n"},
	} {
		sdl, err := RenderConfig(m, Config{Names: cfg})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(sdl, want) {
				t.Errorf("names=%s lacks %q:\n%s", cfg, want, sdl)
			}
		}
		if strings.Contains(sdl, "customerIDGT") || strings.Contains(sdl, "customerIdGT") {
			t.Errorf("names=%s: a foreign key has an order predicate:\n%s", cfg, sdl)
		}
	}
}

// WhereOps() on a field named as a key gives its kind's predicates; under
// idops=equality equality alone, with IsNil and NotNil when it is nullable.
// Other strings keep theirs either way.
func TestIDOps(t *testing.T) {
	ref := &plugin.Field{Name: "reference_id", GoName: "ReferenceID", Type: "string", Annotations: ann(t, graphql.WhereOps())}
	org := &plugin.Field{Name: "org_id", GoName: "OrgID", Type: "int64", Nullable: true, Annotations: ann(t, graphql.WhereOps())}
	code := &plugin.Field{Name: "code", GoName: "Code", Type: "string", Annotations: ann(t, graphql.WhereOps())}
	m := model(entity("Thing", ref, org, code))
	sdl, err := RenderConfig(m, Config{IDOps: "equality"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  referenceIDNotIn: [String!]\n", "  orgIDIsNil: Boolean\n", "  codeContains: String\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
	for _, bad := range []string{"referenceIDContains", "orgIDGT", "idGT"} {
		if strings.Contains(sdl, bad) {
			t.Errorf("has %s:\n%s", bad, sdl)
		}
	}
	plain, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"referenceIDContains", "orgIDGT", "idGT", "orgIDIsNil"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the default lacks %s:\n%s", want, plain)
		}
	}
}

// An Other field compares in SQL: WhereOps gives it equality and order,
// typed with its graphql.Type, and IsNil and NotNil when it is nullable, as
// ent's predicates do.
func TestOtherPredicates(t *testing.T) {
	amt := &plugin.Field{Name: "conversion_value", GoName: "ConversionValue", Type: "other", ElemType: "decimal.Decimal", PkgPath: "github.com/shopspring/decimal", Nullable: true,
		Annotations: ann(t, graphql.Type("Decimal"), graphql.WhereOps(graphql.OpsComparison))}
	m := model(entity("Participant", amt))
	s, sdl := load(t, m, "scalar Decimal\n")
	if typeOf(s, "Participant", "conversionValue") != "Decimal" {
		t.Errorf("type:\n%s", sdl)
	}
	for _, want := range []string{"  conversionValueGT: Decimal\n", "  conversionValueLTE: Decimal\n", "  conversionValueIsNil: Boolean\n", "  conversionValueNotNil: Boolean\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
}

// A foreign key whose edge GraphQL leaves out is an ID in the type still,
// with its predicates named from it.
func TestSkippedEdgeKey(t *testing.T) {
	fk := &plugin.Field{Name: "tax_code_id", GoName: "TaxCodeID", Type: "int64", ElemType: "int64", Nullable: true, Annotations: ann(t, graphql.WhereOps())}
	asset := entity("Asset", fk)
	ed := edge("tax_code", plugin.M2O, "TaxCode")
	ed.Field, ed.FieldOn = "tax_code_id", "Asset"
	ed.Annotations = ann(t, graphql.Skip(graphql.SkipAll))
	asset.Edges = []*plugin.Edge{ed}
	m := model(asset, entity("TaxCode"))
	sdl, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  taxCodeID: ID\n", "  taxCodeIDNEQ: ID\n", "  taxCodeIDNotIn: [ID!]\n", "  taxCodeIDIsNil: Boolean\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("lacks %q:\n%s", want, sdl)
		}
	}
	if strings.Contains(sdl, "taxCode:") || strings.Contains(sdl, "taxCodeIDGT") {
		t.Errorf("the edge or an order predicate is shown:\n%s", sdl)
	}
}

// Each string option refuses a value it does not know, naming the option
// and its values; the empty value is the default.
func TestCheckOptions(t *testing.T) {
	m := model(entity("Thing", field("name", "string")))
	for _, key := range []string{"names", "filters", "node", "idops", "orderfields", "queryfields", "optional", "requirededges", "inputkeys", "jsontypes"} {
		var cfg Config
		if !cfg.SetOption(key, "bogus") {
			t.Fatalf("SetOption(%q) does not know it", key)
		}
		if _, err := RenderConfig(m, cfg); err == nil || !strings.Contains(err.Error(), key+"=bogus: "+key+" is ") {
			t.Errorf("%s=bogus: %v", key, err)
		}
	}
	var cfg Config
	if cfg.SetOption("compat", "x") || cfg.SetOption("where", "whitelist") {
		t.Error("SetOption takes an option that is not a string option")
	}
	for _, v := range []string{"kind", "equality"} {
		if _, err := RenderConfig(m, Config{IDOps: v}); err != nil {
			t.Errorf("idops=%s: %v", v, err)
		}
	}
	if got := strings.Join(OptionHelp(), ", "); !strings.Contains(got, "idops=kind or idops=equality") || !strings.Contains(got, "names=go or names=schema") {
		t.Errorf("OptionHelp: %s", got)
	}
}
