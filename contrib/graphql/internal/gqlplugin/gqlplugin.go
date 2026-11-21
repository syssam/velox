// Package gqlplugin is velox-graphql's answer to a request, which the tool
// and package graphqlgen share.
package gqlplugin

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/gogen"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// Supports is the protocol and the model velox-graphql is written for.
var Supports = plugin.Supports{Protocol: 2, Model: 3}

// Generate answers a request: the schema, velox.graphql, and the Go files
// gqlgen binds it to; or with only=enums, the enum marshalers alone. With
// compat=veloxe, the API is the one veloxe's generator made (sdl.Config's
// Veloxe); with where=whitelist, only fields with WhereInput filter
// (sdl.Config's WhereWhitelist); with engine=graphql-go, the schema and the
// Go are for graphql-go's gqlc (sdl.Config's GraphQLGo), and engine=gqlgen
// is the default. only=enums writes the same Go for either engine. It
// refuses any other option, as the plugin protocol asks.
func Generate(req *plugin.Request) ([]plugin.File, []string, error) {
	var cfg sdl.Config
	for k, v := range req.Options {
		switch {
		case k == "only" && v == "enums":
		case k == "compat" && v == "veloxe":
			cfg.Veloxe, cfg.Package = true, req.OutPath
		case k == "where" && v == "whitelist":
			cfg.WhereWhitelist = true
		case k == "engine" && v == "gqlgen":
		case k == "engine" && v == "graphql-go":
			cfg.GraphQLGo, cfg.Package = true, req.OutPath
		default:
			return nil, nil, fmt.Errorf("velox-graphql takes the options only=enums, compat=veloxe, where=whitelist and engine=gqlgen or engine=graphql-go; it is given %s", options(req.Options))
		}
	}
	if req.Options["only"] == "enums" {
		if cfg.Veloxe || cfg.WhereWhitelist {
			return nil, nil, fmt.Errorf("velox-graphql: compat=veloxe and where=whitelist shape the SDL velox-graphql writes, and only=enums writes none: give one or the other")
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
