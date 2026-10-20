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
	if ix := u.Indexes[0]; ix.Name != "users_org_id_name" || !ix.Unique || ix.Columns[1] != "name" {
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
	for name, want := range map[string]bool{"seen": true, "nick": false, "label": false, "count": false, "bio": true, "opt": false} {
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
		field.Int("count").DefaultFunc(func() int { return 1 }),
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

// A unique edge to its own schema is a bidirectional O2O.
type Person struct{ schema.Schema }

func (Person) Edges() []schema.Edge { return []schema.Edge{edge.To("spouse", Person.Type).Unique()} }

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

type defaultBool struct{ schema.Schema }

func (defaultBool) Fields() []schema.Field { return []schema.Field{field.Bool("active").Default(true)} }

type defaultNumber struct{ schema.Schema }

func (defaultNumber) Fields() []schema.Field {
	return []schema.Field{field.Float("score").Optional().Default(1.5)}
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

type bytesTenant struct{ schema.Schema }

func (bytesTenant) Fields() []schema.Field { return []schema.Field{field.Bytes("org").Tenant()} }

type emptyEnum struct{ schema.Schema }

func (emptyEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s")} }

type dupEnum struct{ schema.Schema }

func (dupEnum) Fields() []schema.Field { return []schema.Field{field.Enum("s").Values("a", "b", "a")} }

type statusValues string

func (statusValues) Values() []string { return []string{"a", "b"} }

type enumGoTypeDup struct{ schema.Schema }

func (enumGoTypeDup) Fields() []schema.Field {
	return []schema.Field{field.Enum("s").Values("a").GoType(statusValues(""))}
}

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

type cycle struct{ schema.Schema }

func (cycle) Edges() []schema.Edge {
	return []schema.Edge{edge.To("next", cycle.Type).From("cycles")}
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
		{"8 wrong ref", []schema.Interface{wrongRef{}, refHolder{}, Pet{}}, `schema wrongRef: edge "x": refHolder.pets points to Pet, not to this schema`},
		{"9 both required", []schema.Interface{reqA{}, reqB{}}, `schema reqB: edge "a": cannot be required in both directions with reqA.b`},
		{"10 field missing", []schema.Interface{fieldMissing{}}, `schema fieldMissing: edge "parent": field "parent_id" is not declared`},
		{"11 field wrong side", []schema.Interface{fieldWrongSide{}, Pet{}}, `schema fieldWrongSide: edge "pets": binds field "x", but the foreign key is in table pets`},
		{"11 field on M2M", []schema.Interface{fieldMany{}}, `schema fieldMany: edge "peers": binds field "x", but the foreign key is in table field_many_peers`},
		{"12 field wrong type", []schema.Interface{fieldStringFK{}}, `schema fieldStringFK: edge "parent": field "parent_id" is string; fieldStringFK's ID is int64`},
		{"12 ported int field", []schema.Interface{fieldWrongType{}}, `schema fieldWrongType: edge "parent": field "parent_id" is int; fieldWrongType's ID is int64; declare it field.Int64, or declare the ID with field.Int("id")`},
		{"13 field optional", []schema.Interface{fieldOptional{}}, `schema fieldOptional: edge "parent": field "parent_id" must be Optional exactly when the edge is not Required`},
		{"14 two tenants", []schema.Interface{twoTenants{}}, `schema twoTenants: field "b": a second tenant field; "a" is the tenant`},
		{"15 optional tenant", []schema.Interface{optionalTenant{}}, `schema optionalTenant: field "org": the tenant field must be required and not Nillable`},
		{"16 default tenant", []schema.Interface{defaultTenant{}}, `schema defaultTenant: field "org": the tenant field cannot have a default: the context's tenant fills it`},
		{"17 bytes tenant", []schema.Interface{bytesTenant{}}, `schema bytesTenant: field "org": the tenant field must be of a comparable type`},
		{"18 empty enum", []schema.Interface{emptyEnum{}}, `schema emptyEnum: field "s": an enum needs Values`},
		{"19 duplicate enum value", []schema.Interface{dupEnum{}}, `schema dupEnum: field "s": value "a" is given twice`},
		{"19 value from Values and GoType", []schema.Interface{enumGoTypeDup{}}, `schema enumGoTypeDup: field "s": value "a" is given twice`},
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
		{"35 join columns collide on an inverse", []schema.Interface{cycle{}}, `schema cycle: edge "cycles": join table cycle_next has two columns named cycle_id; name them with edge.Columns`},
		{"36 default on a bool", []schema.Interface{defaultBool{}}, `schema defaultBool: field "active": a Default on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default`},
		{"36 default on an optional number", []schema.Interface{defaultNumber{}}, `schema defaultNumber: field "score": a Default on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default`},
		{"37 optional enum", []schema.Interface{optionalEnum{}}, `schema optionalEnum: field "mood": an Optional enum needs a Default or Nillable`},
		{"38 optional edge field", []schema.Interface{optionalFK{}}, `schema optionalFK: edge "parent": field "parent_id" must be Nillable when the edge is not Required: an unset foreign key is NULL`},
		{"39 optional bytes", []schema.Interface{optionalBytes{}}, `schema optionalBytes: field "blob": an Optional bytes field needs a Default or Nillable`},
		{"40 optional time", []schema.Interface{optionalTime{}}, `schema optionalTime: field "seen": an Optional time field needs a Default or Nillable`},
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
		{tableOnO2M{}, Pet{}}, {badFieldName{}}, {emptyFieldName{}}, {badEdgeName{}}, {fieldGoNames{}}, {edgeGoNames{}},
		{enumGoNames{}}, {clashOwner{}, clashPet{}}, {columnFieldMismatch{}}, {keyA{}, keyB{}}, {oneColumnM2M{}},
		{twoColumnsO2M{}, Pet{}}, {invAssoc{}, invItem{}}, {enumGoTypeDup{}},
		{fkUnbound{}}, {fieldStringFK{}}, {immutableEdge{}}, {immutableField{}}, {loop{}}, {cycle{}},
		{defaultBool{}}, {defaultNumber{}}, {optionalEnum{}}, {optionalFK{}}, {optionalBytes{}}, {optionalTime{}},
	} {
		_, err := build(t, ss...)
		if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
			t.Errorf("%T: %d errors, want 1:\n%v", ss[0], n, err)
		}
	}
}

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
	want := `schema manyErrors: field "a": declared twice
schema manyErrors: edge "x": an inverse edge needs Ref(name)
schema manyErrors: field "s": an enum needs Values
schema manyErrors: index 0: names no fields or edges`
	if err == nil || err.Error() != want {
		t.Fatalf("got\n%v\nwant\n%s", err, want)
	}
}
