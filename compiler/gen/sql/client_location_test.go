package sql

// Phase B structural guard: pins the target routing for heavy generator
// outputs (client, create, update, delete, mutation, runtime, filter).
// After Phase B, each of those files must be written to
// client/{entity}/ — not to the {entity}/ leaf.  The {entity}/ leaf
// retains only schema constants (user.go) and predicate helpers (where.go).
//
// Test parses compiler/gen/generate.go via go/ast and checks every
// writeFileResult() call-site.  This is the same source-level assertion
// style used by wiring_test.go (e.g. TestQueryCloneCopiesEveryField).

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"testing"
)

// TestClient_InClientSubPackage pins the cycle-break Phase B invariant:
// the dispatcher (compiler/gen/generate.go) must route the "heavy"
// per-entity generator outputs — client.go, create.go, update.go,
// delete.go, mutation.go, runtime.go, and filter.go — to the
// client/{entity}/ sub-package, not to the {entity}/ leaf.
//
// The {entity}/ leaf retains only the schema metadata file ({entity}.go)
// and the predicate helpers (where.go); everything else moves under
// client/{entity}/.
//
// Current (FAILING) state:
//
//	All seven files are routed to entityDir (which equals t.PackageDir(),
//	e.g. "user/").  The dir argument of the writeFileResult() calls does
//	NOT contain the string "client".
//
// Target state (Task 7 will make this pass):
//
//	The dir argument for each of the seven files must contain "client"
//	(e.g. filepath.Join("client", entityDir) == "client/user/").
func TestClient_InClientSubPackage(t *testing.T) {
	t.Parallel()

	// These are the filenames that MUST be routed to client/{entity}/.
	// Any writeFileResult() whose fifth argument is one of these strings
	// must have a fourth argument (the directory) that contains "client".
	clientFiles := map[string]bool{
		`"client.go"`:   true,
		`"create.go"`:   true,
		`"update.go"`:   true,
		`"delete.go"`:   true,
		`"mutation.go"`: true,
		`"runtime.go"`:  true,
		`"filter.go"`:   true,
	}

	src, err := os.ReadFile("../generate.go")
	if err != nil {
		t.Fatalf("read compiler/gen/generate.go: %v", err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generate.go", src, 0)
	if err != nil {
		t.Fatalf("parse compiler/gen/generate.go: %v", err)
	}

	var violations int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "writeFileResult" {
			return true
		}
		// Signature: writeFileResult(ctx, f, err, dir, filename)
		// Indices:                    0    1  2    3    4
		if len(call.Args) < 5 {
			return true
		}

		filename := renderExpr(fset, call.Args[4])
		if !clientFiles[filename] {
			// Not a file we care about (e.g. "where.go", "{entity}.go").
			return true
		}

		dirExpr := renderExpr(fset, call.Args[3])
		if !containsClientPrefix(dirExpr) {
			violations++
			t.Errorf(
				"writeFileResult(..., dir=%s, %s): "+
					"file %s must be routed to client/{entity}/ (dir must contain \"client\"), "+
					"but the current dir expression %q does not — "+
					"Phase B requires heavy generator outputs to live under client/{entity}/",
				dirExpr, filename, filename, dirExpr,
			)
		}
		return true
	})

	if violations == 0 {
		t.Log("TestClient_InClientSubPackage: all 7 client-file routes already contain \"client\" — Phase B routing is complete")
	} else {
		t.Logf("TestClient_InClientSubPackage: %d routing violation(s) found (expected ≥7 before Phase B Task 7)", violations)
	}
}

// renderExpr returns the source text of an ast.Expr, including quoted
// string literals exactly as written.  Uses go/printer for fidelity.
func renderExpr(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, e); err != nil {
		return "<render-error>"
	}
	return buf.String()
}

// containsClientPrefix reports whether a directory expression (already
// rendered to source text) indicates routing to a client/ sub-tree.
// Acceptable forms (post-Phase-B):
//   - filepath.Join("client", ...)  — a call expression containing "client"
//   - "client/..."                  — a string literal starting with client/
//   - clientDir, clientEntityDir    — a variable whose name contains client
func containsClientPrefix(expr string) bool {
	// Covers: filepath.Join("client", entityDir), "client/user/", clientDir, etc.
	return bytes.Contains([]byte(expr), []byte("client"))
}
