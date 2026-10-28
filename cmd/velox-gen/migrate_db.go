package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
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

// A secret is what of a DSN no message may show: the DSN itself, in each
// spelling, and its password where a DSN would hold it (":pw@",
// "password=pw", "pwd=pw"), so that a short or common password, such as
// velox, leaves the words of a message alone.
type secret struct {
	dsns      []string // longest first
	passwords []string // longest first
	pg        bool     // pgconn's own echo of a DSN is scrubbed too
}

// secrets is the secret of dsn, read with d's driver and as each way of
// writing a DSN may hold its password, plain and escaped. A SQLite DSN is a
// path, which messages name, unless it carries parameters.
func secrets(d velox2.Dialect, dsn string) secret {
	if d == velox2.SQLite {
		if strings.Contains(dsn, "?") {
			return secret{dsns: []string{dsn}}
		}
		return secret{}
	}
	var pws []string
	switch d {
	case velox2.Postgres:
		if c, err := pgconn.ParseConfig(dsn); err == nil {
			pws = append(pws, c.Password)
		}
	case velox2.MySQL:
		if c, err := mysql.ParseDSN(dsn); err == nil {
			pws = append(pws, c.Passwd)
		}
	}
	// user:password@ before any @ of the DSN, after a URL's scheme.
	heads := []string{dsn}
	if _, rest, ok := strings.Cut(dsn, "://"); ok {
		heads = append(heads, rest)
	}
	for _, h := range heads {
		for i := range len(h) {
			if h[i] != '@' {
				continue
			}
			if _, pw, ok := strings.Cut(h[:i], ":"); ok {
				pws = append(pws, pw)
			}
		}
	}
	// password=… of a keyword DSN, quoted or not.
	for _, m := range keywordPassword.FindAllStringSubmatch(dsn, -1) {
		pws = append(pws, m[1], strings.Trim(m[1], "'"))
	}
	return secret{dsns: spellings([]string{dsn}), passwords: spellings(pws), pg: d == velox2.Postgres}
}

// spellings is each of ss plain, unescaped and escaped, without "",
// longest first.
func spellings(ss []string) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s, url.QueryEscape(s), url.PathEscape(s))
		if u, err := url.PathUnescape(s); err == nil {
			out = append(out, u)
		}
	}
	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	return slices.Compact(out)
}

// joined adds to s each of its spellings as join, which text applies before
// redact, would print it: a DSN or password holding a file's name, such as
// velox.sum, is still hidden.
func (s secret) joined(join func(string) string) secret {
	add := func(ss []string) []string {
		out := slices.Clone(ss)
		for _, v := range ss {
			out = append(out, join(v))
		}
		return spellings(out)
	}
	s.dsns, s.passwords = add(s.dsns), add(s.passwords)
	return s
}

// keywordPassword is the password of a keyword DSN, "password=x" or
// "password='x y'".
var keywordPassword = regexp.MustCompile(`password\s*=\s*('(?:[^'\\]|\\.)*'|[^\s]+)`)

// pgEcho is pgconn's echo of a DSN or of what it read from one, in a
// message: "cannot parse `…`" and "failed to connect to `…`".
var pgEcho = regexp.MustCompile("(cannot parse|failed to connect to) `[^`]*`")

// redact is text with the DSN replaced by "xxxxx", in each spelling, and
// the password where a DSN would hold it.
func (s secret) redact(text string) string {
	for _, d := range s.dsns {
		text = strings.ReplaceAll(text, d, "xxxxx")
	}
	for _, pw := range s.passwords {
		for _, f := range [...]string{":%s@", "password='%s'", "password=%s", "pwd=%s"} {
			text = strings.ReplaceAll(text, fmt.Sprintf(f, pw), fmt.Sprintf(f, "xxxxx"))
		}
	}
	if s.pg {
		text = pgEcho.ReplaceAllString(text, "$1 `xxxxx`")
	}
	return text
}

// dsnForm refuses a dsn that is not of d's driver's form, or is a SQLite
// database in memory, before it is used: the message names src (-db or
// $VELOX_DB) and whose dialect d is (velox.json's or -dialect), never the
// DSN or the parser's text. A PostgreSQL DSN naming a file that cannot be
// read, such as sslrootcert, says so and names that file, and nothing else of
// the DSN.
func dsnForm(d velox2.Dialect, dsn, src, whose string) error {
	var name string
	ok := true
	switch d {
	case velox2.Postgres:
		name = "a PostgreSQL connection string"
		_, err := pgconn.ParseConfig(dsn)
		if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
			return fmt.Errorf("%s names a file that cannot be read: %v", src, pe)
		}
		ok = err == nil
	case velox2.MySQL:
		name = "a MySQL DSN"
		if strings.Contains(dsn, "://") {
			return fmt.Errorf("%s is not %s (%s is mysql); MySQL DSNs look like user:pass@tcp(host:3306)/db", src, name, whose)
		}
		_, err := mysql.ParseDSN(dsn)
		ok = err == nil
	case velox2.SQLite:
		name = "a SQLite path or URI"
		if scheme, _, found := strings.Cut(dsn, "://"); found && scheme != "file" {
			ok = false
		}
		if strings.HasPrefix(dsn, "file:") {
			_, err := url.Parse(dsn)
			ok = ok && err == nil
		}
		ok = ok && dsn != ""
		if ok && sqlitePath(dsn) == "" {
			return fmt.Errorf("%s is a SQLite database in memory, which ends with its connection; name a file", src)
		}
	}
	if !ok {
		return fmt.Errorf("%s is not %s (%s is %s)", src, name, whose, d)
	}
	return nil
}

// sqlitePath is the file of a SQLite DSN: a path, or a file: URI's, without
// its parameters; "" for a database in memory.
func sqlitePath(dsn string) string {
	path, query, _ := strings.Cut(dsn, "?")
	if rest, ok := strings.CutPrefix(path, "file:"); ok {
		path = rest
		if u, err := url.PathUnescape(path); err == nil {
			path = u
		}
		if q, err := url.ParseQuery(query); err == nil && q.Get("mode") == "memory" {
			return ""
		}
	}
	if path == "" || path == ":memory:" {
		return ""
	}
	return path
}

// readWrite is a SQLite DSN that opens its file without creating it
// (mode=rw), as a file: URI.
func readWrite(dsn string) string {
	path, query, _ := strings.Cut(dsn, "?")
	if !strings.HasPrefix(path, "file:") {
		path = "file:" + (&url.URL{Path: path}).EscapedPath()
	}
	q, err := url.ParseQuery(query)
	if err != nil || q.Has("mode") {
		return dsn
	}
	if query != "" {
		return path + "?" + query + "&mode=rw"
	}
	return path + "?mode=rw"
}
