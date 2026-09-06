package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func watchCmd(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	target := fs.String("target", "", "output directory (default: parent of schema dir)")
	pkg := fs.String("package", "", "output package path")
	debounce := fs.Duration("debounce", 500*time.Millisecond, "debounce duration for file changes")
	verbose := setupVerbose(fs)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Watch schema files and regenerate on changes.

Usage:
  velox watch <schema-path> [flags]

Runs an initial code generation, then watches the schema directory for .go file
changes. When a change is detected, code generation is re-run after a debounce
period. Press Ctrl+C to stop.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), `
Examples:
  velox watch ./schema
  velox watch ./schema --target ./velox
  velox watch ./schema --debounce 1s
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
		return fmt.Errorf("missing schema path\nusage: velox watch <schema-path> [--target <dir>] [--debounce <duration>]")
	}

	schemaPath := resolved.schemaPath
	opts := resolved.opts

	// Listen for shutdown signals.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initial generation.
	slog.Info("velox: running initial code generation")
	if genErr := generate(ctx, schemaPath, opts); genErr != nil {
		slog.Error("velox: initial generation failed", "error", genErr)
		// Continue watching even if initial generation fails.
	} else {
		slog.Info("velox: initial generation complete")
	}

	// Set up file watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	absSchema, err := filepath.Abs(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to resolve schema path: %w", err)
	}

	if err := watcher.Add(absSchema); err != nil {
		return fmt.Errorf("failed to watch %s: %w", absSchema, err)
	}

	slog.Info("velox: watching for changes", "path", absSchema, "debounce", *debounce)

	var timer *time.Timer
	var genMu sync.Mutex // Prevents overlapping generation runs.
	for {
		select {
		case <-ctx.Done():
			slog.Info("velox: shutting down watcher")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Only trigger on .go file changes.
			if !strings.HasSuffix(event.Name, ".go") {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			// Debounce: reset timer on each event.
			if timer != nil {
				timer.Stop()
			}
			changedFile := event.Name // capture by value to avoid data race with loop
			timer = time.AfterFunc(*debounce, func() {
				genMu.Lock()
				defer genMu.Unlock()
				slog.Info("velox: change detected, regenerating", "file", changedFile)
				if err := generate(ctx, schemaPath, opts); err != nil {
					slog.Error("velox: generation failed", "error", err)
				} else {
					slog.Info("velox: regeneration complete")
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("velox: watcher error", "error", err)
		}
	}
}

// generate runs code generation for the given schema path and options.
func generate(ctx context.Context, schemaPath string, opts []gen.Option) error {
	cfg, err := gen.NewConfig(opts...)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return compiler.GenerateContext(ctx, schemaPath, cfg)
}
