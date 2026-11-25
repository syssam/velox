package gqlgenx_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestMaxBodyBytes: a body over the limit is refused with 413, whether its
// length is known or not, and one under it, of either kind, is served.
func TestMaxBodyBytes(t *testing.T) {
	db, _ := handlerDB(t)
	srv := handler.New(testSchema(handlerResolvers(db, nil)))
	h := gqlgenx.MaxBodyBytes(100)(srv)
	srv.AddTransport(transport.POST{})
	do := func(body io.Reader, length int64) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/", body)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = length
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	ok := `{"query":"{ count }"}`
	if code, got := do(strings.NewReader(ok), int64(len(ok))); code != http.StatusOK || !strings.Contains(got, `"count"`) {
		t.Errorf("under the limit: %d %s", code, got)
	}
	if code, got := do(strings.NewReader(ok), -1); code != http.StatusOK || !strings.Contains(got, `"count"`) {
		t.Errorf("of unknown length: %d %s", code, got)
	}
	long := `{"query":"{ count }","extensions":{"x":"` + strings.Repeat("x", 100) + `"}}`
	const want = `{"data":null,"errors":[{"extensions":{"code":"BAD_USER_INPUT"},"message":"request is too large: its body is over 100 bytes"}]}` + "\n"
	for _, length := range []int64{int64(len(long)), -1} {
		if code, got := do(strings.NewReader(long), length); code != http.StatusRequestEntityTooLarge || got != want {
			t.Errorf("length %d: %d %s", length, code, got)
		}
	}
	// A length that lies, under the limit, is cut by it.
	if code, got := do(strings.NewReader(long), 50); code == http.StatusOK && !strings.Contains(got, "errors") {
		t.Errorf("a lying length: %d %s", code, got)
	}
}

// TestMaxBodyBytesPanics: a limit below 1 would refuse every request, so it
// panics, naming the limit.
func TestMaxBodyBytesPanics(t *testing.T) {
	for _, n := range []int64{0, -1} {
		func() {
			want := fmt.Sprintf("gqlgenx.MaxBodyBytes(%d): the limit must be at least 1", n)
			defer func() {
				if p := recover(); p == nil || fmt.Sprint(p) != want {
					t.Errorf("MaxBodyBytes(%d) panicked with %v, want %q", n, p, want)
				}
			}()
			gqlgenx.MaxBodyBytes(n)
		}()
	}
	// 1 is the least.
	_ = gqlgenx.MaxBodyBytes(1)
}
