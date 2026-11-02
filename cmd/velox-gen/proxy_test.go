package main

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// A cutProxy forwards TCP connections to a database server until cut, which
// closes every connection and takes no more: as after a kill -9 of the run, the
// client's side is gone at once, and the server sees it only when it next reads
// from the connection, after the statement it is running.
type cutProxy struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	done  bool
}

// newCutProxy is a cutProxy to target, a host:port, cut when t ends.
func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s, err := net.DialTimeout("tcp", target, 5*time.Second)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			if p.done {
				p.mu.Unlock()
				_ = c.Close()
				_ = s.Close()
				return
			}
			p.conns = append(p.conns, c, s)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(s, c); _ = s.Close() }()
			go func() { _, _ = io.Copy(c, s); _ = c.Close() }()
		}
	}()
	t.Cleanup(p.cut)
	return p
}

// cut closes every connection, both ways, and the listener.
func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	_ = p.ln.Close()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

// pgThrough is the PostgreSQL URL dsn through a cutProxy.
func pgThrough(t *testing.T, dsn string) (string, *cutProxy) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p := newCutProxy(t, u.Host)
	u.Host = p.ln.Addr().String()
	return u.String(), p
}

// mysqlThrough is the MySQL DSN dsn through a cutProxy.
func mysqlThrough(t *testing.T, dsn string) (string, *cutProxy) {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p := newCutProxy(t, cfg.Addr)
	cfg.Addr = p.ln.Addr().String()
	return cfg.FormatDSN(), p
}
