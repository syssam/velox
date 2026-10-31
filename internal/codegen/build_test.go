package codegen

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/internal/other"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/internal/ddl"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/load"
	"github.com/syssam/velox/schema/mixin"
)

func build(t *testing.T, ss ...schema.Interface) (*Graph, error) {
	t.Helper()
	b, err := load.Marshal("github.com/syssam/velox/internal/codegen", ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Errors) > 0 {
		t.Fatalf("builder errors %+v", m.Errors)
	}
	return Build(&m)
}

// plain is the text of err, a join of *Error values, without their positions.
func plain(err error) string {
	j, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return fmt.Sprint(err)
	}
	var lines []string
	for _, e := range j.Unwrap() {
		if ge, ok := e.(*Error); ok {
			c := *ge
			c.Pos = ""
			e = &c
		}
		lines = append(lines, e.Error())
	}
	return strings.Join(lines, "\n")
}

// here returns the line it is called on.
func here() int {
	_, _, line, _ := runtime.Caller(1)
	return line
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
	return []schema.Field{field.Int64("org_id").Tenant(), field.String("name"), field.Bool("active").Nillable().Default(true)}
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
	if ix := u.Indexes[0]; ix.Name != "user_org_id_name" || !ix.Derived || !ix.Unique || ix.Columns[1] != "name" {
		t.Fatalf("index %+v", ix)
	}
}

// Nillable alone makes a pointer; Optional does not, and a Default does not
// change it.
func TestBuildNillableIsPointer(t *testing.T) {
	g := mustBuild(t, User{}, Post{}, Group{}, defaults{})
	if f := g.Entity("User").Field("active"); !f.Pointer || !f.Nillable || f.Optional {
		t.Fatalf("active %+v: Nillable makes a pointer, and its Default does not change that", f)
	}
	d := g.Entity("defaults")
	for name, want := range map[string]bool{"seen": true, "nick": false, "label": false, "count": true, "bio": true, "opt": false} {
		if f := d.Field(name); f.Pointer != want {
			t.Errorf("%s: pointer %v, want %v", name, f.Pointer, want)
		}
	}
}

// defaults has a pointer exactly where it has Nillable.
type defaults struct{ schema.Schema }

func (defaults) Fields() []schema.Field {
	return []schema.Field{
		field.Time("seen").Optional().Nillable(),
		field.String("nick").Optional().Default("n"),
		field.String("label").Default("l"),
		field.Int("count").DefaultFunc(func() int { return 1 }).Nillable(),
		field.Text("bio").Nillable(),
		field.Int("opt").Optional(),
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

func (Home) Fields() []schema.Field { return []schema.Field{field.UUID("id", UUIDValue{})} }

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
		hk.GoType.Ident != "codegen.UUIDValue" || hk.Optional || hk.Implicit != "home" || a.Fields[len(a.Fields)-1] != hk {
		t.Fatalf("home %+v account_home %+v", home, hk)
	}
}

// A unique assoc edge with a non-unique inverse: the foreign key is in the
// assoc owner's table.
type Seat struct{ schema.Schema }

func (Seat) Edges() []schema.Edge { return []schema.Edge{edge.To("venue", Venue.Type).Unique()} }

type Venue struct{ schema.Schema }

func (Venue) Edges() []schema.Edge { return []schema.Edge{edge.From("seats", Seat.Type).Ref("venue")} }

func TestBuildUniqueAssocInverse(t *testing.T) {
	g := mustBuild(t, Seat{}, Venue{})
	s, v := g.Entity("Seat"), g.Entity("Venue")
	venue, seats := s.Edge("venue"), v.Edge("seats")
	if venue.Rel.Kind != "M2O" || seats.Rel.Kind != "O2M" || venue.Rel.Table != "seats" || seats.Rel.Table != "seats" ||
		venue.Rel.Columns[0] != "seat_venue" || seats.Rel.Columns[0] != "seat_venue" || venue.Field != "seat_venue" || seats.Field != "seat_venue" {
		t.Fatalf("venue %+v seats %+v", venue, seats)
	}
	if f := s.Field("seat_venue"); f == nil || f.Implicit != "venue" || !f.Optional || f.Unique || v.Field("seat_venue") != nil {
		t.Fatalf("seat_venue %+v", f)
	}
}

// Schemas whose names ent's plural leaves unchanged: their tables are the
// names' snake_case, and their handles end in Slice.
type (
	Status    struct{ schema.Schema }
	Settings  struct{ schema.Schema }
	Data      struct{ schema.Schema }
	Media     struct{ schema.Schema }
	Equipment struct{ schema.Schema }
)

func TestEntityNamedStatus(t *testing.T) {
	g := mustBuild(t, Status{}, Settings{}, Data{}, Media{}, Equipment{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, table, handles string }{
		{"Status", "status", "StatusSlice"},
		{"Settings", "settings", "SettingsSlice"},
		{"Data", "data", "DataSlice"},
		{"Media", "media", "MediaSlice"},
		{"Equipment", "equipment", "EquipmentSlice"},
	} {
		if e := o.entity(c.name); e == nil || e.Table != c.table || e.Handles != c.handles {
			t.Errorf("%s: %+v, want table %s and handles %s", c.name, e, c.table, c.handles)
		}
	}
}

// A self many-to-many edge's second column is the singular of its name:
// ent's, so children gives child_id.
type Category struct{ schema.Schema }

func (Category) Edges() []schema.Edge { return []schema.Edge{edge.To("children", Category.Type)} }

func TestBuildJoinColumnsSingular(t *testing.T) {
	c := mustBuild(t, Category{}).Entity("Category").Edge("children")
	if c.Rel.Kind != "M2M" || c.Rel.Table != "category_children" || c.Rel.Columns[0] != "category_id" || c.Rel.Columns[1] != "child_id" {
		t.Fatalf("children %+v", c.Rel)
	}
}

// A join entity is named by the singular of its table, which is a plural
// already, and its handles by the table itself; a table whose singular is
// itself takes GoPlural's handles.
type (
	Vault struct{ schema.Schema }
	Crew  struct{ schema.Schema }
)

func (Vault) Edges() []schema.Edge { return []schema.Edge{edge.To("data", Vault.Type)} }

func (Crew) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("teams", Crew.Type).StorageKey(edge.Table("crew_team")),
		edge.To("kit", Crew.Type).StorageKey(edge.Table("crew_equipment")),
	}
}

func TestJoinEntityNames(t *testing.T) {
	o, err := Lower(mustBuild(t, Category{}, Vault{}, Crew{}), Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ table, name, handles string }{
		{"category_children", "CategoryChild", "CategoryChildren"},
		{"vault_data", "VaultDatum", "VaultData"},
		{"crew_team", "CrewTeam", "CrewTeams"},
		{"crew_equipment", "CrewEquipment", "CrewEquipments"},
	} {
		var e *OutEntity
		for _, x := range o.Entities {
			if x.Join && x.Table == c.table {
				e = x
			}
		}
		if e == nil || e.Name != c.name || e.Handles != c.handles {
			t.Errorf("%s: %+v, want %s and %s", c.table, e, c.name, c.handles)
		}
	}
}

// A unique edge to its own schema is a bidirectional O2O.
type Person struct{ schema.Schema }

func (Person) Edges() []schema.Edge { return []schema.Edge{edge.To("spouse", Person.Type).Unique()} }

// reqSpouse's spouse edge is a Required bidirectional O2O (rule 53).
type reqSpouse struct{ schema.Schema }

func (reqSpouse) Edges() []schema.Edge {
	return []schema.Edge{edge.To("spouse", reqSpouse.Type).Unique().Required()}
}

func TestBuildO2OBidi(t *testing.T) {
	p := mustBuild(t, Person{}).Entity("Person")
	sp := p.Edge("spouse")
	if sp.Rel.Kind != "O2O" || !sp.Bidi || sp.Rel.Table != "persons" || sp.Rel.Columns[0] != "person_spouse" || sp.Field != "person_spouse" {
		t.Fatalf("spouse %+v", sp)
	}
	if f := p.Field("person_spouse"); f == nil || !f.Unique || !f.Nillable || f.Implicit != "spouse" {
		t.Fatalf("person_spouse %+v", f)
	}
}

// edge.Columns on the inverse of an M2M still names the assoc owner's column
// first, as in ent.
type Tag struct{ schema.Schema }

func (Tag) Edges() []schema.Edge { return []schema.Edge{edge.To("articles", Article.Type)} }

type Article struct{ schema.Schema }

func (Article) Edges() []schema.Edge {
	return []schema.Edge{edge.From("tags", Tag.Type).Ref("articles").StorageKey(edge.Columns("tag_ref", "article_ref"))}
}

func TestBuildM2MColumnsOnInverse(t *testing.T) {
	g := mustBuild(t, Tag{}, Article{})
	a, i := g.Entity("Tag").Edge("articles"), g.Entity("Article").Edge("tags")
	if a.Rel.Table != "tag_articles" || a.Rel.Columns[0] != "tag_ref" || a.Rel.Columns[1] != "article_ref" ||
		i.Rel.Columns[0] != "article_ref" || i.Rel.Columns[1] != "tag_ref" {
		t.Fatalf("articles %+v tags %+v", a, i)
	}
}

// edge.Column equal to the bound field's column is accepted.
type columnFieldMatch struct{ schema.Schema }

func (columnFieldMatch) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional().Nillable()}
}
func (columnFieldMatch) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", columnFieldMatch.Type).Unique().Field("parent_id").StorageKey(edge.Column("parent_id"))}
}

func TestBuildColumnMatchesField(t *testing.T) {
	if e := mustBuild(t, columnFieldMatch{}).Entity("columnFieldMatch").Edge("parent"); e.Field != "parent_id" || e.Rel.Columns[0] != "parent_id" {
		t.Fatalf("parent %+v", e)
	}
}

// An edge's Immutable and Symbols are in the Graph.
type symA struct{ schema.Schema }

func (symA) Edges() []schema.Edge {
	return []schema.Edge{edge.To("bs", symB.Type).Immutable().StorageKey(edge.Symbols("a_fk", "b_fk"))}
}

type symB struct{ schema.Schema }

func (symB) Edges() []schema.Edge { return []schema.Edge{edge.From("as", symA.Type).Ref("bs")} }

func TestBuildEdgeImmutableSymbols(t *testing.T) {
	g := mustBuild(t, symA{}, symB{})
	bs, as := g.Entity("symA").Edge("bs"), g.Entity("symB").Edge("as")
	if !bs.Immutable || len(bs.Symbols) != 2 || bs.Symbols[0] != "a_fk" || bs.Symbols[1] != "b_fk" || as.Immutable || as.Symbols != nil {
		t.Fatalf("bs %+v as %+v", bs, as)
	}
	j, _ := json.Marshal(bs)
	if !strings.Contains(string(j), `"immutable":true`) || !strings.Contains(string(j), `"symbols":["a_fk","b_fk"]`) {
		t.Fatalf("json %s", j)
	}
}

// --- one schema type (or pair) per rule

type UUIDValue [16]byte

func (UUIDValue) Value() (driver.Value, error) { return nil, nil }

type PointValue struct{}

func (PointValue) Value() (driver.Value, error) { return nil, nil }

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

// badRefNear's Ref is one letter short of its assoc edge.
type badRefNear struct{ schema.Schema }

func (badRefNear) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", badRefNear.Type), edge.From("x", badRefNear.Type).Ref("post")}
}

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
	return []schema.Field{field.Int("parent_id").Optional().Nillable()}
}
func (fieldWrongType) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fieldWrongType.Type).Unique().Field("parent_id")}
}

type fieldOptional struct{ schema.Schema }

func (fieldOptional) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional()}
}
func (fieldOptional) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", fieldOptional.Type).From("parent").Unique().Required().Field("parent_id")}
}

type twoTenants struct{ schema.Schema }

func (twoTenants) Fields() []schema.Field {
	return []schema.Field{field.Int64("a").Tenant(), field.Int64("b").Tenant()}
}

type optionalTenant struct{ schema.Schema }

func (optionalTenant) Fields() []schema.Field {
	return []schema.Field{field.Int64("org").Tenant().Optional()}
}

type defaultBool struct{ schema.Schema }

func (defaultBool) Fields() []schema.Field { return []schema.Field{field.Bool("active").Default(true)} }

type defaultNumber struct{ schema.Schema }

func (defaultNumber) Fields() []schema.Field {
	return []schema.Field{field.Float("score").Optional().Default(1.5)}
}

type defaultFuncNumber struct{ schema.Schema }

func (defaultFuncNumber) Fields() []schema.Field {
	return []schema.Field{field.Int("count").DefaultFunc(func() int { return 1 })}
}

type optionalEnum struct{ schema.Schema }

func (optionalEnum) Fields() []schema.Field {
	return []schema.Field{field.Enum("mood").Values("calm", "busy").Optional()}
}

type optionalBytes struct{ schema.Schema }

func (optionalBytes) Fields() []schema.Field { return []schema.Field{field.Bytes("blob").Optional()} }

// optionalBytesOK has the Optional bytes fields that pass: with a Default, or Nillable.
type optionalBytesOK struct{ schema.Schema }

func (optionalBytesOK) Fields() []schema.Field {
	return []schema.Field{field.Bytes("a").Optional().Default([]byte("x")), field.Bytes("b").Optional().Nillable()}
}

type optionalTime struct{ schema.Schema }

func (optionalTime) Fields() []schema.Field { return []schema.Field{field.Time("seen").Optional()} }

// optionalTimeOK has the Optional time fields that pass: with a Default, or
// Nillable.
type optionalTimeOK struct{ schema.Schema }

func (optionalTimeOK) Fields() []schema.Field {
	return []schema.Field{field.Time("a").Optional().Default(time.Now), field.Time("b").Optional().Nillable()}
}

// optionalJSONStruct has an Optional JSON field of a struct: its column holds
// NULL, which its Go field cannot (rule 48).
type optionalJSONStruct struct{ schema.Schema }

func (optionalJSONStruct) Fields() []schema.Field {
	return []schema.Field{field.JSON("shape", JSONShape{}).Optional()}
}

// optionalJSONScalar has an Optional JSON field of a scalar type (rule 48).
type optionalJSONScalar struct{ schema.Schema }

func (optionalJSONScalar) Fields() []schema.Field {
	return []schema.Field{field.JSON("n", 0).Optional()}
}

// JSONShape is a struct a json field holds.
type JSONShape struct{ W, H int }

// optionalJSONOK has the Optional JSON fields that pass: of a slice, a map
// or a pointer, which nil holds, and of a struct that is Nillable.
type optionalJSONOK struct{ schema.Schema }

func (optionalJSONOK) Fields() []schema.Field {
	return []schema.Field{
		field.Strings("tags").Optional(),
		field.JSON("attrs", map[string]any{}).Optional(),
		field.JSON("ptr", &JSONShape{}).Optional(),
		field.JSON("shape", JSONShape{}).Optional().Nillable(),
		field.JSON("req", JSONShape{}),
	}
}

func TestBuildOptionalJSONNeedsNilable(t *testing.T) {
	mustBuild(t, optionalJSONOK{})
	for _, s := range []schema.Interface{optionalJSONStruct{}, optionalJSONScalar{}} {
		_, err := build(t, s)
		if err == nil || !strings.Contains(err.Error(), `: an Optional JSON field of a non-nil-able type needs Nillable`) {
			t.Errorf("%T: %v", s, err)
		}
	}
	_, err := build(t, optionalJSONStruct{})
	if err == nil || !strings.Contains(err.Error(), `schema optionalJSONStruct: field "shape": an Optional JSON field of a non-nil-able type needs Nillable`) {
		t.Errorf("the message: %v", err)
	}
}

// JSONCallback is a named func type: velox-gen can write it, and rule 49
// refuses it. An unnamed func fails in the field package already.
type JSONCallback func()

// JSONFunc is a func type with its own encoding; rule 49 still refuses it.
type JSONFunc func()

func (JSONFunc) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// JSONComplex is a complex type with its own encoding, which rule 49 accepts.
type JSONComplex complex128

func (JSONComplex) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

type jsonFuncField struct{ schema.Schema }

func (jsonFuncField) Fields() []schema.Field {
	return []schema.Field{field.JSON("cb", JSONCallback(nil))}
}

// jsonOptionalFuncField is an Optional JSON func: rule 49 refuses it, and
// rule 48's advice to add Nillable would lead into rule 49.
type jsonOptionalFuncField struct{ schema.Schema }

func (jsonOptionalFuncField) Fields() []schema.Field {
	return []schema.Field{field.JSON("cb", JSONCallback(nil)).Optional()}
}

type jsonChanField struct{ schema.Schema }

func (jsonChanField) Fields() []schema.Field {
	return []schema.Field{field.JSON("ch", make(chan int))}
}

type jsonUnsafeField struct{ schema.Schema }

func (jsonUnsafeField) Fields() []schema.Field {
	return []schema.Field{field.JSON("p", unsafe.Pointer(nil))}
}

type jsonComplexField struct{ schema.Schema }

func (jsonComplexField) Fields() []schema.Field {
	return []schema.Field{field.JSON("z", complex(1, 2))}
}

type jsonComplex64Field struct{ schema.Schema }

func (jsonComplex64Field) Fields() []schema.Field {
	return []schema.Field{field.JSON("z", complex64(1))}
}

type jsonFuncMarshalerField struct{ schema.Schema }

func (jsonFuncMarshalerField) Fields() []schema.Field {
	return []schema.Field{field.JSON("cb", JSONFunc(nil))}
}

type jsonComplexMarshalerField struct{ schema.Schema }

func (jsonComplexMarshalerField) Fields() []schema.Field {
	return []schema.Field{field.JSON("z", JSONComplex(0))}
}

// A complex type with its own encoding is a value type, like a struct.
func TestBuildJSONComplexWithMarshaler(t *testing.T) {
	g := mustBuild(t, jsonComplexMarshalerField{})
	if f := g.Entity("jsonComplexMarshalerField").Field("z"); f.Optional || f.Nillable {
		t.Errorf("the field is %+v, want required", f)
	}
}

// optionalFK binds an Optional edge field that is not Nillable.
type optionalFK struct{ schema.Schema }

func (optionalFK) Fields() []schema.Field { return []schema.Field{field.Int64("parent_id").Optional()} }
func (optionalFK) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", optionalFK.Type).Unique().Field("parent_id")}
}

type defaultTenant struct{ schema.Schema }

func (defaultTenant) Fields() []schema.Field {
	return []schema.Field{field.Int64("org").Tenant().Default(1)}
}

type sdTenant struct{ schema.Schema }

func (sdTenant) Fields() []schema.Field {
	return []schema.Field{field.Int64("org").Tenant().SchemaDefault(map[string]string{field.Postgres: "1"})}
}

type sdIntID struct{ schema.Schema }

func (sdIntID) Fields() []schema.Field {
	return []schema.Field{field.Int64("id").SchemaDefault(map[string]string{field.Postgres: "1"})}
}

type sdStringID struct{ schema.Schema }

func (sdStringID) Fields() []schema.Field {
	return []schema.Field{field.String("id").SchemaDefault(map[string]string{field.Postgres: "gen_random_uuid()::text"})}
}

type bytesTenant struct{ schema.Schema }

func (bytesTenant) Fields() []schema.Field { return []schema.Field{field.Bytes("org").Tenant()} }

type emptyEnum struct{ schema.Schema }

func (emptyEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s")} }

type dupEnum struct{ schema.Schema }

func (dupEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s").Values("a", "b", "a")} }

type StatusValues string

func (StatusValues) Values() []string { return []string{"a", "b"} }

type enumGoTypeDup struct{ schema.Schema }

func (enumGoTypeDup) Fields() []schema.Field {
	return []schema.Field{field.Enum("s").Values("a").GoType(StatusValues(""))}
}

type badEnum struct{ schema.Schema }

func (badEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s").Values("in-progress")} }

type optionalID struct{ schema.Schema }

func (optionalID) Fields() []schema.Field { return []schema.Field{field.Int64("id").Optional()} }

type floatID struct{ schema.Schema }

func (floatID) Fields() []schema.Field { return []schema.Field{field.Float("id")} }

type unknownIndexField struct{ schema.Schema }

func (unknownIndexField) Indexes() []schema.Index { return []schema.Index{index.Fields("nope")} }

// nearIndexField's index names its field with two letters swapped.
type nearIndexField struct{ schema.Schema }

func (nearIndexField) Fields() []schema.Field  { return []schema.Field{field.String("name")} }
func (nearIndexField) Indexes() []schema.Index { return []schema.Index{index.Fields("nmae")} }

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

func (otherNoType) Fields() []schema.Field { return []schema.Field{field.Other("o", PointValue{})} }

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

type tableOnO2M struct{ schema.Schema }

func (tableOnO2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Table("x"))}
}

type badFieldName struct{ schema.Schema }

func (badFieldName) Fields() []schema.Field { return []schema.Field{field.String("in-stock")} }

type emptyFieldName struct{ schema.Schema }

func (emptyFieldName) Fields() []schema.Field { return []schema.Field{field.String("")} }

type badEdgeName struct{ schema.Schema }

func (badEdgeName) Edges() []schema.Edge { return []schema.Edge{edge.To("my-peers", badEdgeName.Type)} }

type fieldGoNames struct{ schema.Schema }

func (fieldGoNames) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id"), field.Int64("orgID")}
}

type edgeGoNames struct{ schema.Schema }

func (edgeGoNames) Edges() []schema.Edge {
	return []schema.Edge{edge.To("user_posts", edgeGoNames.Type), edge.To("userPosts", edgeGoNames.Type)}
}

type enumGoNames struct{ schema.Schema }

func (enumGoNames) Fields() []schema.Field {
	return []schema.Field{field.Enum("s").NamedValues("A", "a", "A", "b")}
}

type clashOwner struct{ schema.Schema }

func (clashOwner) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", clashPet.Type)} }

type clashPet struct{ schema.Schema }

func (clashPet) Fields() []schema.Field { return []schema.Field{field.Int64("clashOwnerPets")} }

type columnFieldMismatch struct{ schema.Schema }

func (columnFieldMismatch) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional().Nillable()}
}
func (columnFieldMismatch) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", columnFieldMismatch.Type).Unique().Field("parent_id").StorageKey(edge.Column("pid"))}
}

type keyA struct{ schema.Schema }

func (keyA) Edges() []schema.Edge {
	return []schema.Edge{edge.To("b", keyB.Type).StorageKey(edge.Column("x"))}
}

type keyB struct{ schema.Schema }

func (keyB) Edges() []schema.Edge {
	return []schema.Edge{edge.From("a", keyA.Type).Ref("b").Unique().StorageKey(edge.Column("y"))}
}

type oneColumnM2M struct{ schema.Schema }

func (oneColumnM2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", oneColumnM2M.Type).StorageKey(edge.Column("x"))}
}

type twoColumnsO2M struct{ schema.Schema }

func (twoColumnsO2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Columns("a", "b"))}
}

type invAssoc struct{ schema.Schema }

func (invAssoc) Edges() []schema.Edge { return []schema.Edge{edge.To("items", invItem.Type)} }

type invItem struct{ schema.Schema }

func (invItem) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("a", invAssoc.Type).Ref("items").Unique(),
		edge.From("b", invAssoc.Type).Ref("items").Unique(),
	}
}

type fkUnbound struct{ schema.Schema }

func (fkUnbound) Fields() []schema.Field {
	return []schema.Field{field.Int64("fk_unbound_parent").Optional()}
}
func (fkUnbound) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fkUnbound.Type).Unique()}
}

type fieldStringFK struct{ schema.Schema }

func (fieldStringFK) Fields() []schema.Field {
	return []schema.Field{field.String("parent_id").Optional().Nillable()}
}
func (fieldStringFK) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fieldStringFK.Type).Unique().Field("parent_id")}
}

type Cents int64

type fkNamedType struct{ schema.Schema }

func (fkNamedType) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").GoType(Cents(0)).Optional().Nillable()}
}
func (fkNamedType) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fkNamedType.Type).Unique().Field("parent_id")}
}

// secretCents is unexported: the generated package cannot name it, so
// Lower refuses a GoType of it, which Build and Tables accept.
type secretCents int64

type unexportedGoType struct{ schema.Schema }

func (unexportedGoType) Fields() []schema.Field {
	return []schema.Field{field.Int64("n").GoType(secretCents(0))}
}

// unexportedElem's unexported type is the element of the field's map.
type unexportedElem struct{ schema.Schema }

func (unexportedElem) Fields() []schema.Field {
	return []schema.Field{field.JSON("m", map[string][]*secretCents{})}
}

// MyInt is an int of its own, so rule 12's hint to declare field.Int64
// would not help a foreign key of it.
type MyInt int

type fkCustomInt struct{ schema.Schema }

func (fkCustomInt) Fields() []schema.Field {
	return []schema.Field{field.Int("parent_id").GoType(MyInt(0)).Optional().Nillable()}
}
func (fkCustomInt) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fkCustomInt.Type).Unique().Field("parent_id")}
}

// fkOtherPkg's ID and foreign key are both codegen.Cents; a test moves the ID's
// type to another package named gen.
type fkOtherPkg struct{ schema.Schema }

func (fkOtherPkg) Fields() []schema.Field {
	return []schema.Field{field.Int64("id").GoType(Cents(0)), field.Int64("parent_id").GoType(Cents(0)).Optional().Nillable()}
}
func (fkOtherPkg) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", fkOtherPkg.Type).Unique().Field("parent_id")}
}

type immutableEdge struct{ schema.Schema }

func (immutableEdge) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional().Nillable()}
}
func (immutableEdge) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", immutableEdge.Type).Unique().Field("parent_id").Immutable()}
}

type immutableField struct{ schema.Schema }

func (immutableField) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional().Nillable().Immutable()}
}
func (immutableField) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", immutableField.Type).Unique().Field("parent_id")}
}

// loop's edge "loops" names its second join column, Singular("loops")_id,
// after the schema itself: loop_id twice.
type loop struct{ schema.Schema }

func (loop) Edges() []schema.Edge { return []schema.Edge{edge.To("loops", loop.Type)} }

// Man's edge men has the singular man, Man's own label (rule 35).
type Man struct{ schema.Schema }

func (Man) Edges() []schema.Edge { return []schema.Edge{edge.To("men", Man.Type)} }

type cycle struct{ schema.Schema }

func (cycle) Edges() []schema.Edge {
	return []schema.Edge{edge.To("next", cycle.Type).From("cycles")}
}

// long64 is a name one byte over the cap on database names.
var long64 = strings.Repeat("x", 64)

type longTable struct{ schema.Schema }

func (longTable) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table(long64)} }

type longColumn struct{ schema.Schema }

func (longColumn) Fields() []schema.Field {
	return []schema.Field{field.String("x").StorageKey(long64)}
}

type longDerivedColumn struct{ schema.Schema }

func (longDerivedColumn) Fields() []schema.Field { return []schema.Field{field.String(long64)} }

// longDerivedTableAndSoOnAndSoOnUntilTheNameIsLongerThanTheCapAllowsThing's
// table, its snake-case plural, is 90 bytes.
type longDerivedTableAndSoOnAndSoOnUntilTheNameIsLongerThanTheCapAllowsThing struct{ schema.Schema }

type longIndexKey struct{ schema.Schema }

func (longIndexKey) Fields() []schema.Field { return []schema.Field{field.String("a")} }
func (longIndexKey) Indexes() []schema.Index {
	return []schema.Index{index.Fields("a").StorageKey(long64)}
}

type longSymbol struct{ schema.Schema }

func (longSymbol) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Symbol(long64))}
}

// longImplicit's edge makes the implicit column long_implicit_<edge>, 64 bytes.
type longImplicit struct{ schema.Schema }

func (longImplicit) Edges() []schema.Edge {
	return []schema.Edge{edge.To(strings.Repeat("p", 50), Pet.Type)}
}

type longJoinTable struct{ schema.Schema }

func (longJoinTable) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", longJoinTable.Type).StorageKey(edge.Table(long64))}
}

type longEnumType struct{ schema.Schema }

func (longEnumType) Fields() []schema.Field {
	return []schema.Field{field.Enum("s").Values("a").SchemaType(map[string]string{field.Postgres: long64})}
}

type twoSymbolsO2M struct{ schema.Schema }

func (twoSymbolsO2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Symbols("a", "b"))}
}

type oneSymbolM2M struct{ schema.Schema }

func (oneSymbolM2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", oneSymbolM2M.Type).StorageKey(edge.Symbol("a"))}
}

type symOwner struct{ schema.Schema }

func (symOwner) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", symPet.Type).StorageKey(edge.Symbol("b"))}
}

type symPet struct{ schema.Schema }

func (symPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", symOwner.Type).Ref("pets").Unique().StorageKey(edge.Symbol("a"))}
}

// enumOrder and enumTicket name one PostgreSQL enum type, with other values.
type enumOrder struct{ schema.Schema }

var enumOrderLine int

func (enumOrder) Fields() []schema.Field {
	f, l := field.Enum("status").Values("a", "b").SchemaType(map[string]string{field.Postgres: "status"}), here()
	enumOrderLine = l
	return []schema.Field{f}
}

type enumTicket struct{ schema.Schema }

func (enumTicket) Fields() []schema.Field {
	return []schema.Field{field.Enum("state").Values("b", "a").SchemaType(map[string]string{field.Postgres: "status"})}
}

// nsOrder's enum type orders_status is nsOrderStatus's table.
type nsOrder struct{ schema.Schema }

func (nsOrder) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("orders")} }
func (nsOrder) Fields() []schema.Field           { return []schema.Field{field.Enum("status").Values("a")} }

type nsOrderStatus struct{ schema.Schema }

func (nsOrderStatus) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("orders_status")}
}

// lpPrefix is 58 bytes: lpA's and lpB's tables, of 59 and 60 bytes, share
// it, and PostgreSQL's names of their primary keys and sequences cut each
// table back to it, or shorter.
var lpPrefix = "long_table_" + strings.Repeat("t", 47)

type lpA struct{ schema.Schema }

func (lpA) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table(lpPrefix + "a")}
}

type lpB struct{ schema.Schema }

func (lpB) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table(lpPrefix + "bb")}
}

// dupNamed's two indexes have one derived name: a full index takes no hash.
type dupNamed struct{ schema.Schema }

func (dupNamed) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (dupNamed) Indexes() []schema.Index {
	return []schema.Index{index.Fields("name"), index.Fields("name").Unique()}
}

func TestBuildRules(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   []schema.Interface
		want string
	}{
		{"1 duplicate field", []schema.Interface{dupField{}}, `schema dupField: field "a": declared twice`},
		{"2 duplicate column", []schema.Interface{dupColumn{}}, `schema dupColumn: field "b": column "a" is also field "a"'s`},
		{"2 unbound foreign key", []schema.Interface{fkUnbound{}}, `schema fkUnbound: field "fk_unbound_parent": column "fk_unbound_parent" is also field "fk_unbound_parent"'s; bind it with edge.Field(...)`},
		{"3 duplicate edge", []schema.Interface{dupEdge{}}, `schema dupEdge: edge "e": declared twice`},
		{"4 edge go name", []schema.Interface{edgeGoName{}}, `schema edgeGoName: edge "owner": its Go name Owner is also field "owner"'s`},
		{"5 unknown target", []schema.Interface{unknownTarget{}}, `schema unknownTarget: edge "x": type notSchema is not a schema of this package`},
		{"5 other package", []schema.Interface{otherPkgTarget{}}, `schema otherPkgTarget: edge "x": type other.Thing is not a schema of this package`},
		{"5 not a schema", []schema.Interface{notSchemaTarget{}}, `schema notSchemaTarget: edge "x": type time.Time is not a schema of this package`},
		{"6 no ref", []schema.Interface{noRef{}}, `schema noRef: edge "x": an inverse edge needs Ref(name)`},
		{"7 bad ref", []schema.Interface{badRef{}}, `schema badRef: edge "x": badRef has no edge "nope" to refer to`},
		{"7 bad ref near", []schema.Interface{badRefNear{}}, `schema badRefNear: edge "x": badRefNear has no edge "post" to refer to; did you mean "posts"?`},
		{"8 wrong ref", []schema.Interface{wrongRef{}, refHolder{}, Pet{}}, `schema wrongRef: edge "x": refHolder.pets points to Pet, not to this schema`},
		{"9 both required", []schema.Interface{reqA{}, reqB{}}, `schema reqB: edge "a": cannot be required in both directions with reqA.b`},
		{"10 field missing", []schema.Interface{fieldMissing{}}, `schema fieldMissing: edge "parent": field "parent_id" is not declared`},
		{"11 field wrong side", []schema.Interface{fieldWrongSide{}, Pet{}}, `schema fieldWrongSide: edge "pets": binds field "x", but the foreign key is in table pets`},
		{"11 field on M2M", []schema.Interface{fieldMany{}}, `schema fieldMany: edge "peers": binds field "x", but the foreign key is in table field_many_peers`},
		{"12 field wrong type", []schema.Interface{fieldStringFK{}}, `schema fieldStringFK: edge "parent": field "parent_id" is string; fieldStringFK's ID is int64`},
		{"12 ported int field", []schema.Interface{fieldWrongType{}}, `schema fieldWrongType: edge "parent": field "parent_id" is int; fieldWrongType's ID is int64; declare it field.Int64, or declare the ID with field.Int("id")`},
		{"12 field of a named type", []schema.Interface{fkNamedType{}}, `schema fkNamedType: edge "parent": field "parent_id" is codegen.Cents; fkNamedType's ID is int64`},
		{"12 field of a custom int", []schema.Interface{fkCustomInt{}}, `schema fkCustomInt: edge "parent": field "parent_id" is codegen.MyInt; fkCustomInt's ID is int64`},
		{"13 field optional", []schema.Interface{fieldOptional{}}, `schema fieldOptional: edge "parent": field "parent_id" must be Optional exactly when the edge is not Required`},
		{"14 two tenants", []schema.Interface{twoTenants{}}, `schema twoTenants: field "b": a second tenant field; "a" is the tenant`},
		{"15 optional tenant", []schema.Interface{optionalTenant{}}, `schema optionalTenant: field "org": the tenant field must be required and not Nillable`},
		{"16 default tenant", []schema.Interface{defaultTenant{}}, `schema defaultTenant: field "org": the tenant field cannot have a default: the context's tenant fills it`},
		{"16 schema default on the tenant", []schema.Interface{sdTenant{}}, `schema sdTenant: field "org": the tenant field cannot have a default: the context's tenant fills it`},
		{"17 bytes tenant", []schema.Interface{bytesTenant{}}, `schema bytesTenant: field "org": the tenant field must be of a comparable type`},
		{"18 empty enum", []schema.Interface{emptyEnum{}}, `schema emptyEnum: field "s": an enum needs Values`},
		{"19 duplicate enum value", []schema.Interface{dupEnum{}}, `schema dupEnum: field "s": value "a" is given twice`},
		{"19 value from Values and GoType", []schema.Interface{enumGoTypeDup{}}, `schema enumGoTypeDup: field "s": value "a" is given twice`},
		{"20 enum go name", []schema.Interface{badEnum{}}, `schema badEnum: field "s": value "in-progress" has no Go name; use NamedValues`},
		{"21 optional id", []schema.Interface{optionalID{}}, `schema optionalID: field "id": the ID must be required, not Nillable, and have no UpdateDefault`},
		{"21 schema default on an integer ID", []schema.Interface{sdIntID{}}, `schema sdIntID: field "id": an integer ID is assigned by the database and cannot have a SchemaDefault`},
		{"22 float id", []schema.Interface{floatID{}}, `schema floatID: field "id": the ID cannot be of type float64`},
		{"23 unknown index field", []schema.Interface{unknownIndexField{}}, `schema unknownIndexField: index 0: names unknown field "nope"`},
		{"23 near index field", []schema.Interface{nearIndexField{}}, `schema nearIndexField: index 0: names unknown field "nmae"; did you mean "name"?`},
		{"23 unknown index edge", []schema.Interface{unknownIndexEdge{}}, `schema unknownIndexEdge: index 0: names unknown edge "nope"`},
		{"24 index foreign edge", []schema.Interface{indexForeignEdge{}, Pet{}}, `schema indexForeignEdge: index 0: edge "pets" has no column in table index_foreign_edges`},
		{"25 empty index", []schema.Interface{emptyIndex{}}, `schema emptyIndex: index 0: names no fields or edges`},
		{"26 other without schema type", []schema.Interface{otherNoType{}}, `schema otherNoType: field "o": an Other field needs SchemaType`},
		{"27 join table clash", []schema.Interface{joinClash{}, clashTable{}}, `schema joinClash: edge "links": join table join_clash_links is also the table of schema clashTable`},
		{"5 inline inverse", []schema.Interface{inlineNotSchema{}}, `schema inlineNotSchema: edge "x": type notSchema is not a schema of this package`},
		{"28 inline inverse to another schema", []schema.Interface{inlineOther{}, Pet{}}, `schema inlineOther: edge "owner": From(...) inline is allowed only on an edge to its own schema`},
		{"29 table on O2M", []schema.Interface{tableOnO2M{}, Pet{}}, `schema tableOnO2M: edge "pets": edge.Table is allowed only on many-to-many edges`},
		{"30 field go name", []schema.Interface{badFieldName{}}, `schema badFieldName: field "in-stock": its Go name In-stock is not a Go identifier`},
		{"30 empty field name", []schema.Interface{emptyFieldName{}}, `schema emptyFieldName: field "": its Go name "" is not a Go identifier`},
		{"30 edge go name", []schema.Interface{badEdgeName{}}, `schema badEdgeName: edge "my-peers": its Go name My-peers is not a Go identifier`},
		{"30 fields share a go name", []schema.Interface{fieldGoNames{}}, `schema fieldGoNames: field "orgID": its Go name OrgID is also field "org_id"'s`},
		{"30 edges share a go name", []schema.Interface{edgeGoNames{}}, `schema edgeGoNames: edge "userPosts": its Go name UserPosts is also edge "user_posts"'s`},
		{"30 enum values share a go name", []schema.Interface{enumGoNames{}}, `schema enumGoNames: field "s": values "a" and "b" have one Go name A`},
		{"30 implicit field shares a go name", []schema.Interface{clashOwner{}, clashPet{}}, `schema clashPet: field "clash_owner_pets": its Go name ClashOwnerPets is also field "clashOwnerPets"'s`},
		{"31 column differs from field", []schema.Interface{columnFieldMismatch{}}, `schema columnFieldMismatch: edge "parent": column "pid" differs from field "parent_id"'s column "parent_id"`},
		{"32 keys on both edges", []schema.Interface{keyA{}, keyB{}}, `schema keyB: edge "a": storage keys are declared on both keyA.b and this edge`},
		{"32 one column on M2M", []schema.Interface{oneColumnM2M{}}, `schema oneColumnM2M: edge "peers": a many-to-many edge needs two columns`},
		{"32 two columns on O2M", []schema.Interface{twoColumnsO2M{}, Pet{}}, `schema twoColumnsO2M: edge "pets": edge.Columns is allowed only on many-to-many edges; use edge.Column`},
		{"33 second inverse", []schema.Interface{invAssoc{}, invItem{}}, `schema invItem: edge "b": invAssoc.items already has the inverse edge "a"`},
		{"34 immutable edge", []schema.Interface{immutableEdge{}}, `schema immutableEdge: edge "parent": is Immutable but field "parent_id" is not`},
		{"34 immutable field", []schema.Interface{immutableField{}}, `schema immutableField: edge "parent": field "parent_id" is Immutable but the edge is not`},
		{"35 join columns collide", []schema.Interface{loop{}}, `schema loop: edge "loops": join table loop_loops has two columns named loop_id; name them with edge.Columns`},
		{"35 singular is the owner's label", []schema.Interface{Man{}}, `schema Man: edge "men": join table man_men has two columns named man_id; name them with edge.Columns`},
		{"35 join columns collide on an inverse", []schema.Interface{cycle{}}, `schema cycle: edge "cycles": join table cycle_next has two columns named cycle_id; name them with edge.Columns`},
		{"36 default on a bool", []schema.Interface{defaultBool{}}, `schema defaultBool: field "active": a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default`},
		{"36 default on an optional number", []schema.Interface{defaultNumber{}}, `schema defaultNumber: field "score": a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default`},
		{"36 default func on a number", []schema.Interface{defaultFuncNumber{}}, `schema defaultFuncNumber: field "count": a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default`},
		{"37 optional enum", []schema.Interface{optionalEnum{}}, `schema optionalEnum: field "mood": an Optional enum needs a Default or Nillable`},
		{"38 optional edge field", []schema.Interface{optionalFK{}}, `schema optionalFK: edge "parent": field "parent_id" must be Nillable when the edge is not Required: an unset foreign key is NULL`},
		{"39 optional bytes", []schema.Interface{optionalBytes{}}, `schema optionalBytes: field "blob": an Optional bytes field needs a Default or Nillable`},
		{"40 optional time", []schema.Interface{optionalTime{}}, `schema optionalTime: field "seen": an Optional time field needs a Default or Nillable`},
		{"44 shortened primary keys", []schema.Interface{lpA{}, lpB{}}, `schema lpB: name "` + lpPrefix + `_pkey" is also schema lpA's primary key`},
		{"44 shortened sequences", []schema.Interface{lpA{}, lpB{}}, `schema lpB: field "id": name "` + lpPrefix[:56] + `_id_seq" is also schema lpA's identity sequence`},
		{"44 indexes of one name", []schema.Interface{dupNamed{}}, `schema dupNamed: index 1: name "dupnamed_name" is also this schema's index 0`},
		{"44 table and enum type", []schema.Interface{nsOrder{}, nsOrderStatus{}}, `schema nsOrder: field "status": name "orders_status" is also schema nsOrderStatus's table`},
		{"49 func", []schema.Interface{jsonFuncField{}}, `schema jsonFuncField: field "cb": its Go type codegen.JSONCallback is a func, which a JSON column cannot hold`},
		{"49 chan", []schema.Interface{jsonChanField{}}, `schema jsonChanField: field "ch": its Go type chan int is a chan, which a JSON column cannot hold`},
		{"49 unsafe pointer", []schema.Interface{jsonUnsafeField{}}, `schema jsonUnsafeField: field "p": its Go type unsafe.Pointer is an unsafe.Pointer, which a JSON column cannot hold`},
		{"49 complex", []schema.Interface{jsonComplexField{}}, `schema jsonComplexField: field "z": its Go type complex128 is a complex128, which a JSON column cannot hold; give the type a MarshalJSON or MarshalText method`},
		{"49 complex64", []schema.Interface{jsonComplex64Field{}}, `schema jsonComplex64Field: field "z": its Go type complex64 is a complex64, which a JSON column cannot hold; give the type a MarshalJSON or MarshalText method`},
		{"49 func with MarshalJSON", []schema.Interface{jsonFuncMarshalerField{}}, `schema jsonFuncMarshalerField: field "cb": its Go type codegen.JSONFunc is a func, which a JSON column cannot hold`},
		{"50 long table", []schema.Interface{longTable{}}, `schema longTable: table name "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"50 long derived table", []schema.Interface{longDerivedTableAndSoOnAndSoOnUntilTheNameIsLongerThanTheCapAllowsThing{}}, `table name "long_derived_table_and_so_on_and_so_on_until_the_name_is_longer_than_the_cap_allows_things" is 90 bytes; a database name has at most 63; name it with schema.Table`},
		{"50 long column", []schema.Interface{longColumn{}}, `schema longColumn: field "x": column name "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"50 long derived column", []schema.Interface{longDerivedColumn{}}, `schema longDerivedColumn: field "` + long64 + `": column name "` + long64 + `" is 64 bytes; a database name has at most 63; name it with StorageKey`},
		{"50 long implicit column", []schema.Interface{longImplicit{}, Pet{}}, `schema longImplicit: edge "` + strings.Repeat("p", 50) + `": column name "long_implicit_` + strings.Repeat("p", 50) + `" is 64 bytes; a database name has at most 63; name it with edge.Column`},
		{"50 long index key", []schema.Interface{longIndexKey{}}, `schema longIndexKey: index 0: index name "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"50 long symbol", []schema.Interface{longSymbol{}, Pet{}}, `schema longSymbol: edge "pets": symbol "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"50 long join table", []schema.Interface{longJoinTable{}}, `schema longJoinTable: edge "peers": join table name "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"50 long enum type", []schema.Interface{longEnumType{}}, `schema longEnumType: field "s": enum type name "` + long64 + `" is 64 bytes; a database name has at most 63`},
		{"51 two symbols on O2M", []schema.Interface{twoSymbolsO2M{}, Pet{}}, `schema twoSymbolsO2M: edge "pets": a relation that is not many-to-many has one foreign key; use edge.Symbol`},
		{"51 one symbol on M2M", []schema.Interface{oneSymbolM2M{}}, `schema oneSymbolM2M: edge "peers": a many-to-many relation has two foreign keys; name them with edge.Symbols`},
		{"51 symbols disagree", []schema.Interface{symOwner{}, symPet{}}, `schema symPet: edge "owner": Symbols ["a"] differ from edge "pets"'s ["b"] on symOwner`},
		{"52 shared values differ", []schema.Interface{enumOrder{}, enumTicket{}}, `schema enumTicket: field "state": enum type status is also field "status" of schema enumOrder (`},
		{"53 required bidirectional one-to-one", []schema.Interface{reqSpouse{}}, `schema reqSpouse: edge "spouse": a bidirectional one-to-one edge cannot be Required: its key is cleared whenever its partner re-points; make it optional, or give it an inverse edge`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := build(t, c.ss...)
			var ge *Error
			if err == nil || !strings.Contains(err.Error(), c.want) || !errors.As(err, &ge) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			// A suggestion is only for a name close to one there is.
			if !strings.Contains(c.want, "did you mean") && strings.Contains(err.Error(), "did you mean") {
				t.Errorf("got %v, want no suggestion", err)
			}
		})
	}
}

// A GoType that names an unexported type is refused by Lower, with the
// field's position, also as an element; Build, which Tables runs, takes it.
func TestLowerRefusesUnexportedGoTypes(t *testing.T) {
	for s, want := range map[schema.Interface]string{
		unexportedGoType{}: `schema unexportedGoType: field "n": its Go type codegen.secretCents names codegen.secretCents, which is unexported: the generated package cannot refer to it; export it`,
		unexportedElem{}:   `schema unexportedElem: field "m": its Go type map[string][]*codegen.secretCents names codegen.secretCents, which is unexported: the generated package cannot refer to it; export it`,
	} {
		_, err := lower(t, Config{}, s)
		var ge *Error
		if err == nil || plain(err) != want || !errors.As(err, &ge) || !strings.Contains(ge.Pos, "build_test.go:") {
			t.Errorf("%T: got %v, want %q", s, err, want)
		}
	}
}

// Rule 12's hint to declare field.Int64 is for a foreign key of a plain
// integer type: with a GoType of its own, the hint would not help.
func TestBuildFKHintOnlyWithoutGoType(t *testing.T) {
	_, err := build(t, fkCustomInt{})
	if err == nil || strings.Contains(err.Error(), "declare it field.Int64") {
		t.Fatalf("got %v, want no hint", err)
	}
}

// Two Go types that print alike, codegen.Cents, but are of two packages: a
// foreign key of one cannot hold an ID of the other.
func TestBuildFKOfAnotherPackagesType(t *testing.T) {
	b, err := load.Marshal("github.com/syssam/velox/internal/codegen", fkOtherPkg{})
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(&m); err != nil {
		t.Fatalf("one package: %v", err)
	}
	id := m.Schemas[0].Fields[0]
	if id.Name != "id" {
		t.Fatalf("field 0 is %q", id.Name)
	}
	g := *id.GoType
	g.PkgPath, g.Imports = "example.com/other/gen", []string{"example.com/other/gen"}
	id.GoType = &g
	_, err = Build(&m)
	want := `schema fkOtherPkg: edge "parent": field "parent_id" is codegen.Cents of github.com/syssam/velox/internal/codegen; fkOtherPkg's ID is codegen.Cents of example.com/other/gen`
	if err == nil || plain(err) != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// The rule schemas each break one rule and nothing else.
func TestBuildRulesAreSingle(t *testing.T) {
	for _, ss := range [][]schema.Interface{
		{dupField{}}, {dupColumn{}}, {dupEdge{}}, {edgeGoName{}}, {unknownTarget{}}, {otherPkgTarget{}}, {notSchemaTarget{}},
		{noRef{}}, {badRef{}}, {badRefNear{}}, {wrongRef{}, refHolder{}, Pet{}}, {reqA{}, reqB{}}, {fieldMissing{}},
		{fieldWrongSide{}, Pet{}}, {fieldMany{}}, {fieldWrongType{}}, {fieldOptional{}}, {twoTenants{}}, {optionalTenant{}},
		{defaultTenant{}}, {sdTenant{}}, {sdIntID{}}, {bytesTenant{}}, {emptyEnum{}}, {dupEnum{}}, {badEnum{}}, {optionalID{}}, {floatID{}},
		{unknownIndexField{}}, {nearIndexField{}}, {unknownIndexEdge{}}, {indexForeignEdge{}, Pet{}}, {emptyIndex{}}, {otherNoType{}},
		{joinClash{}, clashTable{}}, {inlineOther{}, Pet{}}, {inlineNotSchema{}},
		{tableOnO2M{}, Pet{}}, {badFieldName{}}, {emptyFieldName{}}, {badEdgeName{}}, {fieldGoNames{}}, {edgeGoNames{}},
		{enumGoNames{}}, {clashOwner{}, clashPet{}}, {columnFieldMismatch{}}, {keyA{}, keyB{}}, {oneColumnM2M{}},
		{twoColumnsO2M{}, Pet{}}, {invAssoc{}, invItem{}}, {enumGoTypeDup{}},
		{fkUnbound{}}, {fieldStringFK{}}, {immutableEdge{}}, {immutableField{}}, {loop{}}, {Man{}}, {cycle{}},
		{odM2M{}, odM2M2{}}, {odSetNullRequired{}, odSetNullTarget{}}, {odDiffOwner{}, odDiffPet{}},
		{defaultBool{}}, {defaultNumber{}}, {defaultFuncNumber{}}, {optionalEnum{}}, {optionalFK{}}, {optionalBytes{}}, {optionalTime{}},
		{optionalJSONStruct{}},
		{nsOrder{}, nsOrderStatus{}}, {longTable{}}, {longDerivedTableAndSoOnAndSoOnUntilTheNameIsLongerThanTheCapAllowsThing{}},
		{longColumn{}}, {longDerivedColumn{}}, {longImplicit{}, Pet{}}, {longIndexKey{}}, {longSymbol{}, Pet{}},
		{longJoinTable{}}, {longEnumType{}}, {twoSymbolsO2M{}, Pet{}}, {oneSymbolM2M{}}, {symOwner{}, symPet{}},
		{enumOrder{}, enumTicket{}}, {jsonFuncField{}}, {jsonChanField{}}, {jsonUnsafeField{}}, {jsonComplexField{}},
		{jsonComplex64Field{}}, {jsonFuncMarshalerField{}}, {jsonOptionalFuncField{}},
		{fkNamedType{}}, {fkCustomInt{}},
	} {
		_, err := build(t, ss...)
		if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
			t.Errorf("%T: %d errors, want 1:\n%v", ss[0], n, err)
		}
	}
}

// A string ID may have a SchemaDefault; only an integer ID is the database's.
func TestBuildSchemaDefaultOnStringID(t *testing.T) { mustBuild(t, sdStringID{}) }

func TestBuildOptionalBytesWithDefaultOrNillable(t *testing.T) { mustBuild(t, optionalBytesOK{}) }

func TestBuildOptionalTimeWithDefaultOrNillable(t *testing.T) { mustBuild(t, optionalTimeOK{}) }

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
	got := plain(err)
	want := `schema manyErrors: field "a": declared twice
schema manyErrors: edge "x": an inverse edge needs Ref(name)
schema manyErrors: field "s": an enum needs Values
schema manyErrors: index 0: names no fields or edges`
	if err == nil || got != want {
		t.Fatalf("got\n%v\nwant\n%s", got, want)
	}
}

// posErrors breaks rules on a field, an edge and an index, each declared on a
// line of its own.
type posErrors struct{ schema.Schema }

var posLines [3]int

func (posErrors) Fields() []schema.Field {
	a := field.String("a")
	b, l := field.String("a"), here()
	posLines[0] = l
	return []schema.Field{a, b}
}

func (posErrors) Edges() []schema.Edge {
	e, l := edge.From("x", posErrors.Type), here()
	posLines[1] = l
	return []schema.Edge{e}
}

func (posErrors) Indexes() []schema.Index {
	ix, l := index.Fields(), here()
	posLines[2] = l
	return []schema.Index{ix}
}

func TestRuleErrorPos(t *testing.T) {
	_, err := build(t, posErrors{})
	errs := err.(interface{ Unwrap() []error }).Unwrap()
	if len(errs) != 3 {
		t.Fatalf("got %v", err)
	}
	for i, want := range []string{`field "a": declared twice`, `edge "x": an inverse edge needs Ref(name)`, `index 0: names no fields or edges`} {
		suffix := fmt.Sprintf("/build_test.go:%d", posLines[i])
		e := errs[i].(*Error)
		if !strings.HasSuffix(e.Pos, suffix) || e.Error() != e.Pos+": schema posErrors: "+want {
			t.Errorf("error %d: %q (pos %q), want pos ...%s", i, e.Error(), e.Pos, suffix)
		}
	}
	// An implicit foreign key has no position.
	_, err = build(t, clashOwner{}, clashPet{})
	var ge *Error
	if !errors.As(err, &ge) || ge.Pos != "" || !strings.HasPrefix(ge.Error(), "schema clashPet: ") {
		t.Fatalf("got %v", err)
	}
}

// Positions are kept out of the Graph's JSON: describe -json and the
// generated files do not depend on where the schema was declared.
func TestOutputIgnoresPositionsInGraph(t *testing.T) {
	g := mustBuild(t, User{}, Post{}, Group{})
	var hasPos bool
	for _, e := range g.Entities {
		for _, f := range e.Fields {
			hasPos = hasPos || f.Pos != ""
		}
	}
	if !hasPos || g.Entity("User").Edge("posts").Pos == "" || g.Entity("User").Indexes[0].Pos == "" {
		t.Fatal("Build fills the positions of declared elements")
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "build_test.go") || strings.Contains(string(b), `"pos"`) {
		t.Fatalf("a position is in the graph's JSON:\n%s", b)
	}
	files1, err := Generate(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Entities {
		for _, f := range e.Fields {
			f.Pos = "/elsewhere/user.go:1"
		}
		for _, ed := range e.Edges {
			ed.Pos = "/elsewhere/user.go:2"
		}
		for _, ix := range e.Indexes {
			ix.Pos = "/elsewhere/user.go:3"
		}
	}
	files2, err := Generate(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files1) != len(files2) {
		t.Fatalf("%d files, then %d", len(files1), len(files2))
	}
	for i, f := range files1 {
		if f.Name != files2[i].Name || !bytes.Equal(f.Data, files2[i].Data) {
			t.Errorf("%s depends on the positions", f.Name)
		}
	}
}

// No Field marshals a position, however it was built: neither the embedded
// loadjson.Field's Pos nor its own. The rest of the JSON is as it was, HTML
// unescaped for describe.
func TestFieldJSONHasNoPos(t *testing.T) {
	f := &Field{Field: loadjson.Field{Name: "a<b", Type: "string", Pos: "/x/user.go:3"}, Pos: "/x/user.go:3", GoName: "A"}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); strings.Contains(got, `"pos"`) || strings.Contains(got, "user.go") || !strings.Contains(got, `"name":"a<b"`) || !strings.Contains(got, `"go_name":"A"`) {
		t.Fatalf("got %s", got)
	}
	if f.Field.Pos == "" {
		t.Fatal("MarshalJSON changed the field")
	}
}

// optionalUnset breaks rules 37, 39 and 40 once each, beside the fields of
// those types that pass: Nillable, or with a Default, or with a DefaultFunc
// where the type has one.
type optionalUnset struct{ schema.Schema }

func (optionalUnset) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("mood").Values("calm", "busy").Optional(),
		field.Enum("mood_n").Values("calm").Optional().Nillable(),
		field.Enum("mood_d").Values("calm").Optional().Default("calm"),
		field.Bytes("blob").Optional(),
		field.Bytes("blob_n").Optional().Nillable(),
		field.Bytes("blob_d").Optional().Default([]byte("x")),
		field.Bytes("blob_f").Optional().DefaultFunc(func() []byte { return nil }),
		field.Time("seen").Optional(),
		field.Time("seen_n").Optional().Nillable(),
		field.Time("seen_d").Optional().Default(time.Now),
		field.Bytes("blob_r"), // required: no rule applies
	}
}

// Rules 37, 39 and 40 are one table; their messages are as they were.
func TestOptionalNeedsTable(t *testing.T) {
	_, err := build(t, optionalUnset{})
	want := `schema optionalUnset: field "mood": an Optional enum needs a Default or Nillable
schema optionalUnset: field "blob": an Optional bytes field needs a Default or Nillable
schema optionalUnset: field "seen": an Optional time field needs a Default or Nillable`
	if got := plain(err); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	rules := map[int]bool{}
	for _, r := range optionalNeeds {
		rules[r.rule] = true
	}
	if len(rules) != len(optionalNeeds) {
		t.Fatalf("two rows of optionalNeeds share a rule number: %+v", optionalNeeds)
	}
	if len(rules) != 3 || !rules[37] || !rules[39] || !rules[40] {
		t.Fatalf("rules %v, want 37, 39 and 40", rules)
	}
}

type uniqueJSON struct{ schema.Schema }

func (uniqueJSON) Fields() []schema.Field { return []schema.Field{field.Strings("tags").Unique()} }

type indexJSON struct{ schema.Schema }

func (indexJSON) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Strings("tags").StorageKey("tag_list")}
}
func (indexJSON) Indexes() []schema.Index {
	return []schema.Index{index.Fields("name", "tags"), index.Fields("name")}
}

// Rules 41 and 42: no index covers a JSON column, the unique one of a field
// or a declared one.
func TestJSONIndexRefused(t *testing.T) {
	for _, c := range []struct {
		s    schema.Interface
		want string
	}{
		{uniqueJSON{}, `schema uniqueJSON: field "tags": a JSON field cannot be Unique`},
		{indexJSON{}, `schema indexJSON: index 0: field "tags" is JSON, which no index can cover`},
	} {
		_, err := build(t, c.s)
		if got := plain(err); got != c.want {
			t.Errorf("got\n%s\nwant\n%s", got, c.want)
		}
	}
}

type dupIndexes struct{ schema.Schema }

func (dupIndexes) Fields() []schema.Field {
	return []schema.Field{field.String("a"), field.String("b").Unique(), field.Strings("tags")}
}
func (dupIndexes) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("a", "b"),
		index.Fields("b", "a"),            // another order
		index.Fields("a", "b"),            // rule 43
		index.Fields("a", "b").Unique(),   // unique, but of index 0's name: rule 44
		index.Fields("a", "b").Where("x"), // partial
		index.Fields("a", "b").Where("y"), // another predicate
		index.Fields("b"),                 // b's Unique is not compared
		index.Fields("a").StorageKey("dup"),
		index.Fields("b", "a").Unique().StorageKey("dup"), // rule 44
		index.Fields("b").Unique(),                        // beside b's Unique; index 6's name
		index.Fields("tags"),                              // rule 42
		index.Fields("tags"),                              // rule 42, and not 43
	}
}

// ixMix declares an index its schemas repeat.
type ixMix struct{ mixin.Schema }

func (ixMix) Indexes() []schema.Index { return []schema.Index{index.Fields("a")} }

type mixedDup struct{ schema.Schema }

var mixedDupLine int

func (mixedDup) Mixin() []schema.Mixin  { return []schema.Mixin{ixMix{}} }
func (mixedDup) Fields() []schema.Field { return []schema.Field{field.String("a")} }
func (mixedDup) Indexes() []schema.Index {
	ix, l := index.Fields("a"), here()
	mixedDupLine = l
	return []schema.Index{ix}
}

// Rules 43 and 44: declared indexes, mixins' included, neither repeat one
// another nor share a name.
func TestDuplicateIndexes(t *testing.T) {
	_, err := build(t, dupIndexes{})
	want := `schema dupIndexes: index 10: field "tags" is JSON, which no index can cover
schema dupIndexes: index 11: field "tags" is JSON, which no index can cover
schema dupIndexes: index 2: repeats index 0
schema dupIndexes: index 3: name "dupindexes_a_b" is also this schema's index 0
schema dupIndexes: index 8: name "dup" is also this schema's index 7
schema dupIndexes: index 9: name "dupindexes_b" is also this schema's index 6`
	if got := plain(err); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	_, err = build(t, mixedDup{})
	var ge *Error
	if got := plain(err); got != `schema mixedDup: index 1: repeats index 0` || !errors.As(err, &ge) ||
		!strings.HasSuffix(ge.Pos, fmt.Sprintf("/build_test.go:%d", mixedDupLine)) {
		t.Fatalf("got %v", err)
	}
}

// indexNames has the indexes on one column whose derived names are ent's,
// partial ones with a hash of Where.
type indexNames struct{ schema.Schema }

func (indexNames) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("names")}
}
func (indexNames) Fields() []schema.Field { return []schema.Field{field.String("a")} }
func (indexNames) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("a"),
		index.Fields("a").Where("x"),
		index.Fields("a").Where("y"),
		index.Fields("a").Unique().StorageKey("names_a_key"),
	}
}

// A derived index name is ent's: the entity's name in lower case and the
// columns. A partial index that shares it takes the hash of its Where; a full
// one keeps it.
func TestIndexNames(t *testing.T) {
	var got []string
	for _, ix := range mustBuild(t, indexNames{}).Entity("indexNames").Indexes {
		got = append(got, ix.Name)
	}
	want := []string{"indexnames_a", "indexnames_a_fd0c5087", "indexnames_a_fc0c4ef4", "names_a_key"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// fkPartial's table is its name in lower case, so a partial index on its
// foreign-key column alone is named like velox's foreign-key index of the
// column on PostgreSQL and SQLite, and takes the hash of its Where instead.
// fkPartialFull's full index leads with the column, so it has no foreign-key
// index and its partial index keeps its name.
type (
	fkPartial     struct{ schema.Schema }
	fkPartialFull struct{ schema.Schema }
)

func (fkPartial) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("fkpartial")}
}
func (fkPartial) Fields() []schema.Field { return []schema.Field{field.Int64("owner_id")} }
func (fkPartial) Edges() []schema.Edge {
	return []schema.Edge{edge.To("owner", Data.Type).Unique().Required().Field("owner_id")}
}
func (fkPartial) Indexes() []schema.Index {
	return []schema.Index{index.Fields("owner_id").Where("owner_id > 0")}
}

func (fkPartialFull) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("fkpartialfull")}
}
func (fkPartialFull) Fields() []schema.Field { return []schema.Field{field.Int64("owner_id")} }
func (fkPartialFull) Edges() []schema.Edge {
	return []schema.Edge{edge.To("owner", Data.Type).Unique().Required().Field("owner_id")}
}
func (fkPartialFull) Indexes() []schema.Index {
	return []schema.Index{index.Fields("owner_id").Where("owner_id > 0"), index.Fields("owner_id", "id")}
}

func TestPartialIndexNamedLikeAForeignKeyIndex(t *testing.T) {
	g := mustBuild(t, fkPartial{}, fkPartialFull{}, Data{})
	if got := g.Entity("fkPartial").Indexes[0].Name; got != partialIndexName("fkPartial", []string{"owner_id"}, "owner_id > 0") || got == "fkpartial_owner_id" {
		t.Errorf("fkPartial: %q", got)
	}
	if got := g.Entity("fkPartialFull").Indexes[0].Name; got != "fkpartialfull_owner_id" {
		t.Errorf("fkPartialFull: %q", got)
	}
}

type keyShareA struct{ schema.Schema }

func (keyShareA) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("share_a")}
}
func (keyShareA) Fields() []schema.Field { return []schema.Field{field.String("a")} }
func (keyShareA) Indexes() []schema.Index {
	return []schema.Index{index.Fields("a").StorageKey("shared"), index.Fields("a").Unique()}
}

type keyShareB struct{ schema.Schema }

func (keyShareB) Fields() []schema.Field { return []schema.Field{field.String("b")} }
func (keyShareB) Indexes() []schema.Index {
	return []schema.Index{index.Fields("b").StorageKey("shared"), index.Fields("b").Unique().StorageKey("keysharea_a")}
}

// keyShareC holds its table in another database schema, whose index names
// are its own.
type keyShareC struct{ schema.Schema }

func (keyShareC) Fields() []schema.Field { return []schema.Field{field.String("c")} }
func (keyShareC) Indexes() []schema.Index {
	return []schema.Index{index.Fields("c").StorageKey("shared")}
}
func (keyShareC) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("other", "key_share_cs")}
}

type keyMix struct{ mixin.Schema }

func (keyMix) Indexes() []schema.Index { return []schema.Index{index.Fields("a").StorageKey("k")} }

type keyMixed struct{ schema.Schema }

func (keyMixed) Mixin() []schema.Mixin  { return []schema.Mixin{keyMix{}} }
func (keyMixed) Fields() []schema.Field { return []schema.Field{field.String("a"), field.String("b")} }
func (keyMixed) Indexes() []schema.Index {
	return []schema.Index{index.Fields("b").StorageKey("k")}
}

// Rule 44 compares every index's name, a StorageKey or a derived one, across
// the graph, within one database schema.
func TestIndexNamesAcrossGraph(t *testing.T) {
	_, err := build(t, keyShareA{}, keyShareB{}, keyShareC{})
	want := `schema keyShareB: index 0: name "shared" is also schema keyShareA's index 0
schema keyShareB: index 1: name "keysharea_a" is also schema keyShareA's index 1`
	if got := plain(err); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	_, err = build(t, keyMixed{})
	if got := plain(err); got != `schema keyMixed: index 1: name "k" is also this schema's index 0` {
		t.Fatalf("got %v", err)
	}
}

type odOwner struct{ schema.Schema }

func (odOwner) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", odPet.Type).OnDelete(edge.Cascade)}
}

type odPet struct{ schema.Schema }

func (odPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", odOwner.Type).Ref("pets").Unique()}
}

type odInverseOnly struct{ schema.Schema }

func (odInverseOnly) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", odPet2.Type)}
}

type odPet2 struct{ schema.Schema }

func (odPet2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", odInverseOnly.Type).Ref("pets").Unique().OnDelete(edge.SetNull)}
}

type odInline struct{ schema.Schema }

func (odInline) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", odInline.Type).From("parent").Unique().OnDelete(edge.Restrict)}
}

type odM2M struct{ schema.Schema }

func (odM2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", odM2M2.Type).OnDelete(edge.Cascade)}
}

type odM2M2 struct{ schema.Schema }

func (odM2M2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("others", odM2M.Type).Ref("peers")}
}

// odM2MInv and odM2MInv2 are a many-to-many pair with OnDelete on the
// inverse only.
type odM2MInv struct{ schema.Schema }

func (odM2MInv) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", odM2MInv2.Type)}
}

type odM2MInv2 struct{ schema.Schema }

func (odM2MInv2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("others", odM2MInv.Type).Ref("peers").OnDelete(edge.Cascade)}
}

// odSetNullRequired's key to odSetNullTarget is NOT NULL: a key to its own
// schema would be NULL-able, as in ent.
type odSetNullRequired struct{ schema.Schema }

func (odSetNullRequired) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", odSetNullTarget.Type).Unique().Required().OnDelete(edge.SetNull)}
}

type odSetNullTarget struct{ schema.Schema }

type odSetNullField struct{ schema.Schema }

func (odSetNullField) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional()}
}
func (odSetNullField) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", odSetNullField.Type).Unique().Field("parent_id").OnDelete(edge.SetNull)}
}

type odSetNullOK struct{ schema.Schema }

func (odSetNullOK) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Optional().Nillable()}
}
func (odSetNullOK) Edges() []schema.Edge {
	return []schema.Edge{edge.To("parent", odSetNullOK.Type).Unique().Field("parent_id").OnDelete(edge.SetNull)}
}

type odDiffOwner struct{ schema.Schema }

func (odDiffOwner) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", odDiffPet.Type).OnDelete(edge.Restrict)}
}

type odDiffPet struct{ schema.Schema }

func (odDiffPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", odDiffOwner.Type).Ref("pets").Unique().OnDelete(edge.Cascade)}
}

func TestOnDelete(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   []schema.Interface
		want string
	}{
		{"45 many-to-many", []schema.Interface{odM2M{}, odM2M2{}}, `schema odM2M: edge "peers": OnDelete has no foreign key to act on in a many-to-many edge; join rows are deleted with either end`},
		{"45 many-to-many on the inverse only", []schema.Interface{odM2MInv{}, odM2MInv2{}}, `schema odM2MInv2: edge "others": OnDelete has no foreign key to act on in a many-to-many edge; join rows are deleted with either end`},
		{"46 required", []schema.Interface{odSetNullRequired{}, odSetNullTarget{}}, `schema odSetNullRequired: edge "parent": SET NULL needs a NULL-able foreign key`},
		{"47 pair differs", []schema.Interface{odDiffOwner{}, odDiffPet{}}, `schema odDiffPet: edge "owner": OnDelete CASCADE differs from edge "pets"'s RESTRICT on odDiffOwner`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := build(t, c.ss...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
				t.Fatalf("%d errors, want 1:\n%v", n, err)
			}
		})
	}

	t.Run("46 field not Nillable", func(t *testing.T) {
		_, err := build(t, odSetNullField{})
		if err == nil || !strings.Contains(err.Error(), "SET NULL needs a NULL-able foreign key") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("46 Nillable field passes", func(t *testing.T) {
		if got := mustBuild(t, odSetNullOK{}).Entity("odSetNullOK").Edges[0].Rel.OnDelete; got != "SET NULL" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("either side sets the relation's action", func(t *testing.T) {
		g := mustBuild(t, odOwner{}, odPet{})
		for _, n := range []string{"odOwner", "odPet"} {
			if got := g.Entity(n).Edges[0].Rel.OnDelete; got != "CASCADE" {
				t.Errorf("%s: got %q", n, got)
			}
		}
		g = mustBuild(t, odInverseOnly{}, odPet2{})
		for _, n := range []string{"odInverseOnly", "odPet2"} {
			if got := g.Entity(n).Edges[0].Rel.OnDelete; got != "SET NULL" {
				t.Errorf("%s: got %q", n, got)
			}
		}
	})
	t.Run("inline sets the inverse side", func(t *testing.T) {
		e := mustBuild(t, odInline{}).Entity("odInline")
		for _, ed := range e.Edges {
			if ed.Rel.OnDelete != "RESTRICT" {
				t.Errorf("%s: got %q", ed.Name, ed.Rel.OnDelete)
			}
		}
	})
	t.Run("unset is SET NULL on a NULL-able key and none on a NOT NULL one", func(t *testing.T) {
		if got := mustBuild(t, Owner{}, Pet{}).Entity("Owner").Edges[0].Rel.OnDelete; got != "SET NULL" {
			t.Fatalf("got %q", got)
		}
		if got := mustBuild(t, dodReq{}, dodReqPet{}).Entity("dodReq").Edges[0].Rel.OnDelete; got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("describe", func(t *testing.T) {
		var b bytes.Buffer
		if err := Describe(&b, mustBuild(t, odOwner{}, odPet{}), false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "od_pets.od_owner_pets on delete CASCADE") {
			t.Fatalf("got\n%s", b.String())
		}
		b.Reset()
		_ = Describe(&b, mustBuild(t, dodReq{}, dodReqPet{}), false)
		if strings.Contains(b.String(), "on delete") {
			t.Fatalf("an unset action is shown:\n%s", b.String())
		}
	})
}

// capIndexes has two indexes whose derived names, 76 bytes, differ only
// at their end.
type capIndexes struct{ schema.Schema }

var capColumn = strings.Repeat("c", 63)

func (capIndexes) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("caps")} }
func (capIndexes) Fields() []schema.Field {
	return []schema.Field{field.String(capColumn), field.String("p"), field.String("q")}
}
func (capIndexes) Indexes() []schema.Index {
	return []schema.Index{index.Fields(capColumn, "p"), index.Fields(capColumn, "q")}
}

// A derived index name over the cap is capped as ent caps it, on each
// dialect, and stays apart from another that differs only past the cut. The
// Graph keeps it whole; the plugin model gives PostgreSQL's.
func TestIndexNamesCapped(t *testing.T) {
	g := mustBuild(t, capIndexes{})
	ixs := g.Entity("capIndexes").Indexes
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	var plugged []string
	for _, e := range PluginModel(g, o).Entities {
		if e.Name == "capIndexes" {
			for _, ix := range e.Indexes {
				plugged = append(plugged, ix.Name)
			}
		}
	}
	wholes := []string{"capindexes_" + capColumn + "_p", "capindexes_" + capColumn + "_q"}
	for i, whole := range wholes {
		if got := ixs[i].Name; got != whole || !ixs[i].Derived {
			t.Errorf("index %d: %q, derived %v; want %q", i, got, ixs[i].Derived, whole)
		}
	}
	for d, size := range map[velox.Dialect]int{velox.Postgres: 63, velox.MySQL: 64, velox.SQLite: 64} {
		s, err := Tables(g, d)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, ix := range s.Tables[0].Indexes {
			got = append(got, ix.Name)
			if len(ix.Name) != size {
				t.Errorf("%v: %q is %d bytes, want %d", d, ix.Name, len(ix.Name), size)
			}
		}
		want := []string{ddl.Symbol(d, wholes[0]), ddl.Symbol(d, wholes[1])}
		if slices.Sort(want); !slices.Equal(got, want) || got[0] == got[1] {
			t.Errorf("%v: indexes %q, want %q", d, got, want)
		}
	}
	if want := []string{ddl.Symbol(velox.Postgres, wholes[0]), ddl.Symbol(velox.Postgres, wholes[1])}; !slices.Equal(plugged, want) {
		t.Errorf("the plugin model names them %q, want %q", plugged, want)
	}
}

type nsUser struct{ schema.Schema }

func (nsUser) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("users")} }
func (nsUser) Fields() []schema.Field {
	return []schema.Field{field.Enum("role").Values("a", "b")}
}

type nsUsers struct{ schema.Schema }

func (nsUsers) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("Users")} }

type nsPost struct{ schema.Schema }

func (nsPost) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("posts")} }
func (nsPost) Fields() []schema.Field           { return []schema.Field{field.String("title")} }
func (nsPost) Indexes() []schema.Index {
	return []schema.Index{index.Fields("title").StorageKey("posts")}
}

// nsCheckUser's edge's symbol is its enum's check.
type nsCheckUser struct{ schema.Schema }

func (nsCheckUser) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("users")}
}
func (nsCheckUser) Fields() []schema.Field {
	return []schema.Field{field.Enum("role").Values("a", "b")}
}
func (nsCheckUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Symbol("users_role_check"))}
}

type nsVeloxNew struct{ schema.Schema }

func (nsVeloxNew) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("velox_new_users")}
}

// nsOther holds a table of nsUser's name in another database schema, with
// an enum type of its name and other values.
type nsOther struct{ schema.Schema }

func (nsOther) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("other", "users")}
}
func (nsOther) Fields() []schema.Field { return []schema.Field{field.Enum("role").Values("x")} }

type NsStatus string

func (NsStatus) Values() []string { return []string{"open", "closed"} }

type nsTicket struct{ schema.Schema }

func (nsTicket) Fields() []schema.Field { return []schema.Field{field.EnumOf[NsStatus]("status")} }

type nsIssue struct{ schema.Schema }

func (nsIssue) Fields() []schema.Field { return []schema.Field{field.EnumOf[NsStatus]("state")} }

// nsSharedTable has two relations on one join table.
type nsSharedTable struct{ schema.Schema }

func (nsSharedTable) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("friends", nsSharedTable.Type).StorageKey(edge.Table("shared_links")),
		edge.To("rivals", nsSharedTable.Type).StorageKey(edge.Table("shared_links")),
	}
}

// Two distinct relations cannot share one edge.Table: their rows would mix.
func TestSharedEdgeTableRefused(t *testing.T) {
	_, err := build(t, nsSharedTable{})
	if got, want := plain(err), `schema nsSharedTable: edge "rivals": name "shared_links" is also this schema's join table`; err == nil || got != want {
		t.Fatalf("got\n%v\nwant\n%s", got, want)
	}
}

// Rule 44 compares, case-insensitively, the names of relations, of types and
// of constraints, each within one database schema, the planner's helpers
// included.
func TestNamespace(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   []schema.Interface
		want string
	}{
		{"tables by case", []schema.Interface{nsUser{}, nsUsers{}}, `schema nsUsers: name "Users" is also schema nsUser's table`},
		{"table and enum type", []schema.Interface{nsOrder{}, nsOrderStatus{}}, `schema nsOrder: field "status": name "orders_status" is also schema nsOrderStatus's table`},
		{"index and table", []schema.Interface{nsPost{}}, `schema nsPost: index 0: name "posts" is also this schema's table`},
		{"symbol and check", []schema.Interface{nsCheckUser{}, Pet{}}, `schema nsCheckUser: edge "pets": name "users_role_check" is also this schema's check of field "role"`},
		{"table and rebuild table", []schema.Interface{nsUser{}, nsVeloxNew{}}, `schema nsVeloxNew: name "velox_new_users" is also schema nsUser's rebuild table`},
		{"two database schemas", []schema.Interface{nsUser{}, nsOther{}}, ``},
		{"one shared enum type", []schema.Interface{nsTicket{}, nsIssue{}}, ``},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := build(t, c.ss...)
			if got := plain(err); err == nil && c.want != "" || err != nil && got != c.want {
				t.Fatalf("got\n%v\nwant\n%s", got, c.want)
			}
		})
	}
}

// PostgreSQL keeps indexes and types apart: an index and an enum type may
// share the name orders_status.
type nsIndexedEnum struct{ schema.Schema }

func (nsIndexedEnum) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("orders")}
}
func (nsIndexedEnum) Fields() []schema.Field { return []schema.Field{field.Enum("status").Values("a")} }
func (nsIndexedEnum) Indexes() []schema.Index {
	return []schema.Index{index.Fields("status").StorageKey("orders_status")}
}

func TestNamespaceIndexOnEnumColumn(t *testing.T) {
	ix := mustBuild(t, nsIndexedEnum{}).Entity("nsIndexedEnum").Indexes[0]
	if ix.Name != "orders_status" {
		t.Fatalf("index %q", ix.Name)
	}
}

// Rule 52's error names the other field with its position.
func TestSharedEnumValuesPos(t *testing.T) {
	_, err := build(t, enumOrder{}, enumTicket{})
	want := fmt.Sprintf("/build_test.go:%d), with other values", enumOrderLine)
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("got %v, want the suffix %q", err, want)
	}
}

func TestUnexportedName(t *testing.T) {
	for ident, want := range map[string]string{
		"int64":                    "",
		"[16]uint8":                "",
		"uuid.UUID":                "",
		"map[string]*http.Request": "",
		"schema.cents":             "schema.cents",
		"[]*other.x":               "other.x",
		"map[a.K]chan b.v":         "b.v",
		"map[a.k]b.V":              "a.k",
	} {
		if got := unexportedName(ident); got != want {
			t.Errorf("unexportedName(%q) = %q, want %q", ident, got, want)
		}
	}
}

// ukUser and ukProfile are a one-to-one pair whose key, in the inverse's
// table as in ent, is bound to a field; ukNode a one-to-one edge to its own
// schema bound to a field; and ukLine a many-to-one edge to ukInvoice bound
// to a field.
type ukUser struct{ schema.Schema }

func (ukUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("profile", ukProfile.Type).Unique()}
}

type ukProfile struct{ schema.Schema }

func (ukProfile) Fields() []schema.Field {
	return []schema.Field{field.Int64("owner_id").Optional().Nillable()}
}
func (ukProfile) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", ukUser.Type).Ref("profile").Unique().Field("owner_id")}
}

type ukNode struct{ schema.Schema }

func (ukNode) Fields() []schema.Field {
	return []schema.Field{field.Int64("next_id").Optional().Nillable()}
}
func (ukNode) Edges() []schema.Edge {
	return []schema.Edge{edge.To("next", ukNode.Type).Unique().Field("next_id")}
}

type ukLine struct{ schema.Schema }

func (ukLine) Fields() []schema.Field {
	return []schema.Field{field.Int64("invoice_id").Optional().Nillable()}
}
func (ukLine) Edges() []schema.Edge {
	return []schema.Edge{edge.To("invoice", ukInvoice.Type).Unique().Field("invoice_id")}
}

type ukInvoice struct{ schema.Schema }

// Every one-to-one key column is unique, implicit or bound, as ent's fkColumn
// makes it; a many-to-one key is not.
func TestOneToOneKeyIsUnique(t *testing.T) {
	g := mustBuild(t, ukUser{}, ukProfile{}, ukNode{}, ukLine{}, ukInvoice{})
	for _, c := range []struct {
		entity, field string
		unique        bool
	}{
		{"ukProfile", "owner_id", true},
		{"ukNode", "next_id", true},
		{"ukLine", "invoice_id", false},
	} {
		if f := g.Entity(c.entity).Field(c.field); f == nil || f.Unique != c.unique {
			t.Errorf("%s.%s: got %+v, want Unique %v", c.entity, c.field, f, c.unique)
		}
	}
}

// dodOwner and dodPet are a one-to-many pair with a NULL-able key, dodReq and
// dodReqPet one with a NOT NULL key, and dodM2M and dodM2M2 a many-to-many
// pair; none sets OnDelete.
type dodOwner struct{ schema.Schema }

func (dodOwner) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", dodPet.Type)} }

type dodPet struct{ schema.Schema }

func (dodPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", dodOwner.Type).Ref("pets").Unique()}
}

type dodReq struct{ schema.Schema }

func (dodReq) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", dodReqPet.Type)} }

type dodReqPet struct{ schema.Schema }

func (dodReqPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", dodReq.Type).Ref("pets").Unique().Required()}
}

type dodM2M struct{ schema.Schema }

func (dodM2M) Edges() []schema.Edge { return []schema.Edge{edge.To("peers", dodM2M2.Type)} }

type dodM2M2 struct{ schema.Schema }

func (dodM2M2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("others", dodM2M.Type).Ref("peers")}
}

// With no OnDelete, a NULL-able key is SET NULL and a NOT NULL one has none
// (NO ACTION), as ent's deleteAction; an explicit OnDelete wins, and a
// many-to-many relation has no action.
func TestDefaultOnDelete(t *testing.T) {
	g := mustBuild(t, dodOwner{}, dodPet{}, dodReq{}, dodReqPet{}, dodM2M{}, dodM2M2{}, odOwner{}, odPet{})
	for _, c := range []struct{ entity, want string }{
		{"dodOwner", "SET NULL"},
		{"dodPet", "SET NULL"},
		{"dodReq", ""},
		{"dodReqPet", ""},
		{"odOwner", "CASCADE"},
		{"odPet", "CASCADE"},
		{"dodM2M", ""},
		{"dodM2M2", ""},
	} {
		if got := g.Entity(c.entity).Edges[0].Rel.OnDelete; got != c.want {
			t.Errorf("%s: got %q, want %q", c.entity, got, c.want)
		}
	}
}

// reqSelf is a tree whose parent is Required, and reqSelfCascade one whose
// Required parent says OnDelete(Cascade), which is not the default. (A
// Required unique edge to its own schema with no inverse is a bidirectional
// one-to-one, which rule 53 refuses.)
type reqSelf struct{ schema.Schema }

func (reqSelf) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", reqSelf.Type).From("parent").Unique().Required()}
}

type reqSelfCascade struct{ schema.Schema }

func (reqSelfCascade) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", reqSelfCascade.Type).From("parent").Unique().Required().OnDelete(edge.Cascade)}
}

// A Required edge to its own schema keeps its implicit key NULL-able, as
// ent's and veloxe's tables do (n != e.Type), so its default is SET NULL;
// the edge stays Required.
func TestRequiredSelfKeyIsNullable(t *testing.T) {
	g := mustBuild(t, reqSelf{}, reqSelfCascade{})
	e := g.Entity("reqSelf")
	if f := e.Field("req_self_children"); f == nil || !f.Optional || !f.Nillable || !f.Pointer {
		t.Fatalf("req_self_children %+v", f)
	}
	for _, ed := range e.Edges {
		if ed.Rel.OnDelete != "SET NULL" {
			t.Errorf("%s: got %q, want SET NULL", ed.Name, ed.Rel.OnDelete)
		}
	}
	if p := e.Edge("parent"); !p.Required {
		t.Errorf("parent is not Required: %+v", p)
	}
	if got := g.Entity("reqSelfCascade").Edges[0].Rel.OnDelete; got != "CASCADE" {
		t.Errorf("reqSelfCascade: got %q", got)
	}
	for _, d := range allDialects {
		s := tables(t, d, reqSelf{})
		if c := column(t, table(t, s, e.Table), "req_self_children"); !c.Nullable {
			t.Errorf("%v: req_self_children is NOT NULL", d)
		}
	}
}

// reqSelfBound is a tree whose Required parent edge binds the key to a
// field that is neither Optional nor Nillable.
type reqSelfBound struct{ schema.Schema }

func (reqSelfBound) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id")}
}

func (reqSelfBound) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", reqSelfBound.Type).From("parent").Unique().Required().Field("parent_id")}
}

// A Required edge to its own schema is NULL-able only through its implicit
// key. A key bound to a field stays NOT NULL unless the field is Nillable.
// (An Optional bound key on a Required edge is refused: TestBuildRules, rule
// 13.)
func TestRequiredSelfBoundKeyIsNotNull(t *testing.T) {
	g := mustBuild(t, reqSelfBound{})
	e := g.Entity("reqSelfBound")
	if f := e.Field("parent_id"); f == nil || f.Optional || f.Nillable {
		t.Fatalf("parent_id %+v", f)
	}
	for _, d := range allDialects {
		s := tables(t, d, reqSelfBound{})
		if c := column(t, table(t, s, e.Table), "parent_id"); c.Nullable {
			t.Errorf("%v: parent_id is NULL-able", d)
		}
	}
}

// reqSelfBoundNillable is reqSelfBound with a Nillable, not Optional, key.
type reqSelfBoundNillable struct{ schema.Schema }

func (reqSelfBoundNillable) Fields() []schema.Field {
	return []schema.Field{field.Int64("parent_id").Nillable()}
}

func (reqSelfBoundNillable) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", reqSelfBoundNillable.Type).From("parent").Unique().Required().Field("parent_id")}
}

// A key bound to a Nillable field is NULL-able on a Required edge to its own
// schema, though the field is not Optional.
func TestRequiredSelfBoundNillableKeyIsNullable(t *testing.T) {
	g := mustBuild(t, reqSelfBoundNillable{})
	e := g.Entity("reqSelfBoundNillable")
	if f := e.Field("parent_id"); f == nil || f.Optional || !f.Nillable {
		t.Fatalf("parent_id %+v", f)
	}
	for _, d := range allDialects {
		s := tables(t, d, reqSelfBoundNillable{})
		if c := column(t, table(t, s, e.Table), "parent_id"); !c.Nullable {
			t.Errorf("%v: parent_id is NOT NULL", d)
		}
	}
}

// noaOwner and noaPet are a one-to-many pair with a NULL-able key that sets
// NoAction; noaM2M and noaM2M2 a many-to-many pair that sets it.
type noaOwner struct{ schema.Schema }

func (noaOwner) Edges() []schema.Edge { return []schema.Edge{edge.To("pets", noaPet.Type)} }

type noaPet struct{ schema.Schema }

func (noaPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", noaOwner.Type).Ref("pets").Unique().OnDelete(edge.NoAction)}
}

type noaM2M struct{ schema.Schema }

func (noaM2M) Edges() []schema.Edge {
	return []schema.Edge{edge.To("peers", noaM2M2.Type).OnDelete(edge.NoAction)}
}

type noaM2M2 struct{ schema.Schema }

func (noaM2M2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("others", noaM2M.Type).Ref("peers")}
}

// An explicit NoAction is not overwritten by the default: the model keeps it,
// describe shows it, and the database model has migrate's NO ACTION, "",
// which renders no ON DELETE clause.
func TestNoAction(t *testing.T) {
	g := mustBuild(t, noaOwner{}, noaPet{})
	for _, n := range []string{"noaOwner", "noaPet"} {
		if got := g.Entity(n).Edges[0].Rel.OnDelete; got != "NO ACTION" {
			t.Errorf("%s: got %q", n, got)
		}
	}
	var b bytes.Buffer
	if err := Describe(&b, g, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "on delete NO ACTION") {
		t.Errorf("describe:\n%s", b.String())
	}
	for _, d := range allDialects {
		fks := table(t, tables(t, d, noaOwner{}, noaPet{}), "noa_pets").ForeignKeys
		if len(fks) != 1 || fks[0].OnDelete != "" {
			t.Errorf("%v: %+v", d, fks)
		}
	}
	_, err := build(t, noaM2M{}, noaM2M2{})
	if err == nil || !strings.Contains(err.Error(), "OnDelete has no foreign key to act on in a many-to-many edge") {
		t.Errorf("many-to-many: got %v", err)
	}
}

// The bound one-to-one key's unique index has ent's name for a unique
// column: <table>_<column>_key, the column on MySQL.
func TestOneToOneKeyIndex(t *testing.T) {
	for d, want := range map[velox.Dialect]string{velox.Postgres: "uk_profiles_owner_id_key", velox.MySQL: "owner_id", velox.SQLite: "uk_profiles_owner_id_key"} {
		ixs := table(t, tables(t, d, ukUser{}, ukProfile{}), "uk_profiles").Indexes
		if !slices.ContainsFunc(ixs, func(ix migrate.Index) bool {
			return ix.Name == want && ix.Unique && slices.Equal(ix.Columns, []string{"owner_id"})
		}) {
			t.Errorf("%v: %+v, want unique %s", d, ixs, want)
		}
	}
}
