// Package graphqlgen runs velox-graphql in the process of a program that
// calls velox's package generate, so that the program needs no tool build.
//
// Plugin writes what "-plugin velox-graphql" writes, byte for byte, and
// takes the same options:
//
//	generate.Run(ctx, "", generate.Config{
//		Plugins: []generate.Plugin{graphqlgen.Plugin(map[string]string{"compat": "veloxe"})},
//	})
//
// is "go tool velox-gen -plugin velox-graphql:compat=veloxe". The command's
// package documentation lists the options.
package graphqlgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/syssam/velox/contrib/graphql/internal/gqlplugin"
	"github.com/syssam/velox/generate"
	"github.com/syssam/velox/plugin"
)

// Plugin is velox-graphql with the options opts, called in the process.
//
// It is named "velox-graphql" in messages and as the owner of its files, as
// the tool is.
// It answers through plugin.Run, as the tool does, so it checks the
// request's protocol and model the same way.
// A panic in it is the response's error, as the tool's is.
func Plugin(opts map[string]string) generate.Plugin {
	return generate.Plugin{Tool: "velox-graphql", Options: opts, Run: run}
}

// run answers req as the tool answers it on its standard input.
func run(_ context.Context, req *plugin.Request) (*plugin.Response, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := plugin.Run(gqlplugin.Supports, bytes.NewReader(in), &out, gqlplugin.Generate); err != nil {
		return nil, err
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	return &resp, nil
}
