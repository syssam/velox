package multitenant_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"

	mt "example.com/multitenant"
	"example.com/multitenant/velox/customer"
	"example.com/multitenant/velox/entity"
	"example.com/multitenant/velox/salesorder"

	_ "modernc.org/sqlite"
)

const (
	acme   = "acme"
	globex = "globex"
)

func open(t *testing.T) *mt.Clients {
	t.Helper()
	c, err := mt.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	require.NoError(t, c.System.Schema.Create(mt.WithSystem(context.Background(), acme)))
	return c
}

// seed creates one customer with one active order per tenant, through the
// scoped client, as a request handler would.
func seed(t *testing.T, c *mt.Clients, tenant string) (*entity.Customer, *entity.SalesOrder) {
	t.Helper()
	ctx := mt.WithTenant(context.Background(), tenant, "u1")

	cust, err := c.Scoped.Customer.Create().SetName(tenant + " customer").Save(ctx)
	require.NoError(t, err)
	order, err := c.Scoped.SalesOrder.Create().
		SetReference(tenant + "-001").
		SetCustomerID(cust.ID).
		Save(ctx)
	require.NoError(t, err)
	return cust, order
}

// The tenant column is stamped from the viewer, never supplied by the
// caller. Without this a create that omits it passes every policy rule —
// a policy whose rules all skip allows — and lands a row with an empty
// tenant that no tenant can read.
func TestCreate_StampsTenantFromViewer(t *testing.T) {
	c := open(t)
	cust, _ := seed(t, c, acme)
	assert.Equal(t, acme, cust.TenantID)
}

func TestCreate_WithoutViewerIsRefused(t *testing.T) {
	c := open(t)
	_, err := c.Scoped.Customer.Create().SetName("nobody").Save(context.Background())
	require.Error(t, err, "a create with no viewer must fail rather than land an untenanted row")
}

// Reads through the scoped client see one tenant; the same query through
// the system client sees everything.
func TestScopedReads_AreNarrowed_SystemReadsAreNot(t *testing.T) {
	c := open(t)
	seed(t, c, acme)
	seed(t, c, globex)

	acmeCtx := mt.WithTenant(context.Background(), acme, "u1")
	sysCtx := mt.WithSystem(context.Background(), acme)

	got, err := c.Scoped.Customer.Query().All(acmeCtx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, acme, got[0].TenantID)

	all, err := c.System.Customer.Query().All(sysCtx)
	require.NoError(t, err)
	assert.Len(t, all, 2, "the system client must not inherit the scope")
}

func TestScopedRead_WithoutViewerIsRefused(t *testing.T) {
	c := open(t)
	seed(t, c, acme)

	_, err := c.Scoped.Customer.Query().All(context.Background())
	require.Error(t, err, "a scoped read with no tenant must fail, not return every tenant's rows")
}

// This is the failure the whole two-client split exists to prevent.
//
// "Can I delete this customer?" is answered by asking whether any order
// still depends on it. Asked through the scoped client, the guard returns
// false whenever the blocking order belongs to another tenant — and the
// delete proceeds. That is data destruction, not disclosure: nothing
// detects it afterwards.
//
// The same guard on the system client answers correctly. Invariant checks
// belong there.
func TestInvariantGuard_MustRunOnTheSystemClient(t *testing.T) {
	c := open(t)
	otherCustomer, _ := seed(t, c, globex)

	acmeCtx := mt.WithTenant(context.Background(), acme, "u1")
	sysCtx := mt.WithSystem(context.Background(), acme)

	scopedSaysBlocked, err := c.Scoped.SalesOrder.Query().
		Where(salesorder.CustomerIDField.EQ(otherCustomer.ID), salesorder.ActiveField.EQ(true)).
		Exist(acmeCtx)
	require.NoError(t, err)

	systemSaysBlocked, err := c.System.SalesOrder.Query().
		Where(salesorder.CustomerIDField.EQ(otherCustomer.ID), salesorder.ActiveField.EQ(true)).
		Exist(sysCtx)
	require.NoError(t, err)

	assert.True(t, systemSaysBlocked,
		"the system client sees the blocking order — this is the correct answer")
	assert.False(t, scopedSaysBlocked,
		"DOCUMENTED HAZARD: the scoped client reports no dependents for an "+
			"out-of-tenant owner. A delete guarded by this check would destroy data. "+
			"Run invariant checks on Clients.System.")
}

// Writes are constrained by the Policy on TenantMixin, which is why a bulk
// UPDATE with no Where() of its own still cannot touch another tenant.
func TestBulkWrites_AreConstrainedToTheTenant(t *testing.T) {
	c := open(t)
	seed(t, c, acme)
	seed(t, c, globex)

	acmeCtx := mt.WithTenant(context.Background(), acme, "u1")
	sysCtx := mt.WithSystem(context.Background(), acme)

	affected, err := c.Scoped.SalesOrder.Update().SetActive(false).Save(acmeCtx)
	require.NoError(t, err)
	assert.Equal(t, 1, affected, "the tenant filter must reach the UPDATE WHERE clause")

	remaining, err := c.System.SalesOrder.Query().
		Where(salesorder.ActiveField.EQ(true)).
		All(sysCtx)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, globex, remaining[0].TenantID, "the other tenant's order is untouched")
}

// A write through the SYSTEM client is still governed by the Policy, because
// a Policy is process-global. The system role is what exempts it — and that
// role has to be constructed, so every cross-tenant write is greppable.
func TestSystemRole_IsTheOnlyWriteEscape(t *testing.T) {
	c := open(t)
	seed(t, c, acme)
	seed(t, c, globex)

	// Without the system role, even the System client is tenant-filtered.
	asUser := mt.WithTenant(context.Background(), acme, "u1")
	affected, err := c.System.SalesOrder.Update().SetActive(false).Save(asUser)
	require.NoError(t, err)
	assert.Equal(t, 1, affected, "the write Policy applies to every client")

	// With it, a reconciliation job can span tenants.
	affected, err = c.System.SalesOrder.Update().
		SetActive(true).
		Save(mt.WithSystem(context.Background(), acme))
	require.NoError(t, err)
	assert.Equal(t, 2, affected, "the system role lifts the tenant filter, explicitly")
}

// Edge predicates are not scoped by either mechanism: HasOrdersWith(...)
// compiles to a subquery built directly on a *sql.Selector, so it never
// becomes a Query and no rule observes it. Asserted here as a known gap so
// it cannot change silently.
func TestEdgePredicates_AreNotScoped(t *testing.T) {
	c := open(t)
	seed(t, c, acme)
	seed(t, c, globex)

	acmeCtx := mt.WithTenant(context.Background(), acme, "u1")

	// The outer Customer query IS scoped, so only one row can come back.
	got, err := c.Scoped.Customer.Query().
		Where(customer.HasOrdersWith(salesorder.ActiveField.EQ(true))).
		All(acmeCtx)
	require.NoError(t, err)
	assert.Len(t, got, 1,
		"the outer query is scoped; it is the subquery over orders that is not. "+
			"Treat edge predicates as unscoped in tenant-sensitive paths.")
}
