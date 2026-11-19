// Command velox-graphql is the velox-gen plugin that writes the GraphQL
// schema of a velox schema package, veloxgen/velox.graphql, and the Go code in the
// generated package that gqlgen's autobind binds the schema's types to,
// veloxgen/*_gql.go. An application adds it as a tool of its module and names it
// to velox-gen:
//
//	go get -tool github.com/syssam/velox/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out veloxgen -plugin velox-graphql ./veloxschema
//
// velox-gen runs it with the request, the resolved model as JSON, on its
// standard input, and it answers with the files on its standard output (see
// package github.com/syssam/velox/plugin). velox-gen writes them, with the
// generated-file header, and owns them from then on.
//
// It takes two options, one at a time. An application that writes its own
// SDL and binds it with gqlgen's autobind asks for only the marshalers of
// velox-gen's enum types, with no velox.graphql and no other Go:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:only=enums ./veloxschema
//
// A value is then written to GraphQL as the stored one upper-cased, or as
// graphql.EnumValues names it, and read back from it: open is OPEN.
//
// An application whose clients are built against the API veloxe's generator
// made asks for that API:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:compat=veloxe ./veloxschema
//
// A field or edge is then named from its schema name as veloxe named it
// (customer_id is customerId, image_url is imageUrl); a foreign key an edge
// is bound to is an ID in the type, beside its edge; and its where
// predicates are named from it (customerId, customerIdNEQ, customerIdIn,
// customerIdNotIn). Any other option fails the run.
package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/gogen"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

func main() {
	plugin.Main(plugin.Supports{Protocol: 2, Model: 3}, generate)
}

// generate answers a request: the schema, velox.graphql, and the Go files
// gqlgen binds it to; or with only=enums, the enum marshalers alone. With
// compat=veloxe, the API is the one veloxe's generator made (sdl.Config's
// Veloxe). It refuses any other option, as the plugin protocol asks.
func generate(req *plugin.Request) ([]plugin.File, []string, error) {
	var cfg sdl.Config
	for k, v := range req.Options {
		switch {
		case k == "only" && v == "enums":
		case k == "compat" && v == "veloxe":
			cfg.Veloxe = true
		default:
			return nil, nil, fmt.Errorf("velox-graphql takes the options only=enums and compat=veloxe; it is given %s", options(req.Options))
		}
	}
	if req.Options["only"] == "enums" {
		if cfg.Veloxe {
			return nil, nil, fmt.Errorf("velox-graphql: compat=veloxe shapes the SDL velox-graphql writes, and only=enums writes none: give one of them")
		}
		files, err := gogen.RenderEnums(req.Model, req.OutPath, req.OutPackage)
		return files, nil, err
	}
	s, err := sdl.RenderConfig(req.Model, cfg)
	if err != nil {
		return nil, nil, err
	}
	gofiles, err := gogen.RenderConfig(req.Model, req.OutPath, req.OutPackage, cfg)
	if err != nil {
		return nil, nil, err
	}
	return append([]plugin.File{{Path: "velox.graphql", Content: s}}, gofiles...), nil, nil
}

// options are opts as written, in key order: key=value, or key alone.
func options(opts map[string]string) string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		if v := opts[k]; v != "" {
			k += "=" + v
		}
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}
