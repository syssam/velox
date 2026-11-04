// Command tracker is an issue tracker's JSON API on velox: organizations
// that scope their rows, logins, roles enforced by policy, relations, transactions, keyset
// pages and migrations, on SQLite or PostgreSQL. See README.md.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // migrations run through database/sql
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	_ "github.com/syssam/velox/examples/tracker/gen/runtime" // binds the schema's hooks and policies
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/veloxpgx"
)

// shutdownWait is how long requests in flight get to finish. dbLimits bound what
// any one of them can hold on to: on SQLite, whose one connection everyone shares,
// a statement without a limit would queue every request behind it.
const shutdownWait = 25 * time.Second

var dbLimits = velox.WithDeadlines(velox.Deadlines{Statement: 10 * time.Second, Tx: 20 * time.Second})

// The tables come from a directory per dialect, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name <name> -dir ./tracker/migrations/sqlite ./tracker/schema
//	go tool velox-gen migrate diff -dialect postgres -name <name> -dir ./tracker/migrations/postgres ./tracker/schema
//
//go:embed migrations/sqlite migrations/postgres
var migrations embed.FS

func main() {
	addr := flag.String("addr", "localhost:8080", "the address to serve on")
	flag.Parse()
	if err := run(*addr, os.Getenv("TRACKER_DB")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run serves on addr until an interrupt. dsn comes from the environment, where
// its password stays out of the process list: a postgres:// URL, a SQLite file
// path, or empty for a SQLite file that lasts as long as the process.
func run(addr, dsn string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if dsn == "" {
		dir, err := os.MkdirTemp("", "velox-tracker-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		dsn = filepath.Join(dir, "tracker.db")
	}
	db, closeDB, err := open(ctx, dsn)
	if err != nil {
		return err
	}
	defer closeDB()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("listening on http://%s", ln.Addr())
	return serveUntil(ctx, ln, newServer(db))
}

// serveUntil serves on ln until ctx ends, then lets the requests in flight finish,
// for up to shutdownWait. It returns only then, so that the caller can close the
// database under no request.
func serveUntil(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Printf("shutdown: %v", err)
			_ = srv.Close()
		}
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-drained
	return nil
}

// open applies the migrations and returns a DB on dsn, with what closes it.
func open(ctx context.Context, dsn string) (*velox.DB, func(), error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return openPostgres(ctx, dsn)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	sqlDB, err := sql.Open("sqlite", dsn+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, err
	}
	sqlDB.SetMaxOpenConns(1) // SQLite has one writer
	closeDB := func() { _ = sqlDB.Close() }
	if err := up(ctx, sqlDB, "migrations/sqlite"); err != nil {
		closeDB()
		return nil, nil, err
	}
	return velox.Open(velox.SQLite, sqlDB, dbLimits), closeDB, nil
}

// openPostgres migrates through database/sql, then runs statements on a pgx pool
// through veloxpgx, pgx's native protocol.
func openPostgres(ctx context.Context, dsn string) (*velox.DB, func(), error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	if err := up(ctx, sqlDB, "migrations/postgres"); err != nil {
		return nil, nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return veloxpgx.Open(pool, dbLimits), pool.Close, nil
}

// up applies the migrations of dir, an embedded directory.
func up(ctx context.Context, db *sql.DB, dir string) error {
	sub, err := fs.Sub(migrations, dir)
	if err != nil {
		return err
	}
	_, err = migrate.Up(ctx, db, sub)
	return err
}
