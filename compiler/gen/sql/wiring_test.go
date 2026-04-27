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
	return genQueryPkg(h, node, h.graph.Nodes, h.LeafPkgPath(node)).GoString()
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
	userSrc := genQueryPkg(helper, userType, graph.Nodes, helper.LeafPkgPath(userType)).GoString()

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
	postSrc := genQueryPkg(helper, postType, graph.Nodes, helper.LeafPkgPath(postType)).GoString()

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
				src := genQueryPkg(helper, node, graph.Nodes, helper.LeafPkgPath(node)).GoString()
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
		src := genQueryPkg(helper, node, graph.Nodes, helper.LeafPkgPath(node)).GoString()
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
		src := genQueryPkg(helper, node, graph.Nodes, helper.LeafPkgPath(node)).GoString()
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

	if !strings.Contains(src, "runtime.RegisterNodeResolver(user.Table") {
		t.Error("entity runtime.go must call runtime.RegisterNodeResolver(user.Table, ...) " +
			"(qualified leaf ref after Phase B) so client.Noder can resolve this entity by global ID")
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

// edgeQueryMethodRE matches every generated entity-side edge query method
// shape: `func (_e *X) QueryFoo() FooQuerier { ... }`. Used by the sweep
// tests below to enumerate every emitted edge query body without relying
// on graph-fixture knowledge of which edges exist.
var edgeQueryMethodRE = regexp.MustCompile(`(?m)^func \(_e \*\w+\) Query(\w+)\(\)\s+\w+Querier\s*\{`)

// extractEdgeQueryBody returns the brace-balanced body of the edge query
// method whose declaration starts at offset `start` in src. Returns the
// entity-method body string (between the outer `{` and matching `}`),
// or "" if the source is malformed.
func extractEdgeQueryBody(src string, start int) string {
	open := strings.IndexByte(src[start:], '{')
	if open < 0 {
		return ""
	}
	depth := 1
	i := start + open + 1
	for i < len(src) && depth > 0 {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	if depth != 0 {
		return ""
	}
	return src[start+open : i]
}

// TestEveryEdgeQueryWiresInterStoreAndPath sweeps the regenerated
// integration prototype and asserts that EVERY generated edge-query
// method (`(*X).QueryY()`) calls BOTH `SetInterStore(...)` AND
// `SetPath(...)` in its body. This is the structural defense against
// the reviewer-flagged risk class: "一條 edge query 少 wire 一個 setter"
// (a generator change that emits a new edge query but forgets one of
// the two required setter calls). A missing SetInterStore causes a nil
// pointer panic at runtime when interceptors are registered; a missing
// SetPath bypasses the FK column entirely and silently returns wrong
// rows. CLAUDE.md documents these invariants but until now there was
// no comprehensive structural test enforcing them across every emitted
// edge.
//
// Skipped if the integration prototype hasn't been regenerated.
func TestEveryEdgeQueryWiresInterStoreAndPath(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob("../../../tests/integration/entity/*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("integration prototype not regenerated; skipping edge-query sweep")
	}

	scanned := 0
	for _, path := range matches {
		base := filepath.Base(path)
		// gql_*, hooks.go, edges.go, etc. — only entity{name}.go-style files
		// declare the per-entity QueryX methods. Filter on emit pattern.
		if strings.HasPrefix(base, "gql_") || base == "hooks.go" || base == "edges.go" ||
			base == "node.go" || base == "config.go" || base == "client.go" ||
			strings.HasPrefix(base, "build_") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(raw)

		for _, m := range edgeQueryMethodRE.FindAllStringSubmatchIndex(src, -1) {
			edgeName := src[m[2]:m[3]]
			body := extractEdgeQueryBody(src, m[0])
			if body == "" {
				t.Errorf("%s: malformed edge-query body for Query%s", path, edgeName)
				continue
			}
			scanned++
			if !strings.Contains(body, "SetInterStore(") {
				t.Errorf("%s::Query%s body lacks SetInterStore(...) call — "+
					"interceptors will not propagate to this edge query, breaking "+
					"the shared-pointer wiring contract (see CLAUDE.md gotcha "+
					"\"Entity-level edge queries ... must wire BOTH SetInterStore AND SetPath\")",
					base, edgeName)
			}
			if !strings.Contains(body, "SetPath(") {
				t.Errorf("%s::Query%s body lacks SetPath(...) call — "+
					"the edge query has no foreign-key step and will silently "+
					"return wrong rows (typically `WHERE target.id = owner.id` "+
					"instead of the intended FK predicate)", base, edgeName)
			}
		}
	}
	if scanned == 0 {
		t.Skip("no edge-query methods found in entity/ — schema has no edges?")
	}
}

// TestEveryEdgeQueryToPolicyEntitySetsPolicy is the structural defense
// against the reviewer-flagged risk: "privacy 回到 interceptor chain"
// in its specific edge-query manifestation. When `(*User).QueryPosts()`
// constructs the Post query, it MUST look up the target entity's policy
// via `runtime.EntityPolicy("Post")` and call `SetPolicy(...)` on the
// constructed query — otherwise the edge traversal silently bypasses
// the target's privacy rules.
//
// The current generator emits the SetPolicy guarded block unconditionally
// (it's a no-op at runtime when the target has no policy). This test
// asserts that block is present in every edge-query body so a future
// generator simplification ("we only need SetPolicy when target has a
// policy") can't accidentally drop the lookup for entities that ARE
// policy-bearing.
func TestEveryEdgeQueryToPolicyEntitySetsPolicy(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob("../../../tests/integration/entity/*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("integration prototype not regenerated; skipping policy-edge sweep")
	}

	scanned := 0
	missing := 0
	for _, path := range matches {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "gql_") || base == "hooks.go" || base == "edges.go" ||
			base == "node.go" || base == "config.go" || base == "client.go" ||
			strings.HasPrefix(base, "build_") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(raw)

		for _, m := range edgeQueryMethodRE.FindAllStringSubmatchIndex(src, -1) {
			edgeName := src[m[2]:m[3]]
			body := extractEdgeQueryBody(src, m[0])
			if body == "" {
				continue
			}
			scanned++
			// Both halves of the SetPolicy block must be present:
			// (a) registry lookup,  (b) conditional SetPolicy call.
			hasLookup := strings.Contains(body, "runtime.EntityPolicy(")
			hasSetPolicy := strings.Contains(body, "SetPolicy(")
			if !hasLookup || !hasSetPolicy {
				missing++
				t.Errorf("%s::Query%s body lacks the runtime.EntityPolicy(...) "+
					"lookup + SetPolicy(...) wiring — edge traversal to a policy-"+
					"bearing target will silently bypass privacy rules. Has lookup: %v, "+
					"Has SetPolicy: %v", base, edgeName, hasLookup, hasSetPolicy)
			}
		}
	}
	if scanned == 0 {
		t.Skip("no edge-query methods found in entity/")
	}
	if missing > 0 {
		t.Logf("scanned=%d edge methods, %d missing the policy wiring", scanned, missing)
	}
}

// TestPrivacyHasNoInterceptorTraces extends TestPolicyExplicitEvaluation
// with negative-invariant breadth: ensures no generated query file in
// the integration prototype carries ANY trace of the pre-2026-04-10
// "privacy lives in the interceptor chain" pattern. Catches the broader
// regression class — not just `effectiveInters()`, but also Hooks[0] /
// Interceptors[0] privacy-slot wrappers, `numHooks++` accounting that
// reserved a slot for privacy, etc.
//
// If a future refactor reintroduces ANY of these patterns, this test
// fails with a precise pointer to the file and the regressed pattern,
// preventing the silent-policy-bypass bug class from coming back via
// generator drift.
func TestPrivacyHasNoInterceptorTraces(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob("../../../tests/integration/query/*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("integration prototype not regenerated; skipping privacy-trace scan")
	}

	// Each entry: regex pattern + human-readable explanation of the bug
	// class it pins. The patterns are scoped narrowly so we don't false-
	// match unrelated occurrences (e.g. legitimate Hooks[i] indexing
	// inside the hook chain itself, only at index 0 with a privacy
	// neighbor).
	type forbidden struct {
		pattern string
		reason  string
	}
	forbiddenPatterns := []forbidden{
		{"effectiveInters", "effectiveInters() merged privacy with interceptors; privacy is now its own explicit step"},
		{"Hooks[0] = privacy.", "Hooks[0] = privacy.X reintroduces the privacy-as-hook-slot pattern"},
		{"Interceptors[0] = privacy.", "Interceptors[0] = privacy.X reintroduces the privacy-as-interceptor-slot pattern"},
		{"numHooks++ // privacy", "numHooks++ accounting for privacy means it's been re-added to the hook chain"},
		{"numInterceptors++ // privacy", "numInterceptors++ accounting for privacy means it's been re-added to the interceptor chain"},
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(raw)
		for _, f := range forbiddenPatterns {
			if strings.Contains(src, f.pattern) {
				t.Errorf("%s contains forbidden pattern %q — %s",
					filepath.Base(path), f.pattern, f.reason)
			}
		}
	}
}

// TestValidateRegistriesEmitted is a structural pin: the root client must emit
// (a) a package-level `expectedEntities` slice that lists every entity in the
// schema, and (b) a `(*Client).ValidateRegistries() error` method that walks
// that slice and consults the runtime non-panicking accessors.
//
// Without this guard, a future generator refactor could silently drop the
// emission and the fail-fast guarantee at startup would regress to the latent
// runtime.NewEntityQuery panic class — visible only on the path that triggers
// an unimported entity, which a healthy CI suite typically would not exercise.
func TestValidateRegistriesEmitted(t *testing.T) {
	graph, _, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	src := genClient(helper).GoString()

	// expectedEntities slice exists and lists every node in the graph by name.
	if !strings.Contains(src, "var expectedEntities = []expectedEntity{") {
		t.Fatal("client.go must declare `var expectedEntities = []expectedEntity{...}` " +
			"so ValidateRegistries has something to iterate")
	}
	for _, n := range graph.Nodes {
		needle := `"` + n.Name + `"`
		if !strings.Contains(src, needle) {
			t.Errorf("expectedEntities is missing entry for entity %q — schema/graph drift "+
				"will leave that entity unchecked at startup", n.Name)
		}
	}

	// Method signature.
	if !strings.Contains(src, "func (c *Client) ValidateRegistries() error {") {
		t.Error("client.go must declare `func (c *Client) ValidateRegistries() error` " +
			"as the user-facing fail-fast entry point")
	}

	// Required runtime checks. We intentionally pin the *names* of the runtime
	// accessors used: any rename or replacement should be a deliberate decision
	// that touches this test.
	required := []struct {
		fragment string
		why      string
	}{
		{
			fragment: "runtime.HasEntityRegistration(e.name)",
			why:      "primary signal — RegisterEntity covers mutator + query factory + entity client",
		},
		{
			fragment: "runtime.HasNodeResolver(e.table)",
			why:      "RegisterNodeResolver is emitted alongside RegisterEntity; missing it means init() did not run",
		},
		{
			fragment: "runtime.HasColumns(e.table)",
			why:      "RegisterColumns is populated by RegisterEntity internally; it is the cross-check that init() ran",
		},
		{
			fragment: "runtime.HasEntityPolicy(e.name)",
			why:      "policy registration is feature-conditional; ValidateRegistries must check it when the schema declares one",
		},
	}
	for _, r := range required {
		if !strings.Contains(src, r.fragment) {
			t.Errorf("ValidateRegistries body must call %s — %s", r.fragment, r.why)
		}
	}

	// Structured error path: returns fmt.Errorf with a leading marker so users
	// can grep startup logs for it.
	if !strings.Contains(src, `"velox: registry validation failed:`) {
		t.Error("ValidateRegistries must return a `velox: registry validation failed:` " +
			"prefixed error so startup logs are greppable")
	}
}

// TestValidateRegistriesIsOptIn pins the deliberate non-feature: NewClient and
// Open MUST NOT auto-invoke ValidateRegistries. Some users deliberately run
// with partial imports (e.g. a worker that only writes a subset of entities),
// and auto-invocation would break those setups silently. This test is the line
// of defense against a well-meaning future change that wires ValidateRegistries
// into the constructor.
func TestValidateRegistriesIsOptIn(t *testing.T) {
	graph, _, _ := buildWiringTestGraph(t)
	helper := newMockHelper()
	helper.graph = graph

	src := genClient(helper).GoString()

	for _, fn := range []string{"func NewClient", "func Open"} {
		body := extractFuncBody(t, src, fn)
		if strings.Contains(body, "ValidateRegistries") {
			t.Errorf("%s must NOT auto-invoke ValidateRegistries — it is opt-in by design "+
				"(see godoc on the method); auto-invocation breaks partial-import setups",
				fn)
		}
	}
}

// extractFuncBody returns the substring of src starting at the first
// declaration matching fnHeader and ending at its matching closing brace.
// Brace matching is naive (no string-literal awareness) but sufficient for the
// generated client.go shape we control.
func extractFuncBody(t *testing.T, src, fnHeader string) string {
	t.Helper()
	start := strings.Index(src, fnHeader)
	if start == -1 {
		t.Fatalf("could not find %q in generated client", fnHeader)
	}
	depth := 0
	seenOpen := false
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
			seenOpen = true
		case '}':
			depth--
			if seenOpen && depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("could not find balanced braces for %q starting at offset %d", fnHeader, start)
	return ""
}
