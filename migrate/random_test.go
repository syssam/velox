package migrate_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
)

// The generator behind TestMigrateRandom and FuzzDiff: a model of 1 to 4
// schemas R0…R3, each with 1 to 6 fields f0…f5, up to two indexes and up to
// two edges, every one a model the DSL accepts, and mutate, which edits a copy
// of it. Both draw only from r, so a seed names a pair of models.

// fieldTypes are the types a generated field takes.
var fieldTypes = []string{"string", "int64", "int", "bool", "float64", "time", "bytes", "enum"}

// enumValues are the values an enum draws from; the first of an enum's values
// is the one rowValue inserts, and a mutation never drops it.
var enumValues = []string{"a", "b", "c"}

// maxSchemas, maxFields and maxEdges bound a generated model.
const (
	maxSchemas = 4
	maxFields  = 6
	maxEdges   = 2
)

// chance reports true one time in n.
func chance(r *rand.Rand, n int) bool { return r.IntN(n) == 0 }

// randomModel is a valid model of 1 to 4 schemas.
func randomModel(r *rand.Rand) *loadjson.Model {
	m := &loadjson.Model{Version: loadjson.Version, Package: pkgPath}
	for _, i := range slices.Sorted(slices.Values(r.Perm(maxSchemas)[:1+r.IntN(maxSchemas)])) {
		m.Schemas = append(m.Schemas, randomSchema(r, "R"+strconv.Itoa(i)))
	}
	for _, s := range slices.Clone(m.Schemas) {
		for range r.IntN(maxEdges + 1) {
			addEdgePair(r, m, s, m.Schemas[r.IntN(len(m.Schemas))])
		}
	}
	normalize(m)
	return m
}

// randomSchema is a schema of 1 to 6 fields and up to two indexes.
func randomSchema(r *rand.Rand, name string) *loadjson.Schema {
	s := &loadjson.Schema{Name: name}
	for i := range 1 + r.IntN(maxFields) {
		s.Fields = append(s.Fields, randomField(r, "f"+strconv.Itoa(i)))
	}
	for range r.IntN(3) {
		addIndexTo(r, s)
	}
	return s
}

// freeName is the first of prefix0…prefix(n-1) that taken and avoid both
// leave free, or "" when none is.
func freeName(avoid map[string]bool, prefix string, n int, taken func(string) bool) string {
	for i := range n {
		if name := prefix + strconv.Itoa(i); !taken(name) && !avoid[name] {
			return name
		}
	}
	return ""
}

func hasField(s *loadjson.Schema, name string) bool {
	return slices.ContainsFunc(s.Fields, func(f *loadjson.Field) bool { return f.Name == name })
}

func hasEdge(s *loadjson.Schema, name string) bool {
	return slices.ContainsFunc(s.Edges, func(e *loadjson.Edge) bool { return e.Name == name })
}

// randomField is a field of a random type that is Optional, Nillable, Unique
// or has a literal Default by r; normalize makes it valid.
func randomField(r *rand.Rand, name string) *loadjson.Field {
	f := &loadjson.Field{Name: name, Type: fieldTypes[r.IntN(len(fieldTypes))]}
	if f.Type == "enum" {
		setEnumValues(f, enumSubset(r, ""))
	}
	f.Optional = chance(r, 4)
	f.Nillable = chance(r, 4)
	f.Unique = chance(r, 6) && f.Type != "bytes"
	if chance(r, 3) {
		setDefault(r, f)
	}
	return f
}

// enumSubset is first, when it is not "", and then the values of enumValues
// after it that r keeps; with no first, the first is a value r picks.
func enumSubset(r *rand.Rand, first string) []string {
	if first == "" {
		first = enumValues[r.IntN(len(enumValues))]
	}
	out := []string{first}
	for _, v := range enumValues {
		if v != first && chance(r, 2) {
			out = append(out, v)
		}
	}
	return out
}

func setEnumValues(f *loadjson.Field, vs []string) {
	f.Enums = nil
	for _, v := range vs {
		f.Enums = append(f.Enums, loadjson.Enum{Name: goName(v), Value: v})
	}
	if len(f.Default) > 0 {
		f.Default = mustJSON(vs[0]) // the default is the first value, which stays
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// setDefault gives f a literal Default of its type, if it takes one. A bool or
// a number with a Default must be Nillable, so f becomes Nillable. An enum's
// is its first value.
func setDefault(r *rand.Rand, f *loadjson.Field) {
	switch f.Type {
	case "string":
		f.Default = mustJSON([]string{"x", "y", "it's"}[r.IntN(3)])
	case "int64", "int":
		f.Default = mustJSON([]int{0, 3, 7}[r.IntN(3)])
	case "float64":
		f.Default = mustJSON([]float64{0, 1.5, 2.5}[r.IntN(3)])
	case "bool":
		f.Default = mustJSON(chance(r, 2))
	case "enum":
		f.Default = mustJSON(f.Enums[0].Value)
	default:
		return
	}
	if f.Type != "string" && f.Type != "enum" {
		f.Nillable = true
	}
}

// addIndexTo gives s an index over 1 or 2 of its fields, if it has a field
// and no index over the same fields.
func addIndexTo(r *rand.Rand, s *loadjson.Schema) {
	if len(s.Fields) == 0 {
		return
	}
	var names []string
	want := 1 + r.IntN(2)
	for _, i := range r.Perm(len(s.Fields)) {
		if f := s.Fields[i]; f.Type != "bytes" && len(names) < want {
			names = append(names, f.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	if slices.ContainsFunc(s.Indexes, func(ix *loadjson.Index) bool { return sameSet(ix.Fields, names) }) {
		return
	}
	s.Indexes = append(s.Indexes, &loadjson.Index{Fields: names, Unique: chance(r, 2)})
}

// addEdgePair gives x the edge to y and y its inverse, if x has room for an
// edge; Required only to another schema, and on one of the two.
func addEdgePair(r *rand.Rand, m *loadjson.Model, x, y *loadjson.Schema) {
	var to int
	for _, e := range x.Edges {
		if !e.Inverse {
			to++
		}
	}
	if to >= maxEdges {
		return
	}
	// An edge name is free in the whole model: two edges of one name between
	// the same tables would make one foreign key symbol twice.
	free := func(prefix string) string {
		return freeName(nil, prefix, 99, func(n string) bool {
			return slices.ContainsFunc(m.Schemas, func(s *loadjson.Schema) bool { return hasEdge(s, n) })
		})
	}
	name, inv := free("e"), free("i")
	a := &loadjson.Edge{Name: name, Type: y.Name, TypePkg: m.Package, Unique: chance(r, 3)}
	b := &loadjson.Edge{Name: inv, Type: x.Name, TypePkg: m.Package, Inverse: true, Ref: name, Unique: chance(r, 3)}
	if x != y && chance(r, 4) {
		if chance(r, 2) {
			a.Required = true
		} else {
			b.Required = true
		}
	}
	x.Edges = append(x.Edges, a)
	y.Edges = append(y.Edges, b)
}

// dropEdgePair drops x's edge e to another schema or its own, and its
// inverse.
func dropEdgePair(m *loadjson.Model, x *loadjson.Schema, e *loadjson.Edge) {
	for _, y := range m.Schemas {
		if y.Name == e.Type {
			y.Edges = slices.DeleteFunc(y.Edges, func(i *loadjson.Edge) bool { return i.Inverse && i.Ref == e.Name && i.Type == x.Name })
		}
	}
	x.Edges = slices.DeleteFunc(x.Edges, func(i *loadjson.Edge) bool { return i == e })
}

// normalize makes every edit a model the DSL accepts: a Default on a bool or
// a number needs Nillable (or goes), an Optional field that is not Nillable
// needs a Default (or becomes Nillable), bytes are neither Unique
// nor indexed (MySQL covers a blob only by a prefix), an index names fields
// that exist and no two indexes repeat, and the positions count the lists.
func normalize(m *loadjson.Model) {
	slices.SortFunc(m.Schemas, func(a, b *loadjson.Schema) int { return strings.Compare(a.Name, b.Name) })
	for _, s := range m.Schemas {
		for _, f := range s.Fields {
			if f.Type == "bytes" {
				f.Unique = false // MySQL covers a blob only by a prefix
			}
			if len(f.Default) > 0 && !f.Nillable && f.Type != "string" && f.Type != "enum" {
				f.Default = nil
			}
			if f.Unique && f.Optional {
				f.Nillable = true // unset rows must not all hold the zero value
			}
			if f.Optional && !f.Nillable && f.Type != "json" && len(f.Default) == 0 && !f.DefaultFunc && len(f.SchemaDefault) == 0 {
				f.Nillable = true
			}
		}
		var kept []*loadjson.Index
		for _, ix := range s.Indexes {
			ix.Fields = slices.DeleteFunc(ix.Fields, func(n string) bool {
				return !slices.ContainsFunc(s.Fields, func(f *loadjson.Field) bool { return f.Name == n && f.Type != "bytes" })
			})
			if len(ix.Fields) > 0 && !slices.ContainsFunc(kept, func(k *loadjson.Index) bool { return sameSet(k.Fields, ix.Fields) }) {
				kept = append(kept, ix)
			}
		}
		s.Indexes = kept
		for _, ix := range kept { // a unique index of one column holds it as Unique does
			if ix.Unique && len(ix.Fields) == 1 {
				for _, f := range s.Fields {
					if f.Name == ix.Fields[0] && f.Optional {
						f.Nillable = true
					}
				}
			}
		}
		for i, f := range s.Fields {
			f.Position.Index = i
		}
		for i, e := range s.Edges {
			e.Position.Index = i
		}
		for i, ix := range s.Indexes {
			ix.Position.Index = i
		}
	}
}

// cloneModel is a deep copy of m.
func cloneModel(m *loadjson.Model) *loadjson.Model {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	var c loadjson.Model
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	return &c
}

// mutate is a copy of m with 1 to 3 edits: a schema added or dropped, a field
// added or dropped, an Optional, Nillable or Unique flipped, a Default changed
// or dropped, an index added or dropped, an edge added or dropped, an enum's
// values changed. A name a mutation drops is not taken again by one that adds,
// so no field changes its type under its name and no table is dropped and made
// again with other columns. An enum keeps its first value, which rowValue
// inserts: dropping it would be a correct drop-enum-value failure on that row.
func mutate(r *rand.Rand, m *loadjson.Model) *loadjson.Model {
	m = cloneModel(m)
	dropped := map[string]bool{} // names dropped: "R1", "R1.f2"
	edits := 1 + r.IntN(3)
	for done := 0; done < edits; {
		if mutateOnce(r, m, dropped) {
			done++
			normalize(m)
		}
	}
	return m
}

// mutateOnce makes one edit, or reports false when the model has nothing for
// the edit r chose.
func mutateOnce(r *rand.Rand, m *loadjson.Model, dropped map[string]bool) bool {
	s := m.Schemas[r.IntN(len(m.Schemas))]
	switch r.IntN(10) {
	case 0: // add a schema
		var name string
		for i := range maxSchemas {
			if n := "R" + strconv.Itoa(i); !dropped[n] && !slices.ContainsFunc(m.Schemas, func(s *loadjson.Schema) bool { return s.Name == n }) {
				name = n
				break
			}
		}
		if name == "" {
			return false
		}
		m.Schemas = append(m.Schemas, randomSchema(r, name))
	case 1: // drop a schema
		if len(m.Schemas) < 2 {
			return false
		}
		dropped[s.Name] = true
		m.Schemas = slices.DeleteFunc(m.Schemas, func(o *loadjson.Schema) bool { return o == s })
		for _, o := range m.Schemas {
			o.Edges = slices.DeleteFunc(o.Edges, func(e *loadjson.Edge) bool { return e.Type == s.Name })
		}
	case 2: // add a field
		avoid := map[string]bool{}
		for k := range dropped {
			if f, ok := strings.CutPrefix(k, s.Name+"."); ok {
				avoid[f] = true
			}
		}
		name := freeName(avoid, "f", maxFields, func(n string) bool { return hasField(s, n) })
		if name == "" {
			return false
		}
		s.Fields = append(s.Fields, randomField(r, name))
	case 3: // drop a field
		if len(s.Fields) < 2 {
			return false
		}
		f := s.Fields[r.IntN(len(s.Fields))]
		dropped[s.Name+"."+f.Name] = true
		s.Fields = slices.DeleteFunc(s.Fields, func(o *loadjson.Field) bool { return o == f })
	case 4: // flip Optional, Nillable or Unique
		f := s.Fields[r.IntN(len(s.Fields))]
		switch r.IntN(3) {
		case 0:
			f.Optional = !f.Optional
		case 1:
			f.Nillable = !f.Nillable
		default:
			f.Unique = !f.Unique
		}
	case 5: // change or drop a Default
		f := s.Fields[r.IntN(len(s.Fields))]
		if len(f.Default) > 0 && chance(r, 2) {
			f.Default = nil
		} else {
			setDefault(r, f)
		}
	case 6: // add or drop an index
		if len(s.Indexes) > 0 && chance(r, 2) {
			i := r.IntN(len(s.Indexes))
			s.Indexes = slices.Delete(s.Indexes, i, i+1)
		} else {
			addIndexTo(r, s)
		}
	case 7: // add an edge
		addEdgePair(r, m, s, m.Schemas[r.IntN(len(m.Schemas))])
	case 8: // drop an edge
		var tos []*loadjson.Edge
		for _, e := range s.Edges {
			if !e.Inverse {
				tos = append(tos, e)
			}
		}
		if len(tos) == 0 {
			return false
		}
		dropEdgePair(m, s, tos[r.IntN(len(tos))])
	default: // change an enum's values
		var enums []*loadjson.Field
		for _, f := range s.Fields {
			if f.Type == "enum" {
				enums = append(enums, f)
			}
		}
		if len(enums) == 0 {
			return false
		}
		f := enums[r.IntN(len(enums))]
		setEnumValues(f, enumSubset(r, f.Enums[0].Value))
	}
	return true
}

// seedModels is the models of seed: the random model and its mutation, as
// load models and as tables on d. A model the DSL or Tables refuses is a
// generator bug.
func seedModels(t testing.TB, seed uint64, d velox.Dialect) (a, b *loadjson.Model, from, to *migrate.Schema) {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, 0))
	a = randomModel(r)
	b = mutate(r, a)
	tables := func(what string, m *loadjson.Model) *migrate.Schema {
		g, err := codegen.Build(m)
		if err != nil {
			t.Fatalf("seed %d: Build refuses the generated %s model: %v\n%s", seed, what, err, modelJSON(m))
		}
		s, err := codegen.Tables(g, d)
		if err != nil {
			t.Fatalf("seed %d: Tables on %v refuses the generated %s model: %v\n%s", seed, d, what, err, modelJSON(m))
		}
		return s
	}
	return a, b, tables("first", a), tables("second", b)
}

func modelJSON(m *loadjson.Model) string {
	b, err := json.Marshal(m)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

// randomSeeds is how many seeds TestMigrateRandom runs on d: 200 on SQLite
// (50 under -short) and a fixed 50 on PostgreSQL and MySQL. SQLite under
// -race, the gate's run, costs about 0.14 s of CPU a seed: 500 seeds took 67 s
// and 300 took 39 to 59 s on a loaded machine, past or near builder ruling
// 17's 60 s; 200 take 31 s, and 2 s without -race. PostgreSQL's and
// MySQL's seeds each make a database, and a PostgreSQL server has 100
// connections to give.
func randomSeeds(d velox.Dialect) uint64 {
	if d != velox.SQLite || testing.Short() {
		return 50
	}
	return 200
}

// TestMigrateRandom: for each seed, a random model and its mutation migrate as
// TestMigrateCases does, with every destructive finding allowed: from applied
// and checked, the plan to to applied and checked, and the rows to keeps read
// back the same. A failing seed is a finding: the message names it, the
// dialect, both models and the scripts. The seeds run in parallel: each has a
// database of its own.
func TestMigrateRandom(t *testing.T) {
	t.Parallel()
	env := map[velox.Dialect]string{velox.SQLite: "", velox.Postgres: "VELOX_TEST_POSTGRES", velox.MySQL: "VELOX_TEST_MYSQL_ADMIN"}
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			if v := env[d]; v != "" && os.Getenv(v) == "" {
				t.Skip("needs " + v)
			}
			start := time.Now()
			n := randomSeeds(d)
			var (
				mu  sync.Mutex
				ran int // under -run, fewer seeds run than n
			)
			// After the parallel seeds, which run once this function returns.
			t.Cleanup(func() {
				t.Logf("%d seeds on %v in %v", ran, d, time.Since(start).Round(10*time.Millisecond))
			})
			for seed := uint64(1); seed <= n; seed++ {
				t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
					t.Parallel()
					mu.Lock()
					ran++
					mu.Unlock()
					a, b, from, to := seedModels(t, seed, d)
					defer func() {
						if t.Failed() {
							t.Logf("seed %d on %v\nfrom model: %s\nto model: %s", seed, d, modelJSON(a), modelJSON(b))
						}
					}()
					migrateSchemas(t, d, from, to, planOptionsAllowing(t, from, to))
				})
			}
		})
	}
}

// planOptionsAllowing is PlanOptions that allow every destructive finding of
// the plan from → to.
func planOptionsAllowing(t *testing.T, from, to *migrate.Schema) migrate.PlanOptions {
	t.Helper()
	_, err := migrate.NewPlan(from, to, migrate.PlanOptions{})
	var blocked *migrate.BlockedError
	switch {
	case err == nil:
		return migrate.PlanOptions{}
	case errors.As(err, &blocked):
		var opts migrate.PlanOptions
		for _, f := range blocked.Findings {
			opts.Allow = append(opts.Allow, migrate.Allow{Code: f.Code, Target: f.Target})
		}
		return opts
	}
	t.Fatalf("NewPlan: %v", err)
	return migrate.PlanOptions{}
}

// FuzzDiff: for a seed's models on each dialect, with no database, Diff of a
// schema and itself is empty; Diff of the two succeeds or returns a
// *migrate.Error, and when it succeeds Render does too; and the first model
// round-trips through its JSON to an equal schema.
func FuzzDiff(f *testing.F) {
	for seed := uint64(1); seed <= 5; seed++ {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			a, b, from, to := seedModels(t, seed, d)
			fail := func(format string, args ...any) {
				t.Helper()
				t.Fatalf("seed %d on %v: %s\nfrom model: %s\nto model: %s", seed, d, fmt.Sprintf(format, args...), modelJSON(a), modelJSON(b))
			}
			if cs, err := migrate.Diff(from, from, migrate.DiffOptions{}); err != nil || len(cs) > 0 {
				fail("Diff of a schema and itself: %v, %v", cs, err)
			}
			data, err := json.Marshal(from)
			if err != nil {
				fail("Marshal: %v", err)
			}
			var back migrate.Schema
			if err := json.Unmarshal(data, &back); err != nil {
				fail("Unmarshal: %v\n%s", err, data)
			}
			if !from.Equal(&back) {
				fail("the schema read back from its JSON differs:\n%s", data)
			}
			cs, err := migrate.Diff(from, to, migrate.DiffOptions{})
			if err != nil {
				if _, ok := errors.AsType[*migrate.Error](err); !ok {
					fail("Diff returned %T: %v; want a *migrate.Error", err, err)
				}
				continue
			}
			if _, err := migrate.Render(from, to, cs, migrate.RenderOptions{}); err != nil {
				fail("Render of Diff's changes: %v", err)
			}
		}
	})
}
