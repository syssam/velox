package velox

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// TestRelationsResolveWithoutDeadlock is half of Review Focus 1.
func TestRelationsResolveWithoutDeadlock(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := CheckEntity[company](); err != nil {
				t.Error(err)
			}
			if err := CheckEntity[employee](); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	co, em := (*company)(nil).VeloxMeta().info, (*employee)(nil).VeloxMeta().info
	for _, c := range []struct {
		owner         *EntityInfo
		rel           int
		target        *EntityInfo
		ownerCol, col string
		use           useTarget // the target as the use site names it
	}{
		{co, 0, em, "id", "company_id", targetOf[employee]()}, // Many: employee.CompanyID
		{em, 0, em, "boss_id", "id", targetOf[employee]()},    // One
		{em, 1, co, "company_id", "id", targetOf[company]()},  // One: employee.CompanyID
	} {
		l, err := c.owner.rels[c.rel].resolve(c.owner, c.use)
		if err != nil || l.target != c.target || c.owner.Columns[l.ownerCol].Name != c.ownerCol || l.target.Columns[l.targetCol].Name != c.col {
			t.Errorf("%s.%s: %+v, %v", c.owner.Name, c.owner.rels[c.rel].name, l, err)
		}
	}
}

// TestResolveRefusesMismatchedKeys is half of Review Focus 2.
func TestResolveRefusesMismatchedKeys(t *testing.T) {
	info, err := loadDesc(keyedDesc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info, targetOf[keyedLine]())
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "the key types differ: velox.keyed.ID is string and velox.keyedLine.KeyedID is int64") {
		t.Errorf("%v", err)
	}
}

type badRels struct {
	ID    int64
	Lines Many[keyedLine]
	Other One[keyedLine]
}

func (x *badRels) Schema(s *Schema) {
	s.Many(&x.Lines).References(&x.ID)
	s.One(&x.Other).ForeignKey(func(l *keyedLine) *int64 { return &l.KeyedID }).References(&x.ID)
}

func TestRelationDeclarationErrors(t *testing.T) {
	x := new(badRels)
	_, err := buildInfo(reflect.TypeFor[badRels](), unsafe.Pointer(x), any(x), []string{"ID"},
		[]Relation{RelationTo[keyedLine]("Lines"), RelationTo[keyedLine]("Other")})
	for _, want := range []string{
		"References applies to a One relation; a Many relation's key is on its target: use ForeignKey",
		"relation Other declares both ForeignKey and References",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

type lonely struct {
	ID    int64
	Lines Many[keyedLine] // keyedLine has no LonelyID
}

func (x *lonely) Schema(*Schema) {}

func TestMissingConventionalKey(t *testing.T) {
	x := new(lonely)
	info, err := buildInfo(reflect.TypeFor[lonely](), unsafe.Pointer(x), any(x), []string{"ID"}, []Relation{RelationTo[keyedLine]("Lines")})
	if err == nil {
		_, err = info.rels[0].resolve(info, useTarget{})
	}
	if err == nil || !strings.Contains(err.Error(), "velox.keyedLine has no field LonelyID; name one, or declare the key with s.Many(&x.Lines).ForeignKey") {
		t.Errorf("%v", err)
	}
}

func TestHolderLayout(t *testing.T) {
	var o One[int]
	var m Many[int]
	var h holder
	if unsafe.Sizeof(o) != unsafe.Sizeof(h) || unsafe.Sizeof(m) != unsafe.Sizeof(h) ||
		unsafe.Offsetof(o.loaded) != unsafe.Offsetof(h.loaded) || unsafe.Offsetof(m.loaded) != unsafe.Offsetof(h.loaded) {
		t.Fatal("One and Many must have holder's layout")
	}
}

// shapeOf is the Shape of a fixture whose generated code is not written out.
func shapeOf[E any](cols []string, rels ...Relation) Shape[E] {
	return Shape[E]{Columns: cols, Relations: rels, Addr: func(*E, int) any { return nil }}
}

// build runs buildInfo on a zero E, as MetaOnce.Get does.
func build[E any](cols []string, rels ...Relation) (*EntityInfo, error) {
	x := new(E)
	return buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), cols, rels)
}

// resolveAll builds E and resolves its first relation.
func resolveAll[E any](cols []string, rels ...Relation) error {
	info, err := build[E](cols, rels...)
	if err != nil {
		return err
	}
	_, err = info.rels[0].resolve(info, useTarget{})
	return err
}

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), want) {
		t.Errorf("want a *SchemaError containing %q, got %v", want, err)
	}
	if err != nil && strings.Contains(err.Error(), "velox: velox:") || err != nil && strings.Contains(err.Error(), ": velox: ") {
		t.Errorf("doubled prefix in %v", err)
	}
}

// A ForeignKey function that panics poisons nothing: every check reports it.
type panicFK struct {
	ID    int64
	Lines Many[keyedLine]
}

func (x *panicFK) Schema(s *Schema) {
	s.Many(&x.Lines).ForeignKey(func(*keyedLine) *int64 { panic("boom") })
}

var panicFKMeta MetaOnce[panicFK]

func (*panicFK) VeloxMeta() *Meta[panicFK] {
	return panicFKMeta.Get(func() Shape[panicFK] {
		return shapeOf[panicFK]([]string{"ID"}, RelationTo[keyedLine]("Lines"))
	})
}

func TestForeignKeyPanicIsReportedEveryTime(t *testing.T) {
	for range 2 {
		wantErr(t, CheckEntity[panicFK](), "boom")
	}
}

type strayFK struct {
	ID    int64
	Lines Many[keyedLine]
	Other Many[keyedLine]
}

func (x *strayFK) Schema(s *Schema) {
	s.Many(&x.Lines).ForeignKey(func(*keyedLine) *int64 { return new(int64) })
	s.Many(&x.Other).ForeignKey(func(*keyedLine) *int64 { return nil })
}

func TestForeignKeyMustReturnAField(t *testing.T) {
	_, err := build[strayFK]([]string{"ID"}, RelationTo[keyedLine]("Lines"), RelationTo[keyedLine]("Other"))
	wantErr(t, err, "Lines: ForeignKey's function must return a field of velox.keyedLine")
	wantErr(t, err, "Other: ForeignKey's function must return a field of velox.keyedLine")
}

type twice struct {
	ID      int64
	OwnerID *int64
	Lines   Many[keyedLine]
	A       One[keyedLine]
	B       One[keyedLine]
	C       One[keyedLine]
}

func (x *twice) Schema(s *Schema) {
	fk := func(l *keyedLine) *int64 { return &l.KeyedID }
	s.Many(&x.Lines).ForeignKey(fk).ForeignKey(fk)
	s.One(&x.A).References(&x.OwnerID).References(&x.OwnerID)
	s.One(&x.B).References(&x.OwnerID)
	s.One(&x.B).References(&x.OwnerID)
	s.One(&x.C).References(&x.OwnerID).ForeignKey(fk)
}

func TestRelationKeyDeclaredTwice(t *testing.T) {
	kl := RelationTo[keyedLine]
	_, err := build[twice]([]string{"ID", "OwnerID"}, kl("Lines"), kl("A"), kl("B"), kl("C"))
	for _, want := range []string{
		"twice.Lines: relation Lines's key is declared twice",
		"twice.A: relation A's key is declared twice",
		"twice.B: relation B's key is declared twice",
		"twice.C: relation C declares both ForeignKey and References",
	} {
		wantErr(t, err, want)
	}
}

func TestNilTargetIsAnError(t *testing.T) {
	wantErr(t, resolveAll[lonely]([]string{"ID"}, Relation{name: "Lines"}), "Lines: it has no target in the generated code")
}

type strangePtr struct {
	ID    int64
	Lines Many[keyedLine]
}

func (x *strangePtr) Schema(s *Schema) { s.Many(new(Many[keyedLine])) }

func TestRelationPointerMustBeAField(t *testing.T) {
	_, err := build[strangePtr]([]string{"ID"}, RelationTo[keyedLine]("Lines"))
	wantErr(t, err, "a pointer given to Many or One is not a relation field of the entity")
}

type compOwner struct {
	A, B  int64
	Lines Many[keyedLine]
}

func (x *compOwner) Schema(s *Schema) {
	s.PrimaryKey(&x.A, &x.B)
	s.Many(&x.Lines).ForeignKey(func(l *keyedLine) *int64 { return &l.KeyedID })
}

type compTarget struct {
	A, B   int64
	Parent One[keyedLine]
	OwnID  int64
}

func (x *compTarget) Schema(s *Schema) { s.PrimaryKey(&x.A, &x.B) }

var compTargetMeta MetaOnce[compTarget]

func (*compTarget) VeloxMeta() *Meta[compTarget] {
	return compTargetMeta.Get(func() Shape[compTarget] {
		return shapeOf[compTarget]([]string{"A", "B", "OwnID"}, RelationTo[keyedLine]("Parent"))
	})
}

type compPointing struct {
	ID    int64
	Owner One[compTarget]
	Ref   int64
}

func (x *compPointing) Schema(s *Schema) { s.One(&x.Owner).References(&x.Ref) }

func TestCompositeKeysAreRefused(t *testing.T) {
	// The owner's key is composite, and the target holds the foreign key.
	wantErr(t, resolveAll[compOwner]([]string{"A", "B"}, RelationTo[keyedLine]("Lines")),
		"velox.compOwner has a composite primary key; a relation needs a single-column key")
	// The target's key is composite and the owner holds the reference.
	wantErr(t, resolveAll[compPointing]([]string{"ID", "Ref"}, RelationTo[compTarget]("Owner")),
		"velox.compTarget has a composite primary key; a relation needs a single-column key")
}

func TestNonComparableKeyIsRefused(t *testing.T) {
	info, err := loadDesc(blobOwnerDesc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info, targetOf[blobLine]())
	wantErr(t, err, "Lines: a key of type []uint8 cannot be compared")
}

type refThenFK struct {
	ID    int64
	Ref   int64
	Owner One[keyedLine]
}

func (x *refThenFK) Schema(s *Schema) {
	s.One(&x.Owner).References(&x.Ref).ForeignKey(func(l *keyedLine) *int64 { return &l.KeyedID })
}

func TestReferencesThenForeignKeyIsRefused(t *testing.T) {
	_, err := build[refThenFK]([]string{"ID", "Ref"}, RelationTo[keyedLine]("Owner"))
	wantErr(t, err, "relation Owner declares both ForeignKey and References")
}

// pref.Parent finds ParentID on pref, though prefTarget also has PrefID.
type pref struct {
	ID       int64
	ParentID int64
	Parent   One[prefTarget]
}

func (x *pref) Schema(*Schema) {}

type prefTarget struct {
	ID     int64
	PrefID int64
}

func (x *prefTarget) Schema(*Schema) {}

var prefTargetMeta MetaOnce[prefTarget]

func (*prefTarget) VeloxMeta() *Meta[prefTarget] {
	return prefTargetMeta.Get(func() Shape[prefTarget] { return shapeOf[prefTarget]([]string{"ID", "PrefID"}) })
}

func TestOnePrefersTheOwnersKey(t *testing.T) {
	info, err := build[pref]([]string{"ID", "ParentID"}, RelationTo[prefTarget]("Parent"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := info.rels[0].resolve(info, useTarget{})
	if err != nil || info.Columns[l.ownerCol].Name != "parent_id" || l.target.Columns[l.targetCol].Name != "id" {
		t.Errorf("%+v, %v", l, err)
	}
}

// mixKey is embedded in mixTarget: its field is a column of mixTarget.
type mixKey struct{ OwnerRef int64 }

type mixTarget struct {
	mixKey
	ID int64
}

func (x *mixTarget) Schema(*Schema) {}

var mixTargetMeta MetaOnce[mixTarget]

func (*mixTarget) VeloxMeta() *Meta[mixTarget] {
	return mixTargetMeta.Get(func() Shape[mixTarget] { return shapeOf[mixTarget]([]string{"OwnerRef", "ID"}) })
}

type mixOwner struct {
	ID    int64
	Items Many[mixTarget]
}

func (x *mixOwner) Schema(s *Schema) {
	s.Many(&x.Items).ForeignKey(func(t *mixTarget) *int64 { return &t.OwnerRef })
}

func TestForeignKeyInEmbeddedStruct(t *testing.T) {
	info, err := build[mixOwner]([]string{"ID"}, RelationTo[mixTarget]("Items"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := info.rels[0].resolve(info, useTarget{})
	if err != nil || l.target.Columns[l.targetCol].Name != "owner_ref" || info.Columns[l.ownerCol].Name != "id" {
		t.Errorf("%+v, %v", l, err)
	}
}

func TestInvalidTargetIsNamed(t *testing.T) {
	// brokenTarget's Desc has no Addr, so it cannot build.
	info, err := loadDesc(invalidHolderDesc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info, targetOf[brokenTarget]())
	wantErr(t, err, "T: its target github.com/syssam/velox2.brokenTarget is invalid: velox.brokenTarget: the generated metadata has no Addr; run go tool velox-gen")
}

func TestTypeNameUpperCasesTheFirstRune(t *testing.T) {
	for in, want := range map[string]string{"billing.Invoice": "Invoice", "company": "Company", "pkg.élan": "Élan"} {
		if got := typeName(in); got != want {
			t.Errorf("typeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEdgeToNamesNoTarget: the Desc path's relations carry no reference to
// their target's metadata, so linking an entity does not link the entities it
// relates to, and through them the whole connected model. The use site names
// the target (Rel). EdgeThrough keeps its join entity's, which has no
// relations of its own.
func TestEdgeToNamesNoTarget(t *testing.T) {
	for _, r := range []Relation{EdgeTo[employee]("Staff", 0, 1), EdgeThrough[company, employee]("Through", 0, 0, 0, 0)} {
		if r.target != nil {
			t.Errorf("%s has a target", r.name)
		}
	}
	if r := EdgeThrough[company, employee]("Through", 0, 0, 0, 0); r.through == nil || r.through.join == nil {
		t.Error("EdgeThrough lost its join entity")
	}
	if RelationTo[employee]("Old").target == nil {
		t.Error("the old path's RelationTo lost its target")
	}
}
