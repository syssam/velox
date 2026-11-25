package gqlgenx

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// MaxBodyBytes returns the HTTP middleware that holds a request's body, its
// variables and any upload included, to n bytes, as nothing in gqlgen does.
// It is opt-in, for a server open to clients:
//
//	http.Handle("/graphql", gqlgenx.MaxBodyBytes(1<<20)(srv))
//
// A body over n is refused with 413, and BAD_USER_INPUT wrapping
// veloxgql.ErrRequestTooLarge. It holds the whole of a body of unknown
// length, up to n. A websocket's messages are held to n too: the connection
// of one over closes, with the code 1009, before it is read, whichever
// transport or gqlgen. It takes the extensions off the upgrade, so no
// message is compressed: what a client sends is what is counted.
//
// How many messages or connections a client opens is not bounded here: a
// rate limit in front is. It panics on an n below 1.
func MaxBodyBytes(n int64) func(http.Handler) http.Handler {
	if n < 1 {
		panic(fmt.Sprintf("gqlgenx.MaxBodyBytes(%d): the limit must be at least 1", n))
	}
	return func(next http.Handler) http.Handler { return bodyLimit(next, n) }
}

// bodyLimit refuses a body of more than most bytes.
func bodyLimit(next http.Handler, most int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A header is no reason to skip the body's limit: a POST may name it too.
		if r.Header.Get("Upgrade") != "" {
			r.Header.Del("Sec-WebSocket-Extensions")
			w = &wsLimitWriter{ResponseWriter: w, most: most}
		}
		switch {
		case r.ContentLength > most:
			refuseBody(w, most)
			return
		case r.ContentLength > 0:
			r.Body = http.MaxBytesReader(w, r.Body, most)
		case r.ContentLength < 0:
			// Of unknown length: a transport that fails to read it answers with
			// its own text, so it is read here.
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, most))
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				refuseBody(w, most)
				return
			} else if err != nil {
				http.Error(w, "could not read the request's body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			r.ContentLength = int64(len(b))
		}
		next.ServeHTTP(w, r)
	})
}

// wsLimitWriter gives a websocket library a connection that holds each message to most
// bytes: neither gqlgen's Upgrader at v0.17.86 nor its Implementation since has a limit
// the other has, and a message is decoded in full before any extension sees it.
type wsLimitWriter struct {
	http.ResponseWriter
	most int64
}

// Hijack implements http.Hijacker. What the server read past the upgrade's
// request is the client's first frames: coder/websocket, gqlgen's default
// at v0.17.95 and v0.17.98, sends the 101 before it hijacks, so a client
// that answers at once may have sent them. They pass the limit as any others do.
func (w *wsLimitWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rc := http.NewResponseController(w.ResponseWriter)
	conn, brw, err := rc.Hijack()
	if err != nil {
		return nil, nil, err
	}
	c := &wsLimitConn{Conn: conn, most: w.most}
	if n := brw.Reader.Buffered(); n > 0 {
		b, _ := brw.Peek(n)
		c.early = bytes.Clone(b)
	}
	brw.Reader.Reset(c)
	return c, brw, nil
}

// Unwrap is for http.ResponseController.
func (w *wsLimitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

var errWebsocketTooLarge = errors.New("veloxgql: a websocket message over MaxBodyBytes")

// wsLimitConn reads a client's websocket frames as they pass, and
// closes the connection when a message's payload passes most.
type wsLimitConn struct {
	net.Conn
	most int64
	// mu holds each Write whole, so that the close frame sent when a message is too
	// large comes between the library's frames, never inside one.
	mu sync.Mutex
	// header is the frame's header so far; skip the payload of it left to pass; message
	// the payload of the message so far, of its data frames.
	header  [14]byte
	headerN int
	skip    uint64
	message uint64
	// early is what the server read past the upgrade's request, which Read
	// returns first.
	early []byte
}

// Read implements net.Conn.
func (c *wsLimitConn) Read(p []byte) (int, error) {
	var n int
	var err error
	if len(c.early) > 0 {
		n = copy(p, c.early)
		c.early = c.early[n:]
	} else {
		n, err = c.Conn.Read(p)
	}
	if !c.read(p[:n]) {
		// 1009, message too big: a client told 1006, an abnormal close, retries the
		// same message.
		c.mu.Lock()
		_, _ = c.Conn.Write([]byte(wsTooBig))
		c.mu.Unlock()
		_ = c.Close()
		return 0, errWebsocketTooLarge
	}
	return n, err
}

// wsTooBig is a server's close frame, unmasked, of code 1009 (0x03f1), whose reason
// is the 25 bytes after them; 0x1b is their length and the code's 2.
const wsTooBig = "\x88\x1b\x03\xf1message over MaxBodyBytes"

// Write implements net.Conn.
func (c *wsLimitConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Write(b)
}

// read passes b, and reports whether its messages are within the limit.
func (c *wsLimitConn) read(b []byte) bool {
	for len(b) > 0 {
		if c.skip > 0 {
			k := min(c.skip, uint64(len(b)))
			c.skip -= k
			b = b[k:]
			continue
		}
		c.header[c.headerN] = b[0]
		c.headerN++
		b = b[1:]
		need := 2
		if c.headerN >= 2 {
			switch c.header[1] & 0x7f {
			case 126:
				need += 2
			case 127:
				need += 8
			}
			if c.header[1]&0x80 != 0 {
				need += 4 // the mask
			}
		}
		if c.headerN < need {
			continue
		}
		c.headerN = 0
		var size uint64
		switch payload := c.header[1] & 0x7f; payload {
		case 126:
			size = uint64(binary.BigEndian.Uint16(c.header[2:]))
		case 127:
			size = binary.BigEndian.Uint64(c.header[2:])
		default:
			size = uint64(payload)
		}
		c.skip = size
		if c.header[0]&0x08 != 0 { // a control frame, between a message's frames
			continue
		}
		if size > uint64(c.most) || c.message+size > uint64(c.most) {
			return false
		}
		c.message += size
		if c.header[0]&0x80 != 0 { // its last frame
			c.message = 0
		}
	}
	return true
}

// refuseBody writes bodyLimit's response.
func refuseBody(w http.ResponseWriter, most int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{
			"message":    fmt.Sprintf("%v: its body is over %d bytes", veloxgql.ErrRequestTooLarge, most),
			"extensions": map[string]any{"code": veloxgql.CodeBadUserInput},
		}},
		"data": nil,
	})
}
