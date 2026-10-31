package ddl

import (
	"cmp"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/syssam/velox"
)

var update = flag.Bool("update", false, "write the golden files")

// golden checks the tab-separated rows got against testdata/<name>, or
// writes it under -update.
func golden(t *testing.T, name string, got [][]string) {
	t.Helper()
	var b strings.Builder
	for _, r := range got {
		b.WriteString(strings.Join(r, "\t") + "\n")
	}
	path := "testdata/" + name
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("%s differs; got:\n%s", path, b.String())
	}
}

func fnv32a(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func TestCap(t *testing.T) {
	for _, n := range []int{62, 63} {
		if s := strings.Repeat("a", n); Cap(s) != s {
			t.Errorf("Cap of %d bytes changed it: %q", n, Cap(s))
		}
	}
	a64 := strings.Repeat("a", 64)
	want := strings.Repeat("a", 54) + "_" + fmt.Sprintf("%08x", fnv32a(a64))
	if got := Cap(a64); got != want || len(got) != MaxName {
		t.Errorf("Cap(a64) = %q (%d bytes), want %q", got, len(got), want)
	}
	// The cut falls back before a rune that would cross byte 54.
	s := strings.Repeat("a", 53) + "é" + strings.Repeat("b", 20)
	got := Cap(s)
	if want := strings.Repeat("a", 53) + "_" + fmt.Sprintf("%08x", fnv32a(s)); got != want || len(got) != 62 || !utf8.ValidString(got) {
		t.Errorf("Cap = %q (%d bytes), want %q", got, len(got), want)
	}
}

// A helper is capped as a whole, so it stays within the cap and apart from the
// object it stands beside.
func TestCapHelpers(t *testing.T) {
	base := strings.Repeat("t", MaxName)
	for _, c := range []struct{ got, whole string }{
		{OldEnum(base), base + "_velox_old"},
		{NotNull(base, "c"), base + "_c_not_null"},
		{Rebuild(base), "velox_new_" + base},
	} {
		if len(c.got) > MaxName || c.got == base || c.got == Cap(base) || c.got != Cap(c.whole) {
			t.Errorf("got %q, want Cap(%q)", c.got, c.whole)
		}
	}
	for got, want := range map[string]string{
		OldEnum("users_role"):     "users_role_velox_old",
		NotNull("users", "email"): "users_email_not_null",
		Rebuild("users"):          "velox_new_users",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Symbol is ent's cap: 63 bytes on PostgreSQL, 64 on MySQL and SQLite, with
// the md5 of the whole name.
func TestSymbol(t *testing.T) {
	var inputs []string
	for _, n := range []int{62, 63, 64, 65, 80} {
		inputs = append(inputs, "posts_"+strings.Repeat("x", n-6))
	}
	var rows [][]string
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		for _, in := range inputs {
			rows = append(rows, []string{d.String(), in, Symbol(d, in)})
		}
	}
	accents := "posts_x" + strings.Repeat("é", 30)
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		rows = append(rows, []string{d.String(), accents, Symbol(d, accents)})
	}
	for _, r := range rows {
		limit := 64
		if r[0] == velox.Postgres.String() {
			limit = 63
		}
		if len(r[2]) > limit || !utf8.ValidString(r[2]) {
			t.Errorf("Symbol on %s of %q = %q: %d bytes, valid UTF-8 %v", r[0], r[1], r[2], len(r[2]), utf8.ValidString(r[2]))
		}
	}
	golden(t, "symbol.golden", rows)
}

// PostgresName is PostgreSQL's makeObjectName.
func TestPostgresName(t *testing.T) {
	type in struct{ name1, name2, label string }
	var ins []in
	for _, n := range []int{58, 59, 70} {
		table := "audit_entries_" + strings.Repeat("x", n-14)
		ins = append(ins, in{table, "", "pkey"}, in{table, "id", "seq"})
	}
	post := "post_" + strings.Repeat("é", 30)
	ins = append(ins,
		in{"posts_" + strings.Repeat("y", 34), "body_" + strings.Repeat("z", 35), "seq"},
		in{post, "", "pkey"},
		in{post, "id", "seq"},
	)
	var rows [][]string
	for _, c := range ins {
		got := PostgresName(c.name1, c.name2, c.label)
		if len(got) > MaxName || !utf8.ValidString(got) {
			t.Errorf("PostgresName(%q, %q, %q) = %q: %d bytes", c.name1, c.name2, c.label, got, len(got))
		}
		rows = append(rows, []string{c.name1, cmp.Or(c.name2, "-"), c.label, got})
	}
	golden(t, "pgname.golden", rows)
	for got, want := range map[string]string{
		PostgresName("users", "", "pkey"):  "users_pkey",
		PostgresName("users", "id", "seq"): "users_id_seq",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestClip(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{{"abc", 5, "abc"}, {"abc", 2, "ab"}, {"aé", 2, "a"}, {"aé", 3, "aé"}} {
		if got := Clip(c.s, c.n); got != c.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
