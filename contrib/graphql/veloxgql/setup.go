package veloxgql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox"
)

// Server is what Setup needs of gqlgen's *handler.Server.
type Server interface {
	http.Handler
	Use(graphql.HandlerExtension)
	SetErrorPresenter(graphql.ErrorPresenterFunc)
	SetParserTokenLimit(int)
	SetDisableSuggestion(bool)
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
// and turns off the suggestions of a field's name, which take 25 KB for each
// unknown field and show a client the schema. The handler it returns refuses a
// body over the limits' MaxBodyBytes with 413, and BAD_USER_INPUT wrapping
// ErrRequestTooLarge; nothing else in a request bounds one. It holds the whole of
// a body of unknown length, up to that.
//
// Call SetErrorPresenter again, after it, to give another presenter. Without
// Setup, the Extension still enforces MaxTokens and MaxFields, and no body limit
// holds.
func Setup(srv Server, db *velox.DB, opts ...Option) http.Handler {
	e := NewExtension(db, opts...)
	srv.Use(e)
	srv.SetErrorPresenter(ErrorPresenter(nil))
	srv.SetParserTokenLimit(e.limits.MaxTokens)
	srv.SetDisableSuggestion(true)
	return bodyLimit(srv, int64(e.limits.MaxBodyBytes))
}

// bodyLimit refuses a body of more than most bytes.
func bodyLimit(next http.Handler, most int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
