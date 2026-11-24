package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestSourceResolves: a DB's Source is its own handle; a WithoutPolicy or
// Record copy shares it. A transaction's resolves to its handle while it is
// live, to a copy without the WithoutPolicy reason when it was begun
// WithoutPolicy, and to the DB it was begun on once it has ended, its handle
// released; a savepoint's falls back to the transaction's.
func TestSourceResolves(t *testing.T) {
	db := newDB(SQLite, &fakeconn.Exec{}, nil)
	OwnSource(&db)
	if got := db.Source().DB(); got != &db {
		t.Fatalf("the DB's Source is %p, want the DB, %p", got, &db)
	}
	priv := db.WithoutPolicy("test")
	if priv.Source() != db.Source() || priv.Source().DB().bypass != "" {
		t.Error("a WithoutPolicy copy has a Source of its own, or one with the reason")
	}
	if db.Record(&Recording{}).Source() != db.Source() {
		t.Error("a Record copy has a Source of its own")
	}

	var txSrc, spSrc *Source
	err := priv.Tx(context.Background(), func(ctx context.Context, tx *DB) error {
		txSrc = tx.Source()
		if got := txSrc.DB(); got == tx || got.bypass != "" || got.txRoot != tx.txRoot {
			t.Errorf("in a WithoutPolicy transaction, the Source is %p (reason %q), want the transaction's handle without the reason", got, got.bypass)
		}
		return tx.Tx(ctx, func(_ context.Context, sp *DB) error {
			spSrc = sp.Source()
			if spSrc.DB().txSelf != sp.txSelf {
				t.Error("in a savepoint, the Source is not the savepoint's")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if txSrc.liveTx() != nil || spSrc.liveTx() != nil {
		t.Error("an ended transaction's Source keeps its handle")
	}
	if txSrc.DB() != &db || spSrc.DB() != &db {
		t.Error("an ended transaction's Source does not fall back to the DB it was begun on")
	}

	stop := errors.New("stop")
	err = db.Tx(context.Background(), func(_ context.Context, tx *DB) error {
		txSrc = tx.Source()
		if txSrc.DB() != tx {
			t.Error("a transaction's Source is not its handle")
		}
		return stop
	})
	if !errors.Is(err, stop) || txSrc.liveTx() != nil || txSrc.DB() != &db {
		t.Errorf("after a rollback: %v; the Source keeps its handle or does not fall back", err)
	}
}

// TestSourceNoneFails: an edge query on no Source runs on a DB each of
// whose statements fails with ErrNoSource.
func TestSourceNoneFails(t *testing.T) {
	db, err := EdgeDB(nil)
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("EdgeDB(nil): %v", err)
	}
	st := Statement{Op: OpSelect, Entity: &EntityInfo{Name: "x"}}
	if err := db.outsideTx(context.Background(), &st); !errors.Is(err, ErrNoSource) {
		t.Errorf("a statement on it: %v", err)
	}
}
