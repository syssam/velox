package load

import (
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// funcRef describes the function value f of a schema for velox-gen: its
// package and name when generated code can call it by name, else why not.
// Whether gen can import the package is velox-gen's to check.
func funcRef(f any) loadjson.FuncRef {
	v := reflect.ValueOf(f)
	ref := loadjson.FuncRef{Type: v.Type().String()}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		ref.Why = "the binary does not name it"
		return ref
	}
	ref.Pkg, ref.Name, ref.Why = splitFuncName(fn.Name())
	return ref
}

// splitFuncName splits sym, a function's name as runtime.Func.Name reports
// it, such as "github.com/google/uuid.New", into its package's import path
// and its name. It accepts only an exported function declared at package
// level, outside package main; why says what sym is otherwise.
func splitFuncName(sym string) (pkg, name, why string) {
	// The package path ends at the first dot after its last slash: the
	// linker escapes a dot of the last element as %2e.
	slash := strings.LastIndexByte(sym, '/')
	dot := strings.IndexByte(sym[slash+1:], '.')
	if dot < 0 {
		return "", "", "its name " + sym + " has no package"
	}
	pkg, rest := sym[:slash+1+dot], sym[slash+1+dot+1:]
	if p, err := url.PathUnescape(pkg); err == nil {
		pkg = p
	}
	switch {
	case strings.HasSuffix(rest, "-fm"):
		return "", "", "a method value"
	case strings.Contains(rest, "["):
		return "", "", "an instance of a generic function"
	case strings.Contains(rest, ".func") || strings.HasPrefix(rest, "glob."):
		return "", "", "a closure"
	case strings.Contains(rest, "."):
		return "", "", "a method"
	case pkg == "main":
		return "", "", "declared in package main, which no package imports"
	case strings.HasPrefix(pkg, "vendor/") || strings.Contains(pkg, "/vendor/"):
		return "", "", "declared in a vendored package"
	}
	if r, _ := utf8.DecodeRuneInString(rest); !unicode.IsUpper(r) {
		return "", "", "unexported"
	}
	return pkg, rest, ""
}
