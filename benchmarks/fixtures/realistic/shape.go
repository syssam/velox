//go:build ignore

// shape prints the statistics of the fixture's schema that README.md compares
// with the application it models.
//
//	go run shape.go
package main

import (
	"fmt"
	"log"
	"slices"
	"sort"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func quantiles(v []int) string {
	slices.Sort(v)
	at := func(p float64) int { return v[int(float64(len(v))*p)] }
	return fmt.Sprintf("p10=%d p25=%d p50=%d p75=%d p90=%d p99=%d max=%d", at(.1), at(.25), at(.5), at(.75), at(.9), at(.99), v[len(v)-1])
}

func main() {
	cfg, err := gen.NewConfig(gen.WithTarget("./velox"), gen.WithPackage("example.com/realistic/velox"))
	if err != nil {
		log.Fatal(err)
	}
	g, err := compiler.LoadGraph("./schema", cfg)
	if err != nil {
		log.Fatal(err)
	}
	var fields, edges []int
	types := map[string]int{}
	var total, opt, nill, def, enum, js, imm, val, pol, hooks, idx, unique int
	rels := map[string]int{}
	for _, t := range g.Nodes {
		fields = append(fields, len(t.Fields))
		edges = append(edges, len(t.Edges))
		for _, f := range t.Fields {
			total++
			types[f.Type.Type.String()]++
			if f.Optional {
				opt++
			}
			if f.Nillable {
				nill++
			}
			if f.Default {
				def++
			}
			if f.IsEnum() {
				enum++
			}
			if f.IsJSON() {
				js++
			}
			if f.Immutable {
				imm++
			}
			if f.Validators > 0 {
				val++
			}
		}
		for _, x := range t.Edges {
			rels[x.Rel.Type.String()]++
			if x.Unique {
				unique++
			}
		}
		if len(t.PolicyPositions()) > 0 {
			pol += len(t.PolicyPositions())
		}
		hooks += len(t.HookPositions())
		idx += len(t.Indexes)
	}
	n := len(g.Nodes)
	fmt.Printf("entities %d, fields %d (mean %.1f), edges %d (mean %.2f)\n", n, total, float64(total)/float64(n), sum(edges), float64(sum(edges))/float64(n))
	fmt.Println("fields per entity:", quantiles(fields))
	fmt.Println("edges per entity: ", quantiles(edges))
	var ks []string
	for k := range types {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return types[ks[i]] > types[ks[j]] })
	for _, k := range ks {
		fmt.Printf("  %-16s %.3f\n", k, float64(types[k])/float64(total))
	}
	p := func(name string, v int) { fmt.Printf("  %-10s %.3f\n", name, float64(v)/float64(total)) }
	p("optional", opt)
	p("nillable", nill)
	p("default", def)
	p("enum", enum)
	p("json", js)
	p("immutable", imm)
	p("validated", val)
	fmt.Printf("relations %v, unique edges %d\n", rels, unique)
	fmt.Printf("policies %d, hooks %d, indexes per entity %.2f\n", pol, hooks, float64(idx)/float64(n))
}

func sum(v []int) (s int) {
	for _, x := range v {
		s += x
	}
	return
}
