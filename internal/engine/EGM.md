# velox/internal/engine Blueprint

## System Overview

`internal/engine` is everything non-generic under velox: statements, SQL
rendering and its cache, policies and scope, relation loading, routing,
transactions, recording and errors. The root package (`github.com/syssam/velox`)
holds only the generic API (`Query[E]`, `Update[E]`, `Insert`, `Tx`) and
re-exports most names here as aliases (decision 0020; see
[the decision index](../../docs/decisions/README.md)).

A statement moves through one pipeline: a builder fills a `Statement`, the
`DB` resolves names, runs the guard, Rewrite hooks, policy and scope, renders
(through the shape cache), routes to a primary or replica, sends, and scans.
The sections below follow that path. Each claim cites `[file:symbol]` in this
directory. The package has no subdirectories.

## Local Interface & Invariants

### Pipeline, options and connections (`pipeline.go`, `db.go`, `pgversion.go`, `route.go`, `deadline.go`, `comment.go`, `callers.go`, `count.go`, `log.go`)

**Interface**

- `DB` (exported fields D, Ex, Rt, Cfg, InTx), `Open`, `Connect`, `OpenConn`, `OpenShards`, `Pool`, `DB.Close`, `Hooked`, `Plain`, `Run`, `DryRun`, `ExecWrite` [pipeline.go:DB].
- Options: `Option`, `CombineOptions`, `WithRewrite`, `WithAround`, `WithValue`/`Value`, `WithMaxLimit`, `WithMaxRows`, `WithSQLiteLockingReads`, `WithMaxParams`, `WithRollbackTimeout`, `CheckColumns` [db.go:Option]; `WithDeadlines`, `TxTimeout` [deadline.go:WithDeadlines]; `WithoutReturningOld` [pgversion.go:WithoutReturningOld].
- Observability: `WithCallers`, `WithCallerSkip` [callers.go:WithCallers]; `WithSQLComment` [comment.go:WithSQLComment]; `StatementCounter`, `CountStatements`, `WithStatementCounting` [count.go:CountStatements]; `WithLog`, `LogOption`, `LogArgs`, `ArgShow`, `ShownArgs`, `MaskedArgs`, `RedactedError` [log.go:WithLog].
- Routing: `Conn`, `SQLConn`, `DriverConn`, `NewConn`, `WithReplicas`, `WithShardFor`, `WithPinWindow`, `WithReplicaBackoff`, `Shard`, `WithShard`, `WithPrimaryReads`, `WithSession`, `PinUntil`, `PinnedUntil` [route.go:Conn].

**Invariants**

- `Open`, `OpenConn` and `OpenShards` panic on an invalid dialect; `OpenConn` and a shard's Primary or replica panic on a zero Conn or one of another dialect; `OpenShards` also panics on no shards, an empty shard name, a missing `WithShardFor`, or `WithReplicas` (replicas belong on each `Shard`); `Open`, `OpenConn` and `Connect` panic if `WithShardFor` is set. `Open` with a nil `*sql.DB` makes a render-only DB whose statements fail [pipeline.go:Open] [pipeline.go:OpenShards] [route.go:newShard].
- `Connect` on MySQL needs `parseTime=true` and `clientFoundRows=true`; its errors never quote the DSN; a SQLite pool is capped at one connection [pipeline.go:checkMySQLDSN].
- Every write passes through `Plain` or `Run`, so `ErrDryRun` is enforced in one place; `dryWrite` is applied there and never in `prepare`, because `SQL(ctx)` renders through `prepare` [pipeline.go:dryWrite].
- `Plain` order: dryWrite, outsideTx, held, ResolveStatement, guard, scope, keyReturning, capLimit, render, route. `Run` order: dryWrite, `prepare` (outsideTx, ResolveStatement, guard, observe, Rewrite hooks and their checks, scope, keyReturning, capLimit, render), held, route, checkParams, then `execute`, where the Around hooks wrap the send; Rewrite hooks run after the guard and before scope, Around hooks after rendering and routing [pipeline.go:Plain] [pipeline.go:Run] [pipeline.go:prepare].
- After Rewrite hooks the statement is restored and checked; a hook cannot change Op, Writable, InTx, Count, Exists, Distinct, Aggregate, GroupBy, Lock, Each, Project, Joins, the order-value count, Columns (count/exists/aggregate) or Returning (recorded writes, updates). The Entity must be a copy with the same kit, column count, policy and filters; pointing `st.Entity` at another EntityInfo bypasses the render cache [rewrite.go:rewriteSnapshot.restoreAndCheck] [rewrite.go:rewriteSnapshot.checkEntity] [pipeline.go:prepare].
- `takeRewriteSnapshot` runs before `cloneForHooks`; velox's own marks (bidi, pin, iter, loaded, recordState) are restored whole before the checks [rewrite.go:takeRewriteSnapshot].
- The `read` function given to `Run` runs once per execution (an Around retry reruns it) and must not close Rows; an Around hook must call next and not discard its error [pipeline.go:Run].
- One statement is in flight per transaction: `exec` takes `txRoot.sending` by CAS and returns `errTxShared` otherwise [pipeline.go:exec].
- Statements over the dialect or `WithMaxParams` argument limit are refused before sending [db.go:checkParams]. Option constructors panic on nil (`WithRewrite`, `WithAround`, `WithValue` key, `WithMaxLimit` n<2, `WithMaxRows` 1) and the zero Option panics at Connect/Open [db.go:Option].
- Velox's own deadlines carry cause `ErrTimeout` (the caller's never) and apply only when earlier than the context's [deadline.go:bound].
- `WithSQLComment` panics on an empty app or a bad tag; keys and values are percent-encoded; the fingerprint excludes the comment [comment.go:WithSQLComment].
- Routing precedence: writes (raw Exec too), statements in a transaction, `WithPrimaryReads` and a pinned session go to the primary; a With-load reuses its query's shard and replica (primary with Fallback if that replica is backed off); a dry run uses `Pick(peek)` and takes no round-robin turn [route.go:DB.route] [route.go:shardConn.Pick].
- In a transaction on a sharded DB a statement resolving to another shard is an error; the context's route marks are copy-on-write under one key [route.go:DB.routeTx] [route.go:routeCtx].
- A failed replica read is retried once on the primary only when no row reached the caller; writes never fail over; `capLimit` sets Limit to cap+1 so an overrun returns `ErrTooManyRows` [pipeline.go:sendStatement] [pipeline.go:capLimit].
- `failoverable` is false for context errors and query errors; `connLost` is `failoverable` less PG class 28 and MySQL 1045/1129/1130 [route.go:failoverable] [route.go:connLost].
- The pin end only rises (CAS); a write counts once it reached the driver, even if it failed [route.go:session.Raise] [route.go:DB.wrote].
- A transaction's deadline (velox's or the caller's, whichever is earlier) bounds every statement and savepoint inside it whatever ctx they get; `over()` waits up to 50 ms (`overGrace`) for Done after the deadline [deadline.go:bound] [tx.go:over].
- A statement with `st.iter` skips the `txRoot.sending` CAS, so an `Iter` loop body may run statements in the same transaction; overlap with an Iter is deliberately not detected [pipeline.go:exec] [iterconn.go:holdConn].
- `StatementCounter.Check` tests a `StatementBudget` (N+1 limits) and returns a `BudgetError`; `WithValue` panics on a non-comparable key, `WithMaxParams` and `WithRollbackTimeout` clamp negatives to 0, `WithMaxRows(n<=0)` means no cap [count.go:StatementBudget] [db.go:WithValue] [db.go:WithMaxParams]. `Pool` is nil inside a transaction and `Close` closes only a DB that `Connect` opened [pipeline.go:Pool].
- The server version is never read while a concurrent reader is loading it (`versionLoading` CAS, `errVersionBusy`; waiting could deadlock a one-connection pool); `autoIncrementStep` has no such marker, so concurrent callers may each send the MySQL query. Inside a transaction `returnsOld` reads only the cached version (outside one it calls `serverVersion`, which may send the query); `primeVersion` runs before BEGIN for a transaction velox opens, and in a caller's transaction `loadVersion` runs in a savepoint of its own [pgversion.go:loadVersion] [pgversion.go:autoIncrementStep] [pgversion.go:primeVersion].

**Side Effects**

- Sends `SHOW server_version_num` (PostgreSQL) and `SELECT @@auto_increment_increment` (MySQL) straight to the executor, and caches them in atomics on the config or shard [pgversion.go:loadVersion].
- `Run`/`Plain` mutate the Statement (SQL, Args, Fingerprint, Route, Result) and use the shared render cache; `fallBack` backs off the failed replica and resets `st.Route` [pipeline.go:Run] [route.go:DB.fallBack].
- Per-DB caches behind locks: the callers PC cache (4096 entries) is made per config, the comment cache (4096 entries, 4 MiB, oldest evicted first) per `WithSQLComment`; both limits are package vars. A `StatementCounter` is per context and sits under a mutex [callers.go:WithCallers] [comment.go:newCommenter] [count.go:StatementCounter].
- `WithLog` installs an Around hook that logs `velox: statement` at Debug with timing and rows; args are masked before next, and error text is shown only with `LogArgs` and every arg shown [log.go:WithLog] [log.go:RedactedError].
- Round-robin and back-off use atomics (`shardConn.next`, `replica.DownUntil`); `WithShardFor`'s function may run concurrently and several times per write [route.go:shardConn.Pick] [route.go:WithShardFor].
- A pool-of-one guard refuses a statement issued under a holding `Iter` loop or `Tx` context; `watchHeld` starts a timer that logs `slog.Warn` after 5 s [iterconn.go:holdConn] [iterconn.go:watchHeld].

### Transactions (`tx.go`, `txfrom.go`, `txhook.go`, `adopt.go`, `aftercommit.go`, `deadline.go`)

**Interface**

- `TxOption` (zero is invalid), `TxIsolation`, `TxReadOnly`, `TxRetryRolledBack`, `TxCancelOnEnd`, `TxRetries`, `WithTxDefaults`, `DefaultRollbackTimeout`, `WithoutTx`, `DB.Tx`, `DB.In`, `DB.Foreign` [tx.go:TxOption]; `TxFrom` [txfrom.go:TxFrom]; `WithTxAround` [txhook.go:WithTxAround].
- `UseTx` adopts a caller-owned `*sql.Tx` [adopt.go:UseTx]; `AfterCommit`, `BeforeCommit`, `AfterRollback` queue callbacks on the root frame [aftercommit.go:runAfter].

**Invariants**

- The zero `TxOption` panics at `WithTxDefaults` and, through a nil call, in `TxRetries` and in a `DB.Tx` that begins an outermost transaction; a nested `Tx` given any option returns an error and uses the outermost's [tx.go:DB.Tx] [tx.go:WithTxDefaults].
- Checks before BEGIN: In, misuse, Foreign (`ErrOutsideTx`), dry run (`ErrDryRun`), the savepoint branch, shard (`ErrNoShard`, before any TxAround hook), connection wait; a Tx on a foreign DB fails before anything is sent [tx.go:DB.Tx].
- Defer order in `transaction` is load-bearing: the rollback runs first, then `txSource.end` and `f.ended` (after the rollback, before the note), then `releaseConn`, and with `TxCancelOnEnd` `cancel(ErrTxEnded)` last; a panic or Goexit in fn rolls back and queues no after-functions [tx.go:DB.transaction].
- COMMIT is never sent after ctx ended or the deadline passed, nor when the frame is shared or broken; ROLLBACK, ROLLBACK TO and the RELEASE that follows it run on `context.WithoutCancel` with `RollbackTimeout` (default 5 s), capped at the transaction deadline for savepoints; the success-path `RELEASE SAVEPOINT` keeps the statement's ctx [tx.go:DB.commit] [tx.go:rollbackContext] [tx.go:DB.savepoint].
- Savepoints are `velox_N` from an atomic counter and never reused; rollback is ROLLBACK TO then RELEASE; a failed savepoint rollback sets `txRoot.broken` (first error only, and not once the transaction deadline has passed), after which the transaction can only roll back [tx.go:DB.savepoint] [tx.go:rollbackSavepoint] [tx.go:DB.breakOn].
- `TxRetryRolledBack(n)` retries a rolled-back attempt (including a COMMIT the server reports as rolled back, such as PG 40001 or MySQL 1213) and a lost connection (`connLost`) at BEGIN or in a statement; a COMMIT error matching `ErrCommitUnknown` is never retried. The `wrote` and `lost` flags are fresh per attempt; the wait is half the ceiling plus a random part, ceiling from `retryBase` (10 ms) doubling to `retryMax` (1 s) [tx.go:DB.attempts] [tx.go:retryDelay].
- `TxCancelOnEnd` cancels fn's context (and savepoint contexts) with cause `ErrTxEnded` when the attempt ends; COMMIT, ROLLBACK and AfterCommit run on the outer ctx; a retry gets a fresh ctx (ADR 0182) [tx.go:TxCancelOnEnd].
- A transaction is single-goroutine: overlapping statements force a rollback with `errTxShared`; `afterMu` guards the callback queues and `afterClosed` refuses late queuing [tx.go:txFrame].
- A TxAround hook calls next exactly once and returns its error; the first hook added is outermost; a panic after commit runs AfterCommit functions on the caller ctx first [txhook.go:DB.txAround].
- A DB joins a transaction only when src, bypass, misuse and rec match the beginner's (`TestDBFieldsClassified` keeps every DB field classified); `TxFrom` rejects an ended transaction, another Cfg or another rec [tx.go:sameMode] [txfrom.go:TxFrom].
- `UseTx` refuses a nil tx, a DB already in a tx, a sharded DB and a connection that cannot adopt; an adopted frame refuses the callback queues [adopt.go:UseTx] [aftercommit.go:txOf].
- Callback queues take nothing for a nil fn, a DB outside a tx, an ended tx, or the DB of a nested Tx that has already returned; a rolled-back savepoint drops its commit callbacks and makes its rollback callbacks due; `runAfter` runs in order on `context.WithoutCancel`, a panic does not stop the others and the first is re-raised [aftercommit.go:runAfter].

**Side Effects**

- Calls `Begin(ctx, sql.TxOptions)`, sends COMMIT/ROLLBACK, holds and releases a connection, ends the Source and sets `f.ended` [tx.go:DB.transaction].
- Savepoints send `SAVEPOINT`, `RELEASE SAVEPOINT` and `ROLLBACK TO SAVEPOINT`, truncate the recording batch and drop kept rows and callbacks queued past the mark [tx.go:DB.savepoint].
- After a failed statement `noteServerRollback` breaks the transaction on MySQL 1213, sends `SELECT @@innodb_rollback_on_timeout` for 1205, and on SQLite probes with `BEGIN` (unless past the deadline) [tx.go:DB.noteServerRollback].
- `retryBase`, `retryMax` and `retryWait` are mutable package vars that tests override; `txIDs` is a global atomic counter [tx.go:retryBase] [txhook.go:txIDs].
- User callbacks run at different points: `fn` and `BeforeCommit` functions inside the held transaction (`runBefore` before COMMIT), TxAround hooks around the whole call including BEGIN, and only AfterCommit/AfterRollback functions after the connection is back in the pool [tx.go:DB.Tx] [txhook.go:DB.txAround] [aftercommit.go:runAfter].

### Statements, predicates and rendering (`statement.go`, `pred.go`, `predset.go`, `named.go`, `expr.go`, `term.go`, `render.go`, `render_cache.go`, `repeat.go`, `json*.go`, `arrayin.go`, `datetrunc.go`, `decimal.go`)

**Interface**

- `Statement`, `ConflictClause`, `EachClause`, `JoinClause`, `Rewrite`, `Around`, `KeepDefaults` [statement.go:Statement].
- Predicates: `Node`, `Pred[E]`, `Predicate`, `Group`, `JoinedNode`, `ColumnEQ`, `ResolveNode`, `ResolveStatement`, `EscapeLike`, `AndPredicates`, `OrPredicates`, `NotPredicate` [pred.go:Node] [predset.go:AndPredicates]; by name: `FieldEQ`…`FieldContainsFold`, `FieldIn`, `FieldIsNull`, `HasEdge`, `HasEdgeWith`, `FieldInSelect`, `FieldInSelectRaw` [named.go:ColumnNamed].
- Terms: `Projection`, `Term`, `ColumnTerm`, `ExcludedTerm`, `ExprTerm`, `JSONTerm`, `NamedTerm`, `AggTerm`, `RelTerm`, `DateTruncTerm`, `DecimalTerm` [term.go:Term]; `ExprNode`, `ExprRawNode` [expr.go:ExprNode]; JSON conditions `JSONNode`, `JSONHasNode`, `JSONContainsNode`, `JSONAtPath` [jsonpath.go:JSONNode] [jsonarray.go:JSONHasNode].
- `RenderStmt`, `RenderCache` (`Get`, `Size`), `ShapeKey`, `Fingerprint`, `DefaultMaxShapes`, `DefaultMaxCacheBytes`, `MaxCachedRows` [render.go:RenderStmt] [render_cache.go:RenderCache].

**Invariants**

- A zero `Predicate` fails the statement holding it; trees are built through `Group` so Deep/ByName are set; `ResolveNode` never edits its input (changed nodes are copies, unchanged subtrees shared) [pred.go:Group] [pred.go:ResolveNode].
- `ResolveStatement` order: joins, names, conditions, before guard, Rewrite hooks, scope and rendering; `Sound` reports whether a condition is built only from public-API parts and is used by `OrPredicates`, while a Rewrite hook's by-name conditions are resolved and its columns checked afterwards by `checkRewritten` [pred.go:ResolveStatement] [pred.go:Sound] [rewrite.go:DB.checkRewritten].
- A name-based condition fails on an unknown column; nil comparison values are errors; `FieldInSelectRaw` needs a reason; a named node keeps its name as `unsafe.Pointer` in W, so the string must outlive the node [named.go:namedLeaf] [named.go:nameData].
- `AndPredicates` drops Allow kids; `OrPredicates` collapses to Allow only when a kid is Allow and every kid is Sound; `NotPredicate` stays `//go:noinline` for compile cost [predset.go:NotPredicate].
- `ExprNode`/`ExprTerm` text has no quotes except plain string literals, no `; \ $ #` and no banned words; `ExprRawNode` (needs a reason) allows quoted identifiers, subqueries and banned words but keeps the ban on `; \ $ #` comments and unbalanced parentheses; both need the `?` count to equal the slots, and `ExprNode` (not `ExprRawNode`) refuses a window function; the shape is resealed after any change [expr.go:ExprNode] [expr.go:ExprRawNode].
- Every Term constructor ends in `.seal()` and any copy that changes the text reseals it (`TextTerm`, `IntegerTerm`, `TimeTerm` and `BoolTerm` only set a mark, which `seal` reads for decimals alone), because the cache key reads `t.shape` as is; `Term.problem` stays unexported so Term's type information stays small [term.go:Term.seal].
- A Projection is a plain OpSelect with at least one term and no Count, Exists, Aggregate, GroupBy, Each, Columns or Lock; `ExcludedTerm` is valid only in an upsert's update branch [term.go:checkProject] [term.go:ExcludedTerm].
- Text-changing parts of a statement are in the shape key, and bound values are not, because cached text is reused with only the args re-collected; the few values that change text are keyed (the LIMIT literal, nil/false/true for OpEQ/OpNEQ, which INSERT columns are literal NULL, the IN length, the SQLite append length); an INSERT over `MaxCachedRows` rows or with DEFAULT cells is never cached; the cache is keyed by `*EntityInfo` pointer and evicts at random on a miss [render_cache.go:ShapeKey] [render_cache.go:RenderCache.Get].
- IN lists are padded to `inLen(n)` (powers of two to 1024, then multiples of 1024 to 16384) so shapes are shared; LIKE uses `ESCAPE '!'` on every dialect; LIMIT is a literal and OFFSET a parameter; NULL ordering follows PostgreSQL by default (native on PostgreSQL, an `IS NULL` term on MySQL and SQLite), and with `Nulls` set PostgreSQL and SQLite write native `NULLS FIRST/LAST` while only MySQL emulates [render.go:inLen] [render.go:selectTail] [render.go:orderBy] [render.go:nullsWay].
- The writer: with `argsOnly` it collects args and writes no text; the first error wins and text and args are unusable once `w.err` is set; an unknown Statement.Op panics [render.go:writer.fail] [render.go:writer.statement].
- Upsert limits: MySQL takes no UpdateWhere, OnConflictWhere or OnConstraint and needs row alias `new` (8.4+); OnConstraint is PostgreSQL only; RETURNING is written only off MySQL [render.go:writer.conflict] [render.go:writer.returning].
- Repeated terms are dialect-specific: PostgreSQL reuses placeholders, MySQL wraps in `ANY_VALUE`/`MIN`, SQLite repeats nothing; on MySQL a bound term repeating a `WITH ROLLUP` key fails [repeat.go:writer.writeSpan].
- Encoded (Codec) columns support only IsNull/NotNull and a JSON handle's Text/Number/HasKey; compare, order, group, Add and After over them are errors; Decode must neither modify nor retain its input [json.go:errCodecCondition] [json.go:codecScanner.Scan].
- JSON path keys are non-empty with no double quote, backslash or control character, because keys are inlined; bad input becomes an `OpInvalid` node, not a panic; SQLite has no JSON containment; PostgreSQL's `@>` form applies only in a non-negated filter and needs jsonb [jsonpath.go:JSONPath.check] [jsonarray.go:jsonContains] [jsonpath.go:containable].
- Append on SQLite requires the element count to equal `appendLen`, because the render cache keys SQLite text by slice length [jsonappend.go:appendLen].
- PostgreSQL array form (`= ANY`) is used only on PostgreSQL, outside JSON paths, for plain element kinds [arrayin.go:arrayElemOK].
- `Statement.Entity` is shared read-only across goroutines; a Rewrite hook changes only the statement (it also runs for `SQL(ctx)`); an Around hook calls next and does not assign `*st` after it; Statement size stays in the 640-byte class [statement.go:Statement] [statement.go:Rewrite].

**Side Effects**

- `RenderCache.Get` takes `c.mu` (read to look up, write to add); a hit re-renders in args-only mode to collect arguments; `evict` deletes a random shape [render_cache.go:RenderCache.Get] [render_cache.go:RenderCache.evict].
- The writer mutates and restores `in`, `alias`, `depth`, `joinAlias`, `qualify`, `filter`, `neg`, `upsert`, `having`, `raw`, `grouped`, `keys`, `distinct`, `path` and `reuse`; `joins` and `spans` are set or appended and never restored, so the code relies on a fresh writer per `RenderStmt`, no global mutable state [render.go:writer].
- `ResolveStatement` replaces `st.Where`, `Conflict` and `Project` with resolved copies when something changed, and `st.Joins` with a cloned, linked set; when the DB is hooked, `cloneForHooks` replaces Where, OrderBy, Columns, Set, Returning, Joins, Tags, Rows, Project, Conflict and Each with copies [pred.go:ResolveStatement] [rewrite.go:cloneForHooks].
- Codec scanning allocates one `codecScanner` per encoded column per row and writes through reflect and `unsafe` [json.go:ScanDst].
- Package globals are read-only tables and sentinels: `likeEscaper` (`sync.OnceValue`), `jsonParsedSQL`, `exprBanned`, `termFnSQL`, `cmpText`, `lockSQL`, `dateUnitSQL` and the like [pred.go:EscapeLike] [jsonparsed.go:jsonParsedSQL] [expr.go:exprBanned].

### Policy, filters, scope and guard (`policy.go`, `behaviour.go`, `privacy.go`, `scope.go`, `filterdb.go`, `guard.go`, `validate.go`)

**Interface**

- `WithoutPolicy`, `PolicyBypass`, `WithScopeValue`, `RowScopeFromContext`, `IsDryRun`, `Deny`, and the hook helpers `MutationState.SetAffected`, `MutateSet`, `MutateWhere` (they record a misuse, first error wins, rather than panic) [policy.go:WithoutPolicy] [policy.go:MutateSet]; `Policy`, `PolicyFor`, `FiltersFor`, `WithEntityHooks`, `WithAllHooks`, `WithFilter` [behaviour.go:PolicyFor]; `Decision`, `PrivacyAllow`/`Deny`/`Skip`, `QueryEval`, `MutationEval`, `PrivacyDecisionContext` [privacy.go:Decision].
- `Scoper` with `RowCond`, `PolicyCond`, `PolicyOn`, `Record`, `Walk`, `Node`, `ConditionUpsert` [scope.go:Scoper].
- `ValidateRow`, `ValidateInsert`, `ValidateSetters` [validate.go:ValidateRow].

**Invariants**

- A policy returns `Allow()` for no restriction; a zero Predicate from a policy, filter or interceptor fails the statement; a policy's error denies (a `*PolicyError` matching `ErrDenied`), a filter's or interceptor's error is a plain failure, not a denial; an Insert policy calling `Row.Set` fails the insert [behaviour.go:PolicyFor] [scope.go:Scoper.PolicyCond].
- Hook order: `WithAllHooks`, the entity's own, `WithEntityHooks`; privacy decisions: nil/Skip continue, Allow ends with nil, anything else ends with itself, and a decision in the context replaces rule evaluation [behaviour.go:hooksOf] [privacy.go:decided].
- `DB.guard` runs before hooks on the statement as built: an entity SELECT needs `Limit>0` or `AllowUnbounded` (a GroupBy always needs `AllowUnbounded`) and `Limit<=MaxLimit` when `MaxLimit` is set; Count, Exists and Aggregate selects skip those checks; Update and Delete need a non-trivial Where unless `AllRows`; locking reads need a Tx [guard.go:DB.guard].
- Scope runs after guard and Rewrite hooks so neither sees its additions; raw SQL is never scoped; `Walk`, `Node` and `ConditionUpsert` never mutate caller-owned predicates or the Conflict clause [scope.go:DB.scope] [scope.go:Scoper.Walk].
- Self-referencing conditions are cut (ADR 0091): an inner EXISTS on an entity whose condition is being computed gets none; `WithFilter` filters and interceptors run in `rowCond` on a DB with the bypass reason cleared, so `WithoutPolicy` never lifts them (ADR 0151); the join-entity check calls the filters with the DB as given [scope.go:Scoper.RowCond] [scope.go:Scoper.keptDB].
- A MySQL upsert that updates is refused with `ErrPolicyUpsert` when a Read or Write policy applies (not under `WithoutPolicy`), the entity has an interceptor, or a `WithFilter` filter returns a condition; Mutation rules count as Write, a Query policy does not; DoNothing is exempt [scope.go:Scoper.mysqlUpsert] [scope.go:DB.scopeInsert].
- A many-to-many join entity must not be restricted by any `WithFilter` filter (asked with OpSelect); interceptors and policies on it are not checked here [scope.go:Scoper.joinFree].
- `Policy.Query` applies only to reading statements (a non-Writable SELECT and the EXISTS and joins inside it), after Read, never to UPDATE, DELETE or an upsert's update branch; `Policy.Mutation` folds into Write and Insert [behaviour.go:Policy] [scope.go:Scoper.rowCond].
- The op handed to `WithFilter` filters: OpUpdate for a Writable SELECT, OpDelete for a soft-delete UPDATE, OpInsert for an upsert's update branch, OpSelect for an entity reached through EXISTS [scope.go:DB.scope].
- A raw condition (`ExprRaw`, `FieldInSelectRaw`) lists its reason in `Statement.RawReasons` (Where, join On, Having, upsert TargetWhere and Where) but does not set PolicyBypass; its columns are written qualified, and `Link.Raw` skips the target's Read conditions [expr.go:RawReasons] [named.go:FieldInSelectRaw].
- A filter, interceptor or policy function is skipped when ctx is already under a call of the same function (ends read recursion) [filterdb.go:enter].
- `WithoutPolicy` needs a non-blank reason, else every statement and Tx fails; the copy owns nothing, so Close on it closes nothing [policy.go:WithoutPolicy].
- Validation order: column check, nil `RequiredEdge` key, immutable (setters only), then column validators; validators are skipped for a column left to the DB default unless the conflict clause writes it or the setters set it explicitly [validate.go:ValidateInsert].

**Side Effects**

- `DB.scope` rewrites the Statement in place (Where, OrderBy and Project copies, cloned Joins with On set, a denied LEFT join gets a false On); it calls user filters and policies, which may query the database, and sends no SQL itself [scope.go:DB.scope].
- Raw `RawExec`/`RawRows` go through `Run` with the policy bypassed [raw.go:RawExec].

### Writes, inserts, edges and recording (`hook.go`, `insert.go`, `lean.go`, `bulk.go`, `setters.go`, `write.go`, `edgeset.go`, `bidi.go`, `record.go`, `record_upsert.go`, `change.go`, `row.go`)

**Interface**

- `Mutate`, `MutationState`, `Row` (`Value`, `Set`, `Entity`), `NewRow`, `Setters`, `AssignSet`…`AssignAppend`, `AssignSkip`, `Insert`, `InsertSQL`, `Update`, `UpdateOne`, `Write`, `DeleteOne`, `InsertBulk` [hook.go:Mutate] [row.go:Row] [setters.go:Setters] [write.go:Write] [bulk.go:InsertBulk].
- `EdgeSet`, `PlanEdges`, `InsertEdges`, `UpdateEdges` [edgeset.go:PlanEdges]; `Recording`, `ChangeRow`, `DB.Record` [record.go:Recording].

**Invariants**

- `Mutate`: with no hooks core runs alone; a hook calls next once; one that calls `SetAffected` must not call next; the first hook is outermost [hook.go:Mutate].
- Insert defaults fill before hooks, so a hook's value wins; the lean path runs after hooks and before Rewrite hooks and drops columns every row leaves empty except conflict-written or explicit ones [insert.go:FillDefaults] [lean.go:LeanInsert].
- Upsert: `CheckConflict` requires at least one conflict column (the database decides whether they form a unique key); immutable and version columns are not updatable; `CheckUpserted` fails closed on an unknown affected count (`ErrUnknownAffected` for -1) only when the scope conditioned the update branch (`st.upsertPolicy`: a Read policy, an interceptor or filter condition, or a Write policy) [insert.go:CheckConflict] [insert.go:CheckUpserted].
- `Setters.Add` never mutates its receiver; the last setter of a column wins; an insert accepts only `AssignSet` column setters; `UpsertSet` refuses edge setters [setters.go:Setters.Add] [setters.go:Setters.CheckInsert] [setters.go:UpsertSet].
- `UpdateOne` needs a primary key and refuses Add/Sub/expression on it, and always runs in a transaction (`ownTx` unless already in one; `atomically`, a savepoint in a caller's transaction, when edges are written); it sends by key (no key SELECT) when Where pins every key column and there are no edge setters, else reads the key first (Writable, under policy, `FOR UPDATE` except SQLite); it reads the row back by key under `WithoutPolicy`, unscoped, on the key-first path when n>0, assignments were given or edges were written, and on the key-pinned path only when n==1 (at n==0 it re-reads by the original Where under the caller's policy and scope) [setters.go:updateOne] [setters.go:updateByKey].
- `Write`: the guard runs on the caller's statement before the hooks when the entity has hooks, and always in the pipeline; an empty patch sends nothing; the caller may not assign the Version column (`ErrVersionSet`); `DeleteOne` with Where pinning every key column is one `Write` (a count over 1 is taken as one row), otherwise it runs atomically, returns `ErrNotFound`, and rolls back an n>1 delete with `ErrNotSingular` [write.go:writeCore] [write.go:DeleteOne].
- `InsertBulk` rows are built by Insert on the same DB with no upsert option and no edge setters; every row is checked before any changes; several column groups run in one Tx [bulk.go:prepareBulk] [bulk.go:insertBulk].
- Edge setters: `Set` only for an owner-row FK naming the target's single-column key; `Add`/`Remove`/`Clear` are refused for owner-FK and bidirectional one-to-one edges; write order is Clear, Remove, Add inside one transaction [edgeset.go:PlanEdges].
- Edge writes go through the untyped Table API, so the target's and join entity's policies apply: Clear and Remove on a many-to-many edge delete only links to readable targets, Add of a join row is INSERT ON CONFLICT DO NOTHING, Clear pages join rows `EdgePage` (500) at a time, a one-to-one Add of a child held by another owner fails with `ErrLinked`, and an Add of children whose foreign key is on the target's rows is refused for more than one owner at once (a many-to-many Add accepts several owners) [edgeset.go:writeThrough] [edgeset.go:replaceChild].
- `InsertBulk` runs each row's hooks nested (row 0 outermost) around one core; rows are grouped by identical given columns, key generation and upsert update columns, each group an InsertAll; hooks see one-row MutationStates and every row's `Affected` is the total [bulk.go:bulkHooks] [bulk.go:bulkGroups].
- SQLite has no DEFAULT keyword in VALUES, so InsertAll splits into runs by which cells are left to defaults (`DefaultRuns`); the join entity of a bidirectional edge doubles each row with a mirror (`MirrorRows`, except a self-link) and halves the rows per statement [lean.go:DefaultRuns] [insert.go:MirrorRows].
- An update keeps the last assignment per column (Add/Sub after Add/Sub of the same column is an error); `UpdateDefault` values are appended for unassigned columns unless named by `KeepDefaults`; on a bidirectional join entity a delete's Where is widened to match each row's mirror (`bidiWhere`) [write.go:lastAssignments] [write.go:withUpdateDefaults].
- Bidirectional edges need a self-referential One field, a one-column key and a nullable key with its own unique index; updates pin exactly one row; a Rewrite hook may not change it [bidi.go:checkEdgeBidi] [bidi.go:checkBidiRewrite].
- Recording: `emit` runs only inside a recorded write's frame; updates changing no non-ignored column are dropped; times are UTC; `batch.flush` calls `Recording.Sink` before RELEASE/COMMIT (a sink error rolls the write back) when `Keep` is nil or the transaction was adopted by `UseTx`, otherwise it calls `Keep` and notes the ids in `txRoot.kept` (`Drop` on rollback); the sink's own writes join the transaction unrecorded [record.go:emit] [record.go:batch.flush].
- A recorded MySQL upsert is refused when a conflict column is NULL, or when the generated key is itself a conflict column and is zero; the affected count must match the rows read back (needs `clientFoundRows=true`); recorded rows are deep-copied; conflict reads bypass Read policy and scope [record_upsert.go:refuseUnfindable] [record_upsert.go:conflictRead.counted] [record_upsert.go:ownCopy].
- `Row` misuse (unknown column, wrong entity, bad index, Set on a read-only insert row, nil into non-nullable, wrong type) keeps the first error and returns it; a Row is valid only while the write runs [row.go:Row.record] [row.go:MutationState.set].

**Side Effects**

- Inserts send one INSERT per default run in batches (RETURNING on PostgreSQL/SQLite, follow-up SELECT by key on MySQL); `insertMany` opens `db.Tx` for more than one default run (SQLite), more than `per` rows, or a conditioned upsert; the single-row `InsertRow` opens one only for a MySQL upsert that reads back outside a transaction or a bidirectional-key row, while a recording handle (`recordInsertRow`, `recordMany`) always runs the write in a recorded transaction or savepoint [twrite.go:table.insertMany] [lean.go:SendBatch] [insert.go:InsertRow].
- `UpdateOne` sends (key-first path) a SELECT of the key, the UPDATE, edge writes and a SELECT read-back; the key-pinned path sends UPDATE then read-back [setters.go:updateOne].
- Recorded writes queue `ChangeRow`s with `emit` and the sink or `Keep` runs later in `batch.flush`; a recorded upsert on MySQL, SQLite or PostgreSQL below 18 (or with `WithoutReturningOld`) first pre-reads the conflicting rows (`lockConflicts`, `FOR UPDATE` except on SQLite), and on MySQL reads back by the conflict columns after the INSERT; PostgreSQL 18 sends one INSERT with RETURNING old and new and no pre-read; a plain recorded MySQL insert reads back by primary key; recorded UPDATE and DELETE pre-read their matched rows the same way (`FOR UPDATE` except SQLite) unless PostgreSQL 18 returns old and new in one statement [record_upsert.go:sendRecorded] [record_upsert.go:lockConflicts] [change.go:trackedWrite].
- `Row.Set`, `PrepareInsert`, `SetElem` and `BumpVersion` write into entity memory through `unsafe`/reflect; `utcTimes` rewrites row times in place [row.go:storeValue] [setters.go:PrepareInsert] [record.go:utcTimes].
- Process-wide caches: `equalMethods` (`sync.Map`) in `change.go`; `tables` keyed by `*EntityInfo` in `behaviour.go` [change.go:equalMethods] [behaviour.go:MutationTable].

### Reads, loading and relations (`query.go`, `load.go`, `each.go`, `join.go`, `joinset.go`, `joinedterm.go`, `relation.go`, `ordervalues.go`, `raw.go`, `rawrows.go`, `scanproj.go`, `tquery.go`, `twrite.go`, `source.go`, `state.go`, `table.go`, `tcheck.go`)

**Interface**

- `AddAfter`, `AddAfterValues`, `CloneStatement`, `CountRun`, `ExistsRun`, `ErrIterAgain` [query.go:AddAfterValues]; `OrderValueTerm`, `EncodeOrderValue`, `DecodeOrderValue`, `ErrOrderValue` [ordervalues.go:OrderValueTerm].
- `LoadRelations`, `LoadSpec`, `EachQuery`, `LoadEach`, `CountEach` [load.go:LoadRelations] [each.go:LoadEach]; `JoinSet`, `JoinFill`, `JoinedTerm` [joinset.go:JoinSet].
- `Link`, `RelRef`, `ColRef`, `EdgeDescriptor`, `Kit`, `Holder` [relation.go:Link]; `ScanPlan`, `NewScanPlan`, `ScanRows` [scanproj.go:NewScanPlan].
- Raw SQL: `DB.RawExec`, `DB.RawRows`, `RawRows`, `RawRow`, `ProjectRows` [raw.go:RawExec] [rawrows.go:ProjectRows].
- Untyped layer: `Table`, `TableQuery`, `TableInsert`, `TableUpdate`, `TableDelete`, `TableRows`, `Source`, `QueryState`, `MetaState[E]`, `WithTags` [table.go:Table] [tquery.go:TableQuery] [twrite.go:TableInsert] [source.go:Source] [state.go:QueryState] [tags.go:WithTags].

**Invariants**

- `AddAfter` needs a total non-empty OrderBy (primary key or a unique index of non-null columns), selected order columns, and no Codec, slice or map columns; `len(vals)` must equal the number of order terms for which `OrderValueTerm` is true (expression terms, a `JoinedTerm` included); plain columns, an `OrderTerm` on a joined relation's column and relation aggregates take none, and a forged or cross-dialect value fails with `ErrOrderValue` [query.go:AddAfterValues] [ordervalues.go:OrderValueTerm].
- Raw SQL needs a non-empty reason, and may not end an open transaction (COMMIT, ROLLBACK, BEGIN, `PREPARE TRANSACTION`, and on MySQL implicit-commit DDL return `ErrTxControl`); a RawRow is valid only in its loop body and its rows read once per run (`ErrIterAgain`) [raw.go:refuseTxEnd] [rawrows.go:RawRow].
- Joins are OpSelect only, to-one only, never with Aggregate, GroupBy, Each through a join table or locking; a joined term needs the matching Join/LeftJoin in the same query; a Rewrite hook may not change the joins [join.go:guardJoins] [joinedterm.go:JoinedTerm].
- `LoadEach`/`CountEach` refuse Limit, Offset, locks and a nil owner element; `CheckLoads` runs before the owner query in `LoadEach` and the `TableQuery` read paths, not in `CountEach`; `LoadRelations` marks every owner Loaded (`loadOne`), `LoadEach` returns per-owner slices and sets no Holder; statements stay under the dialect's parameter limit; block arenas never move rows [each.go:eachRefused] [load.go:CheckLoads] [load.go:loadOne].
- `relation.Resolve` checks the target's One/Many type against the field holder before its cached link; through relations need a Many field and a join entity with no Read policy or interceptors [relation.go:relation.Resolve] [relation.go:relation.buildThrough].
- A scan plan is built before any SQL is sent: every result column has a name and a distinct field, the field type must hold the column type, and a velox tag wins over a name match; ambiguity is an error [scanproj.go:NewScanPlan].
- `EntityInfo` is shared and read-only, slices included [meta.go:EntityInfo]; `validate` refuses duplicate columns, empty or duplicate key sets, and Codec key columns [meta.go:validate].
- `TableQuery` and its writers are value types whose methods return copies; the first error sticks; `One` forces Limit=2, `First` Limit=1; Select projections are read with `Rows` only [tquery.go:TableQuery.fail] [tquery.go:TableQuery.one].
- Untyped entry points validate column ranges, the entity tag and value types before running and return errors, never panics; a Table row must be a non-nil `*E` of the exact type [tcheck.go:checkStatement] [table.go:table.rowOf].
- A Source never resolves to a DB carrying a bypass reason; `Source.tx` stays an `unsafe.Pointer` accessed with `sync/atomic`; `Source.Put` stays inlineable [source.go:Source] [source.go:Source.Put].
- `TableCache` holds the Table as `any` so no Meta names Table (compile cost) [state.go:TableCache].

**Side Effects**

- Loads send chunked SELECTs through `Run`/`Plain`, pinned to the owner's replica or shard, and put results in the DB's Source; reads write rows through `unsafe` pointer arithmetic and set `Holder.Loaded/V` [load.go:LoadRelations] [tquery.go:readTable].
- `relation.once` memoises each relation's Link across all handles; `entityTypes` (`sync.Map`) and `noSourceDB` are package globals in `source.go`; `TableOfState` builds each Table once through `sync.Once` [relation.go:relation] [source.go:entityTypes] [state.go:TableOfState].
- On PostgreSQL `noteRawEnd` probes with `SAVEPOINT velox_open` only after a raw multi-statement text (`multiStatement`), SQLite probes after every raw statement; either may break the Tx [raw.go:postgresTxGone] [raw.go:noteRawEnd].
- `openSQLite` runs `PRAGMA foreign_keys = ON` on every new pooled connection unless the DSN names `foreign_keys` [sqlitefk.go:openSQLite].

### Schema metadata and errors (`descriptor.go`, `meta.go`, `entities.go`, `errors.go`, `dialect.go`, `veloxtype.go`)

**Interface**

- `BuildDescriptor`, `Through`, `NewEdgeDescriptor`, `EdgeThroughOf` [descriptor.go:BuildDescriptor]; `EntityInfo`, `ColumnInfo`, `GoType`, `ElemType`, `SetInfoKit` [meta.go:EntityInfo]; `EntityTable.Load`, `TypeKey`, `PackageEntities` [entities.go:EntityTable]; `QuoteIdent` [dialect.go:QuoteIdent].
- Errors: `ErrNotFound`, `ErrNotSingular`, `ErrUnbounded`, `ErrNoWhere`, `ErrTooManyRows`, `ErrLocked`, `ErrTimeout`, `ErrPolicyUpsert`, `ErrStale`, `ErrNoShard`, `ErrOutsideTx`, `ErrTxEnded`, `ErrCommitUnknown`, `ErrTxControl`, `ErrDryRun`, `IsNotFound` [errors.go:ErrNotFound].
- `veloxtype` aliases and constants (Op, Dialect, Lock, Assign*, Constraint*, `ErrDenied`) re-exported for the root [veloxtype.go:ErrDenied].

**Invariants**

- A descriptor build recovers panics into `*SchemaError`; it needs `FieldPtr` and a non-empty Table, and an entity with Policy, Hooks or Interceptors methods needs `Schema: *E` [descriptor.go:BuildDescriptor].
- `GoType` and `ElemType` are functions, not methods, so no velox type reaches `reflect.Type` (binary size) [meta.go:GoType].
- Errors read `velox: <Entity>: ...`; `dbErr` never re-wraps a `*ConstraintError`; classification order is classified, constraint, lock (`ErrLocked`), generic [errors.go:dbErr].
- `EntityTable.Load` builds once; a failed build makes lookups fail rather than widen, and ambiguous names are an error [entities.go:EntityTable.Load].

**Side Effects**

- `BuildDescriptor` registers the entity in the global registry on success; `rootPkg` is computed from reflect at init [descriptor.go:BuildDescriptor] [meta.go:rootPkg].

## Subdirectories & System Boundaries

- None: the package has no subdirectories. Its drivers' error reading lives in `internal/dberr` and its public data types in `veloxtype` (see [the decision index](../../docs/decisions/README.md), decisions 0021 and 0053).

## Global Invariants

- Behaviour tests live here (often `package engine_test`); public-API tests live in `internal/apitest`; repository invariants (API snapshot, compile-cost limits, coverage floors) live in `internal/repocheck` [CLAUDE.md](../../CLAUDE.md).
- A statement's shape (everything but entity and argument values) fully determines its text; code that changes text must change the shape key, or the render cache serves stale SQL [render_cache.go:ShapeKey].
- Exported names here cost compile time in every generated entity package; the code keeps `//go:noinline` and `any`-typed fields where the compile-cost test requires it [predset.go:NotPredicate] [state.go:TableCache].

## Global Side Effects

- Statements leave through `DB.Run`/`DB.Plain` to an `executor`, except those sent straight to `db.Ex` or the executor: SAVEPOINT / RELEASE / ROLLBACK TO, the SQLite `BEGIN` probe, the `innodb_rollback_on_timeout`, `server_version_num` and `auto_increment_increment` reads, and `SAVEPOINT velox_open`, while a transaction's own BEGIN, COMMIT and ROLLBACK go through `Begin` and the connection's controls and `PRAGMA foreign_keys = ON` runs on each new SQLite connection; the one-statement-per-transaction rule and the pool-of-one guard keep a single connection from deadlocking itself [pipeline.go:exec] [iterconn.go:holdConn] [tx.go:DB.savepoint] [raw.go:postgresTxGone].
- Process-global state is `equalMethods`, `tables`, `entityTypes` (also the descriptor registry), `noSourceDB`, `likeEscaper`, the `txIDs` counter and the mutable vars `maxPCs`, `maxComments`, `maxCommentBytes`, `heldWarnAfter`, `retryBase`, `retryMax` and `retryWait`; the render cache, callers cache and comment cache are per DB [change.go:equalMethods] [behaviour.go:MutationTable] [source.go:entityTypes] [render_cache.go:RenderCache].

## System Verification

- Run: `go test ./...`
