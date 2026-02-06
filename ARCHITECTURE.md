# Velox ORM Architecture

> A type-safe Go ORM framework with integrated code generation for GraphQL and gRPC services.

## Table of Contents
- [High-Level Architecture](#high-level-architecture)
- [Schema Definition Flow](#schema-definition-flow)
- [Code Generation Pipeline](#code-generation-pipeline)
- [Runtime Execution Flow](#runtime-execution-flow)
- [Component Reference](#component-reference)
- [Package Dependency Graph](#package-dependency-graph)

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              VELOX ORM FRAMEWORK                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────────────┐ │
│  │  SCHEMA LAYER   │    │  GENERATION     │    │  RUNTIME LAYER          │ │
│  │                 │    │  LAYER          │    │                         │ │
│  │  schema/        │───▶│  graph/         │───▶│  generated/orm/         │ │
│  │  ├─ field/      │    │  gen/           │    │  ├─ client.go           │ │
│  │  ├─ edge/       │    │  ├─ orm/        │    │  ├─ models.go           │ │
│  │  ├─ mixin/      │    │  ├─ graphql/    │    │  ├─ predicate.go        │ │
│  │  ├─ index/      │    │  ├─ grpc/       │    │  └─ {entity}/           │ │
│  │  └─ annotation/ │    │  └─ migrate/    │    │      ├─ query.go        │ │
│  │                 │    │                 │    │      ├─ create.go       │ │
│  └─────────────────┘    └─────────────────┘    │      ├─ update.go       │ │
│          │                      │              │      └─ where.go        │ │
│          │                      │              └─────────────────────────┘ │
│          ▼                      ▼                          │               │
│  ┌─────────────────┐    ┌─────────────────┐               │               │
│  │  velox.go       │    │  config/        │               ▼               │
│  │  Core Types     │    │  velox.yaml     │    ┌─────────────────────────┐ │
│  │  ├─ Interface   │    │                 │    │  RUNTIME SUPPORT        │ │
│  │  ├─ Field       │    └─────────────────┘    │                         │ │
│  │  ├─ Edge        │                           │  velox.go (hooks)       │ │
│  │  ├─ Mixin       │                           │  pagination.go          │ │
│  │  ├─ Hook        │                           │  errors.go              │ │
│  │  └─ Interceptor │                           │  dialect/sql/           │ │
│  └─────────────────┘                           └─────────────────────────┘ │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              GENERATED OUTPUT                               │
├───────────────────┬───────────────────┬────────────────┬───────────────────┤
│  generated/orm/   │  generated/       │  generated/    │  migrations/      │
│                   │  graphql/         │  grpc/         │                   │
│  Type-safe CRUD   │  GraphQL Schema   │  Proto Defs    │  SQL Migrations   │
│  Query Builders   │  Resolvers        │  Service Impl  │  Up/Down Files    │
│  Predicates       │  gqlgen Config    │                │                   │
└───────────────────┴───────────────────┴────────────────┴───────────────────┘
```

---

## Schema Definition Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           SCHEMA DEFINITION FLOW                            │
└─────────────────────────────────────────────────────────────────────────────┘

    DEVELOPER                        SCHEMA PACKAGES                    VELOX CORE
        │                                  │                                 │
        │  1. Define Entity Struct         │                                 │
        ├─────────────────────────────────▶│                                 │
        │                                  │                                 │
        │  type User struct {              │                                 │
        │      velox.Schema ◄──────────────┼─────────────────────────────────┤
        │  }                               │                                 │
        │                                  │                                 │
        │  2. Define Fields                │                                 │
        ├─────────────────────────────────▶│                                 │
        │                                  │                                 │
        │  func (User) Fields() {          │  ┌─────────────────────────┐   │
        │      return []velox.Field{       │  │  schema/field/          │   │
        │          field.String("email")───┼─▶│  ├─ String()            │   │
        │              .Unique()           │  │  ├─ Int64()             │   │
        │              .Index(),           │  │  ├─ Time()              │   │
        │          field.Time("created_at")│  │  ├─ UUID()              │   │
        │              .CreatedAt(),       │  │  ├─ Enum()              │   │
        │      }                           │  │  └─ Custom()            │   │
        │  }                               │  │                         │   │
        │                                  │  │  Fluent Builders ───────┼──▶│ velox.FieldDescriptor
        │                                  │  └─────────────────────────┘   │
        │  3. Define Relationships         │                                 │
        ├─────────────────────────────────▶│                                 │
        │                                  │                                 │
        │  func (User) Edges() {           │  ┌─────────────────────────┐   │
        │      return []velox.Edge{        │  │  schema/edge/           │   │
        │          edge.To("posts",        │  │  ├─ To()   (O2M default)│   │
        │              Post.Type),─────────┼─▶│  ├─ From() (belongs to) │   │
        │      }                           │  │  ├─ Unique() (O2O)      │   │
        │  }                               │  │  └─ Through() (M2M)     │   │
        │                                  │  │  Fluent Builders ───────┼──▶│ velox.EdgeDescriptor
        │                                  │  └─────────────────────────┘   │
        │  4. Apply Mixins                 │                                 │
        ├─────────────────────────────────▶│                                 │
        │                                  │                                 │
        │  func (User) Mixin() {           │  ┌─────────────────────────┐   │
        │      return []velox.Mixin{       │  │  schema/mixin/          │   │
        │          mixin.ID{},─────────────┼─▶│  ├─ ID{}                │   │
        │          mixin.Time{},           │  │  ├─ Time{}              │   │
        │          mixin.SoftDelete{},     │  │  ├─ SoftDelete{}        │   │
        │      }                           │  │  └─ TenantID{}          │   │
        │  }                               │  │                         │   │
        │                                  │  │                         │   │
        │                                  │  │  Provides:              │   │
        │                                  │  │  ├─ Fields()            │   │
        │                                  │  │  ├─ Hooks()             │   │
        │                                  │  │  └─ Interceptors()      │   │
        │                                  │  └─────────────────────────┘   │
        │  5. Register Schemas             │                                 │
        ├─────────────────────────────────▶│                                 │
        │                                  │                                 │
        │  // schema/entities.go           │                                 │
        │  func Schemas() []velox.Interface {                                │
        │      return []velox.Interface{   │                                 │
        │          User{},                 │                                 │
        │          Post{},                 │                                 │
        │          Tag{},                  │                                 │
        │      }                           │                                 │
        │  }                               │                                 │
        ▼                                  ▼                                 ▼
```

### Field Builder Chain

```
field.String("email")
    │
    ├──▶ .Unique()         ──▶ desc.Unique = true
    ├──▶ .Index()          ──▶ desc.Index = true
    ├──▶ .MaxLen(255)      ──▶ desc.Size = 255
    ├──▶ .Nullable()       ──▶ desc.Nullable = true
    ├──▶ .Optional()       ──▶ desc.Optional = true
    ├──▶ .Nillable()       ──▶ desc.Nillable = true  (*string in Go)
    ├──▶ .Default("val")   ──▶ desc.Default = "val"
    ├──▶ .Order()          ──▶ desc.Orderable = true
    ├──▶ .Immutable()      ──▶ desc.Immutable = true
    └──▶ .Descriptor()     ──▶ *velox.FieldDescriptor
```

### Edge Relationship Types

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        EDGE RELATIONSHIP TYPES                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ONE-TO-MANY (User has many Posts) - O2M is the default                 │
│  ───────────────────────────────────────────────────────                │
│                                                                         │
│  // User schema                        // Post schema                   │
│  edge.To("posts", Post.Type)           edge.From("author", User.Type)   │
│                                            .Field("user_id")            │
│                                                                         │
│     ┌──────┐  1      N  ┌──────┐                                        │
│     │ User │───────────▶│ Post │                                        │
│     └──────┘            └──────┘                                        │
│                          user_id (FK)                                   │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ONE-TO-ONE (User has one Profile) - use .Unique()                      │
│  ──────────────────────────────────────────────────                     │
│                                                                         │
│  // User schema                        // Profile schema                │
│  edge.To("profile", Profile.Type)      edge.From("user", User.Type)     │
│      .Unique()                             .Field("user_id")            │
│                                                                         │
│     ┌──────┐  1      1  ┌─────────┐                                     │
│     │ User │───────────▶│ Profile │                                     │
│     └──────┘            └─────────┘                                     │
│                          user_id (FK)                                   │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  MANY-TO-MANY (Post has many Tags via PostTag) - use .Through()         │
│  ───────────────────────────────────────────────────────────            │
│                                                                         │
│  // Post schema                                                         │
│  edge.To("tags", Tag.Type)                                              │
│      .Through(PostTag.Type)                                             │
│                                                                         │
│                                                                         │
│  // PostTag schema (join table)                                         │
│  func (PostTag) Edges() []velox.Edge {                                  │
│      return []velox.Edge{                                               │
│          edge.From("Post", Post.Type).Field("post_id"),                 │
│          edge.From("Tag", Tag.Type).Field("tag_id"),                    │
│      }                                                                  │
│  }                                                                      │
│                                                                         │
│     ┌──────┐  N      N  ┌─────────┐  N      N  ┌─────┐                  │
│     │ Post │───────────▶│ PostTag │◀───────────│ Tag │                  │
│     └──────┘            └─────────┘            └─────┘                  │
│                          post_id                                        │
│                          tag_id                                         │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  SELF-REFERENTIAL M2M (User follows Users via Follow)                   │
│  ─────────────────────────────────────────────────────                  │
│                                                                         │
│  // User schema                                                         │
│  edge.To("following", User.Type)                                        │
│      .Through(Follow.Type)                                              │
│      .Ref("followee")  // disambiguate target                           │
│                                                                         │
│                                                                         │
│  // Follow schema (join table)                                          │
│  func (Follow) Edges() []velox.Edge {                                   │
│      return []velox.Edge{                                               │
│          edge.From("Follower", User.Type).Field("follower_id"),         │
│          edge.From("Followee", User.Type).Field("followee_id"),         │
│      }                                                                  │
│  }                                                                      │
│                                                                         │
│            ┌────────────────────────────┐                               │
│            │                            │                               │
│            ▼        ┌────────┐          │                               │
│     ┌──────────┐───▶│ Follow │◀─────────┘                               │
│     │   User   │    └────────┘                                          │
│     └──────────┘     follower_id                                        │
│                      followee_id                                        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Code Generation Pipeline

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CODE GENERATION PIPELINE                             │
└─────────────────────────────────────────────────────────────────────────────┘

  $ velox generate
        │
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  PHASE 1: CONFIGURATION LOADING                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐          ┌─────────────────────────────────────────┐  │
│  │  velox.yaml     │─────────▶│  config.Config                          │  │
│  │                 │          │  ├─ Schema.Path: "./schema"             │  │
│  │  schema:        │          │  ├─ Output.ORM.Path: "./generated/orm"  │  │
│  │    path: ./schema          │  ├─ Database.Dialect: "postgres"        │  │
│  │  output:        │          │  ├─ Features.SoftDelete: true           │  │
│  │    orm:         │          │  └─ GraphQL.Relay: true                 │  │
│  │      path: ...  │          └─────────────────────────────────────────┘  │
│  │  database:      │                                                       │
│  │    dialect: postgres                                                    │
│  └─────────────────┘                                                       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  PHASE 2: SCHEMA PARSING                                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐          ┌──────────────────────────────────────────┐ │
│  │  schema/*.go    │          │  graph.Graph                             │ │
│  │                 │          │  └─ Types: []*Type                       │ │
│  │  user.go        │─────────▶│      ├─ User                             │ │
│  │  post.go        │  Parser  │      │   ├─ Fields: [id, email, name]    │ │
│  │  tag.go         │          │      │   ├─ Edges: [Posts, Profile]      │ │
│  │  entities.go    │          │      │   └─ Indexes: [...]               │ │
│  └─────────────────┘          │      ├─ Post                             │ │
│                               │      │   ├─ Fields: [id, title, body]    │ │
│  Parser extracts:             │      │   └─ Edges: [Author, Tags]        │ │
│  ├─ Type definitions          │      └─ Tag                              │ │
│  ├─ Fields() calls            │          ├─ Fields: [id, name]           │ │
│  ├─ Edges() calls             │          └─ Edges: [Posts]               │ │
│  ├─ Mixin() calls             └──────────────────────────────────────────┘ │
│  └─ Indexes() calls                                                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  PHASE 3: GRAPH VALIDATION                                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  graph.Validate()                                                          │
│      │                                                                      │
│      ├──▶ Check duplicate type names                                       │
│      ├──▶ Validate field definitions                                       │
│      │    ├─ Type compatibility                                            │
│      │    ├─ Primary key exists                                            │
│      │    └─ Enum values defined                                           │
│      ├──▶ graph.LinkEdges()                                                │
│      │    └─ Resolve edge.Target → *Type reference                         │
│      └──▶ graph.ResolveJoinTableFKs()                                      │
│           └─ Resolve M2M foreign keys from join table edges                │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  PHASE 4: CODE GENERATION                                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐ │
│  │  gen.Pipeline                                                         │ │
│  │  ├─ ORM Generator ─────────────────────────────────────────────────┐  │ │
│  │  │                                                                 │  │ │
│  │  │  gen/orm/generator.go                                           │  │ │
│  │  │      │                                                          │  │ │
│  │  │      ├──▶ client.go      (Database client, entity managers)     │  │ │
│  │  │      ├──▶ models.go      (Entity struct definitions)            │  │ │
│  │  │      ├──▶ predicate.go   (WHERE clause predicates)              │  │ │
│  │  │      ├──▶ pagination.go  (Connection, Edge, PageInfo)           │  │ │
│  │  │      └──▶ {entity}/                                             │  │ │
│  │  │           ├──▶ query.go   (SELECT, WHERE, JOIN)                 │  │ │
│  │  │           ├──▶ create.go  (INSERT)                              │  │ │
│  │  │           ├──▶ update.go  (UPDATE)                              │  │ │
│  │  │           ├──▶ delete.go  (DELETE)                              │  │ │
│  │  │           ├──▶ where.go   (Entity predicates)                   │  │ │
│  │  │           └──▶ order.go   (ORDER BY)                            │  │ │
│  │  │                                                                 │  │ │
│  │  ├─ GraphQL Generator ─────────────────────────────────────────────┤  │ │
│  │  │                                                                 │  │ │
│  │  │  gen/graphql/generator.go                                       │  │ │
│  │  │      │                                                          │  │ │
│  │  │      └──▶ gen.graphql    (Schema: types, inputs, queries)       │  │ │
│  │  │                                                                 │  │ │
│  │  ├─ gRPC Generator ────────────────────────────────────────────────┤  │ │
│  │  │                                                                 │  │ │
│  │  │  gen/grpc/generator.go                                          │  │ │
│  │  │      │                                                          │  │ │
│  │  │      ├──▶ service.proto  (Protocol buffer definitions)          │  │ │
│  │  │      └──▶ service.go     (gRPC service stubs)                   │  │ │
│  │  │                                                                 │  │ │
│  │  └─ Migration Generator ───────────────────────────────────────────┘  │ │
│  │                                                                       │ │
│  │     gen/migrate/generator.go                                          │ │
│  │         │                                                             │ │
│  │         ├──▶ {timestamp}_create_{entity}.up.sql                       │ │
│  │         └──▶ {timestamp}_create_{entity}.down.sql                     │ │
│  │                                                                       │ │
│  └───────────────────────────────────────────────────────────────────────┘ │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  OUTPUT STRUCTURE                                                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  generated/                                                                 │
│  ├── orm/                                                                   │
│  │   ├── client.go          # Client, Tx, entity managers                   │
│  │   ├── models.go          # User, Post, Tag structs                       │
│  │   ├── predicate.go       # Predicate type aliases                        │
│  │   ├── pagination.go      # Connection[T], Edge[T], PageInfo              │
│  │   ├── user/                                                              │
│  │   │   ├── user.go        # User entity with CRUD                         │
│  │   │   ├── query.go       # UserQuery builder                             │
│  │   │   ├── create.go      # UserCreate builder                            │
│  │   │   ├── update.go      # UserUpdate builder                            │
│  │   │   ├── delete.go      # UserDelete builder                            │
│  │   │   ├── where.go       # user.Email.EQ(), user.ID.In()                 │
│  │   │   └── order.go       # user.ByEmail(), user.ByID()                   │
│  │   ├── post/                                                              │
│  │   │   └── ...                                                            │
│  │   └── tag/                                                               │
│  │       └── ...                                                            │
│  ├── graphql/                                                               │
│  │   └── gen.graphql        # GraphQL schema                                │
│  └── grpc/                                                                  │
│      ├── service.proto      # Protobuf service definition                   │
│      └── service.go         # gRPC service implementation                   │
│                                                                             │
│  migrations/                                                                │
│  ├── 20241201000001_create_user.up.sql                                      │
│  ├── 20241201000001_create_user.down.sql                                    │
│  ├── 20241201000002_create_post.up.sql                                      │
│  └── ...                                                                    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Runtime Execution Flow

### Query Execution

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         QUERY EXECUTION FLOW                                │
└─────────────────────────────────────────────────────────────────────────────┘

  APPLICATION CODE                   GENERATED ORM                DATABASE
        │                                  │                          │
        │  client.User.                    │                          │
        │      Query().                    │                          │
        │      Where(user.Email.EQ("x")). │                          │
        │      WithPosts().                │                          │
        │      First(ctx)                  │                          │
        │─────────────────────────────────▶│                          │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ UserQuery         │                │
        │                        │ ├─ where          │                │
        │                        │ ├─ eager: [Posts] │                │
        │                        │ └─ limit: 1       │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ Interceptors      │                │
        │                        │ (from Mixins)     │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ SoftDelete        │                │
        │                        │ WHERE deleted_at  │                │
        │                        │   IS NULL         │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ TenantID          │                │
        │                        │ WHERE tenant_id   │                │
        │                        │   = ctx.tenant    │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ SQL Builder       │                │
        │                        │ dialect/sql/      │                │
        │                        │ Selector          │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                                  │  SELECT * FROM users     │
        │                                  │  WHERE email = $1        │
        │                                  │    AND deleted_at IS NULL│
        │                                  │    AND tenant_id = $2    │
        │                                  │  LIMIT 1                 │
        │                                  │─────────────────────────▶│
        │                                  │                          │
        │                                  │◀─────────────────────────│
        │                                  │  Row data                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ Eager Loading     │                │
        │                        │ Query Posts       │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                                  │  SELECT * FROM posts     │
        │                                  │  WHERE user_id = $1      │
        │                                  │─────────────────────────▶│
        │                                  │                          │
        │                                  │◀─────────────────────────│
        │◀─────────────────────────────────│                          │
        │  *User with Posts loaded         │                          │
        ▼                                  ▼                          ▼
```

### Mutation Execution (Create/Update/Delete)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        MUTATION EXECUTION FLOW                              │
└─────────────────────────────────────────────────────────────────────────────┘

  APPLICATION CODE                   GENERATED ORM                DATABASE
        │                                  │                          │
        │  client.User.                    │                          │
        │      Create().                   │                          │
        │      SetEmail("x@y.com").        │                          │
        │      SetName("John").            │                          │
        │      Save(ctx)                   │                          │
        │─────────────────────────────────▶│                          │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ UserCreate        │                │
        │                        │ ├─ email: "x@y"   │                │
        │                        │ └─ name: "John"   │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ Hooks Chain       │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ BeforeSave()      │                │
        │                        │ (if implemented)  │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ BeforeCreate()    │                │
        │                        │ (if implemented)  │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ TenantID Mixin    │                │
        │                        │ Auto-set          │                │
        │                        │ tenant_id = ctx   │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ Time Mixin        │                │
        │                        │ Auto-set          │                │
        │                        │ created_at = NOW  │                │
        │                        │ updated_at = NOW  │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ SQL Builder       │                │
        │                        │ InsertBuilder     │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                                  │  INSERT INTO users       │
        │                                  │  (email, name, tenant_id,│
        │                                  │   created_at, updated_at)│
        │                                  │  VALUES ($1,$2,$3,$4,$5) │
        │                                  │  RETURNING id            │
        │                                  │─────────────────────────▶│
        │                                  │                          │
        │                                  │◀─────────────────────────│
        │                                  │  id = 123                │
        │                        ┌─────────┴─────────┐                │
        │                        │ AfterCreate()     │                │
        │                        │ (if implemented)  │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ AfterSave()       │                │
        │                        │ (if implemented)  │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │◀─────────────────────────────────│                          │
        │  *User{ID: 123, ...}             │                          │
        ▼                                  ▼                          ▼


  HOOK EXECUTION ORDER
  ════════════════════

  CREATE:  BeforeSave → BeforeCreate → [DB INSERT] → AfterCreate → AfterSave
  UPDATE:  BeforeSave → BeforeUpdate → [DB UPDATE] → AfterUpdate → AfterSave
  DELETE:  BeforeDelete → [DB DELETE or UPDATE for soft] → AfterDelete


  SOFT DELETE FLOW (via SoftDelete mixin)
  ═══════════════════════════════════════

        │  client.User.                    │                          │
        │      DeleteOne(user).            │                          │
        │      Exec(ctx)                   │                          │
        │─────────────────────────────────▶│                          │
        │                                  │                          │
        │                        ┌─────────┴─────────┐                │
        │                        │ SoftDelete Hook   │                │
        │                        │ Converts DELETE   │                │
        │                        │ to UPDATE         │                │
        │                        └─────────┬─────────┘                │
        │                                  │                          │
        │                                  │  UPDATE users            │
        │                                  │  SET deleted_at = NOW()  │
        │                                  │  WHERE id = $1           │
        │                                  │─────────────────────────▶│
```

### Relay Cursor Pagination

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      RELAY CURSOR PAGINATION FLOW                           │
└─────────────────────────────────────────────────────────────────────────────┘

  GraphQL Query                     Resolver                      ORM Query
        │                              │                              │
        │  query {                     │                              │
        │    users(first: 10,          │                              │
        │           after: "abc123") { │                              │
        │      edges {                 │                              │
        │        node { id, name }     │                              │
        │        cursor                │                              │
        │      }                       │                              │
        │      pageInfo {              │                              │
        │        hasNextPage           │                              │
        │        endCursor             │                              │
        │      }                       │                              │
        │    }                         │                              │
        │  }                           │                              │
        │─────────────────────────────▶│                              │
        │                              │                              │
        │                    ┌─────────┴─────────┐                    │
        │                    │ DecodeCursor      │                    │
        │                    │ "abc123" → id=42  │                    │
        │                    └─────────┬─────────┘                    │
        │                              │                              │
        │                              │  client.User.Query().        │
        │                              │      Where(user.ID.GT(42)).  │
        │                              │      Order(user.ByID()).     │
        │                              │      Limit(11).              │
        │                              │      All(ctx)                │
        │                              │─────────────────────────────▶│
        │                              │                              │
        │                              │◀─────────────────────────────│
        │                              │  [11 users]                  │
        │                              │                              │
        │                    ┌─────────┴─────────┐                    │
        │                    │ Build Connection  │                    │
        │                    │ ├─ Take first 10  │                    │
        │                    │ ├─ hasNextPage:   │                    │
        │                    │ │   len == 11     │                    │
        │                    │ ├─ Build edges    │                    │
        │                    │ └─ Encode cursors │                    │
        │                    └─────────┬─────────┘                    │
        │                              │                              │
        │◀─────────────────────────────│                              │
        │  {                           │                              │
        │    edges: [                  │                              │
        │      { node: {...},          │                              │
        │        cursor: "def456" }    │                              │
        │    ],                        │                              │
        │    pageInfo: {               │                              │
        │      hasNextPage: true,      │                              │
        │      endCursor: "xyz789"     │                              │
        │    }                         │                              │
        │  }                           │                              │
        ▼                              ▼                              ▼


  CURSOR ENCODING (velox/pagination.go)
  ═════════════════════════════════════

  EncodeCursor(id int64) string
      │
      ├──▶ Convert to bytes
      ├──▶ Base64 URL encode
      └──▶ Return cursor string

  DecodeCursor(s string) (int64, error)
      │
      ├──▶ Base64 URL decode
      ├──▶ Parse as int64
      └──▶ Return ID or error
```

---

## Component Reference

### Core Interfaces (velox.go)

| Interface | Purpose | Key Methods |
|-----------|---------|-------------|
| `Interface` | Schema contract | `Fields()`, `Edges()`, `Indexes()`, `Mixin()`, `Hooks()` |
| `Schema` | Embeddable default | Provides default nil implementations |
| `View` | Read-only schema | Restricts mutations |
| `Field` | Field definition | `Descriptor() *FieldDescriptor` |
| `Edge` | Relationship definition | `Descriptor() *EdgeDescriptor` |
| `Mixin` | Reusable components | Fields, Edges, Hooks, Interceptors |
| `Hook` | Mutation middleware | `func(Mutator) Mutator` |
| `Interceptor` | Query middleware | `Intercept(Querier) Querier` |
| `Traverser` | Graph traversal | `Traverse(context.Context, Query) error` |
| `Policy` | Privacy rules | `EvalMutation()`, `EvalQuery()` |

### Field Types

| Function | Go Type | DB Type (PostgreSQL) |
|----------|---------|---------------------|
| `field.String()` | `string` | `VARCHAR(255)` |
| `field.Text()` | `string` | `TEXT` |
| `field.Int64()` | `int64` | `BIGINT` |
| `field.Float64()` | `float64` | `DOUBLE PRECISION` |
| `field.Bool()` | `bool` | `BOOLEAN` |
| `field.Time()` | `time.Time` | `TIMESTAMP` |
| `field.UUID(name, typ)` | `uuid.UUID` | `UUID` |
| `field.JSON()` | `any` | `JSONB` |
| `field.Enum()` | Generated type | `VARCHAR` or native ENUM |
| `field.Bytes()` | `[]byte` | `BYTEA` |
| `field.Decimal()` | `decimal.Decimal` | `DECIMAL(p,s)` |

### Built-in Mixins

| Mixin | Fields Added | Features |
|-------|--------------|----------|
| `mixin.ID{}` | `id int64` | Auto-detected as PK by name |
| `mixin.Time{}` | `created_at`, `updated_at` | Auto timestamps |
| `mixin.SoftDelete{}` | `deleted_at` | Soft delete + interceptor |
| `mixin.TimeSoftDelete{}` | All time fields + soft delete | Combined |
| `mixin.TenantID{}` | `tenant_id int64` | Multi-tenancy isolation |
| `mixin.TenantUUID{}` | `tenant_id UUID` | UUID-based multi-tenancy |

---

## Package Dependency Graph

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PACKAGE DEPENDENCY GRAPH                            │
└─────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────┐
                              │   velox.go   │
                              │  (core types)│
                              └──────┬───────┘
                                     │
           ┌─────────────────────────┼─────────────────────────┐
           │                         │                         │
           ▼                         ▼                         ▼
    ┌─────────────┐          ┌─────────────┐          ┌─────────────┐
    │ schema/     │          │  graph/     │          │  config/    │
    │ ├─ field/   │          │  (internal  │          │  (velox.yaml│
    │ ├─ edge/    │─────────▶│   repr)     │◀─────────│   parsing)  │
    │ ├─ mixin/   │          │             │          │             │
    │ ├─ index/   │          └──────┬──────┘          └─────────────┘
    │ └─ annotation                │
    └─────────────┘                │
                                   │
           ┌───────────────────────┼───────────────────────┐
           │                       │                       │
           ▼                       ▼                       ▼
    ┌─────────────┐          ┌─────────────┐         ┌─────────────┐
    │  gen/orm/   │          │gen/graphql/ │         │  gen/grpc/  │
    │  (ORM code) │          │(GQL schema) │         │(proto/stub) │
    └──────┬──────┘          └─────────────┘         └─────────────┘
           │
           ▼
    ┌─────────────┐          ┌─────────────┐
    │ dialect/    │          │gen/migrate/ │
    │ ├─ dialect/ │          │(SQL files)  │
    │ └─ sql/     │          └─────────────┘
    │   (builders)│
    └─────────────┘


                         RUNTIME DEPENDENCIES
                         ════════════════════

    ┌─────────────────────────────────────────────────────────────┐
    │  generated/orm/                                              │
    │  ├─ imports velox (hooks, pagination, errors)               │
    │  ├─ imports dialect/sql (query builders)                    │
    │  └─ imports database/sql (DB driver)                        │
    └─────────────────────────────────────────────────────────────┘
```

---

## CLI Commands

| Command | Description | Example |
|---------|-------------|---------|
| `velox init` | Initialize new project | `velox init` |
| `velox generate` | Generate all targets | `velox generate` |
| `velox generate -t orm` | Generate ORM only | `velox generate -t orm` |
| `velox generate -t graphql` | Generate GraphQL only | `velox generate -t graphql` |
| `velox generate -t grpc` | Generate gRPC only | `velox generate -t grpc` |
| `velox generate -t migrations` | Generate migrations only | `velox generate -t migrations` |
| `velox status` | Show schema status | `velox status` |

---

## Configuration Reference

```yaml
# velox.yaml
version: 1

schema:
  path: ./schema           # Schema definition directory
  package: schema          # Package name

output:
  orm:
    path: ./generated/orm
    package: orm
  graphql:
    path: ./generated/graphql
    package: graphql
  grpc:
    path: ./generated/grpc
    package: grpc
  migrations:
    path: ./migrations

database:
  dialect: postgres        # postgres | mysql | sqlite

features:
  softDelete: true         # Enable soft delete support
  timestamps: true         # Enable created_at/updated_at
  hooks: true              # Enable lifecycle hooks

graphql:
  relay: true              # Relay cursor connections
  offsetPagination: false  # Alternative pagination
  filters: true            # WhereInput types
  mutations: true          # CRUD mutations
  inputs: true             # CreateInput/UpdateInput
  ordering: true           # OrderBy types
  nodeInterface: true      # Node interface for Relay

codegen:
  features:
    - columns              # Generic typed columns
    - filtering            # EQ, NEQ, In, NotIn, IsNil, NotNil
    - comparison           # GT, GTE, LT, LTE
    - strings              # Contains, HasPrefix, HasSuffix
    - edges                # Has<Edge>, Has<Edge>With
    - where                # WhereInput filter types
    - order                # OrderField constants
    - input                # CreateInput/UpdateInput
    - nillable             # SetNillable<Field> methods
    - clear                # Clear<Field> methods
    - subpkg               # Per-entity subdirectories

performance:
  parallel: true           # Parallel generation
  workers: 4               # Number of workers
  incremental: true        # Cache-based regeneration
```

---

## Best Practices Summary

### Schema Design
1. Always define a primary key (field named "id" is auto-detected)
2. Use snake_case for database column names
3. Apply appropriate mixins for common patterns
4. Define relationships explicitly with `edge.To()` and `edge.From()`

### Code Generation
5. Run `velox generate` after schema changes
6. Use `-t` flag for faster targeted generation
7. Commit generated code to version control

### Querying
8. Use generated predicates for type safety
9. Leverage eager loading with `With<Edge>()` methods
10. Use cursor pagination for large datasets

### Mutations
11. Implement hooks for cross-cutting concerns
12. Use transactions for multi-entity operations
13. Validate input with struct tags

---

*Generated for Velox ORM Framework - A++ Grade Developer Documentation*
