// Command velox is the code generator for Velox ORM.
//
// Usage:
//
//	go run github.com/syssam/velox/cmd/velox generate ./schema
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler/gen"
)

// version is set via ldflags at build time.
var version = "dev"
var commit = "none"
var date = "unknown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("velox", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		usage(os.Stderr)
		return fmt.Errorf("no command specified")
	}

	switch args[0] {
	case "generate":
		return generateCmd(args[1:])
	case "validate":
		return validateCmd(args[1:])
	case "init":
		return initCmd(args[1:])
	case "watch":
		return watchCmd(args[1:])
	case "version":
		return versionCmd()
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `Velox - Type-safe Go ORM code generator

Usage:
  velox <command> [arguments]

Commands:
  generate    Generate code from schema definitions
  validate    Validate schema without generating code
  init        Scaffold a new Velox project
  watch       Watch schema files and regenerate on changes
  version     Print the version
  help        Show this help

Flags (generate, validate, watch):
  --target <dir>        Output directory (default: parent of schema dir)
  --package <path>      Output Go package import path
  --dry-run             Validate schema and config without writing files (generate only)
  --check               Generate into a temp dir and diff against target; exit 1
                        on drift. No files written. Intended for CI (generate only).
  --debounce <duration> Debounce duration for file changes (watch only, default 500ms)
  --verbose             Enable verbose logging (debug level)

Configuration:
  Velox looks for a .velox.yml file in the current directory (and parent
  directories). CLI flags override config file values. Run "velox init" to
  create a starter config. Supported keys:

    schema:   ./schema        # path to schema directory
    target:   ./velox         # output directory
    package:  mymodule/velox  # output package import path
    features:                 # list of feature names to enable
      - privacy
      - intercept

Examples:
  velox generate ./schema
  velox generate ./schema --target ./velox --package mymodule/velox
  velox generate ./schema --dry-run
  velox generate ./schema --check
  velox validate ./schema
  velox init
  velox watch ./schema
  velox watch ./schema --debounce 1s`)
}

// setupVerbose registers a --verbose flag on the given FlagSet.
func setupVerbose(fs *flag.FlagSet) *bool {
	return fs.Bool("verbose", false, "enable verbose logging (debug level)")
}

// applyVerbose sets the global slog level to Debug if verbose is true.
func applyVerbose(verbose bool) {
	if verbose {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))
	}
}

func versionCmd() error {
	fmt.Printf("velox version %s (commit: %s, built: %s)\n", version, commit, date)
	return nil
}

// configOpts holds the result of config resolution.
type configOpts struct {
	schemaPath string
	opts       []gen.Option
}

// resolveConfig loads config file, determines schema path from args or config,
// and builds gen.Option slice. CLI flag values for target and pkg override config file.
func resolveConfig(args []string, target, pkg string) (*configOpts, error) {
	// Try to load config file from current directory upward.
	var fileCfg *configFile
	if cfgPath := findConfigFile("."); cfgPath != "" {
		var err error
		fileCfg, err = loadConfigFile(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load config file %s: %w", cfgPath, err)
		}
		slog.Info("velox: loaded config", "path", cfgPath)
	}

	// Determine schema path: CLI arg > config file.
	schemaPath := ""
	if len(args) >= 1 {
		schemaPath = args[0]
	} else if fileCfg != nil && fileCfg.Schema != "" {
		schemaPath = fileCfg.Schema
	}

	// Build config options: config file values as defaults, CLI flags override.
	var opts []gen.Option
	if fileCfg != nil && fileCfg.Target != "" {
		opts = append(opts, gen.WithTarget(fileCfg.Target))
	}
	if fileCfg != nil && fileCfg.Package != "" {
		opts = append(opts, gen.WithPackage(fileCfg.Package))
	}

	// Map config file features to gen.Feature values.
	if fileCfg != nil && len(fileCfg.Features) > 0 {
		var features []gen.Feature
		featureMap := make(map[string]gen.Feature, len(gen.AllFeatures))
		for _, f := range gen.AllFeatures {
			featureMap[f.Name] = f
		}
		for _, name := range fileCfg.Features {
			if f, ok := featureMap[name]; ok {
				features = append(features, f)
			} else {
				return nil, fmt.Errorf("unknown feature %q in config file", name)
			}
		}
		opts = append(opts, gen.WithFeatures(features...))
	}

	// CLI flags override config file values.
	if target != "" {
		opts = append(opts, gen.WithTarget(target))
	}
	if pkg != "" {
		opts = append(opts, gen.WithPackage(pkg))
	}

	return &configOpts{schemaPath: schemaPath, opts: opts}, nil
}

func generateCmd(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	target := fs.String("target", "", "output directory (default: parent of schema dir)")
	pkg := fs.String("package", "", "output package path")
	dryRun := fs.Bool("dry-run", false, "validate schema and config without generating code")
	check := fs.Bool("check", false, "generate into a temp dir and diff against target; exit 1 on drift")
	verbose := setupVerbose(fs)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Generate code from schema definitions.

Usage:
  velox generate <schema-path> [flags]

The schema path points to a directory containing Go files with Velox schema
definitions. If omitted, the path is read from .velox.yml.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), `
Examples:
  velox generate ./schema
  velox generate ./schema --target ./velox
  velox generate ./schema --package mymodule/velox
  velox generate ./schema --dry-run
  velox generate ./schema --check
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	applyVerbose(*verbose)

	resolved, err := resolveConfig(fs.Args(), *target, *pkg)
	if err != nil {
		return err
	}
	if resolved.schemaPath == "" {
		return fmt.Errorf("missing schema path\nusage: velox generate <schema-path> [--target <dir>]")
	}

	if *dryRun && *check {
		return fmt.Errorf("--dry-run and --check are mutually exclusive")
	}

	if *dryRun {
		_, err := gen.NewConfig(resolved.opts...)
		if err != nil {
			return fmt.Errorf("config validation failed: %w", err)
		}
		slog.Info("velox: dry-run passed — schema and config are valid")
		return nil
	}

	if *check {
		if err := runCheck(context.Background(), resolved.schemaPath, resolved.opts); err != nil {
			return err
		}
		slog.Info("velox: check passed — generated code is up to date")
		return nil
	}

	if err := generate(context.Background(), resolved.schemaPath, resolved.opts); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}

	slog.Info("velox: code generation complete")
	return nil
}
