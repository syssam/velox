package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// This file is the bidirectional one-to-one edge's (EdgeBidi): both partners'
// rows hold the key, so a write of one row's key writes the other's too, as
// ent's sqlgraph does for a Bidi O2O edge (clearFKEdges, addFKEdges).

// bidiKey is the key column of an EdgeBidi relation.
type bidiKey struct {
	Col  int    // the key column, an index into Columns
	Edge string // the relation's name, for errors
}

// bidiReason is the WithoutPolicy reason of the statement
// that clears the old partner's key: integrity maintenance, as ent's
// clearFKEdges is, which no row scope may leave half done.
const bidiReason = "velox: clearing the old partner of a bidirectional one-to-one edge"

// checkEdgeBidi refuses the EdgeBidi relation r of the entity name, of Go
// type t, unless it relates t to itself through a One field (rt), its id
// column is the one-column primary key, and its key column is a nullable,
// unencoded pointer field of the primary key's Go type, with no
// RequiredEdge, not the primary key, with a unique index
// of its own: that index is what refuses a
// partner that already has one before anything is cleared.
func checkEdgeBidi(name string, t reflect.Type, d *DescriptorFields, cols []ColumnInfo, r *EdgeDescriptor, rt reflect.Type) (bidiKey, error) {
	fail := func(format string, args ...any) (bidiKey, error) {
		return bidiKey{}, &SchemaError{Entity: name, Field: r.name, Reason: fmt.Sprintf(format, args...)}
	}
	if target := rtype(r.bidi); target != t {
		return fail("EdgeBidi relates an entity to itself, not to %v", target)
	}
	if rt != r.holders[0] {
		return fail("EdgeBidi's field is a %v; a one-to-one relation is a One", rt)
	}
	inRange := func(i int) bool { return i >= 0 && i < len(cols) }
	if !inRange(r.targetCol) || len(d.PrimaryKey) != 1 || d.PrimaryKey[0] != r.targetCol {
		col := fmt.Sprint(r.targetCol)
		if inRange(r.targetCol) {
			col = cols[r.targetCol].Name
		}
		return fail("EdgeBidi's id column %s is not the entity's one-column primary key", col)
	}
	if !inRange(r.ownerCol) {
		return fail("EdgeBidi's key names column %d, of %d", r.ownerCol, len(cols))
	}
	k, id := cols[r.ownerCol], cols[r.targetCol]
	switch {
	case r.ownerCol == r.targetCol:
		return fail("EdgeBidi's key column %s is a primary-key column", k.Name)
	case rtype(k.Type).Kind() != reflect.Pointer:
		// A Nullable slice or map would read a NULL key back as an empty
		// value, which a write would take for a partner.
		return fail("EdgeBidi's key column %s is a %v; make its field a pointer, which a NULL key leaves nil", k.Name, rtype(k.Type))
	case k.RequiredEdge != "":
		// velox-gen refuses it (rule 53): step 2 of bidiUpdate clears the
		// key past the RequiredEdge check.
		return fail("EdgeBidi's key column %s has a RequiredEdge, and velox clears a partner's key whenever its partner re-points", k.Name)
	case k.Codec:
		return fail("EdgeBidi's key column %s has a codec (Encode and Decode): an encoded column cannot be compared", k.Name)
	case base(k.Type) != rtype(id.Type):
		return fail("EdgeBidi's key column %s holds %v, and the primary key %s %v", k.Name, base(k.Type), id.Name, rtype(id.Type))
	case !linkKey(rtype(id.Type)):
		return fail("EdgeBidi's key column %s holds %v, which cannot be compared; use a comparable type or a byte slice", k.Name, rtype(id.Type))
	}
	for _, ix := range d.Indexes {
		if ix.Unique && len(ix.Columns) == 1 && ix.Columns[0] == r.ownerCol {
			return bidiKey{Col: r.ownerCol, Edge: r.name}, nil
		}
	}
	return fail("EdgeBidi's key column %s has no unique index of its own, which refuses a partner that already has one", k.Name)
}

// bidiSet returns the bidirectional keys set assigns and their values; a nil
// value clears the key.
func bidiSet(in *EntityInfo, set []Assignment) ([]bidiKey, []any) {
	var keys []bidiKey
	var vs []any
	for _, bk := range in.bidiKeys {
		for _, a := range set {
			if a.Column == bk.Col {
				v, _ := keyOf(a.Value) // a nil pointer is nil
				keys, vs = append(keys, bk), append(vs, v)
				break
			}
		}
	}
	return keys, vs
}

// pinnedKey returns v when where holds, at its top level, the condition
// <primary key> = v that Save writes and Where(pk.EQ(v)) adds.
func pinnedKey(in *EntityInfo, where []Predicate) (any, bool) {
	pk := int32(in.PrimaryKey[0]) // one column: checkEdgeBidi
	var pinned func(n Node) (any, bool)
	pinned = func(n Node) (any, bool) {
		switch n.Op {
		case OpEQ:
			if n.Col == pk {
				if v, ok := keyOf(n.V); ok && v != nil {
					return v, true
				}
			}
		case OpAnd:
			for _, k := range n.Kids {
				if v, ok := pinned(k); ok {
					return v, true
				}
			}
		}
		return nil, false
	}
	for _, p := range where {
		if v, ok := pinned(p.n); ok {
			return v, true
		}
	}
	return nil, false
}

// checkBidiUpdate refuses st, an update that assigns the bidirectional keys
// keys the values vs (bidiSet), unless it sets them with Set on the one row
// that its Where names by primary key, and none to that row itself. It
// returns that row's key.
func checkBidiUpdate(st *Statement, keys []bidiKey, vs []any) (any, error) {
	in := st.Entity
	for _, a := range st.Set {
		for _, bk := range keys {
			if a.Column == bk.Col && a.Op != AssignSet {
				return nil, fmt.Errorf("velox: %s: %s, the key of the bidirectional edge %s, can only be set", in.Name, in.Columns[bk.Col].Field, bk.Edge)
			}
		}
	}
	k, ok := pinnedKey(in, st.Where)
	if !ok || st.AllRows {
		pk := in.Columns[in.PrimaryKey[0]].Field
		col := in.Columns[keys[0].Col].Field
		return nil, fmt.Errorf("velox: %s: an update that sets %s, the key of the bidirectional edge %s, must name one row by its primary key (Where on %s.EQ), as velox re-points the other row too", in.Name, col, keys[0].Edge, pk)
	}
	for i, v := range vs {
		if v != nil && selfLink(k, v) {
			return nil, errSelfLink(in, keys[i])
		}
	}
	return k, nil
}

// errSelfLink is the error of a write that makes a row its own partner: a
// *ValidationError on the key's field, as the value of one field is refused.
func errSelfLink(in *EntityInfo, bk bidiKey) error {
	return &ValidationError{Entity: in.Name, Field: in.Columns[bk.Col].Field, Err: fmt.Errorf("a row cannot be its own %s", bk.Edge)}
}

// errLinkCount is the error of a bidirectional write whose driver did not
// report the rows a statement wrote (-1): whether the row was written, or
// the partner linked, cannot be told, so the write fails closed.
func errLinkCount(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: the driver did not report the rows the link wrote", in.Name)
}

// bidiStep marks a statement that a bidirectional write sends, for the
// checks a Rewrite hook's changes get (checkBidiRewrite).
type bidiStep struct {
	Keys   []bidiKey // the bidirectional keys the statement sets
	Vals   []any     // their values, as KeyOf gives them, nil for a clear: what the write's later statements link
	Pinned any       // step 1 of bidiUpdate: the key of the row its Where names
	Insert bool      // bidiInsert's INSERT
}

// checkBidiRewrite refuses what a Rewrite hook made of st, a statement of a
// bidirectional write that b marks, that the write's later statements could
// not follow: step 1 of an update re-targeted to another row, whose old
// partner and new one steps 2 and 3 would then not re-point; an INSERT made
// an upsert, whose row the link could not name; and a key's value changed or
// dropped, in step 1's Set or the INSERT's row, which would leave the pair
// one-way, as the later statements link the value set before the hooks.
// prepare calls it, before the statement is sent, only when the DB has
// Rewrite hooks, with b as it was before them: a hook that replaces the whole
// statement cannot drop it.
func checkBidiRewrite(st *Statement, b *bidiStep) error {
	if b == nil {
		return nil
	}
	in := st.Entity
	if b.Insert {
		if st.Conflict != nil {
			return fmt.Errorf("velox: %s: an upsert cannot set the key of the bidirectional edge %s; insert the row, then Save the key", in.Name, b.Keys[0].Edge)
		}
		for i, bk := range b.Keys {
			if v, ok := insertedKey(st, bk.Col); !ok || !equalKey(v, b.Vals[i]) {
				return errBidiValue(in, bk)
			}
		}
		return nil
	}
	if k, ok := pinnedKey(in, st.Where); !ok || !equalKey(k, b.Pinned) {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the row that the update of the bidirectional key %s names", in.Name, in.Columns[b.Keys[0].Col].Field)
	}
	for i, bk := range b.Keys {
		if v, ok := assignedKey(st.Set, bk.Col); !ok || !equalKey(v, b.Vals[i]) {
			return errBidiValue(in, bk)
		}
	}
	return nil
}

// errBidiValue is checkBidiRewrite's error for a key a Rewrite hook changed.
func errBidiValue(in *EntityInfo, bk bidiKey) error {
	return fmt.Errorf("velox: %s: a Rewrite hook changed the value of %s, the key of the bidirectional edge %s", in.Name, in.Columns[bk.Col].Field, bk.Edge)
}

// assignedKey returns the value set gives column col, as keyOf gives it,
// when set assigns col exactly once, with AssignSet.
func assignedKey(set []Assignment, col int) (any, bool) {
	var v any
	n := 0
	for _, a := range set {
		if a.Column == col {
			if a.Op != AssignSet {
				return nil, false
			}
			v, _ = keyOf(a.Value)
			n++
		}
	}
	return v, n == 1
}

// insertedKey returns the value st, an insert, gives column col in its one
// row, as keyOf gives it, when it lists col exactly once.
func insertedKey(st *Statement, col int) (any, bool) {
	if len(st.Rows) != 1 {
		return nil, false
	}
	at := -1
	for i, c := range st.Columns {
		if c == col {
			if at >= 0 {
				return nil, false
			}
			at = i
		}
	}
	if at < 0 || at >= len(st.Rows[0]) {
		return nil, false
	}
	v, _ := keyOf(st.Rows[0][at])
	return v, true
}

// equalKey reports whether the key values a and b, as keyOf gives them, are
// equal: both nil, or of one type and equal by selfLink.
func equalKey(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.TypeOf(a) == reflect.TypeOf(b) && selfLink(a, b)
}

// bidiRow returns the bidirectional keys the row of in at p sets, to a
// non-nil value, and those values.
func bidiRow(in *EntityInfo, p unsafe.Pointer) ([]bidiKey, []any) {
	var keys []bidiKey
	var vs []any
	for _, bk := range in.bidiKeys {
		if v, ok := keyOf(in.kit.Value(p, bk.Col)); ok && v != nil {
			keys, vs = append(keys, bk), append(vs, v)
		}
	}
	return keys, vs
}

// refuseBidiInsert refuses an upsert (many false) or an InsertAll (many true)
// of a row of in at p that sets a bidirectional key. Both are stricter than
// ent, which links through them: an upsert's DO NOTHING or DO UPDATE leaves
// unknown whether the row that holds the key is the caller's, so the partner
// to link is ambiguous, and InsertAll reads no generated key back, so velox
// cannot name the rows to link.
func refuseBidiInsert(in *EntityInfo, p unsafe.Pointer, many bool) error {
	keys, _ := bidiRow(in, p)
	switch {
	case len(keys) == 0:
		return nil
	case many:
		return fmt.Errorf("velox: %s: InsertAll cannot set the key of the bidirectional edge %s; insert the rows, then Save each key", in.Name, keys[0].Edge)
	}
	return fmt.Errorf("velox: %s: an upsert cannot set the key of the bidirectional edge %s; insert the row, then Save the key", in.Name, keys[0].Edge)
}

// refuseBidiUpsert refuses an upsert whose update branch writes a bidirectional
// key: the database writes one row's, and the old partner would keep pointing
// at it, whatever the inserted row holds.
func refuseBidiUpsert(in *EntityInfo, cf *ConflictClause) error {
	for _, bk := range in.bidiKeys {
		if slices.Contains(cf.Update, bk.Col) {
			return fmt.Errorf("velox: %s: an upsert cannot update the key of the bidirectional edge %s; Save the key, which re-points both rows", in.Name, bk.Edge)
		}
	}
	return nil
}

// RefuseBidiRows refuses an InsertAll of the n rows of in, size bytes apart
// from base, when one sets a bidirectional key (refuseBidiInsert).
func RefuseBidiRows(in *EntityInfo, base unsafe.Pointer, size uintptr, n int) error {
	if len(in.bidiKeys) == 0 {
		return nil
	}
	for i := range n {
		if err := refuseBidiInsert(in, unsafe.Add(base, uintptr(i)*size), true); err != nil {
			return err
		}
	}
	return nil
}

// BidiInTx sets InTx on st, the insert of the row at p, when Exec sends it in
// bidiInsert's transaction: when the row sets a bidirectional key and st is
// no upsert, which refuseBidiInsert refuses.
func BidiInTx(st *Statement, p unsafe.Pointer) {
	if len(st.Entity.bidiKeys) > 0 && st.Conflict == nil {
		if keys, _ := bidiRow(st.Entity, p); len(keys) > 0 {
			st.InTx = true
		}
	}
}

// bidiTx runs fn under db.Tx for a bidirectional write of in, after the
// refusals Tx would make as the transaction's, made here as in's, so that the
// error names the entity as the same write without the key does: a dry run,
// a statement under another transaction's mark, a transaction a savepoint
// broke, and, outside a transaction, a context that names no shard.
func bidiTx(ctx context.Context, db *DB, in *EntityInfo, fn func(ctx context.Context, tx *DB) error) error {
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", in.Name, ErrDryRun)
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", in.Name, ErrOutsideTx)
	}
	if db.txRoot != nil && db.txRoot.broken != nil {
		return fmt.Errorf("velox: %s: %w", in.Name, db.txRoot.broken)
	}
	if !db.InTx {
		if _, err := db.ctxShard(ctx); err != nil {
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
//     not change the row it names (checkBidiRewrite). When it writes no row
//     nothing else is sent; when it writes more than one, the write fails.
//  2. For each key, UPDATE … SET key = NULL WHERE key = k, scope-free
//     (WithoutPolicy): it clears the old partner, which the
//     caller may not see, so that no row is left pointing at k.
//  3. For each key set to a partner v, bidiLink.
//
// The statements go through the pipeline, so Rewrite and Around hooks see
// each. mu.affected is step 1's count, the caller's rows.
func bidiUpdate(ctx context.Context, db *DB, mu *MutationState, keys []bidiKey, k any, vs []any) error {
	st := mu.St
	in := st.Entity
	var n int64
	err := bidiTx(ctx, db, in, func(ctx context.Context, tx *DB) error {
		st.InTx = true
		setStmtBidi(st, &bidiStep{Keys: keys, Vals: vs, Pinned: k})
		res, err := ExecWrite(ctx, tx, st)
		setStmtBidi(st, nil)
		if err != nil {
			return err
		}
		switch n = res.Affected; {
		case n == 0:
			return nil
		case n < 0:
			return errLinkCount(in)
		case n > 1:
			return fmt.Errorf("velox: %s: the update that sets the bidirectional key %s wrote %d rows", in.Name, in.Columns[keys[0].Col].Field, n)
		}
		free := WithoutPolicy(ctx, bidiReason)
		for _, bk := range keys {
			unlink := Statement{Op: OpUpdate, Entity: in, InTx: true,
				Set: []Assignment{{Column: bk.Col, Op: AssignSet}}, Where: []Predicate{ColumnEQ(bk.Col, k)}}
			// Any number of rows, none included, is right: the count is not read.
			if _, err := ExecWrite(free, tx, &unlink); err != nil {
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
func bidiLink(ctx context.Context, db *DB, in *EntityInfo, bk bidiKey, k, v any) error {
	pk := in.PrimaryKey[0]
	st := Statement{Op: OpUpdate, Entity: in, InTx: db.InTx,
		Set:   []Assignment{{Column: bk.Col, Op: AssignSet, Value: k}},
		Where: []Predicate{ColumnEQ(pk, v), NewPredicate(Node{Op: OpIsNull, Col: int32(bk.Col)})}}
	res, err := ExecWrite(ctx, db, &st)
	switch {
	case err != nil:
		return err
	case res.Affected < 0:
		return errLinkCount(in)
	case res.Affected > 0:
		return nil
	}
	probe := Statement{Op: OpSelect, Entity: in, InTx: db.InTx, Exists: true, Limit: 1, Writable: true,
		Where: []Predicate{ColumnEQ(pk, v)}}
	var found bool
	read := func(r Rows) error {
		found = r.Next()
		return nil
	}
	if db.Hooked() {
		err = db.Run(ctx, &probe, read, nil)
	} else {
		err = db.Plain(ctx, &probe, read, nil)
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
// the partners vs (bidiRow), under db.Tx: send sends its INSERT, then
// bidiLink sets each partner's key to the row's key, as the INSERT returned
// it when the database generated it (generated). A row given its own key as
// a partner is refused before anything is sent, and so is an upsert, which
// a hook may have made of the insert after check.
func bidiInsert(ctx context.Context, db *DB, in *EntityInfo, p unsafe.Pointer, keys []bidiKey, vs []any, generated, upsert bool, send func(ctx context.Context, tx *DB) error) error {
	if upsert {
		return refuseBidiInsert(in, p, false)
	}
	pk := in.PrimaryKey[0]
	if !generated {
		id := infoKit(in).Value(p, pk)
		for i, v := range vs {
			if selfLink(id, v) {
				return errSelfLink(in, keys[i])
			}
		}
	}
	return bidiTx(ctx, db, in, func(ctx context.Context, tx *DB) error {
		if err := send(ctx, tx); err != nil {
			return err
		}
		id := infoKit(in).Value(p, pk)
		if generated {
			if reflect.ValueOf(id).IsZero() {
				// The database generated the key, and did not return it.
				return fmt.Errorf("velox: %s: the insert did not return the row's key, so its %s cannot be linked", in.Name, keys[0].Edge)
			}
			for i, v := range vs {
				if selfLink(id, v) {
					return errSelfLink(in, keys[i]) // the partner given was the key generated
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
