package engine

import (
	"context"
	"fmt"
	"slices"
	"unsafe"
)

// This file is what velox's InsertQuery and UpdateQuery do with their
// setters that does not depend on the entity's type, so that it is compiled
// once, not in every entity's instantiation.

// Setters are a write's setters: its column assignments and its edge
// setters, apart.
type Setters struct {
	Set   []Assignment
	Edges []*EdgeSet
}

// Add returns s with the setters ss, each in its list. s's lists are not
// changed.
func (s Setters) Add(ss []Assignment) Setters {
	s.Set, s.Edges = slices.Clip(s.Set), slices.Clip(s.Edges)
	for _, a := range ss {
		if e := EdgeOf(a); e != nil {
			s.Edges = append(s.Edges, e)
		} else {
			s.Set = append(s.Set, a)
		}
	}
	return s
}

// Empty reports whether s holds no setter.
func (s Setters) Empty() bool { return len(s.Set) == 0 && len(s.Edges) == 0 }

// CheckInsert refuses a column setter of an insert into in that is not a
// Set: Add and Sub need a row.
func (s Setters) CheckInsert(in *EntityInfo) error {
	for _, a := range s.Set {
		if a.Op != AssignSet {
			return fmt.Errorf("velox: %s: an insert's setter of %s is Add or Sub, which needs a row; use Set", in.Name, in.Columns[a.Column].Field)
		}
	}
	return nil
}

// PrepareInsert resolves s's edge setters for an insert of rows of in (an
// upsert when upsert, an InsertAll when many), checks them, and stores the
// column setters, Set's included, in each row. A plan that writes edges
// after the rows needs their keys: an upsert, or an InsertAll of a row
// whose key the database assigns, fails.
func PrepareInsert(in *EntityInfo, s Setters, rows []unsafe.Pointer, upsert, many bool) (EdgePlan, error) {
	var p EdgePlan
	set := s.Set
	if len(s.Edges) > 0 {
		var err error
		if p, err = PlanEdges(in, s.Edges); err != nil {
			return EdgePlan{}, err
		}
		if p.Writes() {
			gen := false
			if many && len(in.PrimaryKey) == 1 {
				for _, r := range rows {
					gen = gen || ZeroInt(fieldAddr(in, r, in.PrimaryKey[0]))
				}
			}
			if err := CheckEdgeInsert(in, upsert, many, gen); err != nil {
				return EdgePlan{}, err
			}
		}
		set = append(slices.Clip(set), p.Set...)
	}
	for _, r := range rows {
		for _, a := range set {
			SetElem(fieldAddr(in, r, a.Column), a.Value)
		}
	}
	return p, nil
}

// Insert runs an insert of rows of in with the setters s: PrepareInsert,
// then send, the insert's work inside the entity's hooks, which it runs
// through them. An insert with edge setters runs as InsertEdges says.
func Insert(ctx context.Context, db *DB, in *EntityInfo, s Setters, rows []unsafe.Pointer, upsert, many bool, send func(ctx context.Context, db *DB, mu *MutationState) error) error {
	p, err := PrepareInsert(in, s, rows, upsert, many)
	if err != nil {
		return err
	}
	run := func(ctx context.Context, db *DB) (int64, error) {
		mu := &MutationState{Op: OpInsert, Rows: rows}
		if !db.HasHooks(in) {
			err := send(ctx, db, mu)
			return mu.Affected, err
		}
		err := Mutate(ctx, db, in, mu, func(ctx context.Context, mu *MutationState) error { return send(ctx, db, mu) })
		return mu.Affected, err
	}
	if len(s.Edges) == 0 {
		_, err := run(ctx, db)
		return err
	}
	return InsertEdges(ctx, db, p, rows, run)
}

// Update runs st, an update, with the edge setters edges, as UpdateEdges
// says; with none, it is Write's.
func Update(ctx context.Context, db *DB, st *Statement, edges []*EdgeSet) (int64, error) {
	if len(edges) == 0 {
		n, _, _, err := Write(ctx, db, st, false, false)
		return n, err
	}
	p, err := PlanEdges(st.Entity, edges)
	if err != nil {
		return 0, err
	}
	st.Set = append(st.Set, p.Set...)
	return UpdateEdges(ctx, db, st, p)
}

// UpdateSQL is UpdateQuery.SQL's work for st with the edge setters edges:
// the statement and arguments, "" for an update of only edge setters.
func UpdateSQL(ctx context.Context, db *DB, st *Statement, edges []*EdgeSet) (string, []any, error) {
	if len(edges) > 0 {
		p, err := PlanEdges(st.Entity, edges)
		if err != nil {
			return "", nil, err
		}
		st.Set = append(st.Set, p.Set...)
		if len(st.Set) == 0 {
			return "", nil, db.guard(st)
		}
	}
	_, q, args, err := Write(ctx, db, st, true, false)
	return q, args, err
}
