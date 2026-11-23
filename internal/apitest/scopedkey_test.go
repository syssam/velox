package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/untyped"
)

type hideIOwnerKey struct{}

// hideIOwner is a context under which hideIOwnerFilter hides owner 1.
func hideIOwner(ctx context.Context) context.Context {
	return context.WithValue(ctx, hideIOwnerKey{}, true)
}

// hideIOwnerFilter hides IOwner 1 from reads under hideIOwner's context, and
// restricts nothing else.
func hideIOwnerFilter(ctx context.Context, _ *velox.DB, fe velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
	if fe.Name() != "IOwner" || op != velox.OpSelect || ctx.Value(hideIOwnerKey{}) == nil {
		return velox.Allow(), nil
	}
	return velox.FieldNEQ("id", 1), nil
}

// TestScopedKey: Table.ScopedKey is its condition alone where the target
// gets no row condition on the statement, with no EXISTS, and with the
// edge's Has where it gets one, from a WithFilter filter, an interceptor or
// a Read policy: IS NULL is the Has negated, IS NOT NULL the Has, and any
// other condition it AND the Has. The choice is the statement's, so one
// condition, run on one DB under contexts that differ, gives each its own
// SQL, and the render cache, on the plain path and the hooked one, never
// hands one context's SQL to the other.
func TestScopedKey(t *testing.T) {
	notes, owners := untyped.TableOf[fixture.INote](), untyped.TableOf[fixture.IOwner]()
	noteOwner := int(fixture.INotes.Owner.I)
	ownerPinned := int(fixture.IOwners.Pinned.I)
	noteKey := func(op untyped.PredOp, v any) velox.Predicate {
		return notes.ScopedKey(notes.Pred(int(fixture.INotes.OwnerID.I), op, v), noteOwner, owners)
	}
	pinnedKey := func(op untyped.PredOp) velox.Predicate {
		return owners.ScopedKey(owners.Pred(int(fixture.IOwners.PinnedID.I), op, nil), ownerPinned, notes)
	}
	plain := context.Background()
	// INote's interceptor restricts nothing under FilterSkip, and with the
	// policy lifted INote gets no row condition at all.
	open := fixture.WithFilterMode(plain, fixture.FilterSkip)
	type run struct {
		ctx    context.Context
		bypass bool // run on db.WithoutPolicy
		want   []int64
		exists bool
	}
	cases := []struct {
		name  string
		table untyped.Table
		p     velox.Predicate
		runs  []run
	}{
		// Notes 1 and 4 are visible, of owners 1 and 2.
		{"owner EQ 1", notes, noteKey(untyped.PredEQ, int64(1)), []run{
			{plain, false, []int64{1}, false}, {hideIOwner(plain), false, []int64{}, true},
		}},
		{"owner NotIn 2", notes, noteKey(untyped.PredNotIn, []any{int64(2)}), []run{
			{plain, false, []int64{1}, false}, {hideIOwner(plain), false, []int64{}, true},
		}},
		{"owner In 1, 2", notes, noteKey(untyped.PredIn, []any{int64(1), int64(2)}), []run{
			{plain, false, []int64{1, 4}, false}, {hideIOwner(plain), false, []int64{4}, true},
		}},
		// Owner 1 pins note 2, which the interceptor hides; owner 2 pins
		// note 1.
		{"pinned IsNull", owners, pinnedKey(untyped.PredIsNull), []run{
			{open, true, []int64{}, false}, {plain, false, []int64{1}, true}, {open, false, []int64{}, true},
		}},
		{"pinned NotNull", owners, pinnedKey(untyped.PredNotNull), []run{
			{open, true, []int64{1, 2}, false}, {plain, false, []int64{2}, true}, {open, false, []int64{1, 2}, true},
		}},
	}
	var mu sync.Mutex
	var ran []string
	hooked := interceptorDB(t, velox.WithFilter(hideIOwnerFilter), velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		ran = append(ran, st.SQL)
		mu.Unlock()
		return next(ctx)
	}))
	bare := interceptorDB(t, velox.WithFilter(hideIOwnerFilter))
	ids := func(db *velox.DB, tbl untyped.Table, ctx context.Context, p velox.Predicate) []int64 {
		t.Helper()
		rows, err := untyped.Query(db, tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for i := range rows.Len() {
			out = append(out, *tbl.Field(rows.At(i), 0).(*int64))
		}
		return out
	}
	for _, c := range cases {
		// Every run twice, in turn: the second finds its shape cached.
		for i, r := range append(slices.Clone(c.runs), c.runs...) {
			mu.Lock()
			ran = nil
			mu.Unlock()
			for _, db := range []*velox.DB{hooked, bare} {
				if r.bypass {
					db = db.WithoutPolicy("test")
				}
				if got := ids(db, c.table, r.ctx, c.p); !slices.Equal(got, r.want) {
					t.Errorf("%s, run %d: %v, want %v", c.name, i, got, r.want)
				}
			}
			mu.Lock()
			sql := slices.Clone(ran)
			mu.Unlock()
			if len(sql) != 1 {
				t.Fatalf("%s, run %d: %q", c.name, i, sql)
			}
			if got := strings.Contains(sql[0], "EXISTS"); got != r.exists {
				t.Errorf("%s, run %d: EXISTS %v in %s", c.name, i, got, sql[0])
			}
		}
	}
	// A condition that fails, or a relation that does not resolve, fails
	// the statement.
	if _, err := untyped.Query(bare, notes).Where(notes.ScopedKey(notes.Pred(99, untyped.PredEQ, 1), noteOwner, owners)).Limit(1).All(plain); err == nil {
		t.Error("ScopedKey of an invalid condition: no error")
	}
	if _, err := untyped.Query(bare, notes).Where(notes.ScopedKey(notes.Pred(int(fixture.INotes.OwnerID.I), untyped.PredEQ, int64(1)), 7, owners)).Limit(1).All(plain); err == nil || !strings.Contains(err.Error(), "relation 7") {
		t.Errorf("ScopedKey of no relation: %v", err)
	}
}

// TestScopedKeyDenied: a target whose Read policy denies the statement, or
// fails, fails a statement that holds a ScopedKey to it, on the plain path
// and the hooked one, as it fails any EXISTS on the target.
func TestScopedKeyDenied(t *testing.T) {
	notes, owners := untyped.TableOf[fixture.INote](), untyped.TableOf[fixture.IOwner]()
	pinned := owners.ScopedKey(owners.Pred(int(fixture.IOwners.PinnedID.I), untyped.PredNotNull, nil), int(fixture.IOwners.Pinned.I), notes)
	plain := context.Background()
	hooked := interceptorDB(t, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	}))
	bare := interceptorDB(t)
	for _, db := range []*velox.DB{hooked, bare} {
		_, err := untyped.Query(db, owners).Where(pinned).Limit(5).All(fixture.WithPolicyMode(plain, fixture.PolicyDeny))
		var pe *velox.PolicyError
		if !errors.Is(err, velox.ErrDenied) || !errors.As(err, &pe) || !strings.Contains(err.Error(), "INote") {
			t.Errorf("a denied target: %v", err)
		}
		_, err = untyped.Query(db, owners).Where(pinned).Limit(5).All(fixture.WithPolicyMode(plain, fixture.PolicyFail))
		if !errors.Is(err, fixture.ErrPolicy) || !strings.Contains(err.Error(), "INote") {
			t.Errorf("a failing target policy: %v", err)
		}
		// The default mode reads, so the denial is the mode's.
		if _, err := untyped.Query(db, owners).Where(pinned).Limit(5).All(plain); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScopedKeyChecks: ScopedKey takes a comparison on the foreign key of a
// to-one relation whose key is the owner's; anything else fails the
// statement, never reads.
func TestScopedKeyChecks(t *testing.T) {
	notes, owners := untyped.TableOf[fixture.INote](), untyped.TableOf[fixture.IOwner]()
	db := interceptorDB(t)
	noteOwner, ownerPinned := int(fixture.INotes.Owner.I), int(fixture.IOwners.Pinned.I)
	cases := []struct {
		name  string
		table untyped.Table
		p     velox.Predicate
		want  string
	}{
		{"another column", notes, notes.ScopedKey(notes.Pred(int(fixture.INotes.ID.I), untyped.PredEQ, int64(1)), noteOwner, owners), "foreign key OwnerID"},
		{"not a comparison", notes, notes.ScopedKey(velox.Or(notes.Pred(int(fixture.INotes.OwnerID.I), untyped.PredEQ, int64(1))), noteOwner, owners), "foreign key OwnerID"},
		{"a text condition", owners, owners.ScopedKey(owners.Pred(int(fixture.IOwners.Name.I), untyped.PredHasPrefix, "o"), ownerPinned, notes), "foreign key PinnedID"},
		{"another table's column", owners, owners.ScopedKey(notes.Pred(int(fixture.INotes.OwnerID.I), untyped.PredEQ, int64(2)), ownerPinned, notes), "foreign key PinnedID"},
		{"a to-many relation", owners, owners.ScopedKey(owners.Pred(int(fixture.IOwners.PinnedID.I), untyped.PredNotNull, nil), int(fixture.IOwners.Notes.I), notes), "not a to-one"},
		{"a many-to-many relation", owners, owners.ScopedKey(owners.Pred(int(fixture.IOwners.PinnedID.I), untyped.PredNotNull, nil), int(fixture.IOwners.Tagged.I), notes), "not a to-one"},
	}
	for _, c := range cases {
		_, err := untyped.Query(db, c.table).Where(c.p).Limit(5).All(context.Background())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// skPerson has one skCard, whose key is on the card: the card's person_code
// holds the person's code, a column outside both primary keys. It is an
// inverse to-one relation, which no ScopedKey takes.
type skPerson struct {
	ID   int64
	Code int64
	Card velox.One[skCard]
}

var skPersonMeta velox.MetaOnce[skPerson]

func (*skPerson) VeloxMeta() *velox.Meta[skPerson] { return skPersonMeta.Load(skPersonDescriptor) }

func skPersonDescriptor() velox.Descriptor[skPerson] {
	return velox.Descriptor[skPerson]{
		Table:        "sk_persons",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Code", Name: "code"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[skCard]("card", 1, 1)},
		FieldPtr:     func(x *skPerson, i int) any { return [...]any{&x.ID, &x.Code}[i] },
		EdgeFieldPtr: func(x *skPerson, i int) any { return &x.Card },
	}
}

type skCard struct {
	ID         int64
	PersonCode int64
}

var skCardMeta velox.MetaOnce[skCard]

func (*skCard) VeloxMeta() *velox.Meta[skCard] { return skCardMeta.Load(skCardDescriptor) }

func skCardDescriptor() velox.Descriptor[skCard] {
	return velox.Descriptor[skCard]{
		Table:      "sk_cards",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PersonCode", Name: "person_code"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *skCard, i int) any { return [...]any{&x.ID, &x.PersonCode}[i] },
	}
}

// skLink is keyed by its two columns, the first of which is the key of its
// to-one relation to skPerson: a foreign key in the owner's primary key,
// which no ScopedKey takes.
type skLink struct {
	PersonID int64
	Slot     int64
	Person   velox.One[skPerson]
}

var skLinkMeta velox.MetaOnce[skLink]

func (*skLink) VeloxMeta() *velox.Meta[skLink] { return skLinkMeta.Load(skLinkDescriptor) }

func skLinkDescriptor() velox.Descriptor[skLink] {
	return velox.Descriptor[skLink]{
		Table:        "sk_links",
		Columns:      []velox.ColumnDescriptor{{Field: "PersonID", Name: "person_id"}, {Field: "Slot", Name: "slot"}},
		PrimaryKey:   []int{0, 1},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[skPerson]("person", 0, 0)},
		FieldPtr:     func(x *skLink, i int) any { return [...]any{&x.PersonID, &x.Slot}[i] },
		EdgeFieldPtr: func(x *skLink, i int) any { return &x.Person },
	}
}

// TestScopedKeyRelationShapes: ScopedKey refuses a to-one relation whose key
// is on the target, and one whose foreign key is in the owner's primary key,
// and fails the statement before it is sent.
func TestScopedKeyRelationShapes(t *testing.T) {
	persons, cards, links := untyped.TableOf[skPerson](), untyped.TableOf[skCard](), untyped.TableOf[skLink]()
	db := interceptorDB(t)
	cases := []struct {
		name  string
		table untyped.Table
		p     velox.Predicate
	}{
		{"the key on the target", persons, persons.ScopedKey(persons.Pred(1, untyped.PredEQ, int64(1)), 0, cards)},
		{"the key in the owner's primary key", links, links.ScopedKey(links.Pred(0, untyped.PredEQ, int64(1)), 0, persons)},
	}
	for _, c := range cases {
		_, err := untyped.Query(db, c.table).Where(c.p).Limit(5).All(context.Background())
		if err == nil || !strings.Contains(err.Error(), "not a to-one relation whose foreign key is") {
			t.Errorf("%s: %v, want the to-one refusal", c.name, err)
		}
	}
}
