package velox

import (
	"errors"
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

func TestHolderLayout(t *testing.T) {
	var o One[int]
	var m Many[int]
	var h holder
	if unsafe.Sizeof(o) != unsafe.Sizeof(h) || unsafe.Sizeof(m) != unsafe.Sizeof(h) ||
		unsafe.Offsetof(o.loaded) != unsafe.Offsetof(h.loaded) || unsafe.Offsetof(m.loaded) != unsafe.Offsetof(h.loaded) {
		t.Fatal("One and Many must have holder's layout")
	}
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

// TestResolveWithoutATargetIsAnError: a relation resolved without the use
// site's target (a hand-written call that does not go through a Rel handle) is
// refused, not guessed at.
func TestResolveWithoutATargetIsAnError(t *testing.T) {
	info, err := loadDesc(keyedDesc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info, useTarget{})
	wantErr(t, err, "Lines: it has no target in the generated code")
}

func TestNonComparableKeyIsRefused(t *testing.T) {
	info, err := loadDesc(blobOwnerDesc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info, targetOf[blobLine]())
	wantErr(t, err, "Lines: a key of type []uint8 cannot be compared")
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

// TestEdgeThroughKeepsItsJoin: EdgeThrough names its join entity, which has
// no relations of its own. EdgeTo and EdgeThrough name no target: a Relation
// has no field for one, so linking an entity does not link the entities it
// relates to and, through them, the whole connected model (the use site names
// the target: Rel; see TestServiceLinksOnlyItsEntities in bench/scale).
func TestEdgeThroughKeepsItsJoin(t *testing.T) {
	if r := EdgeThrough[company, employee]("Through", 0, 0, 0, 0); r.through == nil || r.through.join == nil {
		t.Error("EdgeThrough lost its join entity")
	}
}
