package sql

// SP-2 structural guards. These tests pin the post-refactor shape
// of generated cross-package state propagation. Any future generator
// change that reintroduces a SetInters interface assertion or a
// per-query []Interceptor slice fails these tests at codegen time —
// the silent-wiring-loss bug class is structurally impossible.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// buildWiringTestGraph returns a User + Post fixture with a one-to-many
// edge so both the entity_client edge query path AND the query_pkg
// eager-load path are exercised by the structural assertions.
func buildWiringTestGraph(t *testing.T) (*gen.Graph, *gen.Type, *gen.Type) {
	t.Helper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}
	return graph, userType, postType
}

// renderEntityClient + renderQueryPkg are tiny wrappers that turn the
// generator output for a single entity into a Go source string for
// substring assertions.
func renderEntityClient(t *testing.T, h *mockHelper, node *gen.Type) string {
	t.Helper()
	return genEntityClient(h, node).GoString()
}

func renderQueryPkg(t *testing.T, h *mockHelper, node *gen.Type) string {
	t.Helper()
	return genQueryPkg(h, node, h.graph.Nodes, h.EntityPkgPath(node)).GoString()
}

// TestNoSetIntersInterfaceAssertion guards against silent interceptor
// wiring loss. After SP-2, no generated code should type-assert to an
// inline interface with SetInters — interceptors propagate via the
// shared *entity.InterceptorStore pointer set at construction time,
// not via a per-query slice setter that callers must remember to call.
func TestNoSetIntersInterfaceAssertion(t *testing.T) {
	graph, userType, postType := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	for _, node := range []*gen.Type{userType, postType} {
		client := renderEntityClient(t, helper, node)
		query := renderQueryPkg(t, helper, node)
		for label, src := range map[string]string{
			"entity_client": client,
			"query_pkg":     query,
		} {
			if strings.Contains(src, "SetInters(c.Interceptors())") {
				t.Errorf("entity %s file %s still calls SetInters(c.Interceptors()); "+
					"interceptors must propagate via shared *entity.InterceptorStore pointer "+
					"set at query construction, not via a per-query slice setter",
					node.Name, label)
			}
			if strings.Contains(src, ".(interface{ SetInters(") ||
				strings.Contains(src, ".(interface {\n\t\tSetInters(") {
				t.Errorf("entity %s file %s still uses an inline SetInters interface "+
					"assertion; the wiring should be a typed pointer field on the query struct",
					node.Name, label)
			}
		}
	}
}

// TestPrivacyIsExplicitInPrepareQuery guards that, after the privacy
// separation refactor, prepareQuery evaluates the entity's Policy
// explicitly via q.policy.EvalQuery(ctx, q) *before* RunTraversers.
// Privacy is no longer compiled into Interceptors[0]; it is an explicit
// field on the query (wired by the entity client at construction time).
//
// We test against the regenerated tests/integration/query/*.go files
// because the synthetic createTestType() helper doesn't set
// t.schema.Policy, so the policy-conditional codegen is gated off.
// The integration prototype has User with an actual Policy() method,
// which is the realistic case.
func TestPrivacyIsExplicitInPrepareQuery(t *testing.T) {
	matches, err := filepath.Glob("../../../tests/integration/query/*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("integration prototype not regenerated; skipping privacy scan")
	}
	// Find at least one query file that has a policy field — that entity
	// must also call policy.EvalQuery in prepareQuery.
	var foundPolicyQuery bool
	for _, path := range matches {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Contains(src, []byte("policy velox.Policy")) {
			continue
		}
		foundPolicyQuery = true
		if !bytes.Contains(src, []byte("q.policy.EvalQuery(ctx, q)")) {
			t.Errorf("%s declares policy field but prepareQuery does not "+
				"call q.policy.EvalQuery(ctx, q) — privacy is disconnected", path)
		}
	}
	if !foundPolicyQuery {
		t.Skip("no regenerated query carries a policy field; skipping")
	}
}

// TestPolicyExplicitEvaluation guards that when privacy is enabled on an
// entity, prepareQuery evaluates q.policy.EvalQuery explicitly (outside
// the interceptor chain). Interceptors NEVER see privacy — it's its own
// explicit step. Entities without a policy have no EvalQuery call and no
// policy field.
func TestPolicyExplicitEvaluation(t *testing.T) {
	userType := createTypeWithPolicies(t, "User", []*load.Position{{MixedIn: false}})
	postType := createTestType("Post") // no policy
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}

	helper := newFeatureMockHelper().withFeatures("privacy")
	helper.graph = graph

	// --- User (has policy) ---
	userSrc := genQueryPkg(helper, userType, graph.Nodes, helper.EntityPkgPath(userType)).GoString()

	if !strings.Contains(userSrc, "q.policy.EvalQuery(ctx, q)") {
		t.Error("User query prepareQuery should call q.policy.EvalQuery(ctx, q) explicitly")
	}
	if !strings.Contains(userSrc, "SetPolicy(p velox.Policy)") {
		t.Error("User query should expose SetPolicy(p velox.Policy) method")
	}
	if strings.Contains(userSrc, "effectiveInters") {
		t.Error("effectiveInters should be removed — privacy is no longer in the interceptor chain")
	}

	// --- Post (no policy) ---
	postSrc := genQueryPkg(helper, postType, graph.Nodes, helper.EntityPkgPath(postType)).GoString()

	if strings.Contains(postSrc, "EvalQuery") {
		t.Error("Post query (no policy) should not reference EvalQuery")
	}
	if strings.Contains(postSrc, "SetPolicy") {
		t.Error("Post query (no policy) should not emit SetPolicy")
	}
	if strings.Contains(postSrc, "effectiveInters") {
		t.Error("Post query (no policy) should not have effectiveInters()")
	}

	// clone() must copy the policy pointer. First/Only/FirstID/OnlyID/Exist
	// all route through q.clone().IDs(ctx), which re-enters prepareQuery;
	// dropping policy on clone silently bypasses tenant/privacy filters.
	userClone := cloneBlock(userSrc, "UserQuery")
	if !strings.Contains(userClone, "q.policy") {
		t.Errorf("User query clone() must copy q.policy — otherwise First/Only/Exist bypass privacy\nclone:\n%s", userClone)
	}
	postClone := cloneBlock(postSrc, "PostQuery")
	if strings.Contains(postClone, "q.policy") {
		t.Error("Post query (no policy) should not reference q.policy in clone()")
	}
}

// cloneBlock extracts the body of the clone() function for the given
// query type from generated source, to assert field-by-field without
// depending on gofmt alignment.
func cloneBlock(src, queryName string) string {
	needle := "func (q *" + queryName + ") clone()"
	i := strings.Index(src, needle)
	if i < 0 {
		return ""
	}
	end := i + 800
	if end > len(src) {
		end = len(src)
	}
	return src[i:end]
}

// TestQueryCloneCopiesEveryField is a structural invariant: every field
// declared on *XxxQuery MUST appear as a key in the clone() composite
// literal. Catches the bug class where a refactor adds a new field to
// the query struct but forgets to extend clone() — which is exactly how
// the 2026-04-15 policy-clone bug slipped in. Parses generated output
// with go/ast so the check is robust against gofmt alignment and field
// order. Covers every entity shape we emit.
func TestQueryCloneCopiesEveryField(t *testing.T) {
	cases := []struct {
		name     string
		features []string
		policy   bool
	}{
		{"no-features", nil, false},
		{"privacy", []string{"privacy"}, true},
		{"schemaconfig", []string{"schemaconfig"}, false},
		{"namedges-with-policy", []string{"privacy", "namedges"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var userType *gen.Type
			if tc.policy {
				userType = createTypeWithPolicies(t, "User", []*load.Position{{MixedIn: false}})
			} else {
				userType = createTestType("User")
			}
			postType := createTestType("Post")
			userType.Edges = []*gen.Edge{
				createO2MEdge("posts", postType, "posts", "user_id"),
			}
			graph := &gen.Graph{
				Config: &gen.Config{Package: "github.com/test/project/ent"},
				Nodes:  []*gen.Type{userType, postType},
			}

			helper := newFeatureMockHelper().withFeatures(tc.features...)
			helper.graph = graph

			for _, node := range []*gen.Type{userType, postType} {
				src := genQueryPkg(helper, node, graph.Nodes, helper.EntityPkgPath(node)).GoString()
				fields := parseQueryStructFields(t, src, node.Name+"Query")
				cloned := parseCloneCompositeKeys(t, src, node.Name+"Query")
				for _, f := range fields {
					if _, ok := cloned[f]; !ok {
						t.Errorf("%s.clone() does not copy field %q — any state dropped in clone silently breaks First/Only/Exist/FirstID/OnlyID (they all route through q.clone())",
							node.Name+"Query", f)
					}
				}
			}
		})
	}
}

// parseQueryStructFields returns the names of every field declared on
// the given query struct in src.
func parseQueryStructFields(t *testing.T, src, structName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "query.go", src, 0)
	if err != nil {
		t.Fatalf("parse generated source: %v", err)
	}
	var fields []string
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != structName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	if len(fields) == 0 {
		t.Fatalf("struct %s not found or has no fields", structName)
	}
	return fields
}

// parseCloneCompositeKeys returns the set of struct-literal keys used
// in the clone() function's &XxxQuery{...} composite literal. We check
// membership rather than equality to allow the "make named maps after"
// pattern (where withNamedXxx is populated post-Values via assignment).
func parseCloneCompositeKeys(t *testing.T, src, structName string) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "query.go", src, 0)
	if err != nil {
		t.Fatalf("parse generated source: %v", err)
	}
	keys := map[string]struct{}{}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "clone" || fd.Recv == nil {
			continue
		}
		// Match receiver *{structName}.
		if star, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); !ok || id.Name != structName {
				continue
			}
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// Only care about &structName{...} composite.
			switch t := cl.Type.(type) {
			case *ast.Ident:
				if t.Name != structName {
					return true
				}
			default:
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := kv.Key.(*ast.Ident); ok {
					keys[id.Name] = struct{}{}
				}
			}
			return false
		})
		// Also pick up post-literal assignments like c.withNamedPosts = ...
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "c" {
					keys[sel.Sel.Name] = struct{}{}
				}
			}
			return true
		})
	}
	return keys
}

// TestQueryIntersUnifiedAccess guards that all queries (with or without
// privacy) access interceptors via q.inters.<Entity> directly — no
// effectiveInters() wrapper, no per-entity append(...). Privacy runs as
// an explicit prepareQuery step, NOT as an interceptor.
func TestQueryIntersUnifiedAccess(t *testing.T) {
	userType := createTypeWithPolicies(t, "User", []*load.Position{{MixedIn: false}})
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}

	helper := newFeatureMockHelper().withFeatures("privacy")
	helper.graph = graph

	for _, node := range []*gen.Type{userType, postType} {
		src := genQueryPkg(helper, node, graph.Nodes, helper.EntityPkgPath(node)).GoString()
		if strings.Contains(src, "effectiveInters") {
			t.Errorf("%s query should not have effectiveInters", node.Name)
		}
		if strings.Contains(src, "append(q.inters."+node.Name) {
			t.Errorf("%s query should not merge package Interceptors into q.inters.%s — privacy is now explicit", node.Name, node.Name)
		}
	}
}

// TestQueryHasInterceptorStorePointer guards that every generated
// *EntityQuery struct holds a pointer to *entity.InterceptorStore for
// client-level interceptors, not a per-query []Interceptor slice copy.
// This eliminates the slice-copy alloc on every query construction
// and makes client.Intercept(...) immediately visible to all queries
// holding the same pointer.
func TestQueryHasInterceptorStorePointer(t *testing.T) {
	graph, userType, postType := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	for _, node := range []*gen.Type{userType, postType} {
		src := renderQueryPkg(t, helper, node)
		if strings.Contains(src, "inters             []Interceptor") ||
			strings.Contains(src, "inters []runtime.Interceptor") ||
			strings.Contains(src, "inters []velox.Interceptor") {
			t.Errorf("entity %s query still has []Interceptor slice field for "+
				"client-level interceptors; use *entity.InterceptorStore pointer instead",
				node.Name)
		}
		if !strings.Contains(src, "InterceptorStore") {
			t.Errorf("entity %s query is missing *InterceptorStore pointer field; "+
				"the SP-2 refactor requires it for shared-pointer client-level wiring",
				node.Name)
		}
	}
}

// TestQueryImplementsQueryReader guards that the generated query type
// has QueryReader getter methods (instead of the old queryBase() allocation)
// and delegates to runtime.BuildQueryFrom / BuildSelectorFrom.
func TestQueryImplementsQueryReader(t *testing.T) {
	graph, userType, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	src := renderQueryPkg(t, helper, userType)

	for _, getter := range []string{
		"func (q *UserQuery) GetDriver()",
		"func (q *UserQuery) GetTable()",
		"func (q *UserQuery) GetColumns()",
		"func (q *UserQuery) GetFKColumns()",
		"func (q *UserQuery) GetIDFieldType()",
		"func (q *UserQuery) GetPath()",
		"func (q *UserQuery) GetPredicates()",
		"func (q *UserQuery) GetOrder()",
		"func (q *UserQuery) GetModifiers()",
		"func (q *UserQuery) GetWithFKs()",
	} {
		if !strings.Contains(src, getter) {
			t.Errorf("missing QueryReader getter: %s", getter)
		}
	}

	if strings.Contains(src, "func (q *UserQuery) queryBase()") {
		t.Error("queryBase() method still present; should be replaced by QueryReader getters")
	}

	if !strings.Contains(src, "BuildQueryFrom") {
		t.Error("buildQuery should delegate to runtime.BuildQueryFrom")
	}
	if !strings.Contains(src, "BuildSelectorFrom") {
		t.Error("buildSelector should delegate to runtime.BuildSelectorFrom")
	}
}

func TestWhereUsesNamedPredicateType(t *testing.T) {
	graph, userType, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	src := renderQueryPkg(t, helper, userType)

	// Where should accept predicate.User
	if !strings.Contains(src, "predicate.User") {
		t.Error("Where() should use predicate.User type, not raw func(*sql.Selector)")
	}
}

// TestUpdateOneSelectFieldsRestriction guards that UpdateOne.sqlSave
// respects selectFields for both the UPDATE SET clause and the
// post-update re-query. Without this, UpdateOne.Select("name") would
// still update ALL mutated fields (the B3 bug).
func TestUpdateOneSelectFieldsRestriction(t *testing.T) {
	graph, userType, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	file, err := genUpdate(helper, userType)
	if err != nil {
		t.Fatalf("genUpdate: %v", err)
	}
	src := file.GoString()

	// The UpdateOne.sqlSave must reference selectFields to guard field operations.
	if !strings.Contains(src, "len(_u.selectFields)") {
		t.Error("UpdateOne.sqlSave does not check len(_u.selectFields); " +
			"Select() restriction on SET clause is not implemented")
	}

	// The UpdateOne.sqlSave must use slices.Contains to filter by selectFields.
	if !strings.Contains(src, "slices.Contains(_u.selectFields") {
		t.Error("UpdateOne.sqlSave does not use slices.Contains on selectFields; " +
			"individual field filtering is not implemented")
	}

	// The post-update re-query must use selectFields for column selection.
	if !strings.Contains(src, `columns = append([]string{user.FieldID}, _u.selectFields...)`) {
		t.Error("UpdateOne.sqlSave re-query does not narrow columns by selectFields")
	}

	// The bulk UserUpdate.sqlSave must NOT reference selectFields (it has no Select method).
	// Find the bulk sqlSave body — it ends at the next top-level func declaration.
	bulkIdx := strings.Index(src, "func (_u *UserUpdate) sqlSave(")
	if bulkIdx < 0 {
		t.Fatal("could not find UserUpdate.sqlSave in generated code")
	}
	// Find the end of the bulk sqlSave: next "func " at the start of a line.
	bulkRest := src[bulkIdx+1:]
	nextFunc := strings.Index(bulkRest, "\nfunc ")
	if nextFunc < 0 {
		t.Fatal("could not find end of UserUpdate.sqlSave")
	}
	bulkBody := bulkRest[:nextFunc]
	if strings.Contains(bulkBody, "selectFields") {
		t.Error("UserUpdate (bulk) sqlSave references selectFields; " +
			"selectFields is only for UpdateOne")
	}
}

// TestSelectScanUsesDirectInters guards that Select.Scan and GroupBy.Scan
// always access q.inters.<Entity> directly, regardless of whether the
// entity has a privacy policy. Privacy is evaluated at prepareQuery time
// via q.policy.EvalQuery — it is NOT part of the interceptor chain, so
// the append(...) merge is gone.
func TestSelectScanUsesDirectInters(t *testing.T) {
	userType := createTypeWithPolicies(t, "User", []*load.Position{{MixedIn: false}})
	postType := createTestType("Post") // no policy
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}

	helper := newFeatureMockHelper().withFeatures("privacy")
	helper.graph = graph

	for _, node := range []*gen.Type{userType, postType} {
		src := genQueryPkg(helper, node, graph.Nodes, helper.EntityPkgPath(node)).GoString()
		if strings.Contains(src, "effectiveInters") {
			t.Errorf("%s: effectiveInters must be removed", node.Name)
		}
		// Each Select.Scan and GroupBy.Scan body should reference q.inters.<Entity>
		// (exact access pattern via ScanWithInterceptors).
		needle := ".inters." + node.Name
		selectIdx := strings.Index(src, "func (s *"+node.Name+"Select) Scan(")
		if selectIdx < 0 {
			t.Fatalf("%s: could not find Select.Scan", node.Name)
		}
		rest := src[selectIdx:]
		end := strings.Index(rest[1:], "\nfunc ")
		if end < 0 {
			t.Fatalf("%s: could not find end of Select.Scan", node.Name)
		}
		body := rest[:end+1]
		if !strings.Contains(body, needle) {
			t.Errorf("%sSelect.Scan should reference %s directly", node.Name, needle)
		}
	}
}

// TestConfigMethodNameHonoredInCreateAndUpdate pins that the config-propagation
// call sites in genCreate/genUpdate (_node and _old) route through
// Type.SetConfigMethodName() instead of a hardcoded "SetConfig". If the schema
// has a user field named "Config" or "config", the entity package emits
// SetRuntimeConfig to avoid clashing with a field-setter; hardcoding
// "SetConfig" at these paths would compile-break such schemas.
func TestConfigMethodNameHonoredInCreateAndUpdate(t *testing.T) {
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "Config", Info: &field.TypeInfo{Type: field.TypeString}},
	})
	if got := userType.SetConfigMethodName(); got != "SetRuntimeConfig" {
		t.Fatalf("fixture precondition failed: SetConfigMethodName=%q, want SetRuntimeConfig", got)
	}
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType},
	}
	helper := newMockHelper()
	helper.graph = graph

	createFile, err := genCreate(helper, userType)
	if err != nil {
		t.Fatalf("genCreate: %v", err)
	}
	updateFile, err := genUpdate(helper, userType)
	if err != nil {
		t.Fatalf("genUpdate: %v", err)
	}

	for _, tc := range []struct {
		label string
		src   string
	}{
		{"create", createFile.GoString()},
		{"update", updateFile.GoString()},
	} {
		// The _node / _old / nodes[i] propagation sites must resolve to
		// SetRuntimeConfig when the schema collides with "Config".
		if strings.Contains(tc.src, ".SetConfig(_u.config)") ||
			strings.Contains(tc.src, ".SetConfig(_c.config)") ||
			strings.Contains(tc.src, ".SetConfig(_cb.config)") {
			t.Errorf("%s: generator emitted hardcoded SetConfig on a config-field schema; "+
				"must use Type.SetConfigMethodName() so it becomes SetRuntimeConfig when "+
				"the schema has a user-defined Config/config field", tc.label)
		}
		if !strings.Contains(tc.src, ".SetRuntimeConfig(") {
			t.Errorf("%s: generator did not emit SetRuntimeConfig for a schema with a Config field", tc.label)
		}
	}
}

// TestMutationImplementsFilterable guards that, when FeaturePrivacy is
// enabled, genMutation emits AddPredicate + Filter() methods on
// *XxxMutation so that privacy.FilterFunc can inject WHERE clauses into
// Update/Delete mutations the same way it does on queries. Without
// these methods, FilterFunc.EvalMutation always returns privacy.Deny
// because *XxxMutation does not implement privacy.Filterable.
//
// Pinned regression: prior to 2026-04-14 the mutation generator had no
// Filter emission at all (only the Query side did), so every mutation
// FilterFunc rule silently denied at runtime.
func TestMutationImplementsFilterable(t *testing.T) {
	userType := createTypeWithPolicies(t, "User", []*load.Position{{MixedIn: false}})
	postType := createTestType("Post") // no policy

	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}
	helper := newFeatureMockHelper().withFeatures("privacy")
	helper.graph = graph

	// User (privacy enabled) must emit AddPredicate + Filter().
	userSrc := genMutation(helper, userType).GoString()
	if !strings.Contains(userSrc, "func (m *UserMutation) AddPredicate(") {
		t.Error("UserMutation must emit AddPredicate(func(*sql.Selector)) when FeaturePrivacy is on")
	}
	if !strings.Contains(userSrc, "func (m *UserMutation) Filter() privacy.Filter") {
		t.Error("UserMutation must emit Filter() privacy.Filter so privacy.FilterFunc can inject predicates")
	}
	if !strings.Contains(userSrc, "NewUserFilter(m.config, m)") {
		t.Error("UserMutation.Filter() must return NewUserFilter(m.config, m) — the filter lives in the same entity sub-package")
	}

	// Post (no privacy feature applied at type level) — but when
	// FeaturePrivacy is globally on, every mutation gets the methods
	// so policy rules at higher layers still work. Match the query
	// side which emits Filter() for all entities when the feature
	// flag is on, not per-entity policy presence.
	postSrc := genMutation(helper, postType).GoString()
	if !strings.Contains(postSrc, "func (m *PostMutation) Filter() privacy.Filter") {
		t.Error("PostMutation must also emit Filter() when FeaturePrivacy is on — the feature flag is global, not per-policy")
	}
}

// TestEntityRuntimeRegistersNodeResolver guards that genEntityRuntime emits a
// runtime.RegisterNodeResolver call alongside RegisterEntity. Without this,
// runtime.NodeResolvers() is empty and client.Noder always fails to resolve.
func TestEntityRuntimeRegistersNodeResolver(t *testing.T) {
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	src := genEntityRuntime(helper, userType).GoString()

	if !strings.Contains(src, "runtime.RegisterNodeResolver(Table") {
		t.Error("entity runtime.go must call runtime.RegisterNodeResolver(Table, ...) " +
			"so client.Noder can resolve this entity by global ID")
	}
	if !strings.Contains(src, `Type: "User"`) {
		t.Error("RegisterNodeResolver must set Type to the entity name (\"User\")")
	}
	if !strings.Contains(src, "ConfigFromContext(ctx)") {
		t.Error("NodeResolver Resolve closure must read Config via runtime.ConfigFromContext(ctx)")
	}
	if !strings.Contains(src, "NewUserClient(cfg).Get(ctx, typedID)") {
		t.Error("NodeResolver must construct an entity client via NewUserClient(cfg) and call Get")
	}
}

// TestNodeResolverIDTypeAssertion guards that the resolver's id type assertion
// targets the entity's actual Go ID type — int64 for default, uuid.UUID for a
// UUID-id schema. A wrong assertion makes the resolver fail at runtime.
func TestNodeResolverIDTypeAssertion(t *testing.T) {
	t.Run("int64_default", func(t *testing.T) {
		helper := newMockHelper()
		helper.rootPkg = "github.com/test/project/ent"
		userType := createTestType("User")
		src := genEntityRuntime(helper, userType).GoString()
		if !strings.Contains(src, "id.(int64)") {
			t.Error("NodeResolver for int64-id schema must assert id.(int64)")
		}
	})

	t.Run("uuid_id", func(t *testing.T) {
		_, devType := goldenTestTypeUUID()
		helper := newMockHelper()
		helper.rootPkg = "github.com/test/project/ent"
		helper.graph.Nodes = []*gen.Type{devType}
		src := genEntityRuntime(helper, devType).GoString()
		if !strings.Contains(src, "id.(uuid.UUID)") {
			t.Errorf("NodeResolver for UUID-id schema must assert id.(uuid.UUID); got:\n%s", src)
		}
	})
}

// TestMutationTypedStateInvariants pins Velox's core design advantage
// over Ent: mutations store state inline with Go types, not in a
// map[string]any with a generic ConvertOldValue[T] shim. This test
// catches any refactor that regresses toward the Ent model (which
// trades type-safety for smaller generated code).
func TestMutationTypedStateInvariants(t *testing.T) {
	userType := createTestType("User")
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType},
	}
	helper := newFeatureMockHelper()
	helper.graph = graph

	src := genMutation(helper, userType).GoString()

	// 1. oldValue field is typed closure, not a map or any
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "mutation.go", src, 0)
	if err != nil {
		t.Fatalf("parse generated mutation: %v", err)
	}
	mutationStruct := findStruct(t, file, "UserMutation")

	oldValueField := findField(mutationStruct, "oldValue")
	if oldValueField == nil {
		t.Fatal("UserMutation must have an oldValue field (typed closure, not a map)")
	}
	ft, ok := oldValueField.Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("UserMutation.oldValue must be a func type, got %T — regression toward Ent-style generic shim", oldValueField.Type)
	}
	// Return type should be (*entity.User, error)
	if ft.Results == nil || len(ft.Results.List) != 2 {
		t.Fatal("oldValue signature must be func(context.Context) (*entity.User, error)")
	}

	// 2. No field named _changes or fieldChanges of type map[string]any
	for _, f := range mutationStruct.Fields.List {
		for _, name := range f.Names {
			if name.Name == "_changes" || name.Name == "fieldChanges" {
				t.Errorf("UserMutation must NOT have %q field — mutation state is inline+typed, not a map[string]any", name.Name)
			}
		}
	}

	// 3. At least one typed field pointer (e.g. _name *string).
	hasTypedFieldPointer := false
	for _, f := range mutationStruct.Fields.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if id, ok := star.X.(*ast.Ident); ok {
			switch id.Name {
			case "string", "int", "int64", "float64", "bool":
				hasTypedFieldPointer = true
			}
		}
	}
	if !hasTypedFieldPointer {
		t.Error("UserMutation must have at least one typed field pointer (e.g. _name *string) — regression to map-based field changes")
	}

	// 4. Op(), Type(), ID(), SetID() are methods on *UserMutation.
	required := map[string]bool{"Op": false, "Type": false, "ID": false, "SetID": false}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil {
			continue
		}
		star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if id, ok := star.X.(*ast.Ident); !ok || id.Name != "UserMutation" {
			continue
		}
		if _, wanted := required[fd.Name.Name]; wanted {
			required[fd.Name.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("UserMutation must have method %s() on the concrete type (no MutationBase embedding)", name)
		}
	}

	// 5. No import of a generic ConvertOldValue helper.
	if strings.Contains(src, "ConvertOldValue") {
		t.Error("UserMutation must not reference ConvertOldValue[T] — old values read typed fields directly off loaded entity")
	}
}

func findStruct(t *testing.T, file *ast.File, name string) *ast.StructType {
	t.Helper()
	var st *ast.StructType
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != name {
			return true
		}
		if s, ok := ts.Type.(*ast.StructType); ok {
			st = s
		}
		return false
	})
	if st == nil {
		t.Fatalf("struct %s not found", name)
	}
	return st
}

func findField(st *ast.StructType, name string) *ast.Field {
	for _, f := range st.Fields.List {
		for _, n := range f.Names {
			if n.Name == name {
				return f
			}
		}
	}
	return nil
}

// TestUnwrapSwapsTxDriver pins the Ent-parity Unwrap contract: the generated
// Unwrap() must type-assert config.Driver to runtime.TxDriverUnwrapper and
// reassign it to BaseDriver() — NOT be a no-op return.
//
// Regression guard: before 2026-04-15, Unwrap was a no-op (`return e`), so
// entities produced inside a tx silently kept a *txDriver reference after
// Commit and failed GraphQL edge reads with "sql: transaction has already
// been committed". Do NOT revert to a no-op body.
func TestUnwrapSwapsTxDriver(t *testing.T) {
	graph, userType, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	file := genEntityPkgFileWithRegistry(helper, userType, graph.Nodes, nil)
	src := file.GoString()

	if !strings.Contains(src, "func (e *User) Unwrap() *User") {
		t.Fatalf("Unwrap method missing from entity package")
	}
	// Must type-assert to the runtime interface (cross-package, because
	// entity/ cannot import the root *txDriver type).
	if !strings.Contains(src, "runtime.TxDriverUnwrapper") {
		t.Errorf("Unwrap must type-assert config.Driver to runtime.TxDriverUnwrapper; "+
			"a no-op body silently keeps committed *txDriver references alive.\n%s", src)
	}
	// Must reassign the driver via BaseDriver() — without this the assertion
	// is observed but not acted on.
	if !strings.Contains(src, "e.config.Driver = u.BaseDriver()") {
		t.Errorf("Unwrap must reassign e.config.Driver = u.BaseDriver() to swap back to the base driver")
	}
	// Must panic on non-tx entities (Ent parity) so misuse is loud.
	if !strings.Contains(src, `panic("velox: User.Unwrap() called on non-transactional entity")`) {
		t.Errorf("Unwrap must panic when called on a non-transactional entity (Ent parity)")
	}
}

// TestTxDriverExposesBaseDriver pins that the generated *txDriver implements
// runtime.TxDriverUnwrapper by emitting a BaseDriver() method returning drv.
// Without this method, entity Unwrap() panics on every tx-produced entity.
func TestTxDriverExposesBaseDriver(t *testing.T) {
	graph, _, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	src := genTx(helper).GoString()
	if !strings.Contains(src, "func (tx *txDriver) BaseDriver() dialect.Driver") {
		t.Errorf("txDriver must expose BaseDriver() dialect.Driver to satisfy runtime.TxDriverUnwrapper")
	}
	if !strings.Contains(src, "return tx.drv") {
		t.Errorf("BaseDriver() must return tx.drv (the underlying non-tx driver)")
	}
}

// TestGraphQLEdgeResolverUsesDirectCall pins the post-2026-04-15 invariant:
// generated GraphQL edge methods must delegate to entity-level QueryXxx()
// methods (NOT runtime.QueryEdgeUntyped / runtime.PaginateEdge).
//
// Reintroducing the registry silently resurrects Bug 3: O2M resolvers
// using the idColumn fallback return 0 rows (or coincidental matches).
func TestGraphQLEdgeResolverUsesDirectCall(t *testing.T) {
	matches, err := filepath.Glob("../../../examples/fullgql/velox/entity/gql_edge_*.go")
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(matches) == 0 {
		// fullgql/velox/ is gitignored generator output. On a fresh
		// checkout (CI without a prior `go run generate.go` in
		// examples/fullgql) there's nothing to invariant-check. Skip
		// rather than Fatal — the examples (fullgql, test) CI job
		// catches this path via its own generate + build.
		t.Skip("examples/fullgql/velox/ not generated; skipping invariant check")
	}

	banned := []string{
		"runtime.QueryEdgeUntyped",
		"runtime.PaginateEdge",
		"runtime.RegisterEdgeQuery",
		"runtime.RegisterEdgePaginate",
		"runtime.BuildEdgeQuery",
		".AllAny(",
		".OnlyAny(",
	}

	directCall := regexp.MustCompile(`m\.Query[A-Z]\w*\(\)(?:\.\([^)]+\))?\.(All|Only|Paginate)`)

	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("generated file %s must not contain %q — edge resolvers must call m.QueryXxx() directly",
					filepath.Base(path), b)
			}
		}
		if strings.Contains(src, "func (m *") {
			if !directCall.MatchString(src) {
				t.Errorf("generated file %s has edge methods but no direct m.QueryXxx() call — generator regression",
					filepath.Base(path))
			}
		}
	}
}

// TestEnumMethodsHaveDocComments pins that every generated enum method
// (String, IsValid, Scan, Value, MarshalGQL, UnmarshalGQL) carries a
// non-empty doc comment. IDE tooltips surface these for every schema
// enum, so a missing comment silently degrades the user-facing API.
//
// Uses the regenerated integration prototype because the synthetic
// createEnumField helper does not round-trip through genEntityPkg with
// a fully populated *gen.Field; asserting against the real emitted
// prototype matches what users actually see.
func TestEnumMethodsHaveDocComments(t *testing.T) {
	matches, err := filepath.Glob("../../../tests/integration/entity/*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("integration prototype not regenerated; skipping enum docstring scan")
	}

	// Methods that velox emits for every enum type. Value and MarshalGQL
	// are value-receiver; Scan and UnmarshalGQL are pointer-receiver.
	required := []string{"String", "IsValid", "Scan", "Value", "MarshalGQL", "UnmarshalGQL"}

	var scanned bool
	for _, path := range matches {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Base(path), src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		// Find enum method receivers by scanning for a type named XxxStatus / XxxRole / etc.
		// We detect enum types heuristically: any type backed by `type T string`.
		enumTypes := map[string]bool{}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if id, ok := ts.Type.(*ast.Ident); ok && id.Name == "string" {
					enumTypes[ts.Name.Name] = true
				}
			}
		}
		if len(enumTypes) == 0 {
			continue
		}
		scanned = true
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			recvType := fd.Recv.List[0].Type
			if star, ok := recvType.(*ast.StarExpr); ok {
				recvType = star.X
			}
			id, ok := recvType.(*ast.Ident)
			if !ok || !enumTypes[id.Name] {
				continue
			}
			if !slices.Contains(required, fd.Name.Name) {
				continue
			}
			if fd.Doc == nil || strings.TrimSpace(fd.Doc.Text()) == "" {
				t.Errorf("%s: method %s on enum type %s has no doc comment — "+
					"IDE tooltips show this to every user; every exported "+
					"generated method must carry a docstring",
					filepath.Base(path), fd.Name.Name, id.Name)
			}
		}
	}
	if !scanned {
		t.Skip("no enum types found in integration prototype")
	}
}

// TestPerEntityPackageIsTrueLeaf pins the cycle-break invariant: the per-entity
// leaf package ({entity}/{entity}.go) must not import back into the shared
// entity/ package. After Phase A, enum types live in the leaf; there is no
// remaining reason for the leaf to depend on entity/. Any future generator
// change that re-introduces such an import reopens the import-cycle pathway
// that Plan 2 (cycle-break) was written to close.
func TestPerEntityPackageIsTrueLeaf(t *testing.T) {
	t.Parallel()

	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	src := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes)).GoString()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse leaf package source: %v", err)
	}

	// Anything ending in "/entity" (the shared entity package) or starting at
	// the root package path is a cycle-back into the parent. Predicate, sql,
	// sqlgraph, runtime, stdlib are all fine.
	forbiddenSuffix := "/entity"
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.HasSuffix(path, forbiddenSuffix) {
			t.Errorf("leaf package imports %q; the per-entity leaf must not depend "+
				"on entity/ (cycle-break invariant — see Plan 2 Phase A)", path)
		}
	}
}
