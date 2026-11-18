package engine

import (
	"fmt"
	"slices"
	"unsafe"
)

// cloneForHooks gives st its own copies of the slices and clauses a hook may
// edit in place, so that the query st came from stays as it was.
func cloneForHooks(st *Statement) {
	st.Where = slices.Clone(st.Where)
	st.OrderBy = slices.Clone(st.OrderBy)
	st.Columns = slices.Clone(st.Columns)
	st.Set = slices.Clone(st.Set)
	st.Returning = slices.Clone(st.Returning)
	st.Joins = slices.Clone(st.Joins)
	st.Project = cloneProject(st.Project)
	st.Tags = slices.Clone(st.Tags)
	if st.Rows != nil {
		rows := make([][]any, len(st.Rows))
		for i, r := range st.Rows {
			rows[i] = slices.Clone(r)
		}
		st.Rows = rows
	}
	if st.Conflict != nil {
		cf := *st.Conflict
		cf.Columns, cf.Update, cf.Where = slices.Clone(cf.Columns), slices.Clone(cf.Update), slices.Clone(cf.Where)
		st.Conflict = &cf
	}
	if st.Each != nil {
		ec := *st.Each
		ec.Keys = slices.Clone(ec.Keys)
		st.Each = &ec
	}
}

// rewriteSnapshot is what a statement was before the Rewrite hooks ran: what
// they may not change, and velox's own marks, which they cannot set.
type rewriteSnapshot struct {
	entity         *EntityInfo
	op             Op
	writable, inTx bool
	count, exists  bool
	group          int
	agg            Aggregate
	lock           Lock
	wait           LockWait
	cols           []int
	joins          []JoinClause
	project        *Projection // velox's, before cloneForHooks
	each           *EachClause // the hooks' Each: a copy of velox's when there are hooks
	was            *EachClause // velox's Each, before cloneForHooks
	returning      bool
	bidi           *bidiStep
	pin            Target
	iter, loaded   bool
}

// takeRewriteSnapshot records st before cloneForHooks and the Rewrite hooks.
// The caller sets each after cloneForHooks.
func takeRewriteSnapshot(st *Statement) rewriteSnapshot {
	return rewriteSnapshot{
		entity: st.Entity, op: st.Op, writable: st.Writable, inTx: st.InTx,
		count: st.Count, exists: st.Exists, group: st.GroupBy, agg: st.Aggregate,
		lock: st.Lock, wait: st.LockWait, cols: st.Columns, joins: st.Joins, project: st.Project,
		was: st.Each, returning: len(st.Returning) > 0,
		bidi: stmtBidi(st), pin: stmtPin(st), iter: st.iter, loaded: st.loaded,
	}
}

// restoreAndCheck puts back velox's own marks, which a hook that replaces the
// whole struct (*st = Statement{…}) zeroes, and refuses a statement whose
// hooks changed what the guard, the scope or the scan already relies on.
func (s *rewriteSnapshot) restoreAndCheck(st *Statement) error {
	setStmtBidi(st, s.bidi)
	setStmtPin(st, s.pin)
	st.iter, st.loaded = s.iter, s.loaded
	name := s.entity.Name
	changed := func(what string) error {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's %s", name, what)
	}
	if st.Op != s.op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", name)
	}
	if st.Writable != s.writable {
		// The scope reads it: without it, a Writable query drops the
		// Write policy.
		return changed("Writable")
	}
	if st.InTx != s.inTx {
		// scopeInsert reads it: a hook that claimed a transaction would send
		// a policy-conditioned InsertAll without one.
		return changed("InTx")
	}
	if st.Count != s.count {
		// The scan depends on it, and it renders before Exists and an
		// aggregate: a flip would read a count as a column, or the reverse.
		return changed("Count")
	}
	if st.Exists != s.exists {
		// The scan depends on it: Exists reads no column.
		return changed("Exists")
	}
	if st.Aggregate != s.agg {
		return errAggregateChanged(name)
	}
	if st.GroupBy != s.group {
		// The scan depends on it: a key, then a value.
		return changed("GroupBy")
	}
	if (s.count || s.exists || s.agg != 0) && !slices.Equal(st.Columns, s.cols) {
		// The scan depends on them: one value, of the column's type.
		return changed("Columns")
	}
	if st.Lock != s.lock || st.LockWait != s.wait {
		// The guard checked them.
		return changed("lock")
	}
	if st.Each != s.each || (s.each != nil && !sameEach(s.each, s.was)) {
		// The load reads the rows by it: each owner's, in its order.
		return changed("Each")
	}
	if !sameProject(st.Project, s.project) {
		// The scan reads the terms by position.
		return changed("Project")
	}
	if !sameJoins(st.Joins, s.joins) {
		// The scan reads the joined columns after the entity's own, by them.
		return changed("Joins")
	}
	return s.checkEntity(st)
}

// checkEntity refuses an Entity a hook replaced with anything but a copy of
// it: the scan plan, the policy and the interceptors are read from it.
func (s *rewriteSnapshot) checkEntity(st *Statement) error {
	entity := s.entity
	switch {
	case st.Entity == nil:
		return fmt.Errorf("velox: %s: a Rewrite hook set the statement's Entity to nil", entity.Name)
	case st.Entity != entity && (infoKit(st.Entity) != infoKit(entity) || len(st.Entity.Columns) != len(entity.Columns)):
		// A copy (info := *st.Entity) keeps the scan plan; anything else
		// would scan rows into the wrong fields.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with %s, which is not a copy of it", entity.Name, st.Entity.Name)
	case InfoPolicy(st.Entity) != InfoPolicy(entity):
		// The scope reads the policy from the statement's Entity: a copy without
		// it would unscope the statement.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with a copy whose policy differs", entity.Name)
	case len(st.Entity.filters) != len(entity.filters) || unsafe.SliceData(st.Entity.filters) != unsafe.SliceData(entity.filters):
		// The scope reads the interceptors from the statement's Entity too.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with a copy whose interceptors differ", entity.Name)
	}
	return nil
}

// checkRewritten checks what a Rewrite hook may have added: conditions by
// name, columns, a bidi relation's write, and an upsert's condition.
func (db *DB) checkRewritten(st *Statement, s rewriteSnapshot) error {
	if err := resolveStatementNames(st); err != nil {
		return err
	}
	if err := CheckColumns(st); err != nil {
		return err
	}
	if err := checkBidiRewrite(st, s.bidi); err != nil {
		return err
	}
	cf := st.Conflict
	if cf == nil {
		return nil
	}
	if err := refuseBidiUpsert(st.Entity, cf); err != nil {
		return err
	}
	if len(cf.Where) > 0 {
		switch {
		case db.D == MySQL:
			return fmt.Errorf("velox: %s: a Rewrite hook gave the upsert a condition; MySQL's ON DUPLICATE KEY UPDATE takes none", s.entity.Name)
		case cf.DoNothing || len(cf.Update) == 0:
			// Rendering would drop it: there is no update branch.
			return fmt.Errorf("velox: %s: a Rewrite hook gave the upsert a condition, but the upsert keeps the existing row and has no update branch to apply it to", s.entity.Name)
		}
	}
	return nil
}
