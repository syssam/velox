// Package velox is an ORM for Go 1.27 and later. Entities are described in a
// schema package, in the DSL of the schema packages (schema, schema/field,
// schema/edge, ...). velox-gen generates from it, into a single gen package,
// the entity structs, their typed column handles and their metadata (Desc),
// which velox reads on an entity's first use.
//
// Add velox-gen to your module once, and run it after every change to the
// schema. It writes ./gen; with -check it writes nothing and fails when ./gen
// is out of date:
//
//	go get -tool github.com/syssam/velox2/cmd/velox-gen
//	go tool velox-gen ./schema
//	go tool velox-gen -check ./schema
//
// Queries and writes use the generated entities and handles:
//
//	users, err := db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).Limit(10).All(ctx)
//
// Policies and hooks are not in the schema. You write them as methods on the
// generated entity, in a file of your own in ./gen, and velox-gen binds them:
//
//	func (*User) Policy() velox.Policy[User] {
//		return velox.Policy[User]{
//			Read: func(ctx context.Context) (velox.Pred[User], error) {
//				return Users.DeletedAt.IsNull(), nil
//			},
//		}
//	}
//
// A method with a wrong signature does not compile. Hooks are a method
// Hooks() []velox.Hook[User] in the same way.
//
// A DB can route its statements. WithReplicas sends reads to replicas in turn;
// WithSession keeps a request reading its own writes from the primary for a
// short window after a write; a replica that fails a read falls over to the
// primary. OpenShards runs each statement on one shard, chosen from the
// context's tenant (WithShardFor) or OnShard. See example/routing.
package velox

//go:generate go run ./internal/handlegen/gen

// GeneratedCodeIsVersion3 is referenced by every file velox-gen writes from a
// schema package, so that generated code and this library fail to compile
// together when they disagree. It is replaced by GeneratedCodeIsVersion4 when
// generated code must change. (Version 3: Desc literals and switch-based Addr
// and RelAddr, generated from a schema package.)
const GeneratedCodeIsVersion3 = true

// Lazy is the constraint of the type parameter that every method of a
// generic velox type carries, and that callers never write. A generic method
// is compiled only where it is called; an ordinary method of a generic type is
// compiled into every package that uses the type.
type Lazy[T any] interface{ *T }
