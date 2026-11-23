package gogen

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/syssam/velox/plugin"
)

// file is a Go file being written: its body and the imports the body uses.
type file struct {
	bytes.Buffer
	imports map[string]string // import path → the name the body uses
}

func newFile() *file { return &file{imports: map[string]string{}} }

func (f *file) p(format string, args ...any) {
	_, _ = fmt.Fprintf(f, format, args...) // a bytes.Buffer does not fail
	f.WriteByte('\n')
}

// doc writes text as a comment at indent (tabs), wrapped to 80 columns.
func (f *file) doc(indent, text string) {
	width := 80 - 4*len(indent) - len("// ")
	line := ""
	for w := range strings.FieldsSeq(text) {
		if line != "" && len(line)+1+len(w) > width {
			f.p("%s// %s", indent, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		f.p("%s// %s", indent, line)
	}
}

// use records that the body uses the package at path by name, and returns
// name.
func (f *file) use(path, name string) string {
	f.imports[path] = name
	return name
}

func (f *file) velox() string { return f.use(veloxPath, "velox") }

func (f *file) veloxgql() string { return f.use(veloxgqlPath, "veloxgql") }

func (f *file) gqlgraph() string { return f.use(gqlgraphPath, "gqlgraph") }

func (f *file) untyped() string { return f.use(untypedPath, "untyped") }

// finish is the file's Go source, not yet gofmt'd (formatFiles): the
// package clause, the imports in two groups (the standard library, then the
// rest), the version guard and the body.
func (f *file) finish(name, pkg string) (plugin.File, error) {
	guard := f.veloxgql() // the guard imports it, whatever the body uses
	var src bytes.Buffer
	fmt.Fprintf(&src, "package %s\n\n", pkg)
	byName := map[string]string{}
	var std, other []string
	for _, p := range slices.Sorted(maps.Keys(f.imports)) {
		if prev, ok := byName[f.imports[p]]; ok {
			return plugin.File{}, fmt.Errorf("velox-graphql: %s would import both %s and %s as %s: an application's Go type is of a package named as one velox-graphql imports", name, prev, p, f.imports[p])
		}
		byName[f.imports[p]] = p
		spec := strconv.Quote(p)
		if n := f.imports[p]; n != path.Base(p) && n != pkgNames[p] {
			spec = n + " " + spec
		}
		if strings.Contains(strings.Split(p, "/")[0], ".") {
			other = append(other, spec)
		} else {
			std = append(std, spec)
		}
	}
	if len(std)+len(other) > 0 {
		src.WriteString("import (\n")
		for _, s := range std {
			src.WriteString("\t" + s + "\n")
		}
		if len(std) > 0 && len(other) > 0 {
			src.WriteString("\n")
		}
		for _, s := range other {
			src.WriteString("\t" + s + "\n")
		}
		src.WriteString(")\n\n")
	}
	// Generated code and veloxgql fail to compile together when they
	// disagree on the version, as the core's files do.
	fmt.Fprintf(&src, "const _ = %s.GeneratedCodeIsVersion27\n\n", guard)
	src.Write(f.Bytes())
	return plugin.File{Path: name, Content: src.String()}, nil
}

// formatFiles gofmts the content of each of files, which finish wrote, on up
// to GOMAXPROCS goroutines. Its error is that of the first file in files
// that does not parse: the error formatting them in order returns.
func formatFiles(files []plugin.File) error {
	errs := make([]error, len(files))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(len(files), runtime.GOMAXPROCS(0)) {
		wg.Go(func() {
			for i := int(next.Add(1) - 1); i < len(files); i = int(next.Add(1) - 1) {
				out, err := format.Source([]byte(files[i].Content))
				if err != nil {
					errs[i] = fmt.Errorf("velox-graphql wrote Go that does not parse, which is a bug: %s: %w\n%s", files[i].Path, err, files[i].Content)
					continue
				}
				files[i].Content = string(out)
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// fitItems is the longest list items keeps on one line, and fitLine the
// longest line it writes so, with what comes before and after the list.
const (
	fitItems = 60
	fitLine  = 100
)

// items is the Go list xs between left and right: on one line when it is
// short, else one element a line, with a trailing comma, which gofmt
// indents.
func items(left string, xs []string, right string) string {
	if one := strings.Join(xs, ", "); len(one) <= fitItems && len(left)+len(one)+len(right) <= fitLine {
		return left + one + right
	}
	return left + "\n" + strings.Join(xs, ",\n") + ",\n" + right
}

// sprintf is the call fmtPkg.Sprintf of the format head(names...) and args:
// on one line when it is short, else the format in pieces, each "…"+, then
// the arguments, a few a line.
func sprintf(fmtPkg, head string, names, args []string) string {
	format := head + "(" + strings.Join(names, ", ") + ")"
	if one := fmt.Sprintf("%s.Sprintf(%q, %s)", fmtPkg, format, strings.Join(args, ", ")); len(one) <= 2*fitItems+20 {
		return one
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s.Sprintf(%q+\n", fmtPkg, head+"(")
	lines := runs(names, ", ")
	for i, l := range lines {
		end := ", "
		if i == len(lines)-1 {
			end = ")"
		}
		sep := "+\n"
		if i == len(lines)-1 {
			sep = ",\n"
		}
		fmt.Fprintf(&b, "%q%s", l+end, sep)
	}
	for _, l := range runs(args, ", ") {
		b.WriteString(l + ",\n")
	}
	b.WriteString(")")
	return b.String()
}

// runs are xs joined by sep into runs of at most fitItems bytes, one
// element a run when it is longer.
func runs(xs []string, sep string) []string {
	var out []string
	cur := ""
	for _, x := range xs {
		switch {
		case cur == "":
			cur = x
		case len(cur)+len(sep)+len(x) <= fitItems:
			cur += sep + x
		default:
			out = append(out, cur)
			cur = x
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
