//go:build ignore

// gen_services writes a mock application on top of the fixture: one package
// per service, each making the ORM calls a service makes and padded with
// ordinary Go to a realistic size, once against velox's API
// (services_velox/) and once against vx's (services_vx/). Both flavors are
// rendered from the same plan, so they ask the database for the same things.
//
//	go run gen_services.go           # 312 services, seed 1
//
// The plan follows measurements of the modelled application's 332 services:
// about 24 ORM calls per service, mostly queries (a third with an eager
// load), then lookups by id, updates of one row, creates and deletes, a
// quarter of the writes inside a transaction, and 2,200 lines of code per
// service on average. A service touches its own entity and up to three
// entities related to it.
package main

import (
	"flag"
	"fmt"
	"go/format"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

var (
	nsvc = flag.Int("n", 312, "number of services")
	seed = flag.Uint64("seed", 1, "random seed")
)

const module = "example.com/realistic"

type fld struct {
	pascal, snake, lit string // StructField, column, a literal of its type
	isID, imm          bool
}

type ent struct {
	name, pkg string
	fields    []fld // usable in predicates and setters
	edges     []string
	related   []string
}

func main() {
	flag.Parse()
	cfg, err := gen.NewConfig(gen.WithTarget("./velox"), gen.WithPackage(module+"/velox"))
	if err != nil {
		panic(err)
	}
	g, err := compiler.LoadGraph("./schema", cfg)
	if err != nil {
		panic(err)
	}
	ents := map[string]*ent{}
	var names []string
	for _, t := range g.Nodes {
		e := &ent{name: t.Name, pkg: t.PackageDir()}
		e.fields = append(e.fields, fld{pascal: "ID", snake: "id", lit: "id", isID: true})
		for _, f := range t.Fields {
			if f.IsEnum() || f.IsJSON() || f.IsEdgeField() || f.Type == nil || f.Name == "id" {
				continue
			}
			var lit string
			switch f.Type.Type {
			case field.TypeString:
				lit = `"v"`
			case field.TypeInt, field.TypeInt64, field.TypeFloat64:
				lit = "1"
			case field.TypeBool:
				lit = "true"
			case field.TypeTime:
				lit = "now"
			default:
				continue
			}
			e.fields = append(e.fields, fld{pascal: f.StructField(), snake: f.Name, lit: lit, imm: f.Immutable})
		}
		for _, x := range t.Edges {
			e.edges = append(e.edges, x.StructField())
			e.related = append(e.related, x.Type.Name)
		}
		ents[t.Name] = e
		names = append(names, t.Name)
	}
	sort.Strings(names)

	r := rand.New(rand.NewPCG(*seed, 0xa5a5a5a5))
	main := r.Perm(len(names))
	for _, dir := range []string{"services_velox", "services_vx"} {
		_ = os.RemoveAll(dir)
	}
	for i := 0; i < *nsvc && i < len(main); i++ {
		p := plan(r, ents, names[main[i]], i)
		for _, flavor := range []string{"velox", "vx"} {
			dir := filepath.Join("services_"+flavor, p.pkg)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				panic(err)
			}
			text := render(flavor, p)
			src, err := format.Source([]byte(text))
			if err != nil {
				panic(fmt.Sprintf("%s/%s: %v", flavor, p.pkg, err))
			}
			if err := os.WriteFile(filepath.Join(dir, "service.go"), src, 0o644); err != nil {
				panic(err)
			}
			// REALISTIC_LIST=1 adds what a GraphQL application has per
			// entity: a list endpoint that takes Relay's arguments, an
			// order and a filter, and returns a connection. It needs the
			// ORM generated with its GraphQL layer.
			_ = os.Remove(filepath.Join(dir, "list.go"))
			if os.Getenv("REALISTIC_LIST") == "1" {
				src, err := format.Source([]byte(renderList(flavor, p)))
				if err != nil {
					panic(fmt.Sprintf("%s/%s list: %v", flavor, p.pkg, err))
				}
				if err := os.WriteFile(filepath.Join(dir, "list.go"), src, 0o644); err != nil {
					panic(err)
				}
			}
		}
	}
}

type call struct {
	op     string // query, get, update1, updateN, create, bulk, delete
	ent    *ent
	preds  []fld
	sets   []fld
	order  fld
	with   string
	limit  int
	inTx   bool
	filler int
}

type service struct {
	pkg    string
	used   []*ent
	calls  []call
	filler int // filler functions
}

func plan(r *rand.Rand, ents map[string]*ent, main string, idx int) *service {
	m := ents[main]
	p := &service{pkg: fmt.Sprintf("svc%03d", idx)}
	set := map[string]bool{main: true}
	p.used = []*ent{m}
	if len(m.related) > 0 {
		for range r.IntN(4) {
			n := m.related[r.IntN(len(m.related))]
			if !set[n] {
				set[n] = true
				p.used = append(p.used, ents[n])
			}
		}
	}
	n := max(2, min(150, int(math.Round(math.Exp(r.NormFloat64()*0.8+math.Log(17))))))
	for range n {
		e := p.used[0]
		if r.Float64() < 0.35 {
			e = p.used[r.IntN(len(p.used))]
		}
		c := call{ent: e, limit: 10 + r.IntN(90)}
		x := r.Float64()
		switch {
		case x < 0.52:
			c.op = "query"
		case x < 0.67:
			c.op = "get"
		case x < 0.82:
			c.op = "update1"
		case x < 0.855:
			c.op = "updateN"
		case x < 0.945:
			c.op = "create"
		case x < 0.96:
			c.op = "bulk"
		default:
			c.op = "delete"
		}
		c.preds = pick(r, e.fields, 1+r.IntN(3), false)
		c.sets = pick(r, e.fields, 1+r.IntN(5), true)
		c.order = e.fields[r.IntN(len(e.fields))]
		if c.op == "query" && len(e.edges) > 0 && r.Float64() < 0.33 {
			c.with = e.edges[r.IntN(len(e.edges))]
		}
		c.inTx = c.op != "query" && c.op != "get" && r.Float64() < 0.25
		p.calls = append(p.calls, c)
	}
	p.filler = max(0, int(math.Round(math.Exp(r.NormFloat64()*0.8+math.Log(1200))))-n*14) / 18
	p.filler = min(p.filler, 600)
	return p
}

func pick(r *rand.Rand, fs []fld, n int, noID bool) []fld {
	var out []fld
	seen := map[string]bool{}
	for range n * 3 {
		f := fs[r.IntN(len(fs))]
		if seen[f.pascal] || noID && (f.isID || f.imm) {
			continue
		}
		seen[f.pascal] = true
		out = append(out, f)
		if len(out) == n {
			break
		}
	}
	if len(out) == 0 {
		for _, f := range fs {
			if !(noID && (f.isID || f.imm)) {
				return append(out, f)
			}
		}
		out = append(out, fs[0])
	}
	return out
}

// renderList renders a list endpoint per entity the service uses.
func renderList(flavor string, p *service) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("// Code generated by gen_services.go. DO NOT EDIT.\n\npackage %s\n\nimport (\n\t\"context\"\n\n", p.pkg)
	if flavor == "velox" {
		w("\t\"%[1]s/velox/entity\"\n\t\"%[1]s/velox/filter\"\n\tgqlrelay \"github.com/syssam/velox/contrib/graphql/gqlrelay\"\n)\n", module)
	} else {
		w("\tclient \"%[1]s/vx\"\n\t\"%[1]s/vx/entity\"\n\t\"%[1]s/vx/filter\"\n\t\"github.com/syssam/velox/vx/orm\"\n)\n", module)
	}
	for _, e := range p.used {
		if flavor == "velox" {
			w("\nfunc (s *Service) List%[1]s(ctx context.Context, after *gqlrelay.Cursor, first *int, before *gqlrelay.Cursor, last *int, orderBy *entity.%[1]sOrder, where *filter.%[1]sWhereInput) (*entity.%[1]sConnection, error) {\n\treturn s.client.%[1]s.Query().Paginate(ctx, after, first, before, last,\n\t\tentity.With%[1]sOrder(orderBy),\n\t\tentity.With%[1]sFilter(where.Filter))\n}\n", e.name)
		} else {
			w("\nfunc (s *Service) List%[1]s(ctx context.Context, after *orm.Cursor, first *int, before *orm.Cursor, last *int, orderBy *entity.%[1]sOrder, where *filter.%[1]sWhereInput) (*entity.%[1]sConnection, error) {\n\treturn client.%[1]s.Query(s.db).\n\t\tWhere(where.P()).\n\t\tOrderBy(orderBy.Term()).\n\t\tPaginate(ctx, after, first, before, last)\n}\n", e.name)
		}
	}
	return b.String()
}

func render(flavor string, p *service) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("// Code generated by gen_services.go. DO NOT EDIT.\n\npackage %s\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"fmt\"\n\t\"strings\"\n\t\"time\"\n\n", p.pkg)
	if flavor == "velox" {
		w("\t\"%s/velox\"\n\t\"%s/velox/entity\"\n", module, module)
		for _, e := range p.used {
			w("\t\"%s/velox/%s\"\n", module, e.pkg)
		}
	} else {
		w("\t\"github.com/syssam/velox/vx/orm\"\n\tclient \"%s/vx\"\n\t\"%s/vx/entity\"\n", module, module)
		for _, e := range p.used {
			w("\t\"%s/vx/%s\"\n", module, e.pkg)
		}
	}
	w(")\n\n// Service makes the ORM calls of a mock service.\ntype Service struct {\n")
	if flavor == "velox" {
		w("\tclient *velox.Client\n}\n\nfunc New(c *velox.Client) *Service { return &Service{client: c} }\n\n")
	} else {
		w("\tdb *orm.DB\n}\n\nfunc New(db *orm.DB) *Service { return &Service{db: db} }\n\n")
	}
	w("var (\n\t_ = errors.New\n\t_ = fmt.Sprint\n\t_ = strings.ToUpper\n\t_ time.Time\n")
	for _, e := range p.used {
		if flavor == "velox" {
			w("\t_ = %s.Table\n", e.pkg)
		} else {
			w("\t_ = %s.Table\n", e.pkg)
		}
	}
	if flavor == "velox" {
		w("\t_ *entity.%s\n", p.used[0].name)
	} else {
		w("\t_ *entity.%s\n\t_ = client.%s\n", p.used[0].name, p.used[0].name)
	}
	w(")\n")
	for i, c := range p.calls {
		if flavor == "velox" {
			veloxCall(w, i, c)
		} else {
			vxCall(w, i, c)
		}
	}
	for j := range p.filler {
		w(filler, j, j, 8+j%40, 8+j%40, 1+j%7)
	}
	return b.String()
}

func veloxPreds(c call) string {
	var ps []string
	for _, f := range c.preds {
		op := "EQ"
		if f.lit == "1" || f.lit == "now" {
			op = "GTE"
		}
		ps = append(ps, fmt.Sprintf("%s.%sField.%s(%s)", c.ent.pkg, f.pascal, op, f.lit))
	}
	return strings.Join(ps, ", ")
}

func vxPreds(c call) string {
	var ps []string
	for _, f := range c.preds {
		op := "EQ"
		if f.lit == "1" || f.lit == "now" {
			op = "GTE"
		}
		ps = append(ps, fmt.Sprintf("%s.%s.%s(%s)", c.ent.pkg, f.pascal, op, f.lit))
	}
	return strings.Join(ps, ", ")
}

func veloxCall(w func(string, ...any), i int, c call) {
	e := c.ent
	cl := "s.client"
	head := func(ret string) {
		w("\nfunc (s *Service) op%d(ctx context.Context, id int64, now time.Time) %s {\n", i, ret)
	}
	tx := func(body string) {
		w("\ttx, err := s.client.Tx(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer func() { _ = tx.Rollback() }()\n%s\treturn tx.Commit()\n", body)
	}
	switch c.op {
	case "query":
		head(fmt.Sprintf("([]*entity.%s, error)", e.name))
		w("\treturn %s.%s.Query().\n\t\tWhere(%s).\n", cl, e.name, veloxPreds(c))
		if c.with != "" {
			w("\t\tWith%s().\n", c.with)
		}
		w("\t\tOrder(velox.Desc(%s.Field%s)).\n\t\tLimit(%d).\n\t\tAll(ctx)\n}\n", e.pkg, c.order.pascal, c.limit)
	case "get":
		head(fmt.Sprintf("(*entity.%s, error)", e.name))
		w("\treturn %s.%s.Get(ctx, id)\n}\n", cl, e.name)
	case "update1":
		head("error")
		call := fmt.Sprintf("\t\t_, err = tx.%s.UpdateOneID(id)%s.Save(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n", e.name, setters(c, "velox"))
		if c.inTx {
			tx(call)
		} else {
			w("\t_, err := %s.%s.UpdateOneID(id)%s.Save(ctx)\n\treturn err\n}\n", cl, e.name, setters(c, "velox"))
			return
		}
		w("}\n")
	case "updateN":
		head("(int, error)")
		w("\treturn %s.%s.Update().Where(%s)%s.Save(ctx)\n}\n", cl, e.name, veloxPreds(c), setters(c, "velox"))
	case "create":
		head(fmt.Sprintf("(*entity.%s, error)", e.name))
		w("\treturn %s.%s.Create()%s.Save(ctx)\n}\n", cl, e.name, setters(c, "velox"))
	case "bulk":
		head("error")
		w("\t_, err := %s.%s.CreateBulk(\n\t\t%s.%s.Create()%s,\n\t\t%s.%s.Create()%s,\n\t).Save(ctx)\n\treturn err\n}\n", cl, e.name, cl, e.name, setters(c, "velox"), cl, e.name, setters(c, "velox"))
	case "delete":
		head("error")
		w("\treturn %s.%s.DeleteOneID(id).Exec(ctx)\n}\n", cl, e.name)
	}
}

func vxCall(w func(string, ...any), i int, c call) {
	e := c.ent
	head := func(ret string) {
		w("\nfunc (s *Service) op%d(ctx context.Context, id int64, now time.Time) %s {\n", i, ret)
	}
	switch c.op {
	case "query":
		head(fmt.Sprintf("([]*entity.%s, error)", e.name))
		w("\treturn client.%s.Query(s.db).\n\t\tWhere(%s).\n", e.name, vxPreds(c))
		if c.with != "" {
			w("\t\tWith%s().\n", c.with)
		}
		w("\t\tOrderBy(%s.%s.Desc()).\n\t\tLimit(%d).\n\t\tAll(ctx)\n}\n", e.pkg, c.order.pascal, c.limit)
	case "get":
		head(fmt.Sprintf("(*entity.%s, error)", e.name))
		w("\treturn client.%s.Get(ctx, s.db, id)\n}\n", e.name)
	case "update1":
		head("error")
		if c.inTx {
			w("\treturn s.db.RunTx(ctx, func(tx *orm.Tx) error {\n\t\t_, err := client.%s.UpdateByID(tx, id)%s.SaveOne(ctx)\n\t\treturn err\n\t})\n}\n", e.name, vxSets(c))
		} else {
			w("\t_, err := client.%s.UpdateByID(s.db, id)%s.SaveOne(ctx)\n\treturn err\n}\n", e.name, vxSets(c))
		}
	case "updateN":
		head("(int, error)")
		w("\treturn client.%s.Update(s.db).Where(%s)%s.Exec(ctx)\n}\n", e.name, vxPreds(c), vxSets(c))
	case "create":
		head(fmt.Sprintf("(*entity.%s, error)", e.name))
		w("\treturn client.%s.Create(s.db)%s.Save(ctx)\n}\n", e.name, vxSets(c))
	case "bulk":
		head("error")
		w("\t_, err := client.%s.CreateBulk(s.db,\n\t\tclient.%s.Create(s.db)%s,\n\t\tclient.%s.Create(s.db)%s,\n\t).Save(ctx)\n\treturn err\n}\n", e.name, e.name, vxSets(c), e.name, vxSets(c))
	case "delete":
		head("error")
		w("\treturn client.%s.DeleteByID(ctx, s.db, id)\n}\n", e.name)
	}
}

func setters(c call, flavor string) string {
	var b strings.Builder
	for _, f := range c.sets {
		fmt.Fprintf(&b, ".Set%s(%s)", f.pascal, f.lit)
	}
	return b.String()
}

// vxSets renders the setters of a call: .Set(pkg.Field.Set(v), ...).
func vxSets(c call) string {
	var ss []string
	for _, f := range c.sets {
		ss = append(ss, fmt.Sprintf("%s.%s.Set(%s)", c.ent.pkg, f.pascal, f.lit))
	}
	return ".Set(" + strings.Join(ss, ", ") + ")"
}

const filler = `
func filler%d(in []string, n int) (map[string]int, error) {
	out := make(map[string]int, len(in))
	for i, s := range in {
		if s == "" {
			return nil, fmt.Errorf("svc: empty value at %%d (step %d)", i)
		}
		key := strings.ToUpper(strings.TrimSpace(s))
		if len(key) > %d {
			key = key[:%d]
		}
		out[key] += n + i*%d
	}
	if len(out) == 0 {
		return nil, errors.New("svc: nothing to do")
	}
	return out, nil
}
`
