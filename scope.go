package velox

import (
	"context"
	"fmt"
	"slices"

	"github.com/syssam/velox2/internal/engine"
)

// scope applies the context's tenant and the entities' policies to a statement.
// On a tenant-scoped entity it adds the condition tenant = id, or fails the
// statement with ErrNoTenant when the context has neither WithTenant nor
// WithAllTenants. Unless the context says WithoutPolicy, a SELECT gets the
// entity's Read policy and an UPDATE or DELETE, or a Writable SELECT, its Read
// and Write policies, after the tenant. Every EXISTS gets its target's tenant
// and Read policy the same way. scope runs after the guard and the Rewrite
// hooks, so neither sees what it adds and no hook can remove it.
//
// Raw SQL (OpRawQuery and OpRawExec) is never scoped: prepare does not call
// scope for it, as its SQL is the caller's, and its Statement.TenantBypass
// holds the reason.
//
// st does not escape: scope passes it to no function value, and policy
// functions are given only ctx, so plain's statement stays on the stack.
func (db *DB) scope(ctx context.Context, st *Statement) error {
	switch st.Op {
	case OpSelect, OpUpdate, OpDelete:
	case OpInsert:
		return db.scopeInsert(ctx, st)
	default:
		return nil
	}
	in := st.Entity
	sc := engine.Scoper{Ctx: ctx, Entity: in.Name}
	id, tenant, err := sc.TenantOf(in)
	if err != nil {
		return err
	}
	if tenant {
		if err := engine.CheckTenantAssignments(in, st.Set, id); err != nil {
			return err
		}
	}
	// The entity's own policy first, so that its denial is reported as the
	// statement's, and an EXISTS on the same entity reuses its Read.
	var read, write Predicate
	if pol := engine.InfoPolicy(in); pol != nil {
		if pol.Read != nil && sc.PolicyOn() {
			if read, err = sc.ReadOf(in, st.Op); err != nil {
				return err
			}
		}
		if (st.Op != OpSelect || st.Writable) && pol.Write != nil && sc.PolicyOn() {
			p, err := pol.Write(ctx)
			if write, err = sc.PolicyCond(in, st.Op, "Write", p, err); err != nil {
				return err
			}
		}
	}
	extra := 0
	if tenant {
		extra++
	}
	if engine.PredNode(read).Op != engine.OpZero {
		extra++
	}
	if engine.PredNode(write).Op != engine.OpZero {
		extra++
	}
	if st.Where, err = sc.Walk(st.Where, extra); err != nil {
		return err
	}
	// st.Where is now the scope's own array, or nothing is appended to it.
	if tenant {
		st.Where = append(st.Where, engine.TenantPredicate(in, id))
	}
	if engine.PredNode(read).Op != engine.OpZero {
		st.Where = append(st.Where, read)
	}
	if engine.PredNode(write).Op != engine.OpZero {
		st.Where = append(st.Where, write)
	}
	sc.Record(st)
	return nil
}

// scopeInsert applies the context's tenant and the entity's policy to an
// INSERT. PrepareRow has filled and checked each row's tenant, and
// InsertAllowed has asked the Insert policy; scopeInsert checks that the
// Rewrite hooks left every row in the tenant. An upsert that updates must
// reach neither another tenant's row nor one the policy hides from an update.
// On PostgreSQL and SQLite its conflict columns must hold the tenant column,
// and its update branch gets the Read and Write conditions; an EXISTS a
// Rewrite hook put in that branch's condition gets its target's tenant and
// Read, as one in a Where does. MySQL's ON DUPLICATE KEY UPDATE resolves a
// conflict on any unique key and takes no condition: every unique key must
// hold the tenant column, and an entity with a Read or Write policy is
// refused. Keeping the existing row (DoNothing) changes nothing, so it is
// exempt.
func (db *DB) scopeInsert(ctx context.Context, st *Statement) error {
	in := st.Entity
	sc := engine.Scoper{Ctx: ctx, Entity: in.Name}
	id, tenant, err := sc.TenantOf(in)
	if err != nil {
		return err
	}
	if tenant && len(db.cfg.Rewrites) > 0 {
		if err := engine.CheckTenantRows(in, st, id); err != nil {
			return err
		}
	}
	pol := engine.InfoPolicy(in)
	if pol != nil && pol.Insert != nil {
		sc.PolicyOn() // records a WithoutPolicy that InsertAllowed obeyed
	}
	if cf := st.Conflict; cf != nil && !cf.DoNothing && len(cf.Update) > 0 {
		if tenant {
			if err := db.checkTenantUpsert(in, cf); err != nil {
				return err
			}
		}
		conditioned := pol != nil && (pol.Read != nil || pol.Write != nil) && sc.PolicyOn()
		if conditioned && db.d == MySQL {
			return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE takes no condition, and the entity has a Read or Write policy; keep the existing row with DoNothing, or update it with Update or Save", in.Name, ErrPolicyUpsert)
		}
		if err := sc.ConditionUpsert(in, st, conditioned); err != nil {
			return err
		}
		if engine.StmtUpsertPolicy(st) && len(st.Rows) > 1 && !st.InTx {
			// conditionedUpsert runs every such InsertAll in a transaction:
			// only a Rewrite hook gets here.
			return fmt.Errorf("velox: %s: a Rewrite hook gave a multi-row upsert outside a transaction an update branch the policy conditions; a row the policy hides would fail it after the others were written", in.Name)
		}
	}
	sc.Record(st)
	return nil
}

// checkTenantUpsert refuses an upsert that updates and whose conflict could
// be with another tenant's row. On PostgreSQL and SQLite the conflict columns
// must hold the tenant column. MySQL resolves a conflict on any unique key,
// so the primary key and every unique index must hold it.
func (db *DB) checkTenantUpsert(in *EntityInfo, cf *ConflictClause) error {
	col := in.Columns[in.Tenant].Name
	if db.d != MySQL {
		if slices.Contains(cf.Columns, in.Tenant) {
			return nil
		}
		return fmt.Errorf("velox: %s: %w: the conflict columns (%s) do not hold the tenant column %s; name the tenant column in OnConflict", in.Name, ErrTenantUpsert, engine.ColumnNames(in, cf.Columns), col)
	}
	what, cols := "the primary key", in.PrimaryKey
	if slices.Contains(cols, in.Tenant) {
		what, cols = "", nil
		for _, ix := range in.Indexes {
			if ix.Unique && !slices.Contains(ix.Columns, in.Tenant) {
				what, cols = "the unique index", ix.Columns
				break
			}
		}
	}
	if what == "" {
		return nil
	}
	return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE resolves a conflict on any unique key, and %s (%s) does not hold the tenant column %s", in.Name, ErrTenantUpsert, what, engine.ColumnNames(in, cols), col)
}
