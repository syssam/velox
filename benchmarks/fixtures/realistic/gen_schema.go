//go:build ignore

// gen_schema writes the entity files of the realistic fixture's schema
// package. The output is a pure function of -n and -seed.
//
//	go run gen_schema.go            # 450 entities, seed 1
//
// The shape follows measurements of a real 454-entity multi-tenant
// application (see README.md): fields per entity (median 30, 99th percentile
// 92), the mix of field types and attributes, edge degrees with a few hub
// entities of 15 to 30 edges, three shared mixins carrying the tenant policy
// and hooks, and a minority of entities with a policy or hook of their own.
// Names and content are synthetic.
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	n    = flag.Int("n", 450, "number of entities")
	seed = flag.Uint64("seed", 1, "random seed")
	out  = flag.String("out", "schema", "output directory")
)

// words name fields; a suffix makes them unique within an entity. Names that
// velox generates a package-level identifier for (Label, Limit, Table, Columns,
// Order, Hooks, Policy, ...) are left out: a field with such a name makes
// velox's generated entity package fail to compile, a known limitation that
// has nothing to do with what is being measured.
var words = strings.Fields(`code name title status amount total price qty note memo ref kind level score rank
seq flag active enabled due start end paid sent read slug source channel region country currency
unit weight height width depth volume rate ratio tax fee discount balance quota capacity version
revision priority severity category owner assignee reviewer approver email phone address city zip
url path token secret key hash checksum size count retries attempts interval timeout delay expires
starts ends opened closed started finished approved rejected shipped received billed settled posted
description summary comment reason remark tags meta config options params payload result error warning`)

type relation struct {
	kind          string // "pair", "oneway", "m2m", "o2o"
	child, parent int
	required      bool
}

type ent struct {
	name      string
	mixins    []string
	fields    []string // code of each field expression
	fieldName []string
	edges     []string
	indexes   []string
	imports   map[string]bool
	order     []string // graphql OrderField candidates
	filter    []string // graphql WhereInputFields candidates
	edgeNames []string
	usedField map[string]bool
	usedEdge  map[string]bool
	policy    bool
	hook      bool
	degree    int
}

func main() {
	flag.Parse()
	r := rand.New(rand.NewPCG(*seed, 0x9e3779b97f4a7c15))
	ents := make([]*ent, *n)
	for i := range ents {
		ents[i] = &ent{name: fmt.Sprintf("Rec%03d", i), imports: map[string]bool{}, usedField: map[string]bool{}, usedEdge: map[string]bool{}}
	}

	// Mixins: most entities are tenant-scoped with audit columns.
	for _, e := range ents {
		x := r.Float64()
		switch {
		case x < 0.68:
			e.mixins = []string{"TimeMixin", "WorkspaceMixin", "AuditMixin"}
			if r.Float64() < 0.3 {
				e.mixins = append(e.mixins, "SoftDeleteMixin")
			}
		case x < 0.93:
			e.mixins = []string{"TimeMixin", "WorkspaceMixin"}
		case x < 0.99:
			e.mixins = []string{"TimeMixin"}
		}
		e.policy = r.Float64() < 0.13
		e.hook = r.Float64() < 0.10
	}

	// Relations, by preferential attachment so a few entities become hubs.
	rels := relations(r, ents)
	for _, rel := range rels {
		addRelation(ents, rel, r)
	}
	for _, e := range ents {
		for range clamp(int(math.Round(math.Exp(r.NormFloat64()*0.5+math.Log(22.5)))), 4, 100) {
			addField(e, r)
		}
		addIndexes(e, r)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		panic(err)
	}
	old, _ := filepath.Glob(filepath.Join(*out, "rec*.go"))
	for _, f := range old {
		_ = os.Remove(f)
	}
	for _, e := range ents {
		if err := os.WriteFile(filepath.Join(*out, strings.ToLower(e.name)+".go"), []byte(render(e, r)), 0o644); err != nil {
			panic(err)
		}
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func relations(r *rand.Rand, ents []*ent) []relation {
	count := int(float64(len(ents)) * 1.44)
	deg := make([]int, len(ents))
	var rels []relation
	for len(rels) < count {
		child := r.IntN(len(ents))
		total := 0.0
		w := make([]float64, len(ents))
		for i := range w {
			w[i] = math.Pow(float64(deg[i])+0.4, 1.4)
			total += w[i]
		}
		x, parent := r.Float64()*total, 0
		for parent = range w {
			if x -= w[parent]; x < 0 {
				break
			}
		}
		x = r.Float64()
		rel := relation{child: child, parent: parent, required: r.Float64() < 0.5}
		switch {
		case parent == child && x >= 0.02:
			continue // self-references are rare
		case x < 0.88:
			rel.kind = "pair"
		case x < 0.96:
			rel.kind = "oneway"
		case x < 0.98:
			rel.kind = "m2m"
		default:
			rel.kind = "o2o"
		}
		if rel.kind == "m2m" && parent == child {
			continue
		}
		deg[child]++
		deg[parent]++
		rels = append(rels, rel)
	}
	return rels
}

func snake(name string) string { return strings.ToLower(name) }

func unique(used map[string]bool, name string) string {
	n, i := name, 2
	for used[n] {
		n = fmt.Sprintf("%s_%d", name, i)
		i++
	}
	used[n] = true
	return n
}

func addRelation(ents []*ent, rel relation, r *rand.Rand) {
	c, p := ents[rel.child], ents[rel.parent]
	c.degree++
	p.degree++
	switch rel.kind {
	case "pair", "oneway":
		cn := unique(c.usedEdge, snake(p.name))
		fk := unique(c.usedField, cn+"_id")
		opt := ""
		if !rel.required {
			opt = ".Optional()"
		}
		c.fields = append(c.fields, fmt.Sprintf("field.Int64(%q)%s", fk, opt))
		c.fieldName = append(c.fieldName, fk)
		if rel.kind == "pair" {
			pn := unique(p.usedEdge, snake(c.name)+"s")
			p.edges = append(p.edges, fmt.Sprintf("edge.To(%q, %s.Type)", pn, c.name))
			p.edgeNames = append(p.edgeNames, pn)
			c.edges = append(c.edges, fmt.Sprintf("edge.From(%q, %s.Type).Ref(%q).Field(%q).Unique()", cn, p.name, pn, fk))
		} else {
			c.edges = append(c.edges, fmt.Sprintf("edge.To(%q, %s.Type).Field(%q).Unique()", cn, p.name, fk))
		}
		c.edgeNames = append(c.edgeNames, cn)
	case "m2m":
		pn := unique(p.usedEdge, snake(c.name)+"s")
		cn := unique(c.usedEdge, snake(p.name)+"s")
		p.edges = append(p.edges, fmt.Sprintf("edge.To(%q, %s.Type)", pn, c.name))
		c.edges = append(c.edges, fmt.Sprintf("edge.From(%q, %s.Type).Ref(%q)", cn, p.name, pn))
		p.edgeNames = append(p.edgeNames, pn)
		c.edgeNames = append(c.edgeNames, cn)
	case "o2o":
		pn := unique(p.usedEdge, snake(c.name))
		cn := unique(c.usedEdge, snake(p.name)+"_owner")
		p.edges = append(p.edges, fmt.Sprintf("edge.To(%q, %s.Type).Unique()", pn, c.name))
		c.edges = append(c.edges, fmt.Sprintf("edge.From(%q, %s.Type).Ref(%q).Unique()", cn, p.name, pn))
		p.edgeNames = append(p.edgeNames, pn)
		c.edgeNames = append(c.edgeNames, cn)
	}
}

// kinds and their weights among an entity's own fields.
var kinds = []struct {
	name string
	w    float64
}{{"int64", 0.28}, {"string", 0.27}, {"time", 0.12}, {"decimal", 0.08}, {"int", 0.06}, {"bool", 0.05}, {"json", 0.05}, {"float", 0.02}, {"enum", 0.10}}

func pickKind(r *rand.Rand) string {
	total := 0.0
	for _, k := range kinds {
		total += k.w
	}
	x := r.Float64() * total
	for _, k := range kinds {
		if x -= k.w; x < 0 {
			return k.name
		}
	}
	return "string"
}

func addField(e *ent, r *rand.Rand) {
	name := unique(e.usedField, words[r.IntN(len(words))])
	kind := pickKind(r)
	// nullability: 40% optional+nillable, 12% optional with default, 32%
	// required with default, 16% required.
	x := r.Float64()
	var mode string
	switch {
	case x < 0.40:
		mode = "nullable"
	case x < 0.52:
		mode = "optdef"
	case x < 0.84:
		mode = "def"
	default:
		mode = "req"
	}
	if kind == "json" {
		mode = "optjson"
	}
	var b strings.Builder
	def := ""
	switch kind {
	case "int64":
		fmt.Fprintf(&b, "field.Int64(%q)", name)
		def = "0"
	case "int":
		fmt.Fprintf(&b, "field.Int(%q)", name)
		def = "0"
	case "float":
		fmt.Fprintf(&b, "field.Float(%q)", name)
		def = "0"
	case "bool":
		fmt.Fprintf(&b, "field.Bool(%q)", name)
		def = "false"
	case "string":
		fmt.Fprintf(&b, "field.String(%q)", name)
		def = `""`
	case "time":
		fmt.Fprintf(&b, "field.Time(%q)", name)
		def = "time.Now"
	case "decimal":
		fmt.Fprintf(&b, "field.Other(%q, types.Decimal{}).SchemaType(map[string]string{dialect.SQLite: \"numeric\", dialect.Postgres: \"numeric(18,2)\", dialect.MySQL: \"decimal(18,2)\"})", name)
		def = "types.Decimal{}"
		e.imports["types"], e.imports["dialect"] = true, true
	case "json":
		fmt.Fprintf(&b, "field.JSON(%q, map[string]any{})", name)
	case "enum":
		nv := 3 + r.IntN(4)
		vals := make([]string, nv)
		for i := range vals {
			vals[i] = fmt.Sprintf("%q", fmt.Sprintf("v%d", i))
		}
		fmt.Fprintf(&b, "field.Enum(%q).Values(%s)", name, strings.Join(vals, ", "))
		def = `"v0"`
	}
	switch mode {
	case "nullable":
		b.WriteString(".Optional().Nillable()")
	case "optdef":
		fmt.Fprintf(&b, ".Optional().Default(%s)", def)
	case "def":
		fmt.Fprintf(&b, ".Default(%s)", def)
	case "optjson":
		b.WriteString(".Optional()")
	}
	if kind == "enum" && mode == "req" {
		b.WriteString(`.Default("v0")`) // an enum has no zero value to fall back on
	}
	// Validators on strings and numbers without a default that contradicts them.
	switch kind {
	case "string":
		if r.Float64() < 0.50 {
			b.WriteString(".MaxLen(255)")
		}
		if mode == "req" && r.Float64() < 0.20 {
			b.WriteString(".NotEmpty()")
		}
	case "int64", "int":
		if r.Float64() < 0.30 {
			b.WriteString(".NonNegative()")
		}
	}
	if mode == "req" && kind != "json" && r.Float64() < 0.18 {
		b.WriteString(".Immutable()")
	}
	switch kind {
	case "string", "time":
		if r.Float64() < 0.05 {
			up := strings.ToUpper(name)
			fmt.Fprintf(&b, ".Annotations(graphql.OrderField(%q))", up)
		}
	}
	if kind == "string" || kind == "enum" || kind == "bool" || kind == "time" || kind == "int64" {
		e.filter = append(e.filter, name)
	}
	e.fields = append(e.fields, b.String())
	e.fieldName = append(e.fieldName, name)
}

func addIndexes(e *ent, r *rand.Rand) {
	cnt := clamp(int(math.Round(math.Exp(r.NormFloat64()*0.7+math.Log(6.4)))), 0, 20)
	seen := map[string]bool{}
	for range cnt {
		k := 1 + r.IntN(3)
		var cols []string
		for range k {
			cols = append(cols, e.fieldName[r.IntN(len(e.fieldName))])
		}
		sort.Strings(cols)
		key := strings.Join(cols, ",")
		if seen[key] || hasDup(cols) {
			continue
		}
		seen[key] = true
		q := make([]string, len(cols))
		for i, c := range cols {
			q[i] = fmt.Sprintf("%q", c)
		}
		s := fmt.Sprintf("index.Fields(%s)", strings.Join(q, ", "))
		if r.Float64() < 0.1 {
			s += ".Unique()"
		}
		e.indexes = append(e.indexes, s)
	}
}

func hasDup(s []string) bool {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[i] == s[j] {
				return true
			}
		}
	}
	return false
}

func render(e *ent, r *rand.Rand) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	imp := []string{
		"github.com/syssam/velox", "github.com/syssam/velox/contrib/graphql", "github.com/syssam/velox/schema",
		"github.com/syssam/velox/schema/field",
	}
	if len(e.edges) > 0 {
		imp = append(imp, "github.com/syssam/velox/schema/edge")
	}
	if len(e.indexes) > 0 {
		imp = append(imp, "github.com/syssam/velox/schema/index")
	}
	if e.policy {
		imp = append(imp, "github.com/syssam/velox/privacy")
	}
	if strings.Contains(strings.Join(e.fields, "\n"), "time.Now") {
		imp = append(imp, "time")
	}
	if e.imports["dialect"] {
		imp = append(imp, "github.com/syssam/velox/dialect")
	}
	if e.imports["types"] {
		imp = append(imp, "example.com/realistic/types")
	}
	sort.Strings(imp)
	w("// Code generated by gen_schema.go. DO NOT EDIT.\n\npackage schema\n\nimport (\n")
	for _, p := range imp {
		w("\t%q\n", p)
	}
	w(")\n\n// %s is a synthetic entity of the realistic fixture.\ntype %s struct{ velox.Schema }\n\n", e.name, e.name)
	if len(e.mixins) > 0 {
		w("// Mixin of %s.\nfunc (%s) Mixin() []velox.Mixin {\n\treturn []velox.Mixin{", e.name, e.name)
		for i, m := range e.mixins {
			if i > 0 {
				w(", ")
			}
			w("%s{}", m)
		}
		w("}\n}\n\n")
	}
	w("// Fields of %s.\nfunc (%s) Fields() []velox.Field {\n\treturn []velox.Field{\n\t\tentityID(%q),\n", e.name, e.name, strings.ToLower(e.name)+"_id")
	for _, f := range e.fields {
		w("\t\t%s,\n", f)
	}
	w("\t}\n}\n\n")
	if len(e.edges) > 0 {
		w("// Edges of %s.\nfunc (%s) Edges() []velox.Edge {\n\treturn []velox.Edge{\n", e.name, e.name)
		for _, x := range e.edges {
			w("\t\t%s,\n", x)
		}
		w("\t}\n}\n\n")
	}
	if len(e.indexes) > 0 {
		w("// Indexes of %s.\nfunc (%s) Indexes() []velox.Index {\n\treturn []velox.Index{\n", e.name, e.name)
		for _, x := range e.indexes {
			w("\t\t%s,\n", x)
		}
		w("\t}\n}\n\n")
	}
	if e.policy {
		w("// Policy of %s, on top of the mixin's.\nfunc (%s) Policy() velox.Policy {\n\treturn privacy.Policy{\n\t\tQuery:    privacy.QueryPolicy{privacy.AlwaysAllowRule()},\n\t\tMutation: privacy.MutationPolicy{privacy.AlwaysAllowRule()},\n\t}\n}\n\n", e.name, e.name)
	}
	if e.hook {
		w("// Hooks of %s, on top of the mixins'.\nfunc (%s) Hooks() []velox.Hook {\n\treturn []velox.Hook{func(next velox.Mutator) velox.Mutator { return next }}\n}\n\n", e.name, e.name)
	}
	w("// Annotations of %s: a GraphQL connection field with filters, as in the application it models.\nfunc (%s) Annotations() []schema.Annotation {\n\treturn []schema.Annotation{\n\t\tgraphql.QueryField(),\n\t\tgraphql.RelayConnection(),\n", e.name, e.name)
	if len(e.filter) > 0 {
		k := min(len(e.filter), 6)
		q := make([]string, k)
		for i := range k {
			q[i] = fmt.Sprintf("%q", e.filter[i])
		}
		w("\t\tgraphql.WhereInputFields(%s),\n", strings.Join(q, ", "))
	}
	if len(e.edgeNames) > 0 && r.Float64() < 0.5 {
		k := min(len(e.edgeNames), 2)
		q := make([]string, k)
		for i := range k {
			q[i] = fmt.Sprintf("%q", e.edgeNames[i])
		}
		w("\t\tgraphql.WhereInputEdges(%s),\n", strings.Join(q, ", "))
	}
	w("\t}\n}\n")
	return b.String()
}
