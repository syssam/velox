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
// refuses any other option, as the plugin protocol asks. It warns of each
// many-to-many edge whose create or update input has no field that adds
// links, as its edge schema has a column Add cannot fill, and under
// compat=veloxe of each order it drops from a field of a type veloxe did not
// order, such as a decimal (sdl.Surface's Warnings).
//
// sdlfiles=entity writes the SDL as velox.graphql and a file per entity,
// and sdlfiles=one as velox.graphql alone; one is the default, and entity
// is under compat=veloxe, as veloxe wrote it (sdl.Config's PerEntity).
// entities=A+B writes the SDL of the entities A and B alone (sdl.Config's
// Entities); the Go is the whole schema's.
func Generate(req *plugin.Request) ([]plugin.File, []string, error) {
	cfg, err := config(req)
	if err != nil {
		return nil, nil, err
	}
	if req.Options["only"] == "enums" {
		if cfg.Veloxe || cfg.WhereWhitelist || req.Options["sdlfiles"] != "" || len(cfg.Entities) > 0 {
			return nil, nil, fmt.Errorf("velox-graphql: compat=veloxe, where=whitelist, sdlfiles and entities shape the SDL velox-graphql writes, and only=enums writes none: give one or the other")
		}
		files, err := gogen.RenderEnums(req.Model, req.OutPath, req.OutPackage)
		return files, nil, err
	}
	sdlFiles, surface, err := sdl.RenderFiles(req.Model, cfg)
	if err != nil {
		return nil, nil, err
	}
	gofiles, err := gogen.RenderSurface(req.Model, surface, req.OutPath, req.OutPackage, cfg)
	if err != nil {
		return nil, nil, err
	}
	return append(sdlFiles, gofiles...), surface.Warnings, nil
}

// config is the sdl.Config of req's options.
func config(req *plugin.Request) (sdl.Config, error) {
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
		case k == "sdlfiles" && (v == "one" || v == "entity"):
		case k == "entities" && v != "":
			for n := range strings.SplitSeq(v, "+") {
				if n == "" || slices.Contains(cfg.Entities, n) {
					return cfg, fmt.Errorf("velox-graphql: entities=%s: name each entity once, separated by +", v)
				}
				cfg.Entities = append(cfg.Entities, n)
			}
		default:
			return cfg, fmt.Errorf("velox-graphql takes the options only=enums, compat=veloxe, where=whitelist, engine=gqlgen or engine=graphql-go, sdlfiles=one or sdlfiles=entity, and entities=A+B; it is given %s", options(req.Options))
		}
	}
	switch req.Options["sdlfiles"] {
	case "entity":
		cfg.PerEntity = true
	case "":
		cfg.PerEntity = cfg.Veloxe
	}
	return cfg, nil
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
