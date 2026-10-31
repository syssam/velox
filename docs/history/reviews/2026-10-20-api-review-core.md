# velox2 core (root package) — API review before 1.0

### Summary verdict

The core is unusually well thought out. It has one handle type (`*DB`, including inside a transaction), builders with value semantics, guards that fail closed, `iter.Seq2` streaming, `SQL(ctx)` dry runs, a consistent error model, and generic methods used carefully to keep compile cost down. Six things need deciding before 1.0, because changing them later breaks callers or quietly changes security:

- the `Tx` callback gets no context;
- a zero `Pred` means "no restriction" in a policy (fails open) but is an error in `Where` (fails closed);
- writes made inside a dry run are actually sent;
- there is no way to run raw SQL;
- generated-code types sit in the public API with no compatibility statement;
- a few names collide or read badly.

The remaining gaps (First/Exists, querying from a loaded entity's edge, locking reads, aggregates) are additions that won't break anything.

Severity: **Must** means it breaks callers, or is about security or correctness, and can't change after 1.0. **Should** means it is wanted for 1.0. **Could** means it can come later.

### Findings

**1. Must · correct/modern — `Tx` gives `fn` no context.**
- Evidence: `tx.go:59`, `fn func(tx *DB) error`. Every example closes over the outer `ctx` (`example/tx/main.go:72`: `db.Tx(ctx, func(tx *velox.DB) error { return transfer(ctx, tx, …) })`).
- What goes wrong: the bounded context, and the context that `TxAround` hooks decorate (`TxInfo.Context`), never reach user code. contrib/otel has to work around this (followups, "Left by observability"). The classic mistake of calling `db` instead of `tx` inside `fn` goes undetected.
- I disagree with the followups leaving this unprioritised.
- Proposal:
  ```go
  func (db *DB) Tx(ctx context.Context, fn func(ctx context.Context, tx *DB) error, opts ...TxOption) error
  ```
  Mark that ctx as belonging to the transaction. A statement on a non-transaction `*DB` given a ctx marked this way then fails with `ErrOutsideTx`, a guard in velox's usual fail-closed style.
- Breaking: yes.

**2. Must · correct (security) — zero `Pred` fails open in policies but fails closed in queries.**
- Evidence: `scope.go:357` (`case p.n.op == 0: return p, nil // no restriction`) versus `guard.go:9-12`, where `Where(Pred[E]{})` is refused.
- What goes wrong: a policy written as `var p velox.Pred[Doc]; if role == Viewer { p = … }; return p, nil` silently lets every other role through. The layer that enforces security is the only one that fails open.
- Proposal: add `func Allow[E any]() Pred[E]`, an explicit "no restriction". A zero `Pred` returned from `Read` or `Write` then becomes an error. Docs and examples switch to `return velox.Allow[Doc](), nil`.
- Decide in the same change whether `Read`, `Write` and `Insert` get the running `*DB`. The followups need it for inherited insert checks, and adding a parameter to these function fields after 1.0 breaks every policy.
- Breaking: yes (semantics and docs).

**3. Must · correct — writes made inside `SQL(ctx)` are sent.**
- Evidence: `IsDryRun` (`scope_api.go:64`) is only a flag. Nothing checks it before execution (the only uses are `dryRunContext` in `insert.go:151`, `query.go:500` and `write.go:98,163`).
- What goes wrong: a hook that writes through `m.DB().Insert(&audit).Exec(ctx)` really writes during a dry run unless its author remembers `if velox.IsDryRun(ctx)` (`example/hooks/gen/task_hooks.go`).
- Proposal: an `Exec`, `Save` or `Tx` whose ctx is a dry run renders the statement and returns without sending it, or fails with `ErrDryRun`. Reads still run, since a policy may look things up.
- Also document on `InsertQuery.SQL` that it changes the caller's structs (it fills defaults and the tenant).
- Breaking: behaviour only.

**4. Must · complete — no raw SQL escape hatch.**
- Evidence: the `*DB` methods are `Query`, `Insert`, `InsertAll`, `Update`, `Delete`, `Save`, `Tx` and `Dialect`. Inside `Tx` the `*sql.Tx` or `pgx.Tx` is unreachable.
- What goes wrong: `FOR UPDATE`, aggregates, CTEs, advisory locks and `RETURNING` on update can't be done at all inside a transaction. Every gap below has no workaround until this exists.
- Proposal:
  ```go
  func (db *DB) Exec(ctx context.Context, reason, query string, args ...any) (int64, error)
  func (db *DB) Raw[E any, PE Entity[E]](reason, query string, args ...any) RawQuery[E] // All/One/Iter, scanned by column name
  ```
  - Raw statements go through the Around hooks (`OpRaw`), deadlines and routing (`Exec` to the primary; `Raw` to replicas unless `Primary`).
  - They skip tenant scoping and policy. So, as `AllTenants` and `SkipPolicy` already do, they require a `reason`, which hooks see as `Statement.RawReason`.
- Breaking: no.

**5. Should · clean/maintain — generated-only types are in the public API without a compatibility promise.**
- Evidence: 12 exported names exist only for velox-gen: `Desc`, `ColumnDesc`, `Relation`, `EdgeTo`, `EdgeThrough`, `Meta`, `MetaOnce`, `BindPolicy`, `BindHooks`, `BoundPolicy`, `BoundHooks`, `GeneratedCodeIsVersion3`. They are about 18% of the types in godoc.
- Other problems:
  - `Desc.Tenant` uses -1 for "none", so a zero `Desc` makes column 0 the tenant (`desc.go`).
  - Nine godoc passages cite internal specs ("design §3 rule 7", "routing spec §5"), which mean nothing on pkg.go.dev.
- Proposal, minimum: a package-doc section, "Generated-code API: Desc… change only together with GeneratedCodeIsVersionN and are outside semver". Every one of those docs starts "For generated code." Remove the spec citations from exported docs.
- Proposal, better: move these definitions into `internal/meta` and expose generic aliases from `velox2/veloximpl`, as `protoimpl` does. Do this only if the alias layer costs nothing measurable at compile time.
- Keep `CheckEntity`, `CheckRelation`, `GoType`, `Lazy` (contrib/graphql uses it) and the hook-facing types (`Statement`, `EntityInfo`, `Predicate`…) in the root package.
- Breaking: generated code only (bump to v4).

**6. Should · clean — names that collide or read badly.**
- `ConstraintKind` constants `Unique`, `ForeignKey`, `NotNull`, `Check` (`errors.go:78-83`) have no prefix, while `Op*`, `Assign*` and `Tx*` do. `velox.NotNull` sits next to the handle method `.NotNull()` and the field `Desc.Check`. Rename to `ConstraintUnique` and so on.
- The transaction options `Timeout`, `ReadOnly` and `Isolation` read as package-wide settings. Rename to `TxTimeout`, `TxReadOnly` and `TxIsolation`.
- `Primary(ctx)` reads like the field `Shard.Primary`. Rename to `ReadPrimary(ctx)`.
- `With*` names both DB options (`WithReplicas`) and context decorators (`WithTenant`, `WithSession`, `WithTags`), while other decorators have bare names (`AllTenants`, `SkipPolicy`, `OnShard`). At least group them in the godoc: "Context: …", "Options: …".
- Breaking: yes.

**7. Should · maintain — two ways to name a connection pool, and the pgx split will force a reshape anyway.**
- Evidence: `Open(d, *sql.DB)` and `OpenPgx(pool)` (`db.go:121,128`), alongside `Conn`, `SQLConn`, `PgxConn` and `Shard{Primary Conn}` (`route.go:108`).
- Proposal: `Open(d Dialect, primary Conn, opts ...Option)` with `velox.SQLConn(db)` in the core and `veloxpgx.Conn(pool)` in a subpackage, so the core stops linking pgx. Do it in the same change as the pgx split.
- Breaking: yes.

**8. Should · clean/complete — `Save` is the only write that isn't a builder.**
- Evidence: `write.go:185` runs `Save` immediately. It has no `SQL(ctx)`, no `Where`, and naming no columns is a runtime error (`write.go:194`).
- Proposal:
  ```go
  db.Save(&x, U.Email /*, more...*/).Where(U.Version.EQ(v)).Exec(ctx) // + SQL(ctx)
  func (db *DB) Save[E any, PE Entity[E]](x PE, col Column[E], more ...Column[E]) SaveQuery[E]
  ```
  This gives optimistic locking with no new concept, and turns "no columns" into a compile error.
- Breaking: yes.

**9. Should · complete — common reads.**
- Proposal:
  - `First(ctx) (*E, error)`: `Limit(1)`, returns `ErrNotFound` when nothing matches.
  - `Exists(ctx) (bool, error)`: `SELECT 1 … LIMIT 1`. Today it has to be `Count`.
  - `func (r Rel[E,T]) Of[_ Lazy[T]](owners ...*E) Pred[T]`, to query from a loaded entity's edge: `db.Query[gen.Post]().Where(A.Posts.Of(&ada)).Limit(20).All(ctx)`. It uses the target's policy and handles the many-to-many join, as `Has` does.
- Breaking: no.

**10. Should · complete — locking reads.**
- Proposal: `Query.ForUpdate()`, `.ForShare()`, `.SkipLocked()` and `.NoWait()` set `Statement.Lock`. They are refused outside a `Tx`, on SQLite, and combined with `With`.
- This can slip to 1.1 if #4 lands.
- Breaking: no.

**11. Should · complete — aggregates.**
- The followups list these; I rate them needed for 1.0. Example `tour` totals revenue in Go over `Iter`.
- Proposal:
  ```go
  func Sum[E any, V number](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, error) // + Avg, Min, Max
  ```
  These follow `Count`'s rules (policy and tenant applied, ordering ignored).
- `GroupBy` into a user struct is nice-to-have; for 1.0 it can go through `Raw`.
- Breaking: no.

**12. Should · complete — entity hooks can't do soft delete or ID-based audit.**
- Evidence: `Mutation` exposes `Op`, `Rows`, `DB`, `Result`, `Value`, `Set` and `Where` (`scope_api.go:169-251`). A hook can't read the statement's conditions, so it can't turn a delete into `UPDATE … SET deleted_at` on the same rows.
- Proposal: `m.Conds() []Pred[E]` (read-only copies). Also a typed value read on the handle, `Tasks.Title.ValueIn(m) (string, bool)`, which replaces the `any` returned by `Mutation.Value` (that part is already in the followups).
- Breaking: no.

**13. Could · clean/docs — smaller issues.**
- The `DB` doc never says it is safe for concurrent use.
- The `Tx` doc says a panic "rolls it back and carries on". Say "rolls back and re-panics".
- A nested `Tx` silently ignores `RetrySerializable` and `Timeout`. Return an error instead.
- Godoc prints `type Mutation[E any] mutation` and `Option func(*config)`, showing unexported names.
- `Mutation.Result().Rows` is always 0. Replace it with `m.Affected() int64`.
- The sentinel docs read "ErrNotFound: One found…". Go's convention is "ErrNotFound is returned by One when…".
- `ColumnInfo.Codec`'s doc is 25 lines about implementation. Move that to the package doc.
- Breaking: mostly no.

### Keep as is
- `*DB` as the only handle, transactions included; builders with value semantics; `ctx` passed to the final call.
- Fail-closed guards: `ErrUnbounded` with `Unbounded()`, `ErrNoWhere` with `All()`, and the required reasons for `AllTenants` and `SkipPolicy`.
- The family of typed handles (EQ/NEQ/GT…/In/NotIn/IsNull/NotNull, matching ent), the `Lazy` generic-method convention, and the compile-cost rules behind them.
- `One` and `Many` with `Get() (…, bool)`, and no lazy loading.
- `Iter` returning `iter.Seq2[E, error]` and refusing `With`.
- `SQL(ctx)` on every builder (once #3 is fixed).
- `After` keyset pagination.
- The error model: sentinels are bare phrases, wrapped as `velox: <Entity>: …`; typed errors are `*XError` with `Unwrap`; `ErrTimeout` also matches `DeadlineExceeded`; `PolicyError.Is(ErrDenied)`.
- No whole-struct `Save` and no `Find(id)`.
- `Rewrite`, `Around` and `TxAround` over an exported `*Statement`.
- `Policy()` and `Hooks()` written by hand and bound by velox-gen with a compile-time signature check.

### Proposed order of work
1. **Semantics, breaking:** #1 Tx context and `ErrOutsideTx`; #2 `Allow` and the decision on policy signatures; #3 dry-run enforcement.
2. **One rename and reshape release, bumping `GeneratedCodeIsVersion4`:** #6 names; #8 `Save` builder; #7 `Open(d, Conn)` together with the pgx split; #5 the generated-code API section or `veloximpl`.
3. #4 `Exec` and `Raw`.
4. **Additions:** #9 `First`, `Exists`, `Rel.Of`; #11 aggregates; #12 `Mutation.Conds`; #10 locking.
5. #13 docs pass. Then freeze the API with an `apidiff` check in CI.
