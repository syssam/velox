package migrate

import (
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// TestCreateModes: on MySQL, Create refuses a session whose sql_mode would
// read a literal of its statements otherwise, as Up refuses a file's.
func TestCreateModes(t *testing.T) {
	t.Parallel()
	schema := func(def string) *Schema {
		return &Schema{Dialect: velox.MySQL, Tables: []Table{{Name: "t", PrimaryKey: []string{"id"}, PrimaryKeyName: "PRIMARY",
			Columns: []Column{{Name: "id", Type: "bigint", Identity: true}, {Name: "s", Type: "varchar(10)", Default: def}}}}}
	}
	for _, c := range []struct {
		mode, def, reason string
	}{
		{"NO_BACKSLASH_ESCAPES", `'a\\b'`, "Create's statements hold a backslash in a literal, which NO_BACKSLASH_ESCAPES would store as two; turn the mode off for migrating"},
		{"NO_BACKSLASH_ESCAPES", `'ab'`, ""},
		{"STRICT_ALL_TABLES", `'a\\b'`, ""},
	} {
		plan, err := NewPlan(&Schema{Dialect: velox.MySQL}, schema(c.def), PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s := &session{d: velox.MySQL, sqlMode: c.mode}
		err = s.createModes(plan)
		e, _ := errors.AsType[*Error](err)
		switch {
		case c.reason == "" && err != nil:
			t.Errorf("%s, %s: %v", c.mode, c.def, err)
		case c.reason != "" && (e == nil || e.Code != "session" || e.Reason != c.reason):
			t.Errorf("%s, %s: %v; want Code session, %q", c.mode, c.def, err, c.reason)
		}
	}
	// Another dialect has no such mode.
	s := &session{d: velox.SQLite, sqlMode: "NO_BACKSLASH_ESCAPES"}
	if err := s.createModes(&Plan{Scripts: []Script{{Statements: []Statement{{SQL: `SELECT 'a\b'`}}}}}); err != nil {
		t.Errorf("SQLite: %v", err)
	}
}
