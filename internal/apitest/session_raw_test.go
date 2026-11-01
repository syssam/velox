package apitest

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestTxRawSQLPins: velox cannot tell what raw SQL does, and a transaction's
// raw SQL is where its documentation sends a write that returns rows. Exec and
// Raw in a transaction count as a write, whose commit pins the session.
func TestTxRawSQLPins(t *testing.T) {
	for name, fn := range map[string]func(context.Context, *velox.DB) error{
		"Exec": func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Exec(ctx, "test", "UPDATE invoices SET paid = true")
			return err
		},
		"Raw": func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Raw[testmodel.Invoice]("test", "UPDATE invoices SET paid = true RETURNING id").All(ctx)
			return err
		},
		"Raw Iter": func(ctx context.Context, tx *velox.DB) error {
			for _, err := range tx.Raw[testmodel.Invoice]("test", "UPDATE invoices SET paid = true RETURNING id").Iter(ctx) {
				if err != nil {
					return err
				}
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := seeded(hooked)
				s.p.Cols = []string{"id"}
				s.p.Rows = [][]any{{int64(1)}}
				sctx := velox.WithSession(ctx)
				if err := s.db.Tx(sctx, fn); err != nil {
					t.Fatal(err)
				}
				if velox.PinnedUntil(sctx).IsZero() {
					t.Fatal("the session is not pinned after the commit")
				}
				s.p.Rows = [][]any{invoiceRow(1)}
				readOne(t, sctx, s.db)
				if s.r0.Calls+s.r1.Calls != 0 {
					t.Errorf("the read after the commit went to a replica: %v", s.calls())
				}
			})
		})
	}
}

// TestRawOutsideTxDoesNotPin: outside a transaction Raw stays a read.
func TestRawOutsideTxDoesNotPin(t *testing.T) {
	s := seeded(false)
	s.r0.Cols = []string{"id"}
	s.r0.Rows = [][]any{{int64(1)}}
	sctx := velox.WithSession(ctx)
	if _, err := s.db.Raw[testmodel.Invoice]("test", "SELECT id FROM invoices").All(sctx); err != nil {
		t.Fatal(err)
	}
	if !velox.PinnedUntil(sctx).IsZero() {
		t.Error("a Raw read pinned the session")
	}
}
