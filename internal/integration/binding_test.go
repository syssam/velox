package integration

import (
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/internal/testbind"
	"github.com/syssam/velox/internal/testbind/bindtype"
	bindschema "github.com/syssam/velox/internal/testbind/schema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema/load"
)

var B = testbind.Binds

// bindTables is the planner's model of the testbind schema for d, loaded in
// process as velox-gen loads it.
func bindTables(t *testing.T, d velox.Dialect) *migrate.Schema {
	t.Helper()
	b, err := load.Marshal("github.com/syssam/velox/internal/testbind/schema", bindschema.Bind{})
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Errors) > 0 {
		t.Fatalf("load errors %+v", m.Errors)
	}
	g, err := codegen.Build(&m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := codegen.Tables(g, d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// bindDir is a migration directory in memory whose one plan makes s.
func bindDir(t *testing.T, s *migrate.Schema) fstest.MapFS {
	t.Helper()
	dir, err := migrate.NewDir(migrate.Config{Dialect: s.Dialect})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migrate.NewPlan(dir.Snapshot, s, migrate.PlanOptions{LockTimeout: dir.Config.LockTimeout})
	if err != nil {
		t.Fatal(err)
	}
	files, err := dir.Add("", plan, s)
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for p, b := range files {
		fsys[p] = &fstest.MapFile{Data: b}
	}
	return fsys
}

// bindTarget is tg made ready for the binds table: on SQLite a file database
// of its own, since Up runs on a connection of its own and each connection to
// :memory: is a new database; elsewhere tg's database, with the binds table,
// its enum types and the history table dropped before and after.
func bindTarget(t *testing.T, tg target, s *migrate.Schema) target {
	t.Helper()
	if tg.d == velox.SQLite {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bind.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		tg.db, tg.direct, tg.exec = velox.Open(velox.SQLite, db), db, sqlExec(db, velox.SQLite)
		return tg
	}
	drop := func() {
		tg.exec(t, "DROP TABLE IF EXISTS velox_migrations")
		for _, tb := range s.Tables {
			tg.exec(t, "DROP TABLE IF EXISTS "+tb.Name)
		}
		for _, e := range s.Enums {
			tg.exec(t, `DROP TYPE IF EXISTS "`+e.Name+`"`)
		}
	}
	drop()
	t.Cleanup(drop)
	return tg
}

// fullBind is a row with every field set, at the edges of its types.
func fullBind() testbind.Bind {
	note := "a note"
	return testbind.Bind{
		Flag: true, I8: math.MinInt8, I16: math.MinInt16, I32: math.MinInt32, I64: math.MinInt64,
		U8: math.MaxUint8, U16: math.MaxUint16, U32: math.MaxUint32, U64: math.MaxInt64,
		F32: 1.5, F64: 0.1, Str: "héllo, 世界 'quoted' \\ \"x\"", Short: "abcdefghij0123456789",
		Body: strings.Repeat("long text ", 100), Kind: testbind.BindKindB, Level: "high",
		Data: []byte{0, 1, 0xfe, 0xff, '\''}, ShortData: []byte("0123456789abcdef"),
		At:   time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC),
		UID:  bindtype.UUID{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00},
		Tags: []string{"x", "y"}, Labels: []string{"a", "é"}, Counts: []int{-1, 0, 7}, Doc: bindtype.Doc{Title: "t", Pages: 3, Tags: []string{"z"}}, Note: &note,
	}
}

// readBind reads row id back through velox.
func readBind(t *testing.T, tg target, id int64) testbind.Bind {
	t.Helper()
	got, err := tg.db.Query[testbind.Bind]().Where(B.ID.EQ(id)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return *got
}

// sameBind reports whether got is want, with times compared as instants.
func sameBind(t *testing.T, got, want testbind.Bind) {
	t.Helper()
	if !got.At.Equal(want.At) {
		t.Errorf("At: got %v, want %v", got.At, want.At)
	}
	got.At, want.At = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// rawText is column col of row id as the database prints it, and whether it
// is NULL.
func rawText(t *testing.T, tg target, col string, id int64) (string, bool) {
	t.Helper()
	q := "SELECT " + col + " IS NULL, COALESCE(" + col + ", '') FROM binds WHERE id = ?"
	if tg.d == velox.Postgres {
		q = "SELECT " + col + " IS NULL, COALESCE(" + col + "::text, '') FROM binds WHERE id = ?"
	}
	var null bool
	var text string
	if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, q), id).Scan(&null, &text); err != nil {
		t.Fatal(err)
	}
	return text, null
}

func TestBindingRoundTrip(t *testing.T) {
	for _, tg := range targets(t, nil) {
		t.Run(tg.name, func(t *testing.T) {
			s := bindTables(t, tg.d)
			tg := bindTarget(t, tg, s)
			if _, err := migrate.Up(ctx, tg.direct, bindDir(t, s)); err != nil {
				t.Fatalf("Up: %v", err)
			}

			t.Run("every field", func(t *testing.T) {
				want := fullBind()
				if err := tg.db.Insert(&want).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				sameBind(t, readBind(t, tg, want.ID), want)
			})

			t.Run("uint64", func(t *testing.T) {
				low := fullBind()
				low.U64 = math.MaxInt64
				if err := tg.db.Insert(&low).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if got := readBind(t, tg, low.ID); got.U64 != math.MaxInt64 {
					t.Errorf("2^63-1 read back as %d", got.U64)
				}
				high := fullBind()
				high.U64 = math.MaxUint64
				before, err := tg.db.Query[testbind.Bind]().Count(ctx)
				if err != nil {
					t.Fatal(err)
				}
				err = tg.db.Insert(&high).Exec(ctx)
				if tg.d == velox.SQLite {
					// SQLite's integer is signed: the value is refused, never stored as another.
					if err == nil {
						t.Fatalf("2^64-1 bound on SQLite, as row %d: %+v", high.ID, readBind(t, tg, high.ID))
					}
					if after, err := tg.db.Query[testbind.Bind]().Count(ctx); err != nil || after != before {
						t.Errorf("rows %d before, %d after (%v)", before, after, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := readBind(t, tg, high.ID); got.U64 != math.MaxUint64 {
					t.Errorf("2^64-1 read back as %d", got.U64)
				}
			})

			t.Run("enums", func(t *testing.T) {
				row := fullBind()
				row.Kind, row.Level = testbind.BindKindA, "low"
				if err := tg.db.Insert(&row).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				got := readBind(t, tg, row.ID)
				if got.Kind != testbind.BindKindA || got.Level != "low" {
					t.Errorf("kind %q, level %q", got.Kind, got.Level)
				}
				// Native on PostgreSQL and MySQL, a check on SQLite.
				for col, want := range map[string]string{"kind": "binds_kind", "level": "level"} {
					var typ string
					switch tg.d {
					case velox.Postgres:
						q := "SELECT pg_typeof(" + col + ")::text FROM binds WHERE id = $1"
						if err := tg.direct.QueryRowContext(ctx, q, row.ID).Scan(&typ); err != nil {
							t.Fatal(err)
						}
					case velox.MySQL:
						q := "SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'binds' AND column_name = ?"
						if err := tg.direct.QueryRowContext(ctx, q, col).Scan(&typ); err != nil {
							t.Fatal(err)
						}
						want = map[string]string{"kind": "enum('a','b')", "level": "enum('low','high')"}[col]
					}
					if tg.d != velox.SQLite && typ != want {
						t.Errorf("%s's type is %q, want %q", col, typ, want)
					}
					q := rebind(tg.d, "UPDATE binds SET "+col+" = 'c' WHERE id = ?")
					if _, err := tg.direct.ExecContext(ctx, q, row.ID); err == nil {
						t.Errorf("%s = 'c' was stored", col)
					}
				}
			})

			t.Run("nil and empty strings", func(t *testing.T) {
				nilTags := fullBind()
				nilTags.Tags = nil
				empty := fullBind()
				empty.Tags = []string{}
				for _, row := range []*testbind.Bind{&nilTags, &empty} {
					if err := tg.db.Insert(row).Exec(ctx); err != nil {
						t.Fatal(err)
					}
				}
				if text, null := rawText(t, tg, "tags", nilTags.ID); !null {
					t.Errorf("nil tags stored %q, not NULL", text)
				}
				if got := readBind(t, tg, nilTags.ID); got.Tags != nil {
					t.Errorf("nil tags read back as %#v", got.Tags)
				}
				if text, null := rawText(t, tg, "tags", empty.ID); null || text != "[]" {
					t.Errorf("empty tags stored %q (NULL %v), want []", text, null)
				}
				if got := readBind(t, tg, empty.ID); got.Tags == nil || len(got.Tags) != 0 {
					t.Errorf("empty tags read back as %#v", got.Tags)
				}
			})

			// A required field.Strings is a NOT NULL column: a nil slice
			// stores the JSON null, where ent's create would refuse it
			// unset, and reads back nil; the database refuses SQL NULL.
			t.Run("required strings", func(t *testing.T) {
				row := fullBind()
				row.Labels = nil
				if err := tg.db.Insert(&row).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if text, null := rawText(t, tg, "labels", row.ID); null || strings.TrimSpace(text) != "null" {
					t.Errorf("nil labels stored %q (NULL %v), want the JSON null", text, null)
				}
				if got := readBind(t, tg, row.ID); got.Labels != nil {
					t.Errorf("nil labels read back as %#v", got.Labels)
				}
				q := rebind(tg.d, "UPDATE binds SET labels = NULL WHERE id = ?")
				if _, err := tg.direct.ExecContext(ctx, q, row.ID); err == nil {
					t.Error("labels = NULL was stored")
				}
			})
		})
	}
}
