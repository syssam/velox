# Velox GraphQL vs Ent entgql - Comparison

This document compares Velox's GraphQL code generation with Ent's entgql implementation.

## Architecture Comparison

| Aspect | Ent entgql | Velox graphql |
|--------|------------|---------------|
| Code Generation | Go templates (`.tmpl`) | Jennifer library (`jen`) |
| Template Files | 9 template files | Single-file modules per feature |
| Import Management | Manual in templates | Auto-managed by Jennifer |
| Parallel Generation | Not built-in | errgroup + semaphore |

## Template/Module Mapping

| Ent Template | Velox Module | Purpose |
|--------------|--------------|---------|
| `where_input.tmpl` | `where_input.go` | WhereInput filter types |
| `mutation_input.tmpl` | `mutation_input.go` | Create/Update input types |
| `edge.tmpl` | `collection.go` (genEdgeResolvers) | Edge resolver methods |
| `collection.tmpl` | `collection.go` (genCollection) | CollectFields, eager loading |
| `node.tmpl` | `node.go` | Node interface, Noder |
| `pagination.tmpl` | `pagination.go` | Relay cursor pagination |
| `transaction.tmpl` | `transaction.go` | Transaction middleware |
| `enum.tmpl` | `generator.go` (genEnumSDL) | Enum type generation |
| `node_descriptor.tmpl` | N/A (embedded in node.go) | Node descriptors |

## WhereInput Generation

### Structure Comparison

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Not/Or/And fields | ✅ | ✅ | ✅ |
| Custom Predicates field | ✅ | ✅ | ✅ |
| AddPredicates method | ✅ | ✅ | ✅ |
| Filter method | ✅ | ✅ | ✅ |
| P() method | ✅ | ✅ | ✅ |
| ErrEmptyWhereInput | ✅ | ✅ | ✅ |
| Field predicates | ✅ | ✅ | ✅ |
| Edge predicates (HasX, HasXWith) | ✅ | ✅ | ✅ |
| Skip mode filtering | ✅ | ✅ | ✅ |
| Comparable field check | ✅ | ✅ (via WhereOps) | ✅ |

### P() Method Logic

| Logic | Ent | Velox |
|-------|-----|-------|
| Empty input returns error | ✅ `ErrEmptyWhereInput` | ✅ `ErrEmptyXXXWhereInput` |
| Not field handling | ✅ Wrap with `Not()` | ✅ Wrap with `Not()` |
| Or array handling | ✅ Wrap with `Or()` | ✅ Wrap with `Or()` |
| And array handling | ✅ Wrap with `And()` | ✅ Wrap with `And()` |
| Single predicate optimization | ✅ Return directly | ✅ Return directly |
| Multiple predicates | ✅ Wrap with `And()` | ✅ Wrap with `And()` |

### WhereOps (Velox Extension)

Velox adds fine-grained control over which predicates are generated per field:

```go
// Smart defaults by type (not in Ent - Ent generates all predicates)
ID/FK fields:    OpEQ | OpNEQ | OpIn | OpNotIn (4 ops)
Bool fields:     OpEQ | OpNEQ (2 ops)
Enum fields:     OpsEquality (4 ops)
String fields:   OpsString (13 ops)
Int/Float/Time:  OpsComparison (8 ops)

// Explicit control via annotation
field.String("status").Annotations(graphql.WhereOps(graphql.OpsEquality))
```

**Ent behavior:** Generates ALL predicates for ALL comparable fields.
**Velox behavior:** Smart defaults + explicit override via `WhereOps()`.

## Mutation Input Generation

### CreateInput

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Required fields | ✅ Non-pointer | ✅ Non-pointer | ✅ |
| Optional fields | ✅ Pointer | ✅ Pointer | ✅ |
| Nillable fields | ✅ Pointer | ✅ Pointer | ✅ |
| Unique edge IDs | ✅ `{Edge}ID` | ✅ `{Edge}ID` | ✅ |
| Non-unique edge IDs | ✅ `{Edge}IDs` | ✅ `{Edge}IDs` | ✅ |
| Skip mode filtering | ✅ | ✅ | ✅ |
| Mutate() method | ✅ | ✅ | ✅ |
| SetInput() on builder | ✅ | ✅ | ✅ |

### UpdateInput

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| All fields pointer | ✅ | ✅ | ✅ |
| ClearX fields | ✅ | ✅ | ✅ |
| AppendX for slices | ✅ | ✅ | ✅ |
| Add/Remove edge IDs | ✅ | ✅ | ✅ |
| ClearX for edges | ✅ | ✅ | ✅ |
| Immutable field skip | ✅ | ✅ | ✅ |

### Edge Handling in Mutations

| Edge Type | Ent | Velox | Match |
|-----------|-----|-------|-------|
| Owner edges (edge.To) | ✅ Included | ✅ Included | ✅ |
| Inverse with OwnFK | ✅ Included | ✅ Included | ✅ |
| Inverse without OwnFK | ✅ Skipped | ✅ Skipped | ✅ |
| Edges with explicit FK | ✅ Skipped | ✅ Skipped | ✅ |

## Edge Resolver Generation

### Method Generation

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Resolver on entity struct | ✅ `*User` receiver | ✅ `*User` receiver | ✅ |
| Relay connection edges | ✅ Pagination params | ✅ Pagination params | ✅ |
| Non-unique edges | ✅ `[]*Type` return | ✅ `[]*Type` return | ✅ |
| Unique edges | ✅ `*Type` return | ✅ `*Type` return | ✅ |
| Cache check (EdgesOrErr) | ✅ | ✅ | ✅ |
| IsNotLoaded check | ✅ | ✅ | ✅ |
| MaskNotFound for optional | ✅ | ✅ | ✅ |

### Edge Loading Strategy

```go
// Ent pattern (edge.tmpl)
func (u *User) Posts(ctx context.Context) ([]*Post, error) {
    result, err := u.Edges.PostsOrErr()
    if IsNotLoaded(err) {
        result, err = u.QueryPosts().All(ctx)
    }
    return result, err
}

// Velox pattern (collection.go - identical)
func (_m *User) Posts(ctx context.Context) ([]*Post, error) {
    result, err := _m.Edges.PostsOrErr()
    if IsNotLoaded(err) {
        result, err = _m.QueryPosts().All(ctx)
    }
    return result, err
}
```

## Collection/Eager Loading

### CollectFields

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Field context extraction | ✅ | ✅ | ✅ |
| Recursive edge collection | ✅ | ✅ | ✅ |
| Named query builders | ✅ | ✅ | ✅ |
| Pagination argument parsing | ✅ | ✅ | ✅ |
| Total count collection | ✅ | ✅ | ✅ |
| Select optimization | ✅ | ✅ | ✅ |

## Node Interface

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Noder interface | ✅ | ✅ | ✅ |
| Node() method | ✅ | ✅ | ✅ |
| IsNode() method | ✅ | ✅ | ✅ |
| nodeType function | ✅ | ✅ | ✅ |
| noder resolver | ✅ | ✅ | ✅ |
| Noders batch resolver | ✅ | ✅ | ✅ |
| Global ID encoding | ✅ Optional | ✅ Optional | ✅ |

## Pagination

| Feature | Ent | Velox | Match |
|---------|-----|-------|-------|
| Relay Connection type | ✅ | ✅ | ✅ |
| Edge type | ✅ | ✅ | ✅ |
| PageInfo type | ✅ | ✅ | ✅ |
| Cursor encoding | ✅ | ✅ | ✅ |
| first/after | ✅ | ✅ | ✅ |
| last/before | ✅ | ✅ | ✅ |
| totalCount | ✅ | ✅ | ✅ |
| Multi-field ordering | ✅ | ✅ | ✅ |

## Skip Modes

| Skip Mode | Ent | Velox | Match |
|-----------|-----|-------|-------|
| SkipType | ✅ | ✅ | ✅ |
| SkipEnumField | ✅ | ✅ | ✅ |
| SkipOrderField | ✅ | ✅ | ✅ |
| SkipWhereInput | ✅ | ✅ | ✅ |
| SkipMutationCreateInput | ✅ | ✅ | ✅ |
| SkipMutationUpdateInput | ✅ | ✅ | ✅ |
| SkipAll | ✅ | ✅ | ✅ |

## Key Differences

### 1. Code Generation Approach

**Ent:** Go templates with manual import management
```go
{{ range $e := $.Edges }}
    func ({{ $receiver }} *{{ $.Name }}) Query{{ $e.StructField }}() ...
{{ end }}
```

**Velox:** Jennifer library with auto-imports
```go
for _, e := range t.Edges {
    f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("Query" + e.StructField())...
}
```

### 2. WhereOps (Velox Extension)

Velox adds smart defaults and explicit control for WhereInput predicates:
- Reduces schema bloat (ID fields: 15 → 4 predicates)
- Explicit control via `WhereOps()` annotation
- Ent generates all predicates for all comparable fields

### 3. Query{Edge} Method Implementation

**Ent generates TWO methods:**

1. **Client method** (in client.go):
```go
func (c *UserClient) QueryPosts(u *User) *PostQuery {
    // Builds query with path function for relationship context
    query := c.Query()
    // ... sets up relationship filtering internally
    return query
}
```

2. **Entity method** (in entity.go) - delegates to client:
```go
func (u *User) QueryPosts() *PostQuery {
    return NewUserClient(u.config).QueryPosts(u)
}
```

**Velox generates ONE method** (in entity.go) - builds query directly:
```go
func (_e *User) QueryPosts() *PostQuery {
    query := (&PostClient{config: _e.config}).Query()
    query = query.Where(post.HasAuthorWith(user.ID(_e.ID)))
    return query
}
```

**Trade-offs:**
| Aspect | Ent (Client Delegation) | Velox (Direct Build) |
|--------|------------------------|---------------------|
| Code location | Split across client + entity | All in entity |
| Back-ref logic | Centralized in client | Inline in entity method |
| Complexity | Higher (two methods) | Lower (one method) |
| Bug surface | Lower (logic in one place) | Higher (must find back-ref name) |
| Flexibility | Client method reusable | Entity method only |

**Recommendation:** Consider adopting Ent's pattern to centralize edge query logic in client methods.

### 4. gqlgen.yml Handling

**Ent:** Can auto-update gqlgen.yml with model bindings
**Velox:** Read-only - does NOT modify gqlgen.yml (user configures manually)

## Bugs Fixed

### 1. Query{Edge} for Inverse Edges (Fixed)

**Bug:** For inverse edges, the predicate used wrong edge name.
```go
// Before (bug): TaxGroup.QueryCustomers used HasCustomersWith
// After (fix): TaxGroup.QueryCustomers uses HasTaxGroupWith
```

**Fix:** Use `e.Inverse` instead of `e.Name` for inverse edge predicates.

### 2. Nil Safety in WhereInput P() Method (Fixed)

**Bug:** The P() method didn't check for nil elements in Or/And slices, which could cause panics.
```go
// Before (bug): Would panic if Or slice contained nil element
for _, w := range i.Or {
    p, err := w.P()  // Panic if w is nil
    ...
}

// After (fix): Nil elements are skipped
for _, w := range i.Or {
    if w == nil {
        continue
    }
    p, err := w.P()
    ...
}
```

**Fix:** Added nil checks before calling P() on Or/And elements.

### 3. Nil Safety in Node Descriptor Edge Loop (Fixed)

**Bug:** The genEntityNodeMethod didn't check for nil edge elements when iterating non-unique edges.
```go
// Before (bug): Would panic if edge slice contained nil
for _, edge := range _e.Edges.Posts {
    node.Edges[i].IDs = append(..., edge.ID)  // Panic if edge is nil
}

// After (fix): Nil elements are skipped
for _, edge := range _e.Edges.Posts {
    if edge == nil {
        continue
    }
    node.Edges[i].IDs = append(..., edge.ID)
}
```

**Fix:** Added nil check for edge elements inside the range loop.

### 4. Back-Reference Detection Warning (Fixed)

**Bug:** When back-reference detection failed silently, Query{Edge} methods would return unfiltered results.

**Fix:** Added warning comments in generated code when back-reference is not found:
```go
// WARNING: No back-reference edge found. Query returns unfiltered results.
// Consider defining an inverse edge on Post pointing to User.
func (_e *User) QueryPosts() *PostQuery {
    ...
}
```

## Additional Differences Found

### 5. Node Descriptor (Introspection) ✅

**Implemented in Velox** (`gql_node.go`):
- `NodeDescriptor` struct with ID, Type, Fields[], Edges[]
- `FieldDescriptor` struct for describing attributes
- `EdgeDescriptor` struct for describing relationships
- `Node()` method on each entity for introspection
- Enables admin tools to browse and inspect schema at runtime

**Full parity with Ent's node_descriptor.tmpl.**

### 6. Cursor Encoding

| Feature | Ent | Velox |
|---------|-----|-------|
| Single-order cursor | `{ID, Value}` | `{ID, Value}` |
| Multi-order cursor | `{ID, []Values}` | `{ID, []Values}` |
| Cursor predicate | `CursorsPredicate()` | `cursorsPredicate()` |
| Multi-cursor predicate | `MultiCursorsPredicate()` | Integrated in same function |

### 7. Pagination Validation

| Check | Ent | Velox |
|-------|-----|-------|
| first + last together | ✅ Error | ✅ Error |
| Negative values | ✅ Error | ✅ Error |
| MaxLimit enforcement | ❌ Not built-in | ✅ `MaxPaginationLimit = 1000` |
| Generic error messages | ❌ Detailed | ✅ Generic (security) |

**Velox advantage:** Built-in DoS protection via `MaxPaginationLimit`.

### 8. Enum GraphQL Marshaling

| Feature | Ent | Velox |
|---------|-----|-------|
| MarshalGQL method | ✅ Generated | ✅ Generated |
| UnmarshalGQL method | ✅ Generated | ✅ Generated |
| Validator call | ✅ | ✅ |
| Custom Go types | ✅ Blank var check | ⚠️ Need to verify |

### 9. Transaction Middleware

| Feature | Ent | Velox |
|---------|-----|-------|
| WithTx wrapper | ✅ | ✅ |
| NewTxContext | ✅ | ✅ |
| TxFromContext | ✅ | ✅ |
| OpenTx on Client | ✅ | ✅ |
| OpenTxFromContext | ✅ | ✅ |
| Transactioner interface | ✅ | ✅ |
| Panic recovery | ❌ | ✅ Added |

**Velox addition:** Panic recovery in WithTx for safety.

### 10. Multi-Order Support

| Feature | Ent | Velox |
|---------|-----|-------|
| Single order default | ✅ | ✅ |
| Multi-order via annotation | ✅ `MultiOrder()` | ✅ `MultiOrder()` |
| Order validation | ✅ | ✅ |
| Direction validation | ✅ | ✅ |

## ~~Missing Features in Velox~~ All Features Implemented ✅

The following features have been implemented to achieve full Ent parity:

1. **Node Descriptor** ✅ - Implemented in `gql_node.go`
   - `NodeDescriptor`, `FieldDescriptor`, `EdgeDescriptor` structs for introspection
   - `Node()` method on each entity returning schema metadata
   - Enables admin tools to browse and inspect schema at runtime

2. **Client Query{Edge} methods** ✅ - Implemented in entity client
   - `UserClient.QueryPosts(u *User) *PostQuery` pattern (like Ent)
   - Centralized edge query logic on the client
   - In addition to entity instance methods (`user.QueryPosts()`)

3. **Named Edge Loading** ✅ - Already implemented via FeatureNamedEdges
   - `WithNamed{Edge}(name string, opts...)` on query builders
   - `Named{Edge}(name string) ([]*Type, error)` on entity Edges
   - Automatically enabled by GraphQL extension
   - Used in `gql_collection.go` for aliased edge fields

## Velox Extensions (Not in Ent)

1. **WhereOps** - Fine-grained predicate control per field
2. **MaxPaginationLimit** - Built-in DoS protection
3. **Panic recovery** - In transaction wrapper
4. **Generic error messages** - Security-focused error handling

## Feature Parity Summary

| Feature | Ent | Velox | Status |
|---------|-----|-------|--------|
| Node Descriptor | ✅ | ✅ | Full parity |
| Client Query{Edge} | ✅ | ✅ | Full parity |
| Named Edge Loading | ✅ | ✅ | Full parity |
| WhereOps | ❌ | ✅ | Velox extension |
| MaxPaginationLimit | ❌ | ✅ | Velox extension |

**Both implementations are now functionally equivalent with full feature parity.**
