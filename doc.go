// Package velox is an ORM for Go 1.27 and later. Entities are described in a
// schema package, in the DSL of the schema packages (schema, schema/field,
// schema/edge, ...). velox-gen generates from it, into a single gen package,
// the entity structs, their typed column handles and their metadata (Descriptor),
// which velox reads on an entity's first use. The DSL follows ent's; the
// repository's docs/coming-from-ent.md lists where velox differs, Optional
// staying NOT NULL among them.
//
// Add velox-gen to your module once, and run it after every change to the
// schema. It writes ./gen; with -check it writes nothing and fails when ./gen
// is out of date:
//
//	go get -tool github.com/syssam/velox/cmd/velox-gen
//	go tool velox-gen ./schema
//	go tool velox-gen -check ./schema
//
// Connect opens a database, through a database/sql driver the application
// imports. Queries and writes use the generated entities and handles:
//
//	db, err := velox.Connect("sqlite", "app.db")
//	if err != nil {
//		return err
//	}
//	defer db.Close()
//
//	users, err := db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).Limit(10).All(ctx)
//
// Policies and hooks are declared in the schema, as ent's are, typed with the
// generated entity and handles. The schema imports them from gen/entity,
// which velox-gen writes when the schema declares behaviour:
//
//	func (User) Policy() velox.Policy[entity.User] {
//		return velox.Policy[entity.User]{
//			Read: func(ctx context.Context) (velox.Pred[entity.User], error) {
//				return entity.Users.DeletedAt.IsNull(), nil
//			},
//		}
//	}
//
// Hooks are a method Hooks() []velox.Hook[entity.User] in the same way.
// Package gen imports gen/entity and the schema, binds the methods when the
// program starts, and re-exports every name of gen/entity: gen.User is
// entity.User. A program imports gen, as without behaviour, and that links
// the schema. A program that imports only gen/entity fails at its first use
// of an entity, rather than run without the policies. Everything in ./gen
// is generated.
//
// # Reads
//
// Query.All returns the rows, One exactly one (ErrNotFound for none,
// ErrNotSingular for several) and First the first in the query's order,
// without One's check for a second. A query that reads rows needs a Limit,
// or fails with ErrUnbounded, unless it says Unbounded. Count and Exists ask
// the database instead of loading rows, and need no Limit; Exists is a
// SELECT 1 … LIMIT 1.
//
// Sum, Avg, Min and Max take a query and a number column:
//
//	paid, err := velox.Sum(ctx, db.Query[gen.Invoice]().Where(gen.Invoices.Status.EQ(gen.InvoiceStatusPaid)), gen.Invoices.TotalCents)
//
// The policy, a scope included, applies to them as to Count. Sum is 0 when
// no row matches; Avg, Min and Max also return false then, as there is no
// value to give. A nullable number column (a NullNumberCol, for a Nillable
// field) takes SumNull, AvgNull, MinNull and MaxNull, which skip NULLs and
// return the same.
//
// CountBy, SumBy, AvgBy, MinBy and MaxBy do the same per value of a key column,
// made by the column handle's Key method, in the order of the key:
//
//	totals, err := velox.SumBy(ctx, db.Query[gen.Invoice]().Unbounded(), gen.Invoices.OrgID.Key(), gen.Invoices.TotalCents)
//
// Each returns a []Group[K, V]; SumByNull, AvgByNull, MinByNull and MaxByNull
// take a NullNumberCol. The groups can be as many as the rows, so the
// query says Unbounded, and WithMaxRows caps them as it caps a read's rows.
//
// Rel.Of is a condition on the rows related to given owners, which loading
// the relation for them would load:
//
//	db.Query[gen.Post]().Where(gen.Users.Posts.Of(&ada)).All(ctx)
//
// ForUpdate and ForShare lock the rows a query reads, until the transaction
// ends, and SkipLocked and NoWait change what the query does with a row that
// another transaction holds. A locking read runs inside Tx only, on
// PostgreSQL and MySQL. On MySQL every row the read scans is locked, so the
// filter wants an index.
//
// # Per-owner pages
//
// Rel.LimitEach loads at most n related entities for each owner, the first in
// the relation's order, in one statement for all of them:
//
//	db.Query[gen.User]().With(gen.Users.Posts.OrderBy(gen.Posts.CreatedAt.Desc()).LimitEach(3)).Limit(20).All(ctx)
//
// The statement ranks each owner's rows with ROW_NUMBER() OVER (PARTITION BY
// …), on PostgreSQL, MySQL 8 and SQLite 3.25 or later; ties break by the
// target's primary key. The Read policy, a scope included, applies inside the
// ranking, so a row they hide takes no place in a page. A many-to-many page
// joins the join table in the same statement. Such a load is bounded by its
// limit, so neither the owner query's MaxRows nor the DB's cap applies to it;
// Rel.MaxRows gives any load a cap of its own.
//
// LoadEach and CountEach read the same pages, and count them, for owners
// already in hand, with a Query of the target that gives the conditions,
// the order, the columns and After:
//
//	pages, err := velox.LoadEach(ctx, db.Query[gen.Post]().OrderBy(gen.Posts.CreatedAt.Desc()), gen.Users.Posts.LimitEach(3), users)
//	counts, err := velox.CountEach(ctx, db.Query[gen.Post](), gen.Users.Posts, users)
//
// A many-to-many page or count joins the join table in the target's
// statement: no statement is sent for the join entity, and a link held twice
// counts once. A per-owner page names its rank vx_rn and, through a join, the
// owner's key vx_owner, so a target column cannot have either name.
//
// # Writes
//
// DB.Insert and DB.InsertAll insert entities: Exec fills their defaults and
// runs their hooks and policy. Insert reads the generated key back; InsertAll reads nothing
// back, so give its entities their keys when later writes need them. OnConflict, followed by
// Update or DoNothing, makes an insert an upsert. DB.Update and DB.Delete
// change the rows their conditions match, and Exec returns how many; one
// with no Where fails with ErrNoWhere, unless it says All. DB.Save writes
// the named columns of an entity to the row with its primary key, and fails
// with ErrNotFound when there is none:
//
//	err := db.Insert(&inv).Exec(ctx)
//	n, err := db.Update[gen.Invoice]().Set(gen.Invoices.Status.Set(gen.InvoiceStatusPaid)).Where(gen.Invoices.ID.EQ(id)).Exec(ctx)
//	err = db.Save(&inv, gen.Invoices.Number, gen.Invoices.TotalCents).Exec(ctx)
//
// A self-referencing edge with no inverse is bidirectional, as in ent. A
// link writes both directions: both join rows of a many-to-many edge, or
// both rows' keys of a one-to-one edge. An unlink removes both. Inserting a
// join row fails when either row is there already;
// OnConflict(...).DoNothing() is the idempotent insert.
//
// The entity's policy applies to writes as to reads. Confining rows to a
// value the context carries, such as an organization, is a policy and a hook
// of the entity: see package scope.
//
// # JSON fields
//
// velox-gen gives every json field's ColumnDescriptor a codec, encoding/json's
// Encode and Decode, and its ColumnInfo says Codec. The value is bound as the
// bytes Encode returns, and only there: hooks and policies see the Go value,
// and only Statement.Args holds the bytes. On SQLite and MySQL bytes that
// are valid UTF-8 are bound as a string: SQLite stores a []byte as a BLOB,
// not TEXT, and MySQL's driver with interpolateParams=true writes a []byte
// as a binary string, which a JSON column refuses. Bytes that are not valid
// UTF-8, which only a binary codec returns, stay []byte.
//
// In a nullable column (a pointer field, or a slice or map field whose
// ColumnDescriptor says Nullable), a nil pointer, or a nil slice or map, binds
// NULL; in any other column a nil slice or map is encoded (the JSON null,
// which reads back as nil). An untyped nil, which only a hook-built value can
// be, binds NULL in either. What is read, as []byte or string, is decoded
// with Decode; NULL leaves the field at its zero value (nil, for a pointer).
//
// A condition other than IsNull and NotNull on an encoded column, and
// ordering by it, are refused when the statement runs, as equality and order
// of encoded values differ by dialect; JSONCol and NullJSONCol have no such
// methods, so generated code refuses them at compile time. A primary-key
// or relation-key column cannot be encoded: the metadata is refused.
// Codec says nothing of the database type, which the migration planner takes
// from the schema's field type.
//
// # Hooks
//
// A Hook wraps an entity's writes and receives each as a Mutation. For an
// update or a delete, Conds returns the conditions the caller and the
// earlier hooks gave, and not the policy's; it is empty when
// the statement said All. ValueIn, a method of each column handle, returns
// the value an update assigns to the column, and whether it assigns one.
// Affected returns the rows the write changed once next has returned. A hook
// that makes an update or delete itself, such as a soft delete, reports its
// count with SetAffected. Mutation shows the pattern.
//
// # Routing
//
// A DB can route its statements. WithReplicas sends reads to replicas in turn;
// WithSession keeps a request reading its own writes from the primary for a
// short window after a write; a replica that fails a read falls over to the
// primary. OpenShards runs each statement on one shard, chosen from the
// context (WithShardFor) or WithShard. See examples/routing.
//
// # Raw SQL
//
// Raw SQL is the escape hatch for CTEs, a GROUP BY of several keys and anything else the
// builder cannot express: DB.Exec runs a statement, and DB.Raw scans a
// query's rows into an entity by column name. velox applies no
// policy to raw SQL, a scope included, so the caller confines the rows, and
// both require a reason, which hooks see as Statement.PolicyBypass.
//
// # SQLite
//
// SQLite has no time type: a time is text, and a comparison compares the text.
// velox binds every time in one form, UTC with nine fractional digits
// (2026-11-01 12:00:00.000000000+00:00), so that the text sorts by the instant,
// and mixin.Time's database default writes that form. So must every other
// writer: a SchemaDefault of your own, raw SQL, another program. Their
// expression is strftime('%Y-%m-%d %H:%M:%f000000+00:00','now'). Text in another
// form compares wrongly: CURRENT_TIMESTAMP's 2026-11-01 12:00:00 sorts below the
// parameter of the same instant, so that After's pages repeat. A migration that
// changes a default leaves the rows' text as it is; rewrite those rows. Reading
// is not so strict: a time field scans every text form of SQLite's date
// functions, the result of MAX(created_at) included.
//
// Connect opens one connection, as SQLite has one writer. A loop over Iter holds
// it, so a statement in the loop's body fails: see Query.Iter.
//
// # Options
//
// Open's options are WithX, as WithMaxRows; a transaction's are TxIsolation,
// TxReadOnly, TxTimeout and TxRetrySerializable; a context is changed by WithX
// or WithoutX, as WithShard and WithoutPolicy, and read by XFromContext, as
// RowScopeFromContext.
//
// An Option configures a DB, once, when it is opened (Connect, Open, OpenConn, veloxpgx.Open, OpenShards).
// What a DB checks and bounds:
//
//   - WithMaxLimit and WithMaxRows cap a query's Limit and the rows it reads.
//   - WithDeadlines sets the time limits of statements and transactions.
//
// What runs around its statements:
//
//   - WithRewrite, WithAround and WithTxAround add hooks that see every
//     statement, or every transaction.
//   - WithCallers, WithCallerSkip and WithSQLComment annotate the SQL.
//
// Where its statements go:
//
//   - WithReplicas, WithPinWindow and WithReplicaBackoff set the replicas.
//   - WithShardFor chooses a shard from the context.
//
// # Context decorators
//
// A context decorator returns a context that changes what the statements
// run under it do. None of them changes the DB:
//
//   - WithScopeValue names a value a statement is scoped by, as a scope key's
//     With does (package scope).
//   - WithoutPolicy lifts the entities' policies, a scope's among them.
//   - WithShard, WithPrimaryReads and WithSession route a statement.
//   - PinUntil restarts a session's pin from a time stored earlier, as
//     PinnedUntil gives it; it changes the session, not the context.
//   - WithTags adds tags to the SQL comment.
//   - WithoutTx removes a transaction's mark from a context.
//
// RowScopeFromContext reads back what a context scopes statements by (the
// scope values, the reason of WithoutPolicy, the shard) as a RowScope, which
// compares with ==: code that reads for several callers in one statement
// keeps callers of different scopes apart by it.
//
// # Transaction options
//
// A TxOption configures one call of DB.Tx: TxIsolation, TxReadOnly,
// TxRetrySerializable and TxTimeout.
//
// # Errors
//
// Errors are returned wrapped, in an error whose text starts
// "velox: <Entity>: " (raw SQL's DB.Exec names its statement "Exec"), or for
// a transaction "velox: transaction: " ("velox: begin: ", "velox: commit: "
// and "velox: savepoint: " likewise). Test for a row not found with
// IsNotFound: errors.Is(err, ErrNotFound) that a driver's typed-nil error in
// err's tree cannot make panic. Test for the other sentinels (ErrDenied, ...)
// with errors.Is, for a violated constraint (ConstraintUnique,
// ConstraintForeignKey, ConstraintNotNull and ConstraintCheck) with errors.As
// to a *ConstraintError, and for a refused value with errors.As to a
// *ValidationError. IsNotFound is the only such helper: where ent has
// IsConstraintError and IsValidationError, velox has errors.As on its typed
// errors, and helpers for them would be an addition. DriverCode returns
// the database's own code for an error that came from it, such as
// PostgreSQL's SQLSTATE, for logs and traces; it is not for an end user.
//
// # Generated code
//
// velox-gen and its plugins write code that uses part of this package, and
// nothing else should: Descriptor, ColumnDescriptor, EdgeDescriptor, EdgeTo, EdgeBidi,
// EdgeThrough, Meta, MetaOnce, NotLinkedError, Show, BindPolicy, BindHooks,
// BindHooksAfter, BoundPolicy, BoundHooks, Lazy, Validate, ValidateSet, the untyped surface (Table, TableOf,
// TableRows, RowsOf, PredOp and its operators PredEQ, PredNEQ, PredIn,
// PredNotIn, PredGT, PredGTE, PredLT, PredLTE, PredContains, PredHasPrefix,
// PredHasSuffix, PredIsNull and PredNotNull, TableQuery, DB.QueryTable,
// Untyped, Typed, AndPredicates, OrPredicates, NotPredicate, TableInsert,
// TableUpdate, TableDelete, DB.InsertTable, DB.InsertRows, DB.UpdateTable,
// DB.DeleteTable and Assignments: an entity's queries and writes with its
// type erased, for runtimes compiled once for every entity) and
// GeneratedCodeIsVersion10. Each
// says "For generated code." in its documentation. They are not part of
// velox's API: they change only when GeneratedCodeIsVersion10 is replaced by
// the next version, which is outside semantic versioning. Run velox-gen
// again after you update velox; code generated for another version fails to
// compile.
package velox

//go:generate go run ./internal/cmd/handlegen

// GeneratedCodeIsVersion10 is referenced by every file velox-gen writes from a
// schema package, so that generated code and this library fail to compile
// together when they disagree. For generated code. It is replaced by
// GeneratedCodeIsVersion11 when generated code must change.
const GeneratedCodeIsVersion10 = true

// Lazy is the constraint of the type parameter that every method of a generic
// velox type carries, and that callers never write. For generated code. A
// generic method is compiled only where it is called; an ordinary method of a
// generic type is compiled into every package that uses the type.
type Lazy[T any] interface{ *T }
