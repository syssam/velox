package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Supports holds the versions a plugin was written for, as literals in its
// own source: plugin.Supports{Protocol: 2, Model: 2}.
//
// They are literals, not [ProtocolVersion] and [ModelVersion], so that the
// check means something: a plugin built with a newer velox2 than it was
// written for is refused, instead of reading a request it does not know.
type Supports struct{ Protocol, Model int }

// GenerateFunc answers one request: the files to write, warnings to show the
// user, or an error that fails the run.
type GenerateFunc func(req *Request) (files []File, warnings []string, err error)

// Run reads a request from in, checks it against s, calls gen and writes the
// response to out. It returns an error only when out cannot be written.
//
// It answers with an error, without calling gen, when s is not what this
// package speaks (the plugin was written for another velox2), when the request
// cannot be read, or when its protocol or its model's version is not s's. An
// error from gen is the response's Error, and its files are dropped. A panic
// in gen is recovered into the Error too, as "the plugin panicked: " and the
// panic's value, without the stack. Every response carries Protocol:
// [ProtocolVersion].
func Run(s Supports, in io.Reader, out io.Writer, gen GenerateFunc) error {
	resp := answer(s, in, gen)
	resp.Protocol = ProtocolVersion
	return json.NewEncoder(out).Encode(resp)
}

// Main is Run on standard input and output; it exits 1 when Run fails.
func Main(s Supports, gen GenerateFunc) {
	if err := Run(s, os.Stdin, os.Stdout, gen); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(os.Args[0]), err)
		os.Exit(1)
	}
}

func answer(s Supports, in io.Reader, gen GenerateFunc) Response {
	fail := func(format string, args ...any) Response { return Response{Error: fmt.Sprintf(format, args...)} }
	switch {
	case s.Protocol < ProtocolVersion:
		return fail("this plugin was written for plugin protocol %d; the velox2 it is built with speaks %d: update the plugin", s.Protocol, ProtocolVersion)
	case s.Protocol > ProtocolVersion:
		return fail("this plugin was written for plugin protocol %d; the velox2 it is built with speaks %d: build it with a newer velox2", s.Protocol, ProtocolVersion)
	case s.Model < ModelVersion:
		return fail("this plugin was written for model %d; the velox2 it is built with has model %d: update the plugin", s.Model, ModelVersion)
	case s.Model > ModelVersion:
		return fail("this plugin was written for model %d; the velox2 it is built with has model %d: build it with a newer velox2", s.Model, ModelVersion)
	}
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fail("reading the request: %v", err)
	}
	switch {
	case req.Protocol != s.Protocol:
		return fail("the request is plugin protocol %d; this plugin speaks %d: use velox-gen and the plugin of one velox2 release", req.Protocol, s.Protocol)
	case req.Model == nil:
		return fail("the request has no model")
	case req.Model.Version != s.Model:
		return fail("the model is version %d; this plugin reads %d: use velox-gen and the plugin of one velox2 release", req.Model.Version, s.Model)
	}
	files, warnings, err := call(gen, &req)
	if err != nil {
		return Response{Warnings: warnings, Error: err.Error()}
	}
	return Response{Files: files, Warnings: warnings}
}

// call is gen(req), with a panic recovered into the error.
func call(gen GenerateFunc, req *Request) (files []File, warnings []string, err error) {
	defer func() {
		if v := recover(); v != nil {
			files, err = nil, fmt.Errorf("the plugin panicked: %v", v)
		}
	}()
	return gen(req)
}
