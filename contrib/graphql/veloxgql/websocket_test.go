package veloxgql_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// wsDial upgrades a raw connection to url's server, as a client that writes frames itself
// does: the limit is on what it sends, whatever a library would let it.
func wsDial(t *testing.T, url string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	_, err = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Protocol: graphql-transport-ws\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	var head strings.Builder
	b := make([]byte, 1)
	for !strings.HasSuffix(head.String(), "\r\n\r\n") {
		if _, err := c.Read(b); err != nil {
			t.Fatalf("the upgrade: %v, after %q", err, head.String())
		}
		head.WriteByte(b[0])
	}
	if !strings.Contains(head.String(), "101") || strings.Contains(strings.ToLower(head.String()), "permessage-deflate") {
		t.Fatalf("the upgrade: %s", head.String())
	}
	return c
}

// wsFrame is a client's frame, masked by zeros, its opcode op and fin as given.
func wsFrame(op byte, fin bool, payload string) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	f := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		f = append(f, 0x80|byte(n))
	case n < 1<<16:
		f = append(f, 0x80|126)
		f = binary.BigEndian.AppendUint16(f, uint16(n))
	default:
		f = append(f, 0x80|127)
		f = binary.BigEndian.AppendUint64(f, uint64(n))
	}
	f = append(f, 0, 0, 0, 0)
	return append(f, payload...)
}

// wsAck reads one server frame, and reports whether the connection gave one.
func wsAck(c net.Conn) (string, bool) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(c, h); err != nil {
		return "", false
	}
	p := make([]byte, h[1]&0x7f)
	if _, err := io.ReadFull(c, p); err != nil {
		return "", false
	}
	return string(p), true
}

// TestWebsocketMessageBounded: under Setup a websocket's message is held to MaxBodyBytes,
// however it is split, before gqlgen reads it: 64 MB of connection_init took 512 MB, and
// at v0.17.86 gqlgen's transport has no limit to set. Control frames, the payload's bytes
// passing a read's end and a message under it pass; only what a client sends counts, as
// the upgrade drops its compression.
func TestWebsocketMessageBounded(t *testing.T) {
	db, _ := handlerDB(t)
	srv := handler.New(testSchemaOf(budgetSDL, map[string]resolver{"text": nil}))
	const most = 4096
	h := veloxgql.Setup(srv, db, veloxgql.WithLimits(veloxgql.Limits{MaxBodyBytes: most}))
	srv.AddTransport(transport.Websocket{})
	ts := httptest.NewServer(h)
	defer ts.Close()

	init := func(pad int) string {
		b, err := json.Marshal(map[string]any{"type": "connection_init", "payload": map[string]string{"x": strings.Repeat("x", pad)}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	const ack = `{"type":"connection_ack"}`
	// One byte at a time, a ping between a message's frames, and each frame's sizes.
	ok := wsDial(t, ts.URL)
	msg := init(most - 100)
	first, second := msg[:len(msg)/2], msg[len(msg)/2:]
	stream := append(append(wsFrame(0x1, false, first), wsFrame(0x9, true, "ping")...), wsFrame(0x0, true, second)...)
	for _, b := range stream {
		if _, err := ok.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	for {
		got, alive := wsAck(ok)
		if !alive {
			t.Fatal("a message at the limit closed the connection")
		}
		if strings.Contains(got, "connection_ack") || got == ack {
			break
		}
	}
	// A message after it starts anew.
	if _, err := ok.Write(wsFrame(0x1, true, `{"type":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, alive := wsAck(ok); !alive {
		t.Fatal("the next message closed the connection")
	}

	for name, frames := range map[string][][]byte{
		"one frame":  {wsFrame(0x1, true, init(2*most))},
		"two frames": {wsFrame(0x1, false, init(most-100)), wsFrame(0x0, true, strings.Repeat("x", 200))},
		// Its length alone says so: the 64 MB need not be sent, or held.
		"a length": {[]byte{0x81, 0x80 | 127, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0}},
	} {
		c := wsDial(t, ts.URL)
		for _, f := range frames {
			_, _ = c.Write(f)
		}
		for {
			if _, alive := wsAck(c); !alive {
				break
			}
		}
		// The read returned because the connection closed, and not at its deadline.
		if err := c.SetDeadline(time.Time{}); err != nil {
			t.Fatal(name, err)
		}
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Errorf("%s: the connection is open", name)
		}
	}
}

// TestSetupKeepsOtherTransports: the websocket's writer is only the upgrade's: a POST goes on
// as before.
func TestSetupKeepsOtherTransports(t *testing.T) {
	db, _ := handlerDB(t)
	srv := handler.New(testSchemaOf(budgetSDL, map[string]resolver{"slow": func(_ context.Context, _ map[string]any) (any, error) { return 1, nil }}))
	h := veloxgql.Setup(srv, db)
	srv.AddTransport(transport.POST{})
	if got := post(t, t.Context(), h, `{ slow }`); got != `{"data":{"slow":1}}` {
		t.Errorf("a POST: %s", got)
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r) // no Hijacker, no transport: gqlgen says so, and nothing panics
	if w.Code == http.StatusOK {
		t.Errorf("an upgrade it cannot make: %d %s", w.Code, w.Body)
	}
}
