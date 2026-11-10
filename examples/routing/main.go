// Command routing shows routing: two shards chosen by organization, a read replica
// for one of them, and a session that reads its own write from the primary
// while the replica is behind.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/routing/veloxgen"
	"github.com/syssam/velox/examples/routing/veloxschema"
	"github.com/syssam/velox/migrate"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./routing/migrations ./routing/veloxschema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

// The organizations, of the scope key's Go type: int64.
const (
	acme   int64 = 1 // its data is on the eu shard
	globex int64 = 2 // its data is on the us shard
)

// shardFor maps a statement's organization to its shard. An application would
// look the organization up in its own directory. A context with no organization
// names no shard, which refuses the statement with ErrNoShard; an error refuses
// it too.
func shardFor(ctx context.Context) (string, error) {
	org, ok := veloxschema.OrgKey.From(ctx)
	if !ok {
		return "", nil
	}
	switch org {
	case acme:
		return "eu", nil
	case globex:
		return "us", nil
	}
	return "", fmt.Errorf("org %d has no shard", org)
}

// A handler serves one request, with its context.
type handler func(ctx context.Context) error

// withSession wraps a handler so that its context carries a session, as an
// HTTP middleware would for each request. A session is what makes a request
// read its own writes.
func withSession(next handler) handler {
	return func(ctx context.Context) error { return next(velox.WithSession(ctx)) }
}

// where says where a statement ran, from its Statement.Route.
func where(r velox.Route) string {
	switch {
	case r.Fallback:
		return r.Shard + " primary, after the replica failed"
	case r.Replica < 0:
		return r.Shard + " primary"
	}
	return fmt.Sprintf("%s replica %d", r.Shard, r.Replica)
}

// open opens a SQLite file with the tables of migrations/, as one database server's pool.
func open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := migrate.Up(ctx, db, sub); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// bodies lists the notes in a database, bypassing velox, to show which files
// a write reached.
func bodies(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "SELECT body FROM notes ORDER BY id")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return "", err
		}
		parts = append(parts, b)
	}
	return "[" + strings.Join(parts, " ") + "]", rows.Err()
}

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-routing-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	pools := map[string]*sql.DB{}
	for _, name := range []string{"eu", "eu-replica", "us"} {
		p, err := open(ctx, filepath.Join(dir, name+".db"))
		if err != nil {
			return err
		}
		defer func() { _ = p.Close() }()
		pools[name] = p
	}
	// Replication is out of scope: both eu files start with the same row,
	// and from here on the replica is never updated, so it stays behind.
	for _, name := range []string{"eu", "eu-replica"} {
		if _, err := pools[name].ExecContext(ctx, "INSERT INTO notes (id, org_id, body) VALUES (1, 1, 'v1')"); err != nil {
			return err
		}
	}

	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// The Around hook sees each statement's Route once it has run.
	var ran velox.Route
	record := func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		ran = st.Route
		return err
	}
	db := velox.OpenShards(velox.SQLite, map[string]velox.Shard{
		"eu": {
			Primary:  velox.SQLConn(pools["eu"]),
			Replicas: []velox.Conn{velox.SQLConn(pools["eu-replica"])},
		},
		"us": {Primary: velox.SQLConn(pools["us"])},
	},
		velox.WithShardFor(shardFor),
		// Short, so the example waits only a moment. The default is 2 s.
		velox.WithPinWindow(250*time.Millisecond),
		velox.WithAround(record),
	)
	N := veloxgen.Notes
	read := func(ctx context.Context, label string) error {
		n, err := db.Query[veloxgen.Note]().Where(N.ID.EQ(1)).One(ctx)
		if err != nil {
			return err
		}
		say("%-18s %q from %s", label+":", n.Body, where(ran))
		return nil
	}

	// A request for acme: it writes, then reads.
	edit := withSession(func(ctx context.Context) error {
		if _, err := db.Update[veloxgen.Note]().Set(N.Body.Set("v2")).Where(N.ID.EQ(1)).Exec(ctx); err != nil {
			return err
		}
		say("%-18s set v2 on %s", "write:", where(ran))

		// A write pins the session's reads to the primary for the window.
		if err := read(ctx, "read, in window"); err != nil {
			return err
		}

		time.Sleep(600 * time.Millisecond) // well past the 250 ms window
		// Now the read goes to the replica, which has not got v2. This is
		// replication lag, simulated: the example never copies the write.
		if err := read(ctx, "read, after"); err != nil {
			return err
		}
		say("%-18s the stale read above is a stand-in for replication lag; replication is out of scope", "")

		// WithPrimaryReads forces the primary for one read that must be
		// current.
		return read(velox.WithPrimaryReads(ctx), "read, Primary")
	})
	if err := edit(veloxschema.OrgKey.With(ctx, acme)); err != nil {
		return err
	}

	// A write for globex reaches only the us shard.
	note := veloxgen.Note{Body: "us note"}
	if err := db.Insert(&note).Exec(veloxschema.OrgKey.With(ctx, globex)); err != nil {
		return err
	}
	say("%-18s %q for org %d on %s", "insert:", note.Body, note.OrgID, where(ran))
	for _, name := range []string{"eu", "eu-replica", "us"} {
		b, err := bodies(ctx, pools[name])
		if err != nil {
			return err
		}
		say("  file %-10s %s", name, b)
	}

	// A statement that names no shard is refused, and nothing is sent to any of
	// them. Without an organization, a scoped entity would fail with
	// scope.ErrNoScope; WithoutPolicy lifts that check, and then no shard is
	// left to pick.
	all := db.WithoutPolicy("example admin count")
	_, err = all.Query[veloxgen.Note]().Count(ctx)
	say("%-18s ErrNoShard=%t", "no shard:", errors.Is(err, velox.ErrNoShard))
	say("  %v", err)
	// WithShard names one.
	n, err := all.Query[veloxgen.Note]().Count(velox.WithShard(ctx, "us"))
	say("%-18s %d note on us (err=%v)", "WithShard:", n, err)
	return nil
}
