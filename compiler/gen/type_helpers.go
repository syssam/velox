package gen

import (
	"encoding/json"
	"fmt"
	"go/token"
	"reflect"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// Helper functions
// =============================================================================

// titleCase capitalizes the first letter of a string.
// This is a replacement for the deprecated strings.Title function
// for simple single-word capitalization.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func structTag(name, tag string) string {
	t := fmt.Sprintf(`json:"%s,omitempty"`, name)
	if tag == "" {
		return t
	}
	if _, ok := reflect.StructTag(tag).Lookup("json"); !ok {
		tag = t + " " + tag
	}
	return tag
}

// builderField returns the struct field for the given name
// and ensures it doesn't conflict with Go keywords and other
// builder fields, and it is not exported.
func builderField(name string) string {
	if name == "" {
		return "_"
	}
	_, ok := privateField[name]
	if ok || token.Lookup(name).IsKeyword() || strings.ToUpper(name[:1]) == name[:1] {
		return "_" + name
	}
	return name
}

// fieldAnnotate extracts the field annotation from a loaded annotation format.
func fieldAnnotate(annotation map[string]any) *field.Annotation {
	annotate := &field.Annotation{}
	if annotation == nil || annotation[annotate.Name()] == nil {
		return nil
	}
	if buf, err := json.Marshal(annotation[annotate.Name()]); err == nil {
		_ = json.Unmarshal(buf, &annotate)
	}
	return annotate
}

// sqlAnnotate extracts the sqlschema.Annotation from a loaded annotation format.
// Annotations are validated during graph construction (validateSQLAnnotation),
// so this accessor does not return an error.
func sqlAnnotate(annotation map[string]any) *sqlschema.Annotation {
	annotate := &sqlschema.Annotation{}
	if annotation == nil || annotation[annotate.Name()] == nil {
		return nil
	}
	if buf, err := json.Marshal(annotation[annotate.Name()]); err == nil {
		_ = json.Unmarshal(buf, &annotate)
	}
	return annotate
}

// validateSQLAnnotation validates the SQL annotation from a loaded annotation map.
// Returns an error if ColumnType, Check, or DefaultExpr values are invalid.
// Called during graph construction so invalid annotations are caught early.
func validateSQLAnnotation(annotation map[string]any) error {
	a := sqlAnnotate(annotation)
	if a == nil {
		return nil
	}
	if a.ColumnType != "" {
		if err := sqlschema.ValidateColumnType(a.ColumnType); err != nil {
			return fmt.Errorf("invalid ColumnType: %w", err)
		}
	}
	if a.Check != "" {
		if err := sqlschema.ValidateExpression(a.Check); err != nil {
			return fmt.Errorf("invalid Check expression: %w", err)
		}
	}
	if a.DefaultExpr != "" {
		if err := sqlschema.ValidateExpression(a.DefaultExpr); err != nil {
			return fmt.Errorf("invalid DefaultExpr: %w", err)
		}
	}
	return nil
}

// sqlIndexAnnotate extracts the entsql annotation from a loaded annotation format.
func sqlIndexAnnotate(annotation map[string]any) *sqlschema.IndexAnnotation {
	annotate := &sqlschema.IndexAnnotation{}
	if annotation == nil || annotation[annotate.Name()] == nil {
		return nil
	}
	if buf, err := json.Marshal(annotation[annotate.Name()]); err == nil {
		_ = json.Unmarshal(buf, &annotate)
	}
	return annotate
}

func names(ids ...string) map[string]struct{} {
	m := make(map[string]struct{})
	for i := range ids {
		m[ids[i]] = struct{}{}
	}
	return m
}

func sortedKeys(m map[int]struct{}) []int {
	s := make([]int, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	slices.Sort(s)
	return s
}

// =============================================================================
// Global variables
// =============================================================================

var (
	// global identifiers used by the generated package.
	globalIdent = names(
		"AggregateFunc",
		"As",
		"Asc",
		"Client",
		"config",
		"Count",
		"Debug",
		"Desc",
		"Driver",
		"Hook",
		"Interceptor",
		"Log",
		"MutateFunc",
		"Mutation",
		"Mutator",
		"Op",
		"Option",
		"OrderFunc",
		"Max",
		"Mean",
		"Min",
		"Schema",
		"Sum",
		"Policy",
		"Query",
		"Value",
	)
	// private fields used by the different builders.
	// Fields with these names get prefixed with "_" by builderField() to avoid conflicts.
	privateField = names(
		"config",
		"ctx",
		"done",
		"hooks",
		"inters",
		"limit",
		"mutation",
		"offset",
		"oldValue",
		"order",
		"op",
		"path",
		"predicates",
		"typ",
		"unique",
		"driver",
		"selectValues",
		"Edges",
	)
)

// mutMethods returns the method names of mutation interface.
var mutMethods = func() map[string]bool {
	names := map[string]bool{"Client": true, "Tx": true, "Where": true, "SetOp": true}
	t := reflect.TypeFor[velox.Mutation]()
	for i := range t.NumMethod() {
		names[t.Method(i).Name] = true
	}
	return names
}()
