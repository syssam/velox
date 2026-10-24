package main

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the pgx driver
	_ "modernc.org/sqlite"             // the sqlite driver

	velox2 "github.com/syssam/velox2"
)

// openDB opens dsn with the driver of d: "pgx", "mysql" or "sqlite", whose
// packages this file imports. dsn is -db, else $VELOX_DB from env.
func openDB(d velox2.Dialect, dsn string) (*sql.DB, error) {
	driver := map[velox2.Dialect]string{velox2.Postgres: "pgx", velox2.MySQL: "mysql", velox2.SQLite: "sqlite"}[d]
	if driver == "" {
		return nil, fmt.Errorf("no driver for the dialect %q", d.String())
	}
	return sql.Open(driver, dsn)
}

// lookupEnv is the value of key in env, the last one given; a nil env is the
// process's own.
func lookupEnv(env []string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	for _, kv := range slices.Backward(env) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// notifyContext is a context cancelled on SIGINT or SIGTERM. After the
// first signal, a second one ends the process as usual.
func notifyContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// signalContext is the context of a subcommand that opens a database:
// notifyContext; tests replace it.
var signalContext = notifyContext

// secrets is what of dsn no message may show: dsn itself and its password,
// as the driver reads it and as each way of writing a DSN may hold it,
// plain and escaped, longest first.
func secrets(d velox2.Dialect, dsn string) []string {
	found := []string{dsn}
	switch d {
	case velox2.Postgres:
		if c, err := pgconn.ParseConfig(dsn); err == nil {
			found = append(found, c.Password)
		}
	case velox2.MySQL:
		if c, err := mysql.ParseDSN(dsn); err == nil {
			found = append(found, c.Passwd)
		}
	}
	// user:password@ before any @ of the DSN, after a URL's scheme.
	heads := []string{dsn}
	if _, rest, ok := strings.Cut(dsn, "://"); ok {
		heads = append(heads, rest)
	}
	for _, s := range heads {
		for i := range len(s) {
			if s[i] != '@' {
				continue
			}
			if _, pw, ok := strings.Cut(s[:i], ":"); ok {
				found = append(found, pw)
			}
		}
	}
	// password=… of a keyword DSN, quoted or not.
	for _, m := range keywordPassword.FindAllStringSubmatch(dsn, -1) {
		found = append(found, m[1], strings.Trim(m[1], "'"))
	}
	var out []string
	for _, s := range found {
		for _, v := range []string{s, url.QueryEscape(s), url.PathEscape(s)} {
			if u, err := url.PathUnescape(s); err == nil {
				out = append(out, u)
			}
			out = append(out, v)
		}
	}
	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	return slices.Compact(out)
}

// keywordPassword is the password of a keyword DSN, "password=x" or
// "password='x y'".
var keywordPassword = regexp.MustCompile(`password\s*=\s*('(?:[^'\\]|\\.)*'|[^\s]+)`)

// redact is text with each of secrets replaced by "xxxxx".
func redact(text string, secrets []string) string {
	for _, s := range secrets {
		text = strings.ReplaceAll(text, s, "xxxxx")
	}
	return text
}
