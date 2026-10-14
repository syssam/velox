package velox

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// The relation fixtures: a company has employees, each with a manager.
type company struct {
	ID    int64
	Staff Many[employee]
}

func (x *company) Schema(*Schema) {}

var companyMeta MetaOnce[company]

func (*company) VeloxMeta() *Meta[company] {
	return companyMeta.Get(func() Shape[company] {
		return Shape[company]{Columns: []string{"ID"}, Relations: []Relation{RelationTo[employee]("Staff")},
			Addr: func(x *company, i int) any { return &x.ID }}
	})
}

type employee struct {
	ID        int64
	CompanyID int64
	BossID    *int64
	Boss      One[employee]
	Company   One[company]
}

func (x *employee) Schema(s *Schema) { s.One(&x.Boss).References(&x.BossID) }

var employeeMeta MetaOnce[employee]

func (*employee) VeloxMeta() *Meta[employee] {
	return employeeMeta.Get(func() Shape[employee] {
		return Shape[employee]{Columns: []string{"ID", "CompanyID", "BossID"},
			Relations: []Relation{RelationTo[employee]("Boss"), RelationTo[company]("Company")},
			Addr: func(x *employee, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.CompanyID
				case 2:
					return &x.BossID
				}
				return nil
			}}
	})
}

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
	}{
		{co, 0, em, "id", "company_id"}, // Many by convention: employee.CompanyID
		{em, 0, em, "boss_id", "id"},    // One, declared References
		{em, 1, co, "company_id", "id"}, // One by convention: employee.CompanyID
	} {
		l, err := c.owner.rels[c.rel].resolve(c.owner)
		if err != nil || l.target != c.target || c.owner.Columns[l.ownerCol].Name != c.ownerCol || l.target.Columns[l.targetCol].Name != c.col {
			t.Errorf("%s.%s: %+v, %v", c.owner.Name, c.owner.rels[c.rel].name, l, err)
		}
	}
}

type keyed struct {
	ID    string
	Lines Many[keyedLine]
}

func (x *keyed) Schema(*Schema) {}

type keyedLine struct {
	ID      int64
	KeyedID int64 // an int64 cannot refer to keyed's string key
}

func (x *keyedLine) Schema(*Schema) {}

var keyedLineMeta MetaOnce[keyedLine]

func (*keyedLine) VeloxMeta() *Meta[keyedLine] {
	return keyedLineMeta.Get(func() Shape[keyedLine] {
		return Shape[keyedLine]{Columns: []string{"ID", "KeyedID"}, Addr: func(*keyedLine, int) any { return nil }}
	})
}

// TestResolveRefusesMismatchedKeys is half of Review Focus 2.
func TestResolveRefusesMismatchedKeys(t *testing.T) {
	x := new(keyed)
	info, err := buildInfo(reflect.TypeFor[keyed](), unsafe.Pointer(x), any(x), []string{"ID"}, []Relation{RelationTo[keyedLine]("Lines")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info)
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
		_, err = info.rels[0].resolve(info)
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
