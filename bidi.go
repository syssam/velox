package velox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/syssam/velox2/internal/engine"
)

// bidiTx runs fn under db.Tx for a bidirectional write of in, after the
// refusals Tx would make as the transaction's, made here as in's, so that the
// error names the entity as the same write without the key does: a dry run,
// a statement under another transaction's mark, a transaction a savepoint
// broke, and, outside a transaction, a context that names no shard.
func bidiTx(ctx context.Context, db *DB, in *EntityInfo, fn func(ctx context.Context, tx *DB) error) error {
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", in.Name, ErrDryRun)
	}
	if db.e.Foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", in.Name, ErrOutsideTx)
	}
	if db.e.TxBroken() != nil {
		return fmt.Errorf("velox: %s: %w", in.Name, db.e.TxBroken())
	}
	if !db.e.InTx {
		if _, err := db.e.CtxShard(ctx); err != nil {
			return fmt.Errorf("velox: %s: %w", in.Name, err)
		}
	}
	return db.Tx(ctx, fn)
}

// bidiUpdate sends an update that sets bidirectional keys of row k, and then
// the statements that re-point the partners, always under db.Tx: a
// transaction, or a savepoint inside the caller's, which a failed step rolls
// back alone.
//
//  1. mu's statement, the caller's UPDATE, scoped as any. A Rewrite hook may
//     not change the row it names (CheckBidiRewrite). When it writes no row
//     nothing else is sent; when it writes more than one, the write fails.
//  2. For each key, UPDATE … SET key = NULL WHERE key = k, scope-free
//     (WithoutPolicy and WithAllTenants): it clears the old partner, which the
//     caller may not see, so that no row is left pointing at k.
//  3. For each key set to a partner v, bidiLink.
//
// The statements go through the pipeline, so Rewrite and Around hooks see
// each. mu.affected is step 1's count, the caller's rows.
func bidiUpdate(ctx context.Context, db *DB, mu *engine.MutationState, keys []engine.BidiKey, k any, vs []any) error {
	st := mu.St
	in := st.Entity
	var n int64
	err := bidiTx(ctx, db, in, func(ctx context.Context, tx *DB) error {
		st.InTx = true
		engine.SetStmtBidi(st, &engine.BidiStep{Keys: keys, Vals: vs, Pinned: k})
		res, err := execWrite(ctx, tx, st)
		engine.SetStmtBidi(st, nil)
		if err != nil {
			return err
		}
		switch n = res.Affected; {
		case n == 0:
			return nil
		case n < 0:
			return engine.ErrLinkCount(in)
		case n > 1:
			return fmt.Errorf("velox: %s: the update that sets the bidirectional key %s wrote %d rows", in.Name, in.Columns[keys[0].Col].Field, n)
		}
		free := WithAllTenants(WithoutPolicy(ctx, engine.BidiReason), engine.BidiReason)
		for _, bk := range keys {
			unlink := Statement{Op: OpUpdate, Entity: in, InTx: true,
				Set: []Assignment{{Column: bk.Col, Op: AssignSet}}, Where: []Predicate{ColumnEQ(bk.Col, k)}}
			// Any number of rows, none included, is right: the count is not read.
			if _, err := execWrite(free, tx, &unlink); err != nil {
				return err
			}
		}
		for i, bk := range keys {
			if vs[i] != nil {
				if err := bidiLink(ctx, tx, in, bk, k, vs[i]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	mu.Affected = n
	return nil
}

// bidiLink sets the key of row v to k, if v has no partner:
// UPDATE … SET key = k WHERE id = v AND key IS NULL, scoped as the caller's
// statement is. When no row matches, it returns a *ConstraintError{Entity,
// Kind: ConstraintUnique, Constraint: the key column's name, Err: …}: an
// Exists under the same scope (Writable) on id = v tells a row the caller
// can see, which already has a partner ("row v already has a <Edge>; clear
// it first"), from one it cannot, of which the error says nothing more
// ("row v cannot be linked").
func bidiLink(ctx context.Context, db *DB, in *EntityInfo, bk engine.BidiKey, k, v any) error {
	pk := in.PrimaryKey[0]
	st := Statement{Op: OpUpdate, Entity: in, InTx: db.e.InTx,
		Set:   []Assignment{{Column: bk.Col, Op: AssignSet, Value: k}},
		Where: []Predicate{ColumnEQ(pk, v), engine.NewPredicate(engine.Node{Op: engine.OpIsNull, Col: int32(bk.Col)})}}
	res, err := execWrite(ctx, db, &st)
	switch {
	case err != nil:
		return err
	case res.Affected < 0:
		return engine.ErrLinkCount(in)
	case res.Affected > 0:
		return nil
	}
	probe := Statement{Op: OpSelect, Entity: in, InTx: db.e.InTx, Exists: true, Limit: 1, Writable: true,
		Where: []Predicate{ColumnEQ(pk, v)}}
	var found bool
	read := func(r engine.Rows) error {
		found = r.Next()
		return nil
	}
	if db.e.Hooked() {
		err = db.run(ctx, &probe, read, nil)
	} else {
		err = db.plain(ctx, &probe, read, nil)
	}
	if err != nil {
		return err
	}
	why := fmt.Sprintf("row %v cannot be linked", v)
	if found {
		why = fmt.Sprintf("row %v already has a %s; clear it first", v, bk.Edge)
	}
	return &ConstraintError{Entity: in.Name, Kind: ConstraintUnique, Constraint: in.Columns[bk.Col].Name, Err: errors.New(why)}
}

// bidiInsert inserts the row of in at p, whose bidirectional keys keys hold
// the partners vs (BidiRow), under db.Tx: send sends its INSERT, then
// bidiLink sets each partner's key to the row's key, as the INSERT returned
// it when the database generated it (generated). A row given its own key as
// a partner is refused before anything is sent, and so is an upsert, which
// a hook may have made of the insert after check.
func bidiInsert(ctx context.Context, db *DB, in *EntityInfo, p unsafe.Pointer, keys []engine.BidiKey, vs []any, generated, upsert bool, send func(ctx context.Context, tx *DB) error) error {
	if upsert {
		return engine.RefuseBidiInsert(in, p, false)
	}
	pk := in.PrimaryKey[0]
	if !generated {
		id := engine.InfoKit(in).Value(p, pk)
		for i, v := range vs {
			if engine.SelfLink(id, v) {
				return engine.ErrSelfLink(in, keys[i])
			}
		}
	}
	return bidiTx(ctx, db, in, func(ctx context.Context, tx *DB) error {
		if err := send(ctx, tx); err != nil {
			return err
		}
		id := engine.InfoKit(in).Value(p, pk)
		if generated {
			if reflect.ValueOf(id).IsZero() {
				// The database generated the key, and did not return it.
				return fmt.Errorf("velox: %s: the insert did not return the row's key, so its %s cannot be linked", in.Name, keys[0].Edge)
			}
			for i, v := range vs {
				if engine.SelfLink(id, v) {
					return engine.ErrSelfLink(in, keys[i]) // the partner given was the key generated
				}
			}
		}
		for i, bk := range keys {
			if err := bidiLink(ctx, tx, in, bk, id, vs[i]); err != nil {
				return err
			}
		}
		return nil
	})
}
