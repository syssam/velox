package gen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/imports"
)

// TemplateWriter generates code using templates with parallel execution
// and optimized formatting (using go/format library instead of CLI).
type TemplateWriter struct {
	graph   *Graph
	tmpl    *Template
	outDir  string
	workers int

	// Metrics for performance monitoring
	mu      sync.Mutex
	metrics *WriterMetrics
}

// WriterMetrics tracks generation performance
type WriterMetrics struct {
	FilesGenerated int
	TotalBytes     int64
	TemplateTime   int64 // nanoseconds
	FormatTime     int64 // nanoseconds
	WriteTime      int64 // nanoseconds
}

// NewTemplateWriter creates a new template-based writer.
func NewTemplateWriter(g *Graph, tmpl *Template, outDir string) *TemplateWriter {
	return &TemplateWriter{
		graph:   g,
		tmpl:    tmpl,
		outDir:  outDir,
		workers: runtime.GOMAXPROCS(0),
		metrics: &WriterMetrics{},
	}
}

// WithWorkers sets the number of parallel workers.
func (w *TemplateWriter) WithWorkers(n int) *TemplateWriter {
	if n > 0 {
		w.workers = n
	}
	return w
}

// Metrics returns the generation metrics.
func (w *TemplateWriter) Metrics() *WriterMetrics {
	return w.metrics
}

// GenerateAll generates all files using templates in parallel.
func (w *TemplateWriter) GenerateAll(ctx context.Context) error {
	// Ensure output directory exists
	if err := os.MkdirAll(w.outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Collect all files to generate
	var files []fileTask

	// Per-type templates
	for _, t := range w.graph.Nodes {
		for _, tmpl := range Templates {
			if tmpl.Cond != nil && !tmpl.Cond(t) {
				continue
			}
			files = append(files, fileTask{
				name:     tmpl.Format(t),
				template: tmpl.Name,
				data:     t,
			})
		}
	}

	// Graph-level templates
	for _, tmpl := range GraphTemplates {
		if tmpl.Skip != nil && tmpl.Skip(w.graph) {
			continue
		}
		files = append(files, fileTask{
			name:     tmpl.Format,
			template: tmpl.Name,
			data:     w.graph,
		})
	}

	// Generate files in parallel
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(w.workers)

	for _, f := range files {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return w.generateFile(f)
			}
		})
	}

	return eg.Wait()
}

// fileTask represents a single file generation task.
type fileTask struct {
	name     string // output file path (relative to outDir)
	template string // template name to execute
	data     any    // data to pass to template
}

// generateFile generates a single file.
func (w *TemplateWriter) generateFile(f fileTask) error {
	// 1. Execute template
	var buf bytes.Buffer
	if err := w.tmpl.ExecuteTemplate(&buf, f.template, f.data); err != nil {
		return fmt.Errorf("execute template %q for %s: %w", f.template, f.name, err)
	}

	// 2. Format using goimports (removes unused imports and adds missing ones)
	fullPath := filepath.Join(w.outDir, f.name)
	formatted, err := imports.Process(fullPath, buf.Bytes(), nil)
	if err != nil {
		// Write unformatted file for debugging (errors intentionally ignored as we're already in error state)
		debugPath := fullPath + ".error"
		_ = os.MkdirAll(filepath.Dir(debugPath), 0o755)
		_ = os.WriteFile(debugPath, buf.Bytes(), 0o644)
		return fmt.Errorf("format %s: %w (unformatted written to %s)", f.name, err, debugPath)
	}

	// 3. Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", f.name, err)
	}

	// 4. Write file
	if err := os.WriteFile(fullPath, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", f.name, err)
	}

	// Update metrics
	w.mu.Lock()
	w.metrics.FilesGenerated++
	w.metrics.TotalBytes += int64(len(formatted))
	w.mu.Unlock()

	return nil
}

// GenerateType generates all files for a single type.
func (w *TemplateWriter) GenerateType(ctx context.Context, t *Type) error {
	var files []fileTask

	for _, tmpl := range Templates {
		if tmpl.Cond != nil && !tmpl.Cond(t) {
			continue
		}
		files = append(files, fileTask{
			name:     tmpl.Format(t),
			template: tmpl.Name,
			data:     t,
		})
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(w.workers)

	for _, f := range files {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return w.generateFile(f)
			}
		})
	}

	return eg.Wait()
}

// GenerateGraph generates all graph-level files.
func (w *TemplateWriter) GenerateGraph(ctx context.Context) error {
	var files []fileTask

	for _, tmpl := range GraphTemplates {
		if tmpl.Skip != nil && tmpl.Skip(w.graph) {
			continue
		}
		files = append(files, fileTask{
			name:     tmpl.Format,
			template: tmpl.Name,
			data:     w.graph,
		})
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(w.workers)

	for _, f := range files {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return w.generateFile(f)
			}
		})
	}

	return eg.Wait()
}

// GenerateTemplates is the convenience function to generate code using templates.
// It replaces the Jennifer-based generation for backward compatibility with templates.
func GenerateTemplates(g *Graph) error {
	if g.Config == nil || g.Config.Target == "" {
		return NewConfigError("Target", nil, "missing target directory in config")
	}

	// Initialize templates
	initTemplates()

	// Create writer with templates
	w := NewTemplateWriter(g, templates, g.Config.Target)

	// Generate all files
	return w.GenerateAll(context.Background())
}
