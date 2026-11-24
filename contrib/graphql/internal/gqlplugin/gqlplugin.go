// Package gqlplugin is velox-graphql's answer to a request, which the tool
// and package graphqlgen share.
package gqlplugin

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/gogen"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// Supports is the protocol and the model velox-graphql is written for.
var Supports = plugin.Supports{Protocol: 2, Model: 3}

// Generate answers a request: the schema, velox.graphql, and the Go files
// gqlgen binds it to; or with only=enums, the enum marshalers alone.
//
// The options names=, filters=, node=, idops=, orderfields=, queryfields=,
// optional=, requirededges=, inputkeys= and jsontypes= shape the SDL, each
// apart, as sdl.Config's fields of those names say; each refuses a value it
// does not know. With where=whitelist, only fields with WhereInput filter
// (sdl.Config's WhereWhitelist); with engine=graphql-go, the schema and the
// Go are for graphql-go's gqlc (sdl.Config's GraphQLGo), and engine=gqlgen
// is the default. only=enums writes the same Go for either engine.
// sdlfiles=entity writes the SDL as velox.graphql and a file per entity,
// and sdlfiles=one, the default, as velox.graphql alone (sdl.Config's
// PerEntity). entities=A+B writes the SDL of the entities A and B alone
// (sdl.Config's Entities); the Go is the whole schema's. apptypes=A+B names
// the types the application declares (sdl.Config's AppTypes).
//
// For gqlgen it refuses a gqlgen configuration that binds nothing from
// gqlgenx (checkGQLGenConfig), unless gqlgenx=blank says the server
// imports gqlgenx blank (decision 0173).
//
// It refuses any other option, as the plugin protocol asks; compat= is
// refused with the options that replace it. It warns of each many-to-many
// edge whose create or update input has no field that adds links, as its
// edge schema has a column Add cannot fill, and of each annotation that has
// no effect (sdl.Surface's Warnings).
func Generate(req *plugin.Request) ([]plugin.File, []string, error) {
	cfg, err := config(req)
	if err != nil {
		return nil, nil, err
	}
	if req.Options["only"] == "enums" {
		for k := range req.Options {
			if k != "only" && k != "engine" {
				return nil, nil, fmt.Errorf("velox-graphql: %s shapes the SDL velox-graphql writes, and only=enums writes none: give one or the other", k)
			}
		}
		files, err := gogen.RenderEnums(req.Model, req.OutPath, req.OutPackage)
		return files, nil, err
	}
	if !cfg.GraphQLGo && req.Options["gqlgenx"] != "blank" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("velox-graphql: %w", err)
		}
		if err := checkGQLGenConfig(dir, req.OutPath); err != nil {
			return nil, nil, err
		}
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

// compatGone is the error for the option compat=, which is gone: each
// option that replaces one of its parts.
const compatGone = "velox-graphql: compat= is gone; give the options it stood for, each apart: names=schema, filters=lenient, node=on, sdlfiles=entity, orderfields=all, queryfields=all, optional=nullable, requirededges=nonnull, jsontypes=named, idops=equality, inputkeys=column and apptypes= naming the scalars the application declares (such as JSON+UUID+Uint64)"

// config is the sdl.Config of req's options.
func config(req *plugin.Request) (sdl.Config, error) {
	var cfg sdl.Config
	for k, v := range req.Options {
		switch {
		case k == "compat":
			return cfg, errors.New(compatGone)
		case k == "only" && v == "enums":
		case v != "" && cfg.SetOption(k, v):
			if k == "node" {
				cfg.Package = req.OutPath
			}
		case k == "where" && v == "whitelist":
			cfg.WhereWhitelist = true
		case k == "engine" && v == "gqlgen":
		case k == "gqlgenx" && v == "blank":
		case k == "engine" && v == "graphql-go":
			cfg.GraphQLGo, cfg.Package = true, req.OutPath
		case k == "sdlfiles" && v == "one":
		case k == "sdlfiles" && v == "entity":
			cfg.PerEntity = true
		case k == "entities" && v != "":
			for n := range strings.SplitSeq(v, "+") {
				if n == "" || slices.Contains(cfg.Entities, n) {
					return cfg, fmt.Errorf("velox-graphql: entities=%s: name each entity once, separated by +", v)
				}
				cfg.Entities = append(cfg.Entities, n)
			}
		case k == "apptypes" && v != "":
			for n := range strings.SplitSeq(v, "+") {
				if n == "" || slices.Contains(cfg.AppTypes, n) {
					return cfg, fmt.Errorf("velox-graphql: apptypes=%s: name each type once, separated by +", v)
				}
				cfg.AppTypes = append(cfg.AppTypes, n)
			}
		default:
			return cfg, fmt.Errorf("velox-graphql takes the options only=enums, %s, where=whitelist, engine=gqlgen or engine=graphql-go, gqlgenx=blank, sdlfiles=one or sdlfiles=entity, entities=A+B and apptypes=A+B; it is given %s", strings.Join(sdl.OptionHelp(), ", "), options(req.Options))
		}
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
