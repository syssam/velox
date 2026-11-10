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
// generated-file header, and owns them from then on. It takes no options:
// -plugin velox-graphql:key=value fails the run.
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
// gqlgen binds it to. velox-graphql takes no options, and refuses any it is
// given, as the plugin protocol asks.
func generate(req *plugin.Request) ([]plugin.File, []string, error) {
	if len(req.Options) > 0 {
		keys := slices.Sorted(maps.Keys(req.Options))
		return nil, nil, fmt.Errorf("velox-graphql takes no options; it is given %s", strings.Join(keys, ", "))
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
