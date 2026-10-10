package orm

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

type level string

type row struct {
	ID    int64
	Name  string
	Note  *string
	Level level
	Seen  *time.Time
	Tags  []string
	Score float64
	owner *int
}

var rowMeta = Meta{
	Name:    "Row",
	Table:   "rows",
	Columns: []string{"id", "name", "note", "level", "seen", "tags", "score", "owner_id"},
	New:     func() any { return new(row) },
	Ptr: func(v any, i int) any {
		e := v.(*row)
		switch i {
		case 0:
			return &e.ID
		case 1:
			return &e.Name
		case 2:
			return &e.Note
		case 3:
			return &e.Level
		case 4:
			return &e.Seen
		case 5:
			return &e.Tags
		case 6:
			return &e.Score
		case 7:
			return &e.owner
		}
		return nil
	},
	Fields: []*FieldMeta{
		{Name: "id", Column: "id", Type: field.TypeInt64, Optional: true},
		{Name: "name", Column: "name", Type: field.TypeString},
		{Name: "note", Column: "note", Type: field.TypeString, Nillable: true, Optional: true},
		{Name: "level", Column: "level", Type: field.TypeEnum, Optional: true},
		{Name: "seen", Column: "seen", Type: field.TypeTime, Nillable: true, Optional: true},
		{Name: "tags", Column: "tags", Type: field.TypeJSON, Optional: true},
		{Name: "score", Column: "score", Type: field.TypeFloat64, Optional: true},
	},
}

func TestMetaAssignAndGet(t *testing.T) {
	now := time.Now()
	e := new(row)
	for col, v := range map[string]any{
		"id":       7,             // an int for an int64 field is converted
		"name":     "a",           // direct
		"note":     "n",           // pointer field
		"level":    "high",        // a string for a named string type
		"seen":     now,           // pointer to a struct
		"tags":     []string{"x"}, // through reflection
		"score":    float32(1.5),  // converted
		"owner_id": 3,             // a foreign key without a schema field
	} {
		if err := rowMeta.assign(e, col, v); err != nil {
			t.Fatalf("assign %s: %v", col, err)
		}
	}
	if e.ID != 7 || e.Name != "a" || e.Note == nil || *e.Note != "n" || e.Level != "high" ||
		e.Seen == nil || !e.Seen.Equal(now) || len(e.Tags) != 1 || e.Score != 1.5 || e.owner == nil || *e.owner != 3 {
		t.Fatalf("assigned row = %+v", e)
	}
	if got := rowMeta.get(e, "id"); got != int64(7) {
		t.Errorf("get id = %#v", got)
	}
	if got := rowMeta.get(e, "level"); got != level("high") {
		t.Errorf("get level = %#v", got)
	}
	if got := rowMeta.get(e, "note"); got != "n" {
		t.Errorf("get note = %#v", got)
	}
	if got := rowMeta.get(e, "owner_id"); got != 3 {
		t.Errorf("get owner_id = %#v", got)
	}
	if got := rowMeta.get(new(row), "note"); got != nil {
		t.Errorf("get of a nil pointer field = %#v, want nil", got)
	}
	if got := rowMeta.get(new(row), "seen"); got != nil {
		t.Errorf("get of a nil *time.Time field = %#v, want nil", got)
	}
	if err := rowMeta.assign(e, "name", 3.5); err == nil {
		t.Error("assigning a float to a string field did not fail")
	}
	if err := rowMeta.assign(e, "nope", 1); err != nil {
		t.Errorf("assigning an unknown column: %v", err)
	}
}

func TestMetaDestKinds(t *testing.T) {
	e := new(row)
	want := map[string]any{
		"id":       (*int64)(nil),   // the id is never NULL
		"name":     (*string)(nil),  // required
		"note":     (**string)(nil), // nillable: database/sql handles NULL
		"level":    &nullable{},     // optional, not nillable, no fast path
		"seen":     (**time.Time)(nil),
		"tags":     &jsonDest{},
		"score":    nullFloat64{},
		"owner_id": (**int)(nil),
	}
	for col, w := range want {
		got := rowMeta.dest(e, rowMeta.col(col), new(slot))
		if gt, wt := typeName(got), typeName(w); gt != wt {
			t.Errorf("dest %s is a %s, want %s", col, gt, wt)
		}
	}
	// A NULL in an optional non-pointer column leaves the zero value.
	if err := rowMeta.dest(e, rowMeta.col("score"), new(slot)).(nullFloat64).Scan(nil); err != nil || e.Score != 0 {
		t.Errorf("scanning NULL into score: %v, %v", err, e.Score)
	}
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }

// A slot stitches as soon as it has both the entity and the schema's
// function, in whichever order package initialization gives them, and once.
func TestSlot(t *testing.T) {
	stitch := func(calls *int) func(*Meta) {
		return func(m *Meta) { *calls++; m.Hooks = make([]velox.Hook, 1) }
	}
	for name, order := range map[string]func(s *Slot, m *Meta, fn func(*Meta)){
		"entity first": func(s *Slot, m *Meta, fn func(*Meta)) { s.Bind(m); s.Stitch(fn) },
		"schema first": func(s *Slot, m *Meta, fn func(*Meta)) { s.Stitch(fn); s.Bind(m) },
		"bound twice":  func(s *Slot, m *Meta, fn func(*Meta)) { s.Bind(m); s.Stitch(fn); s.Bind(m) },
	} {
		var s Slot
		var calls int
		m := &Meta{Name: "T", Runtime: true, Package: "example.com/db"}
		order(&s, m, stitch(&calls))
		if calls != 1 || len(m.Hooks) != 1 {
			t.Errorf("%s: the stitch function ran %d times", name, calls)
		}
		if err := m.ready(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Half a slot stitches nothing, and the entity refuses to run.
	var calls int
	var unbound, unstitched Slot
	unbound.Stitch(stitch(&calls))
	m := &Meta{Name: "T", Runtime: true, Package: "example.com/db"}
	unstitched.Bind(m)
	if calls != 0 {
		t.Errorf("a slot without an entity ran its function")
	}
	if err := m.ready(); err == nil || !strings.Contains(err.Error(), `import _ "example.com/db/schemainit"`) {
		t.Errorf("an entity without its schema's code runs: %v", err)
	}
	// An entity whose schema has no code needs no slot.
	if err := (&Meta{Name: "Plain"}).ready(); err != nil {
		t.Error(err)
	}
}
