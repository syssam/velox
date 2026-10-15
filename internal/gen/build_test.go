package gen

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2/internal/gen/internal/other"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/load"
)

func build(t *testing.T, ss ...schema.Interface) (*Graph, error) {
	t.Helper()
	b, err := load.Marshal("github.com/syssam/velox2/internal/gen", ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m load.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return Build(&m)
}

func mustBuild(t *testing.T, ss ...schema.Interface) *Graph {
	t.Helper()
	g, err := build(t, ss...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The blog: users write posts; a post has one author.
type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Tenant(), field.String("name"), field.Bool("active").Default(true)}
}
func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", Post.Type), edge.To("groups", Group.Type)}
}
func (User) Indexes() []schema.Index { return []schema.Index{index.Fields("org_id", "name").Unique()} }

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{field.Int64("author_id"), field.String("title")}
}
func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id")}
}

type Group struct{ schema.Schema }

func (Group) Edges() []schema.Edge { return []schema.Edge{edge.From("users", User.Type).Ref("groups")} }

func TestBuildBlog(t *testing.T) {
	g := mustBuild(t, User{}, Post{}, Group{})
	u, p := g.Entity("User"), g.Entity("Post")
	if u.Table != "users" || u.Fields[0].Name != "id" || !u.Fields[0].AutoID || u.Fields[0].Type != "int64" || u.Tenant != "org_id" {
		t.Fatalf("user %+v", u)
	}
	posts, author := u.Edge("posts"), p.Edge("author")
	if posts.Rel.Kind != "O2M" || author.Rel.Kind != "M2O" || posts.Rel.Table != "posts" ||
		posts.Rel.Columns[0] != "author_id" || posts.Field != "author_id" || author.Ref != "posts" || posts.Ref != "author" {
		t.Fatalf("posts %+v author %+v", posts, author)
	}
	groups, users := u.Edge("groups"), g.Entity("Group").Edge("users")
	if groups.Rel.Kind != "M2M" || groups.Rel.Table != "user_groups" || groups.Rel.Columns[0] != "user_id" || groups.Rel.Columns[1] != "group_id" ||
		users.Rel.Table != "user_groups" || users.Rel.Columns[0] != "group_id" || users.Rel.Columns[1] != "user_id" {
		t.Fatalf("groups %+v users %+v", groups, users)
	}
	if ix := u.Indexes[0]; ix.Name != "users_org_id_name" || !ix.Unique || ix.Columns[1] != "name" {
		t.Fatalf("index %+v", ix)
	}
}

func TestBuildDefaultBoolIsPointer(t *testing.T) {
	f := mustBuild(t, User{}, Post{}, Group{}).Entity("User").Field("active")
	if !f.Pointer || f.Nillable || f.Optional {
		t.Fatalf("active %+v: a literal Default on a bool makes a pointer, NOT NULL column", f)
	}
}

// An O2M without a bound field gets an implicit foreign key on the target.
type Owner struct{ schema.Schema }

func (Owner) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", Pet.Type)} }

type Pet struct{ schema.Schema }

func TestBuildImplicitForeignKey(t *testing.T) {
	g := mustBuild(t, Owner{}, Pet{})
	f := g.Entity("Pet").Field("owner_pets")
	if f == nil || f.Implicit != "pets" || f.GoName != "OwnerPets" || !f.Nillable || f.Type != "int64" {
		t.Fatalf("implicit %+v", f)
	}
	if e := g.Entity("Owner").Edge("pets"); e.Field != "owner_pets" || e.Rel.Columns[0] != "owner_pets" {
		t.Fatalf("pets %+v", e)
	}
}

// A tree and a friends M2M on one type.
type Node struct{ schema.Schema }

func (Node) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", Node.Type).From("parent").Unique(), edge.To("friends", Node.Type)}
}

func TestBuildSelfReference(t *testing.T) {
	g := mustBuild(t, Node{})
	n := g.Entity("Node")
	c, p, f := n.Edge("children"), n.Edge("parent"), n.Edge("friends")
	if c.Rel.Kind != "O2M" || p.Rel.Kind != "M2O" || c.Rel.Table != "nodes" || p.Field == "" {
		t.Fatalf("children %+v parent %+v", c, p)
	}
	if f.Rel.Kind != "M2M" || !f.Bidi || f.Rel.Table != "node_friends" || f.Rel.Columns[0] != "node_id" || f.Rel.Columns[1] != "friend_id" {
		t.Fatalf("friends %+v", f)
	}
	a, _ := json.Marshal(g)
	b, _ := json.Marshal(mustBuild(t, Node{}))
	if string(a) != string(b) {
		t.Fatal("Build is not deterministic")
	}
}

func TestBuildSelfReferenceFields(t *testing.T) {
	n := mustBuild(t, Node{}).Entity("Node")
	if c, p := n.Edge("children"), n.Edge("parent"); c.Bidi || p.Bidi || c.Ref != "parent" || p.Ref != "children" ||
		p.Field != "node_children" || c.Field != "node_children" {
		t.Fatalf("children %+v parent %+v: a pair is not bidirectional", c, p)
	}
	if f := n.Field("node_children"); f == nil || f.Implicit != "parent" || !f.Optional || n.Fields[len(n.Fields)-1] != f {
		t.Fatalf("node_children %+v", f)
	}
}

// A one-to-one pair, a declared string ID, a table annotation and an edge
// storage key.
type Account struct{ schema.Schema }

func (Account) Fields() []schema.Field {
	return []schema.Field{field.String("email"), field.String("id").MaxLen(36)}
}
func (Account) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("profile", Profile.Type).Unique(),
		edge.To("roles", Role.Type).StorageKey(edge.Table("account_role_links"), edge.Columns("acct", "role")),
		edge.To("home", Home.Type).Unique().Required(),
	}
}

type Profile struct{ schema.Schema }

func (Profile) Edges() []schema.Edge {
	return []schema.Edge{edge.From("account", Account.Type).Ref("profile").Unique().Required()}
}
func (Profile) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("app", "account_profiles")}
}

type Role struct{ schema.Schema }

func (Role) Edges() []schema.Edge {
	return []schema.Edge{edge.From("accounts", Account.Type).Ref("roles")}
}

type Home struct{ schema.Schema }

func (Home) Fields() []schema.Field { return []schema.Field{field.UUID("id", uuidValue{})} }

func TestBuildRelations(t *testing.T) {
	g := mustBuild(t, Account{}, Profile{}, Role{}, Home{})
	a, p := g.Entity("Account"), g.Entity("Profile")
	if a.Fields[0].Name != "id" || a.Fields[0].AutoID || a.Fields[0].Type != "string" || a.Fields[1].Name != "email" {
		t.Fatalf("account fields %+v %+v", a.Fields[0], a.Fields[1])
	}
	if p.Table != "account_profiles" || p.TableSchema != "app" {
		t.Fatalf("profile table %q.%q", p.TableSchema, p.Table)
	}
	prof, acct := a.Edge("profile"), p.Edge("account")
	if prof.Rel.Kind != "O2O" || acct.Rel.Kind != "O2O" || prof.Rel.Table != "account_profiles" || prof.Rel.Columns[0] != "account_profile" {
		t.Fatalf("profile %+v account %+v", prof, acct)
	}
	fk := p.Field("account_profile")
	if fk == nil || fk.Type != "string" || fk.Optional || fk.Nillable || !fk.Unique || fk.Implicit != "account" || prof.Field != fk.Name {
		t.Fatalf("account_profile %+v", fk)
	}
	roles, accts := a.Edge("roles"), g.Entity("Role").Edge("accounts")
	if roles.Rel.Table != "account_role_links" || roles.Rel.Columns[0] != "acct" || roles.Rel.Columns[1] != "role" ||
		accts.Rel.Table != "account_role_links" || accts.Rel.Columns[0] != "role" || accts.Rel.Columns[1] != "acct" {
		t.Fatalf("roles %+v accounts %+v", roles, accts)
	}
	home := a.Edge("home")
	hk := a.Field("account_home")
	if home.Rel.Kind != "M2O" || home.Rel.Table != "accounts" || hk == nil || hk.Type != "uuid" || hk.GoType == nil ||
		hk.GoType.Ident != "gen.uuidValue" || hk.Optional || hk.Implicit != "home" || a.Fields[len(a.Fields)-1] != hk {
		t.Fatalf("home %+v account_home %+v", home, hk)
	}
}

// --- one schema type (or pair) per rule

type uuidValue [16]byte

func (uuidValue) Value() (driver.Value, error) { return nil, nil }

type pointValue struct{}

func (pointValue) Value() (driver.Value, error) { return nil, nil }

// notSchema is a type of this package that is not a schema.
type notSchema struct{}

func (notSchema) Type() {}

type dupField struct{ schema.Schema }

func (dupField) Fields() []schema.Field { return []schema.Field{field.String("a"), field.Int("a")} }

type dupColumn struct{ schema.Schema }

func (dupColumn) Fields() []schema.Field {
	return []schema.Field{field.String("a"), field.String("b").StorageKey("a")}
}

type dupEdge struct{ schema.Schema }

func (dupEdge) Edges() []schema.Edge {
	return []schema.Edge{edge.To("e", dupEdge.Type), edge.To("e", dupEdge.Type)}
}

type edgeGoName struct{ schema.Schema }

func (edgeGoName) Fields() []schema.Field { return []schema.Field{field.String("owner")} }
func (edgeGoName) Edges() []schema.Edge {
	return []schema.Edge{edge.To("owner", edgeGoName.Type).Unique()}
}

type unknownTarget struct{ schema.Schema }

func (unknownTarget) Edges() []schema.Edge { return []schema.Edge{edge.To("x", notSchema.Type)} }

type otherPkgTarget struct{ schema.Schema }

func (otherPkgTarget) Edges() []schema.Edge { return []schema.Edge{edge.To("x", other.Thing.Type)} }

type notSchemaTarget struct{ schema.Schema }

func (notSchemaTarget) Edges() []schema.Edge { return []schema.Edge{edge.To("x", time.Time.IsZero)} }

type noRef struct{ schema.Schema }

func (noRef) Edges() []schema.Edge { return []schema.Edge{edge.From("x", noRef.Type)} }

type badRef struct{ schema.Schema }

func (badRef) Edges() []schema.Edge { return []schema.Edge{edge.From("x", badRef.Type).Ref("nope")} }

type wrongRef struct{ schema.Schema }

func (wrongRef) Edges() []schema.Edge {
	return []schema.Edge{edge.From("x", refHolder.Type).Ref("pets")}
}

type refHolder struct{ schema.Schema }

func (refHolder) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", Pet.Type)} }

type reqA struct{ schema.Schema }

func (reqA) Edges() []schema.Edge { return []schema.Edge{edge.To("b", reqB.Type).Unique().Required()} }

type reqB struct{ schema.Schema }

func (reqB) Edges() []schema.Edge {
	return []schema.Edge{edge.From("a", reqA.Type).Ref("b").Unique().Required()}
}

type fieldMissing struct{ schema.Schema }

func (fieldMissing) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fieldMissing.Type).Unique().Field("parent_id")}
}

type fieldWrongSide struct{ schema.Schema }

func (fieldWrongSide) Fields() []schema.Field { return []schema.Field{field.Int64("x")} }
func (fieldWrongSide) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).Field("x")}
}

type fieldMany struct{ schema.Schema }

func (fieldMany) Fields() []schema.Field { return []schema.Field{field.Int64("x")} }
func (fieldMany) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", fieldMany.Type).Field("x")}
}

type fieldWrongType struct{ schema.Schema }

func (fieldWrongType) Fields() []schema.Field {
	return []schema.Field{field.Int("parent_id").Optional()}
}
func (fieldWrongType) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fieldWrongType.Type).Unique().Field("parent_id")}
}

type fieldOptional struct{ schema.Schema }

func (fieldOptional) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional()}
}
func (fieldOptional) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fieldOptional.Type).Unique().Required().Field("parent_id")}
}

type twoTenants struct{ schema.Schema }

func (twoTenants) Fields() []schema.Field {
	return []schema.Field{field.Int64("a").Tenant(), field.Int64("b").Tenant()}
}

type optionalTenant struct{ schema.Schema }

func (optionalTenant) Fields() []schema.Field {
	return []schema.Field{field.Int64("org").Tenant().Optional()}
}

type defaultTenant struct{ schema.Schema }

func (defaultTenant) Fields() []schema.Field {
	return []schema.Field{field.Int64("org").Tenant().Default(1)}
}

type bytesTenant struct{ schema.Schema }

func (bytesTenant) Fields() []schema.Field { return []schema.Field{field.Bytes("org").Tenant()} }

type emptyEnum struct{ schema.Schema }

func (emptyEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s")} }

type dupEnum struct{ schema.Schema }

func (dupEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s").Values("a", "b", "a")} }

type badEnum struct{ schema.Schema }

func (badEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s").Values("in-progress")} }

type optionalID struct{ schema.Schema }

func (optionalID) Fields() []schema.Field { return []schema.Field{field.Int64("id").Optional()} }

type floatID struct{ schema.Schema }

func (floatID) Fields() []schema.Field { return []schema.Field{field.Float64("id")} }

type unknownIndexField struct{ schema.Schema }

func (unknownIndexField) Indexes() []schema.Index { return []schema.Index{index.Fields("nope")} }

type unknownIndexEdge struct{ schema.Schema }

func (unknownIndexEdge) Indexes() []schema.Index { return []schema.Index{index.Edges("nope")} }

type indexForeignEdge struct{ schema.Schema }

func (indexForeignEdge) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", Pet.Type)} }
func (indexForeignEdge) Indexes() []schema.Index {
	return []schema.Index{index.Edges("pets")}
}

type emptyIndex struct{ schema.Schema }

func (emptyIndex) Indexes() []schema.Index { return []schema.Index{index.Fields()} }

type otherNoType struct{ schema.Schema }

func (otherNoType) Fields() []schema.Field { return []schema.Field{field.Other("o", pointValue{})} }

type joinClash struct{ schema.Schema }

func (joinClash) Edges() []schema.Edge { return []schema.Edge{edge.To("links", joinClash.Type)} }

type clashTable struct{ schema.Schema }

func (clashTable) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("join_clash_links")}
}

type inlineNotSchema struct{ schema.Schema }

func (inlineNotSchema) Edges() []schema.Edge {
	return []schema.Edge{edge.To("x", notSchema.Type).From("y")}
}

type inlineOther struct{ schema.Schema }

func (inlineOther) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).From("owner")}
}

func TestBuildRules(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   []schema.Interface
		want string
	}{
		{"1 duplicate field", []schema.Interface{dupField{}}, `schema dupField: field "a": declared twice`},
		{"2 duplicate column", []schema.Interface{dupColumn{}}, `schema dupColumn: field "b": column "a" is also field "a"'s`},
		{"3 duplicate edge", []schema.Interface{dupEdge{}}, `schema dupEdge: edge "e": declared twice`},
		{"4 edge go name", []schema.Interface{edgeGoName{}}, `schema edgeGoName: edge "owner": its Go name Owner is also field "owner"'s`},
		{"5 unknown target", []schema.Interface{unknownTarget{}}, `schema unknownTarget: edge "x": type notSchema is not a schema of this package`},
		{"5 other package", []schema.Interface{otherPkgTarget{}}, `schema otherPkgTarget: edge "x": type other.Thing is not a schema of this package`},
		{"5 not a schema", []schema.Interface{notSchemaTarget{}}, `schema notSchemaTarget: edge "x": type time.Time is not a schema of this package`},
		{"6 no ref", []schema.Interface{noRef{}}, `schema noRef: edge "x": an inverse edge needs Ref(name)`},
		{"7 bad ref", []schema.Interface{badRef{}}, `schema badRef: edge "x": badRef has no edge "nope" to refer to`},
		{"8 wrong ref", []schema.Interface{wrongRef{}, refHolder{}, Pet{}}, `schema wrongRef: edge "x": refHolder.pets points to Pet, not to this schema`},
		{"9 both required", []schema.Interface{reqA{}, reqB{}}, `schema reqB: edge "a": cannot be required in both directions with reqA.b`},
		{"10 field missing", []schema.Interface{fieldMissing{}}, `schema fieldMissing: edge "parent": field "parent_id" is not declared`},
		{"11 field wrong side", []schema.Interface{fieldWrongSide{}, Pet{}}, `schema fieldWrongSide: edge "pets": binds field "x", but the foreign key is in table pets`},
		{"11 field on M2M", []schema.Interface{fieldMany{}}, `schema fieldMany: edge "peers": binds field "x", but the foreign key is in table field_many_peers`},
		{"12 field wrong type", []schema.Interface{fieldWrongType{}}, `schema fieldWrongType: edge "parent": field "parent_id" is int; fieldWrongType's ID is int64`},
		{"13 field optional", []schema.Interface{fieldOptional{}}, `schema fieldOptional: edge "parent": field "parent_id" must be Optional exactly when the edge is not Required`},
		{"14 two tenants", []schema.Interface{twoTenants{}}, `schema twoTenants: field "b": a second tenant field; "a" is the tenant`},
		{"15 optional tenant", []schema.Interface{optionalTenant{}}, `schema optionalTenant: field "org": the tenant field must be required and not Nillable`},
		{"16 default tenant", []schema.Interface{defaultTenant{}}, `schema defaultTenant: field "org": the tenant field cannot have a default: the context's tenant fills it`},
		{"17 bytes tenant", []schema.Interface{bytesTenant{}}, `schema bytesTenant: field "org": the tenant field must be of a comparable type`},
		{"18 empty enum", []schema.Interface{emptyEnum{}}, `schema emptyEnum: field "s": an enum needs Values`},
		{"19 duplicate enum value", []schema.Interface{dupEnum{}}, `schema dupEnum: field "s": value "a" is given twice`},
		{"20 enum go name", []schema.Interface{badEnum{}}, `schema badEnum: field "s": value "in-progress" has no Go name; use NamedValues`},
		{"21 optional id", []schema.Interface{optionalID{}}, `schema optionalID: field "id": the ID must be required, not Nillable, and have no UpdateDefault`},
		{"22 float id", []schema.Interface{floatID{}}, `schema floatID: field "id": the ID cannot be of type float64`},
		{"23 unknown index field", []schema.Interface{unknownIndexField{}}, `schema unknownIndexField: index 0: names unknown field "nope"`},
		{"23 unknown index edge", []schema.Interface{unknownIndexEdge{}}, `schema unknownIndexEdge: index 0: names unknown edge "nope"`},
		{"24 index foreign edge", []schema.Interface{indexForeignEdge{}, Pet{}}, `schema indexForeignEdge: index 0: edge "pets" has no column in table index_foreign_edges`},
		{"25 empty index", []schema.Interface{emptyIndex{}}, `schema emptyIndex: index 0: names no fields or edges`},
		{"26 other without schema type", []schema.Interface{otherNoType{}}, `schema otherNoType: field "o": an Other field needs SchemaType`},
		{"27 join table clash", []schema.Interface{joinClash{}, clashTable{}}, `schema joinClash: edge "links": join table join_clash_links is also the table of schema clashTable`},
		{"5 inline inverse", []schema.Interface{inlineNotSchema{}}, `schema inlineNotSchema: edge "x": type notSchema is not a schema of this package`},
		{"28 inline inverse to another schema", []schema.Interface{inlineOther{}, Pet{}}, `schema inlineOther: edge "owner": From(...) inline is allowed only on an edge to its own schema`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := build(t, c.ss...)
			var ge *Error
			if err == nil || !strings.Contains(err.Error(), c.want) || !errors.As(err, &ge) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// The rule schemas each break one rule and nothing else.
func TestBuildRulesAreSingle(t *testing.T) {
	for _, ss := range [][]schema.Interface{
		{dupField{}}, {dupColumn{}}, {dupEdge{}}, {edgeGoName{}}, {unknownTarget{}}, {otherPkgTarget{}}, {notSchemaTarget{}},
		{noRef{}}, {badRef{}}, {wrongRef{}, refHolder{}, Pet{}}, {reqA{}, reqB{}}, {fieldMissing{}},
		{fieldWrongSide{}, Pet{}}, {fieldMany{}}, {fieldWrongType{}}, {fieldOptional{}}, {twoTenants{}}, {optionalTenant{}},
		{defaultTenant{}}, {bytesTenant{}}, {emptyEnum{}}, {dupEnum{}}, {badEnum{}}, {optionalID{}}, {floatID{}},
		{unknownIndexField{}}, {unknownIndexEdge{}}, {indexForeignEdge{}, Pet{}}, {emptyIndex{}}, {otherNoType{}},
		{joinClash{}, clashTable{}}, {inlineOther{}, Pet{}}, {inlineNotSchema{}},
	} {
		_, err := build(t, ss...)
		if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
			t.Errorf("%T: %d errors, want 1:\n%v", ss[0], n, err)
		}
	}
}

func TestBuildErrorsAreSortedAndJoined(t *testing.T) {
	_, err := build(t, twoTenants{}, dupField{})
	if err == nil || strings.Index(err.Error(), "dupField") > strings.Index(err.Error(), "twoTenants") {
		t.Fatalf("errors must be ordered by schema name: %v", err)
	}
}

// Within a schema, errors follow the order of the rules, not of the steps
// that find them.
type manyErrors struct{ schema.Schema }

func (manyErrors) Fields() []schema.Field {
	return []schema.Field{field.Enum("s"), field.String("a"), field.String("a")}
}
func (manyErrors) Edges() []schema.Edge    { return []schema.Edge{edge.From("x", manyErrors.Type)} }
func (manyErrors) Indexes() []schema.Index { return []schema.Index{index.Fields()} }

func TestBuildErrorsFollowRuleOrder(t *testing.T) {
	_, err := build(t, manyErrors{})
	want := `schema manyErrors: field "a": declared twice
schema manyErrors: edge "x": an inverse edge needs Ref(name)
schema manyErrors: field "s": an enum needs Values
schema manyErrors: index 0: names no fields or edges`
	if err == nil || err.Error() != want {
		t.Fatalf("got\n%v\nwant\n%s", err, want)
	}
}
