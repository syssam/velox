package veloxgql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
// a body of unknown length, up to that.
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
