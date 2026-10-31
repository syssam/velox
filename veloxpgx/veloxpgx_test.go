package veloxpgx_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox/veloxpgx"
)

func TestNilPoolPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"Conn": func() { veloxpgx.Conn(nil) },
		"Open": func() { veloxpgx.Open((*pgxpool.Pool)(nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != "veloxpgx: Conn(nil)" {
					t.Errorf("recovered %v", r)
				}
			}()
			f()
		})
	}
}
