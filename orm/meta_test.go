package orm

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// A slot that has both the entity and the schema's function, in whichever
// order package initialization gives them, stitches when the entity is first
// used, and once: not when the program starts, where every entity of a large
// schema would pay for the few a program uses.
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
		if calls != 0 {
			t.Errorf("%s: the stitch function ran before the entity was used", name)
		}
		// Whatever is asked of the entity first stitches it.
		if got := m.hooks(); calls != 1 || len(got) != 1 {
			t.Errorf("%s: the hooks of an entity used for the first time: %d, after %d runs of the stitch function", name, len(got), calls)
		}
		if err := m.ready(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		_ = m.policy()
		if calls != 1 {
			t.Errorf("%s: the stitch function ran %d times", name, calls)
		}
	}

	// Used by many at once for the first time, an entity is stitched once,
	// and nobody sees it half done.
	{
		var s Slot
		var calls atomic.Int32
		m := &Meta{Name: "T", Runtime: true, Package: "example.com/db"}
		s.Bind(m)
		s.Stitch(func(m *Meta) {
			calls.Add(1)
			time.Sleep(5 * time.Millisecond)
			m.Hooks = make([]velox.Hook, 1)
		})
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got := m.hooks(); len(got) != 1 {
					t.Errorf("an entity being stitched was seen with %d hooks", len(got))
				}
			}()
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Errorf("stitched %d times", calls.Load())
		}
	}

	// StitchAll does it for every entity now.
	{
		var s Slot
		var calls int
		m := &Meta{Name: "T", Runtime: true, Package: "example.com/db"}
		s.Bind(m)
		s.Stitch(stitch(&calls))
		StitchAll()
		StitchAll()
		if calls != 1 {
			t.Errorf("StitchAll ran the stitch function %d times", calls)
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

// What schemainit fills a field in with: the schema's default and
// validators, as the runtime calls them.
func TestStitchHelpers(t *testing.T) {
	if got := DefaultValue(7)(); got != 7 {
		t.Errorf("DefaultValue: %v", got)
	}
	n := 0
	def := DefaultFunc[int](func() int { n++; return n })
	if def() != 1 || def() != 2 {
		t.Errorf("DefaultFunc does not call the schema's function every time")
	}
	var seen []string
	check := Validators[string]([]any{
		func(v string) error { seen = append(seen, "first:"+v); return nil },
		func(v string) error {
			if v == "" {
				return fmt.Errorf("empty")
			}
			return nil
		},
		func(v string) error { seen = append(seen, "third:"+v); return nil },
	})
	if err := check("a"); err != nil || strings.Join(seen, " ") != "first:a third:a" {
		t.Errorf("Validators: %v, ran %v", err, seen)
	}
	// The first error is the field's, and the rest do not run.
	seen = nil
	if err := check(""); err == nil || err.Error() != "empty" || strings.Join(seen, " ") != "first:" {
		t.Errorf("Validators on a value the second refuses: %v, ran %v", err, seen)
	}
	// A value of another type than the field's is an error, not a panic.
	if err := check(7); err == nil {
		t.Errorf("Validators took an int for a string field")
	}
	// A field of a named type is validated as its basic type.
	type code string
	named := ValidatorsOf([]any{func(v string) error {
		if len(v) != 2 {
			return fmt.Errorf("want two letters")
		}
		return nil
	}}, func(v code) string { return string(v) })
	if err := named(code("hk")); err != nil {
		t.Error(err)
	}
	if err := named(code("hkg")); err == nil {
		t.Errorf("ValidatorsOf did not validate")
	}
	// The schema holding another type than the generated code was written
	// for says so when the entity is stitched.
	for name, fn := range map[string]func(){
		"a default of another type":   func() { DefaultFunc[int](func() string { return "" }) },
		"a validator of another type": func() { Validators[int]([]any{func(string) error { return nil }}) },
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "generate the code again") {
					t.Errorf("%s: %v", name, r)
				}
			}()
			fn()
		}()
	}
}

// What schemainit writes of an entity is a row per field; StitchFields
// reads each field's descriptor and gives the field what its row names.
func TestStitchFields(t *testing.T) {
	type code string
	type row struct {
		Name string
		Code code
		Seen int
	}
	newMeta := func() *Meta {
		return &Meta{
			Name:    "T",
			Columns: []string{"name", "code", "seen"},
			New:     func() any { return new(row) },
			Ptr: func(e any, i int) any {
				r := e.(*row)
				return []any{&r.Name, &r.Code, &r.Seen}[i]
			},
			Fields: []*FieldMeta{
				{Name: "name", Column: "name", Type: field.TypeString},
				{Name: "code", Column: "code", Type: field.TypeString},
				{Name: "seen", Column: "seen", Type: field.TypeInt},
			},
		}
	}
	n := 0
	fields := []velox.Field{
		field.String("skipped"),
		field.String("name").Default("ann").NotEmpty(),
		field.String("code").Default("hk").MaxLen(2),
		field.Int("seen").DefaultFunc(func() int { n++; return n }).UpdateDefault(func() int { return -1 }),
	}
	m := newMeta()
	StitchFields(m, fields, []FieldCode{
		{Index: 1, Column: "name", Default: DefaultOf[string], Validate: Validators[string]},
		{Index: 2, Column: "code", Default: DefaultString[code], Validate: ValidatorsString[code]},
		{Index: 3, Column: "seen", Default: DefaultFunc[int], UpdateDefault: DefaultFunc[int]},
	})
	name, c, seen := m.Field("name"), m.Field("code"), m.Field("seen")
	if got := name.Default(); got != "ann" {
		t.Errorf("default of name: %#v", got)
	}
	if name.Validate("x") != nil || name.Validate("") == nil {
		t.Error("the validator of name was not stitched")
	}
	if name.UpdateDefault != nil {
		t.Error("name has an update default no row gave it")
	}
	// The default of a named string type is of that type, and its
	// validators see a string.
	if got := c.Default(); got != code("hk") {
		t.Errorf("default of code: %#v", got)
	}
	if c.Validate(code("hk")) != nil || c.Validate(code("hkg")) == nil {
		t.Error("the validator of code was not stitched")
	}
	if seen.Default() != 1 || seen.Default() != 2 || seen.UpdateDefault() != -1 {
		t.Error("the functions of seen are not the schema's")
	}
	if seen.Validate != nil {
		t.Error("seen has a validator no row gave it")
	}

	// A schema that is not the one the code was generated for is said so,
	// when the entity is stitched: a field that is no longer there, a column
	// the entity does not have, a default of another type.
	for what, codes := range map[string][]FieldCode{
		"a field past the end":                           {{Index: 4, Column: "name"}},
		"a column of no field":                           {{Index: 1, Column: "nickname"}},
		"a default of another type":                      {{Index: 1, Column: "name", Default: DefaultOf[int]}},
		"a string default for a field that has a number": {{Index: 3, Column: "seen", Default: DefaultString[code]}},
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "generate the code again") {
					t.Errorf("%s: %v", what, r)
				}
			}()
			StitchFields(newMeta(), fields, codes)
		}()
	}
}
