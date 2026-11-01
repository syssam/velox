package veloxgql

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/validator/core"
	"github.com/vektah/gqlparser/v2/validator/rules"

	"github.com/syssam/velox"
)

// Server is what Setup needs of gqlgen's *handler.Server.
type Server interface {
	http.Handler
	Use(graphql.HandlerExtension)
	SetErrorPresenter(graphql.ErrorPresenterFunc)
	SetParserTokenLimit(int)
	SetDisableSuggestion(bool)
	SetValidationRulesFn(func() *rules.Rules)
}

// Setup makes srv a server of db: call it first on a new server, and serve what
// it returns, not srv:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	h := veloxgql.Setup(srv, db)
//	srv.AddTransport(transport.POST{})
//
// It uses NewExtension(db, opts...), first of the server's extensions, and
// ErrorPresenter(nil). It sets the parser's token limit to the limits' MaxTokens,
// and gives validation's rules without their suggestions, of a field, an argument, an
// input field, an enum value or a type, whether or not the server has
// extension.Introspection: they take 25 KB for each unknown field, and show a client
// the schema, where a directive may hide part of it. Those rules return the limits'
// MaxErrors errors, and one that says there are more. The handler it returns refuses a
// body over the limits' MaxBodyBytes with 413, and BAD_USER_INPUT wrapping
// ErrRequestTooLarge; nothing else in a request bounds one. It holds the whole of
// a body of unknown length, up to that. A websocket's messages are held to the same: the
// connection of one over closes before it is read, whichever transport or gqlgen. It
// takes the extensions off the upgrade, so no message is compressed: what a client sends
// is what is counted.
//
// Call SetErrorPresenter again, after it, to give another presenter. Without
// Setup, the Extension still enforces the limits of the document and the variables,
// and no body limit holds, nor one of validation's errors.
func Setup(srv Server, db *velox.DB, opts ...Option) http.Handler {
	e := NewExtension(db, opts...)
	srv.Use(e)
	srv.SetErrorPresenter(ErrorPresenter(nil))
	srv.SetParserTokenLimit(e.limits.MaxTokens)
	srv.SetDisableSuggestion(true)
	srv.SetValidationRulesFn(func() *rules.Rules { return validationRules(e.limits.MaxErrors) })
	return bodyLimit(srv, int64(e.limits.MaxBodyBytes))
}

var (
	quietOnce sync.Once
	quiet     []core.RuleFunc
)

// quietRules are gqlparser's rules with no suggestions, in the order of their names.
func quietRules() []core.RuleFunc {
	quietOnce.Do(func() { quiet = newQuietRules() })
	return quiet
}

func newQuietRules() []core.RuleFunc {
	d := rules.NewDefaultRules()
	for _, r := range []struct {
		name string
		rule core.Rule
	}{
		{"FieldsOnCorrectType", rules.FieldsOnCorrectTypeRuleWithoutSuggestions},
		{"KnownArgumentNames", rules.KnownArgumentNamesRuleWithoutSuggestions},
		{"KnownTypeNames", rules.KnownTypeNamesRuleWithoutSuggestions},
		{"ScalarLeafs", rules.ScalarLeafsRuleWithoutSuggestions},
		{"ValuesOfCorrectType", rules.ValuesOfCorrectTypeRuleWithoutSuggestions},
	} {
		d.RemoveRule(r.name)
		d.AddRule(r.rule.Name, r.rule.RuleFunc)
	}
	inner := d.GetInner()
	out := make([]core.RuleFunc, 0, len(inner))
	for _, name := range slices.Sorted(maps.Keys(inner)) {
		out = append(out, inner[name])
	}
	return out
}

// validationRules is quietRules as one rule, that holds them to at most most errors,
// for one validation: 28 rules each wrapped would be 80 allocations of each request
// gqlgen has not cached. They have the names of the rules gqlgen adds, with no limit,
// when they are absent under SetDisableSuggestion: one at v0.17.86, two at v0.17.98.
func validationRules(most int) *rules.Rules {
	return rules.NewRules(core.Rule{
		Name:     rules.ScalarLeafsRuleWithoutSuggestions.Name,
		RuleFunc: func(*core.Events, core.AddErrFunc) {},
	}, core.Rule{
		Name: rules.FieldsOnCorrectTypeRuleWithoutSuggestions.Name,
		RuleFunc: func(obs *core.Events, addError core.AddErrFunc) {
			n := 0
			limited := func(opts ...core.ErrorOption) {
				switch n++; {
				case n <= most:
					addError(opts...)
				case n == most+1:
					addError(core.Message("over %d validation errors: the rest are left out", most))
				}
			}
			for _, rule := range quietRules() {
				rule(obs, limited)
			}
		},
	})
}

// bodyLimit refuses a body of more than most bytes.
func bodyLimit(next http.Handler, most int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			r.Header.Del("Sec-WebSocket-Extensions")
			next.ServeHTTP(&wsLimitWriter{ResponseWriter: w, most: most}, r)
			return
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
			if tooLong := new(http.MaxBytesError); errors.As(err, &tooLong) {
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

// Hijack implements http.Hijacker. A client must wait for the upgrade's answer (RFC 6455,
// section 4.1), so what it sent before that, which the server has read past the
// connection given here, is refused.
func (w *wsLimitWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rc := http.NewResponseController(w.ResponseWriter)
	conn, brw, err := rc.Hijack()
	if err != nil {
		return nil, nil, err
	}
	if brw.Reader.Buffered() > 0 {
		_ = conn.Close()
		return nil, nil, errWebsocketEarly
	}
	c := &wsLimitConn{Conn: conn, most: w.most}
	brw.Reader.Reset(c)
	return c, brw, nil
}

// Unwrap is for http.ResponseController.
func (w *wsLimitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

var (
	errWebsocketTooLarge = errors.New("veloxgql: a websocket message over Limits.MaxBodyBytes")
	errWebsocketEarly    = errors.New("veloxgql: a websocket client sent frames before the upgrade's answer")
)

// wsLimitConn reads a client's websocket frames (RFC 6455, section 5.2) as they pass, and
// closes the connection when a message's payload passes most.
type wsLimitConn struct {
	net.Conn
	most int64
	// header is the frame's header so far; skip the payload of it left to pass; message
	// the payload of the message so far, of its data frames.
	header  [14]byte
	headerN int
	skip    uint64
	message uint64
}

// Read implements net.Conn.
func (c *wsLimitConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.read(p[:n]) {
		_ = c.Conn.Close()
		return 0, errWebsocketTooLarge
	}
	return n, err
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
			"message":    fmt.Sprintf("%v: its body is over %d bytes", ErrRequestTooLarge, most),
			"extensions": map[string]any{"code": CodeBadUserInput},
		}},
		"data": nil,
	})
}
