// Package generate runs velox-gen's generate from Go: a program, such as a
// generate.go that go:generate runs, calls Run with a Config in place of the
// command's flags.
//
// Run does what "go tool velox-gen" does with the same settings, and writes
// the same files: Schema is the package argument, Out is -out, Package is
// -package, Plugins are the -plugin flags and Check is -check. Run also
// takes what the command cannot: a plugin called in the process, with no
// tool to build.
//
// A Config that only the program can express is checked by the program:
// run it with Check set in CI, in place of velox-gen -check. velox-gen
// -check and verify know nothing of a plugin's Run.
package generate

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/plugin"
)

// Config is what Run generates, and how.
//
// The zero Config is velox-gen with no flags: it generates ./veloxgen,
// package veloxgen, from ./veloxschema.
type Config struct {
	// Schema is the schema package's pattern, relative to Run's dir.
	// It is "./veloxschema" when empty.
	Schema string
	// Out is the generated package's directory, relative to Run's dir unless
	// it is absolute.
	// It is "./veloxgen" when empty.
	Out string
	// Package is the generated package's name.
	// It is the base name of Out when empty.
	// An error about it names velox-gen's flag, -package.
	Package string
	// Plugins run on the model in order, after velox-gen's own files are
	// built and before anything is written.
	Plugins []Plugin
	// Check writes nothing.
	// Result.Stale lists each file a run would write or remove.
	// Run returns ErrStale when Result.Stale is not empty.
	Check bool
	// Env is the environment of the go commands Run starts.
	// It is os.Environ() when nil.
	// GOFLAGS=-p=4 in it bounds the builds those commands run.
	Env []string
	// Stdout receives each path Run writes or removes, one per line.
	// It is io.Discard when nil.
	Stdout io.Writer
	// Stderr receives the warnings and the notes velox-gen prints, in its
	// words.
	// It is os.Stderr when nil.
	Stderr io.Writer
}

// Plugin is one generator plugin, as velox-gen's -plugin flag names one.
type Plugin struct {
	// Tool is the plugin's name, as go tool names it, such as
	// "velox-graphql".
	// Run starts it as "go tool <Tool>" in the schema package's module,
	// with the JSON request on its standard input, unless Run is set.
	// It names the plugin in messages and as the owner of its files, and
	// must not be empty.
	Tool string
	// Options are the request's options, as -plugin tool:key=value gives
	// them.
	Options map[string]string
	// Run, if set, answers the request in this process instead of Tool.
	// It is given the request a tool would read, decoded from its JSON, so
	// it may keep or change it.
	// Its response is checked as a tool's: its warnings are printed, a
	// non-empty Error fails the run, and its files' paths obey the plugin
	// protocol.
	// Its Protocol is not checked, as the plugin is built with this velox.
	// An error it returns fails the run, naming Tool.
	// A panic in it fails the run as "plugin <Tool>: panic: <value>".
	Run func(context.Context, *plugin.Request) (*plugin.Response, error)
}

// Result is what Run did.
// Each path is relative to Run's dir when it is inside it.
type Result struct {
	// Written are the files Run wrote, sorted.
	Written []string
	// Removed are the generated files Run removed, sorted.
	Removed []string
	// Stale are the files a Check found out of date, missing or left over
	// from an interrupted run.
	Stale []string
	// Warnings are the warnings Run printed, without "velox-gen: warning: ".
	// A plugin's starts with "plugin <tool>: ".
	Warnings []string
}

// ErrStale is the error of a Check that finds a stale file.
var ErrStale = genrun.ErrStale

// ErrInterrupted is the error of a run that SIGINT or SIGTERM reached
// while it wrote.
// Run holds both signals while it writes, so that the write finishes or is
// undone.
// When the write failed too, Run returns both errors, joined.
var ErrInterrupted = genrun.ErrInterrupted

// Run generates the package cfg describes, as velox-gen's generate does.
//
// dir is the directory that cfg's relative paths start from, and where the
// go commands run.
// It is the current directory when empty.
//
// Run writes nothing when anything fails: a schema rule, a plugin, a file of
// yours in the way.
// A write that fails part-way is undone.
// It writes, rewrites and removes only files that start with velox-gen's
// header.
// An error names files relative to dir.
//
// ctx stops the go list commands that find the schema package, its module
// and the generated package's import path, and the tool plugins.
// Run also checks ctx before it loads the schema and before it writes.
// ctx is given to an in-process Plugin.Run.
// The build and run of the program that loads the schema, and velox-gen's
// checks of the schema's behaviour, do not see ctx.
func Run(ctx context.Context, dir string, cfg Config) (Result, error) {
	plugins := make([]genrun.Plugin, len(cfg.Plugins))
	for i, p := range cfg.Plugins {
		if strings.TrimSpace(p.Tool) == "" {
			return Result{}, fmt.Errorf("plugin %d has no Tool; it names the plugin", i+1)
		}
		plugins[i] = genrun.Plugin{Name: p.Tool, Options: p.Options, Run: p.Run}
	}
	res, err := genrun.Run(ctx, dir, genrun.Config{
		Schema: cfg.Schema, Out: cfg.Out, Package: cfg.Package,
		Plugins: plugins, Check: cfg.Check, Env: cfg.Env,
		Stdout: cfg.Stdout, Stderr: cfg.Stderr,
	})
	return Result(res), err
}
