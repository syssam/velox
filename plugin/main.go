package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
)

// Supports holds the versions a plugin was written for, as literals in its
// own source: plugin.Supports{Protocol: 2, Model: 2}.
//
// They are literals, not [ProtocolVersion] and [ModelVersion], so that the
// check means something: a plugin built with a newer velox than it was
// written for is refused, instead of reading a request it does not know.
type Supports struct{ Protocol, Model int }

// GenerateFunc answers one request: the files to write, warnings to show the
// user, or an error that fails the run.
type GenerateFunc func(req *Request) (files []File, warnings []string, err error)

// Run reads a request from in, checks it against s, calls gen and writes the
// response to out. It returns an error only when out cannot be written.
//
// It answers with an error, without calling gen, when s is not what this
// package speaks (the plugin was written for another velox), when the request
// cannot be read, or when its protocol or its model's version is not s's. An
// error from gen is the response's Error, and its files are dropped. A panic
// in gen is recovered into the Error too, as "the plugin panicked: " and the
// panic's value, without the stack; with VELOX_PLUGIN_TRACE=1 in the
// plugin's environment, the panic's stack follows on the next lines. velox-gen
// passes its own environment to the plugin, so VELOX_PLUGIN_TRACE=1 go tool
// velox-gen shows where the plugin panicked. Every response carries Protocol:
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
		return fail("this plugin was written for plugin protocol %d; the velox it is built with speaks %d: update the plugin", s.Protocol, ProtocolVersion)
	case s.Protocol > ProtocolVersion:
		return fail("this plugin was written for plugin protocol %d; the velox it is built with speaks %d: build it with a newer velox", s.Protocol, ProtocolVersion)
	case s.Model < ModelVersion:
		return fail("this plugin was written for model %d; the velox it is built with has model %d: update the plugin", s.Model, ModelVersion)
	case s.Model > ModelVersion:
		return fail("this plugin was written for model %d; the velox it is built with has model %d: build it with a newer velox", s.Model, ModelVersion)
	}
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fail("reading the request: %v", err)
	}
	switch {
	case req.Protocol != s.Protocol:
		return fail("the request is plugin protocol %d; this plugin speaks %d: use velox-gen and the plugin of one velox release", req.Protocol, s.Protocol)
	case req.Model == nil:
		return fail("the request has no model")
	case req.Model.Version != s.Model:
		return fail("the model is version %d; this plugin reads %d: use velox-gen and the plugin of one velox release", req.Model.Version, s.Model)
	}
	if err := req.Model.check(); err != nil {
		return fail("the model is malformed: %v; velox-gen never sends one so", err)
	}
	files, warnings, err := call(gen, &req)
	if err != nil {
		return Response{Warnings: warnings, Error: err.Error()}
	}
	return Response{Files: files, Warnings: warnings}
}

// call is gen(req), with a panic recovered into the error, and its stack
// when VELOX_PLUGIN_TRACE is 1.
func call(gen GenerateFunc, req *Request) (files []File, warnings []string, err error) {
	defer func() {
		if v := recover(); v != nil {
			files, err = nil, fmt.Errorf("the plugin panicked: %v", v)
			if os.Getenv("VELOX_PLUGIN_TRACE") == "1" {
				err = fmt.Errorf("the plugin panicked: %v\n%s", v, debug.Stack())
			}
		}
	}()
	return gen(req)
}

// check reports the first reference in m that names nothing: a nil entity,
// field or edge, a key index out of Fields, or an edge's target, join
// entity, pair, foreign key, or an index's field, that is not in m. A plugin
// reads the model by those, so it gets one that holds.
func (m *Model) check() error {
	byName := make(map[string]*Entity, len(m.Entities))
	for i, e := range m.Entities {
		if e == nil {
			return fmt.Errorf("entity %d is null", i)
		}
		byName[e.Name] = e
	}
	for _, e := range m.Entities {
		if err := e.check(byName); err != nil {
			return fmt.Errorf("entity %s: %w", e.Name, err)
		}
	}
	return nil
}

// check is Model.check of e; byName holds the model's entities.
func (e *Entity) check(byName map[string]*Entity) error {
	for i, f := range e.Fields {
		if f == nil {
			return fmt.Errorf("field %d is null", i)
		}
	}
	for _, k := range e.PrimaryKey {
		if k < 0 || k >= len(e.Fields) {
			return fmt.Errorf("primary key %d is not one of its %d fields", k, len(e.Fields))
		}
	}
	for i, ix := range e.Indexes {
		if ix == nil {
			return fmt.Errorf("index %d is null", i)
		}
		for _, f := range ix.Fields {
			if e.field(f) == nil {
				return fmt.Errorf("index %s: no field %s", ix.Name, f)
			}
		}
	}
	for i, ed := range e.Edges {
		if ed == nil {
			return fmt.Errorf("edge %d is null", i)
		}
		if err := ed.check(byName); err != nil {
			return fmt.Errorf("edge %s: %w", ed.Name, err)
		}
	}
	return nil
}

// check is Model.check of ed; byName holds the model's entities.
func (ed *Edge) check(byName map[string]*Entity) error {
	t := byName[ed.Target]
	switch {
	case t == nil:
		return fmt.Errorf("no target entity %s", ed.Target)
	case ed.Ref != "" && !slices.ContainsFunc(t.Edges, func(r *Edge) bool { return r != nil && r.Name == ed.Ref }):
		return fmt.Errorf("its pair %s is not an edge of %s", ed.Ref, ed.Target)
	case ed.Join != "" && byName[ed.Join] == nil:
		return fmt.Errorf("no join entity %s", ed.Join)
	case ed.Field != "" && byName[ed.FieldOn] == nil:
		return fmt.Errorf("no entity %s, which holds its key", ed.FieldOn)
	case ed.Field != "" && byName[ed.FieldOn].field(ed.Field) == nil:
		return fmt.Errorf("its key %s is not a field of %s", ed.Field, ed.FieldOn)
	}
	return nil
}

// field is e's field of the given Name, or nil.
func (e *Entity) field(name string) *Field {
	for _, f := range e.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}
