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
// It takes one option. An application that writes its own SDL and binds it
// with gqlgen's autobind asks for only the marshalers of velox-gen's enum
// types, with no velox.graphql and no other Go:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:only=enums ./veloxschema
//
// A value is then written to GraphQL as the stored one upper-cased, or as
// graphql.EnumValues names it, and read back from it: open is OPEN. Any other
// option fails the run.
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
// gqlgen binds it to; or with only=enums, the enum marshalers alone. It
// refuses any other option, as the plugin protocol asks.
func generate(req *plugin.Request) ([]plugin.File, []string, error) {
	n := len(req.Options)
	if n > 1 || n == 1 && req.Options["only"] != "enums" {
		return nil, nil, fmt.Errorf("velox-graphql takes one option, only=enums; it is given %s", options(req.Options))
	}
	if n == 1 {
		files, err := gogen.RenderEnums(req.Model, req.OutPath, req.OutPackage)
		return files, nil, err
	}
	s, err := sdl.Render(req.Model)
	if err != nil {
		return nil, nil, err
	}
	gofiles, err := gogen.Render(req.Model, req.OutPath, req.OutPackage)
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
