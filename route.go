package velox

import (
	"fmt"
	"maps"
	"slices"

	"github.com/syssam/velox2/internal/engine"
)

// OpenShards returns a DB of dialect d whose statements each run on one of
// shards, chosen from the statement's context:
//
//  1. the shard WithShard names;
//  2. otherwise, the one WithShardFor's function returns for the context's
//     tenant (WithTenant); its error refuses the statement;
//  3. otherwise none: the statement fails with ErrNoShard, as it does under
//     WithAllTenants without WithShard.
//
// A name that is not one of shards fails with ErrNoShard too, naming it.
// Within its shard a statement is routed as on a DB with replicas: writes to
// the primary, reads to the replicas in turn, a failed replica read to the
// shard's primary. Statement.Route.Shard names the shard.
//
// Tx picks its shard from its context at begin, and every statement in the
// transaction runs there: one whose context names no shard as well, but one
// whose context names another fails, and nothing is sent.
//
// WithShardFor is required, and WithReplicas is refused: each Shard has its
// own Replicas. OpenShards panics on either mistake, on a d that is not
// Postgres, MySQL or SQLite, on no shards, on a shard named "" and on a shard
// whose Primary or a replica is the zero Conn, or a Conn valid only on another
// dialect.
func OpenShards(d Dialect, shards map[string]Shard, opts ...Option) *DB {
	if !engine.ValidDialect(d) {
		panic(fmt.Sprintf("velox: OpenShards: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	if len(shards) == 0 {
		panic("velox: OpenShards: no shards")
	}
	cfg := engine.NewConfig(opts)
	if cfg.ShardFor == nil {
		panic("velox: OpenShards: WithShardFor is required, to find a statement's shard from its tenant")
	}
	if len(cfg.Replicas) > 0 {
		panic("velox: OpenShards: WithReplicas is for Open and OpenConn; give each Shard its Replicas")
	}
	rt := &engine.Router{Shards: make(map[string]*engine.ShardConn, len(shards)), ShardFor: cfg.ShardFor}
	for _, name := range slices.Sorted(maps.Keys(shards)) {
		s := shards[name]
		if name == "" {
			panic("velox: OpenShards: a shard's name must not be empty")
		}
		what := fmt.Sprintf("OpenShards: shard %q's", name)
		switch {
		case engine.ConnExec(s.Primary) == nil:
			panic(fmt.Sprintf("velox: %s Primary is the zero Conn; make it with SQLConn or a driver package's Conn", what))
		case engine.ConnOnly(s.Primary) != 0 && engine.ConnOnly(s.Primary) != d:
			panic(fmt.Sprintf("velox: %s Primary is valid only on %s; the DB is %s", what, engine.ConnOnly(s.Primary), d))
		}
		for i, c := range s.Replicas {
			if engine.ConnExec(c) == nil {
				panic(fmt.Sprintf("velox: %s replica %d is the zero Conn; make it with SQLConn or a driver package's Conn", what, i))
			}
		}
		sh := engine.NewShard(d, engine.ConnExec(s.Primary), s.Replicas, what)
		sh.Name = name
		rt.Shards[name] = sh
	}
	return &DB{e: engine.DB{D: d, Rt: rt, Cfg: cfg}}
}
