package gen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/template/parse"

	"github.com/dave/jennifer/jen"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/imports"
)

// maxDefaultWorkers is the upper bound on parallel generation workers,
// chosen to balance throughput against file-descriptor and memory pressure.
const maxDefaultWorkers = 16

// JenniferGenerator generates code using Jennifer instead of templates.
// This provides better performance by:
// - Auto-tracking imports (no goimports needed)
// - Streaming writes to disk (lower memory)
// - Compile-time type safety
type JenniferGenerator struct {
	graph   *Graph
	workers int
	outDir  string
	pkg     string

	// Dialect generator for database-specific code
	// Requires at least MinimalDialect, but full DialectGenerator is supported
	dialect MinimalDialect

	// Optional interface implementations detected at runtime
	featureGen       FeatureGenerator
	optionalGen      OptionalFeatureGenerator
	migrateGen       MigrateGenerator
	privacyFilterGen PrivacyFilterGenerator
	typesGen         TypesGenerator

	// Track generated enum types to avoid duplicates
	enumsMu        sync.Mutex
	generatedEnums map[string]bool

	// Track all generated file paths for manifest-based cleanup.
	// Protected by manifestMu for concurrent writes from parallel generation.
	manifestMu     sync.Mutex
	generatedFiles []string
}

// NewJenniferGenerator creates a new Jennifer-based generator.
// You must call WithDialect() to set a dialect before calling Generate().
//
// Example:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//
//	gen := gen.NewJenniferGenerator(graph, outDir)
//	dialect := sql.NewDialect(gen)
//	gen.WithDialect(dialect)
//	gen.Generate(ctx)
func NewJenniferGenerator(g *Graph, outDir string) *JenniferGenerator {
	return &JenniferGenerator{
		graph:          g,
		workers:        min(runtime.GOMAXPROCS(0), maxDefaultWorkers),
		outDir:         outDir,
		pkg:            filepath.Base(outDir),
		generatedEnums: make(map[string]bool),
	}
}

// WithWorkers sets the number of parallel workers.
func (g *JenniferGenerator) WithWorkers(n int) *JenniferGenerator {
	if n > 0 {
		g.workers = n
	}
	return g
}

// WithPackage sets the output package name.
func (g *JenniferGenerator) WithPackage(pkg string) *JenniferGenerator {
	if pkg != "" {
		g.pkg = pkg
	}
	return g
}

// WithDialect sets a custom dialect generator.
// This allows using different database dialects (e.g., Gremlin).
// The dialect must implement MinimalDialect at minimum.
// Additional capabilities are detected via FeatureGenerator and OptionalFeatureGenerator.
//
// NOTE: The dialect must also implement EntityPackageDialect for entity sub-package
// generation. Generate() will return a clear error if this requirement is not met.
func (g *JenniferGenerator) WithDialect(d MinimalDialect) *JenniferGenerator {
	if d != nil {
		g.dialect = d
		// Detect optional capabilities via type assertion
		if fg, ok := d.(FeatureGenerator); ok {
			g.featureGen = fg
		}
		if og, ok := d.(OptionalFeatureGenerator); ok {
			g.optionalGen = og
		}
		if mg, ok := d.(MigrateGenerator); ok {
			g.migrateGen = mg
		}
		if pf, ok := d.(PrivacyFilterGenerator); ok {
			g.privacyFilterGen = pf
		}
		if tg, ok := d.(TypesGenerator); ok {
			g.typesGen = tg
		}
	}
	return g
}

// optionalFeatureSpec defines a feature that may be generated when enabled.
type optionalFeatureSpec struct {
	feature Feature
	dir     string
	file    string
	gen     func(OptionalFeatureGenerator) (*jen.File, error)
}

// optionalFeatureSpecs lists all optional features and their output locations.
var optionalFeatureSpecs = []optionalFeatureSpec{
	{FeatureSchemaConfig, "internal", "schemaconfig.go", OptionalFeatureGenerator.GenSchemaConfig},
	{FeatureIntercept, "intercept", "intercept.go", OptionalFeatureGenerator.GenIntercept},
	{FeaturePrivacy, "privacy", "privacy.go", OptionalFeatureGenerator.GenPrivacy},
	{FeatureSnapshot, "internal", "schema.go", OptionalFeatureGenerator.GenSnapshot},
	// NOTE: migrate/migrate.go is owned by generateMigrations (the Schema.Create
	// auto-migration API). The versioned-migration types live in their own file —
	// pointing this spec at migrate.go made two concurrent writers race on one
	// path, and the feature's output was silently lost whenever the migration
	// writer's rename landed last. Pinned by TestOptionalFeatureSpecs_UniqueOutputs.
	{FeatureVersionedMigration, "migrate", "versioned.go", OptionalFeatureGenerator.GenVersionedMigration},
	{FeatureGlobalID, "internal", "globalid.go", OptionalFeatureGenerator.GenGlobalID},
	{FeatureEntQL, "", "querylanguage.go", OptionalFeatureGenerator.GenEntQL},
}

// Generate generates all code with parallel execution and streaming writes.
// It uses the configured dialect generator for database-specific code.
// Returns an error if no dialect has been set via WithDialect().
func (g *JenniferGenerator) Generate(ctx context.Context) error {
	if g.dialect == nil {
		return NewConfigError("Dialect", nil, "no dialect set: call WithDialect() before Generate()", nil)
	}
	if _, ok := g.dialect.(EntityPackageDialect); !ok {
		return NewConfigError("Dialect", nil,
			fmt.Sprintf("dialect %q must implement EntityPackageDialect for entity sub-package generation", g.dialect.Name()),
			nil,
		)
	}
	if err := os.MkdirAll(g.outDir, 0o755); err != nil {
		return err
	}

	errg, ctx := errgroup.WithContext(ctx)
	errg.SetLimit(g.workers)

	g.generateEntities(ctx, errg)
	g.generateShared(ctx, errg)
	g.generateFeatures(ctx, errg)
	g.generateMigrations(ctx, errg)

	if err := errg.Wait(); err != nil {
		return err
	}
	if err := g.generateExternalTemplates(); err != nil {
		return err
	}

	// AGENTS.md is written before cleanup so it is present in the manifest
	// and is not treated as a stale file.
	if err := g.writeAgentDocs(); err != nil {
		return err
	}

	var cleanupErrs []error
	if err := g.cleanupFeatures(); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if err := g.cleanupStaleFiles(); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if err := g.writeManifest(); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if len(cleanupErrs) > 0 {
		return fmt.Errorf("generation succeeded but cleanup failed: %w", errors.Join(cleanupErrs...))
	}
	return nil
}

// generateEntities dispatches per-entity code generation tasks.
// The EntityPackageDialect requirement is validated upfront in Generate().
// entityFileSpec declares a single per-entity output file: where to write it
// and how to generate it. Used by generateEntities to drive emission via a
// table instead of repeating errg.Go boilerplate per output.
type entityFileSpec struct {
	dir  string                    // output directory (relative to outDir)
	file string                    // output filename
	gen  func() (*jen.File, error) // captures the right helper / args per entry
}

func (g *JenniferGenerator) generateEntities(ctx context.Context, errg *errgroup.Group) {
	// Safe: validated in Generate() before this is called.
	creator, _ := g.dialect.(EntityPackageDialect)
	rootPkg := ""
	if g.graph.Config != nil && g.graph.Package != "" {
		rootPkg = g.graph.Package
	}

	for _, t := range g.graph.Nodes {
		entityDir := t.PackageDir()
		lowerName := strings.ToLower(t.Name)

		// Three helpers for three output destinations, each scoped to t:
		//  - clientHelper: client/{entity}/ (package {entity}client) — heavy code
		//  - entityHelper: entity/           — shared entity struct & interfaces
		//  - queryHelper:  query/            — query builders
		// The leaf {entity}/ files (meta + predicates) use the dialect generator directly.
		clientHelper := newClientPkgHelper(g, entityDir, rootPkg)
		entityHelper := newEntityPkgHelper(g, "entity", rootPkg)
		queryHelper := newEntityPkgHelper(g, "query", rootPkg)
		clientDir := filepath.Join("client", entityDir)

		entityPkgImport := "entity"
		if rootPkg != "" {
			entityPkgImport = rootPkg + "/entity"
		}

		ed := creator.WithHelper(clientHelper)

		specs := []entityFileSpec{
			// Heavy per-entity code → client/{entity}/ (package {entity}client)
			{clientDir, "mutation.go", func() (*jen.File, error) { return ed.GenMutation(t) }},
			{clientDir, "client.go", func() (*jen.File, error) { return creator.GenEntityClient(clientHelper, t) }},
			{clientDir, "runtime.go", func() (*jen.File, error) { return creator.GenEntityRuntime(clientHelper, t) }},
			{clientDir, "create.go", func() (*jen.File, error) { return creator.GenCreate(clientHelper, t) }},
			{clientDir, "update.go", func() (*jen.File, error) { return creator.GenUpdate(clientHelper, t) }},
			{clientDir, "delete.go", func() (*jen.File, error) { return creator.GenDelete(clientHelper, t) }},

			// Shared entity types → entity/
			{"entity", lowerName + ".go", func() (*jen.File, error) { return creator.GenEntityPkg(entityHelper, t) }},

			// Query builders → query/
			{"query", lowerName + ".go", func() (*jen.File, error) {
				return creator.GenQueryPkg(queryHelper, t, entityPkgImport)
			}},

			// True leaf: schema metadata + predicate helpers → {entity}/
			{entityDir, entityDir + ".go", func() (*jen.File, error) { return g.dialect.GenPackage(t) }},
			{entityDir, "where.go", func() (*jen.File, error) { return g.dialect.GenPredicate(t) }},
		}

		for _, s := range specs {
			s := s // capture per iteration
			errg.Go(func() error {
				f, err := s.gen()
				return g.writeFileResult(ctx, f, err, s.dir, s.file)
			})
		}
	}
}

// generateShared dispatches shared infrastructure file generation.
func (g *JenniferGenerator) generateShared(ctx context.Context, errg *errgroup.Group) {
	errg.Go(g.genAndWrite(ctx, g.dialect.GenPredicatePackage, "predicate", "predicate.go"))
	errg.Go(g.genAndWrite(ctx, g.dialect.GenClient, "", "client.go"))
	errg.Go(g.genAndWrite(ctx, g.dialect.GenVelox, "", "velox.go"))
	errg.Go(g.genAndWrite(ctx, g.dialect.GenErrors, "", "errors.go"))
	errg.Go(g.genAndWrite(ctx, g.dialect.GenTx, "", "tx.go"))

	if creator, ok := g.dialect.(EntityPackageDialect); ok {
		errg.Go(func() error {
			queryHelper := newEntityPkgHelper(g, "query", "")
			if g.graph.Config != nil && g.graph.Package != "" {
				queryHelper = newEntityPkgHelper(g, "query", g.graph.Package)
			}
			f, err := creator.GenQueryHelpers(queryHelper)
			return g.writeFileResult(ctx, f, err, "query", "helpers.go")
		})
		errg.Go(func() error {
			entityHelper := newEntityPkgHelper(g, "entity", "")
			if g.graph.Config != nil && g.graph.Package != "" {
				entityHelper = newEntityPkgHelper(g, "entity", g.graph.Package)
			}
			f, err := creator.GenEntityHooks(entityHelper)
			return g.writeFileResult(ctx, f, err, "entity", "hooks.go")
		})
	}

	errg.Go(g.genAndWrite(ctx, g.dialect.GenRuntime, "", "runtime.go"))

	if tg, ok := g.dialect.(TypesGenerator); ok {
		errg.Go(g.genAndWrite(ctx, tg.GenTypes, "", "types.go"))
	}
}

// coreFeatures lists feature names that are dispatched via FeatureGenerator.
// Note: "migrate" is handled by MigrateGenerator (see generateMigrations), and
// "intercept"/"privacy" by OptionalFeatureGenerator (see optionalFeatureSpecs) —
// do not add them here.
var coreFeatures = []string{"hook"}

// generateFeatures dispatches feature-specific code generation.
func (g *JenniferGenerator) generateFeatures(ctx context.Context, errg *errgroup.Group) {
	if g.featureGen != nil {
		for _, feature := range coreFeatures {
			if g.featureGen.SupportsFeature(feature) {
				dir, filename := "", feature+".go"
				if feature == "hook" {
					dir, filename = "hook", "hook.go"
				}
				errg.Go(func() error {
					f, err := g.featureGen.GenFeature(feature)
					return g.writeFileResult(ctx, f, err, dir, filename)
				})
			}
		}
	}
	if g.optionalGen != nil {
		for _, spec := range optionalFeatureSpecs {
			if enabled, _ := g.graph.FeatureEnabled(spec.feature.Name); !enabled {
				continue
			}
			errg.Go(func() error {
				f, err := spec.gen(g.optionalGen)
				return g.writeFileResult(ctx, f, err, spec.dir, spec.file)
			})
		}
	}

	// Generate privacy filters (per-entity filter types used with the privacy feature).
	if g.privacyFilterGen != nil {
		if enabled, _ := g.graph.FeatureEnabled(FeaturePrivacy.Name); enabled {
			creator, useEntityPkg := g.dialect.(EntityPackageDialect)
			rootPkg := ""
			if useEntityPkg && g.graph.Config != nil && g.graph.Package != "" {
				rootPkg = g.graph.Package
			}
			for _, t := range g.graph.Nodes {
				errg.Go(func() error {
					if useEntityPkg {
						clientHelper := newClientPkgHelper(g, t.PackageDir(), rootPkg)
						ed := creator.WithHelper(clientHelper)
						if pf, ok := ed.(PrivacyFilterGenerator); ok {
							f, err := pf.GenFilter(t)
							clientDir := filepath.Join("client", t.PackageDir())
							return g.writeFileResult(ctx, f, err, clientDir, "filter.go")
						}
						return nil
					}
					f, err := g.privacyFilterGen.GenFilter(t)
					return g.writeFileResult(ctx, f, err, "", strings.ToLower(t.Name)+"_filter.go")
				})
			}
		}
	}
}

// generateMigrations dispatches migration file generation.
func (g *JenniferGenerator) generateMigrations(ctx context.Context, errg *errgroup.Group) {
	if g.migrateGen == nil {
		return
	}
	errg.Go(func() error {
		files, err := g.migrateGen.GenMigrate()
		if err != nil {
			return fmt.Errorf("generate migrations: %w", err)
		}
		if files.Schema != nil {
			if err := g.writeFile(ctx, files.Schema, "migrate", "schema.go"); err != nil {
				return err
			}
		}
		if files.Migrate != nil {
			if err := g.writeFile(ctx, files.Migrate, "migrate", "migrate.go"); err != nil {
				return err
			}
		}
		return nil
	})
}

// generateExternalTemplates processes user-provided templates from Config.Templates.
// These are external templates registered by users or extensions for backward
// compatibility with the template-based extension mechanism.
func (g *JenniferGenerator) generateExternalTemplates() error {
	if len(g.graph.Templates) == 0 {
		return nil
	}
	initTemplates()
	var external []GraphTemplate
	for _, rootT := range g.graph.Templates {
		templates.Funcs(rootT.FuncMap)
		for _, tmpl := range rootT.Templates() {
			if parse.IsEmptyTree(tmpl.Root) {
				continue
			}
			name := tmpl.Name()
			switch {
			case strings.HasPrefix(name, "helper/"):
			case strings.Contains(name, "/helper/"):
			case templates.Lookup(name) == nil:
				external = append(external, GraphTemplate{
					Name:   name,
					Format: snake(name) + ".go",
					Skip:   rootT.condition,
				})
			}
			templates = MustParse(templates.AddParseTree(name, tmpl.Tree))
		}
	}
	if len(external) == 0 {
		return nil
	}
	for _, tmpl := range external {
		if tmpl.Skip != nil && tmpl.Skip(g.graph) {
			continue
		}
		var b bytes.Buffer
		if err := templates.ExecuteTemplate(&b, tmpl.Name, g.graph); err != nil {
			return fmt.Errorf("execute template %q: %w", tmpl.Name, err)
		}
		outPath := filepath.Join(g.outDir, tmpl.Format)
		dir := filepath.Dir(outPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		formatted, err := imports.Process(outPath, b.Bytes(), nil)
		if err != nil {
			return fmt.Errorf("format template %q: %w", tmpl.Name, err)
		}
		// Atomic write, skipped when content is unchanged (preserves mtime).
		if _, err := WriteFileIfChanged(outPath, formatted, 0o644); err != nil {
			return err
		}
		// Track external template files in the manifest so they are
		// correctly cleaned up when the template is removed.
		if rel, err := filepath.Rel(g.outDir, outPath); err == nil && rel != "" {
			g.manifestMu.Lock()
			g.generatedFiles = append(g.generatedFiles, rel)
			g.manifestMu.Unlock()
		}
	}
	return nil
}

// cleanupFeatures removes generated files for disabled features.
func (g *JenniferGenerator) cleanupFeatures() error {
	var errs []error
	for _, f := range allFeatures {
		if f.cleanup == nil || g.graph.featureEnabled(f) {
			continue
		}
		if err := f.cleanup(g.graph.Config); err != nil {
			errs = append(errs, fmt.Errorf("cleanup feature %s: %w", f.Name, err))
		}
	}
	return errors.Join(errs...)
}

// manifestFile is the name of the file that records all generated file paths.
// Used for exact cleanup of stale files between generation runs.
const manifestFile = ".velox-manifest"

// cleanupStaleFiles removes files that were in the previous manifest but are
// not in the current generation run. This replaces heuristic-based cleanup
// with exact file tracking, eliminating fragile pattern matching.
func (g *JenniferGenerator) cleanupStaleFiles() error {
	prev, err := g.readManifest()
	if err != nil || len(prev) == 0 {
		return nil // No previous manifest — nothing to clean up.
	}
	current := make(map[string]struct{}, len(g.generatedFiles))
	for _, f := range g.generatedFiles {
		current[f] = struct{}{}
	}
	var errs []error
	removedDirs := make(map[string]struct{})
	for _, rel := range prev {
		if _, ok := current[rel]; ok {
			continue
		}
		p := filepath.Join(g.outDir, rel)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove stale %s: %w", p, err))
		}
		// Track parent directories for empty-dir cleanup.
		if dir := filepath.Dir(rel); dir != "." {
			removedDirs[dir] = struct{}{}
		}
	}
	// Remove directories that became empty after stale file removal.
	// Walk up through ancestors to handle nested empty directories.
	for dir := range removedDirs {
		for d := dir; d != "."; d = filepath.Dir(d) {
			fullDir := filepath.Join(g.outDir, d)
			entries, err := os.ReadDir(fullDir)
			if err != nil || len(entries) > 0 {
				break
			}
			os.Remove(fullDir) //nolint:errcheck // Best-effort empty-dir cleanup.
		}
	}
	return errors.Join(errs...)
}

// writeManifest writes the current set of generated files to the manifest file.
// Uses atomic temp-file + rename (skipped when unchanged) to prevent
// corruption on crash.
func (g *JenniferGenerator) writeManifest() error {
	slices.Sort(g.generatedFiles)
	data := strings.Join(g.generatedFiles, "\n") + "\n"
	outPath := filepath.Join(g.outDir, manifestFile)
	_, err := WriteFileIfChanged(outPath, []byte(data), 0o644)
	return err
}

// readManifest reads the previous manifest file and returns the list of relative paths.
func (g *JenniferGenerator) readManifest() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(g.outDir, manifestFile))
	if err != nil {
		return nil, err
	}
	var paths []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// writeFileResult wraps writeFile to handle the (*jen.File, error) tuple
// returned by all generation methods. If err is non-nil, it is wrapped with
// file context. If f is nil (e.g., no-op generator), the write is skipped.
func (g *JenniferGenerator) writeFileResult(ctx context.Context, f *jen.File, err error, subdir, filename string) error {
	if err != nil {
		return fmt.Errorf("generate %s/%s: %w", subdir, filename, err)
	}
	if f == nil {
		return nil
	}
	return g.writeFile(ctx, f, subdir, filename)
}

// genAndWrite returns a closure that generates a file and writes it.
// Used for graph-level generators that take no arguments.
func (g *JenniferGenerator) genAndWrite(ctx context.Context, gen func() (*jen.File, error), subdir, filename string) func() error {
	return func() error {
		f, err := gen()
		return g.writeFileResult(ctx, f, err, subdir, filename)
	}
}

// writeFile writes a Jennifer file to disk atomically via WriteFileIfChanged,
// which skips the write (preserving mtime) when the rendered content is
// byte-identical to what's already on disk. It checks for context
// cancellation first, which provides early exit when errgroup cancels the
// context on first error.
func (g *JenniferGenerator) writeFile(ctx context.Context, f *jen.File, subdir, filename string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := g.outDir
	if subdir != "" {
		dir = filepath.Join(g.outDir, subdir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	outPath := filepath.Join(dir, filename)
	formatted, err := FormatJenFile(f, outPath)
	if err != nil {
		return fmt.Errorf("format %s: %w", outPath, err)
	}
	if _, err := WriteFileIfChanged(outPath, formatted, 0o644); err != nil {
		return err
	}
	// Track the generated file for manifest-based cleanup — also when the
	// write was skipped: the file is still part of the generated set.
	rel, _ := filepath.Rel(g.outDir, outPath)
	if rel != "" {
		g.manifestMu.Lock()
		g.generatedFiles = append(g.generatedFiles, rel)
		g.manifestMu.Unlock()
	}
	return nil
}

// FormatJenFile renders a Jennifer file and post-processes the output with
// x/tools/imports in format-only mode, which groups stdlib imports
// separately from third-party imports — matching Ent's assets.format()
// pass. Jennifer alone emits one flat alphabetical import block that mixes
// stdlib with external packages. filename is only used for diagnostics.
func FormatJenFile(f *jen.File, filename string) ([]byte, error) {
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		return nil, err
	}
	return FormatGoBytes(filename, buf.Bytes())
}

// formatOnlyOptions mirrors the defaults imports.Process applies for a nil
// *Options, plus FormatOnly. It is the fallback path of FormatGoBytes for
// source whose import block regroupImports does not recognize. Jennifer
// already tracks every import a generated file needs, so import resolution
// is never wanted here — and it is expensive: with resolution on,
// imports.Process builds a fresh ProcessEnv per call and spawns one `go env`
// subprocess per generated file. Pinned by
// TestFormatGoBytes_DoesNotResolveImports.
var formatOnlyOptions = &imports.Options{
	FormatOnly: true,
	Comments:   true,
	TabIndent:  true,
	TabWidth:   8,
}

// FormatGoBytes applies the import grouping goimports would produce —
// stdlib imports in their own block above third-party ones, each block
// sorted by path — to Go source that is already gofmt-formatted (Jennifer
// output is). Exposed so golden tests (which compare jen.File.GoString())
// can match the on-disk layout that writeFile produces via FormatJenFile.
//
// It rewrites the import block textually. Jennifer's Render already runs
// go/format.Source, and imports.Process would parse and print the whole
// file twice more (once for its own printer, once through format.Source),
// so the parsing pass was the single largest cost of generation: three
// parse+print rounds per file. Source with an import block the textual
// pass does not recognize (comments, blank lines, cgo) falls back to
// imports.Process in format-only mode so the output is never wrong, only
// slower. Pinned byte-for-byte by every golden in compiler/gen/sql and
// contrib/graphql.
func FormatGoBytes(filename string, src []byte) ([]byte, error) {
	if out, ok := regroupImports(src); ok {
		return out, nil
	}
	return imports.Process(filename, src, formatOnlyOptions)
}

// importLine matches one gofmt-formatted import spec inside a block:
// an optional name (identifier, "." or "_") and the quoted path.
var importLine = regexp.MustCompile(`^\t(?:([A-Za-z_][A-Za-z0-9_]*|\.) )?"([^"]+)"$`)

// importSpec is one parsed line of an import block.
type importSpec struct {
	line []byte // the full line, tab-indented, as it will be written back
	path []byte // the quoted path's contents
}

// regroupImports splits the first parenthesised import block of src into a
// stdlib group and a third-party group (goimports' rule: a path whose first
// element contains no dot is stdlib), sorts each by path, and returns the
// rewritten source. ok is false when there is a block the function does
// not fully understand — the caller then uses the parsing fallback. Source
// with no parenthesised block (no imports, or a single one-line import) is
// returned unchanged: there is nothing to group.
func regroupImports(src []byte) (out []byte, ok bool) {
	const open, closing = "\nimport (\n", "\n)\n"
	start := bytes.Index(src, []byte(open))
	if start < 0 {
		return src, true
	}
	bodyStart := start + len(open)
	bodyLen := bytes.Index(src[bodyStart:], []byte(closing))
	if bodyLen < 0 {
		return nil, false
	}
	body := src[bodyStart : bodyStart+bodyLen]
	var std, other []importSpec
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		m := importLine.FindSubmatch(line)
		if m == nil {
			// Blank line (an existing grouping), comment, or anything
			// else: leave it to the parser-backed path.
			return nil, false
		}
		spec := importSpec{line: line, path: m[2]}
		first, _, _ := bytes.Cut(spec.path, []byte("/"))
		if bytes.IndexByte(first, '.') < 0 {
			std = append(std, spec)
		} else {
			other = append(other, spec)
		}
	}
	sortImportSpecs(std)
	sortImportSpecs(other)

	buf := make([]byte, 0, len(src)+1)
	buf = append(buf, src[:bodyStart]...)
	buf = appendImportSpecs(buf, std)
	if len(std) > 0 && len(other) > 0 {
		buf = append(buf, '\n', '\n')
	}
	buf = appendImportSpecs(buf, other)
	buf = append(buf, src[bodyStart+bodyLen:]...)
	return buf, true
}

// sortImportSpecs orders import specs by path, then by the full line (so a
// named and an unnamed import of the same path keep a deterministic order),
// matching go/ast.SortImports within one group.
func sortImportSpecs(specs []importSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		if c := bytes.Compare(specs[i].path, specs[j].path); c != 0 {
			return c < 0
		}
		return bytes.Compare(specs[i].line, specs[j].line) < 0
	})
}

// appendImportSpecs writes the specs' lines newline-separated (no trailing
// newline) to buf.
func appendImportSpecs(buf []byte, specs []importSpec) []byte {
	for i, spec := range specs {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, spec.line...)
	}
	return buf
}
