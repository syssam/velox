// Command velox-graphql is the velox-gen plugin that writes the GraphQL
// schema of a velox schema package, gen/velox.graphqls, and the Go code in the
// generated package that gqlgen's autobind binds the schema's types to,
// gen/gql_*.go. An application adds it as a tool of its module and names it
// to velox-gen:
//
//	go get -tool github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out gen -plugin velox-graphql ./schema
//
// velox-gen runs it with the request, the resolved model as JSON, on its
// standard input, and it answers with the files on its standard output (see
// package github.com/syssam/velox2/plugin). velox-gen writes them, with the
// generated-file header, and owns them from then on.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/syssam/velox2/contrib/graphql/internal/gogen"
	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

// run answers the request on in with a response on out. A request it cannot
// answer is a response with an error, which fails velox-gen's run; the exit
// code is non-zero only when the response cannot be written.
func run(in io.Reader, out, errs io.Writer) int {
	resp := answer(in)
	if err := json.NewEncoder(out).Encode(resp); err != nil {
		_, _ = fmt.Fprintln(errs, "velox-graphql:", err)
		return 1
	}
	return 0
}

func answer(in io.Reader) plugin.Response {
	var req plugin.Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return plugin.Response{Error: fmt.Sprintf("reading the request: %v", err)}
	}
	switch {
	case req.Protocol != plugin.ProtocolVersion:
		return plugin.Response{Error: fmt.Sprintf("plugin protocol %d is not supported: this velox-graphql speaks %d; use velox-gen and velox-graphql of one velox release", req.Protocol, plugin.ProtocolVersion)}
	case req.Model == nil:
		return plugin.Response{Error: "the request has no model"}
	case req.Model.Version != plugin.ModelVersion:
		return plugin.Response{Error: fmt.Sprintf("model version %d is not supported: this velox-graphql reads %d; use velox-gen and velox-graphql of one velox release", req.Model.Version, plugin.ModelVersion)}
	}
	s, err := sdl.Render(req.Model)
	if err != nil {
		return plugin.Response{Error: err.Error()}
	}
	gofiles, err := gogen.Render(req.Model, req.OutPath, req.OutPackage)
	if err != nil {
		return plugin.Response{Error: err.Error()}
	}
	return plugin.Response{Files: append([]plugin.File{{Path: "velox.graphqls", Content: s}}, gofiles...)}
}
