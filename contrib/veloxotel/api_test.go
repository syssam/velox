package veloxotel_test

import (
	"database/sql/driver"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/syssam/velox2/contrib/veloxotel"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestPublicAPI: the package's exported names are exactly these. A task that
// adds one adds it here.
func TestPublicAPI(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	d, err := doc.NewFromFiles(fset, files, "github.com/syssam/velox2/contrib/veloxotel")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	add := func(vs []*doc.Value) {
		for _, v := range vs {
			got = append(got, v.Names...)
		}
	}
	add(d.Consts)
	add(d.Vars)
	for _, ty := range d.Types {
		got = append(got, ty.Name)
		add(ty.Consts)
		add(ty.Vars)
		for _, f := range ty.Funcs {
			got = append(got, f.Name)
		}
		for _, m := range ty.Methods {
			got = append(got, ty.Name+"."+m.Name)
		}
	}
	for _, f := range d.Funcs {
		got = append(got, f.Name)
	}
	slices.Sort(got)
	want := []string{"Callers", "CallersOff", "CallersOn", "Config", "Options"}
	if !slices.Equal(got, want) {
		t.Errorf("exported names %v, want %v", got, want)
	}
}

// TestConfigBooleans: each boolean is a verb and what it acts on: Omit turns
// off what is on by default, Record turns on what is off.
func TestConfigBooleans(t *testing.T) {
	ty := reflect.TypeFor[veloxotel.Config]()
	for f := range ty.Fields() {
		if f.Type.Kind() != reflect.Bool {
			continue
		}
		if !strings.HasPrefix(f.Name, "Omit") && !strings.HasPrefix(f.Name, "Record") {
			t.Errorf("Config.%s is a bool that starts with neither Omit nor Record", f.Name)
		}
	}
}

// TestOptionsCount: velox.WithAround and WithTxAround always, WithCallers
// unless CallersOff, WithSQLComment when App is set.
func TestOptionsCount(t *testing.T) {
	for _, c := range []struct {
		cfg  veloxotel.Config
		want int
	}{
		{veloxotel.Config{}, 3},
		{veloxotel.Config{App: "a"}, 4},
		{veloxotel.Config{Callers: veloxotel.CallersOff}, 2},
		{veloxotel.Config{App: "a", Callers: veloxotel.CallersOff}, 3},
	} {
		if n := len(veloxotel.Options(c.cfg)); n != c.want {
			t.Errorf("Options(%+v) has %d options, want %d", c.cfg, n, c.want)
		}
	}
}

// TestCallersOff: with App set, CallersOff leaves the call site out of the
// spans and out of the SQL comment, so one shape is one text.
func TestCallersOff(t *testing.T) {
	run := func(cfg veloxotel.Config) map[attribute.Key]attribute.Value {
		f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
		db, exp, _ := traced(t, f, cfg)
		if _, err := db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
		return attrs(only(t, exp))
	}
	off := run(veloxotel.Config{App: "billing", Callers: veloxotel.CallersOff})
	for _, k := range []attribute.Key{"code.file.path", "code.line.number", "code.function.name"} {
		if _, ok := off[k]; ok {
			t.Errorf("CallersOff: span has %s", k)
		}
	}
	text := off["db.query.text"].AsString()
	if !strings.HasSuffix(text, "/*app='billing'*/") || strings.Contains(text, "caller=") {
		t.Errorf("CallersOff: db.query.text %q", text)
	}
	if text := run(veloxotel.Config{App: "billing"})["db.query.text"].AsString(); !strings.Contains(text, "caller='") {
		t.Errorf("default: db.query.text %q has no caller", text)
	}
}
