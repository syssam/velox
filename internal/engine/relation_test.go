package engine_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// TestRelationsResolveWithoutDeadlock is half of Review Focus 1.
func TestRelationsResolveWithoutDeadlock(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := velox.CheckEntity[company](); err != nil {
				t.Error(err)
			}
			if err := velox.CheckEntity[employee](); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	co, em := infoOf[company](), infoOf[employee]()
	for _, c := range []struct {
		owner         *velox.EntityInfo
		rel           int
		target        *velox.EntityInfo
		ownerCol, col string
		use           engine.RelTarget // the target as the use site names it
	}{
		{co, 0, em, "id", "company_id", targetOf[employee]()}, // Many: employee.CompanyID
		{em, 0, em, "boss_id", "id", targetOf[employee]()},    // One
		{em, 1, co, "company_id", "id", targetOf[company]()},  // One: employee.CompanyID
	} {
		l, err := engine.InfoRels(c.owner)[c.rel].Resolve(c.owner, c.use)
		if err != nil || l.Target != c.target || c.owner.Columns[l.OwnerCol].Name != c.ownerCol || l.Target.Columns[l.TargetCol].Name != c.col {
			t.Errorf("%s.%s: %+v, %v", c.owner.Name, engine.InfoRels(c.owner)[c.rel].Name, l, err)
		}
	}
}

// TestResolveRefusesMismatchedKeys is half of Review Focus 2.
func TestResolveRefusesMismatchedKeys(t *testing.T) {
	info, err := loadDescriptor(keyedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.InfoRels(info)[0].Resolve(info, targetOf[keyedLine]())
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "the key types differ: engine_test.keyed.ID is string and engine_test.keyedLine.KeyedID is int64") {
		t.Errorf("%v", err)
	}
}

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	var se *velox.SchemaError
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
	info, err := loadDescriptor(keyedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.InfoRels(info)[0].Resolve(info, engine.RelTarget{})
	wantErr(t, err, "Lines: it has no target in the generated code")
}

func TestNonComparableKeyIsRefused(t *testing.T) {
	info, err := loadDescriptor(blobOwnerDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.InfoRels(info)[0].Resolve(info, targetOf[blobLine]())
	wantErr(t, err, "Lines: a key of type []uint8 cannot be compared")
}

func TestInvalidTargetIsNamed(t *testing.T) {
	// brokenTarget's Descriptor has no Addr, so it cannot build.
	info, err := loadDescriptor(invalidHolderDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.InfoRels(info)[0].Resolve(info, targetOf[brokenTarget]())
	wantErr(t, err, "T: its target github.com/syssam/velox/internal/engine_test.brokenTarget is invalid: engine_test.brokenTarget: the generated metadata has no Addr; run go tool velox-gen")
}

// TestEdgeThroughKeepsItsJoin: EdgeThrough names its join entity, which has
// no relations of its own. EdgeTo and EdgeThrough name no target: an EdgeDescriptor
// has no field for one, so linking an entity does not link the entities it
// relates to and, through them, the whole connected model (the use site names
// the target: Rel; see TestServiceLinksOnlyItsEntities in bench/scale).
func TestEdgeThroughKeepsItsJoin(t *testing.T) {
	if r := velox.EdgeThrough[company, employee]("Through", 0, 0, 0, 0); engine.EdgeThroughOf(r) == nil || engine.EdgeThroughOf(r).Join == nil {
		t.Error("EdgeThrough lost its join entity")
	}
}

// TestCheckColumnsChecksTheJoin: an OpThrough's join column and its kids'
// columns index the join entity's columns, not the statement's.
func TestCheckColumnsChecksTheJoin(t *testing.T) {
	join := &velox.EntityInfo{Name: "velox.join", Table: "joins", Tenant: -1, Columns: []velox.ColumnInfo{{Name: "a"}, {Name: "b"}}}
	st := func(w int, kid int32) *velox.Statement {
		return &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpThrough, Col: 4, V: join, W: w,
			Kids: []engine.Node{{Op: engine.OpIn, Col: kid, V: []any{int64(1)}}}})}}
	}
	if err := engine.CheckColumns(st(1, 0)); err != nil {
		t.Errorf("valid: %v", err)
	}
	for _, c := range []struct {
		w   int
		kid int32
		bad int
	}{{2, 0, 2}, {0, 2, 2}, {-1, 0, -1}} {
		want := fmt.Sprintf("velox: velox.inv: a condition names column %d of velox.join, which has 2", c.bad)
		if err := engine.CheckColumns(st(c.w, c.kid)); err == nil || err.Error() != want {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

type other struct{ ID int64 }

func TestRelationHolders(t *testing.T) {
	if _, loaded := (velox.Many[other]{}).Get(); loaded {
		t.Error("a zero Many reports loaded")
	}
	if v, loaded := (velox.One[other]{}).Get(); loaded || v != nil {
		t.Error("a zero One reports loaded")
	}
	if !engine.IsRelation(reflect.TypeFor[velox.Many[other]]()) || engine.IsRelation(reflect.TypeFor[time.Time]()) {
		t.Error("isRelation")
	}
}
